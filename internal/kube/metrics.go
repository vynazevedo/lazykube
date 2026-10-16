package kube

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"math"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vynazevedo/lazykube/internal/data"
)

const (
	metricsEvery   = 5 * time.Second
	requestTimeout = 10 * time.Second
	historySize    = 120
	cpuWindow      = 60
	sparkWindow    = 34
	heatRows       = 8
	netBackoffBase = 30 * time.Second
	netBackoffMax  = 5 * time.Minute
	netStaleAfter  = 3 * metricsEvery
)

const (
	reasonQuerying     = "consultando metrics-server"
	reasonNoClient     = "metrics.k8s.io indisponível"
	reasonNotInstalled = "metrics-server não instalado"
	reasonDown         = "metrics-server fora do ar"
	reasonMetricsDeny  = "sem permissão: pods.metrics.k8s.io"
	reasonNodesDeny    = "sem permissão: nodes"
	reasonNoNode       = "nenhum pod com node no escopo"
	reasonNodeUnseen   = "node não observado"
	reasonProxyDeny    = "sem permissão: nodes/proxy"
	reasonNoStats      = "kubelet sem stats/summary"
	reasonKubeletDown  = "kubelet indisponível"
	reasonCollecting   = "coletando amostras"
	reasonNoNetwork    = "sem dados de rede"
	reasonNetStale     = "dados de rede desatualizados"
)

type podSeries struct {
	uid              string
	cpu, mem, rx, tx []float64
	sampledAt        time.Time
	prevRX, prevTX   uint64
	prevAt           time.Time
	netAt            time.Time
}

type netState struct {
	reason   string
	denied   bool
	failures int
	retryAt  time.Time
	okAt     time.Time
}

type usage struct {
	ok        bool
	reason    string
	pods      map[string]*podSeries
	nodeCPU   map[string]int64
	nodeMem   map[string]int64
	net       map[string]*netState
	version   int
	heatGen   int
	eligible  uint64
	heatKey   [3]int
	heatNames []string
	heatCells [][]float64
}

func newUsage() usage {
	return usage{
		pods:    map[string]*podSeries{},
		nodeCPU: map[string]int64{},
		nodeMem: map[string]int64{},
		net:     map[string]*netState{},
	}
}

func push(values []float64, v float64) []float64 {
	values = append(values, v)
	if len(values) > historySize {
		values = append(values[:0:0], values[len(values)-historySize:]...)
	}
	return values
}

func tailCopy(values []float64, n int) []float64 {
	if len(values) > n {
		values = values[len(values)-n:]
	}
	return append([]float64(nil), values...)
}

func last(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	return values[len(values)-1]
}

func maxOf(values []float64) float64 {
	top := 0.0
	for _, v := range values {
		top = math.Max(top, v)
	}
	return top
}

func (u *usage) series(key, uid string) *podSeries {
	ps := u.pods[key]
	if ps == nil || ps.uid != uid {
		if ps != nil {
			u.version++
		}
		ps = &podSeries{uid: uid}
		u.pods[key] = ps
	}
	return ps
}

func (u *usage) live(key string, p *corev1.Pod) *podSeries {
	ps := u.pods[key]
	if ps == nil || p == nil || ps.uid != string(p.UID) {
		return nil
	}
	return ps
}

func (u *usage) netState(node string) *netState {
	st := u.net[node]
	if st == nil {
		st = &netState{}
		u.net[node] = st
	}
	return st
}

func heatEligibility(pods []*corev1.Pod) uint64 {
	h := fnv.New64a()
	for _, p := range pods {
		_, _ = h.Write([]byte(p.Namespace))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(p.Name))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(p.UID))
		if podStatus(p) == data.StatusRunning {
			_, _ = h.Write([]byte{1})
		} else {
			_, _ = h.Write([]byte{2})
		}
	}
	return h.Sum64()
}

func (u *usage) noteEligibility(pods []*corev1.Pod) {
	if fp := heatEligibility(pods); fp != u.eligible {
		u.eligible = fp
		u.heatGen++
	}
}

func netBackoff(base time.Duration, failures int) time.Duration {
	d := base
	for i := 1; i < failures && d < netBackoffMax; i++ {
		d *= 2
	}
	return min(d, netBackoffMax)
}

func (s *Source) pollLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(metricsEvery)
	defer ticker.Stop()
	s.poll()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.poll()
		case <-s.pollNow:
			s.pollNetwork(s.ctx)
			s.notify()
		}
	}
}

func (s *Source) poll() {
	ctx, cancel := context.WithTimeout(s.ctx, requestTimeout)
	defer cancel()
	s.pollMetrics(ctx)
	s.pollNetwork(ctx)
	s.notify()
}

func (s *Source) pollMetrics(ctx context.Context) {
	if s.metrics == nil {
		s.mu.Lock()
		s.usage.ok, s.usage.reason = false, reasonNoClient
		s.mu.Unlock()
		return
	}
	pms, err := s.metrics.MetricsV1beta1().PodMetricses(s.opts.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		s.mu.Lock()
		s.usage.ok, s.usage.reason = false, metricsReason(err)
		s.mu.Unlock()
		return
	}
	type sample struct {
		cpu, mem float64
		at       time.Time
	}
	samples := make(map[string]sample, len(pms.Items))
	for _, pm := range pms.Items {
		var c, m int64
		for _, ct := range pm.Containers {
			c += ct.Usage.Cpu().MilliValue()
			m += ct.Usage.Memory().Value()
		}
		samples[podKey(pm.Namespace, pm.Name)] = sample{cpu: float64(c), mem: float64(m >> 20), at: pm.Timestamp.Time}
	}
	nodeCPU := map[string]int64{}
	nodeMem := map[string]int64{}
	if s.caps.nodes {
		if nms, err := s.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{}); err == nil {
			for _, nm := range nms.Items {
				nodeCPU[nm.Name] = nm.Usage.Cpu().MilliValue()
				nodeMem[nm.Name] = nm.Usage.Memory().Value()
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	u := &s.usage
	u.ok, u.reason = true, ""
	changed := false
	for key, sm := range samples {
		p := s.byKey[key]
		if p == nil || (!sm.at.IsZero() && sm.at.Before(p.CreationTimestamp.Time)) {
			continue
		}
		ps := u.series(key, string(p.UID))
		if !sm.at.IsZero() && !sm.at.After(ps.sampledAt) {
			continue
		}
		ps.sampledAt = sm.at
		ps.cpu = push(ps.cpu, sm.cpu)
		ps.mem = push(ps.mem, sm.mem)
		changed = true
	}
	if changed {
		u.version++
	}
	for key, ps := range u.pods {
		if p := s.byKey[key]; p == nil || string(p.UID) != ps.uid {
			delete(u.pods, key)
			u.version++
		}
	}
	u.nodeCPU, u.nodeMem = nodeCPU, nodeMem
}

type statsSummary struct {
	Pods []struct {
		PodRef struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
			UID       string `json:"uid"`
		} `json:"podRef"`
		Network *struct {
			RxBytes *uint64 `json:"rxBytes"`
			TxBytes *uint64 `json:"txBytes"`
		} `json:"network"`
	} `json:"pods"`
}

func metricsReason(err error) string {
	switch {
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return reasonMetricsDeny
	case apierrors.IsNotFound(err):
		return reasonNotInstalled
	default:
		return reasonDown
	}
}

func (s *Source) pollNetwork(parent context.Context) {
	now := s.opts.Now()
	s.mu.RLock()
	node := s.displayNodeLocked()
	st := s.usage.net[node]
	waiting := st != nil && now.Before(st.retryAt)
	s.mu.RUnlock()
	if s.rest == nil || node == "" || !s.caps.nodes || waiting {
		return
	}
	ctx, cancel := context.WithTimeout(parent, s.netTimeout)
	defer cancel()
	raw, err := s.rest.Get().AbsPath("/api/v1/nodes", node, "proxy", "stats", "summary").Do(ctx).Raw()
	if err != nil {
		if parent.Err() == nil {
			s.netFailed(node, err, now)
		}
		return
	}
	var sum statsSummary
	if err := json.Unmarshal(raw, &sum); err != nil {
		s.netFailed(node, err, now)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	*s.usage.netState(node) = netState{okAt: now}
	for _, p := range sum.Pods {
		n := p.Network
		if n == nil || n.RxBytes == nil || n.TxBytes == nil {
			continue
		}
		key := podKey(p.PodRef.Namespace, p.PodRef.Name)
		cur := s.byKey[key]
		if cur == nil || (p.PodRef.UID != "" && p.PodRef.UID != string(cur.UID)) {
			continue
		}
		ps := s.usage.series(key, string(cur.UID))
		rx, tx := *n.RxBytes, *n.TxBytes
		if !ps.prevAt.IsZero() && rx >= ps.prevRX && tx >= ps.prevTX {
			dt := now.Sub(ps.prevAt).Seconds()
			if dt < 1 {
				continue
			}
			ps.rx = push(ps.rx, float64(rx-ps.prevRX)/dt/1024)
			ps.tx = push(ps.tx, float64(tx-ps.prevTX)/dt/1024)
			ps.netAt = now
		}
		ps.prevRX, ps.prevTX, ps.prevAt = rx, tx, now
	}
}

func (s *Source) netFailed(node string, err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nodeGoneLocked(node, err) {
		delete(s.usage.net, node)
		return
	}
	st := s.usage.netState(node)
	st.failures++
	base := metricsEvery
	switch {
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		st.reason, st.denied, base = reasonProxyDeny, true, netBackoffBase
	case apierrors.IsNotFound(err):
		st.reason, st.denied, base = reasonNoStats, true, netBackoffBase
	default:
		st.reason, st.denied = reasonKubeletDown, false
	}
	st.retryAt = now.Add(netBackoff(base, st.failures))
}

func (s *Source) nodeGoneLocked(node string, err error) bool {
	if !apierrors.IsNotFound(err) {
		return false
	}
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		if d := status.Status().Details; d != nil && d.Kind == "nodes" && d.Name == node {
			return true
		}
	}
	return s.nodeInf != nil && s.nodes[node] == nil
}

func (s *Source) Metrics(_ int, key string, heatCols int) data.Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setActive(key)
	u := &s.usage
	m := data.Metrics{Available: u.ok, Window: cpuWindow, PodCPU: make(map[string]int, len(u.pods))}
	if !u.ok {
		m.Reason = u.reason
		if m.Reason == "" {
			m.Reason = reasonQuerying
		}
	}
	for k := range u.pods {
		if ps := u.live(k, s.byKey[k]); ps != nil && len(ps.cpu) > 0 {
			m.PodCPU[k] = int(last(ps.cpu))
		}
	}
	node := s.displayNodeLocked()
	if n := s.nodes[node]; n != nil {
		if alloc := n.Status.Allocatable.Cpu().MilliValue(); alloc > 0 {
			used, ok := u.nodeCPU[node]
			m.NodeCPU = int(math.Round(float64(used) * 100 / float64(alloc)))
			m.NodeCPUKnown = ok
		}
	}
	p := s.byKey[key]
	ps := u.live(key, p)
	if ps != nil {
		m.CPU = tailCopy(ps.cpu, cpuWindow)
		m.Samples = len(m.CPU)
		m.CPUNow = int(last(ps.cpu))
		m.Mem = tailCopy(ps.mem, sparkWindow)
		m.MemNow = int(last(ps.mem))
		m.MemMax = math.Max(maxOf(m.Mem), 1)
		if _, _, _, lim := resources(p); lim > 0 {
			m.MemMax = float64(lim)
		}
		if st := u.net[p.Spec.NodeName]; (st == nil || !st.denied) && len(ps.rx) > 0 {
			m.RX = tailCopy(ps.rx, sparkWindow)
			m.TX = tailCopy(ps.tx, sparkWindow)
			m.RXNow, m.TXNow = last(ps.rx), last(ps.tx)
			m.RXMax = math.Max(maxOf(m.RX), 1)
			m.TXMax = math.Max(maxOf(m.TX), 1)
		}
	}
	if len(m.RX) > 0 {
		m.NetReason, m.NetStale = s.staleReasonLocked(p, ps)
	} else {
		m.NetReason = s.netReasonLocked(p, ps)
	}
	s.heat(&m, heatCols)
	return m
}

func (s *Source) staleReasonLocked(p *corev1.Pod, ps *podSeries) (string, bool) {
	if st := s.usage.net[p.Spec.NodeName]; st != nil && st.reason != "" {
		return st.reason, true
	}
	if s.opts.Now().Sub(ps.netAt) > netStaleAfter {
		return reasonNetStale, true
	}
	return "", false
}

func (s *Source) netReasonLocked(p *corev1.Pod, ps *podSeries) string {
	if !s.caps.nodes {
		return reasonNodesDeny
	}
	node := s.displayNodeLocked()
	if p != nil && p.Spec.NodeName != "" {
		node = p.Spec.NodeName
	}
	st := s.usage.net[node]
	if st != nil && st.reason != "" {
		return st.reason
	}
	if p == nil || podStatus(p) != data.StatusRunning {
		return ""
	}
	if ps != nil && !ps.prevAt.IsZero() {
		return reasonCollecting
	}
	if st != nil && !st.okAt.IsZero() {
		return reasonNoNetwork
	}
	return reasonCollecting
}

func (s *Source) heat(m *data.Metrics, cols int) {
	if cols <= 0 {
		return
	}
	u := &s.usage
	key := [3]int{u.version, u.heatGen, cols}
	if u.heatKey != key {
		u.heatNames, u.heatCells = s.buildHeat(cols)
		u.heatKey = key
	}
	m.HeatLabels, m.Heat = u.heatNames, u.heatCells
}

func (s *Source) buildHeat(cols int) ([]string, [][]float64) {
	type row struct {
		key   string
		label string
		cells []float64
		score float64
	}
	rows := make([]row, 0, len(s.usage.pods))
	for key := range s.usage.pods {
		p := s.byKey[key]
		ps := s.usage.live(key, p)
		if ps == nil || len(ps.cpu) == 0 || podStatus(p) != data.StatusRunning {
			continue
		}
		cells := tailCopy(ps.cpu, cols)
		score := 0.0
		for _, v := range cells {
			score += v
		}
		rows = append(rows, row{key: key, label: data.ShortName(p.Name), cells: cells, score: score})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].score != rows[j].score {
			return rows[i].score > rows[j].score
		}
		return rows[i].key < rows[j].key
	})
	if len(rows) > heatRows {
		rows = rows[:heatRows]
	}
	top := 1.0
	for _, r := range rows {
		top = math.Max(top, maxOf(r.cells))
	}
	names := make([]string, 0, len(rows))
	heat := make([][]float64, 0, len(rows))
	for _, r := range rows {
		cells := make([]float64, cols)
		offset := cols - len(r.cells)
		for i, v := range r.cells {
			cells[offset+i] = v / top
		}
		names = append(names, r.label)
		heat = append(heat, cells)
	}
	return names, heat
}

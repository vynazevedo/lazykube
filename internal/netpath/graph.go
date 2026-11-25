package netpath

import (
	"context"
	"strconv"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
)

type SetState int

const (
	SetUnknown SetState = iota
	SetKnown
	SetDenied
	SetAbsent
)

type Set struct {
	Rows  []data.ResourceRow
	State SetState
}

type Graph struct {
	Services  Set
	Slices    Set
	Ingresses Set
	Classes   Set
	Policies  Set
	Pods      Set
	Nodes     Set
}

const cancelStride = 256

type objectKey struct {
	namespace, name string
}

func keyOf(r data.ResourceRow) objectKey {
	return objectKey{r.Ref.Namespace, r.Ref.Name}
}

type labelKey struct {
	namespace, key, value string
}

type indexedSet struct {
	state    SetState
	readable bool
	rows     map[objectKey]*data.ResourceRow
}

func (s indexedSet) authoritative() bool {
	return s.state == SetKnown && s.readable
}

func (s indexedSet) gap(label string) string {
	switch s.state {
	case SetDenied:
		return "sem permissão para ler " + label
	case SetAbsent:
		return label + " não é servido por este cluster"
	case SetUnknown:
		return label + ": leitura incompleta, estado desconhecido"
	}
	if !s.readable {
		return label + ": há objetos que não couberam na projeção"
	}
	return ""
}

func (s indexedSet) find(namespace, name string) *data.ResourceRow {
	return s.rows[objectKey{namespace, name}]
}

type index struct {
	ctx       context.Context
	services  indexedSet
	slices    indexedSet
	ingresses indexedSet
	classes   indexedSet
	policies  indexedSet
	pods      indexedSet
	nodes     indexedSet
	byService map[objectKey][]*data.SliceFacts
	byLabel   map[labelKey][]*data.PodFacts
	podCounts map[string]podCount
	defaults  []string
	nodeIPs   []nodeIP
	nodeTotal int
}

type nodeIP struct {
	name, ip string
}

func newIndex(ctx context.Context, g Graph) (*index, error) {
	x := &index{ctx: ctx, byService: map[objectKey][]*data.SliceFacts{}, byLabel: map[labelKey][]*data.PodFacts{}, podCounts: map[string]podCount{}}
	for _, part := range []struct {
		set  *indexedSet
		from Set
		each func(*data.ResourceRow)
	}{
		{&x.services, g.Services, nil},
		{&x.ingresses, g.Ingresses, nil},
		{&x.policies, g.Policies, nil},
		{&x.slices, g.Slices, x.addSlice},
		{&x.pods, g.Pods, x.addPod},
		{&x.classes, g.Classes, x.addClass},
		{&x.nodes, g.Nodes, x.addNode},
	} {
		*part.set = indexedSet{state: part.from.State, readable: true, rows: make(map[objectKey]*data.ResourceRow, len(part.from.Rows))}
		for i := range part.from.Rows {
			if i%cancelStride == 0 && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			row := &part.from.Rows[i]
			part.set.rows[keyOf(*row)] = row
			if row.Network == nil || row.Network.Truncated {
				part.set.readable = false
			}
			if part.each != nil && row.Network != nil {
				part.each(row)
			}
		}
	}
	return x, ctx.Err()
}

func (x *index) addSlice(r *data.ResourceRow) {
	if s := r.Network.Slice; s != nil && s.Service != "" {
		key := objectKey{r.Ref.Namespace, s.Service}
		x.byService[key] = append(x.byService[key], s)
	}
}

func (x *index) addPod(r *data.ResourceRow) {
	if r.Network.Pod == nil {
		return
	}
	for _, l := range r.Network.Pod.Labels {
		key := labelKey{r.Ref.Namespace, l.Key, l.Value}
		x.byLabel[key] = append(x.byLabel[key], r.Network.Pod)
	}
}

func (x *index) addClass(r *data.ResourceRow) {
	if r.Network.Class != nil && r.Network.Class.Default {
		x.defaults = append(x.defaults, r.Ref.Name)
	}
}

func (x *index) addNode(r *data.ResourceRow) {
	n := r.Network.Node
	if n == nil || n.Ready == data.TriFalse {
		return
	}
	ips := n.External
	if len(ips) == 0 {
		ips = n.Internal
	}
	if len(ips) == 0 {
		return
	}
	x.nodeTotal++
	if len(x.nodeIPs) < maxNodes {
		x.nodeIPs = append(x.nodeIPs, nodeIP{r.Ref.Name, ips[0]})
	}
}

func (x *index) service(namespace, name string) (*data.ServiceFacts, data.Tri) {
	row := x.services.find(namespace, name)
	if row == nil {
		if x.services.state == SetKnown {
			return nil, data.TriFalse
		}
		return nil, data.TriUnknown
	}
	if row.Network == nil || row.Network.Service == nil || row.Network.Truncated {
		return nil, data.TriUnknown
	}
	return row.Network.Service, data.TriTrue
}

func selects(selector, labels data.Labels) bool {
	if len(selector) == 0 {
		return false
	}
	for _, want := range selector {
		if got, ok := labels.Get(want.Key); !ok || got != want.Value {
			return false
		}
	}
	return true
}

type podCount struct {
	matched, ready, draining, unclear int
	known                             bool
}

func (c *podCount) add(p *data.PodFacts) {
	c.matched++
	switch {
	case p.Ready == data.TriTrue && p.Terminating:
		c.draining++
	case p.Ready == data.TriTrue:
		c.ready++
	case p.Ready == data.TriUnknown && p.Phase != "Pending":
		c.unclear++
	}
}

func (x *index) candidates(namespace string, selector data.Labels) []*data.PodFacts {
	var best []*data.PodFacts
	for i, l := range selector {
		list := x.byLabel[labelKey{namespace, l.Key, l.Value}]
		if i == 0 || len(list) < len(best) {
			best = list
		}
	}
	return best
}

func selectorID(namespace string, selector data.Labels) string {
	var b strings.Builder
	b.WriteString(strconv.Quote(namespace))
	for _, l := range selector {
		b.WriteString(strconv.Quote(l.Key))
		b.WriteString(strconv.Quote(l.Value))
	}
	return b.String()
}

func (x *index) countPods(namespace string, selector data.Labels) podCount {
	id := selectorID(namespace, selector)
	if cached, ok := x.podCounts[id]; ok {
		return cached
	}
	out := podCount{known: x.pods.authoritative()}
	for i, p := range x.candidates(namespace, selector) {
		if i%cancelStride == 0 && x.ctx.Err() != nil {
			return podCount{}
		}
		if p.Phase != "Succeeded" && p.Phase != "Failed" && selects(selector, p.Labels) {
			out.add(p)
		}
	}
	x.podCounts[id] = out
	return out
}

type endpointCount struct {
	data.EndpointTally
	slices   int
	withPort bool
	diverges bool
	known    bool
}

func protocolOf(p string) string {
	if p == "" {
		return "TCP"
	}
	return p
}

func targetNumber(svc *data.ServiceFacts, port *data.ServicePort) (int, bool) {
	if port == nil || len(svc.Selector) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(port.Target)
	return n, err == nil && n > 0
}

func primaryFamily(svc *data.ServiceFacts) string {
	if svc.Headless() {
		return ""
	}
	if strings.Contains(svc.ClusterIPs[0], ":") {
		return "IPv6"
	}
	return "IPv4"
}

func (x *index) endpoints(namespace, service string, svc *data.ServiceFacts, port *data.ServicePort) endpointCount {
	out := endpointCount{known: x.slices.authoritative()}
	want, numeric := targetNumber(svc, port)
	families := map[string]data.EndpointTally{}
	for _, s := range x.byService[objectKey{namespace, service}] {
		out.slices++
		if port != nil {
			match := matchPort(s, port)
			if match == nil {
				continue
			}
			if match.Port == 0 || numeric && match.Port != want {
				out.diverges = true
				continue
			}
		}
		out.withPort = true
		t := families[s.AddressType]
		t.Merge(s.Tally())
		families[s.AddressType] = t
	}
	if family := primaryFamily(svc); family != "" {
		out.EndpointTally = families[family]
		for other, t := range families {
			if other != family && out.Total == 0 && t.Total > 0 {
				out.diverges = true
			}
		}
		return out
	}
	for family, t := range families {
		if family == "FQDN" {
			out.diverges = true
			continue
		}
		if t.Ready > out.Ready || t.Ready == out.Ready && t.Total > out.Total {
			out.EndpointTally = t
		}
	}
	return out
}

func matchPort(s *data.SliceFacts, port *data.ServicePort) *data.SlicePort {
	for i, p := range s.Ports {
		if p.Name == port.Name && protocolOf(p.Protocol) == protocolOf(port.Protocol) {
			return &s.Ports[i]
		}
	}
	return nil
}

func findPort(svc *data.ServiceFacts, backend data.IngressBackend) *data.ServicePort {
	var other *data.ServicePort
	for i, p := range svc.Ports {
		named := backend.PortName != "" && p.Name == backend.PortName
		numbered := backend.PortName == "" && backend.PortNumber > 0 && p.Port == backend.PortNumber
		if !named && !numbered {
			continue
		}
		if protocolOf(p.Protocol) == "TCP" {
			return &svc.Ports[i]
		}
		if other == nil {
			other = &svc.Ports[i]
		}
	}
	return other
}

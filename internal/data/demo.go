package data

import (
	"fmt"
	"math"
	"strings"

	"github.com/vynazevedo/lazykube/internal/theme"
)

const (
	demoNode        = "demo-node-01"
	demoNodeAddress = "192.168.0.12"
	cpuWindow       = 60
	sparkWindow     = 34
	logEvery        = 8
)

type demoPod struct {
	pod        Pod
	cpuBase    int
	containers []Container
	events     []Event
}

type Demo struct {
	security SecuritySnapshot
	pods     []demoPod
}

func NewDemo() *Demo {
	rows := []struct {
		name, ns string
		status   PodStatus
		restarts int
		age      string
		cpu      int
		ip       int
	}{
		{"bilibili-raffle-5d8f9c7b4-x2kqp", "bilibili-raffle", StatusRunning, 0, "119d", 4, 23},
		{"bilibili-live-7f6b8d9c5-mn4tz", "bilibili-raffle", StatusRunning, 2, "119d", 10, 24},
		{"lgo-7c5d97584-hq8wd", "lgo", StatusRunning, 0, "87d", 6, 31},
		{"psql-6bc7744f9-r7zlc", "ys-odoo", StatusRunning, 0, "64d", 12, 40},
		{"odoo-7575bf6d8-9wjvk", "ys-odoo", StatusRunning, 1, "64d", 18, 41},
		{"qiandao-1605400800-k8f2m", "xcx", StatusCompleted, 0, "2h", 0, 52},
		{"lingqu-1605486000-4tq8n", "xcx", StatusCompleted, 0, "41m", 0, 53},
		{"lingqu-1605489600-b7xsd", "xcx", StatusPending, 0, "8s", 0, 54},
		{"coredns-6799fbcd5-w9t4p", "kube-system", StatusRunning, 0, "119d", 3, 2},
		{"metrics-server-67c658944b-lm2x", "kube-system", StatusRunning, 0, "119d", 5, 3},
		{"traefik-f4564c4f4-zq7rn", "kube-system", StatusRunning, 0, "119d", 2, 4},
		{"local-path-provisioner-84db5d-h4", "kube-system", StatusFailed, 0, "119d", 1, 5},
		{"svclb-traefik-8c2f1-6n2vb", "kube-system", StatusFailed, 7, "119d", 1, 6},
	}
	d := &Demo{pods: make([]demoPod, len(rows))}
	for i, r := range rows {
		d.pods[i] = demoPod{
			cpuBase: r.cpu,
			pod: Pod{
				Name:       r.name,
				Namespace:  r.ns,
				Status:     r.status,
				Restarts:   r.restarts,
				Age:        r.age,
				IP:         fmt.Sprintf("10.42.0.%d", r.ip),
				Node:       demoNode,
				CPURequest: 10,
				CPULimit:   50,
				MemRequest: 64,
				MemLimit:   128,
				Image:      "registry.local/" + r.ns + ":1.4.2",
				Controller: "ReplicaSet/" + withoutLastSegment(r.name),
				QoS:        "Burstable",
			},
		}
		d.pods[i].containers = demoContainers(r.name, d.pods[i].pod.Image)
		d.pods[i].pod.Reason, d.pods[i].pod.Ready, d.pods[i].pod.Containers = demoSummary(r.status, d.pods[i].containers)
		d.pods[i].events = demoPodEvents(d.pods[i].pod)
	}
	d.security = d.buildSecurity()
	return d
}

func (d *Demo) Snapshot() Snapshot {
	pods := make([]Pod, len(d.pods))
	for i, p := range d.pods {
		pods[i] = p.pod
	}
	return Snapshot{
		Synthetic: true,
		Cluster: Cluster{
			Context:    "demo",
			Version:    "demo",
			Nodes:      1,
			Namespaces: 8,
			Pods:       len(pods),
			Uptime:     "119d",
		},
		Node: Node{
			Name:        demoNode,
			Address:     demoNodeAddress,
			Ready:       true,
			MemUsed:     "1.4Gi",
			MemTotal:    "3.8Gi",
			MemFraction: 0.37,
			Pods:        len(pods),
			PodCapacity: 110,
			Arch:        "amd64",
			Coverage:    Coverage{State: CoverageComplete},
		},
		Pods: pods,
		Deployments: []Deployment{
			{"bilibili-raffle", "bilibili-live", 1, 1},
			{"bilibili-raffle", "bilibili-raffle", 1, 1},
			{"ys-odoo", "psql", 1, 1},
			{"ys-odoo", "odoo", 1, 1},
			{"lgo", "lgo", 1, 1},
			{"kube-system", "coredns", 1, 1},
			{"kube-system", "metrics-server", 1, 1},
			{"kube-system", "traefik", 1, 1},
		},
		Namespaces: []Namespace{
			{"default", 0},
			{"kube-system", 5},
			{"kube-public", 0},
			{"kube-node-lease", 0},
			{"bilibili-raffle", 2},
			{"lgo", 1},
			{"xcx", 3},
			{"ys-odoo", 2},
		},
	}
}

func (d *Demo) index(key string) int {
	for i, p := range d.pods {
		if p.pod.Key() == key {
			return i
		}
	}
	return -1
}

func cpuV(i float64) float64 {
	v := 8 + 2.6*math.Sin(i/4) + 3.4*theme.Noise(i)
	if theme.Noise(3*i+1) > 0.93 {
		v += 5
	}
	return theme.Clamp(v, 0.5, 19.5)
}

func memV(i float64) float64 {
	return 73 + 3*math.Sin(i/9) + 2*theme.Noise(2*i)
}

func rxV(i float64) float64 { return theme.Noise(2.3*i) * 10 }
func txV(i float64) float64 { return theme.Noise(4.1*i+5) * 6 }

func (d *Demo) Metrics(tick int, pod string, heatCols int) Metrics {
	step := tick / 2
	m := Metrics{
		Available:    true,
		NodeCPU:      int(math.Round(18 + theme.Noise(float64(step))*8)),
		NodeCPUKnown: true,
		PodCPU:       make(map[string]int, len(d.pods)),
	}
	for i, p := range d.pods {
		if p.pod.Status != StatusRunning {
			m.PodCPU[p.pod.Key()] = 0
			continue
		}
		v := p.cpuBase + int(math.Round((theme.Noise(float64(tick)*0.3+float64(i)*9)-0.5)*4))
		m.PodCPU[p.pod.Key()] = max(0, v)
	}

	idx := d.index(pod)
	running := idx >= 0 && d.pods[idx].pod.Status == StatusRunning
	offset := float64((idx - 1) * 37)
	m.CPU = make([]float64, cpuWindow)
	for k := range m.CPU {
		if running {
			m.CPU[k] = cpuV(float64(step+k) + offset)
		}
	}
	m.CPUNow = int(math.Round(m.CPU[cpuWindow-1]))
	m.Samples, m.Window = cpuWindow, cpuWindow

	m.Mem = make([]float64, sparkWindow)
	m.RX = make([]float64, sparkWindow)
	m.TX = make([]float64, sparkWindow)
	for k := 0; k < sparkWindow; k++ {
		i := float64(step+k) + offset
		m.Mem[k] = memV(i) - 66
		m.RX[k] = rxV(i)
		m.TX[k] = txV(i)
	}
	m.MemNow = int(math.Round(memV(float64(step+sparkWindow-1) + offset)))
	m.RXNow = m.RX[sparkWindow-1]
	m.TXNow = m.TX[sparkWindow-1]

	for _, p := range d.pods {
		if p.pod.Status != StatusRunning || len(m.HeatLabels) == 8 {
			continue
		}
		row := len(m.HeatLabels)
		m.HeatLabels = append(m.HeatLabels, ShortName(p.pod.Name))
		cells := make([]float64, max(0, heatCols))
		for col := range cells {
			n := theme.Noise(float64(step+col)*1.7 + float64(row)*31)
			cells[col] = theme.Clamp(float64(p.cpuBase)/20*0.9+(n-0.4)*0.45, 0, 1)
		}
		m.Heat = append(m.Heat, cells)
	}
	return m
}

var demoLogs = []demoLine{
	{LevelInfo, "ws connected room=21452505 shard=3"},
	{LevelDebug, "heartbeat ok rtt=42ms"},
	{LevelInfo, "danmaku batch received count=38"},
	{LevelInfo, "raffle detected id=883104 type=gift"},
	{LevelInfo, "join raffle ok id=883104 latency=118ms"},
	{LevelWarn, "rate limited by upstream, backoff 2s"},
	{LevelDebug, "heartbeat ok rtt=39ms"},
	{LevelInfo, "danmaku batch received count=51"},
	{LevelInfo, "raffle detected id=883117 type=guard"},
	{LevelError, "join raffle failed id=883117: already ended"},
	{LevelInfo, "reconnecting ws in 1s"},
	{LevelInfo, "ws connected room=21452505 shard=3"},
}

func (d *Demo) Logs(pod string, tick int, n int) []LogLine {
	return d.PodLogs(LogRequest{Pod: pod}, tick, n).Lines
}

type demoLine struct {
	level Level
	msg   string
}

func rotatingLogs(lines []demoLine, tick, n int) []LogLine {
	if n <= 0 || len(lines) == 0 {
		return nil
	}
	end := tick/logEvery + len(lines) - 1
	start := max(0, end-n+1)
	out := make([]LogLine, 0, end-start+1)
	for j := start; j <= end; j++ {
		l := lines[j%len(lines)]
		s := (14*60 + 3*j) % 3600
		out = append(out, LogLine{
			Time:    fmt.Sprintf("20:%02d:%02d", s/60, s%60),
			Level:   l.level,
			Message: l.msg,
		})
	}
	return out
}

func (d *Demo) Events(pod string) []Event {
	return d.ScopedEvents(EventQuery{Scope: ScopePod, Pod: pod}).Items
}

func (d *Demo) Manifest(pod string) []string {
	i := d.index(pod)
	if i < 0 {
		return nil
	}
	p := d.pods[i].pod
	return []string{
		"apiVersion: v1",
		"kind: Pod",
		"metadata:",
		"  name: " + p.Name,
		"  namespace: " + p.Namespace,
		"  labels:",
		"    app: " + ShortName(p.Name),
		"spec:",
		"  containers:",
		"  - name: app",
		"    image: " + p.Image,
		"    resources:",
		"      requests: { cpu: 10m, memory: 64Mi }",
		"      limits: { cpu: 50m, memory: 128Mi }",
		"    env:",
		"    - name: LOG_LEVEL",
		"      value: info",
		"  restartPolicy: Always",
		"  nodeName: " + p.Node,
	}
}

func (d *Demo) Describe(pod string) []Field {
	i := d.index(pod)
	if i < 0 {
		return nil
	}
	p := d.pods[i].pod
	return []Field{
		{"Name", p.Name},
		{"Namespace", p.Namespace},
		{"Node", p.Node + "/" + demoNodeAddress},
		{"Status", p.Reason},
		{"Ready", fmt.Sprintf("%d/%d", p.Ready, p.Containers)},
		{"IP", p.IP},
		{"Controlled By", p.Controller},
		{"Image", p.Image},
		{"Restart Count", fmt.Sprintf("%d", p.Restarts)},
		{"Requests", "cpu: 10m, memory: 64Mi"},
		{"Limits", "cpu: 50m, memory: 128Mi"},
		{"QoS Class", p.QoS},
		{"Conditions", demoConditions(p)},
		{"Tolerations", "node.kubernetes.io/not-ready:NoExecute for 300s"},
	}
}

func ShortName(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) < 2 {
		return name
	}
	return parts[0] + "-" + parts[1]
}

func withoutLastSegment(name string) string {
	i := strings.LastIndex(name, "-")
	if i <= 0 {
		return name
	}
	return name[:i]
}

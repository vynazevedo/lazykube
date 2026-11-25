package netpath

import (
	"slices"

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

func (s Set) authoritative() bool {
	if s.State != SetKnown {
		return false
	}
	for _, r := range s.Rows {
		if r.Network == nil || r.Network.Truncated {
			return false
		}
	}
	return true
}

func (s Set) gap(label string) string {
	switch s.State {
	case SetDenied:
		return "sem permissão para ler " + label
	case SetAbsent:
		return label + " não é servido por este cluster"
	case SetUnknown:
		return label + ": leitura incompleta, estado desconhecido"
	}
	if !s.authoritative() {
		return label + ": há objetos que não couberam na projeção"
	}
	return ""
}

func (s Set) find(namespace, name string) *data.ResourceRow {
	for i, r := range s.Rows {
		if r.Ref.Namespace == namespace && r.Ref.Name == name {
			return &s.Rows[i]
		}
	}
	return nil
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

func (g Graph) service(namespace, name string) (*data.ServiceFacts, data.Tri) {
	row := g.Services.find(namespace, name)
	if row == nil {
		if g.Services.State == SetKnown {
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
	matched, ready int
	known          bool
}

func (g Graph) pods(namespace string, selector data.Labels) podCount {
	out := podCount{known: g.Pods.authoritative()}
	for _, r := range g.Pods.Rows {
		if r.Ref.Namespace != namespace || r.Network == nil || r.Network.Pod == nil {
			continue
		}
		p := r.Network.Pod
		if p.Phase == "Succeeded" || p.Phase == "Failed" || !selects(selector, p.Labels) {
			continue
		}
		out.matched++
		if p.Ready == data.TriTrue && !p.Terminating {
			out.ready++
		}
	}
	return out
}

type endpointCount struct {
	total, ready int
	withPort     bool
	slices       int
	known        bool
}

func (g Graph) endpoints(namespace, service string, port *data.ServicePort) endpointCount {
	out := endpointCount{known: g.Slices.authoritative()}
	type tally struct{ total, ready int }
	families := map[string]tally{}
	for _, r := range g.Slices.Rows {
		if r.Ref.Namespace != namespace || r.Network == nil || r.Network.Slice == nil || r.Network.Slice.Service != service {
			continue
		}
		s := r.Network.Slice
		out.slices++
		if port != nil && !slices.ContainsFunc(s.Ports, func(p data.SlicePort) bool { return p.Name == port.Name }) {
			continue
		}
		out.withPort = true
		t := families[s.AddressType]
		t.total += s.Total
		t.ready += s.Ready
		families[s.AddressType] = t
	}
	for _, t := range families {
		out.total = max(out.total, t.total)
		out.ready = max(out.ready, t.ready)
	}
	return out
}

func findPort(svc *data.ServiceFacts, backend data.IngressBackend) *data.ServicePort {
	for i, p := range svc.Ports {
		if backend.PortName != "" && p.Name == backend.PortName || backend.PortName == "" && backend.PortNumber > 0 && p.Port == backend.PortNumber {
			return &svc.Ports[i]
		}
	}
	return nil
}

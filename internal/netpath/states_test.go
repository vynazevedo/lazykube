package netpath

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
)

var update = flag.Bool("update", false, "update golden files")

type endpointCase struct {
	name  string
	facts []data.SliceEndpoint
	set   *Set
	port  func(*data.SliceFacts)
	class string
}

func endpoint(ready, serving, terminating data.Tri) data.SliceEndpoint {
	return data.SliceEndpoint{Addresses: []string{"10.0.0.9"}, Ready: ready, Serving: serving, Terminating: terminating}
}

const (
	yes  = data.TriTrue
	no   = data.TriFalse
	none = data.TriUnknown
)

var endpointCases = []endpointCase{
	{name: "pronto", facts: []data.SliceEndpoint{endpoint(yes, yes, no)}, class: "ready"},
	{name: "pronto, condições nil", facts: []data.SliceEndpoint{endpoint(none, none, none)}, class: "ready"},
	{name: "pronto e outro drenando", facts: []data.SliceEndpoint{endpoint(yes, yes, no), endpoint(no, yes, yes)}, class: "ready"},
	{name: "só drenando", facts: []data.SliceEndpoint{endpoint(no, yes, yes), endpoint(no, yes, yes)}, class: "draining"},
	{name: "drenando e indisponível", facts: []data.SliceEndpoint{endpoint(no, yes, yes), endpoint(no, no, no)}, class: "draining"},
	{name: "encerrando sem atender", facts: []data.SliceEndpoint{endpoint(no, no, yes)}, class: "down"},
	{name: "encerrando, serving nil", facts: []data.SliceEndpoint{endpoint(no, none, yes)}, class: "unclear"},
	{name: "não pronto", facts: []data.SliceEndpoint{endpoint(no, no, no)}, class: "down"},
	{name: "não pronto, serving nil", facts: []data.SliceEndpoint{endpoint(no, none, none)}, class: "down"},
	{name: "não pronto mas serving", facts: []data.SliceEndpoint{endpoint(no, yes, no)}, class: "unclear"},
	{name: "pronto mas sem serving", facts: []data.SliceEndpoint{endpoint(yes, no, no)}, class: "unclear"},
	{name: "porta com outro protocolo", facts: []data.SliceEndpoint{endpoint(yes, yes, no)}, port: func(s *data.SliceFacts) { s.Ports[0].Protocol = "UDP" }, class: "noport"},
	{name: "porta com outro nome", facts: []data.SliceEndpoint{endpoint(yes, yes, no)}, port: func(s *data.SliceFacts) { s.Ports[0].Name = "grpc" }, class: "noport"},
	{name: "número diferente do targetPort", facts: []data.SliceEndpoint{endpoint(yes, yes, no)}, port: func(s *data.SliceFacts) { s.Ports[0].Port = 9999 }, class: "unclear"},
	{name: "número nil", facts: []data.SliceEndpoint{endpoint(yes, yes, no)}, port: func(s *data.SliceFacts) { s.Ports[0].Port = 0 }, class: "unclear"},
	{name: "só IPv6 para Service IPv4", facts: []data.SliceEndpoint{endpoint(yes, yes, no)}, port: func(s *data.SliceFacts) { s.AddressType = "IPv6" }, class: "unclear"},
	{name: "sem EndpointSlice", set: &Set{State: SetKnown}, class: "none"},
	{name: "EndpointSlice vazio", facts: []data.SliceEndpoint{}, class: "none"},
	{name: "leitura negada", set: &Set{State: SetDenied}, class: "unknown"},
	{name: "não servido", set: &Set{State: SetAbsent}, class: "unknown"},
	{name: "leitura parcial", set: &Set{}, class: "unknown"},
}

type podCase struct {
	name  string
	set   Set
	class string
}

func statePod(phase string, ready data.Tri, terminating bool) data.ResourceRow {
	p := webPod("web-1", ready)
	p.Network.Pod.Phase, p.Network.Pod.Terminating = phase, terminating
	return p
}

var podCases = []podCase{
	{"pronto", known(statePod("Running", yes, false)), "ready"},
	{"não pronto", known(statePod("Running", no, false)), "down"},
	{"Pending", known(statePod("Pending", none, false)), "down"},
	{"Ready desconhecido", known(statePod("Running", none, false)), "unclear"},
	{"encerrando e Ready", known(statePod("Running", yes, true)), "draining"},
	{"encerrando e não Ready", known(statePod("Running", no, true)), "down"},
	{"só concluídos", known(statePod("Succeeded", no, false)), "none"},
	{"nenhum pod", known(), "none"},
	{"leitura negada", Set{State: SetDenied}, "unknown"},
	{"leitura parcial", Set{}, "unknown"},
}

type serviceCase struct {
	name   string
	change func(*data.ServiceFacts)
}

var serviceCases = []serviceCase{
	{"ClusterIP com selector", nil},
	{"sem selector", func(s *data.ServiceFacts) { s.Selector = nil }},
	{"publishNotReadyAddresses", func(s *data.ServiceFacts) { s.PublishNotReady = true }},
	{"headless", func(s *data.ServiceFacts) { s.ClusterIPs = []string{"None"} }},
	{"targetPort nomeada", func(s *data.ServiceFacts) { s.Ports[0].Target = "web" }},
}

func stateGraph(s serviceCase, e endpointCase, p podCase) Graph {
	g := healthy()
	g.Ingresses = known()
	g.Services = known(webService(func(f *data.ServiceFacts) {
		f.Ports[0].Target = "8080"
		if s.change != nil {
			s.change(f)
		}
	}))
	g.Pods = p.set
	if e.set != nil {
		g.Slices = *e.set
		return g
	}
	row := webSlice(0, 0)
	row.Network.Slice.Endpoints, row.Network.Slice.Total = e.facts, len(e.facts)
	if e.port != nil {
		e.port(row.Network.Slice)
	}
	g.Slices = known(row)
	return g
}

func eachState(visit func(serviceCase, endpointCase, podCase, Report)) {
	for _, s := range serviceCases {
		for _, e := range endpointCases {
			for _, p := range podCases {
				visit(s, e, p, Access(stateGraph(s, e, p), serviceTarget))
			}
		}
	}
}

func broken(r Report) []Step {
	var out []Step
	for _, route := range r.Routes {
		for _, step := range route.Steps {
			if step.Status == StatusBroken {
				out = append(out, step)
			}
		}
	}
	return out
}

func endpointClass(s serviceCase, e endpointCase) string {
	switch {
	case e.name == "número diferente do targetPort" && (s.name == "sem selector" || s.name == "targetPort nomeada"):
		return "ready"
	case e.name == "só IPv6 para Service IPv4" && s.name == "headless":
		return "ready"
	}
	return e.class
}

func TestNoCombinationIsFalselyReadyOrFalselyBroken(t *testing.T) {
	seen := map[string]int{}
	eachState(func(s serviceCase, e endpointCase, p podCase, r Report) {
		name := s.name + " | " + e.name + " | " + p.name
		a := r.Addresses[0]
		class := endpointClass(s, e)
		seen[class]++
		bad := broken(r)
		if a.State == StateReady && class != "ready" {
			t.Errorf("falso pronta: %s: %+v", name, a)
		}
		if a.State == StateDraining && class != "draining" {
			t.Errorf("falsa drenagem: %s: %+v", name, a)
		}
		if len(bad) > 0 && (class == "ready" || class == "draining" || class == "unclear") {
			t.Errorf("falso quebrado: %s: %+v", name, bad)
		}
		if len(bad) > 0 && class == "unknown" && (p.class != "down" && p.class != "none" || s.name == "publishNotReadyAddresses" && p.class == "down") {
			t.Errorf("falso quebrado sem leitura dos endpoints: %s: %+v", name, bad)
		}
		if len(bad) == 0 && (class == "down" || class == "none" || class == "noport") {
			t.Errorf("quebra não apontada: %s", name)
		}
		if class == "draining" && a.State != StateDraining {
			t.Errorf("drenagem não apontada: %s: %s", name, a.State)
		}
		if class == "ready" && a.State != StateReady {
			if s.name != "publishNotReadyAddresses" || p.class == "ready" {
				t.Errorf("pronta não apontada: %s: %s", name, a.State)
			}
		}
		if s.name == "publishNotReadyAddresses" && a.State == StateReady && p.class != "ready" {
			t.Errorf("publishNotReadyAddresses sem pod pronto vira pronta: %s", name)
		}
		if a.State == StateDraining && !strings.Contains(a.Reason, " em drenagem") {
			t.Errorf("drenagem sem contagem: %s: %q", name, a.Reason)
		}
		if a.State == StateDraining && !strings.HasPrefix(r.Routes[0].Verdict(), "em drenagem: ") {
			t.Errorf("veredito: %s: %q", name, r.Routes[0].Verdict())
		}
		if strings.HasPrefix(r.Routes[0].Verdict(), "em drenagem") != (a.State == StateDraining) {
			t.Errorf("estado e veredito divergem: %s: %s, %q", name, a.State, r.Routes[0].Verdict())
		}
		if len(bad) > 0 && a.State == StateReady {
			t.Errorf("pronta e quebrada ao mesmo tempo: %s", name)
		}
	})
	for _, class := range []string{"ready", "draining", "down", "unclear", "none", "noport", "unknown"} {
		if seen[class] == 0 {
			t.Errorf("classe %s não coberta", class)
		}
	}
}

func TestStateTableGolden(t *testing.T) {
	var b strings.Builder
	b.WriteString("Service | EndpointSlice | Pods | Estado mostrado | Veredito da rota\n")
	eachState(func(s serviceCase, e endpointCase, p podCase, r Report) {
		fmt.Fprintf(&b, "%s | %s | %s | %s | %s\n", s.name, e.name, p.name, r.Addresses[0].State, r.Routes[0].Verdict())
	})
	path := filepath.Join("testdata", "estados.txt.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden, run go test ./internal/netpath -update: %v", err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != b.String() {
		t.Fatal("the state table changed; run go test ./internal/netpath -update and review the diff")
	}
}

func TestDrainingServiceIsNeitherReadyNorBroken(t *testing.T) {
	g := healthy()
	p := webPod("web-1", yes)
	p.Network.Pod.Terminating = true
	g.Pods = known(p)
	s := webSlice(1, 0)
	s.Network.Slice.Endpoints = []data.SliceEndpoint{endpoint(no, yes, yes)}
	g.Slices = known(s)
	g.Services.Rows[0].Network.Service.Type = "LoadBalancer"
	g.Services.Rows[0].Network.Service.LoadBalancer = []data.LoadBalancerAddress{{IP: "203.0.113.7"}}
	for _, target := range []Target{serviceTarget, ingressTarget, podTarget} {
		r := Access(g, target)
		if len(broken(r)) > 0 {
			t.Fatalf("%+v", broken(r))
		}
		for _, a := range r.Addresses {
			if a.State != StateDraining || !strings.Contains(a.Reason, "1 de 1 em drenagem") {
				t.Fatalf("%+v", a)
			}
		}
		for _, route := range r.Routes {
			if !strings.Contains(route.Verdict(), "o tráfego não foi testado") || !strings.HasPrefix(route.Verdict(), "em drenagem") {
				t.Fatal(route.Verdict())
			}
		}
	}
}

func TestSlicePortNeedsTheSameProtocol(t *testing.T) {
	for _, tc := range []struct {
		service, slice string
		ready          bool
	}{{"", "", true}, {"TCP", "", true}, {"", "TCP", true}, {"TCP", "UDP", false}, {"UDP", "UDP", true}, {"UDP", "", false}, {"UDP", "TCP", false}, {"SCTP", "SCTP", true}, {"SCTP", "TCP", false}} {
		g := healthy()
		g.Ingresses = known()
		g.Services.Rows[0].Network.Service.Selector = nil
		g.Services.Rows[0].Network.Service.Ports[0].Protocol = tc.service
		g.Slices.Rows[0].Network.Slice.Ports[0].Protocol = tc.slice
		if a := Access(g, serviceTarget).Addresses[0]; (a.State == StateReady) != tc.ready {
			t.Errorf("service %q slice %q: %s", tc.service, tc.slice, a.State)
		}
	}
}

func TestIngressBackendPrefersTheTCPPort(t *testing.T) {
	ports := []data.ServicePort{{Name: "quic", Port: 443, Protocol: "UDP"}, {Name: "https", Port: 443, Protocol: "TCP"}}
	for _, tc := range []struct {
		backend data.IngressBackend
		want    string
	}{
		{data.IngressBackend{Service: "web", PortNumber: 443}, "https 443/TCP"},
		{data.IngressBackend{Service: "web", PortName: "https"}, "https 443/TCP"},
		{data.IngressBackend{Service: "web", PortName: "quic"}, "não é TCP"},
	} {
		g := healthy()
		g.Services.Rows[0].Network.Service.Ports = ports
		g.Slices.Rows[0].Network.Slice.Ports = []data.SlicePort{{Name: "quic", Port: 443, Protocol: "UDP"}, {Name: "https", Port: 443, Protocol: "TCP"}}
		g.Ingresses.Rows[0].Network.Ingress.Rules[0].Paths[0].Backend = tc.backend
		r := Access(g, ingressTarget)
		text := fmt.Sprintf("%+v", r.Routes[0].Steps)
		if !strings.Contains(text, tc.want) {
			t.Errorf("%+v: %s", tc.backend, text)
		}
		if strings.Contains(tc.want, "não é") == (r.Addresses[0].State == StateReady) {
			t.Errorf("%+v: %s", tc.backend, r.Addresses[0].State)
		}
	}
}

func TestServiceRouteChecksEveryPort(t *testing.T) {
	g := healthy()
	g.Ingresses = known()
	g.Services.Rows[0].Network.Service.Ports = append(g.Services.Rows[0].Network.Service.Ports, data.ServicePort{Name: "dns", Port: 53, Protocol: "UDP"})
	g.Slices.Rows[0].Network.Slice.Ports = append(g.Slices.Rows[0].Network.Slice.Ports, data.SlicePort{Name: "dns", Port: 53, Protocol: "TCP"})
	r := Access(g, serviceTarget)
	if v := r.Routes[0].Verdict(); !strings.Contains(v, "dns 53/UDP: nenhum EndpointSlice lido declara esta porta e protocolo") {
		t.Fatal(v)
	}
	if r.Addresses[0].State != StateReady || r.Addresses[1].State != StateNoBackend {
		t.Fatalf("%+v", r.Addresses)
	}
}

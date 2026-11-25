package netpath

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
)

func headless(s *data.ServiceFacts) {
	s.ClusterIPs, s.Selector = []string{"None"}, nil
}

func familySlice(name, family string, endpoints ...data.SliceEndpoint) data.ResourceRow {
	s := webSlice(0, 0)
	s.Ref.Name, s.Network.Slice.AddressType = name, family
	s.Network.Slice.Endpoints, s.Network.Slice.Total = endpoints, len(endpoints)
	return s
}

func shuffled(rng *rand.Rand, g Graph) Graph {
	for _, set := range []*Set{&g.Services, &g.Slices, &g.Ingresses, &g.Classes, &g.Policies, &g.Pods, &g.Nodes} {
		set.Rows = slices.Clone(set.Rows)
		rng.Shuffle(len(set.Rows), func(i, j int) { set.Rows[i], set.Rows[j] = set.Rows[j], set.Rows[i] })
	}
	return g
}

func mixedGraph() Graph {
	g := healthy()
	headless(g.Services.Rows[0].Network.Service)
	g.Services.Rows[0].Network.Service.Selector = webLabels
	g.Services.Rows[0].Network.Service.Ports = append(g.Services.Rows[0].Network.Service.Ports, data.ServicePort{Name: "node", Port: 81, Protocol: "TCP", NodePort: 30081})
	second := webService(func(s *data.ServiceFacts) {
		s.Type, s.ClusterIPs = "NodePort", []string{"10.1.0.2", "fd00::2"}
		s.Ports[0].NodePort = 30080
	})
	second.Ref.Name = "dual"
	g.Services.Rows = append(g.Services.Rows, second)
	dual := familySlice("dual-6", "IPv6", endpoint(no, yes, yes))
	dual.Network.Slice.Service = "dual"
	g.Slices = known(
		familySlice("a", "IPv4", endpoint(no, no, no)),
		familySlice("b", "IPv6", endpoint(no, yes, yes)),
		familySlice("c", "FQDN", endpoint(yes, yes, no)),
		familySlice("d", "IPv4", endpoint(no, none, yes)),
		dual,
	)
	for i := range 40 {
		ing := webIngress(nil)
		ing.Ref.Name = fmt.Sprintf("ing-%02d", i)
		ing.Network.Ingress.Rules[0].Host = fmt.Sprintf("h%02d.example.com", i)
		g.Ingresses.Rows = append(g.Ingresses.Rows, ing)
		g.Policies.Rows = append(g.Policies.Rows, row("apps", fmt.Sprintf("p-%02d", i), data.NetworkFacts{Policy: &data.NetworkPolicyFacts{}}))
		g.Nodes.Rows = append(g.Nodes.Rows, row("", fmt.Sprintf("n-%02d", i), data.NetworkFacts{Node: &data.NodeFacts{Ready: data.TriTrue, Internal: []string{fmt.Sprintf("10.2.0.%d", i+1)}}}))
		g.Classes.Rows = append(g.Classes.Rows, row("", fmt.Sprintf("c-%02d", i), data.NetworkFacts{Class: &data.IngressClassFacts{Default: true}}))
	}
	return g
}

func TestTheSameGraphAlwaysGivesTheSameReport(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 9))
	graphs := map[string]Graph{"mixed": mixedGraph(), "large": largeGraph(300)}
	for name, ready := range map[string][]data.SliceEndpoint{
		"tie draining": {endpoint(no, yes, yes)}, "tie unclear": {endpoint(no, none, yes)}, "tie ready": {endpoint(yes, yes, no)}, "tie down": {endpoint(no, no, no)},
	} {
		g := healthy()
		headless(g.Services.Rows[0].Network.Service)
		g.Slices = known(familySlice("four", "IPv4", endpoint(no, no, no)), familySlice("six", "IPv6", ready...))
		graphs[name] = g
	}
	for name, g := range graphs {
		for _, target := range []Target{podTarget, serviceTarget, ingressTarget, largeTarget} {
			want := fmt.Sprintf("%+v", Access(g, target))
			for i := range 300 {
				if got := fmt.Sprintf("%+v", Access(shuffled(rng, g), target)); got != want {
					t.Fatalf("%s target %d run %d:\n got %s\nwant %s", name, target.Kind, i, got, want)
				}
			}
		}
	}
}

func TestHeadlessFamiliesNeverHideEachOther(t *testing.T) {
	down := []data.SliceEndpoint{endpoint(no, no, no), endpoint(no, no, no)}
	for _, tc := range []struct {
		other  data.SliceEndpoint
		state  State
		counts string
	}{
		{endpoint(no, yes, yes), StateDraining, "IPv4: 0 de 2 prontos, 2 indisponíveis; IPv6: 0 de 1 prontos, 1 em drenagem"},
		{endpoint(no, none, yes), StateUnknown, "IPv4: 0 de 2 prontos, 2 indisponíveis; IPv6: 0 de 1 prontos, 1 indeterminados"},
		{endpoint(yes, yes, no), StateReady, "IPv4: 0 de 2 prontos, 2 indisponíveis; IPv6: 1 de 1 prontos"},
		{endpoint(no, no, no), StatePending, "IPv4: 0 de 2 prontos, 2 indisponíveis; IPv6: 0 de 1 prontos, 1 indisponíveis"},
	} {
		for _, change := range []func(*data.ServiceFacts){headless, func(s *data.ServiceFacts) { s.ClusterIPs, s.Selector = []string{"10.1.0.1", "fd00::1"}, nil }} {
			g := healthy()
			change(g.Services.Rows[0].Network.Service)
			g.Slices = known(familySlice("four", "IPv4", down...), familySlice("six", "IPv6", tc.other))
			r := Access(g, ingressTarget)
			if a := r.Addresses[0]; a.State != tc.state || (len(broken(r)) > 0) != (tc.state == StatePending) {
				t.Fatalf("%+v: %s broken %+v", tc.other, a.State, broken(r))
			}
			if text := fmt.Sprintf("%+v", r.Routes[0].Steps); !strings.Contains(text, tc.counts) {
				t.Fatalf("misses %q: %s", tc.counts, text)
			}
		}
	}
}

func TestIndeterminateEndpointsStayVisibleAndNeverDecide(t *testing.T) {
	unclear := endpoint(no, none, yes)
	g := healthy()
	headless(g.Services.Rows[0].Network.Service)
	g.Slices = known(familySlice("four", "IPv4", unclear), familySlice("six", "IPv6", unclear))
	r := Access(g, ingressTarget)
	if r.Addresses[0].State != StateUnknown || !strings.Contains(r.Addresses[0].Reason, "1 indeterminados") {
		t.Fatalf("%+v", r.Addresses[0])
	}
	g.Classes = known()
	if v := Access(g, ingressTarget).Routes[0].Verdict(); !strings.HasPrefix(v, "primeiro elo quebrado: IngressClass") {
		t.Fatal(v)
	}
	g = healthy()
	headless(g.Services.Rows[0].Network.Service)
	g.Slices = known(familySlice("four", "IPv4", unclear), familySlice("six", "IPv6", endpoint(yes, yes, no)))
	if r := Access(g, ingressTarget); r.Addresses[0].State != StateReady || !strings.Contains(fmt.Sprintf("%+v", r.Routes[0].Steps), "1 indeterminados") {
		t.Fatalf("%+v", r)
	}
}

func TestAbsentOrUnreadFamiliesProveNothing(t *testing.T) {
	g := healthy()
	g.Services.Rows[0].Network.Service.ClusterIPs = []string{"10.1.0.1", "fd00::1"}
	g.Services.Rows[0].Network.Service.Selector = nil
	g.Ingresses = known(webIngress(nil))
	g.Slices = known(familySlice("six", "IPv6", endpoint(yes, yes, no)))
	r := Access(g, serviceTarget)
	if len(broken(r)) > 0 || originAddress(r, OriginIngress).State != StateReady || originAddress(r, OriginClusterIP).State != StateUnknown {
		t.Fatalf("%+v", r)
	}
	g.Slices = Set{Rows: known(familySlice("four", "IPv4", endpoint(no, no, no))).Rows}
	if r := Access(g, serviceTarget); len(broken(r)) > 0 || originAddress(r, OriginIngress).State != StateUnknown {
		t.Fatalf("an incomplete reading proves nothing: %+v", r)
	}
}

func TestNamesAreNotAnIPFamily(t *testing.T) {
	for _, change := range []func(*data.ServiceFacts){headless, func(s *data.ServiceFacts) { s.Selector = nil }} {
		g := healthy()
		change(g.Services.Rows[0].Network.Service)
		g.Slices = known(familySlice("name", "FQDN", endpoint(yes, yes, no)))
		if r := Access(g, ingressTarget); r.Addresses[0].State != StateUnknown || len(broken(r)) > 0 {
			t.Fatalf("%+v", r.Addresses[0])
		}
	}
}

func TestLiteralAddressesOnlyInheritAServedFamily(t *testing.T) {
	ready, drain, down := endpoint(yes, yes, no), endpoint(no, yes, yes), endpoint(no, no, no)
	for _, tc := range []struct {
		name       string
		clusterIPs []string
		four, six  data.SliceEndpoint
		want       map[string]State
	}{
		{"IPv4 service, both ready", []string{"10.1.0.1"}, ready, ready, map[string]State{"4": StateReady, "6": StateUnknown, "name": StateReady}},
		{"IPv6 service, both ready", []string{"fd00::1"}, ready, ready, map[string]State{"4": StateUnknown, "6": StateReady, "name": StateReady}},
		{"IPv4 service, both draining", []string{"10.1.0.1"}, drain, drain, map[string]State{"4": StateDraining, "6": StateUnknown, "name": StateDraining}},
		{"IPv4 service, IPv6 ready only", []string{"10.1.0.1"}, down, ready, map[string]State{"4": StateUnknown, "6": StateUnknown, "name": StateUnknown}},
		{"dual, both ready", []string{"10.1.0.1", "fd00::1"}, ready, ready, map[string]State{"4": StateReady, "6": StateReady, "name": StateReady}},
		{"dual, IPv6 down", []string{"10.1.0.1", "fd00::1"}, ready, down, map[string]State{"4": StateReady, "6": StateUnknown, "name": StateReady}},
		{"dual, IPv6 draining", []string{"10.1.0.1", "fd00::1"}, ready, drain, map[string]State{"4": StateReady, "6": StateUnknown, "name": StateReady}},
	} {
		g := healthy()
		g.Ingresses = known()
		svc := g.Services.Rows[0].Network.Service
		svc.Type, svc.Selector, svc.ClusterIPs = "LoadBalancer", nil, tc.clusterIPs
		svc.ExternalIPs = []string{"203.0.113.20", "2001:db8::20"}
		svc.LoadBalancer = []data.LoadBalancerAddress{{IP: "203.0.113.30"}, {IP: "2001:db8::30"}, {Hostname: "lb.example.com"}}
		g.Slices = known(familySlice("four", "IPv4", tc.four), familySlice("six", "IPv6", tc.six))
		for _, a := range Access(g, serviceTarget).Addresses {
			kind := "name"
			switch {
			case strings.Contains(a.Display, "["):
				kind = "6"
			case !strings.Contains(a.Display, "lb.example.com"):
				kind = "4"
			}
			if a.State != tc.want[kind] {
				t.Errorf("%s: %s %s is %s, want %s (%s)", tc.name, a.Origin, a.Display, a.State, tc.want[kind], a.Reason)
			}
		}
	}
}

func TestNodePortLiteralsOnlyInheritAServedFamily(t *testing.T) {
	g := healthy()
	g.Ingresses = known()
	svc := g.Services.Rows[0].Network.Service
	svc.Type, svc.Selector, svc.Ports[0].NodePort = "NodePort", nil, 30080
	g.Slices = known(familySlice("four", "IPv4", endpoint(yes, yes, no)), familySlice("six", "IPv6", endpoint(yes, yes, no)))
	g.Nodes = known(
		row("", "a", data.NetworkFacts{Node: &data.NodeFacts{Ready: yes, Internal: []string{"10.2.0.1"}}}),
		row("", "b", data.NetworkFacts{Node: &data.NodeFacts{Ready: yes, Internal: []string{"fd00::b"}}}),
	)
	for _, a := range Access(g, serviceTarget).Addresses {
		want := StateReady
		if strings.Contains(a.Display, "[") {
			want = StateUnknown
		}
		if a.State != want {
			t.Errorf("%s %s is %s, want %s", a.Origin, a.Display, a.State, want)
		}
	}
}

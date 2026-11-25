package netpath

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
)

func row(ns, name string, f data.NetworkFacts) data.ResourceRow {
	return data.ResourceRow{Ref: data.ResourceMetadataRef{Namespace: ns, Name: name, UID: ns + name, ResourceVersion: "1"}, Network: &f}
}

func known(rows ...data.ResourceRow) Set { return Set{Rows: rows, State: SetKnown} }

var webLabels = data.Labels{{Key: "app", Value: "web"}}

func webService(change func(*data.ServiceFacts)) data.ResourceRow {
	s := &data.ServiceFacts{Type: "ClusterIP", ClusterIPs: []string{"10.1.0.1"}, Selector: webLabels, Ports: []data.ServicePort{{Name: "http", Port: 80, Protocol: "TCP"}}}
	if change != nil {
		change(s)
	}
	return row("apps", "web", data.NetworkFacts{Service: s})
}

func webSlice(total, ready int) data.ResourceRow {
	return row("apps", "web-x", data.NetworkFacts{Slice: &data.SliceFacts{Service: "web", AddressType: "IPv4", Ports: []data.SlicePort{{Name: "http", Port: 8080}}, Total: total, Ready: ready}})
}

func webPod(name string, ready data.Tri) data.ResourceRow {
	return row("apps", name, data.NetworkFacts{Pod: &data.PodFacts{Labels: webLabels, Phase: "Running", Ready: ready}})
}

func webIngress(change func(*data.IngressFacts)) data.ResourceRow {
	i := &data.IngressFacts{Class: "nginx", Rules: []data.IngressRule{{Host: "web.example.com", Paths: []data.IngressPath{{Path: "/", Type: "Prefix", Backend: data.IngressBackend{Service: "web", PortName: "http"}}}}}, TLS: []data.IngressTLS{{Hosts: []string{"web.example.com"}, Secret: "web-tls"}}, Addresses: []data.LoadBalancerAddress{{IP: "203.0.113.7"}}}
	if change != nil {
		change(i)
	}
	return row("apps", "web", data.NetworkFacts{Ingress: i})
}

func healthy() Graph {
	return Graph{
		Services:  known(webService(nil)),
		Slices:    known(webSlice(2, 2)),
		Ingresses: known(webIngress(nil)),
		Classes:   known(row("", "nginx", data.NetworkFacts{Class: &data.IngressClassFacts{Controller: "k8s.io/nginx"}})),
		Policies:  known(),
		Pods:      known(webPod("web-1", data.TriTrue), webPod("web-2", data.TriTrue)),
		Nodes:     known(),
	}
}

var (
	ingressTarget = Target{Kind: TargetIngress, Namespace: "apps", Name: "web"}
	serviceTarget = Target{Kind: TargetService, Namespace: "apps", Name: "web"}
	podTarget     = Target{Kind: TargetPod, Namespace: "apps", Name: "web-1"}
)

func TestRouteFindsTheFirstBrokenLink(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Graph)
		kind   StepKind
		status Status
		want   string
		state  State
	}{
		{"healthy", func(*Graph) {}, "", StatusOK, "nenhum elo quebrado nos objetos lidos; o tráfego não foi testado", StateReady},
		{"class missing", func(g *Graph) { g.Classes = known() }, StepClass, StatusBroken, "a IngressClass nginx não existe", StateReady},
		{"class denied", func(g *Graph) { g.Classes = Set{State: SetDenied} }, StepClass, StatusUnknown, "sem permissão para ler IngressClasses", StateReady},
		{"class not served", func(g *Graph) { g.Classes = Set{State: SetAbsent} }, StepClass, StatusUnknown, "não é servido", StateReady},
		{"no class and no default", func(g *Graph) { g.Ingresses = known(webIngress(func(i *data.IngressFacts) { i.Class = "" })) }, StepClass, StatusAttention, "sem ingressClassName e sem IngressClass padrão", StateReady},
		{"legacy annotation", func(g *Graph) {
			g.Ingresses = known(webIngress(func(i *data.IngressFacts) { i.Class, i.ClassAnnotation = "", "nginx" }))
		}, StepClass, StatusUnknown, "classe legada por anotação: nginx", StateReady},
		{"tls without secret", func(g *Graph) { g.Ingresses = known(webIngress(func(i *data.IngressFacts) { i.TLS[0].Secret = "" })) }, StepTLS, StatusAttention, "TLS sem secretName", StateReady},
		{"backend missing", func(g *Graph) { g.Services = known() }, StepService, StatusBroken, "o Service apps/web não existe", StateNoBackend},
		{"services denied", func(g *Graph) { g.Services = Set{State: SetDenied} }, StepService, StatusUnknown, "sem permissão para ler Services", StateUnknown},
		{"services incomplete", func(g *Graph) { g.Services = Set{State: SetUnknown} }, StepService, StatusUnknown, "leitura incompleta", StateUnknown},
		{"port missing", func(g *Graph) {
			g.Services = known(webService(func(s *data.ServiceFacts) { s.Ports[0].Name = "grpc" }))
		}, StepPort, StatusBroken, "a porta http não existe no Service web", StateNoBackend},
		{"port number missing", func(g *Graph) {
			g.Ingresses = known(webIngress(func(i *data.IngressFacts) {
				i.Rules[0].Paths[0].Backend = data.IngressBackend{Service: "web", PortNumber: 81}
			}))
		}, StepPort, StatusBroken, "a porta 81 não existe", StateNoBackend},
		{"port not informed", func(g *Graph) {
			g.Ingresses = known(webIngress(func(i *data.IngressFacts) { i.Rules[0].Paths[0].Backend = data.IngressBackend{Service: "web"} }))
		}, StepPort, StatusBroken, "o backend não informa porta", StateNoBackend},
		{"selector without pods", func(g *Graph) { g.Pods, g.Slices = known(), known() }, StepPods, StatusBroken, "o selector app=web não encontra pods", StateNoBackend},
		{"completed pods do not count", func(g *Graph) {
			p := webPod("web-1", data.TriFalse)
			p.Network.Pod.Phase = "Succeeded"
			g.Pods, g.Slices = known(p), known()
		}, StepPods, StatusBroken, "não encontra pods", StateNoBackend},
		{"pods in another namespace do not count", func(g *Graph) {
			p := webPod("web-1", data.TriTrue)
			p.Ref.Namespace = "other"
			g.Pods, g.Slices = known(p), known()
		}, StepPods, StatusBroken, "não encontra pods", StateNoBackend},
		{"pods not ready", func(g *Graph) { g.Pods, g.Slices = known(webPod("web-1", data.TriFalse)), known(webSlice(1, 0)) }, StepPods, StatusBroken, "1 pods selecionados, nenhum pronto", StatePending},
		{"terminating pod without a serving endpoint", func(g *Graph) {
			p := webPod("web-1", data.TriTrue)
			p.Network.Pod.Terminating = true
			g.Pods, g.Slices = known(p), known(webSlice(1, 0))
		}, StepEndpoints, StatusBroken, "1 endpoints, nenhum pronto", StatePending},
		{"pods denied and no endpoints", func(g *Graph) { g.Pods, g.Slices = Set{State: SetDenied}, known() }, StepEndpoints, StatusBroken, "nenhum endpoint do Service na lista lida", StateNoBackend},
		{"pods denied and healthy endpoints", func(g *Graph) { g.Pods = Set{State: SetDenied} }, StepPods, StatusUnknown, "sem permissão para ler Pods", StateReady},
		{"no endpoints", func(g *Graph) { g.Slices = known() }, StepEndpoints, StatusBroken, "nenhum endpoint do Service na lista lida", StatePending},
		{"endpoints not ready", func(g *Graph) { g.Slices = known(webSlice(2, 0)) }, StepEndpoints, StatusBroken, "2 endpoints, nenhum pronto", StatePending},
		{"port without endpoints", func(g *Graph) {
			s := webSlice(2, 2)
			s.Network.Slice.Ports[0].Name = "grpc"
			g.Slices = known(s)
		}, StepEndpoints, StatusBroken, "http 80/TCP: nenhum EndpointSlice lido declara esta porta e protocolo", StateNoBackend},
		{"slices denied", func(g *Graph) { g.Slices = Set{State: SetDenied} }, StepEndpoints, StatusUnknown, "sem permissão para ler EndpointSlices", StateUnknown},
		{"unreadable slice blocks negative claims", func(g *Graph) {
			g.Slices = known(data.ResourceRow{Ref: data.ResourceMetadataRef{Namespace: "apps", Name: "x"}, Network: &data.NetworkFacts{Truncated: true}})
		}, StepEndpoints, StatusUnknown, "não couberam", StateUnknown},
		{"slices of another service do not count", func(g *Graph) {
			s := webSlice(2, 2)
			s.Network.Slice.Service = "other"
			g.Slices = known(s)
		}, StepEndpoints, StatusBroken, "nenhum endpoint do Service na lista lida", StatePending},
		{"stale pods with ready endpoints", func(g *Graph) { g.Pods = known() }, StepPods, StatusAttention, "mas há endpoints que não estão indisponíveis", StateReady},
		{"address not published", func(g *Graph) { g.Ingresses = known(webIngress(func(i *data.IngressFacts) { i.Addresses = nil })) }, StepAddress, StatusAttention, "nenhum endereço publicado em status do Ingress", StatePending},
		{"resource backend", func(g *Graph) {
			g.Ingresses = known(webIngress(func(i *data.IngressFacts) {
				i.Rules[0].Paths[0].Backend = data.IngressBackend{Resource: "Bucket/static"}
			}))
		}, StepService, StatusUnknown, "Bucket/static", StateUnknown},
		{"empty backend", func(g *Graph) {
			g.Ingresses = known(webIngress(func(i *data.IngressFacts) { i.Rules[0].Paths[0].Backend = data.IngressBackend{} }))
		}, StepService, StatusBroken, "backend sem Service", StateNoBackend},
		{"external name backend", func(g *Graph) {
			g.Services = known(webService(func(s *data.ServiceFacts) { s.Type, s.ExternalName = "ExternalName", "x.example.com" }))
		}, StepPort, StatusUnknown, "ExternalName", StateUnknown},
		{"publish not ready", func(g *Graph) {
			g.Services = known(webService(func(s *data.ServiceFacts) { s.PublishNotReady = true }))
			g.Pods = known(webPod("web-1", data.TriFalse))
			g.Slices = known(webSlice(1, 0))
		}, StepEndpoints, StatusBroken, "nenhum pronto", StatePending},
		{"broken link wins over an earlier attention", func(g *Graph) {
			g.Ingresses = known(webIngress(func(i *data.IngressFacts) { i.TLS[0].Secret = "" }))
			g.Services = known()
		}, StepService, StatusBroken, "não existe", StateNoBackend},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := healthy()
			tc.change(&g)
			r := Access(g, ingressTarget)
			if len(r.Routes) != 1 || len(r.Addresses) != 1 {
				t.Fatalf("routes=%d addresses=%d notes=%v", len(r.Routes), len(r.Addresses), r.Notes)
			}
			step, found := r.Routes[0].First()
			if found != (tc.kind != "") || step.Kind != tc.kind || step.Status != tc.status {
				t.Fatalf("step=%+v", step)
			}
			if !strings.Contains(r.Routes[0].Verdict(), tc.want) {
				t.Fatalf("verdict %q misses %q", r.Routes[0].Verdict(), tc.want)
			}
			if r.Addresses[0].State != tc.state {
				t.Fatalf("state=%s reason=%q", r.Addresses[0].State, r.Addresses[0].Reason)
			}
		})
	}
}

func TestIngressAddresses(t *testing.T) {
	ingress := func(host, path string, change func(*data.IngressFacts)) data.ResourceRow {
		return webIngress(func(i *data.IngressFacts) {
			i.Rules[0].Host, i.Rules[0].Paths[0].Path = host, path
			i.TLS = nil
			if change != nil {
				change(i)
			}
		})
	}
	tls := func(hosts ...string) func(*data.IngressFacts) {
		return func(i *data.IngressFacts) { i.TLS = []data.IngressTLS{{Hosts: hosts, Secret: "s"}} }
	}
	for _, tc := range []struct {
		name    string
		row     data.ResourceRow
		url     string
		display string
		reach   Reach
		refused string
	}{
		{"plain", ingress("web.example.com", "/", nil), "http://web.example.com", "http://web.example.com", ReachDeclared, ""},
		{"empty path", ingress("web.example.com", "", nil), "http://web.example.com", "http://web.example.com", ReachDeclared, ""},
		{"tls", ingress("web.example.com", "/a", tls("web.example.com")), "https://web.example.com/a", "https://web.example.com/a", ReachDeclared, ""},
		{"tls wildcard covers", ingress("web.example.com", "/", tls("*.example.com")), "https://web.example.com", "https://web.example.com", ReachDeclared, ""},
		{"tls wildcard covers one label only", ingress("a.web.example.com", "/", tls("*.example.com")), "http://a.web.example.com", "http://a.web.example.com", ReachDeclared, ""},
		{"tls of another host", ingress("web.example.com", "/", tls("other.example.com")), "http://web.example.com", "http://web.example.com", ReachDeclared, ""},
		{"tls without hosts covers all", ingress("web.example.com", "/", tls()), "https://web.example.com", "https://web.example.com", ReachDeclared, ""},
		{"wildcard host", ingress("*.example.com", "/", nil), "", "*.example.com/", ReachUnknown, "host curinga"},
		{"empty host", ingress("", "/x", nil), "", "(qualquer host)/x", ReachUnknown, "host vazio"},
		{"regex path", ingress("web.example.com", "/api(/|$)(.*)", nil), "", "web.example.com/api(/|$)(.*)", ReachUnknown, "path não é literal"},
		{"homograph", ingress("\u0430pple.com", "/", nil), "", "\u0430pple.com/", ReachUnknown, "fora de ASCII"},
		{"userinfo", ingress("trusted.com@evil.com", "/", nil), "", "trusted.com@evil.com/", ReachUnknown, "host inválido"},
		{"localhost", ingress("localhost", "/", nil), "", "localhost/", ReachUnknown, "host local"},
		{"internal suffix", ingress("web.corp", "/", nil), "http://web.corp", "http://web.corp", ReachPrivate, ""},
		{"cluster suffix", ingress("web.apps.svc", "/", nil), "http://web.apps.svc", "http://web.apps.svc", ReachCluster, ""},
		{"private controller", ingress("web.example.com", "/", func(i *data.IngressFacts) { i.Addresses = []data.LoadBalancerAddress{{IP: "192.168.0.12"}} }), "http://web.example.com", "http://web.example.com", ReachPrivate, ""},
		{"mixed controller", ingress("web.example.com", "/", func(i *data.IngressFacts) {
			i.Addresses = []data.LoadBalancerAddress{{IP: "192.168.0.12"}, {Hostname: "lb.example.com"}}
		}), "http://web.example.com", "http://web.example.com", ReachDeclared, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := healthy()
			g.Ingresses = known(tc.row)
			a := Access(g, ingressTarget).Addresses[0]
			if a.URL.String() != tc.url || a.Display != tc.display || a.Reach != tc.reach || a.Origin != OriginIngress || a.Source != "Ingress apps/web" {
				t.Fatalf("%+v", a)
			}
			if tc.refused == "" && a.Refused != "" || !strings.Contains(a.Refused, tc.refused) {
				t.Fatalf("refused=%q", a.Refused)
			}
			if tc.refused != "" && !a.URL.IsZero() {
				t.Fatal("a refused address has no URL")
			}
			if tc.reach == ReachCluster && a.URL.Linkable() {
				t.Fatal("cluster addresses are not clickable")
			}
		})
	}
}

func TestIngressRuleWithoutPaths(t *testing.T) {
	g := healthy()
	g.Ingresses = known(webIngress(func(i *data.IngressFacts) { i.Rules[0].Paths = nil }))
	a := Access(g, ingressTarget).Addresses
	if len(a) != 1 || a[0].State != StateNoBackend || !a[0].URL.IsZero() {
		t.Fatalf("%+v", a)
	}
	g.Ingresses = known(webIngress(func(i *data.IngressFacts) {
		i.Rules[0].Paths = nil
		i.Default = &data.IngressBackend{Service: "web", PortNumber: 80}
	}))
	a = Access(g, ingressTarget).Addresses
	if len(a) != 2 || a[0].URL.String() != "https://web.example.com" || a[1].Display != "(qualquer host)/" || !a[1].URL.IsZero() {
		t.Fatalf("%+v", a)
	}
}

func TestServiceAddresses(t *testing.T) {
	node := func(name string, ready data.Tri, internal, external string) data.ResourceRow {
		f := &data.NodeFacts{Ready: ready}
		if internal != "" {
			f.Internal = []string{internal}
		}
		if external != "" {
			f.External = []string{external}
		}
		return row("", name, data.NetworkFacts{Node: f})
	}
	for _, tc := range []struct {
		name   string
		change func(*data.ServiceFacts)
		nodes  Set
		want   []string
	}{
		{"cluster ip", nil, known(), []string{"ClusterIP|http://10.1.0.1|endereço interno do cluster|pronta"}},
		{"headless", func(s *data.ServiceFacts) { s.ClusterIPs = []string{"None"} }, known(), []string{"ClusterIP|web.apps.svc headless http 80/TCP|endereço interno do cluster|pronta"}},
		{"load balancer hostname", func(s *data.ServiceFacts) {
			s.Type = "LoadBalancer"
			s.LoadBalancer = []data.LoadBalancerAddress{{Hostname: "lb.example.com"}}
		}, known(), []string{"LoadBalancer|http://lb.example.com|declarada, alcance não verificado|pronta", "ClusterIP|http://10.1.0.1|endereço interno do cluster|pronta"}},
		{"load balancer private ip", func(s *data.ServiceFacts) {
			s.Type = "LoadBalancer"
			s.LoadBalancer = []data.LoadBalancerAddress{{IP: "192.168.1.5"}}
		}, known(), []string{"LoadBalancer|http://192.168.1.5|provável rede privada, alcance não verificado|pronta", "ClusterIP|http://10.1.0.1|endereço interno do cluster|pronta"}},
		{"load balancer localhost", func(s *data.ServiceFacts) {
			s.Type = "LoadBalancer"
			s.LoadBalancer = []data.LoadBalancerAddress{{Hostname: "localhost"}}
		}, known(), []string{"LoadBalancer|localhost 80/TCP|alcance desconhecido|pronta", "ClusterIP|http://10.1.0.1|endereço interno do cluster|pronta"}},
		{"load balancer pending", func(s *data.ServiceFacts) { s.Type = "LoadBalancer" }, known(), []string{"LoadBalancer|(sem endereço) http 80/TCP|alcance desconhecido|pendente", "ClusterIP|http://10.1.0.1|endereço interno do cluster|pronta"}},
		{"node port", func(s *data.ServiceFacts) { s.Type = "NodePort"; s.Ports[0].NodePort = 30080 },
			known(node("n1", data.TriTrue, "10.0.0.1", "203.0.113.1"), node("n2", data.TriTrue, "10.0.0.2", ""), node("n3", data.TriFalse, "10.0.0.3", ""), node("n4", data.TriTrue, "10.0.0.4", "")),
			[]string{"NodePort|http://203.0.113.1:30080|porta alocada no node, alcance não verificado|pronta", "NodePort|http://10.0.0.2:30080|porta alocada no node, alcance não verificado|pronta", "ClusterIP|http://10.1.0.1|endereço interno do cluster|pronta"}},
		{"node port without nodes", func(s *data.ServiceFacts) { s.Type = "NodePort"; s.Ports[0].NodePort = 30080 }, Set{State: SetDenied},
			[]string{"NodePort|(node não identificado):30080|porta alocada no node, alcance não verificado|pronta", "ClusterIP|http://10.1.0.1|endereço interno do cluster|pronta"}},
		{"external ip", func(s *data.ServiceFacts) { s.ExternalIPs = []string{"203.0.113.4"} }, known(), []string{"ExternalIP|http://203.0.113.4|declarada, alcance não verificado|pronta", "ClusterIP|http://10.1.0.1|endereço interno do cluster|pronta"}},
		{"unknown scheme", func(s *data.ServiceFacts) {
			s.Ports[0] = data.ServicePort{Name: "http", Port: 5432, Protocol: "TCP", AppProtocol: "postgresql"}
		}, known(), []string{"ClusterIP|10.1.0.1 5432/TCP|endereço interno do cluster|pronta"}},
		{"udp", func(s *data.ServiceFacts) { s.Ports[0].Protocol = "UDP" }, known(), []string{"ClusterIP|10.1.0.1 80/UDP|endereço interno do cluster|sem backend"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := healthy()
			g.Ingresses, g.Nodes = known(), tc.nodes
			g.Services = known(webService(tc.change))
			r := Access(g, serviceTarget)
			var got []string
			for _, a := range r.Addresses {
				got = append(got, fmt.Sprintf("%s|%s|%s|%s", a.Origin, a.Display, a.Reach, a.State))
				if a.Origin == OriginClusterIP && a.URL.Linkable() {
					t.Fatal("a cluster IP is never clickable")
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestSchemeEvidence(t *testing.T) {
	for _, tc := range []struct {
		port   data.ServicePort
		scheme string
		why    string
	}{
		{data.ServicePort{Name: "web", Port: 9000, AppProtocol: "https"}, "https", "appProtocol"},
		{data.ServicePort{Name: "x", Port: 1, AppProtocol: "kubernetes.io/h2c"}, "http", "appProtocol"},
		{data.ServicePort{Name: "http", Port: 80, AppProtocol: "grpc"}, "", ""},
		{data.ServicePort{Name: "https-admin", Port: 9000}, "https", "nome da porta"},
		{data.ServicePort{Name: "http-metrics", Port: 9000}, "http", "nome da porta"},
		{data.ServicePort{Name: "httpd", Port: 9000}, "", ""},
		{data.ServicePort{Port: 443}, "https", "presumido pela porta 443"},
		{data.ServicePort{Port: 80}, "http", "presumido pela porta 80"},
		{data.ServicePort{Port: 8080}, "", ""},
		{data.ServicePort{Name: "http", Port: 80, Protocol: "UDP"}, "", ""},
	} {
		if scheme, why := schemeOf(tc.port); scheme != tc.scheme || why != tc.why {
			t.Errorf("%+v: %q %q", tc.port, scheme, why)
		}
	}
}

func TestPodAndWorkloadAccess(t *testing.T) {
	g := healthy()
	r := Access(g, podTarget)
	if len(r.Addresses) != 2 || r.Addresses[0].URL.String() != "https://web.example.com" || r.Addresses[1].Origin != OriginClusterIP || len(r.Routes) != 2 {
		t.Fatalf("%+v", r)
	}
	w := Access(g, Target{Kind: TargetWorkload, Namespace: "apps", Name: "web", Labels: webLabels, LabelsKnown: true})
	if len(w.Addresses) != 2 {
		t.Fatalf("%+v", w)
	}
	other := Access(g, Target{Kind: TargetWorkload, Namespace: "apps", Name: "x", Labels: data.Labels{{Key: "app", Value: "x"}}, LabelsKnown: true})
	if len(other.Addresses) != 0 || !slices.Contains(other.Notes, "nenhum Service da lista lida seleciona este alvo") {
		t.Fatalf("%+v", other)
	}
	g.Services = Set{State: SetDenied}
	denied := Access(g, podTarget)
	if slices.Contains(denied.Notes, "nenhum Service da lista lida seleciona este alvo") || !slices.Contains(denied.Notes, "sem permissão para ler Services") {
		t.Fatalf("denied is not empty: %v", denied.Notes)
	}
	g = healthy()
	g.Pods = Set{State: SetDenied}
	blind := Access(g, podTarget)
	if len(blind.Addresses) != 0 || !strings.Contains(strings.Join(blind.Notes, " "), "labels desconhecidas") || blind.Policies.Known {
		t.Fatalf("%+v", blind)
	}
	g = healthy()
	g.Services = known(webService(func(s *data.ServiceFacts) { s.Selector = nil }))
	if r := Access(g, podTarget); len(r.Addresses) != 0 {
		t.Fatal("an empty selector selects no pods")
	}
	g = healthy()
	g.Services.Rows[0].Ref.Namespace = "other"
	if r := Access(g, podTarget); len(r.Addresses) != 0 {
		t.Fatal("services of another namespace never select the pod")
	}
}

func TestServiceRouteReportsPendingLoadBalancer(t *testing.T) {
	g := healthy()
	g.Ingresses = known()
	g.Services = known(webService(func(s *data.ServiceFacts) { s.Type = "LoadBalancer" }))
	r := Access(g, serviceTarget)
	step, _ := r.Routes[0].First()
	if step.Kind != StepAddress || step.Status != StatusBroken || !strings.Contains(step.Evidence, "LoadBalancer pendente") {
		t.Fatalf("%+v", step)
	}
	g.Services = known(webService(func(s *data.ServiceFacts) { s.Type, s.ExternalName = "ExternalName", "db.example.com" }))
	r = Access(g, serviceTarget)
	if len(r.Addresses) != 0 || !strings.Contains(strings.Join(r.Notes, " "), "declara ExternalName db.example.com: alias DNS, destino não verificado") {
		t.Fatalf("%+v", r)
	}
	g.Services = known()
	if r = Access(g, serviceTarget); !slices.Contains(r.Notes, "o Service não está mais na lista") {
		t.Fatalf("%v", r.Notes)
	}
	g.Ingresses = Set{State: SetDenied}
	if r = Access(g, ingressTarget); !slices.Contains(r.Notes, "sem permissão para ler Ingresses") {
		t.Fatalf("%v", r.Notes)
	}
}

func TestSelectorlessServiceUsesItsEndpoints(t *testing.T) {
	g := healthy()
	g.Pods = known()
	g.Services = known(webService(func(s *data.ServiceFacts) { s.Selector = nil }))
	if a := Access(g, ingressTarget).Addresses[0]; a.State != StateReady {
		t.Fatalf("%+v", a)
	}
}

func TestDualStackSlicesAreNotCountedTwice(t *testing.T) {
	g := healthy()
	v6 := webSlice(2, 2)
	v6.Ref.Name, v6.Network.Slice.AddressType = "web-6", "IPv6"
	g.Slices = known(webSlice(2, 2), v6)
	for _, s := range Access(g, ingressTarget).Routes[0].Steps {
		if s.Kind == StepEndpoints && s.Evidence != "http 80/TCP: 2 de 2 prontos" {
			t.Fatal(s.Evidence)
		}
	}
}

func TestPoliciesOnlySaySelection(t *testing.T) {
	policy := func(name string, f data.NetworkPolicyFacts) data.ResourceRow {
		return row("apps", name, data.NetworkFacts{Policy: &f})
	}
	expr := func(op string, values ...string) []data.SelectorRequirement {
		return []data.SelectorRequirement{{Key: "app", Operator: op, Values: values}}
	}
	g := healthy()
	foreign := policy("foreign", data.NetworkPolicyFacts{})
	foreign.Ref.Namespace = "other"
	g.Policies = known(
		policy("all", data.NetworkPolicyFacts{}),
		policy("label", data.NetworkPolicyFacts{MatchLabels: webLabels}),
		policy("in", data.NetworkPolicyFacts{Expressions: expr("In", "web", "x")}),
		policy("exists", data.NetworkPolicyFacts{Expressions: expr("Exists")}),
		policy("miss-label", data.NetworkPolicyFacts{MatchLabels: data.Labels{{Key: "app", Value: "db"}}}),
		policy("miss-notin", data.NetworkPolicyFacts{Expressions: expr("NotIn", "web")}),
		policy("miss-absent", data.NetworkPolicyFacts{Expressions: expr("DoesNotExist")}),
		policy("odd", data.NetworkPolicyFacts{Expressions: expr("Gt", "1")}),
		foreign,
	)
	p := Access(g, podTarget).Policies
	if !slices.Equal(p.Selecting, []string{"all", "label", "in", "exists"}) || !slices.Equal(p.Uncertain, []string{"odd"}) || !p.Known || p.Note != policyCaveat {
		t.Fatalf("%+v", p)
	}
	text := strings.ToLower(fmt.Sprintf("%+v", p))
	for _, banned := range []string{"bloqueia", "bloqueado", "libera", "permitido"} {
		if strings.Contains(text, banned) {
			t.Fatalf("policies never claim %q", banned)
		}
	}
	g.Policies = Set{State: SetDenied}
	if p = Access(g, podTarget).Policies; p.Known || p.Note != "sem permissão para ler NetworkPolicies" {
		t.Fatalf("%+v", p)
	}
}

func TestReportsAreBounded(t *testing.T) {
	g := healthy()
	g.Ingresses = known(webIngress(func(i *data.IngressFacts) {
		for n := range 200 {
			i.Rules = append(i.Rules, data.IngressRule{Host: fmt.Sprintf("h%d.example.com", n), Paths: i.Rules[0].Paths})
		}
	}))
	r := Access(g, ingressTarget)
	if len(r.Addresses) != MaxAddresses || len(r.Routes) != MaxRoutes || !r.Truncated {
		t.Fatalf("%d %d %v", len(r.Addresses), len(r.Routes), r.Truncated)
	}
}

func TestNodePortNeverClaimsAnOpenPort(t *testing.T) {
	g := healthy()
	g.Ingresses = known()
	g.Services = known(webService(func(s *data.ServiceFacts) { s.Type = "NodePort"; s.Ports[0].NodePort = 30080 }))
	for _, name := range []string{"a", "b", "c"} {
		g.Nodes.Rows = append(g.Nodes.Rows, row("", name, data.NetworkFacts{Node: &data.NodeFacts{Ready: data.TriTrue, External: []string{"203.0.113.10"}}}))
	}
	r := Access(g, serviceTarget)
	if !slices.Contains(r.Notes, "NodePort 30080, outros nodes não listados: 1; porta alocada no node, alcance não verificado") {
		t.Fatalf("%q", r.Notes)
	}
	for _, a := range r.Addresses {
		if a.Origin == OriginNodePort && a.Reach != ReachNodePort {
			t.Fatalf("%+v", a)
		}
	}
}

func TestInternalExternalNameIsOnlyAnAlias(t *testing.T) {
	for _, name := range []string{"db.apps.svc.cluster.local", "db.example.com"} {
		g := healthy()
		g.Services = known(webService(func(s *data.ServiceFacts) { s.Type, s.ExternalName = "ExternalName", name }))
		for _, target := range []Target{serviceTarget, ingressTarget} {
			r := Access(g, target)
			text := fmt.Sprintf("%+v", r)
			if !strings.Contains(text, "alias DNS, destino não verificado") {
				t.Fatalf("%s: %s", name, text)
			}
		}
	}
}

func TestReportsNeverClaimWhatWasNotRead(t *testing.T) {
	banned := []string{"abert", "acessível", "alcançável", "responde", "fora do cluster", "só dentro do cluster", "expost", "expõe", "funciona", "bloquei", "libera", "permitid", "externa declarada"}
	services := []func(*data.ServiceFacts){
		nil,
		func(s *data.ServiceFacts) { s.Type = "NodePort"; s.Ports[0].NodePort = 30080 },
		func(s *data.ServiceFacts) { s.Type = "LoadBalancer" },
		func(s *data.ServiceFacts) {
			s.Type = "LoadBalancer"
			s.LoadBalancer = []data.LoadBalancerAddress{{IP: "203.0.113.4"}, {IP: "10.0.0.4"}, {Hostname: "lb.example.com"}}
		},
		func(s *data.ServiceFacts) { s.Type, s.ExternalName = "ExternalName", "db.apps.svc.cluster.local" },
		func(s *data.ServiceFacts) { s.ExternalIPs = []string{"203.0.113.9"} },
		func(s *data.ServiceFacts) { s.ClusterIPs = []string{"None"} },
		func(s *data.ServiceFacts) { s.Selector = nil },
		func(s *data.ServiceFacts) { s.PublishNotReady = true },
	}
	slicesSets := []Set{known(webSlice(2, 2)), known(webSlice(2, 0)), known(), {State: SetDenied}, {State: SetAbsent}, {}}
	podSets := []Set{known(webPod("web-1", data.TriTrue)), known(webPod("web-1", data.TriFalse)), known(), {State: SetDenied}}
	nodes := known()
	for _, name := range []string{"a", "b", "c"} {
		nodes.Rows = append(nodes.Rows, row("", name, data.NetworkFacts{Node: &data.NodeFacts{Ready: data.TriTrue, Internal: []string{"10.0.0.1"}}}))
	}
	for _, service := range services {
		for _, eps := range slicesSets {
			for _, pods := range podSets {
				g := healthy()
				g.Services, g.Slices, g.Pods, g.Nodes = known(webService(service)), eps, pods, nodes
				g.Policies = known(row("apps", "deny", data.NetworkFacts{Policy: &data.NetworkPolicyFacts{}}))
				for _, target := range []Target{serviceTarget, ingressTarget, podTarget} {
					r := Access(g, target)
					text := strings.ToLower(fmt.Sprintf("%+v", r))
					for _, route := range r.Routes {
						text += strings.ToLower(route.Verdict())
					}
					for _, word := range banned {
						if strings.Contains(text, word) {
							t.Fatalf("report claims %q: %s", word, text)
						}
					}
				}
			}
		}
	}
}

func TestReportsNeverAdoptAnObjectWithAnotherUID(t *testing.T) {
	for _, target := range []Target{podTarget, serviceTarget, ingressTarget} {
		target.UID = "apps" + target.Name
		if r := Access(healthy(), target); len(r.Addresses) == 0 {
			t.Fatalf("the original is reported: %+v", target)
		}
		target.UID = "another"
		r := Access(healthy(), target)
		if len(r.Addresses) != 0 || len(r.Routes) != 0 || len(r.Policies.Selecting) != 0 || len(r.Notes) == 0 {
			t.Fatalf("a replacement is never reported as the original: %+v", r)
		}
	}
}

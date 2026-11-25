package data

import (
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const demoAppLabel = "app.kubernetes.io/name"

var demoNetworkResources = []struct {
	gvr        schema.GroupVersionResource
	kind       string
	namespaced bool
}{
	{schema.GroupVersionResource{Version: "v1", Resource: "services"}, "Service", true},
	{schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}, "EndpointSlice", true},
	{schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}, "Ingress", true},
	{schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingressclasses"}, "IngressClass", false},
	{schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, "NetworkPolicy", true},
	{schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, "Node", false},
}

type demoService struct {
	ns, name, typ, ip string
	app               string
	ports             []ServicePort
	lb                []LoadBalancerAddress
}

var demoServices = []demoService{
	{"bilibili-raffle", "bilibili-live", "ClusterIP", "10.43.10.11", "bilibili-live", []ServicePort{{Name: "http", Port: 80, Protocol: "TCP", Target: "8080"}}, nil},
	{"bilibili-raffle", "bilibili-raffle", "ClusterIP", "10.43.10.12", "bilibili-raffle", []ServicePort{{Name: "http", Port: 80, Protocol: "TCP", Target: "8080"}}, nil},
	{"lgo", "lgo", "NodePort", "10.43.20.5", "lgo", []ServicePort{{Name: "http", Port: 80, Protocol: "TCP", Target: "8080", NodePort: 30080}}, nil},
	{"ys-odoo", "odoo", "LoadBalancer", "10.43.30.8", "odoo", []ServicePort{{Name: "http", Port: 8069, Protocol: "TCP", Target: "8069", NodePort: 31069}}, nil},
	{"ys-odoo", "psql", "ClusterIP", "10.43.30.9", "psql", []ServicePort{{Name: "postgres", Port: 5432, Protocol: "TCP", Target: "5432"}}, nil},
	{"xcx", "qiandao", "ClusterIP", "10.43.40.2", "qiandao", []ServicePort{{Name: "http", Port: 80, Protocol: "TCP", Target: "http"}}, nil},
	{"kube-system", "kube-dns", "ClusterIP", "10.43.0.10", "coredns", []ServicePort{{Name: "dns", Port: 53, Protocol: "UDP", Target: "53"}}, nil},
	{"kube-system", "traefik", "LoadBalancer", "10.43.0.80", "traefik", []ServicePort{{Name: "web", Port: 80, Protocol: "TCP", Target: "web", NodePort: 32080, AppProtocol: "http"}, {Name: "websecure", Port: 443, Protocol: "TCP", Target: "websecure", NodePort: 32443, AppProtocol: "https"}}, []LoadBalancerAddress{{IP: demoNodeAddress}}},
}

func demoIngress(host, path, service, port string, tls ...IngressTLS) *IngressFacts {
	backend := IngressBackend{Service: service, PortName: port}
	return &IngressFacts{Class: "traefik", Rules: []IngressRule{{Host: host, Paths: []IngressPath{{Path: path, Type: "Prefix", Backend: backend}}}}, TLS: tls, Addresses: []LoadBalancerAddress{{IP: demoNodeAddress}}}
}

func demoApp(name string) string {
	for _, d := range demoDeployments {
		if strings.HasPrefix(name, d.Name+"-") {
			return d.Name
		}
	}
	app, _, _ := strings.Cut(name, "-")
	return app
}

func demoPodFacts(p Pod) *PodFacts {
	phase, ready := "Running", TriFalse
	switch p.Status {
	case StatusRunning:
		ready = TriOf(p.Containers > 0 && p.Ready == p.Containers)
	case StatusCompleted:
		phase = "Succeeded"
	case StatusPending, StatusContainerCreating:
		phase = "Pending"
	case StatusFailed:
		phase = "Failed"
	}
	return &PodFacts{Labels: Labels{{Key: demoAppLabel, Value: demoApp(p.Name)}}, Phase: phase, Ready: ready}
}

func (d *Demo) demoNetworkRows(resource string, at time.Time) ([]ResourceRow, bool) {
	var out []ResourceRow
	add := func(ns, name string, f NetworkFacts) {
		if ns == "" || d.namespace == "" || ns == d.namespace {
			out = append(out, ResourceRow{Ref: ResourceMetadataRef{Namespace: ns, Name: name, UID: "demo-" + resource + "-" + ns + "/" + name, ResourceVersion: "1", CreatedAt: at.Add(-72 * time.Hour)}, Network: &f})
		}
	}
	switch resource {
	case "services":
		for _, s := range demoServices {
			add(s.ns, s.name, NetworkFacts{Service: &ServiceFacts{Type: s.typ, ClusterIPs: []string{s.ip}, Selector: Labels{{Key: demoAppLabel, Value: s.app}}, Ports: s.ports, LoadBalancer: s.lb}})
		}
	case "endpointslices":
		for _, s := range demoServices {
			add(s.ns, s.name+"-7xk2p", NetworkFacts{Slice: d.demoSlice(s)})
		}
	case "ingresses":
		add("bilibili-raffle", "live", NetworkFacts{Ingress: demoIngress("live.example.com", "/", "bilibili-live", "http", IngressTLS{Hosts: []string{"live.example.com"}, Secret: "live-tls"})})
		add("lgo", "lgo", NetworkFacts{Ingress: demoIngress("lgo.example.com", "/", "lgo", "http")})
		add("xcx", "qiandao", NetworkFacts{Ingress: demoIngress("qd.example.com", "/", "qiandao", "http")})
		add("ys-odoo", "odoo", NetworkFacts{Ingress: demoIngress("*.odoo.example.com", "/", "odoo", "http", IngressTLS{Hosts: []string{"*.odoo.example.com"}})})
		add("ys-odoo", "erp", NetworkFacts{Ingress: demoIngress("erp.example.com", "/erp", "erp", "http")})
	case "ingressclasses":
		add("", "traefik", NetworkFacts{Class: &IngressClassFacts{Controller: "traefik.io/ingress-controller", Default: true}})
	case "networkpolicies":
		add("ys-odoo", "psql-from-odoo", NetworkFacts{Policy: &NetworkPolicyFacts{MatchLabels: Labels{{Key: demoAppLabel, Value: "psql"}}, Types: []string{"Ingress"}, IngressRules: 1}})
		add("bilibili-raffle", "default-deny", NetworkFacts{Policy: &NetworkPolicyFacts{Types: []string{"Ingress", "Egress"}}})
	case "nodes":
		add("", demoNode, NetworkFacts{Node: &NodeFacts{Ready: TriTrue, Internal: []string{demoNodeAddress}}})
	default:
		return nil, false
	}
	return out, true
}

func (d *Demo) demoSlice(s demoService) *SliceFacts {
	out := &SliceFacts{Service: s.name, AddressType: "IPv4"}
	for _, p := range s.ports {
		out.Ports = append(out.Ports, SlicePort{Name: p.Name, Port: p.Port, Protocol: p.Protocol})
	}
	for _, p := range d.pods {
		f := demoPodFacts(p.pod)
		if p.pod.Namespace != s.ns || demoApp(p.pod.Name) != s.app || f.Phase != "Running" {
			continue
		}
		out.Total++
		if f.Ready == TriTrue {
			out.Ready++
		}
		out.Endpoints = append(out.Endpoints, SliceEndpoint{Addresses: []string{p.pod.IP}, Ready: f.Ready, TargetKind: "Pod", TargetName: p.pod.Name, Node: p.pod.Node})
	}
	return out
}

func (d *Demo) addDemoFacts(rows []ResourceRow, kind WorkloadKind) {
	for i := range rows {
		if kind != WorkloadPod {
			rows[i].Network = &NetworkFacts{Template: &TemplateFacts{Labels: Labels{{Key: demoAppLabel, Value: rows[i].Ref.Name}}}}
			continue
		}
		if at := d.index(PodKey(rows[i].Ref.Namespace, rows[i].Ref.Name)); at >= 0 {
			rows[i].Network = &NetworkFacts{Pod: demoPodFacts(d.pods[at].pod)}
		}
	}
}

func (h *demoResourceHandle) Search(query string, limit int) ResourcePage {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.page
	out.Items, out.Filtered = nil, true
	for _, row := range h.page.Items {
		if !row.Matches(query) {
			continue
		}
		out.Matched++
		if len(out.Items) < limit {
			out.Items = append(out.Items, row)
		}
	}
	return out
}

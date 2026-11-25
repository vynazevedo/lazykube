package kube

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/netpath"
)

func TestAccessReadsOnlyTheKubernetesAPIAndNeverSecrets(t *testing.T) {
	var canaryHits atomic.Int32
	canary := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { canaryHits.Add(1) }))
	defer canary.Close()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(canary.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	meta := func(name string, labels map[string]any) map[string]any {
		return map[string]any{"name": name, "namespace": "apps", "uid": "u-" + name, "resourceVersion": "5", "labels": labels}
	}
	app := map[string]any{"app": "web"}
	lists := map[string]map[string]any{
		"/api/v1/namespaces/apps/services":                           {"kind": "ServiceList", "apiVersion": "v1", "items": []any{map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": meta("web", nil), "spec": map[string]any{"type": "LoadBalancer", "clusterIP": host, "selector": app, "ports": []any{map[string]any{"name": "http", "port": json.Number(port), "protocol": "TCP"}}}, "status": map[string]any{"loadBalancer": map[string]any{"ingress": []any{map[string]any{"ip": host}, map[string]any{"hostname": "localhost"}}}}}}},
		"/apis/discovery.k8s.io/v1/namespaces/apps/endpointslices":   {"kind": "EndpointSliceList", "apiVersion": "discovery.k8s.io/v1", "items": []any{map[string]any{"apiVersion": "discovery.k8s.io/v1", "kind": "EndpointSlice", "metadata": meta("web-x", map[string]any{"kubernetes.io/service-name": "web"}), "addressType": "IPv4", "ports": []any{map[string]any{"name": "http", "port": json.Number(port)}}, "endpoints": []any{map[string]any{"addresses": []any{host}}}}}},
		"/apis/networking.k8s.io/v1/namespaces/apps/ingresses":       {"kind": "IngressList", "apiVersion": "networking.k8s.io/v1", "items": []any{map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "Ingress", "metadata": meta("web", nil), "spec": map[string]any{"ingressClassName": "nginx", "tls": []any{map[string]any{"hosts": []any{"web.example.com"}, "secretName": "web-tls"}}, "rules": []any{map[string]any{"host": "web.example.com", "http": map[string]any{"paths": []any{map[string]any{"path": "/", "pathType": "Prefix", "backend": map[string]any{"service": map[string]any{"name": "web", "port": map[string]any{"name": "http"}}}}}}}}}}}},
		"/apis/networking.k8s.io/v1/ingressclasses":                  {"kind": "IngressClassList", "apiVersion": "networking.k8s.io/v1", "items": []any{}},
		"/apis/networking.k8s.io/v1/namespaces/apps/networkpolicies": {"kind": "NetworkPolicyList", "apiVersion": "networking.k8s.io/v1", "items": []any{}},
	}
	var mu sync.Mutex
	var requests []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		list, ok := lists[r.URL.Path]
		if !ok || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		list["metadata"] = map[string]any{"resourceVersion": "9"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	}))
	defer api.Close()

	s, _, key := resourcePoolFixture(t)
	client, err := newResourceClient(&rest.Config{Host: api.URL})
	if err != nil {
		t.Fatal(err)
	}
	s.resources.client = client
	key.ProjectionVersion = data.ResourceNetworkProjection
	read := func(gvr schema.GroupVersionResource, namespaced bool) netpath.Set {
		k := key
		k.GVR = gvr
		if !namespaced {
			k.Namespace = ""
		}
		h, err := s.resources.subscribe(t.Context(), k, data.ResourceDescriptor{GVR: gvr, Namespaced: namespaced, List: true, Availability: data.ResourceServed})
		if err != nil {
			t.Fatal(err)
		}
		defer h.Cancel()
		page := resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageListOnly })
		return netpath.Set{Rows: page.Items, State: netpath.SetKnown}
	}
	g := netpath.Graph{
		Services:  read(servicesGVR, true),
		Slices:    read(schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}, true),
		Ingresses: read(ingressGVR, true),
		Classes:   read(schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingressclasses"}, false),
		Policies:  read(schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, true),
	}
	report := netpath.Access(g, netpath.Target{Kind: netpath.TargetService, Namespace: "apps", Name: "web"})
	if len(report.Addresses) == 0 || len(report.Routes) == 0 {
		t.Fatalf("%+v", report)
	}
	shown := fmt.Sprintf("%+v", report)
	if !strings.Contains(shown, host) || !strings.Contains(shown, "web-tls") {
		t.Fatal("the report must be about the observed objects")
	}
	for _, a := range report.Addresses {
		if a.URL.Host() == host || a.URL.Host() == "localhost" {
			t.Fatalf("a loopback address never becomes a URL: %+v", a)
		}
	}
	if canaryHits.Load() != 0 {
		t.Fatalf("the derived address was contacted %d times", canaryHits.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	for _, r := range requests {
		path := strings.TrimPrefix(r, "GET ")
		if _, ok := lists[path]; !ok || path == r {
			t.Errorf("unexpected request %s", r)
		}
		if strings.Contains(r, "secret") {
			t.Errorf("secrets were read: %s", r)
		}
	}
}

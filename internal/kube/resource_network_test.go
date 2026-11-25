package kube

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/vynazevedo/lazykube/internal/data"
)

var (
	servicesGVR = schema.GroupVersionResource{Version: "v1", Resource: "services"}
	ingressGVR  = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
)

func object(kind, api string, body map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": api, "kind": kind, "metadata": map[string]any{"name": "web", "namespace": "apps", "uid": "u1", "resourceVersion": "7"}}}
	for k, v := range body {
		u.Object[k] = v
	}
	return u
}

func TestNetworkFactsProjectEachKind(t *testing.T) {
	annotated := object("IngressClass", "networking.k8s.io/v1", map[string]any{"spec": map[string]any{"controller": "example.com/ctl"}})
	annotated.SetAnnotations(map[string]string{defaultClassMarker: "true"})
	sliceObj := object("EndpointSlice", "discovery.k8s.io/v1", map[string]any{"addressType": "IPv4", "ports": []any{map[string]any{"name": "http", "port": int64(8080), "protocol": "TCP"}}, "endpoints": []any{
		map[string]any{"addresses": []any{"10.0.0.1"}, "conditions": map[string]any{"ready": true}, "targetRef": map[string]any{"kind": "Pod", "name": "web-1"}, "nodeName": "n1"},
		map[string]any{"addresses": []any{"10.0.0.2"}, "conditions": map[string]any{"ready": false, "terminating": true}},
		map[string]any{"addresses": []any{"10.0.0.3"}},
	}})
	sliceObj.SetLabels(map[string]string{sliceServiceLabel: "web"})
	legacy := object("Ingress", "networking.k8s.io/v1", map[string]any{"spec": map[string]any{"ingressClassName": "nginx", "defaultBackend": map[string]any{"resource": map[string]any{"kind": "Bucket", "name": "static"}},
		"rules": []any{map[string]any{"host": "web.example.com", "http": map[string]any{"paths": []any{map[string]any{"path": "/api", "pathType": "Prefix", "backend": map[string]any{"service": map[string]any{"name": "web", "port": map[string]any{"name": "http"}}}}}}}},
		"tls":   []any{map[string]any{"hosts": []any{"web.example.com"}, "secretName": "web-tls"}}},
		"status": map[string]any{"loadBalancer": map[string]any{"ingress": []any{map[string]any{"ip": "203.0.113.7"}}}}})
	legacy.SetAnnotations(map[string]string{legacyClassMarker: "old"})
	for _, tc := range []struct {
		name string
		gvr  schema.GroupVersionResource
		obj  any
		want data.NetworkFacts
	}{
		{"service", servicesGVR, object("Service", "v1", map[string]any{"spec": map[string]any{"type": "LoadBalancer", "clusterIP": "10.1.2.3", "selector": map[string]any{"b": "2", "a": "1"}, "ports": []any{map[string]any{"name": "http", "port": int64(80), "targetPort": "web", "nodePort": int64(30080), "protocol": "TCP", "appProtocol": "http"}}}, "status": map[string]any{"loadBalancer": map[string]any{"ingress": []any{map[string]any{"hostname": "lb.example.com"}}}}}),
			data.NetworkFacts{Service: &data.ServiceFacts{Type: "LoadBalancer", ClusterIPs: []string{"10.1.2.3"}, Selector: data.Labels{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}, Ports: []data.ServicePort{{Name: "http", Port: 80, Protocol: "TCP", Target: "web", NodePort: 30080, AppProtocol: "http"}}, LoadBalancer: []data.LoadBalancerAddress{{Hostname: "lb.example.com"}}}}},
		{"slice", schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}, sliceObj,
			data.NetworkFacts{Slice: &data.SliceFacts{Service: "web", AddressType: "IPv4", Ports: []data.SlicePort{{Name: "http", Port: 8080, Protocol: "TCP"}}, Total: 3, Ready: 2, Endpoints: []data.SliceEndpoint{
				{Addresses: []string{"10.0.0.1"}, Ready: data.TriTrue, TargetKind: "Pod", TargetName: "web-1", Node: "n1"},
				{Addresses: []string{"10.0.0.2"}, Ready: data.TriFalse, Terminating: data.TriTrue},
				{Addresses: []string{"10.0.0.3"}},
			}}}},
		{"ingress", ingressGVR, legacy,
			data.NetworkFacts{Ingress: &data.IngressFacts{Class: "nginx", ClassAnnotation: "old", Default: &data.IngressBackend{Resource: "Bucket/static"}, Rules: []data.IngressRule{{Host: "web.example.com", Paths: []data.IngressPath{{Path: "/api", Type: "Prefix", Backend: data.IngressBackend{Service: "web", PortName: "http"}}}}}, TLS: []data.IngressTLS{{Hosts: []string{"web.example.com"}, Secret: "web-tls"}}, Addresses: []data.LoadBalancerAddress{{IP: "203.0.113.7"}}}}},
		{"class", schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingressclasses"}, annotated,
			data.NetworkFacts{Class: &data.IngressClassFacts{Controller: "example.com/ctl", Default: true}}},
		{"policy", schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, object("NetworkPolicy", "networking.k8s.io/v1", map[string]any{"spec": map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app": "web"}, "matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"a", "b"}}}}, "policyTypes": []any{"Ingress"}, "ingress": []any{map[string]any{}}}}),
			data.NetworkFacts{Policy: &data.NetworkPolicyFacts{MatchLabels: data.Labels{{Key: "app", Value: "web"}}, Expressions: []data.SelectorRequirement{{Key: "tier", Operator: "In", Values: []string{"a", "b"}}}, Types: []string{"Ingress"}, IngressRules: 1}}},
		{"httproute", schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}, object("HTTPRoute", "gateway.networking.k8s.io/v1", map[string]any{"spec": map[string]any{"hostnames": []any{"r.example.com"}, "parentRefs": []any{map[string]any{"name": "gw"}}, "rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "web", "port": int64(80)}}}}}, "status": map[string]any{"parents": []any{map[string]any{"conditions": []any{map[string]any{"type": "Accepted", "status": "True"}}}}}}),
			data.NetworkFacts{Route: &data.HTTPRouteFacts{Hostnames: []string{"r.example.com"}, Parents: []string{"gw"}, Backends: []string{"web:80"}, Rules: 1, Accepted: data.TriTrue}}},
		{"pod", schema.GroupVersionResource{Version: "v1", Resource: "pods"}, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}}},
			data.NetworkFacts{Pod: &data.PodFacts{Labels: data.Labels{{Key: "app", Value: "web"}}, Phase: "Running", Ready: data.TriFalse}}},
		{"node", schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, &corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}, Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.9"}, {Type: corev1.NodeExternalIP, Address: "203.0.113.9"}, {Type: corev1.NodeHostName, Address: "n1"}}}},
			data.NetworkFacts{Node: &data.NodeFacts{Ready: data.TriTrue, Internal: []string{"10.0.0.9"}, External: []string{"203.0.113.9"}}}},
		{"statefulset", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, object("StatefulSet", "apps/v1", map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "db"}}}}}),
			data.NetworkFacts{Template: &data.TemplateFacts{Labels: data.Labels{{Key: "app", Value: "db"}}}}},
		{"cronjob", schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, object("CronJob", "batch/v1", map[string]any{"spec": map[string]any{"jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "c"}}}}}}}),
			data.NetworkFacts{Template: &data.TemplateFacts{Labels: data.Labels{{Key: "app", Value: "c"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := networkFacts(tc.obj, tc.gvr)
			if got.Bytes <= 0 || got.Truncated {
				t.Fatalf("bytes=%d truncated=%v", got.Bytes, got.Truncated)
			}
			got.Bytes = 0
			if !reflect.DeepEqual(*got, tc.want) {
				t.Fatalf("got %s\nwant %s", dump(*got), dump(tc.want))
			}
		})
	}
}

func dump(f data.NetworkFacts) string {
	return fmt.Sprintf("%+v %+v %+v %+v %+v %+v %+v %+v %+v", f.Service, f.Slice, f.Ingress, f.Class, f.Policy, f.Route, f.Pod, f.Node, f.Template)
}

func TestNetworkFactsStayBoundedAndNeverClipIntoAnotherAddress(t *testing.T) {
	long := strings.Repeat("a", 400) + ".example.com"
	ing := object("Ingress", "networking.k8s.io/v1", map[string]any{"spec": map[string]any{"rules": []any{
		map[string]any{"host": long},
		map[string]any{"host": "ok.example.com"},
	}}})
	f := networkFacts(ing, ingressGVR)
	if !f.Truncated || len(f.Ingress.Rules) != 1 || f.Ingress.Rules[0].Host != "ok.example.com" {
		t.Fatalf("an overlong host drops the rule instead of becoming another host: %+v", f.Ingress)
	}
	var rules []any
	for i := range 400 {
		rules = append(rules, map[string]any{"host": fmt.Sprintf("h%d.%s", i, strings.Repeat("b", 60)), "http": map[string]any{"paths": []any{map[string]any{"path": "/" + strings.Repeat("p", 900), "backend": map[string]any{"service": map[string]any{"name": "s"}}}}}})
	}
	f = networkFacts(object("Ingress", "networking.k8s.io/v1", map[string]any{"spec": map[string]any{"rules": rules}}), ingressGVR)
	if !f.Truncated || f.Ingress != nil || f.Bytes > factRowLimit {
		t.Fatalf("an oversized object keeps only the truncated marker: %+v", f)
	}
	if f := networkFacts(object("Service", "v1", map[string]any{"spec": "broken"}), servicesGVR); f == nil || !f.Truncated || f.Service != nil {
		t.Fatalf("a malformed object never invents facts: %+v", f)
	}
}

func TestNetworkProjectionIsOptInAndSecretsStayRefused(t *testing.T) {
	s, _, key := resourcePoolFixture(t)
	s.resources.client = dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{servicesGVR: "ServiceList"}, object("Service", "v1", map[string]any{"spec": map[string]any{"type": "ClusterIP", "clusterIP": "10.1.1.1"}}))
	key.GVR = servicesGVR
	for version, want := range map[int]bool{data.ResourceProjectionVersion: false, data.ResourceNetworkProjection: true} {
		key.ProjectionVersion = version
		h := resourceSubscribe(t, s, key, true, false)
		page := resourceWait(t, h, func(p data.ResourcePage) bool { return len(p.Items) == 1 })
		if (page.Items[0].Network != nil) != want {
			t.Fatalf("projection %d facts=%v", version, page.Items[0].Network)
		}
		h.Cancel()
	}
	if s.resources.factBytes != 0 || s.resources.rows != 0 {
		t.Fatalf("cancel returns the budget: rows=%d bytes=%d", s.resources.rows, s.resources.factBytes)
	}
	key.GVR = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	for _, version := range []int{data.ResourceProjectionVersion, data.ResourceNetworkProjection, 3} {
		key.ProjectionVersion = version
		if h, err := s.resources.subscribe(t.Context(), key, data.ResourceDescriptor{GVR: key.GVR, Namespaced: true, List: true, Availability: data.ResourceServed}); err == nil || h != nil {
			t.Fatalf("secrets are refused in projection %d", version)
		}
	}
}

func TestNetworkFactBudgetAndSearch(t *testing.T) {
	s, _, key := resourcePoolFixture(t)
	var objects []runtime.Object
	for i := range 20 {
		u := object("Service", "v1", map[string]any{"spec": map[string]any{"type": "ClusterIP", "clusterIP": fmt.Sprintf("10.1.1.%d", i)}})
		u.SetName(fmt.Sprintf("svc-%02d", i))
		u.SetUID("u")
		objects = append(objects, u)
	}
	s.resources.client = dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{servicesGVR: "ServiceList"}, objects...)
	key.GVR, key.ProjectionVersion = servicesGVR, data.ResourceNetworkProjection
	h := resourceSubscribe(t, s, key, true, false)
	page := resourceWait(t, h, func(p data.ResourcePage) bool { return len(p.Items) == 20 })
	if page.Filtered {
		t.Fatal("a plain page is not filtered")
	}
	found := h.(data.ResourceSearcher).Search("SVC-1 clusterip", 3)
	if !found.Filtered || found.Matched != 10 || len(found.Items) != 3 || found.Total != 20 || found.Items[0].Ref.Name != "svc-10" {
		t.Fatalf("search = %+v", found)
	}
	if got := h.(data.ResourceSearcher).Search("10.1.1.7", 10); got.Matched != 1 || got.Items[0].Ref.Name != "svc-07" {
		t.Fatalf("search by fact = %+v", got)
	}
	h.Cancel()
	if got := h.(data.ResourceSearcher).Search("", 10); got.Coverage.State != data.CoverageUnavailable {
		t.Fatal("a canceled handle finds nothing")
	}
	s.resources.factLimit = 3 * factBytes(page.Items[0])
	h = resourceSubscribe(t, s, key, true, false)
	page = resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoveragePartial })
	if !page.Truncated || len(page.Items) == 20 {
		t.Fatalf("the byte budget stops the inventory and says so: %+v", page.Coverage)
	}
	h.Cancel()
}

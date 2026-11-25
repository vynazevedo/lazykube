package kube

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
)

type hostileFacts struct {
	name   string
	gvr    schema.GroupVersionResource
	object any
}

func repeated(n int, item func(int) any) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = item(i)
	}
	return out
}

func labelMap(n, size int) map[string]string {
	out := make(map[string]string, n)
	for i := range n {
		out[fmt.Sprintf("k%d", i)] = strings.Repeat("v", size)
	}
	return out
}

func hostileObjects() []hostileFacts {
	network := func(resource string) schema.GroupVersionResource {
		return schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: resource}
	}
	expressions := func(values int, text string) any {
		return map[string]any{"spec": map[string]any{"podSelector": map[string]any{"matchExpressions": repeated(30, func(i int) any {
			return map[string]any{"key": fmt.Sprintf("k%d", i), "operator": "In", "values": repeated(values, func(int) any { return text })}
		})}, "policyTypes": []any{"Ingress"}}}
	}
	labels := object("EndpointSlice", "discovery.k8s.io/v1", map[string]any{"addressType": "IPv4", "endpoints": repeated(32, func(int) any {
		return map[string]any{"addresses": repeated(9, func(int) any { return "" }), "conditions": map[string]any{}}
	})})
	labels.SetLabels(map[string]string{sliceServiceLabel: "web"})
	body := func(v any) map[string]any { return v.(map[string]any) }
	return []hostileFacts{
		{"policy empty values", network("networkpolicies"), object("NetworkPolicy", "networking.k8s.io/v1", body(expressions(63, "")))},
		{"policy short values", network("networkpolicies"), object("NetworkPolicy", "networking.k8s.io/v1", body(expressions(17, "x")))},
		{"policy minimal", network("networkpolicies"), object("NetworkPolicy", "networking.k8s.io/v1", map[string]any{"spec": map[string]any{"podSelector": map[string]any{}}})},
		{"service ports", servicesGVR, object("Service", "v1", map[string]any{"spec": map[string]any{"type": "ClusterIP", "ports": repeated(64, func(i int) any { return map[string]any{"name": "a", "port": int64(i + 1), "protocol": "TCP"} })}})},
		{"service addresses", servicesGVR, object("Service", "v1", map[string]any{"spec": map[string]any{"type": "LoadBalancer", "externalIPs": repeated(9, func(int) any { return "1" })}, "status": map[string]any{"loadBalancer": map[string]any{"ingress": repeated(9, func(int) any { return map[string]any{"ip": "1"} })}}})},
		{"service minimal", servicesGVR, object("Service", "v1", map[string]any{"spec": map[string]any{}})},
		{"slice endpoints", schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}, labels},
		{"slice ports", schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}, object("EndpointSlice", "discovery.k8s.io/v1", map[string]any{"ports": repeated(33, func(i int) any { return map[string]any{"name": "p", "port": int64(i + 1)} })})},
		{"ingress rules", ingressGVR, object("Ingress", "networking.k8s.io/v1", map[string]any{"spec": map[string]any{"rules": repeated(33, func(int) any {
			return map[string]any{"host": "a", "http": map[string]any{"paths": repeated(3, func(int) any {
				return map[string]any{"path": "/", "backend": map[string]any{"service": map[string]any{"name": "s", "port": map[string]any{"number": int64(1)}}}}
			})}}
		})}})},
		{"ingress tls", ingressGVR, object("Ingress", "networking.k8s.io/v1", map[string]any{"spec": map[string]any{"tls": repeated(32, func(int) any { return map[string]any{"hosts": repeated(17, func(int) any { return "a" })} })}})},
		{"ingress oversized", ingressGVR, object("Ingress", "networking.k8s.io/v1", map[string]any{"spec": map[string]any{"rules": repeated(64, func(int) any {
			return map[string]any{"host": "a", "http": map[string]any{"paths": repeated(2, func(int) any { return map[string]any{"path": "/" + strings.Repeat("p", 1000)} })}}
		})}})},
		{"class", network("ingressclasses"), object("IngressClass", "networking.k8s.io/v1", map[string]any{"spec": map[string]any{"controller": "c"}})},
		{"httproute", schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}, object("HTTPRoute", "gateway.networking.k8s.io/v1", map[string]any{"spec": map[string]any{
			"hostnames":  repeated(17, func(int) any { return "a" }),
			"parentRefs": repeated(17, func(int) any { return map[string]any{"name": "g"} }),
			"rules": repeated(17, func(int) any {
				return map[string]any{"backendRefs": []any{map[string]any{"name": "b", "port": int64(80)}}}
			}),
		}})},
		{"statefulset", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, object("StatefulSet", "apps/v1", map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "", "b": ""}}}}})},
		{"pod labels", schema.GroupVersionResource{Version: "v1", Resource: "pods"}, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: labelMap(64, 1)}}},
		{"pod long labels", schema.GroupVersionResource{Version: "v1", Resource: "pods"}, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: labelMap(64, 253)}}},
		{"pod minimal", schema.GroupVersionResource{Version: "v1", Resource: "pods"}, &corev1.Pod{}},
		{"node", schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, &corev1.Node{Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "1"}, {Type: corev1.NodeInternalIP, Address: "2"}, {Type: corev1.NodeInternalIP, Address: "3"}, {Type: corev1.NodeExternalIP, Address: "4"}}}}},
	}
}

func TestAllocationSizeCoversTheRuntimeSizeClasses(t *testing.T) {
	for n := 1; n <= 40000; n++ {
		if real := cap(append([]byte(nil), make([]byte, n)...)); allocationSize(n) < real {
			t.Fatalf("%d bytes allocate %d and are charged %d", n, real, allocationSize(n))
		}
	}
	if allocationSize(0) != 0 {
		t.Fatal("nothing allocated is charged nothing")
	}
}

func independentSize(v reflect.Value) int {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return 0
		}
		return int(v.Elem().Type().Size()) + independentSize(v.Elem())
	case reflect.Struct:
		n := 0
		for i := range v.NumField() {
			n += independentSize(v.Field(i))
		}
		return n
	case reflect.Slice:
		n := v.Cap() * int(v.Type().Elem().Size())
		for i := range v.Len() {
			n += independentSize(v.Index(i))
		}
		return n
	case reflect.String:
		return v.Len()
	case reflect.Map, reflect.Interface, reflect.Chan, reflect.Func, reflect.Array, reflect.UnsafePointer:
		panic("fact type not covered by the accounting: " + v.Type().String())
	}
	return 0
}

func TestEveryFactIsChargedAtLeastWhatItRetains(t *testing.T) {
	for _, tc := range hostileObjects() {
		t.Run(tc.name, func(t *testing.T) {
			f := networkFacts(tc.object, tc.gvr)
			if reached := independentSize(reflect.ValueOf(f)); f.Bytes < reached {
				t.Fatalf("charged %d for %d reachable bytes", f.Bytes, reached)
			}
			if f.Bytes > factRowLimit {
				t.Fatalf("a row of %d bytes is above the row limit", f.Bytes)
			}
		})
	}
}

func hostileEntry(t *testing.T, gvr schema.GroupVersionResource) (*Source, *resourceEntry) {
	s, _, key := resourcePoolFixture(t)
	key.GVR, key.ProjectionVersion = gvr, data.ResourceNetworkProjection
	s.resources.rowLimit = 200000
	s.resources.poolLimit = 200000
	e := &resourceEntry{pool: s.resources, key: key, ctx: t.Context(), rows: make(map[string]data.ResourceRow), handles: make(map[*resourceHandle]struct{})}
	e.key.Namespace = ""
	return s, e
}

func hostileRow(i int, f *data.NetworkFacts) data.ResourceRow {
	return data.ResourceRow{Ref: data.ResourceMetadataRef{Namespace: "apps", Name: fmt.Sprintf("i-%d", i), UID: fmt.Sprint("u", i), ResourceVersion: fmt.Sprint("r", i)}, Network: f}
}

func heapInUse() int {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int(m.HeapAlloc)
}

func TestHostileInventoryRetainsLessHeapThanTheBudget(t *testing.T) {
	for _, tc := range hostileObjects() {
		t.Run(tc.name, func(t *testing.T) {
			s, e := hostileEntry(t, tc.gvr)
			before := heapInUse()
			accepted := 0
			for e.put(hostileRow(accepted, networkFacts(tc.object, tc.gvr)), false) {
				accepted++
			}
			retained := heapInUse() - before
			charged := s.resources.factBytes
			t.Logf("rows=%d charged=%d heap=%d", accepted, charged, retained)
			if accepted == 0 || e.coverage.State != data.CoveragePartial || !e.truncated {
				t.Fatalf("the refusal is explicit: accepted=%d coverage=%+v", accepted, e.coverage)
			}
			if charged > resourceFactBytes || retained > resourceFactBytes {
				t.Fatalf("rows=%d charged=%d heap=%d budget=%d", accepted, charged, retained, resourceFactBytes)
			}
			runtime.KeepAlive(e)
			runtime.KeepAlive(tc)
		})
	}
}

func TestFactBudgetIsReturnedRowByRowAndOnClear(t *testing.T) {
	for _, tc := range hostileObjects() {
		s, e := hostileEntry(t, tc.gvr)
		for i := range 20 {
			if !e.put(hostileRow(i, networkFacts(tc.object, tc.gvr)), false) {
				t.Fatal(tc.name)
			}
		}
		full := s.resources.factBytes
		if !e.put(hostileRow(3, nil), false) || s.resources.factBytes >= full {
			t.Fatalf("%s: replacing a row returns its facts", tc.name)
		}
		for i := range 10 {
			e.put(hostileRow(i, nil), true)
		}
		e.clear(data.CoverageCollecting, "")
		if s.resources.factBytes != 0 || e.factBytes != 0 || s.resources.rows != 0 {
			t.Fatalf("%s: leftovers bytes=%d rows=%d", tc.name, s.resources.factBytes, s.resources.rows)
		}
	}
}

func TestHostilePolicyStaysReadableAndInsideTheBudget(t *testing.T) {
	tc := hostileObjects()[0]
	f := networkFacts(tc.object, tc.gvr)
	if f.Truncated || f.Policy == nil || len(f.Policy.Expressions) != 30 {
		t.Fatal("the fixture must be projected whole to exercise the pool budget")
	}
	s, e := hostileEntry(t, tc.gvr)
	accepted := 0
	for e.put(hostileRow(accepted, networkFacts(tc.object, tc.gvr)), false) {
		accepted++
	}
	retained := accepted * independentSize(reflect.ValueOf(f))
	if accepted == 0 || retained > resourceFactBytes || s.resources.factBytes < retained {
		t.Fatalf("accepted=%d retained=%d charged=%d", accepted, retained, s.resources.factBytes)
	}
}

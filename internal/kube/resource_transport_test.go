package kube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/vynazevedo/lazykube/internal/data"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestResourceHTTPConcurrencyAndCancellation(t *testing.T) {
	var lists, maxLists, watches, maxWatches atomic.Int32
	release := make(chan struct{})
	entered := make(chan struct{}, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("watch") == "true" {
			current := watches.Add(1)
			defer watches.Add(-1)
			for old := maxWatches.Load(); current > old && !maxWatches.CompareAndSwap(old, current); old = maxWatches.Load() {
			}
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		current := lists.Add(1)
		defer lists.Add(-1)
		for old := maxLists.Load(); current > old && !maxLists.CompareAndSwap(old, current); old = maxLists.Load() {
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, `{"apiVersion":"discovery.k8s.io/v1","kind":"EndpointSliceList","metadata":{"resourceVersion":"10"},"items":[]}`)
	}))
	defer server.Close()
	client, err := newResourceClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	s := newSource(fake.NewClientset(), nil, Options{Namespace: "apps"})
	s.initResources(client, nil)
	defer s.Close()
	handles := make([]data.ResourceSubscription, 0, 7)
	for _, selector := range []string{"a=1", "a=2", "a=3", "a=4", "a=5", "a=6", "a=7"} {
		key := data.ResourceSubscriptionKey{Session: s.resourceSession(), Generation: s.generation, GVR: resourceTestGVR, Namespace: "apps", LabelSelector: selector, ProjectionVersion: 1}
		handles = append(handles, resourceSubscribe(t, s, key, true, true))
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("lists not started")
		}
	}
	if lists.Load() != 2 {
		t.Fatal("LIST semaphore not enforced")
	}
	close(release)
	listOnly := 0
	for _, h := range handles {
		page := resourceWait(t, h, func(p data.ResourcePage) bool {
			return p.Coverage.State == data.CoverageComplete || p.Coverage.State == data.CoverageListOnly
		})
		if page.Coverage.State == data.CoverageListOnly {
			listOnly++
		}
	}
	if maxLists.Load() > 2 || maxWatches.Load() > 6 || listOnly != 1 {
		t.Fatal(maxLists.Load(), maxWatches.Load(), listOnly)
	}
	for _, h := range handles {
		h.Cancel()
	}
	deadline := time.Now().Add(5 * time.Second)
	for watches.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if watches.Load() != 0 {
		t.Fatal("watch requests survived cancellation")
	}
}

func TestResourceHTTPResponseLimits(t *testing.T) {
	for _, watchMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "list", true: "watch"}[watchMode], func(t *testing.T) {
			input := `{"apiVersion":"v1","kind":"List","items":[{"metadata":{"name":"` + strings.Repeat("x", resourceObjectLimit) + `"}}]}`
			if watchMode {
				input = `{"type":"ADDED","object":{"metadata":{"name":"` + strings.Repeat("x", resourceObjectLimit) + `"}}}`
			}
			body := &resourceBody{ReadCloser: io.NopCloser(strings.NewReader(input)), watch: watchMode}
			n, err := io.Copy(io.Discard, body)
			if err != errResourceResponseLimit || n > resourceResponseLimit {
				t.Fatal(n, err)
			}
		})
	}
	t.Run("http", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"apiVersion":"v1","kind":"List","items":[{"metadata":{"name":"`+strings.Repeat("x", resourceObjectLimit)+`"}}]}`)
		}))
		defer server.Close()
		c, err := newResourceClient(&rest.Config{Host: server.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Resource(resourceTestGVR).Namespace("apps").List(context.Background(), metav1.ListOptions{Limit: 500})
		if err == nil {
			t.Fatal("oversized object accepted")
		}
	})
}

func TestResourceProjectionBoundedAndSafe(t *testing.T) {
	obj := resourceObject(0)
	obj.SetAnnotations(map[string]string{"credential": "sentinel"})
	obj.Object["data"] = map[string]any{"password": "sentinel"}
	row, ok := projectResource(&obj)
	if !ok {
		t.Fatal("projection rejected")
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 1024 || strings.Contains(string(encoded), "sentinel") {
		t.Fatal("unsafe projection")
	}
	bytes := int(unsafe.Sizeof(row)) + len(row.Ref.Name) + len(row.Ref.Namespace) + len(row.Ref.UID) + len(row.Ref.ResourceVersion)
	if bytes > 1024 {
		t.Fatal("row budget exceeded")
	}
	for _, value := range []string{strings.Repeat("x", 254), "a\x1b[31m", "a\nb"} {
		bad := obj.DeepCopy()
		bad.SetName(value)
		if _, ok := projectResource(bad); ok {
			t.Fatal("invalid identity accepted")
		}
	}
	watchInput := `{"type":"BOOKMARK","object":{"metadata":{"resourceVersion":"1"}}}`
	body := &resourceBody{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat(watchInput, 100000))), watch: true}
	if _, err := io.Copy(io.Discard, body); err != nil {
		t.Fatal("watch limit applied to whole stream", err)
	}
}

func TestResourceNamespaceAndSecretGuards(t *testing.T) {
	s, _, key := resourcePoolFixture(t)
	for _, mutate := range []func(*data.ResourceSubscriptionKey){func(k *data.ResourceSubscriptionKey) { k.Namespace = "" }, func(k *data.ResourceSubscriptionKey) { k.Generation++ }, func(k *data.ResourceSubscriptionKey) { k.GVR.Group = ""; k.GVR.Resource = "nodes/proxy" }, func(k *data.ResourceSubscriptionKey) { k.GVR.Group = ""; k.GVR.Resource = "secrets" }} {
		invalid := key
		mutate(&invalid)
		if _, err := s.resources.subscribe(context.Background(), invalid, data.ResourceDescriptor{GVR: invalid.GVR, Namespaced: true, List: true, Watch: true, Availability: data.ResourceServed}); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}

func TestResourceListCursorLossRelists(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		count := calls.Add(1)
		if count == 1 {
			_, _ = io.WriteString(w, `{"apiVersion":"v1","kind":"List","metadata":{"resourceVersion":"1","continue":"lost"},"items":[]}`)
			return
		}
		if count == 2 {
			w.WriteHeader(410)
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Expired","code":410}`)
			return
		}
		if r.URL.Query().Get("continue") != "" {
			t.Error("expired cursor reused")
		}
		_ = json.NewEncoder(w).Encode(&unstructured.UnstructuredList{Object: map[string]any{"apiVersion": "v1", "kind": "List", "metadata": map[string]any{"resourceVersion": "2"}}})
	}))
	defer server.Close()
	c, err := newResourceClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	s := newSource(fake.NewClientset(), nil, Options{Namespace: "apps"})
	s.initResources(c, nil)
	defer s.Close()
	s.resources.retry = func(int) time.Duration { return time.Millisecond }
	key := data.ResourceSubscriptionKey{Session: s.resourceSession(), Generation: s.generation, GVR: resourceTestGVR, Namespace: "apps", ProjectionVersion: 1}
	h := resourceSubscribe(t, s, key, true, false)
	resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageListOnly })
	if calls.Load() != 3 {
		t.Fatal(calls.Load())
	}
}

func TestResourceDiscoveryCanceledWithSession(t *testing.T) {
	entered := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	s := newSource(fake.NewClientset(), nil, Options{})
	s.initResources(nil, nil)
	s.catalog.factory = func(ctx context.Context) (discovery.DiscoveryInterface, error) {
		return newResourceDiscovery(&rest.Config{Host: server.URL}, ctx)
	}
	done := make(chan data.ResourceCatalogSnapshot, 1)
	go func() { done <- s.Catalog(context.Background(), true) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		s.Close()
		t.Fatal("discovery not started")
	}
	s.Close()
	select {
	case result := <-done:
		if result.Coverage.State == data.CoverageComplete {
			t.Fatal("canceled discovery complete")
		}
	case <-time.After(time.Second):
		t.Fatal("discovery survived session close")
	}
	if s.Catalog(context.Background(), true).Coverage.State != data.CoverageUnavailable {
		t.Fatal("closed session discovery reused")
	}
}

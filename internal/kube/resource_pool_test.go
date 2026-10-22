package kube

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

var resourceTestGVR = schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}

func resourceObject(i int) unstructured.Unstructured {
	u := unstructured.Unstructured{Object: map[string]any{"apiVersion": "discovery.k8s.io/v1", "kind": "EndpointSlice", "metadata": map[string]any{"name": fmt.Sprintf("slice-%05d", i), "namespace": "apps", "uid": fmt.Sprintf("uid-%d", i), "resourceVersion": "1"}, "endpoints": []any{map[string]any{"addresses": []any{"192.0.2.1"}}}}}
	return u
}

func resourcePoolFixture(t testing.TB) (*Source, *dynamicfake.FakeDynamicClient, data.ResourceSubscriptionKey) {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{resourceTestGVR: "EndpointSliceList"})
	s := newSource(fake.NewClientset(), nil, Options{Namespace: "apps", Now: func() time.Time { return fixedNow }})
	s.initResources(client, nil)
	s.resources.retry = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(s.Close)
	key := data.ResourceSubscriptionKey{Session: s.resourceSession(), Generation: s.generation, GVR: resourceTestGVR, Namespace: "apps", ProjectionVersion: 1}
	return s, client, key
}

func resourceSubscribe(t testing.TB, s *Source, key data.ResourceSubscriptionKey, list, watch bool) data.ResourceSubscription {
	t.Helper()
	h, err := s.resources.subscribe(context.Background(), key, data.ResourceDescriptor{GVR: key.GVR, Namespaced: true, List: list, Watch: watch, Availability: data.ResourceServed})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Cancel)
	return h
}

func resourceWait(t testing.TB, h data.ResourceSubscription, condition func(data.ResourcePage) bool) data.ResourcePage {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		page := h.Page("", 500)
		if condition(page) {
			return page
		}
		select {
		case <-h.Changes():
		case <-deadline.C:
			t.Fatalf("resource timeout: %+v", page)
		}
	}
}

func resourceListReactor(client *dynamicfake.FakeDynamicClient, n int) {
	client.PrependReactor("list", "*", func(a clienttesting.Action) (bool, runtime.Object, error) {
		opts := a.(clienttesting.ListActionImpl).GetListOptions()
		offset, _ := strconv.Atoi(opts.Continue)
		page := &unstructured.UnstructuredList{}
		page.SetResourceVersion("10")
		end := min(offset+int(opts.Limit), n)
		if end < n {
			page.SetContinue(strconv.Itoa(end))
		}
		for i := offset; i < end; i++ {
			page.Items = append(page.Items, resourceObject(i))
		}
		return true, page, nil
	})
}

func TestResourcePaginationSharingAndCancel(t *testing.T) {
	s, client, key := resourcePoolFixture(t)
	resourceListReactor(client, 1200)
	stream := watch.NewRaceFreeFake()
	var watches atomic.Int32
	client.PrependWatchReactor("*", func(a clienttesting.Action) (bool, watch.Interface, error) {
		watches.Add(1)
		if a.(clienttesting.WatchAction).GetWatchRestrictions().ResourceVersion != "10" {
			t.Error("list cursor not used")
		}
		return true, stream, nil
	})
	a := resourceSubscribe(t, s, key, true, true)
	b := resourceSubscribe(t, s, key, true, true)
	page := resourceWait(t, a, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageComplete })
	if page.Total != 1200 || len(page.Items) != 500 || page.Continue == "" || watches.Load() != 1 {
		t.Fatal(page.Total, watches.Load())
	}
	next := a.Page(page.Continue, 500)
	if len(next.Items) != 500 || next.Items[0].Ref.Name == page.Items[0].Ref.Name {
		t.Fatal("pagination failed")
	}
	u := resourceObject(1201)
	stream.Add(&u)
	resourceWait(t, b, func(p data.ResourcePage) bool { return p.Total == 1201 })
	if a.Page(page.Continue, 500).Coverage.State != data.CoverageStale {
		t.Fatal("stale cursor accepted")
	}
	a.Cancel()
	if stream.IsStopped() {
		t.Fatal("shared watch stopped too soon")
	}
	b.Cancel()
	deadline := time.Now().Add(time.Second)
	for !stream.IsStopped() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !stream.IsStopped() {
		t.Fatal("watch not canceled")
	}
	s.resources.mu.Lock()
	defer s.resources.mu.Unlock()
	if s.resources.rows != 0 || len(s.resources.entries) != 0 {
		t.Fatal("store retained after cancel")
	}
}

func TestResourceAccessModes(t *testing.T) {
	for _, mode := range []string{"forbidden", "watch-only", "list-only"} {
		t.Run(mode, func(t *testing.T) {
			s, client, key := resourcePoolFixture(t)
			resourceListReactor(client, 1)
			stream := watch.NewRaceFreeFake()
			denied := apierrors.NewForbidden(resourceTestGVR.GroupResource(), "", errors.New("sentinel"))
			if mode != "list-only" {
				client.PrependReactor("list", "*", func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, denied })
			}
			client.PrependWatchReactor("*", func(clienttesting.Action) (bool, watch.Interface, error) {
				if mode == "watch-only" {
					return true, stream, nil
				}
				return true, nil, denied
			})
			h := resourceSubscribe(t, s, key, true, true)
			want := data.CoverageDenied
			if mode == "list-only" {
				want = data.CoverageListOnly
			}
			if mode == "watch-only" {
				want = data.CoveragePartial
			}
			page := resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == want })
			if mode == "watch-only" {
				u := resourceObject(2)
				stream.Add(&u)
				page = resourceWait(t, h, func(p data.ResourcePage) bool { return p.Total == 1 })
				if page.Coverage.State != data.CoveragePartial {
					t.Fatal("watch-only complete")
				}
			}
			if mode == "list-only" && page.Total != 1 {
				t.Fatal("list lost")
			}
		})
	}
}

func TestResourceRelistAfter410AndUIDReplacement(t *testing.T) {
	s, client, key := resourcePoolFixture(t)
	resourceListReactor(client, 1)
	first, second := watch.NewRaceFreeFake(), watch.NewRaceFreeFake()
	var watches atomic.Int32
	client.PrependWatchReactor("*", func(clienttesting.Action) (bool, watch.Interface, error) {
		if watches.Add(1) == 1 {
			return true, first, nil
		}
		return true, second, nil
	})
	h := resourceSubscribe(t, s, key, true, true)
	resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageComplete })
	first.Error(&metav1.Status{Status: "Failure", Reason: metav1.StatusReasonExpired, Code: 410})
	resourceWait(t, h, func(p data.ResourcePage) bool {
		return watches.Load() == 2 && p.Coverage.State == data.CoverageComplete
	})
	old := resourceObject(0)
	replacement := old.DeepCopy()
	replacement.SetUID("new-uid")
	replacement.SetResourceVersion("12")
	second.Modify(replacement)
	resourceWait(t, h, func(p data.ResourcePage) bool { return len(p.Items) == 1 && p.Items[0].Ref.UID == "new-uid" })
	second.Delete(&old)
	time.Sleep(10 * time.Millisecond)
	if h.Page("", 500).Total != 1 {
		t.Fatal("old UID removed replacement")
	}
}

func TestResourceStoreLimitsAreReal(t *testing.T) {
	s, client, key := resourcePoolFixture(t)
	s.resources.rowLimit = 3
	resourceListReactor(client, 20)
	var watches atomic.Int32
	client.PrependWatchReactor("*", func(clienttesting.Action) (bool, watch.Interface, error) {
		watches.Add(1)
		return true, watch.NewRaceFreeFake(), nil
	})
	h := resourceSubscribe(t, s, key, true, true)
	page := resourceWait(t, h, func(p data.ResourcePage) bool { return p.Truncated })
	if page.Total != 3 || page.Coverage.State != data.CoveragePartial || watches.Load() != 0 {
		t.Fatal(page)
	}
	s.resources.mu.Lock()
	defer s.resources.mu.Unlock()
	if len(h.(*resourceHandle).entry.rows) != 3 || s.resources.rows != 3 {
		t.Fatal("unbounded store")
	}
}

func TestResourcePodsReuseExistingCache(t *testing.T) {
	s, client, key := resourcePoolFixture(t)
	s.podInf = cache.NewSharedIndexInformer(&cache.ListWatch{}, &corev1.Pod{}, 0, cache.Indexers{})
	s.podCoverage = data.Coverage{State: data.CoverageComplete}
	for i := range 10000 {
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pod-%05d", i), Namespace: "apps", UID: types.UID(fmt.Sprintf("p-%d", i)), ResourceVersion: "1"}}
		if err := s.podInf.GetStore().Add(p); err != nil {
			t.Fatal(err)
		}
	}
	key.GVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	h := resourceSubscribe(t, s, key, true, true)
	page := resourceWait(t, h, func(p data.ResourcePage) bool { return p.Total == 10000 })
	if !page.Borrowed || page.Coverage.State != data.CoverageComplete || len(client.Actions()) != 0 {
		t.Fatal("pod cache not reused")
	}
}

func TestResourceGlobalBudgetAndSessionIsolation(t *testing.T) {
	s, client, key := resourcePoolFixture(t)
	s.resources.poolLimit = 3
	resourceListReactor(client, 2)
	a := resourceSubscribe(t, s, key, true, false)
	resourceWait(t, a, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageListOnly })
	key.LabelSelector = "!unused"
	b := resourceSubscribe(t, s, key, true, false)
	resourceWait(t, b, func(p data.ResourcePage) bool { return p.Truncated })
	s.resources.mu.Lock()
	count := s.resources.rows
	s.resources.mu.Unlock()
	if count != 3 {
		t.Fatal("global store cap ignored", count)
	}
	s.resources.close()
	if a.Page("", 500).Coverage.State != data.CoverageUnavailable || b.Page("", 500).Total != 0 {
		t.Fatal("session retained live data")
	}
	if _, err := s.resources.subscribe(context.Background(), key, data.ResourceDescriptor{GVR: key.GVR, Namespaced: true, Availability: data.ResourceServed}); err == nil {
		t.Fatal("closed session reopened")
	}
}

func TestResourceUnsupportedAndRepeatedCursor(t *testing.T) {
	s, client, key := resourcePoolFixture(t)
	h := resourceSubscribe(t, s, key, false, false)
	resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageUnsupported })
	if len(client.Actions()) != 0 {
		t.Fatal("unsupported resource queried")
	}
	h.Cancel()
	client.PrependReactor("list", "*", func(clienttesting.Action) (bool, runtime.Object, error) {
		page := &unstructured.UnstructuredList{}
		page.SetResourceVersion("1")
		page.SetContinue("same")
		return true, page, nil
	})
	h = resourceSubscribe(t, s, key, true, true)
	resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoveragePartial })
	if len(client.Actions()) != 2 {
		t.Fatal("cursor loop not stopped")
	}
}

func TestResourceMissingScopeIsNotAbsentAPI(t *testing.T) {
	s, client, key := resourcePoolFixture(t)
	client.PrependReactor("list", "*", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "apps")
	})
	h := resourceSubscribe(t, s, key, true, true)
	resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageUnavailable })
	if len(client.Actions()) != 1 {
		t.Fatal("missing scope retried automatically")
	}
}

func TestResourceBorrowedDenialDoesNotOpenDynamicWatch(t *testing.T) {
	s, client, key := resourcePoolFixture(t)
	s.deploymentCoverage = data.Coverage{State: data.CoverageDenied, Reason: "deployments denied"}
	key.GVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	h := resourceSubscribe(t, s, key, true, true)
	resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageDenied })
	if len(client.Actions()) != 0 {
		t.Fatal("borrowed denial retried through dynamic client")
	}
}

type resourceRefreshWatch struct {
	watch.Interface
	stopped chan struct{}
	release chan struct{}
}

func (w *resourceRefreshWatch) Stop() {
	close(w.stopped)
	<-w.release
	w.Interface.Stop()
}

func TestResourceRefreshCollectingWithoutBackoff(t *testing.T) {
	s, client, key := resourcePoolFixture(t)
	s.resources.retry = func(int) time.Duration { return time.Hour }
	stopped, stopRelease := make(chan struct{}), make(chan struct{})
	stream := &resourceRefreshWatch{Interface: watch.NewRaceFreeFake(), stopped: stopped, release: stopRelease}
	watchEntered, watchRelease := make(chan struct{}), make(chan struct{})
	var watches int
	client.PrependWatchReactor("*", func(clienttesting.Action) (bool, watch.Interface, error) {
		watches++
		if watches == 1 {
			return true, stream, nil
		}
		close(watchEntered)
		<-watchRelease
		return true, watch.NewRaceFreeFake(), nil
	})
	resourceListReactor(client, 1)
	h := resourceSubscribe(t, s, key, true, true)
	resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageComplete })
	entered, release := make(chan struct{}), make(chan struct{})
	var stopOnce, listOnce, watchOnce sync.Once
	defer stopOnce.Do(func() { close(stopRelease) })
	defer listOnce.Do(func() { close(release) })
	defer watchOnce.Do(func() { close(watchRelease) })
	client.PrependReactor("list", "*", func(clienttesting.Action) (bool, runtime.Object, error) {
		close(entered)
		<-release
		page := &unstructured.UnstructuredList{}
		page.SetResourceVersion("20")
		page.Items = []unstructured.Unstructured{resourceObject(2)}
		return true, page, nil
	})
	h.Refresh()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("watch not stopped")
	}
	if page := h.Page("", 10); page.Coverage.State != data.CoverageCollecting || page.Total != 1 {
		t.Fatalf("stopping coverage: %+v", page)
	}
	stopOnce.Do(func() { close(stopRelease) })
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("explicit refresh entered failure backoff")
	}
	if page := h.Page("", 10); page.Coverage.State != data.CoverageCollecting || page.Total != 0 {
		t.Fatalf("relist coverage: %+v", page)
	}
	listOnce.Do(func() { close(release) })
	select {
	case <-watchEntered:
	case <-time.After(time.Second):
		t.Fatal("new watch not requested")
	}
	if page := h.Page("", 10); page.Coverage.State != data.CoverageCollecting || page.Total != 1 {
		t.Fatalf("handshake coverage: %+v", page)
	}
	watchOnce.Do(func() { close(watchRelease) })
	resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageComplete && p.Total == 1 })
}

package kube

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

func resourceMemoryFixture(t testing.TB) (*Source, data.ResourceSubscriptionKey) {
	t.Helper()
	s, client, key := resourcePoolFixture(t)
	resourceListReactor(client, 10000)
	client.PrependWatchReactor("*", func(clienttesting.Action) (bool, watch.Interface, error) { return true, watch.NewRaceFreeFake(), nil })
	s.podInf = cache.NewSharedIndexInformer(&cache.ListWatch{}, &corev1.Pod{}, 0, cache.Indexers{})
	s.podCoverage = data.Coverage{State: data.CoverageComplete}
	for i := range 10000 {
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pod-%05d", i), Namespace: "apps", UID: types.UID(fmt.Sprintf("uid-%d", i)), ResourceVersion: "10", Annotations: map[string]string{"unretained": strings.Repeat("x", 4096)}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "example.invalid/app:v1"}}}}
		if err := s.podInf.GetStore().Add(p); err != nil {
			t.Fatal(err)
		}
	}
	return s, key
}

func TestResourceBrowserMemory10kPodsAnd10kSlices(t *testing.T) {
	s, key := resourceMemoryFixture(t)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	slices := resourceSubscribe(t, s, key, true, true)
	podKey := key
	podKey.GVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	pods := resourceSubscribe(t, s, podKey, true, true)
	resourceWaitWithin(t, slices, 60*time.Second, func(p data.ResourcePage) bool { return p.Total == 10000 && p.Coverage.State == data.CoverageComplete })
	resourceWaitWithin(t, pods, 60*time.Second, func(p data.ResourcePage) bool { return p.Total == 10000 })
	runtime.GC()
	runtime.ReadMemStats(&after)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("10k pods + 10k EndpointSlices: incremental retained heap %d bytes; allocation volume %d bytes", retained, after.TotalAlloc-before.TotalAlloc)
	if retained > 64<<20 {
		t.Fatal("resource browser memory budget exceeded")
	}
	runtime.KeepAlive(s)
}

func BenchmarkResourceBrowser10kPods10kSlices(b *testing.B) {
	s, key := resourceMemoryFixture(b)
	podKey := key
	podKey.GVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		a := resourceSubscribe(b, s, key, true, true)
		p := resourceSubscribe(b, s, podKey, true, true)
		resourceWaitWithin(b, a, 60*time.Second, func(page data.ResourcePage) bool {
			return page.Total == 10000 && page.Coverage.State == data.CoverageComplete
		})
		resourceWaitWithin(b, p, 60*time.Second, func(page data.ResourcePage) bool { return page.Total == 10000 })
		a.Cancel()
		p.Cancel()
	}
	b.StopTimer()
	a := resourceSubscribe(b, s, key, true, true)
	p := resourceSubscribe(b, s, podKey, true, true)
	resourceWaitWithin(b, a, 60*time.Second, func(page data.ResourcePage) bool {
		return page.Total == 10000 && page.Coverage.State == data.CoverageComplete
	})
	resourceWaitWithin(b, p, 60*time.Second, func(page data.ResourcePage) bool { return page.Total == 10000 })
	runtime.GC()
	runtime.ReadMemStats(&after)
	b.ReportMetric(float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)), "retained-B")
	runtime.KeepAlive(s)
}

func BenchmarkResourcePage500Of10k(b *testing.B) {
	s, client, key := resourcePoolFixture(b)
	resourceListReactor(client, 10000)
	h := resourceSubscribe(b, s, key, true, false)
	resourceWaitWithin(b, h, 60*time.Second, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageListOnly })
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.Page("", 500)
	}
}

func TestResourceWatchGrowthStopsAtStoreLimit(t *testing.T) {
	s, client, key := resourcePoolFixture(t)
	s.resources.rowLimit = 2
	resourceListReactor(client, 1)
	stream := watch.NewRaceFreeFake()
	client.PrependWatchReactor("*", func(clienttesting.Action) (bool, watch.Interface, error) { return true, stream, nil })
	h := resourceSubscribe(t, s, key, true, true)
	resourceWait(t, h, func(p data.ResourcePage) bool { return p.Coverage.State == data.CoverageComplete })
	for _, i := range []int{1, 2} {
		u := resourceObject(i)
		stream.Add(&u)
	}
	got := resourceWait(t, h, func(p data.ResourcePage) bool { return p.Truncated })
	if got.Total != 2 || got.Coverage.State != data.CoveragePartial {
		t.Fatal(got)
	}
	deadline := time.Now().Add(time.Second)
	for !stream.IsStopped() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !stream.IsStopped() {
		t.Fatal("limited watch remains alive")
	}
}

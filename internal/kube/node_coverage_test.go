package kube

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/vynazevedo/lazykube/internal/data"
)

func denyNodes(s *Source) {
	s.client.(*fake.Clientset).PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", errors.New("denied"))
	})
}

func nodeMetrics(cpu string) *metricsfake.Clientset {
	mc := metricsfake.NewSimpleClientset()
	mc.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &metricsv1beta1.NodeMetricsList{Items: []metricsv1beta1.NodeMetrics{{
			ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			Usage:      corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse("1Gi")},
		}}}, nil
	})
	return mc
}

func TestNodeCoverageIsTyped(t *testing.T) {
	s := startTest(t, nil, testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	s.Metrics(0, "apps/web-1", 10)
	if n := s.Snapshot().Node; n.Name != "node-a" || n.Coverage.State != data.CoverageComplete || !n.Ready {
		t.Fatalf("observed node coverage = %+v", n)
	}

	denied := startWith(t, nil, denyNodes, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	denied.Metrics(0, "apps/web-1", 10)
	n := denied.Snapshot().Node
	if n.Name != "node-a" || n.Coverage.State != data.CoverageDenied || n.Coverage.Reason != reasonNodesDeny {
		t.Fatalf("node without permission must be typed as denied: %+v", n)
	}

	missing := startTest(t, nil, testNode("node-b"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	missing.Metrics(0, "apps/web-1", 10)
	if n := missing.Snapshot().Node; n.Name != "node-a" || n.Coverage.State != data.CoverageUnavailable {
		t.Fatalf("node absent from the informer must be unavailable: %+v", n)
	}

	none := startTest(t, nil, testPod("apps", "web-1", "", corev1.PodPending))
	if n := none.Snapshot().Node; n.Coverage.State != data.CoverageUnavailable || n.Ready {
		t.Fatalf("no node in scope must be unavailable and not ready: %+v", n)
	}
}

func TestNodeCPUKnownOnlyWithNodeMetrics(t *testing.T) {
	s := startTest(t, nodeMetrics("0"), testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	s.pollMetrics(context.Background())
	if m := s.Metrics(0, "apps/web-1", 10); !m.NodeCPUKnown || m.NodeCPU != 0 {
		t.Fatalf("zero usage from node metrics is known: %+v", m)
	}
	quiet := startTest(t, metricsfake.NewSimpleClientset(), testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	quiet.pollMetrics(context.Background())
	if m := quiet.Metrics(0, "apps/web-1", 10); m.NodeCPUKnown {
		t.Fatalf("missing node metrics must not be reported as 0%%: %+v", m)
	}
	denied := startWith(t, nodeMetrics("500m"), denyNodes, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	denied.pollMetrics(context.Background())
	if m := denied.Metrics(0, "apps/web-1", 10); m.NodeCPUKnown {
		t.Fatalf("node CPU needs the node allocatable: %+v", m)
	}
}

type flakyMetrics struct {
	mu       sync.Mutex
	podsErr  error
	nodeName string
}

func (f *flakyMetrics) set(podsErr error, nodeName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.podsErr, f.nodeName = podsErr, nodeName
}

func (f *flakyMetrics) client() *metricsfake.Clientset {
	mc := metricsfake.NewSimpleClientset()
	mc.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.podsErr != nil {
			return true, nil, f.podsErr
		}
		return true, &metricsv1beta1.PodMetricsList{}, nil
	})
	mc.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return true, &metricsv1beta1.NodeMetricsList{Items: []metricsv1beta1.NodeMetrics{{
			ObjectMeta: metav1.ObjectMeta{Name: f.nodeName},
			Usage:      corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("0"), corev1.ResourceMemory: resource.MustParse("1Gi")},
		}}}, nil
	})
	return mc
}

func TestNodeUsageIsInvalidatedWhenCollectionFails(t *testing.T) {
	f := &flakyMetrics{nodeName: "node-a"}
	s := startWith(t, f.client(), func(s *Source) { s.manualPoll = true }, testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	ctx := context.Background()
	measured := func(step string, want bool) {
		t.Helper()
		m := s.Metrics(0, "apps/web-1", 10)
		n := s.Snapshot().Node
		if m.NodeCPUKnown != want || (n.MemUsed != "n/a") != want {
			t.Fatalf("%s: cpu known %v mem %q, want measured %v", step, m.NodeCPUKnown, n.MemUsed, want)
		}
		if want && (m.NodeCPU != 0 || n.MemUsed != "1.0Gi") {
			t.Fatalf("%s: measured zero must stay zero: cpu %d mem %q", step, m.NodeCPU, n.MemUsed)
		}
	}
	s.pollMetrics(ctx)
	measured("first collection", true)

	f.set(apierrors.NewServiceUnavailable("down"), "node-a")
	s.pollMetrics(ctx)
	if m := s.Metrics(0, "apps/web-1", 10); m.Available {
		t.Fatal("failed collection is unavailable")
	}
	measured("failed collection", false)

	f.set(nil, "node-a")
	s.pollMetrics(ctx)
	measured("recovered collection", true)

	f.set(nil, "node-b")
	s.pollMetrics(ctx)
	measured("node absent from node metrics", false)

	s.metrics = nil
	f.set(nil, "node-a")
	s.pollMetrics(ctx)
	measured("metrics client gone", false)
}

type nodeGate struct {
	mu      sync.Mutex
	err     error
	watches []*watch.FakeWatcher
	lists   int
}

func (g *nodeGate) set(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err = err
}

func (g *nodeGate) current() (*watch.FakeWatcher, int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.watches) == 0 {
		return nil, 0, g.lists
	}
	return g.watches[len(g.watches)-1], len(g.watches), g.lists
}

func (g *nodeGate) install(s *Source) {
	c := s.client.(*fake.Clientset)
	c.PrependReactor("list", "nodes", func(a k8stesting.Action) (bool, runtime.Object, error) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if lo, ok := a.(k8stesting.ListActionImpl); ok && lo.ListOptions.Limit == 1 {
			return false, nil, nil
		}
		g.lists++
		if g.err != nil {
			return true, nil, g.err
		}
		return false, nil, nil
	})
	c.PrependWatchReactor("nodes", func(k8stesting.Action) (bool, watch.Interface, error) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.err != nil {
			return true, nil, g.err
		}
		w := watch.NewFake()
		g.watches = append(g.watches, w)
		return true, w, nil
	})
}

func waitLong(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func nodeHealthCycle(t *testing.T, failure error, state data.CoverageState, reason string) {
	g := &nodeGate{}
	s := startWith(t, nil, g.install, testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	s.Metrics(0, "apps/web-1", 10)
	coverage := func() data.CoverageState { return s.Snapshot().Node.Coverage.State }
	waitLong(t, "initial watch", func() bool { w, _, _ := g.current(); return w != nil })
	waitLong(t, "synced coverage", func() bool { return coverage() == data.CoverageComplete })

	first, watches, _ := g.current()
	updated := testNode("node-a")
	updated.ResourceVersion = "7"
	first.Modify(updated)
	first.Stop()
	waitLong(t, "rewatch after a normal close", func() bool { _, n, _ := g.current(); return n > watches })
	if got := coverage(); got != data.CoverageComplete {
		t.Fatalf("a normal watch close is not a failure, coverage %q", got)
	}

	g.set(failure)
	live, watches, lists := g.current()
	live.Stop()
	waitLong(t, string(state)+" coverage", func() bool { return coverage() == state })
	if n := s.Snapshot().Node; n.Name != "node-a" || n.Coverage.Reason != reason {
		t.Fatalf("cached node after a real failure must be degraded, got %+v", n)
	}
	waitLong(t, "relist attempt while failing", func() bool { _, _, l := g.current(); return l > lists })
	if got := coverage(); got != state {
		t.Fatalf("a failed relist keeps the node degraded, coverage %q", got)
	}
	if _, n, _ := g.current(); n != watches {
		t.Fatal("no watch can be established while failing")
	}

	g.set(nil)
	waitLong(t, "recovered coverage", func() bool { return coverage() == data.CoverageComplete })
	if _, n, _ := g.current(); n <= watches {
		t.Fatal("recovery requires a new list and watch")
	}
}

func TestNodeCoverageFollowsInformerHealth(t *testing.T) {
	t.Run("revoked", func(t *testing.T) {
		nodeHealthCycle(t, apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", errors.New("revoked")), data.CoverageDenied, reasonNodesLostGrant)
	})
	t.Run("persistent failure", func(t *testing.T) {
		nodeHealthCycle(t, apierrors.NewServiceUnavailable("apiserver down"), data.CoverageStale, reasonNodesStale)
	})
}

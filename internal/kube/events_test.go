package kube

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/TNK-Studio/lazykube/internal/data"
)

func fastWide(t *testing.T, retries int) {
	t.Helper()
	saved := wideTiming
	wideTiming = wideTimingConfig{sync: 300 * time.Millisecond, backoff: 20 * time.Millisecond, cooldown: time.Minute, retries: retries}
	t.Cleanup(func() { wideTiming = saved })
}

const eventsWaitLimit = 15 * time.Second

func clusterState(s *Source) data.EventFeed {
	return s.ScopedEvents(data.EventQuery{Scope: data.ScopeCluster})
}

func waitState(t *testing.T, s *Source, want data.FeedState) data.EventFeed {
	t.Helper()
	deadline := time.Now().Add(eventsWaitLimit)
	for {
		got := clusterState(s)
		if got.State == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("cluster events never reached state %v, last = %+v", want, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func isProbe(a k8stesting.Action) bool {
	l, ok := a.(k8stesting.ListActionImpl)
	return ok && l.ListOptions.Limit == 1
}

var (
	eventsGR      = schema.GroupResource{Resource: "events"}
	errUnavailble = apierrors.NewServiceUnavailable("apiserver overloaded")
	errDenied     = apierrors.NewForbidden(eventsGR, "", errors.New("denied"))
)

func TestClusterEventsListFailureAfterProbeIsNotLoadingForever(t *testing.T) {
	fastWide(t, 1)
	s := namespacedStart(t, func(s *Source) {
		s.client.(*fake.Clientset).PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetNamespace() == "" && !isProbe(a) {
				return true, nil, apierrors.NewInternalError(errors.New("etcd timeout"))
			}
			return false, nil, nil
		})
	}, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	if got := clusterState(s); got.State != data.FeedLoading {
		t.Fatalf("first request = %+v", got)
	}
	got := waitState(t, s, data.FeedUnavailable)
	if got.Reason == "" || got.Reason == reasonEventsLoading {
		t.Fatalf("reason must explain the failure: %q", got.Reason)
	}
}

func TestClusterEventsSyncHasDeadline(t *testing.T) {
	fastWide(t, 1)
	release := make(chan struct{})
	s := namespacedStart(t, func(s *Source) {
		s.client.(*fake.Clientset).PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetNamespace() == "" && !isProbe(a) {
				<-release
			}
			return false, nil, nil
		})
	}, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	t.Cleanup(func() { close(release) })
	clusterState(s)
	got := waitState(t, s, data.FeedUnavailable)
	if got.Reason != reasonEventsSyncTimeout {
		t.Fatalf("reason = %q", got.Reason)
	}
}

type watchControl struct {
	mu      sync.Mutex
	deny    bool
	current *watch.FakeWatcher
}

func (w *watchControl) react(a k8stesting.Action) (bool, watch.Interface, error) {
	if a.GetNamespace() != "" {
		return false, nil, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.deny {
		return true, nil, errDenied
	}
	w.current = watch.NewFake()
	return true, w.current, nil
}

func (w *watchControl) setDeny(deny bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deny = deny
	if deny && w.current != nil {
		w.current.Stop()
		w.current = nil
	}
}

func (w *watchControl) denied() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.deny
}

func TestClusterEventsLosePermissionAfterSync(t *testing.T) {
	fastWide(t, 1)
	ctl := &watchControl{}
	s := namespacedStart(t, func(s *Source) {
		c := s.client.(*fake.Clientset)
		c.PrependWatchReactor("events", ctl.react)
		c.PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetNamespace() == "" && !isProbe(a) && ctl.denied() {
				return true, nil, errDenied
			}
			return false, nil, nil
		})
	}, testPod("apps", "web-1", "node-a", corev1.PodRunning),
		event("other", "e1", "Pod", "x", "", "Warning", fixedNow))
	clusterState(s)
	if got := waitState(t, s, data.FeedReady); len(got.Items) != 1 {
		t.Fatalf("synced cluster events = %+v", got.Items)
	}
	ctl.setDeny(true)
	got := waitState(t, s, data.FeedForbidden)
	if len(got.Items) != 0 || got.Reason != reasonClusterLost {
		t.Fatalf("stale events must not look current: %+v", got)
	}
	ctl.setDeny(false)
	if got := waitState(t, s, data.FeedReady); len(got.Items) != 1 {
		t.Fatalf("recovered events = %+v", got.Items)
	}
}

func probeCounter(fail func(n int32) error) (*atomic.Int32, k8stesting.ReactionFunc) {
	var n atomic.Int32
	return &n, func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() != "" || !isProbe(a) {
			return false, nil, nil
		}
		if err := fail(n.Add(1)); err != nil {
			return true, nil, err
		}
		return false, nil, nil
	}
}

func TestClusterEventsRetryTransientProbeFailure(t *testing.T) {
	fastWide(t, 3)
	probes, react := probeCounter(func(n int32) error {
		if n == 1 {
			return errUnavailble
		}
		return nil
	})
	s := namespacedStart(t, func(s *Source) {
		s.client.(*fake.Clientset).PrependReactor("list", "events", react)
	}, testPod("apps", "web-1", "node-a", corev1.PodRunning), event("other", "e1", "Pod", "x", "", "Normal", fixedNow))
	clusterState(s)
	if got := waitState(t, s, data.FeedReady); len(got.Items) != 1 {
		t.Fatalf("events after retry = %+v", got.Items)
	}
	if probes.Load() != 2 {
		t.Fatalf("probes = %d", probes.Load())
	}
}

func TestClusterEventsRestartAfterRetriesExhausted(t *testing.T) {
	fastWide(t, 2)
	clock := &testClock{t: fixedNow}
	probes, react := probeCounter(func(n int32) error {
		if n <= 2 {
			return errUnavailble
		}
		return nil
	})
	s := namespacedStart(t, func(s *Source) {
		s.opts.Now = clock.Now
		s.client.(*fake.Clientset).PrependReactor("list", "events", react)
	}, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	clusterState(s)
	waitState(t, s, data.FeedUnavailable)
	waitFor(t, func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return !s.wide.running
	})
	for i := 0; i < 20; i++ {
		clusterState(s)
	}
	if probes.Load() != 2 {
		t.Fatalf("exhausted retries must wait for the cooldown, probes = %d", probes.Load())
	}
	clock.Add(wideTiming.cooldown + time.Second)
	for i := 0; i < 5; i++ {
		clusterState(s)
	}
	waitState(t, s, data.FeedReady)
	if probes.Load() != 3 {
		t.Fatalf("restart must run exactly one new subscription, probes = %d", probes.Load())
	}
}

func TestPodLogsEmptyTargetStopsStream(t *testing.T) {
	p := testPod("apps", "web-1", "node-a", corev1.PodRunning)
	p.Status.ContainerStatuses = []corev1.ContainerStatus{runningStatus("app", true)}
	s := startWith(t, nil, func(s *Source) { s.manualPoll = true }, p)
	s.PodLogs(data.LogRequest{Pod: "apps/web-1"}, 0, 10)
	s.mu.RLock()
	st := s.stream
	s.mu.RUnlock()
	if st == nil {
		t.Fatal("stream not started")
	}
	s.PodLogs(data.LogRequest{}, 0, 10)
	select {
	case <-st.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("empty target must cancel the subscription")
	}
}

func TestEventsDefaultScopeTracksPermissionLoss(t *testing.T) {
	ctl := &watchControl{}
	s := startWith(t, nil, func(s *Source) {
		s.manualPoll = true
		c := s.client.(*fake.Clientset)
		c.PrependWatchReactor("events", ctl.react)
		c.PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetNamespace() == "" && ctl.denied() {
				return true, nil, errDenied
			}
			return false, nil, nil
		})
	}, testPod("apps", "web-1", "node-a", corev1.PodRunning),
		event("apps", "e1", "Pod", "web-1", "", "Warning", fixedNow))
	namespaceFeed := func() data.EventFeed {
		return s.ScopedEvents(data.EventQuery{Scope: data.ScopeNamespace, Namespace: "apps"})
	}
	if got := waitState(t, s, data.FeedReady); len(got.Items) != 1 {
		t.Fatalf("synced events = %+v", got.Items)
	}
	ctl.setDeny(true)
	got := waitState(t, s, data.FeedForbidden)
	if len(got.Items) != 0 || got.Reason != reasonClusterLost {
		t.Fatalf("stale cluster events must not look current: %+v", got)
	}
	if ns := namespaceFeed(); ns.State != data.FeedForbidden || len(ns.Items) != 0 {
		t.Fatalf("namespace feed must share the informer state: %+v", ns)
	}
	ctl.setDeny(false)
	if got := waitState(t, s, data.FeedReady); len(got.Items) != 1 {
		t.Fatalf("recovered events = %+v", got.Items)
	}
	if ns := namespaceFeed(); ns.State != data.FeedReady || len(ns.Items) != 1 {
		t.Fatalf("namespace feed after recovery = %+v", ns)
	}
}

type eventsRecovery struct {
	mu        sync.Mutex
	phase     int
	current   *watch.FakeWatcher
	release   chan struct{}
	pageState chan data.FeedState
	waitState chan data.FeedState
}

const (
	eventsPhaseLive = iota
	eventsPhasePaged
	eventsPhasePending
)

func (r *eventsRecovery) get() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.phase
}

func (r *eventsRecovery) set(phase int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phase = phase
	if r.current != nil {
		r.current.Stop()
		r.current = nil
	}
}

func eventsReport(ch chan data.FeedState, s *Source) {
	select {
	case ch <- clusterState(s).State:
	default:
	}
}

func TestClusterEventsRecoveryWaitsForFullRelistAndWatch(t *testing.T) {
	fastWide(t, 1)
	r := &eventsRecovery{release: make(chan struct{}), pageState: make(chan data.FeedState, 1), waitState: make(chan data.FeedState, 1)}
	fresh := event("other", "e2", "Pod", "y", "", "Warning", fixedNow)
	fresh.ResourceVersion = "20"
	var s *Source
	s = namespacedStart(t, func(src *Source) {
		c := src.client.(*fake.Clientset)
		c.PrependWatchReactor("events", func(a k8stesting.Action) (bool, watch.Interface, error) {
			if a.GetNamespace() != "" {
				return false, nil, nil
			}
			switch r.get() {
			case eventsPhasePaged:
				return true, nil, errDenied
			case eventsPhasePending:
				eventsReport(r.waitState, s)
				<-r.release
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			r.current = watch.NewFake()
			return true, r.current, nil
		})
		c.PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetNamespace() != "" || isProbe(a) {
				return false, nil, nil
			}
			switch r.get() {
			case eventsPhasePaged:
				if a.(k8stesting.ListActionImpl).ListOptions.Continue == "" {
					return true, &corev1.EventList{ListMeta: metav1.ListMeta{Continue: "page-2", ResourceVersion: "20"}, Items: []corev1.Event{*fresh}}, nil
				}
				eventsReport(r.pageState, s)
				return true, nil, errUnavailble
			case eventsPhasePending:
				return true, &corev1.EventList{ListMeta: metav1.ListMeta{ResourceVersion: "20"}, Items: []corev1.Event{*fresh}}, nil
			}
			return false, nil, nil
		})
	}, testPod("apps", "web-1", "node-a", corev1.PodRunning),
		event("other", "e1", "Pod", "x", "", "Warning", fixedNow))
	t.Cleanup(func() {
		select {
		case <-r.release:
		default:
			close(r.release)
		}
	})
	clusterState(s)
	if got := waitState(t, s, data.FeedReady); len(got.Items) != 1 || got.Items[0].Reason != "Re1" {
		t.Fatalf("synced events = %+v", got.Items)
	}
	r.set(eventsPhasePaged)
	select {
	case st := <-r.pageState:
		if st == data.FeedReady {
			t.Fatal("a first page alone must not announce recovery")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second page never requested")
	}
	r.set(eventsPhasePending)
	select {
	case st := <-r.waitState:
		if st == data.FeedReady {
			t.Fatal("recovery must wait for the watch to be established")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch never requested")
	}
	if got := clusterState(s); got.State == data.FeedReady {
		t.Fatalf("pending watch must not look current: %+v", got)
	}
	close(r.release)
	got := waitState(t, s, data.FeedReady)
	if len(got.Items) != 1 || got.Items[0].Reason != "Re2" {
		t.Fatalf("recovered events must reflect the new list: %+v", got.Items)
	}
}

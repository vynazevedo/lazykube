package kube

import (
	"context"
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
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	k8stesting "k8s.io/client-go/testing"

	"github.com/vynazevedo/lazykube/internal/data"
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

type eventsWatchPlan struct {
	mu      sync.Mutex
	calls   int
	current *watch.FakeWatcher
	steps   map[int]func() error
}

func (p *eventsWatchPlan) react(a k8stesting.Action) (bool, watch.Interface, error) {
	if a.GetNamespace() != "" {
		return false, nil, nil
	}
	p.mu.Lock()
	p.calls++
	step := p.steps[p.calls]
	p.mu.Unlock()
	if step != nil {
		if err := step(); err != nil {
			return true, nil, err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = watch.NewFake()
	return true, p.current, nil
}

func (p *eventsWatchPlan) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *eventsWatchPlan) watcher(t *testing.T) *watch.FakeWatcher {
	t.Helper()
	var w *watch.FakeWatcher
	waitFor(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		w = p.current
		return w != nil
	})
	p.mu.Lock()
	p.current = nil
	p.mu.Unlock()
	return w
}

func eventsEndNormally(t *testing.T, p *eventsWatchPlan, obj runtime.Object) {
	t.Helper()
	w := p.watcher(t)
	w.Add(obj)
	w.Stop()
}

func eventsListCounter(c *fake.Clientset) *atomic.Int32 {
	var n atomic.Int32
	c.PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == "" && !isProbe(a) {
			n.Add(1)
		}
		return false, nil, nil
	})
	return &n
}

func eventsUnblockOnCleanup(t *testing.T, release chan struct{}) {
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
}

type eventsGate func(ctx context.Context, o metav1.ListOptions)

type eventsGatedClient struct {
	*fake.Clientset
	list, watch eventsGate
}

type eventsGatedCore struct {
	corev1client.CoreV1Interface
	list, watch eventsGate
}

type eventsGatedEvents struct {
	corev1client.EventInterface
	list, watch eventsGate
}

func (c eventsGatedClient) CoreV1() corev1client.CoreV1Interface {
	return eventsGatedCore{c.Clientset.CoreV1(), c.list, c.watch}
}

func (c eventsGatedCore) Events(namespace string) corev1client.EventInterface {
	return eventsGatedEvents{c.CoreV1Interface.Events(namespace), c.list, c.watch}
}

func (e eventsGatedEvents) List(ctx context.Context, o metav1.ListOptions) (*corev1.EventList, error) {
	if e.list != nil {
		e.list(ctx, o)
	}
	return e.EventInterface.List(ctx, o)
}

func (e eventsGatedEvents) Watch(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
	if e.watch != nil {
		e.watch(ctx, o)
	}
	return e.EventInterface.Watch(ctx, o)
}

func eventsHold(ctx context.Context, release <-chan struct{}) {
	select {
	case <-release:
	case <-ctx.Done():
	}
}

func eventsDefaultStart(t *testing.T, setup func(*fake.Clientset), gated eventsGatedClient, objs ...runtime.Object) *Source {
	t.Helper()
	return startWith(t, nil, func(s *Source) {
		s.manualPoll = true
		c := s.client.(*fake.Clientset)
		setup(c)
		if gated.list != nil || gated.watch != nil {
			gated.Clientset = c
			s.client = gated
		}
	}, objs...)
}

func eventsStamped(name, rv string) *corev1.Event {
	e := event("apps", name, "Pod", "web-1", "", "Warning", fixedNow)
	e.ResourceVersion = rv
	return e
}

func TestEventsTransientWatchFailureRecoversWithoutRelist(t *testing.T) {
	plan := &eventsWatchPlan{steps: map[int]func() error{
		2: func() error { return apierrors.NewTooManyRequests("slow down", 0) },
	}}
	var lists *atomic.Int32
	s := eventsDefaultStart(t, func(c *fake.Clientset) {
		c.PrependWatchReactor("events", plan.react)
		lists = eventsListCounter(c)
	}, eventsGatedClient{}, testPod("apps", "web-1", "node-a", corev1.PodRunning), eventsStamped("e1", "1"))
	waitState(t, s, data.FeedReady)
	before := lists.Load()
	eventsEndNormally(t, plan, eventsStamped("e2", "2"))
	waitFor(t, func() bool { return plan.count() >= 3 })
	got := waitState(t, s, data.FeedReady)
	if len(got.Items) != 2 {
		t.Fatalf("events after watch retry = %+v", got.Items)
	}
	if lists.Load() != before {
		t.Fatalf("scenario must resume by resourceVersion without a new List, lists %d -> %d", before, lists.Load())
	}
}

func TestEventsExpiredWatchWaitsForPagedRelist(t *testing.T) {
	plan := &eventsWatchPlan{}
	release := make(chan struct{})
	seen := make(chan data.FeedState, 1)
	var relist atomic.Bool
	var s *Source
	s = eventsDefaultStart(t, func(c *fake.Clientset) {
		c.PrependWatchReactor("events", plan.react)
		c.PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetNamespace() != "" || isProbe(a) || !relist.Load() {
				return false, nil, nil
			}
			if a.(k8stesting.ListActionImpl).ListOptions.Continue == "" {
				return true, &corev1.EventList{ListMeta: metav1.ListMeta{Continue: "page-2", ResourceVersion: "9"}, Items: []corev1.Event{*eventsStamped("e3", "8")}}, nil
			}
			return true, &corev1.EventList{ListMeta: metav1.ListMeta{ResourceVersion: "9"}, Items: []corev1.Event{*eventsStamped("e4", "9")}}, nil
		})
	}, eventsGatedClient{list: func(ctx context.Context, o metav1.ListOptions) {
		if o.Continue == "" || !relist.Load() {
			return
		}
		eventsReport(seen, s)
		eventsHold(ctx, release)
	}}, testPod("apps", "web-1", "node-a", corev1.PodRunning), eventsStamped("e1", "1"))
	eventsUnblockOnCleanup(t, release)
	waitState(t, s, data.FeedReady)
	relist.Store(true)
	plan.watcher(t).Error(&metav1.Status{Status: metav1.StatusFailure, Code: 410, Reason: metav1.StatusReasonExpired, Message: "too old resource version"})
	select {
	case st := <-seen:
		if st == data.FeedReady {
			t.Fatal("an expired watch must not keep the old cache as current during the relist")
		}
	case <-time.After(eventsWaitLimit):
		t.Fatal("second page never requested")
	}
	if got := clusterState(s); got.State == data.FeedReady {
		t.Fatalf("paged relist in progress must not look current: %+v", got)
	}
	close(release)
	got := waitState(t, s, data.FeedReady)
	if len(got.Items) != 2 || got.Items[0].Reason == "Re1" || got.Items[1].Reason == "Re1" {
		t.Fatalf("events after relist = %+v", got.Items)
	}
}

func TestEventsDefaultFirstWatchPendingIsNotReady(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	s := eventsDefaultStart(t, func(*fake.Clientset) {}, eventsGatedClient{watch: func(ctx context.Context, _ metav1.ListOptions) {
		first := false
		once.Do(func() { first = true })
		if !first {
			return
		}
		close(entered)
		eventsHold(ctx, release)
	}}, testPod("apps", "web-1", "node-a", corev1.PodRunning), eventsStamped("e1", "1"))
	eventsUnblockOnCleanup(t, release)
	select {
	case <-entered:
	case <-time.After(eventsWaitLimit):
		t.Fatal("first watch never requested")
	}
	if got := clusterState(s); got.State == data.FeedReady {
		t.Fatalf("listed cache without a confirmed watch must not be Ready: %+v", got)
	}
	if ns := s.ScopedEvents(data.EventQuery{Scope: data.ScopeNamespace, Namespace: "apps"}); ns.State == data.FeedReady {
		t.Fatalf("namespace feed without a confirmed watch must not be Ready: %+v", ns)
	}
	close(release)
	if got := waitState(t, s, data.FeedReady); len(got.Items) != 1 {
		t.Fatalf("events after first watch = %+v", got.Items)
	}
}

func TestEventsNormalWatchCloseStaysReady(t *testing.T) {
	release := make(chan struct{})
	seen := make(chan data.FeedState, 1)
	plan := &eventsWatchPlan{}
	var lists *atomic.Int32
	var watches atomic.Int32
	var s *Source
	s = eventsDefaultStart(t, func(c *fake.Clientset) {
		c.PrependWatchReactor("events", plan.react)
		lists = eventsListCounter(c)
	}, eventsGatedClient{watch: func(ctx context.Context, _ metav1.ListOptions) {
		if watches.Add(1) != 2 {
			return
		}
		eventsReport(seen, s)
		eventsHold(ctx, release)
	}}, testPod("apps", "web-1", "node-a", corev1.PodRunning), eventsStamped("e1", "1"))
	eventsUnblockOnCleanup(t, release)
	waitState(t, s, data.FeedReady)
	before := lists.Load()
	eventsEndNormally(t, plan, eventsStamped("e2", "2"))
	select {
	case st := <-seen:
		if st != data.FeedReady {
			t.Fatalf("a normal watch reconnection must not degrade the feed, state = %v", st)
		}
	case <-time.After(eventsWaitLimit):
		t.Fatal("watch never reconnected")
	}
	close(release)
	waitFor(t, func() bool {
		got := clusterState(s)
		return got.State == data.FeedReady && len(got.Items) == 2
	})
	if lists.Load() != before {
		t.Fatalf("normal reconnection must not relist, lists %d -> %d", before, lists.Load())
	}
}

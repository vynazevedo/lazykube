package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/vynazevedo/lazykube/internal/data"
)

func waiting(name, reason, msg string, restarts int32) corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: name, RestartCount: restarts, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: msg}}}
}

func runningStatus(name string, ready bool) corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: name, Ready: ready, Started: ptr(true), State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(fixedNow.Add(-time.Hour))}}}
}

func terminated(reason string, code, signal int32) *corev1.ContainerStateTerminated {
	return &corev1.ContainerStateTerminated{Reason: reason, ExitCode: code, Signal: signal, FinishedAt: metav1.NewTime(fixedNow.Add(-2 * time.Minute))}
}

func withInits(p *corev1.Pod, names ...string) *corev1.Pod {
	for _, n := range names {
		p.Spec.InitContainers = append(p.Spec.InitContainers, corev1.Container{Name: n, Image: "busybox"})
	}
	return p
}

func TestSummarizeMatchesKubectl(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	cases := []struct {
		name   string
		mutate func(*corev1.Pod)
		reason string
		status data.PodStatus
		ready  string
	}{
		{"running ready", func(p *corev1.Pod) { p.Status.ContainerStatuses = []corev1.ContainerStatus{runningStatus("app", true)} }, "Running", data.StatusRunning, "1/1"},
		{"running not ready", func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{runningStatus("app", false)}
		}, "Running", data.StatusRunning, "0/1"},
		{"crashloop", func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{waiting("app", "CrashLoopBackOff", "back-off", 5)}
		}, "CrashLoopBackOff", data.StatusFailed, "0/1"},
		{"image pull", func(p *corev1.Pod) {
			p.Status.Phase = corev1.PodPending
			p.Status.ContainerStatuses = []corev1.ContainerStatus{waiting("app", "ImagePullBackOff", "", 0)}
		}, "ImagePullBackOff", data.StatusFailed, "0/1"},
		{"oom terminated", func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{Terminated: terminated("OOMKilled", 137, 0)}}}
		}, "OOMKilled", data.StatusFailed, "0/1"},
		{"exit code without reason", func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{Terminated: terminated("", 2, 0)}}}
		}, "ExitCode:2", data.StatusFailed, "0/1"},
		{"signal without reason", func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{Terminated: terminated("", 0, 9)}}}
		}, "Signal:9", data.StatusFailed, "0/1"},
		{"init progress", func(p *corev1.Pod) {
			withInits(p, "a", "b")
			p.Status.Phase = corev1.PodPending
			p.Status.InitContainerStatuses = []corev1.ContainerStatus{
				{Name: "a", State: corev1.ContainerState{Terminated: terminated("Completed", 0, 0)}},
				runningStatus("b", false),
			}
		}, "Init:1/2", data.StatusPending, "0/1"},
		{"init crash", func(p *corev1.Pod) {
			withInits(p, "a")
			p.Status.Phase = corev1.PodPending
			p.Status.InitContainerStatuses = []corev1.ContainerStatus{waiting("a", "CrashLoopBackOff", "", 3)}
		}, "Init:CrashLoopBackOff", data.StatusFailed, "0/1"},
		{"init error", func(p *corev1.Pod) {
			withInits(p, "a")
			p.Status.Phase = corev1.PodPending
			p.Status.InitContainerStatuses = []corev1.ContainerStatus{{Name: "a", State: corev1.ContainerState{Terminated: terminated("Error", 1, 0)}}}
		}, "Init:Error", data.StatusFailed, "0/1"},
		{"started sidecar", func(p *corev1.Pod) {
			p.Spec.InitContainers = []corev1.Container{{Name: "proxy", RestartPolicy: &always}}
			p.Status.InitContainerStatuses = []corev1.ContainerStatus{runningStatus("proxy", true)}
			p.Status.ContainerStatuses = []corev1.ContainerStatus{runningStatus("app", true)}
		}, "Running", data.StatusRunning, "2/2"},
		{"completed with running", func(p *corev1.Pod) {
			p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: "side"})
			p.Status.ContainerStatuses = []corev1.ContainerStatus{
				{Name: "app", State: corev1.ContainerState{Terminated: terminated("Completed", 0, 0)}},
				runningStatus("side", true),
			}
		}, "NotReady", data.StatusRunning, "1/2"},
		{"terminating", func(p *corev1.Pod) {
			now := metav1.NewTime(fixedNow)
			p.DeletionTimestamp = &now
			p.Status.ContainerStatuses = []corev1.ContainerStatus{runningStatus("app", true)}
		}, "Terminating", data.StatusUnknown, "1/1"},
		{"succeeded", func(p *corev1.Pod) {
			p.Status.Phase = corev1.PodSucceeded
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{Terminated: terminated("Completed", 0, 0)}}}
		}, "Completed", data.StatusCompleted, "0/1"},
		{"evicted", func(p *corev1.Pod) {
			p.Status.Phase = corev1.PodFailed
			p.Status.Reason = "Evicted"
		}, "Evicted", data.StatusFailed, "0/1"},
	}
	for _, c := range cases {
		p := testPod("ns", "p", "", corev1.PodRunning)
		c.mutate(p)
		got := toPod(p, fixedNow)
		ready := got.Ready
		if got.Reason != c.reason || got.Status != c.status || formatReady(ready, got.Containers) != c.ready {
			t.Errorf("%s: got %q/%v/%d of %d, want %q/%v/%s", c.name, got.Reason, got.Status, ready, got.Containers, c.reason, c.status, c.ready)
		}
	}
}

func formatReady(ready, total int) string {
	return fmt.Sprintf("%d/%d", ready, total)
}

func TestRestartsIncludeInitWhileInitializing(t *testing.T) {
	p := withInits(testPod("ns", "p", "", corev1.PodPending), "a")
	p.Status.InitContainerStatuses = []corev1.ContainerStatus{waiting("a", "CrashLoopBackOff", "", 4)}
	if got := toPod(p, fixedNow); got.Restarts != 4 {
		t.Fatalf("restarts = %d", got.Restarts)
	}
}

func TestToContainersCoversAllKinds(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	p := testPod("ns", "p", "", corev1.PodRunning)
	p.Spec.InitContainers = []corev1.Container{{Name: "migrate", Image: "m"}, {Name: "proxy", Image: "px", RestartPolicy: &always}}
	p.Spec.EphemeralContainers = []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug", Image: "busybox"}}}
	app := waiting("app", "CrashLoopBackOff", "back-off 5m0s \x1b[31mrestarting\x1b[0m", 7)
	app.LastTerminationState.Terminated = terminated("OOMKilled", 137, 0)
	app.LastTerminationState.Terminated.Message = strings.Repeat("x", 2*maxDiagMessage)
	p.Status.InitContainerStatuses = []corev1.ContainerStatus{
		{Name: "migrate", State: corev1.ContainerState{Terminated: terminated("Completed", 0, 0)}},
		runningStatus("proxy", true),
	}
	p.Status.ContainerStatuses = []corev1.ContainerStatus{app}
	got := toContainers(p, fixedNow, time.UTC)
	if len(got) != 4 {
		t.Fatalf("containers = %+v", got)
	}
	kinds := []data.ContainerKind{data.ContainerInit, data.ContainerSidecar, data.ContainerApp, data.ContainerEphemeral}
	for i, k := range kinds {
		if got[i].Kind != k {
			t.Fatalf("container %d kind = %v want %v", i, got[i].Kind, k)
		}
	}
	if got[0].Phase != data.PhaseTerminated || got[0].Current.Reason != "Completed" {
		t.Fatalf("init = %+v", got[0])
	}
	c := got[2]
	if c.Reason != "CrashLoopBackOff" || c.Restarts != 7 || c.Ready || strings.ContainsRune(c.Message, 0x1b) {
		t.Fatalf("app = %+v", c)
	}
	if !c.HasPrevious() || c.Last.Reason != "OOMKilled" || c.Last.ExitCode != 137 || c.Last.Age != "2m" || c.Last.Finished != "2026-10-16 11:58:00" {
		t.Fatalf("last termination = %+v", c.Last)
	}
	if len(c.Last.Message) > maxDiagMessage+len("…") {
		t.Fatalf("termination message not bounded: %d", len(c.Last.Message))
	}
	if got[3].Reason != "sem status reportado" {
		t.Fatalf("ephemeral without status = %+v", got[3])
	}
}

func TestDefaultContainerAnnotation(t *testing.T) {
	p := testPod("ns", "p", "", corev1.PodRunning)
	p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: "main"})
	if defaultContainerName(p) != "app" {
		t.Fatal("first container is the default")
	}
	p.Annotations = map[string]string{defaultContainer: "main"}
	if defaultContainerName(p) != "main" {
		t.Fatal("kubectl default-container annotation must be honored")
	}
	p.Annotations[defaultContainer] = "missing"
	if defaultContainerName(p) != "app" {
		t.Fatal("unknown annotation falls back")
	}
}

func multiPod() *corev1.Pod {
	p := withInits(testPod("apps", "web-1", "node-a", corev1.PodRunning), "migrate")
	p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: "sidecar"})
	app := runningStatus("app", true)
	app.RestartCount = 1
	app.LastTerminationState.Terminated = terminated("Error", 1, 0)
	p.Status.InitContainerStatuses = []corev1.ContainerStatus{{Name: "migrate", State: corev1.ContainerState{Terminated: terminated("Completed", 0, 0)}}}
	p.Status.ContainerStatuses = []corev1.ContainerStatus{app, runningStatus("sidecar", true)}
	return p
}

func streamKeyOf(s *Source) (streamKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.stream == nil {
		return streamKey{}, false
	}
	return s.stream.key, true
}

func TestPodLogsSelectsContainerAndPrevious(t *testing.T) {
	s := startWith(t, nil, func(s *Source) { s.manualPoll = true }, multiPod())
	const key = "apps/web-1"
	waitFor(t, func() bool { return len(s.PodLogs(data.LogRequest{Pod: key}, 0, 10).Lines) > 0 })
	v := s.PodLogs(data.LogRequest{Pod: key}, 0, 10)
	if v.Container != "app" || v.Previous || v.State != data.FeedReady {
		t.Fatalf("default view = %+v", v)
	}
	first, _ := streamKeyOf(s)
	s.mu.RLock()
	firstStream := s.stream
	s.mu.RUnlock()

	v = s.PodLogs(data.LogRequest{Pod: key, Container: "migrate"}, 0, 10)
	if v.Container != "migrate" {
		t.Fatalf("init container must be selectable: %+v", v)
	}
	second, _ := streamKeyOf(s)
	if second == first || second.container != "migrate" {
		t.Fatalf("stream key = %+v", second)
	}
	select {
	case <-firstStream.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("changing target must cancel the previous subscription")
	}

	waitFor(t, func() bool {
		return len(s.PodLogs(data.LogRequest{Pod: key, Container: "app", Previous: true}, 0, 10).Lines) > 0
	})
	prev, _ := streamKeyOf(s)
	if !prev.previous || prev.instance != 1 {
		t.Fatalf("previous key = %+v", prev)
	}
	v = s.PodLogs(data.LogRequest{Pod: key, Container: "sidecar", Previous: true}, 0, 10)
	if v.State != data.FeedUnavailable || !strings.Contains(v.Reason, "sem instância anterior") {
		t.Fatalf("previous without termination = %+v", v)
	}
	if _, active := streamKeyOf(s); active {
		t.Fatal("unavailable previous must not keep a subscription")
	}
	v = s.PodLogs(data.LogRequest{Pod: key, Container: "ghost"}, 0, 10)
	if v.Container != "app" {
		t.Fatalf("unknown container falls back to the default: %+v", v)
	}
}

type logCapture struct {
	mu   sync.Mutex
	opts []corev1.PodLogOptions
}

func (c *logCapture) react(a k8stesting.Action) (bool, runtime.Object, error) {
	if g, ok := a.(k8stesting.GenericAction); ok && a.GetSubresource() == "log" {
		if o, ok := g.GetValue().(*corev1.PodLogOptions); ok {
			c.mu.Lock()
			c.opts = append(c.opts, *o)
			c.mu.Unlock()
		}
	}
	return false, nil, nil
}

func (c *logCapture) last() (corev1.PodLogOptions, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.opts) == 0 {
		return corev1.PodLogOptions{}, false
	}
	return c.opts[len(c.opts)-1], true
}

func TestPodLogsRequestOptions(t *testing.T) {
	capture := &logCapture{}
	s := startWith(t, nil, func(s *Source) {
		s.manualPoll = true
		s.client.(*fake.Clientset).PrependReactor("get", "pods", capture.react)
	}, multiPod())
	s.PodLogs(data.LogRequest{Pod: "apps/web-1", Container: "sidecar"}, 0, 10)
	waitFor(t, func() bool { o, ok := capture.last(); return ok && o.Container == "sidecar" })
	if o, _ := capture.last(); !o.Follow || o.Previous || !o.Timestamps {
		t.Fatalf("current options = %+v", o)
	}
	s.PodLogs(data.LogRequest{Pod: "apps/web-1", Container: "app", Previous: true}, 0, 10)
	waitFor(t, func() bool { o, ok := capture.last(); return ok && o.Previous })
	if o, _ := capture.last(); o.Follow || o.Container != "app" || o.TailLines == nil || *o.TailLines != logTail {
		t.Fatalf("previous must be a finite read: %+v", o)
	}
	waitFor(t, func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.stream != nil && s.stream.done
	})
}

func TestClipBoundsRemoteText(t *testing.T) {
	got := clip("previous terminated container \"app\" not found\n\x1b[2J\ttrace " + strings.Repeat("é", maxDiagMessage))
	if strings.ContainsAny(got, "\n\x1b\t") || len(got) > maxDiagMessage+len("…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("clip = %q", got)
	}
}

func TestWaitingContainerSkipsAPI(t *testing.T) {
	p := testPod("apps", "img", "node-a", corev1.PodPending)
	p.Status.ContainerStatuses = []corev1.ContainerStatus{waiting("app", "ImagePullBackOff", "", 0)}
	s := startWith(t, nil, func(s *Source) { s.manualPoll = true }, p)
	v := s.PodLogs(data.LogRequest{Pod: "apps/img"}, 0, 10)
	if v.State != data.FeedUnavailable || v.Reason != "container app aguardando: ImagePullBackOff" {
		t.Fatalf("view = %+v", v)
	}
	if _, active := streamKeyOf(s); active {
		t.Fatal("waiting container without history must not open a stream")
	}
}

func event(ns, name, kind, obj string, uid types.UID, typ string, at time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID("ev-" + name)},
		InvolvedObject: corev1.ObjectReference{Kind: kind, Namespace: ns, Name: obj, UID: uid},
		Type:           typ,
		Reason:         "R" + name,
		Message:        "m " + name,
		Count:          3,
		LastTimestamp:  metav1.NewTime(at),
	}
}

func TestScopedEvents(t *testing.T) {
	objs := []runtime.Object{
		testPod("apps", "web-1", "node-a", corev1.PodRunning),
		testPod("other", "db-0", "node-a", corev1.PodRunning),
		event("apps", "e1", "Pod", "web-1", "uid-web-1", "Normal", fixedNow.Add(-time.Minute)),
		event("apps", "e2", "Pod", "web-1", "uid-web-1", "Warning", fixedNow.Add(-time.Hour)),
		event("apps", "e3", "Pod", "web-1", "uid-old", "Warning", fixedNow),
		event("apps", "e4", "Deployment", "web", "", "Normal", fixedNow),
		event("other", "e5", "Pod", "db-0", "uid-db-0", "Warning", fixedNow),
		event("", "e6", "Node", "node-a", "", "Normal", fixedNow),
	}
	s := startWith(t, nil, func(s *Source) { s.manualPoll = true }, objs...)
	pod := s.ScopedEvents(data.EventQuery{Scope: data.ScopePod, Pod: "apps/web-1"})
	if len(pod.Items) != 2 || pod.Items[0].Reason != "Re2" || pod.Items[0].Object != "Pod/web-1" || pod.Items[0].Count != 3 {
		t.Fatalf("pod scope must filter recreated UIDs and sort warnings first: %+v", pod.Items)
	}
	ns := s.ScopedEvents(data.EventQuery{Scope: data.ScopeNamespace, Namespace: "apps"})
	if len(ns.Items) != 4 || ns.Target != "apps" || ns.Items[0].Type != "Warning" {
		t.Fatalf("namespace scope = %+v", ns.Items)
	}
	cluster := s.ScopedEvents(data.EventQuery{Scope: data.ScopeCluster})
	if len(cluster.Items) != 6 || cluster.State != data.FeedReady {
		t.Fatalf("cluster scope = %d %+v", len(cluster.Items), cluster)
	}
	if none := s.ScopedEvents(data.EventQuery{Scope: data.ScopeNamespace}); none.State != data.FeedUnavailable || none.Reason != reasonNoNamespace {
		t.Fatalf("namespace without target = %+v", none)
	}
}

func TestEventsCarryStableIdentity(t *testing.T) {
	objs := []runtime.Object{
		testPod("apps", "web-1", "node-a", corev1.PodRunning),
		event("apps", "e1", "Pod", "web-1", "uid-web-1", "Normal", fixedNow.Add(-time.Minute)),
		event("apps", "e2", "Pod", "web-1", "uid-web-1", "Warning", fixedNow.Add(-time.Hour)),
	}
	s := startWith(t, nil, func(s *Source) { s.manualPoll = true }, objs...)
	for _, q := range []data.EventQuery{{Scope: data.ScopePod, Pod: "apps/web-1"}, {Scope: data.ScopeNamespace, Namespace: "apps"}, {Scope: data.ScopeCluster}} {
		feed := s.ScopedEvents(q)
		if len(feed.Items) != 2 {
			t.Fatalf("%v feed = %+v", q.Scope, feed)
		}
		for _, e := range feed.Items {
			if e.UID != "ev-"+strings.TrimPrefix(e.Reason, "R") {
				t.Fatalf("%v event %s must carry the object UID, got %q", q.Scope, e.Reason, e.UID)
			}
		}
	}
}

func TestEventOnlyChangeNotifies(t *testing.T) {
	s := startWith(t, nil, func(s *Source) { s.manualPoll = true }, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	for len(s.Changes()) > 0 {
		<-s.Changes()
	}
	time.Sleep(2 * rebuildEvery)
	for len(s.Changes()) > 0 {
		<-s.Changes()
	}
	if _, err := s.client.CoreV1().Events("apps").Create(context.Background(), event("apps", "new", "Pod", "web-1", "uid-web-1", "Warning", fixedNow), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Changes():
	case <-time.After(5 * time.Second):
		t.Fatal("event changes must notify the view")
	}
	waitFor(t, func() bool {
		return len(s.ScopedEvents(data.EventQuery{Scope: data.ScopePod, Pod: "apps/web-1"}).Items) == 1
	})
}

func TestEventsForbiddenIsNotEmpty(t *testing.T) {
	s := startWith(t, nil, func(s *Source) { s.manualPoll = true }, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	s.eventInf = nil
	for _, scope := range data.EventScopes() {
		feed := s.ScopedEvents(data.EventQuery{Scope: scope, Pod: "apps/web-1", Namespace: "apps"})
		if feed.State != data.FeedForbidden || feed.Reason != reasonEventsDeny {
			t.Fatalf("%v: %+v", scope, feed)
		}
	}
}

func namespacedStart(t *testing.T, setup func(*Source), objs ...runtime.Object) *Source {
	t.Helper()
	return startWith(t, nil, func(s *Source) {
		s.manualPoll = true
		s.opts.Namespace = "apps"
		if setup != nil {
			setup(s)
		}
	}, objs...)
}

func TestClusterEventsAreLazyWhenNamespaced(t *testing.T) {
	s := namespacedStart(t, nil,
		testPod("apps", "web-1", "node-a", corev1.PodRunning),
		event("apps", "e1", "Pod", "web-1", "", "Normal", fixedNow),
		event("other", "e2", "Pod", "x", "", "Warning", fixedNow))
	s.mu.RLock()
	lazy := s.wide == nil
	s.mu.RUnlock()
	if !lazy {
		t.Fatal("no cluster-wide subscription before it is requested")
	}
	if out := s.ScopedEvents(data.EventQuery{Scope: data.ScopeNamespace, Namespace: "other"}); out.State != data.FeedUnavailable || !strings.Contains(out.Reason, "--namespace apps") {
		t.Fatalf("out-of-scope namespace = %+v", out)
	}
	first := s.ScopedEvents(data.EventQuery{Scope: data.ScopeCluster})
	if first.State != data.FeedLoading {
		t.Fatalf("first cluster request starts loading: %+v", first)
	}
	waitFor(t, func() bool { return s.ScopedEvents(data.EventQuery{Scope: data.ScopeCluster}).State == data.FeedReady })
	if got := s.ScopedEvents(data.EventQuery{Scope: data.ScopeCluster}); len(got.Items) != 2 || got.Items[0].Namespace != "other" {
		t.Fatalf("cluster events = %+v", got.Items)
	}
}

func TestClusterEventsForbiddenWhenNamespaced(t *testing.T) {
	s := namespacedStart(t, nil, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	s.client.(*fake.Clientset).PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == "" {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "events"}, "", errors.New("denied"))
		}
		return false, nil, nil
	})
	s.ScopedEvents(data.EventQuery{Scope: data.ScopeCluster})
	waitFor(t, func() bool {
		return s.ScopedEvents(data.EventQuery{Scope: data.ScopeCluster}).State == data.FeedForbidden
	})
	if got := s.ScopedEvents(data.EventQuery{Scope: data.ScopeCluster}); got.Reason != reasonClusterDeny {
		t.Fatalf("reason = %q", got.Reason)
	}
}

func TestEventMessageLoadsFullTextOnDemand(t *testing.T) {
	long := event("apps", "long", "Pod", "web-1", "", "Warning", fixedNow)
	long.Message = strings.Repeat("palavra\x1b[2J ", 200) + "fim"
	huge := event("apps", "huge", "Pod", "web-1", "", "Warning", fixedNow)
	huge.Message = strings.Repeat("é", 12<<10)
	other := event("other", "far", "Pod", "x", "", "Warning", fixedNow)
	other.Message = strings.Repeat("distante ", 100)
	s := namespacedStart(t, nil, testPod("apps", "web-1", "node-a", corev1.PodRunning), long, huge, other)

	feed := s.ScopedEvents(data.EventQuery{Scope: data.ScopeNamespace, Namespace: "apps"})
	for _, e := range feed.Items {
		if !e.Truncated || len(e.Message) > data.EventPreviewBytes+len("…") {
			t.Fatalf("the feed keeps a bounded preview of %s: truncated=%v len=%d", e.UID, e.Truncated, len(e.Message))
		}
	}
	full, ok := s.EventMessage("ev-long")
	want := strings.TrimSpace(strings.Repeat("palavra ", 200)) + " fim"
	if !ok || full.Truncated || full.Text != want {
		t.Fatalf("the opened event loads its whole sanitized message: ok=%v truncated=%v len=%d", ok, full.Truncated, len(full.Text))
	}
	capped, ok := s.EventMessage("ev-huge")
	if !ok || !capped.Truncated || len(capped.Text) > data.EventMessageBytes+len("…") || !utf8.ValidString(capped.Text) {
		t.Fatalf("the full message is capped explicitly: ok=%v truncated=%v len=%d", ok, capped.Truncated, len(capped.Text))
	}
	if _, ok := s.EventMessage("ev-far"); ok {
		t.Fatal("an event outside the connected namespace is not served before the cluster feed is opened")
	}
	if _, ok := s.EventMessage("missing"); ok {
		t.Fatal("unknown UID")
	}
	s.ScopedEvents(data.EventQuery{Scope: data.ScopeCluster})
	waitFor(t, func() bool { return s.ScopedEvents(data.EventQuery{Scope: data.ScopeCluster}).State == data.FeedReady })
	if far, ok := s.EventMessage("ev-far"); !ok || far.Text != strings.TrimSpace(other.Message) {
		t.Fatalf("after the explicit cluster feed the wide cache serves the message: ok=%v", ok)
	}
}

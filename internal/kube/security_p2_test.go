package kube

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/watch"

	"k8s.io/client-go/kubernetes/fake"
	typedapps "k8s.io/client-go/kubernetes/typed/apps/v1"
	typedcore "k8s.io/client-go/kubernetes/typed/core/v1"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/TNK-Studio/lazykube/internal/data"
)

func TestSecurityRecoversAfterRelist(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "unavailable", true: "denied"}[denied], func(t *testing.T) {
			client := fake.NewClientset(hardenedPod())
			watches := make(chan *watch.RaceFreeFakeWatcher, 10)
			client.PrependWatchReactor("pods", func(k8stesting.Action) (bool, watch.Interface, error) {
				w := watch.NewRaceFreeFake()
				watches <- w
				return true, w, nil
			})
			s := newSource(client, nil, Options{Context: "test", SyncTimeout: time.Second})
			s.manualPoll = true
			if err := s.start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			w := <-watches
			err := apierrors.NewServiceUnavailable("temporary")
			if denied {
				err = apierrors.NewForbidden(corev1.Resource("pods"), "", nil)
			}
			s.watchSecurityError(false)(nil, err)
			waitFor(t, func() bool { return s.Security().Coverage.State != data.CoverageComplete })
			w.Error(&metav1.Status{Status: metav1.StatusFailure, Code: 410, Reason: metav1.StatusReasonExpired})
			waitFor(t, func() bool { return s.Security().Coverage.State == data.CoverageComplete })
			if got := s.Security().Pods["apps/safe"].Coverage; got.State != data.CoverageComplete || got.Reason != "" {
				t.Fatalf("pod retained failure: %+v", got)
			}
		})
	}
}

type slowDeploymentClient struct {
	*fake.Clientset
	expired atomic.Bool
}
type slowApps struct {
	typedapps.AppsV1Interface
	owner *slowDeploymentClient
}
type slowDeployments struct {
	typedapps.DeploymentInterface
	owner *slowDeploymentClient
}

func (c *slowDeploymentClient) AppsV1() typedapps.AppsV1Interface {
	return slowApps{c.Clientset.AppsV1(), c}
}
func (c slowApps) Deployments(ns string) typedapps.DeploymentInterface {
	return slowDeployments{c.AppsV1Interface.Deployments(ns), c.owner}
}
func (c slowDeployments) List(ctx context.Context, _ metav1.ListOptions) (*appsv1.DeploymentList, error) {
	<-ctx.Done()
	c.owner.expired.Store(true)
	return nil, ctx.Err()
}

func TestOptionalDeploymentProbeHasOwnBudget(t *testing.T) {
	client := fake.NewClientset(hardenedPod())
	slow := &slowDeploymentClient{Clientset: client}
	s := newSource(slow, nil, Options{SyncTimeout: 200 * time.Millisecond})
	s.manualPoll = true
	if err := s.start(context.Background()); err != nil {
		t.Fatalf("optional timeout aborted probe: %v", err)
	}
	defer s.Close()
	if !s.caps.events || !s.caps.nodes || !s.caps.namespaces || s.caps.deployments {
		t.Fatal("unexpected capabilities")
	}
}

type deadlineCore struct{ typedcore.CoreV1Interface }
type deadlineEvents struct{ typedcore.EventInterface }

func (c *slowDeploymentClient) CoreV1() typedcore.CoreV1Interface {
	return deadlineCore{c.Clientset.CoreV1()}
}
func (c deadlineCore) Events(ns string) typedcore.EventInterface {
	return deadlineEvents{c.CoreV1Interface.Events(ns)}
}
func (c deadlineEvents) List(ctx context.Context, opts metav1.ListOptions) (*corev1.EventList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.EventInterface.List(ctx, opts)
}

func TestDeploymentCoverageRecoversAfterRelist(t *testing.T) {
	client := fake.NewClientset(hardenedPod(), &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "apps", UID: "web", ResourceVersion: "1"}})
	watches := make(chan *watch.RaceFreeFakeWatcher, 10)
	client.PrependWatchReactor("deployments", func(k8stesting.Action) (bool, watch.Interface, error) {
		w := watch.NewRaceFreeFake()
		watches <- w
		return true, w, nil
	})
	s := newSource(client, nil, Options{SyncTimeout: time.Second})
	s.manualPoll = true
	if err := s.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := <-watches
	s.watchSecurityError(true)(nil, apierrors.NewServiceUnavailable("temporary"))
	waitFor(t, func() bool { return s.Snapshot().DeploymentCoverage.State == data.CoverageStale })
	w.Error(&metav1.Status{Status: metav1.StatusFailure, Code: 410, Reason: metav1.StatusReasonExpired})
	waitFor(t, func() bool { return s.Snapshot().DeploymentCoverage.State == data.CoverageComplete })
}

func TestRecoveryRequiresConsumedListAndWatch(t *testing.T) {
	p := hardenedPod()
	tracked := &securityInformer{SharedIndexInformer: cache.NewSharedIndexInformer(nil, &corev1.Pod{}, 0, nil), dirty: func() {}}
	check := func(want bool, objects ...any) {
		t.Helper()
		tracked.mu.Lock()
		got := tracked.consumed(objects)
		tracked.mu.Unlock()
		if got != want {
			t.Fatalf("consumed=%t want %t", got, want)
		}
	}
	tracked.listedPage(&corev1.PodList{ListMeta: metav1.ListMeta{Continue: "next"}, Items: []corev1.Pod{*p}}, true)
	upstream := watch.NewRaceFreeFake()
	stream := tracked.observe(context.Background(), upstream, false)
	defer stream.Stop()
	check(false, p)
	tracked.listedPage(&corev1.PodList{}, false)
	check(false)
	check(true, p)
	updated := p.DeepCopy()
	updated.ResourceVersion = "2"
	upstream.Modify(updated)
	<-stream.ResultChan()
	check(false, p)
	check(true, updated)
	upstream.Delete(updated)
	<-stream.ResultChan()
	check(true)
	tracked.invalidate()
	check(false)
}

func TestWatchListRecoveryRequiresBookmark(t *testing.T) {
	p := hardenedPod()
	tracked := &securityInformer{dirty: func() {}}
	upstream := watch.NewRaceFreeFake()
	stream := tracked.observe(context.Background(), upstream, true)
	defer stream.Stop()
	upstream.Add(p)
	<-stream.ResultChan()
	tracked.mu.Lock()
	before := tracked.consumed([]any{p})
	tracked.mu.Unlock()
	if before {
		t.Fatal("incomplete watch-list accepted")
	}
	upstream.Action(watch.Bookmark, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"k8s.io/initial-events-end": "true"}}})
	<-stream.ResultChan()
	tracked.mu.Lock()
	after := tracked.consumed([]any{p})
	tracked.mu.Unlock()
	if !after {
		t.Fatal("completed watch-list not accepted")
	}
}

func TestSecurityRevisionChangesOnlyWithPosture(t *testing.T) {
	s := newSource(fake.NewClientset(), nil, Options{})
	s.podInf = cache.NewSharedIndexInformer(nil, &corev1.Pod{}, 0, nil)
	s.podCoverage.State = data.CoverageComplete
	p := hardenedPod()
	s.rebuildSecurity([]*corev1.Pod{p}, nil, fixedNow)
	revision := s.SecurityRevision()
	s.rebuildSecurity([]*corev1.Pod{p}, nil, fixedNow.Add(time.Minute))
	if revision != s.SecurityRevision() || s.Security().Revision != revision {
		t.Fatal("unchanged posture changed revision")
	}
	s.watchSecurityError(false)(nil, apierrors.NewServiceUnavailable("temporary"))
	s.rebuildSecurity([]*corev1.Pod{p}, nil, fixedNow.Add(2*time.Minute))
	if s.SecurityRevision() <= revision {
		t.Fatal("coverage change did not invalidate presentation")
	}
}

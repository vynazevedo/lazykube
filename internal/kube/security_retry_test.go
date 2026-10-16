package kube

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/TNK-Studio/lazykube/internal/data"
)

func TestSecurityWatchRetriesDowngradeCoverage(t *testing.T) {
	for _, resource := range []string{"pods", "deployments"} {
		t.Run(resource, func(t *testing.T) {
			p := hardenedPod()
			dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "apps", UID: "web", ResourceVersion: "1"}}
			client := fake.NewClientset(p, dep)
			first := watch.NewRaceFreeFake()
			recovered := watch.NewRaceFreeFake()
			var calls atomic.Int32
			var retry atomic.Bool
			retry.Store(true)
			client.PrependWatchReactor(resource, func(k8stesting.Action) (bool, watch.Interface, error) {
				if calls.Add(1) == 1 {
					return true, first, nil
				}
				if retry.Load() {
					return true, nil, apierrors.NewTooManyRequests("temporary", 0)
				}
				return true, recovered, nil
			})
			s := newSource(client, nil, Options{SyncTimeout: time.Second})
			s.manualPoll = true
			if err := s.start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			coverage := func() data.Coverage {
				if resource == "deployments" {
					return s.Snapshot().DeploymentCoverage
				}
				return s.Security().Coverage
			}
			waitFor(t, func() bool { return coverage().State == data.CoverageComplete })
			var object runtime.Object = p
			if resource == "deployments" {
				object = dep
			}
			first.Modify(object)
			first.Stop()
			waitFor(t, func() bool { return calls.Load() > 1 })
			waitFor(t, func() bool { return coverage().State == data.CoverageStale })
			if coverage().Reason == "" {
				t.Fatal("retry failure reason missing")
			}
			retry.Store(false)
			waitFor(t, func() bool { return coverage().State == data.CoverageComplete })
		})
	}
}

func TestSecurityDeploymentRequiresFirstWatch(t *testing.T) {
	client := fake.NewClientset(hardenedPod(), &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "apps"}})
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	client.PrependWatchReactor("deployments", func(k8stesting.Action) (bool, watch.Interface, error) {
		once.Do(func() { close(entered) })
		<-release
		return true, watch.NewRaceFreeFake(), nil
	})
	s := newSource(client, nil, Options{SyncTimeout: time.Second})
	s.factory = informers.NewSharedInformerFactory(client, 0)
	s.podInf = cache.NewSharedIndexInformer(nil, &corev1.Pod{}, 0, nil)
	s.podCoverage.State = data.CoverageComplete
	if err := s.podInf.GetStore().Add(hardenedPod()); err != nil {
		t.Fatal(err)
	}
	s.deployInf = s.newSecurityInformer(true)
	defer s.Close()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	s.factory.Start(s.ctx.Done())
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("watch not attempted")
	}
	waitFor(t, func() bool { return s.deployInf.HasSynced() })
	s.rebuild()
	if len(s.Snapshot().Deployments) != 1 {
		t.Fatal("deployment list not consumed")
	}
	if got := s.Snapshot().DeploymentCoverage.State; got != data.CoverageCollecting {
		t.Fatalf("coverage before first watch = %q", got)
	}
	unblock()
	tracked := s.deployInf.(*securityInformer)
	waitFor(t, func() bool { tracked.mu.Lock(); defer tracked.mu.Unlock(); return tracked.watching })
	s.rebuild()
	if s.Snapshot().DeploymentCoverage.State != data.CoverageComplete {
		t.Fatal("confirmed watch did not restore coverage")
	}
}

func TestSecurityDeploymentProbeTimeoutReason(t *testing.T) {
	client := &slowDeploymentClient{Clientset: fake.NewClientset(hardenedPod())}
	s := newSource(client, nil, Options{SyncTimeout: 200 * time.Millisecond})
	if err := s.probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.deploymentCoverage.Reason != "timeout: deployments" {
		t.Fatalf("timeout reason = %q", s.deploymentCoverage.Reason)
	}
}

func TestSecurityNormalReconnectDoesNotReportFailure(t *testing.T) {
	var failures atomic.Int32
	tracked := &securityInformer{dirty: func() {}, reportFailure: func(error) { failures.Add(1) }}
	upstream := watch.NewRaceFreeFake()
	stream := tracked.observe(context.Background(), upstream, false)
	defer stream.Stop()
	upstream.Stop()
	select {
	case _, ok := <-stream.ResultChan():
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(time.Second):
		t.Fatal("watch did not close")
	}
	if failures.Load() != 0 {
		t.Fatal("normal reconnect was classified as failure")
	}
}

func TestSecurityStreamErrorReportsFailure(t *testing.T) {
	reported := make(chan error, 1)
	tracked := &securityInformer{dirty: func() {}, reportFailure: func(err error) { reported <- err }}
	upstream := watch.NewRaceFreeFake()
	stream := tracked.observe(context.Background(), upstream, false)
	defer stream.Stop()
	upstream.Error(&metav1.Status{Status: metav1.StatusFailure, Reason: metav1.StatusReasonTooManyRequests, Code: 429})
	select {
	case err := <-reported:
		if !apierrors.IsTooManyRequests(err) {
			t.Fatalf("wrong error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream error was not reported")
	}
	<-stream.ResultChan()
	tracked.mu.Lock()
	watching := tracked.watching
	tracked.mu.Unlock()
	if watching {
		t.Fatal("failed stream still marked active")
	}
}

func TestSecurityListFailureReportsCoverage(t *testing.T) {
	client := fake.NewClientset()
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewTooManyRequests("temporary", 0)
	})
	s := newSource(client, nil, Options{})
	s.factory = informers.NewSharedInformerFactory(client, 0)
	s.podInf = s.newSecurityInformer(false)
	s.podCoverage.State = data.CoverageComplete
	defer s.Close()
	s.factory.Start(s.ctx.Done())
	waitFor(t, func() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.podCoverage.State == data.CoverageStale })
}

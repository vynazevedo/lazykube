package kube

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/TNK-Studio/lazykube/internal/data"
)

func TestSecurityCacheIdentityAndAge(t *testing.T) {
	s := newSource(fake.NewClientset(), nil, Options{Context: "test"})
	s.podInf = cache.NewSharedIndexInformer(nil, &corev1.Pod{}, 0, nil)
	s.podCoverage.State = data.CoverageComplete
	p := hardenedPod()
	other := p.DeepCopy()
	other.Namespace = "other"
	other.UID = "other-uid"
	pods := []*corev1.Pod{p, other}
	names := []data.Namespace{{Name: "empty"}}
	s.rebuildSecurity(pods, names, fixedNow)
	first := s.Security()
	if len(first.Pods) != 2 || len(first.Namespaces) != 3 || first.Namespaces["empty"].Coverage.Total != 0 {
		t.Fatalf("scope lost: %+v", first)
	}
	s.rebuildSecurity(pods, names, fixedNow.Add(time.Hour))
	if !reflect.DeepEqual(first, s.Security()) {
		t.Fatal("age refresh recalculated posture")
	}
	p = p.DeepCopy()
	p.ResourceVersion = "2"
	p.Spec.HostPID = true
	pods[0] = p
	s.rebuildSecurity(pods, names, fixedNow.Add(2*time.Hour))
	got := s.Security().Pods["apps/safe"]
	if _, ok := rule(got, "hostPID"); !ok || got.Target.ResourceVersion != "2" {
		t.Fatal("version change not evaluated")
	}
	p = hardenedPod()
	p.UID = "replacement"
	p.ResourceVersion = "2"
	pods[0] = p
	s.rebuildSecurity(pods, names, fixedNow.Add(3*time.Hour))
	got = s.Security().Pods["apps/safe"]
	if len(got.Findings) != 0 || got.Target.UID != "replacement" {
		t.Fatalf("recreated pod inherited findings: %+v", got)
	}
	s.rebuildSecurity(pods[1:], nil, fixedNow.Add(4*time.Hour))
	if _, ok := s.Security().Pods["apps/safe"]; ok {
		t.Fatal("deleted pod retained")
	}
	if _, ok := s.Security().Namespaces["empty"]; ok {
		t.Fatal("deleted namespace retained")
	}
	copy := s.Security()
	delete(copy.Pods, "other/safe")
	if len(s.Security().Pods) != 1 {
		t.Fatal("caller mutated snapshot")
	}
}

func TestSecurityCacheMissingUIDAndVersion(t *testing.T) {
	s := newSource(fake.NewClientset(), nil, Options{})
	s.podInf = cache.NewSharedIndexInformer(nil, &corev1.Pod{}, 0, nil)
	s.podCoverage.State = data.CoverageComplete
	p := hardenedPod()
	p.UID = ""
	p.ResourceVersion = ""
	s.rebuildSecurity([]*corev1.Pod{p}, nil, fixedNow)
	first := s.Security()
	s.rebuildSecurity([]*corev1.Pod{p}, nil, fixedNow.Add(time.Hour))
	if !reflect.DeepEqual(first, s.Security()) {
		t.Fatal("unchanged object recalculated")
	}
	p = p.DeepCopy()
	p.Spec.HostNetwork = true
	s.rebuildSecurity([]*corev1.Pod{p}, nil, fixedNow.Add(2*time.Hour))
	if _, ok := rule(s.Security().Pods["apps/safe"], "hostNetwork"); !ok {
		t.Fatal("unversioned replacement missed")
	}
	if s.Security().Coverage.State != data.CoveragePartial {
		t.Fatal("identity coverage is not partial")
	}
}

func TestPostureWithoutDeploymentPermission(t *testing.T) {
	for _, denied := range []error{apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "", errors.New("denied")), errors.New("service unavailable")} {
		client := fake.NewClientset(hardenedPod())
		client.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, denied })
		s := newSource(client, nil, Options{Context: "test", Namespace: "apps", SyncTimeout: time.Second})
		s.manualPoll = true
		if err := s.start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if s.Snapshot().DeploymentCoverage.State == data.CoverageComplete || s.Security().Coverage.Evaluated != 1 {
			t.Fatal("deployment denial prevented posture or was hidden")
		}
		for _, a := range client.Actions() {
			if a.GetVerb() == "get" && a.GetResource().Resource == "version" {
				continue
			}
			if a.GetVerb() != "list" && a.GetVerb() != "watch" {
				t.Fatalf("unexpected request: %s %s", a.GetVerb(), a.GetResource())
			}
			if a.GetVerb() == "watch" && a.GetResource().Resource == "deployments" {
				t.Fatal("watch started after list denial")
			}
		}
		s.Close()
	}
}

func TestPostureWatchUpdatesAndDelete(t *testing.T) {
	p := hardenedPod()
	s := startWith(t, nil, func(s *Source) { s.manualPoll = true }, p)
	client := s.client.(*fake.Clientset)
	p = p.DeepCopy()
	p.ResourceVersion = "2"
	p.Spec.HostIPC = true
	if _, err := client.CoreV1().Pods("apps").Update(context.Background(), p, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, ok := rule(s.Security().Pods["apps/safe"], "hostIPC"); return ok })
	if err := client.CoreV1().Pods("apps").Delete(context.Background(), "safe", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(s.Security().Pods) == 0 })
}

func TestSecurityWatchErrorCoverage(t *testing.T) {
	for _, tc := range []struct {
		err   error
		state data.CoverageState
	}{
		{errors.New("token-sentinel"), data.CoverageStale},
		{apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("token-sentinel")), data.CoverageDenied},
	} {
		s := newSource(fake.NewClientset(), nil, Options{})
		s.podInf = cache.NewSharedIndexInformer(nil, &corev1.Pod{}, 0, nil)
		s.podCoverage.State = data.CoverageComplete
		pods := []*corev1.Pod{hardenedPod()}
		s.rebuildSecurity(pods, nil, fixedNow)
		s.watchSecurityError(false)(nil, tc.err)
		s.rebuildSecurity(pods, nil, fixedNow.Add(time.Minute))
		got := s.Security()
		if got.Coverage.State != tc.state || got.Pods["apps/safe"].Coverage.State != tc.state {
			t.Fatalf("error hidden: %+v", got)
		}
		if got.Pods["apps/safe"].Coverage.ObservedAt != fixedNow {
			t.Fatal("stale observation got new timestamp")
		}
		s.watchSecurityError(true)(nil, tc.err)
		if s.deploymentCoverage.State != tc.state {
			t.Fatal("deployment error hidden")
		}
	}
}

func BenchmarkSecurityCacheUnchanged(b *testing.B) {
	for _, count := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s := newSource(fake.NewClientset(), nil, Options{})
			s.podInf = cache.NewSharedIndexInformer(nil, &corev1.Pod{}, 0, nil)
			s.podCoverage.State = data.CoverageComplete
			pods := make([]*corev1.Pod, count)
			for i := range pods {
				p := hardenedPod()
				p.Name = fmt.Sprintf("pod-%d", i)
				pods[i] = p
			}
			s.rebuildSecurity(pods, nil, fixedNow)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				s.rebuildSecurity(pods, nil, fixedNow.Add(time.Hour))
			}
		})
	}
}

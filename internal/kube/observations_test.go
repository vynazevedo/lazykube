package kube

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

func TestObservationSourceUsesCacheWithoutIO(t *testing.T) {
	now := fixedNow
	client := fake.NewClientset()
	client.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		t.Fatalf("unexpected API action %s %s", action.GetVerb(), action.GetResource().Resource)
		return true, nil, nil
	})
	s := newSource(client, nil, Options{Context: "fake", Namespace: "apps", Now: func() time.Time { return now }})
	defer s.cancel()
	s.podInf = cache.NewSharedIndexInformer(&cache.ListWatch{}, &corev1.Pod{}, 0, cache.Indexers{})
	s.podCoverage = data.Coverage{State: data.CoverageComplete}
	p := hardenedPod()
	p.Status.Phase = corev1.PodPending
	p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: p.Spec.Containers[0].Name, RestartCount: 7, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}, LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(now.Add(-time.Minute))}}}}
	if err := s.podInf.GetStore().Add(p); err != nil {
		t.Fatal(err)
	}
	s.rebuild()
	first := s.Observations(insight.Cursor{})
	if len(first.Pods) != 1 || first.Pods[0].Phase != insight.PodPending || first.Pods[0].Containers[0].Reason != insight.ReasonCrashLoopBackOff || first.Pods[0].Containers[0].LastTermination.Reason != insight.ReasonOOMKilled {
		t.Fatalf("projection: %+v", first)
	}
	if first.Coverage.Inventory.State != insight.CoverageComplete || first.Coverage.Network.State != insight.CoverageDisabled || first.Synthetic {
		t.Fatal("source coverage wrong")
	}
	now = now.Add(time.Hour)
	s.rebuild()
	if !reflect.DeepEqual(first, s.Observations(insight.Cursor{})) {
		t.Fatal("age refresh changed observations")
	}
	if !s.Observations(first.Cursor).Unchanged {
		t.Fatal("cursor replay not recognized")
	}
	replacement := p.DeepCopy()
	replacement.UID = "replacement"
	replacement.ResourceVersion = "new"
	replacement.Status.ContainerStatuses[0].RestartCount = 0
	if err := s.podInf.GetStore().Update(replacement); err != nil {
		t.Fatal(err)
	}
	s.rebuild()
	next := s.Observations(first.Cursor)
	if next.Pods[0].Entity.UID == first.Pods[0].Entity.UID || next.Pods[0].FirstObservedAt != now || next.Pods[0].Containers[0].RestartCount != 0 {
		t.Fatal("recreated UID retained previous state")
	}
	s.podCoverage = data.Coverage{State: data.CoverageDenied, Reason: "denied"}
	s.rebuild()
	if s.Observations(next.Cursor).Coverage.Inventory.State != insight.CoverageDenied {
		t.Fatal("coverage loss hidden")
	}
	if len(client.Actions()) != 0 {
		t.Fatal("observation path must not read API")
	}
}

func TestObservationProjectionTypedTimesAndKinds(t *testing.T) {
	p := hardenedPod()
	p.CreationTimestamp = metav1.NewTime(fixedNow.Add(-time.Hour))
	p.DeletionTimestamp = &metav1.Time{Time: fixedNow}
	p.Status.StartTime = &metav1.Time{Time: fixedNow.Add(-time.Minute)}
	p.Spec.NodeName = "node-a"
	p.Status.Reason = "Evicted"
	p.Status.Message = "\x1b[31mmessage\x1b[0m"
	p.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "owner", UID: "owner-id", Controller: ptr(true)}}
	p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(fixedNow), LastProbeTime: metav1.NewTime(fixedNow.Add(-time.Second))}}
	p.Spec.InitContainers = []corev1.Container{{Name: "init"}, {Name: "sidecar", RestartPolicy: ptr(corev1.ContainerRestartPolicyAlways)}}
	p.Spec.EphemeralContainers = []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug"}}}
	p.Status.InitContainerStatuses = []corev1.ContainerStatus{{Name: "init", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Completed", FinishedAt: metav1.NewTime(fixedNow)}}}, {Name: "sidecar", Ready: true, Started: ptr(true), State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(fixedNow)}}}}
	source := insight.SourceIdentity{SessionID: "test", Context: "fake", Generation: 1}
	got := observePod(p, source)
	if got.CreatedAt != p.CreationTimestamp.Time || got.DeletionAt != fixedNow || got.StartedAt != p.Status.StartTime.Time || got.Reason != insight.ReasonEvicted || strings.Contains(got.Message, "\x1b") {
		t.Fatal("typed pod projection lost timestamp/reason/sanitization")
	}
	if got.NodeName != "node-a" || got.Owners[0].Entity.UID != "owner-id" || got.Owners[0].Entity.Source != source || got.Owners[0].Entity.Group != "apps" {
		t.Fatal("assignment/owner identity lost")
	}
	if got.Conditions[0].Kind != insight.ConditionReady || got.Conditions[0].Status != insight.TruthFalse || got.Conditions[0].LastTransitionAt != fixedNow {
		t.Fatal("typed condition lost")
	}
	for n, kind := range []insight.ContainerKind{insight.ContainerInit, insight.ContainerSidecar, insight.ContainerRegular, insight.ContainerEphemeral} {
		if got.Containers[n].Kind != kind {
			t.Fatal("container kinds conflated")
		}
	}
	if got.Containers[0].CurrentTermination.FinishedAt != fixedNow || got.Containers[1].StartedAt != fixedNow || got.Containers[1].Ready != insight.TruthTrue || got.Containers[2].StatusObserved || got.Containers[2].Ready != insight.TruthUnknown {
		t.Fatal("missing status presented as healthy or timestamps lost")
	}
}

func TestObservationReasonAndCoverageMappings(t *testing.T) {
	for raw, want := range map[string]insight.Reason{"": insight.ReasonUnknown, "other": insight.ReasonOther, "CrashLoopBackOff": insight.ReasonCrashLoopBackOff, "OOMKilled": insight.ReasonOOMKilled, "ImagePullBackOff": insight.ReasonImagePullBackOff, "ErrImagePull": insight.ReasonErrImagePull, "ContainerCreating": insight.ReasonContainerCreating, "PodInitializing": insight.ReasonPodInitializing, "Evicted": insight.ReasonEvicted, "NodeLost": insight.ReasonNodeLost, "Completed": insight.ReasonCompleted} {
		if observationReason(raw) != want {
			t.Fatalf("reason %q", raw)
		}
	}
	for raw, want := range map[data.CoverageState]insight.CoverageState{data.CoverageCollecting: insight.CoverageCollecting, data.CoverageComplete: insight.CoverageComplete, data.CoveragePartial: insight.CoveragePartial, data.CoverageDenied: insight.CoverageDenied, data.CoverageStale: insight.CoverageStale, data.CoverageDisabled: insight.CoverageDisabled, data.CoverageUnavailable: insight.CoverageUnavailable} {
		if observationCoverage(data.Coverage{State: raw}).State != want {
			t.Fatalf("coverage %s", raw)
		}
	}
	for raw, want := range map[corev1.PodPhase]insight.PodPhase{corev1.PodPending: insight.PodPending, corev1.PodRunning: insight.PodRunning, corev1.PodSucceeded: insight.PodSucceeded, corev1.PodFailed: insight.PodFailed, corev1.PodUnknown: insight.PodUnknown} {
		p := hardenedPod()
		p.Status.Phase = raw
		if observePod(p, insight.SourceIdentity{}).Phase != want {
			t.Fatalf("phase %s", raw)
		}
	}
}

func TestObservationProjectionBounded(t *testing.T) {
	s := newSource(fake.NewClientset(), nil, Options{Now: func() time.Time { return fixedNow }})
	defer s.cancel()
	s.podCoverage = data.Coverage{State: data.CoverageComplete}
	pods := make([]*corev1.Pod, insight.MaxInventoryPods+1)
	for i := range pods {
		pods[i] = hardenedPod()
		pods[i].Name = fmt.Sprintf("pod-%05d", i)
		pods[i].UID = types.UID(pods[i].Name)
	}
	s.publishObservations(pods)
	batch := s.Observations(insight.Cursor{})
	if len(batch.Pods) != insight.MaxInventoryPods || batch.Coverage.Inventory.Total != len(pods) || batch.Coverage.Inventory.State != insight.CoveragePartial || len(s.observationPods) > insight.MaxInventoryPods {
		t.Fatal("projection budget not enforced")
	}
}

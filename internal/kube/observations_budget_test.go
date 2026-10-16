package kube

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func observationBudgetPods(count, containers int, message string) []*corev1.Pod {
	pods := make([]*corev1.Pod, count)
	for i := range pods {
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pod-%05d", i), Namespace: "apps", UID: types.UID(fmt.Sprint(i)), ResourceVersion: "1"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
		for j := range containers {
			name := fmt.Sprintf("c-%03d", j)
			status := corev1.ContainerStatus{Name: name, Ready: true, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating", Message: message}}}
			switch j % 4 {
			case 0:
				p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: name, Image: "image:v1"})
				p.Status.ContainerStatuses = append(p.Status.ContainerStatuses, status)
			case 1:
				p.Spec.InitContainers = append(p.Spec.InitContainers, corev1.Container{Name: name})
				p.Status.InitContainerStatuses = append(p.Status.InitContainerStatuses, status)
			case 2:
				p.Spec.InitContainers = append(p.Spec.InitContainers, corev1.Container{Name: name, RestartPolicy: ptr(corev1.ContainerRestartPolicyAlways)})
				p.Status.InitContainerStatuses = append(p.Status.InitContainerStatuses, status)
			case 3:
				p.Spec.EphemeralContainers = append(p.Spec.EphemeralContainers, corev1.EphemeralContainer{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: name}})
				p.Status.EphemeralContainerStatuses = append(p.Status.EphemeralContainerStatuses, status)
			}
		}
		pods[i] = p
	}
	return pods
}

func TestObservationContainerBudget(t *testing.T) {
	s := newSource(fake.NewClientset(), nil, Options{Now: func() time.Time { return fixedNow }})
	defer s.cancel()
	s.podCoverage = data.Coverage{State: data.CoverageComplete}
	pods := observationBudgetPods(3200, 32, strings.Repeat("x", 1024))
	s.publishObservations(pods)
	batch := s.Observations(insight.Cursor{})
	containers, text := 0, 0
	for _, p := range batch.Pods {
		containers += len(p.Containers)
		for _, c := range p.Containers {
			text += len(c.Image) + len(c.ReasonText) + len(c.Message)
		}
	}
	if containers > 100000 {
		t.Errorf("snapshot retained %d containers", containers)
	}
	cached := 0
	for _, p := range s.observationPods {
		cached += len(p.Containers)
	}
	if cached > 100000 {
		t.Errorf("cache retained %d containers", cached)
	}
	if text > maxObservationEvidence {
		t.Errorf("retained evidence %d bytes", text)
	}
	if batch.Coverage.Inventory.State != insight.CoveragePartial {
		t.Error("truncation not exposed as partial coverage")
	}
}

func TestObservationAuxiliaryBudget(t *testing.T) {
	p := hardenedPod()
	for i := range 1000 {
		p.OwnerReferences = append(p.OwnerReferences, metav1.OwnerReference{APIVersion: "v1", Kind: "Pod", Name: fmt.Sprint(i), UID: types.UID(fmt.Sprint(i))})
		p.Status.Conditions = append(p.Status.Conditions, corev1.PodCondition{Type: corev1.PodConditionType(fmt.Sprint(i)), Message: strings.Repeat("x", 2048)})
	}
	got := observePod(p, insight.SourceIdentity{})
	if len(got.Owners) > 8 || len(got.Conditions) > 16 {
		t.Fatalf("auxiliary evidence unbounded: owners=%d conditions=%d", len(got.Owners), len(got.Conditions))
	}
}

func TestObservationBudgetOrderingAndReallocation(t *testing.T) {
	s := newSource(fake.NewClientset(), nil, Options{Now: func() time.Time { return fixedNow }})
	defer s.cancel()
	s.podCoverage = data.Coverage{State: data.CoverageComplete}
	pods := observationBudgetPods(3200, 32, "")
	s.publishObservations(pods)
	initial := s.Observations(insight.Cursor{})
	for i, j := 0, len(pods)-1; i < j; i, j = i+1, j-1 {
		pods[i], pods[j] = pods[j], pods[i]
	}
	s.publishObservations(pods)
	if !s.Observations(initial.Cursor).Unchanged {
		t.Fatal("input order changed projection")
	}
	s.publishObservations(pods[:1])
	restored := s.Observations(initial.Cursor)
	if len(restored.Pods) != 1 || len(restored.Pods[0].Containers) != 32 || restored.Pods[0].Omissions.Containers != 0 || restored.Coverage.Inventory.State != insight.CoverageComplete {
		t.Fatal("budget change reused truncated cached pod")
	}
}

func TestObservationEvidenceAndContainerBoundary(t *testing.T) {
	budget := observationBudget{Containers: 3, TextBytes: 5}
	observer := observationProjector{budget: &budget}
	if got := observer.text("ééé"); got != "éé" || observer.omissions.TextBytes != 2 || budget.TextBytes != 1 {
		t.Fatal("text budget split UTF-8 or lost count")
	}
	p := observationBudgetPods(1, 8, "")[0]
	got := observer.containers(p)
	if len(got) != 3 || observer.omissions.Containers != 5 || got[0].Kind != insight.ContainerInit || got[1].Kind != insight.ContainerInit || got[2].Kind != insight.ContainerSidecar {
		t.Fatal("container boundary not deterministic")
	}
}

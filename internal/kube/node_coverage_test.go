package kube

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

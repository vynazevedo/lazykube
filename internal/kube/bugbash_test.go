package kube

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

func TestBugbashNodeReadyProjection(t *testing.T) {
	for _, tc := range []struct {
		status       corev1.ConditionStatus
		ready, known bool
	}{
		{corev1.ConditionTrue, true, true}, {corev1.ConditionFalse, false, true}, {corev1.ConditionUnknown, false, false}, {"", false, false},
	} {
		n := testNode("node")
		n.Status.Conditions[0].Status = tc.status
		if tc.status == "" {
			n.Status.Conditions = nil
		}
		got := toNode(n, 0, 0, false)
		if got.Ready != tc.ready || got.ReadyKnown != tc.known {
			t.Fatalf("%s: %+v", tc.status, got)
		}
	}
}
func TestBugbashCurrentCPUKnowledge(t *testing.T) {
	client := metricsfake.NewSimpleClientset()
	list := podMetricsAt("0", fixedNow)
	var queryErr error
	client.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) { return true, list, queryErr })
	s := newSource(fake.NewClientset(), client, Options{Now: func() time.Time { return fixedNow }})
	defer s.cancel()
	s.byKey["apps/web-1"] = testPod("apps", "web-1", "node", corev1.PodRunning)
	current := func() bool { s.pollMetrics(context.Background()); return s.Metrics(0, "apps/web-1", 0).CPUKnown }
	if !current() || s.Metrics(0, "apps/web-1", 0).CPUNow != 0 {
		t.Fatal("observed zero is unknown")
	}
	if !current() {
		t.Fatal("unchanged current timestamp rejected")
	}
	list.Items[0].Containers[0].Usage = nil
	if current() {
		t.Fatal("absent CPU resource treated as zero")
	}
	list.Items[0].Containers = nil
	if current() {
		t.Fatal("absent containers treated as zero")
	}
	list = &metricsv1beta1.PodMetricsList{}
	if current() {
		t.Fatal("omitted pod kept current CPU")
	}
	list = podMetricsAt("135m", fixedNow.Add(time.Second))
	if !current() {
		t.Fatal("new sample unknown")
	}
	queryErr = errors.New("synthetic unavailable")
	if current() {
		t.Fatal("failure exposes previous CPU")
	}
	queryErr = nil
	list = podMetricsAt("0", fixedNow)
	if current() {
		t.Fatal("regressed sample accepted as current")
	}
	list = podMetricsAt("135m", fixedNow.Add(time.Second))
	if !current() {
		t.Fatal("sample not recovered")
	}
	s.byKey["apps/web-1"].UID = "recreated"
	if s.Metrics(0, "apps/web-1", 0).CPUKnown {
		t.Fatal("recreated pod inherited CPU")
	}
}

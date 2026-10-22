package kube

import (
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

func resourceWorkloadObject(resource string) *unstructured.Unstructured {
	u := resourceObject(0)
	u.SetGeneration(2)
	u.Object["spec"] = map[string]any{"replicas": int64(3)}
	u.Object["status"] = map[string]any{"observedGeneration": int64(2), "replicas": int64(3), "readyReplicas": int64(3), "updatedReplicas": int64(3), "availableReplicas": int64(3)}
	if resource == "daemonsets" {
		u.Object["status"] = map[string]any{"observedGeneration": int64(2), "desiredNumberScheduled": int64(7), "currentNumberScheduled": int64(7), "numberReady": int64(7), "numberAvailable": int64(7), "updatedNumberScheduled": int64(7)}
	}
	return &u
}

func TestResourceWorkloadClassification(t *testing.T) {
	tests := []struct {
		name, resource string
		change         func(*unstructured.Unstructured)
		want           data.WorkloadState
	}{
		{"stateful ready", "statefulsets", nil, data.WorkloadReady},
		{"stateful updating", "statefulsets", func(u *unstructured.Unstructured) { u.Object["status"].(map[string]any)["updatedReplicas"] = int64(2) }, data.WorkloadProgressing},
		{"stateful revision", "statefulsets", func(u *unstructured.Unstructured) {
			s := u.Object["status"].(map[string]any)
			s["currentRevision"] = "old"
			s["updateRevision"] = "new"
		}, data.WorkloadProgressing},
		{"stateful unavailable", "statefulsets", func(u *unstructured.Unstructured) { u.Object["status"].(map[string]any)["readyReplicas"] = int64(1) }, data.WorkloadDegraded},
		{"stateful zero", "statefulsets", func(u *unstructured.Unstructured) { u.Object["spec"].(map[string]any)["replicas"] = int64(0) }, data.WorkloadScaledToZero},
		{"generation stale", "statefulsets", func(u *unstructured.Unstructured) { u.SetGeneration(3) }, data.WorkloadProgressing},
		{"status absent", "statefulsets", func(u *unstructured.Unstructured) { delete(u.Object, "status") }, data.WorkloadUnknown},
		{"daemon ready", "daemonsets", nil, data.WorkloadReady},
		{"daemon misscheduled", "daemonsets", func(u *unstructured.Unstructured) {
			u.Object["status"].(map[string]any)["numberMisscheduled"] = int64(1)
		}, data.WorkloadDegraded},
		{"daemon updating", "daemonsets", func(u *unstructured.Unstructured) {
			u.Object["status"].(map[string]any)["updatedNumberScheduled"] = int64(6)
		}, data.WorkloadProgressing},
		{"replicaset ready", "replicasets", nil, data.WorkloadReady},
		{"replicaset degraded", "replicasets", func(u *unstructured.Unstructured) { u.Object["status"].(map[string]any)["readyReplicas"] = int64(1) }, data.WorkloadDegraded},
		{"deployment deadline", "deployments", func(u *unstructured.Unstructured) {
			u.Object["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded"}}
		}, data.WorkloadFailed},
		{"job failure counter not terminal", "jobs", func(u *unstructured.Unstructured) {
			u.Object["status"] = map[string]any{"active": int64(1), "failed": int64(2)}
		}, data.WorkloadProgressing},
		{"job failed condition", "jobs", func(u *unstructured.Unstructured) {
			u.Object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Failed", "status": "True"}}}
		}, data.WorkloadFailed},
		{"job complete", "jobs", func(u *unstructured.Unstructured) {
			u.Object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
		}, data.WorkloadCompleted},
		{"job waiting", "jobs", func(u *unstructured.Unstructured) { u.Object["status"] = map[string]any{"active": int64(0)} }, data.WorkloadWaiting},
		{"cron suspended", "cronjobs", func(u *unstructured.Unstructured) {
			u.Object["spec"] = map[string]any{"schedule": "*/5 * * * *", "suspend": true}
		}, data.WorkloadSuspended},
		{"cron no inferred lateness", "cronjobs", func(u *unstructured.Unstructured) {
			u.Object["spec"] = map[string]any{"schedule": "*/5 * * * *"}
			delete(u.Object, "status")
		}, data.WorkloadScheduled},
		{"cron active", "cronjobs", func(u *unstructured.Unstructured) {
			u.Object["spec"] = map[string]any{"schedule": "*/5 * * * *"}
			u.Object["status"] = map[string]any{"active": []any{map[string]any{"name": "job"}}}
		}, data.WorkloadProgressing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := resourceWorkloadObject(tt.resource)
			if tt.change != nil {
				tt.change(u)
			}
			group := "apps"
			if tt.resource == "jobs" || tt.resource == "cronjobs" {
				group = "batch"
			}
			got := resourceWorkload(u, schema.GroupVersionResource{Group: group, Version: "v1", Resource: tt.resource})
			if got.State != tt.want {
				t.Fatalf("got %+v want %s", got, tt.want)
			}
			row, ok := projectResource(u)
			row.Workload = got
			if !ok || !resourceRowFits(row) {
				t.Fatal("workload projection exceeds budget")
			}
		})
	}
}

func TestResourceWorkloadTypedCaches(t *testing.T) {
	pod := &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
	if resourceWorkload(pod, schema.GroupVersionResource{Version: "v1", Resource: "pods"}).State != data.WorkloadCompleted {
		t.Fatal("completed pod misclassified")
	}
	desired := int32(3)
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Generation: 2}, Spec: appsv1.DeploymentSpec{Replicas: &desired}, Status: appsv1.DeploymentStatus{ObservedGeneration: 2, ReadyReplicas: 3, UpdatedReplicas: 3, Replicas: 3, AvailableReplicas: 3}}
	got := resourceWorkload(deployment, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"})
	if got.State != data.WorkloadReady || !got.Desired.Known || got.Desired.Value != 3 {
		t.Fatal(got)
	}
	u := resourceWorkloadObject("statefulsets")
	delete(u.Object, "status")
	got = resourceWorkload(u, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"})
	if got.Ready.Known {
		t.Fatal("missing status became zero")
	}
	if resourceWorkload(u, schema.GroupVersionResource{Group: "untrusted", Version: "v1", Resource: "statefulsets"}).Kind != "" {
		t.Fatal("untrusted GVR got workload semantics")
	}
}

func TestResourceWorkloadAggregatesFollowWatch(t *testing.T) {
	s, _, key := resourcePoolFixture(t)
	key.GVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{key.GVR: "StatefulSetList"})
	s.resources.client = client
	u := resourceWorkloadObject("statefulsets")
	client.PrependReactor("list", "*", func(clienttesting.Action) (bool, runtime.Object, error) {
		page := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*u.DeepCopy()}}
		page.SetResourceVersion("1")
		return true, page, nil
	})
	stream := watch.NewRaceFreeFake()
	client.PrependWatchReactor("*", func(clienttesting.Action) (bool, watch.Interface, error) { return true, stream, nil })
	h := resourceSubscribe(t, s, key, true, true)
	resourceWait(t, h, func(p data.ResourcePage) bool { return p.Workloads.Ready == 1 })
	u.Object["status"].(map[string]any)["readyReplicas"] = int64(1)
	u.SetResourceVersion("2")
	stream.Modify(u.DeepCopy())
	p := resourceWait(t, h, func(p data.ResourcePage) bool { return p.Workloads.Degraded == 1 })
	if p.Workloads.Ready != 0 || p.Workloads.Total != 1 {
		t.Fatal(p.Workloads)
	}
	stream.Delete(u.DeepCopy())
	resourceWait(t, h, func(p data.ResourcePage) bool { return p.Workloads.Total == 0 })
}

package kube

import (
	"strings"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func resourceWorkload(object any, gvr schema.GroupVersionResource) data.WorkloadSummary {
	switch obj := object.(type) {
	case *corev1.Pod:
		return resourcePodWorkload(obj)
	case *appsv1.Deployment:
		u := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"generation": obj.Generation}, "spec": map[string]any{"replicas": int64(1)}, "status": map[string]any{"replicas": int64(obj.Status.Replicas), "readyReplicas": int64(obj.Status.ReadyReplicas), "updatedReplicas": int64(obj.Status.UpdatedReplicas), "availableReplicas": int64(obj.Status.AvailableReplicas), "observedGeneration": obj.Status.ObservedGeneration}}}
		if obj.Spec.Replicas != nil {
			u.Object["spec"].(map[string]any)["replicas"] = int64(*obj.Spec.Replicas)
		}
		conditions := make([]any, 0, len(obj.Status.Conditions))
		for _, c := range obj.Status.Conditions {
			conditions = append(conditions, map[string]any{"type": string(c.Type), "status": string(c.Status), "reason": c.Reason})
		}
		u.Object["status"].(map[string]any)["conditions"] = conditions
		return resourceWorkload(u, gvr)
	case *unstructured.Unstructured:
		return resourceUnstructuredWorkload(obj, gvr)
	}
	return data.WorkloadSummary{}
}

func resourceUnstructuredWorkload(obj *unstructured.Unstructured, gvr schema.GroupVersionResource) data.WorkloadSummary {
	kinds := map[schema.GroupVersionResource]data.WorkloadKind{
		{Group: "apps", Version: "v1", Resource: "deployments"}:  data.WorkloadDeployment,
		{Group: "apps", Version: "v1", Resource: "statefulsets"}: data.WorkloadStatefulSet,
		{Group: "apps", Version: "v1", Resource: "daemonsets"}:   data.WorkloadDaemonSet,
		{Group: "apps", Version: "v1", Resource: "replicasets"}:  data.WorkloadReplicaSet,
		{Group: "batch", Version: "v1", Resource: "jobs"}:        data.WorkloadJob,
		{Group: "batch", Version: "v1", Resource: "cronjobs"}:    data.WorkloadCronJob,
	}
	kind := kinds[gvr]
	if kind == "" {
		return data.WorkloadSummary{}
	}
	w := data.WorkloadSummary{Kind: kind, State: data.WorkloadUnknown, Generation: obj.GetGeneration()}
	rawStatus, present, _ := unstructured.NestedFieldNoCopy(obj.Object, "status")
	status, _ := rawStatus.(map[string]any)
	count := func(name string) data.ResourceCount {
		n, ok, err := unstructured.NestedInt64(status, name)
		return data.ResourceCount{Value: n, Known: present && err == nil && (ok || len(status) > 0) && n >= 0}
	}
	w.ObservedGeneration = count("observedGeneration")
	if kind == data.WorkloadCronJob {
		return resourceCronJob(obj, w, status, present)
	}
	if !present || len(status) == 0 {
		return w
	}
	if kind == data.WorkloadJob {
		w.Active = count("active")
		w.Succeeded = count("succeeded")
		w.Failed = count("failed")
		n, ok, _ := unstructured.NestedInt64(obj.Object, "spec", "completions")
		w.Desired = data.ResourceCount{Value: n, Known: ok && n >= 0}
		switch {
		case resourceCondition(status, "Failed", "True", ""):
			w.State = data.WorkloadFailed
			w.Reason = "terminal Failed condition"
		case resourceCondition(status, "Complete", "True", ""):
			w.State = data.WorkloadCompleted
		case w.Active.Known && w.Active.Value > 0:
			w.State = data.WorkloadProgressing
		case w.Active.Known:
			w.State = data.WorkloadWaiting
		}
		return w
	}
	w.Current = count("replicas")
	w.Ready = count("readyReplicas")
	w.Available = count("availableReplicas")
	w.Updated = count("updatedReplicas")
	if kind == data.WorkloadDaemonSet {
		w.Desired = count("desiredNumberScheduled")
		w.Current = count("currentNumberScheduled")
		w.Ready = count("numberReady")
		w.Available = count("numberAvailable")
		w.Updated = count("updatedNumberScheduled")
	} else {
		n, ok, err := unstructured.NestedInt64(obj.Object, "spec", "replicas")
		if !ok && err == nil {
			n = 1
		}
		w.Desired = data.ResourceCount{Value: n, Known: err == nil && n >= 0}
	}
	if resourceCondition(status, "Progressing", "False", "ProgressDeadlineExceeded") {
		w.State = data.WorkloadFailed
		w.Reason = "ProgressDeadlineExceeded"
		return w
	}
	if !w.ObservedGeneration.Known || w.ObservedGeneration.Value < w.Generation {
		w.State = data.WorkloadProgressing
		w.Reason = "generation not observed"
		return w
	}
	if !w.Desired.Known || !w.Ready.Known {
		return w
	}
	if kind == data.WorkloadDaemonSet {
		misscheduled := count("numberMisscheduled")
		if misscheduled.Known && misscheduled.Value > 0 {
			w.State = data.WorkloadDegraded
			w.Reason = "pods misscheduled"
			return w
		}
	}
	if w.Desired.Value == 0 {
		w.State = data.WorkloadScaledToZero
		if kind == data.WorkloadDaemonSet {
			w.State = data.WorkloadNoTargets
		}
		return w
	}
	if kind != data.WorkloadReplicaSet && w.Updated.Known && w.Updated.Value < w.Desired.Value {
		w.State = data.WorkloadProgressing
		return w
	}
	if kind == data.WorkloadStatefulSet {
		current, _, _ := unstructured.NestedString(status, "currentRevision")
		update, _, _ := unstructured.NestedString(status, "updateRevision")
		if current != "" && update != "" && current != update {
			w.State = data.WorkloadProgressing
			w.Reason = "revision differs"
			return w
		}
	}
	if w.Ready.Value < w.Desired.Value || (kind == data.WorkloadDeployment || kind == data.WorkloadDaemonSet) && w.Available.Known && w.Available.Value < w.Desired.Value {
		w.State = data.WorkloadDegraded
	} else {
		w.State = data.WorkloadReady
	}
	return w
}

func resourceCronJob(obj *unstructured.Unstructured, w data.WorkloadSummary, status map[string]any, present bool) data.WorkloadSummary {
	schedule, _, _ := unstructured.NestedString(obj.Object, "spec", "schedule")
	if len(schedule) <= 64 && strings.IndexFunc(schedule, func(r rune) bool { return r < 32 || r > 126 }) < 0 {
		w.Schedule = strings.Clone(schedule)
	}
	last, _, _ := unstructured.NestedString(status, "lastScheduleTime")
	w.LastScheduleAt, _ = time.Parse(time.RFC3339, last)
	suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend")
	rawActive, found, _ := unstructured.NestedFieldNoCopy(status, "active")
	active, _ := rawActive.([]any)
	w.Active = data.ResourceCount{Value: int64(len(active)), Known: present || found}
	switch {
	case suspended:
		w.State = data.WorkloadSuspended
	case w.Active.Value > 0:
		w.State = data.WorkloadProgressing
	case w.Schedule != "":
		w.State = data.WorkloadScheduled
	}
	return w
}

func resourceCondition(status map[string]any, kind, value, reason string) bool {
	rawConditions, _, _ := unstructured.NestedFieldNoCopy(status, "conditions")
	conditions, _ := rawConditions.([]any)
	for _, raw := range conditions {
		c, ok := raw.(map[string]any)
		if ok && c["type"] == kind && c["status"] == value && (reason == "" || c["reason"] == reason) {
			return true
		}
	}
	return false
}

func resourcePodWorkload(p *corev1.Pod) data.WorkloadSummary {
	w := data.WorkloadSummary{Kind: data.WorkloadPod, State: data.WorkloadUnknown}
	switch p.Status.Phase {
	case corev1.PodSucceeded:
		w.State = data.WorkloadCompleted
	case corev1.PodFailed:
		w.State = data.WorkloadFailed
	case corev1.PodPending:
		w.State = data.WorkloadWaiting
	case corev1.PodRunning:
		w.State = data.WorkloadDegraded
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				w.State = data.WorkloadReady
			}
		}
	}
	for _, statuses := range [][]corev1.ContainerStatus{p.Status.ContainerStatuses, p.Status.InitContainerStatuses} {
		for _, s := range statuses {
			if s.State.Waiting != nil {
				switch s.State.Waiting.Reason {
				case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull":
					w.State = data.WorkloadFailed
				}
			}
			if s.State.Terminated != nil && s.State.Terminated.Reason == "OOMKilled" {
				w.State = data.WorkloadFailed
			}
		}
	}
	return w
}

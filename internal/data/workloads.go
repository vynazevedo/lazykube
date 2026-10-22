package data

import "time"

type WorkloadKind string

const (
	WorkloadPod         WorkloadKind = "Pod"
	WorkloadDeployment  WorkloadKind = "Deployment"
	WorkloadStatefulSet WorkloadKind = "StatefulSet"
	WorkloadDaemonSet   WorkloadKind = "DaemonSet"
	WorkloadJob         WorkloadKind = "Job"
	WorkloadCronJob     WorkloadKind = "CronJob"
	WorkloadReplicaSet  WorkloadKind = "ReplicaSet"
)

type WorkloadState string

const (
	WorkloadUnknown      WorkloadState = "unknown"
	WorkloadScheduled    WorkloadState = "scheduled"
	WorkloadNoTargets    WorkloadState = "no targets"
	WorkloadReady        WorkloadState = "ready"
	WorkloadWaiting      WorkloadState = "waiting"
	WorkloadProgressing  WorkloadState = "progressing"
	WorkloadDegraded     WorkloadState = "degraded"
	WorkloadCompleted    WorkloadState = "completed"
	WorkloadFailed       WorkloadState = "failed"
	WorkloadSuspended    WorkloadState = "suspended"
	WorkloadScaledToZero WorkloadState = "scaled to zero"
)

type ResourceCount struct {
	Value int64
	Known bool
}

type WorkloadSummary struct {
	Kind               WorkloadKind
	State              WorkloadState
	Desired            ResourceCount
	Current            ResourceCount
	Ready              ResourceCount
	Updated            ResourceCount
	Available          ResourceCount
	Active             ResourceCount
	Succeeded          ResourceCount
	Failed             ResourceCount
	Generation         int64
	ObservedGeneration ResourceCount
	Schedule           string
	Reason             string
	LastScheduleAt     time.Time
}

type WorkloadDistribution struct {
	Total        int
	Scheduled    int
	NoTargets    int
	Ready        int
	Waiting      int
	Progressing  int
	Degraded     int
	Completed    int
	Failed       int
	Suspended    int
	ScaledToZero int
	Unknown      int
}

func (d *WorkloadDistribution) Add(w WorkloadSummary, delta int) {
	if w.Kind == "" {
		return
	}
	d.Total += delta
	switch w.State {
	case WorkloadScheduled:
		d.Scheduled += delta
	case WorkloadNoTargets:
		d.NoTargets += delta
	case WorkloadReady:
		d.Ready += delta
	case WorkloadWaiting:
		d.Waiting += delta
	case WorkloadProgressing:
		d.Progressing += delta
	case WorkloadDegraded:
		d.Degraded += delta
	case WorkloadCompleted:
		d.Completed += delta
	case WorkloadFailed:
		d.Failed += delta
	case WorkloadSuspended:
		d.Suspended += delta
	case WorkloadScaledToZero:
		d.ScaledToZero += delta
	default:
		d.Unknown += delta
	}
}

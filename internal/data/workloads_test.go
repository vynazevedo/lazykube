package data

import "testing"

func TestWorkloadDistributionReversible(t *testing.T) {
	states := []WorkloadState{WorkloadReady, WorkloadWaiting, WorkloadProgressing, WorkloadDegraded, WorkloadCompleted, WorkloadFailed, WorkloadSuspended, WorkloadScaledToZero, WorkloadScheduled, WorkloadNoTargets, WorkloadUnknown}
	var d WorkloadDistribution
	for _, state := range states {
		d.Add(WorkloadSummary{Kind: WorkloadJob, State: state}, 1)
	}
	if d.Total != len(states) || d.Ready != 1 || d.Waiting != 1 || d.Progressing != 1 || d.Degraded != 1 || d.Completed != 1 || d.Failed != 1 || d.Suspended != 1 || d.ScaledToZero != 1 || d.Scheduled != 1 || d.NoTargets != 1 || d.Unknown != 1 {
		t.Fatal(d)
	}
	for _, state := range states {
		d.Add(WorkloadSummary{Kind: WorkloadJob, State: state}, -1)
	}
	d.Add(WorkloadSummary{}, 1)
	if d != (WorkloadDistribution{}) {
		t.Fatal("old counts retained", d)
	}
}

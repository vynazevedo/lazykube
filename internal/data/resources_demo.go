package data

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (d *Demo) Catalog(ctx context.Context, _ bool) ResourceCatalogSnapshot {
	out := ResourceCatalogSnapshot{Session: "demo:" + d.context + ":" + d.namespace, Generation: 1, Revision: 1, ObservedAt: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC), Coverage: Coverage{State: CoverageComplete}, Synthetic: true}
	if ctx.Err() != nil {
		out.Coverage = Coverage{State: CoverageUnavailable, Reason: "demo request canceled"}
		return out
	}
	for _, r := range []struct {
		group, resource string
		kind            WorkloadKind
	}{{"", "pods", WorkloadPod}, {"apps", "deployments", WorkloadDeployment}, {"apps", "statefulsets", WorkloadStatefulSet}, {"apps", "daemonsets", WorkloadDaemonSet}, {"batch", "jobs", WorkloadJob}, {"batch", "cronjobs", WorkloadCronJob}, {"apps", "replicasets", WorkloadReplicaSet}} {
		out.Resources = append(out.Resources, ResourceDescriptor{GVR: schema.GroupVersionResource{Group: r.group, Version: "v1", Resource: r.resource}, Kind: string(r.kind), Namespaced: true, Preferred: true, List: true, Watch: true, Availability: ResourceServed})
	}
	return out
}

func (d *Demo) Subscribe(ctx context.Context, key ResourceSubscriptionKey) (ResourceSubscription, error) {
	c := d.Catalog(ctx, false)
	descriptor := c.Lookup(key.GVR)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if key.Session != c.Session || key.Generation != c.Generation || key.ProjectionVersion != ResourceProjectionVersion || descriptor.Availability != ResourceServed || d.namespace != "" && key.Namespace != d.namespace {
		return nil, errors.New("demo resource scope unavailable")
	}
	ls, err := labels.Parse(key.LabelSelector)
	if err != nil {
		return nil, errors.New("invalid demo selector")
	}
	fs, err := fields.ParseSelector(key.FieldSelector)
	if err != nil {
		return nil, errors.New("invalid demo selector")
	}
	rows := d.demoResourceRows(WorkloadKind(descriptor.Kind), c.ObservedAt)
	kept := make([]ResourceRow, 0, len(rows))
	for _, row := range rows {
		if (key.Namespace == "" || key.Namespace == row.Ref.Namespace) && ls.Matches(labels.Set{"app": "demo"}) && fs.Matches(fields.Set{"metadata.name": row.Ref.Name, "metadata.namespace": row.Ref.Namespace}) {
			kept = append(kept, row)
		}
	}
	h := &demoResourceHandle{page: ResourcePage{Items: kept, Total: len(kept), Revision: 1, ObservedAt: c.ObservedAt, Coverage: Coverage{State: CoverageComplete}, Synthetic: true}, changes: make(chan struct{}, 1)}
	for _, r := range kept {
		h.page.Workloads.Add(r.Workload, 1)
	}
	h.mu.Lock()
	h.stop = context.AfterFunc(ctx, h.Cancel)
	h.mu.Unlock()
	return h, nil
}

func (d *Demo) demoResourceRows(kind WorkloadKind, at time.Time) []ResourceRow {
	var out []ResourceRow
	appendRow := func(ns, name string, w WorkloadSummary) {
		out = append(out, ResourceRow{Ref: ResourceMetadataRef{Namespace: ns, Name: name, UID: "demo-" + ns + "/" + name, ResourceVersion: "1", CreatedAt: at.Add(-time.Hour)}, Workload: w})
	}
	if kind == WorkloadPod {
		for _, p := range d.Snapshot().Pods {
			state := WorkloadUnknown
			switch p.Status {
			case StatusRunning:
				state = WorkloadReady
				if p.Ready < p.Containers {
					state = WorkloadDegraded
				}
			case StatusCompleted:
				state = WorkloadCompleted
			case StatusPending, StatusContainerCreating:
				state = WorkloadWaiting
			case StatusFailed:
				state = WorkloadFailed
			}
			appendRow(p.Namespace, p.Name, WorkloadSummary{Kind: kind, State: state})
		}
		return out
	}
	if kind == WorkloadDeployment {
		for _, deployment := range d.Snapshot().Deployments {
			state := WorkloadReady
			if deployment.Ready < deployment.Desired {
				state = WorkloadDegraded
			}
			appendRow(deployment.Namespace, deployment.Name, WorkloadSummary{Kind: kind, State: state, Desired: ResourceCount{Value: int64(deployment.Desired), Known: true}, Ready: ResourceCount{Value: int64(deployment.Ready), Known: true}})
		}
		return out
	}
	namespace := d.namespace
	if namespace == "" {
		namespace = "apps"
	}
	states := []WorkloadState{WorkloadReady, WorkloadProgressing, WorkloadDegraded}
	if kind == WorkloadJob {
		states = []WorkloadState{WorkloadCompleted, WorkloadProgressing, WorkloadFailed}
	}
	if kind == WorkloadCronJob {
		states = []WorkloadState{WorkloadScheduled, WorkloadProgressing, WorkloadSuspended}
	}
	for i, state := range states {
		w := WorkloadSummary{Kind: kind, State: state, Generation: 2, ObservedGeneration: ResourceCount{Value: 2, Known: true}}
		switch kind {
		case WorkloadCronJob:
			w.Schedule = "*/5 * * * *"
			w.Active = ResourceCount{Known: true}
			if state == WorkloadProgressing {
				w.Active.Value = 1
			}
		case WorkloadJob:
			w.Active = ResourceCount{Known: true}
			w.Succeeded = ResourceCount{Known: true}
			w.Failed = ResourceCount{Known: true}
			switch state {
			case WorkloadCompleted:
				w.Succeeded.Value = 1
			case WorkloadFailed:
				w.Failed.Value = 3
			case WorkloadProgressing:
				w.Active.Value = 1
			}
		default:
			w.Desired = ResourceCount{Value: 3, Known: true}
			w.Ready = ResourceCount{Value: int64(3 - i), Known: true}
		}
		appendRow(namespace, fmt.Sprintf("demo-%s-%d", strings.ToLower(string(kind)), i+1), w)
	}
	return out
}

type demoResourceHandle struct {
	mu      sync.Mutex
	page    ResourcePage
	changes chan struct{}
	stop    func() bool
}

func (h *demoResourceHandle) Changes() <-chan struct{} { return h.changes }
func (h *demoResourceHandle) Refresh() {
	select {
	case h.changes <- struct{}{}:
	default:
	}
}
func (h *demoResourceHandle) Cancel() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stop != nil {
		h.stop()
	}
	h.page = ResourcePage{Synthetic: true, Coverage: Coverage{State: CoverageUnavailable, Reason: "demo subscription canceled"}}
}
func (h *demoResourceHandle) Page(cursor string, limit int) ResourcePage {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.page
	offset := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 || n > len(out.Items) {
			out.Items = nil
			out.Coverage = Coverage{State: CoverageStale, Reason: "invalid demo cursor"}
			return out
		}
		offset = n
	}
	end := min(offset+min(max(limit, 1), 500), len(out.Items))
	out.Items = slices.Clone(out.Items[offset:end])
	if end < len(h.page.Items) {
		out.Continue = strconv.Itoa(end)
	}
	return out
}

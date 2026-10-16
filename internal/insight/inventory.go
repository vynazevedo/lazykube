package insight

import (
	"cmp"
	"slices"
	"time"
)

const MaxInventoryPods = 10000

type Inventory struct {
	clock Clock
	batch ObservationBatch
}

func NewInventory(clock Clock) *Inventory {
	return &Inventory{clock: clock}
}

func (i *Inventory) Publish(source SourceIdentity, namespace string, synthetic bool, pods []PodObservation, coverage Coverage) bool {
	pods = slices.Clone(pods)
	slices.SortFunc(pods, func(a, b PodObservation) int {
		if n := cmp.Compare(a.Entity.Namespace, b.Entity.Namespace); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Entity.Name, b.Entity.Name); n != 0 {
			return n
		}
		return cmp.Compare(a.Entity.UID, b.Entity.UID)
	})
	coverage.Total = max(coverage.Total, len(pods))
	if coverage.Total > MaxInventoryPods {
		pods = pods[:min(len(pods), MaxInventoryPods)]
		if coverage.State == CoverageComplete {
			coverage.State = CoveragePartial
		}
		coverage.Reason = inventoryCoverageReason(coverage.Reason, "inventory limited to 10000 pods")
	}
	valid := pods[:0]
	missingIdentity := false
	for _, p := range pods {
		if p.Entity.UID == "" || p.Entity.ResourceVersion == "" {
			if coverage.State == CoverageComplete {
				coverage.State = CoveragePartial
			}
			missingIdentity = true
			continue
		}
		p.Entity.Source = source
		valid = append(valid, p)
	}
	pods = make([]PodObservation, len(valid))
	copy(pods, valid)
	if missingIdentity {
		coverage.Reason = inventoryCoverageReason(coverage.Reason, "pod identity or resource version missing")
	}
	coverage.Observed = len(pods)
	previous := make(map[EntityKey]PodObservation, len(i.batch.Pods))
	for _, p := range i.batch.Pods {
		previous[p.Entity.EntityKey] = p
	}
	changed := i.batch.Cursor.Source != source || i.batch.Cursor.Revisions.Inventory == 0 || i.batch.Namespace != namespace || i.batch.Synthetic != synthetic || i.batch.Coverage.Inventory != coverage || len(pods) != len(i.batch.Pods)
	for n, p := range pods {
		old, ok := previous[p.Entity.EntityKey]
		if ok && old.Entity.ResourceVersion == p.Entity.ResourceVersion {
			pods[n] = old
		} else {
			pods[n] = clonePod(p)
			pods[n].FirstObservedAt = old.FirstObservedAt
			pods[n].ReceivedAt = time.Time{}
			changed = true
		}
	}
	if !changed {
		return false
	}
	now := i.clock.Now()
	if now.Before(i.batch.ObservedAt) && i.batch.Cursor.Source == source {
		now = i.batch.ObservedAt
	}
	for n := range pods {
		if pods[n].ReceivedAt.IsZero() {
			pods[n].ReceivedAt = now
		}
		if pods[n].FirstObservedAt.IsZero() {
			pods[n].FirstObservedAt = now
		}
	}
	revision := i.batch.Cursor.Revisions.Inventory + 1
	if i.batch.Cursor.Source != source {
		revision = 1
	}
	unavailable := Coverage{State: CoverageUnavailable, Reason: "not projected in R1"}
	i.batch = ObservationBatch{
		Cursor:    Cursor{Source: source, Revisions: Revisions{Inventory: revision}},
		Namespace: namespace, Synthetic: synthetic, ObservedAt: now, FullInventory: true,
		Coverage: SourceCoverage{Inventory: coverage, Metrics: unavailable, Events: unavailable, Security: unavailable, Owners: unavailable, Nodes: unavailable, Network: Coverage{State: CoverageDisabled, Reason: "network disabled"}},
		Pods:     pods,
	}
	return true
}

func (i *Inventory) Observations(after Cursor) ObservationBatch {
	out := i.batch
	out.Reset = after.Source != out.Cursor.Source || after.Revisions.Inventory == 0 || after.Revisions.Inventory > out.Cursor.Revisions.Inventory
	if !out.Reset && after.Revisions.Inventory < out.Cursor.Revisions.Inventory {
		out.SkippedInventoryRevisions = out.Cursor.Revisions.Inventory - after.Revisions.Inventory - 1
	}
	if out.Cursor.Revisions.Inventory != 0 && after == out.Cursor {
		out.Unchanged = true
		out.FullInventory = false
		out.Pods = nil
		return out
	}
	out.Pods = make([]PodObservation, len(i.batch.Pods))
	for n, p := range i.batch.Pods {
		out.Pods[n] = clonePod(p)
	}
	return out
}

func clonePod(p PodObservation) PodObservation {
	p.Owners = slices.Clone(p.Owners)
	p.Conditions = slices.Clone(p.Conditions)
	p.Containers = slices.Clone(p.Containers)
	for n := range p.Containers {
		c := &p.Containers[n]
		if c.CurrentTermination != nil {
			v := *c.CurrentTermination
			c.CurrentTermination = &v
		}
		if c.LastTermination != nil {
			v := *c.LastTermination
			c.LastTermination = &v
		}
	}
	return p
}

func inventoryCoverageReason(reason, detail string) string {
	if reason == "" {
		return detail
	}
	if reason == detail {
		return reason
	}
	return reason + "; " + detail
}

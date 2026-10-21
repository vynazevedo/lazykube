package insight

import (
	"reflect"
	"testing"
	"time"
)

func TestReviewPendingMissingStatusCannotRecover(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		f := newOperationalFixture(t)
		f.step(0, operationalWaiting(ReasonContainerCreating))
		f.step(5*time.Minute, nil)
		out := f.step(6*time.Minute, func(p *PodObservation) {
			if unknown {
				p.Containers[0].Phase = ContainerUnknown
			} else {
				p.Containers[0].StatusObserved = false
			}
		})
		if operationalSignal(t, out, RulePending).State != SignalSuspended {
			t.Fatal("missing status interpreted as healthy")
		}
		f.clock.now = f.origin.Add(10 * time.Minute)
		if operationalSignal(t, f.engine.Advance(), RulePending).State != SignalSuspended {
			t.Fatal("gap resolved Pending")
		}
		f.step(11*time.Minute, func(p *PodObservation) { operationalHealthy(p); p.Containers[0].StatusObserved = true })
		if operationalSignal(t, f.step(12*time.Minute-time.Nanosecond, nil), RulePending).State == SignalResolved {
			t.Fatal("gap included in healthy interval")
		}
		if operationalSignal(t, f.step(12*time.Minute, nil), RulePending).State != SignalResolved {
			t.Fatal("complete recovery not recognized")
		}
	}
}

func TestReviewAcceptedResetSuspendsAndRebaselines(t *testing.T) {
	f := newOperationalFixture(t)
	f.step(0, nil)
	f.step(time.Second, func(p *PodObservation) { p.Containers[0].RestartCount = 3 })
	f.batch.Reset = true
	out := f.step(2*time.Second, func(p *PodObservation) { p.Containers[0].RestartCount = 30 })
	signal := operationalSignal(t, out, RuleRestarts)
	if signal.State != SignalSuspended || signal.Evidence.WindowRestarts != 0 {
		t.Fatal("reset crossed old baseline")
	}
	if replay := f.engine.Step(f.batch); !reflect.DeepEqual(out, replay) {
		t.Fatal("reset replay changed state")
	}
	f.clock.now = f.origin.Add(10 * time.Minute)
	if operationalSignal(t, f.engine.Advance(), RuleRestarts).State != SignalSuspended {
		t.Fatal("reset resolved active signal")
	}
	f.batch.Reset = false
	out = f.step(11*time.Minute, func(p *PodObservation) { p.Containers[0].RestartCount = 31 })
	if operationalSignal(t, out, RuleRestarts).Evidence.WindowRestarts != 0 {
		t.Fatal("reset recovery counted outage")
	}
	out = f.step(11*time.Minute+time.Second, func(p *PodObservation) { p.Containers[0].RestartCount = 34 })
	if operationalSignal(t, out, RuleRestarts).Evidence.WindowRestarts != 3 {
		t.Fatal("new baseline not used")
	}
}

func TestReviewEvictionTransitionEvidence(t *testing.T) {
	for _, initial := range []bool{true, false} {
		f := newOperationalFixture(t)
		if initial {
			f.batch.Pods[0].Reason = ReasonEvicted
			f.batch.Pods[0].Phase = PodFailed
		}
		first := operationalSignal(t, f.step(0, nil), RuleEvicted)
		if initial && (!first.Preexisting || !first.Evidence.TransitionObservedAt.IsZero()) {
			t.Fatal("baseline invented eviction transition")
		}
		if !initial {
			first = operationalSignal(t, f.step(time.Second, func(p *PodObservation) { p.Reason = ReasonEvicted; p.Phase = PodFailed }), RuleEvicted)
		}
		observed := first.Evidence.TransitionObservedAt
		if !initial && observed.IsZero() {
			t.Fatal("known transition missing")
		}
		f.batch.Coverage.Inventory.State = CoverageStale
		f.step(2*time.Second, func(*PodObservation) {})
		f.batch.Coverage.Inventory.State = CoverageComplete
		resumed := operationalSignal(t, f.step(time.Minute, func(*PodObservation) {}), RuleEvicted)
		if resumed.Evidence.TransitionObservedAt != observed {
			t.Fatal("resume rewrote transition")
		}
	}
}

func TestReviewResetDiscardsAbsentEntities(t *testing.T) {
	f := newOperationalFixture(t)
	f.step(0, nil)
	f.batch.Cursor.Revisions.Inventory++
	f.batch.Reset = true
	f.batch.Pods = nil
	if got := f.engine.Step(f.batch); len(got.Signals) != 0 {
		t.Fatal("relist retained absent entities")
	}
}

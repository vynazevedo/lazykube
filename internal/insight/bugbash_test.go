package insight

import (
	"strings"
	"testing"
	"time"
)

func TestBugbashPendingWithoutContainerStatus(t *testing.T) {
	for _, empty := range []bool{false, true} {
		f := newOperationalFixture(t)
		f.batch.Pods[0].Phase = PodPending
		f.batch.Pods[0].Containers[0].StatusObserved = false
		if empty {
			f.batch.Pods[0].Containers = nil
		}
		first := operationalSignal(t, f.step(0, nil), RulePending)
		if first.State != SignalCandidate {
			t.Fatalf("explicit Pending suspended: %+v", first)
		}
		f.clock.now = f.origin.Add(5*time.Minute - time.Nanosecond)
		if operationalSignal(t, f.engine.Advance(), RulePending).State != SignalCandidate {
			t.Fatal("warning before threshold")
		}
		f.clock.now = f.origin.Add(5 * time.Minute)
		if operationalSignal(t, f.engine.Advance(), RulePending).State != SignalActive {
			t.Fatal("scheduling wait not detected")
		}
		f.clock.now = f.origin.Add(15 * time.Minute)
		if operationalSignal(t, f.engine.Advance(), RulePending).Severity != SignalHigh {
			t.Fatal("high threshold missed")
		}
	}
}

func TestBugbashResetDoesNotInventBudgetOmissions(t *testing.T) {
	f := newOperationalFixture(t)
	f.step(0, nil)
	f.batch.Cursor.Revisions.Inventory++
	f.batch.Reset = true
	f.batch.Pods = nil
	out := f.engine.Step(f.batch)
	if out.TotalSignals != 0 || out.OmittedSignals != 0 || strings.Contains(out.Coverage.Reason, "budget") {
		t.Fatalf("removed entities counted: %+v", out)
	}
}

func TestBugbashResetKeepsOnlyRetainedStateCounts(t *testing.T) {
	f := newOperationalFixture(t)
	second := f.batch.Pods[0]
	second.Entity.UID = "other"
	f.batch.Pods = append(f.batch.Pods, second)
	f.step(0, nil)
	f.batch.Cursor.Revisions.Inventory++
	f.batch.Reset = true
	f.batch.Pods = f.batch.Pods[:1]
	out := f.engine.Step(f.batch)
	if out.TotalSignals != 6 || out.OmittedSignals != 0 || strings.Contains(out.Coverage.Reason, "budget") {
		t.Fatalf("retained counts: %+v", out)
	}
	for _, signal := range out.Signals {
		if signal.State != SignalSuspended {
			t.Fatal("reset lost uncertainty")
		}
	}
}

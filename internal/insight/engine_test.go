package insight

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

type operationalFixture struct {
	t      *testing.T
	clock  *observationClock
	engine *Engine
	batch  ObservationBatch
	origin time.Time
}

func newOperationalFixture(t *testing.T) *operationalFixture {
	t.Helper()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	clock := &observationClock{now: now}
	engine, err := NewEngine(clock, DefaultOperationalConfig())
	if err != nil {
		t.Fatal(err)
	}
	source := SourceIdentity{SessionID: "fixture", Context: "fake", Generation: 1}
	pod := observationFixture("uid", "1")
	pod.Entity.Source = source
	pod.Phase = PodRunning
	pod.CreatedAt = now.Add(-24 * time.Hour)
	pod.ReceivedAt = now
	pod.Containers = []ContainerObservation{{Name: "app", Kind: ContainerRegular, StatusObserved: true, Ready: TruthTrue, Phase: ContainerRunning}}
	return &operationalFixture{t: t, clock: clock, engine: engine, origin: now, batch: ObservationBatch{Cursor: Cursor{Source: source, Revisions: Revisions{Inventory: 1}}, ObservedAt: now, FullInventory: true, Coverage: SourceCoverage{Inventory: Coverage{State: CoverageComplete, Observed: 1, Total: 1}}, Pods: []PodObservation{pod}}}
}

func (f *operationalFixture) step(at time.Duration, change func(*PodObservation)) SignalSnapshot {
	f.t.Helper()
	f.clock.now = f.origin.Add(at)
	if change != nil {
		f.batch.Cursor.Revisions.Inventory++
		change(&f.batch.Pods[0])
		f.batch.Pods[0].Entity.ResourceVersion = fmt.Sprint(f.batch.Cursor.Revisions.Inventory)
		f.batch.Pods[0].ReceivedAt = f.clock.now
	}
	f.batch.ObservedAt = f.clock.now
	return f.engine.Step(f.batch)
}

func operationalSignal(t *testing.T, s SignalSnapshot, rule Rule) Signal {
	t.Helper()
	for _, signal := range s.Signals {
		if signal.Rule == rule {
			return signal
		}
	}
	t.Fatalf("missing rule %s", rule)
	return Signal{}
}

func operationalWaiting(reason Reason) func(*PodObservation) {
	return func(p *PodObservation) {
		p.Containers[0].Phase = ContainerWaiting
		p.Containers[0].Reason = reason
		p.Containers[0].Ready = TruthFalse
	}
}

func operationalHealthy(p *PodObservation) {
	p.Phase = PodRunning
	p.Containers[0].Phase = ContainerRunning
	p.Containers[0].Reason = ReasonUnknown
	p.Containers[0].Ready = TruthTrue
}

func TestOperationalPersistenceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rule          Rule
		bad           func(*PodObservation)
		warning, high time.Duration
		severity      SignalSeverity
	}{
		{"crash", RuleCrashLoop, operationalWaiting(ReasonCrashLoopBackOff), 30 * time.Second, 0, SignalHigh},
		{"pull", RuleImagePull, operationalWaiting(ReasonErrImagePull), 2 * time.Minute, 10 * time.Minute, SignalWarning},
		{"pending", RulePending, func(p *PodObservation) { p.Phase = PodPending }, 5 * time.Minute, 15 * time.Minute, SignalWarning},
		{"creating", RulePending, operationalWaiting(ReasonContainerCreating), 5 * time.Minute, 15 * time.Minute, SignalWarning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOperationalFixture(t)
			tc.bad(&f.batch.Pods[0])
			initial := operationalSignal(t, f.step(0, nil), tc.rule)
			if initial.State != SignalCandidate || !initial.Preexisting {
				t.Fatalf("initial %+v", initial)
			}
			before := operationalSignal(t, f.step(tc.warning-time.Nanosecond, func(*PodObservation) {}), tc.rule)
			if before.State != SignalCandidate {
				t.Fatal("opened before threshold")
			}
			opened := operationalSignal(t, f.step(tc.warning, nil), tc.rule)
			if opened.State != SignalActive || opened.Severity != tc.severity || opened.ID != initial.ID {
				t.Fatalf("threshold %+v", opened)
			}
			if tc.high > 0 {
				before = operationalSignal(t, f.step(tc.high-time.Nanosecond, nil), tc.rule)
				if before.Severity != SignalWarning {
					t.Fatal("early escalation")
				}
				opened = operationalSignal(t, f.step(tc.high, nil), tc.rule)
				if opened.Severity != SignalHigh {
					t.Fatal("high threshold missing")
				}
			}
			recoveredAt := max(tc.warning, tc.high) + time.Second
			recovering := operationalSignal(t, f.step(recoveredAt, operationalHealthy), tc.rule)
			if recovering.State != SignalRecovering {
				t.Fatal("recovery not started")
			}
			if operationalSignal(t, f.step(recoveredAt+time.Minute-time.Nanosecond, nil), tc.rule).State == SignalResolved {
				t.Fatal("early recovery")
			}
			if operationalSignal(t, f.step(recoveredAt+time.Minute, nil), tc.rule).State != SignalResolved {
				t.Fatal("recovery threshold missing")
			}
		})
	}
}

func TestOperationalCrashRequiresDistinctObservations(t *testing.T) {
	f := newOperationalFixture(t)
	operationalWaiting(ReasonCrashLoopBackOff)(&f.batch.Pods[0])
	f.step(0, nil)
	at := operationalSignal(t, f.step(time.Minute, nil), RuleCrashLoop)
	if at.State != SignalCandidate || at.Evidence.Observations != 1 {
		t.Fatal("replay fabricated second observation")
	}
	at = operationalSignal(t, f.step(time.Minute, func(*PodObservation) {}), RuleCrashLoop)
	if at.State != SignalActive || at.Evidence.Observations != 2 {
		t.Fatal("distinct observation missing")
	}
}

func TestOperationalRestartsWindowsAndCounterReset(t *testing.T) {
	f := newOperationalFixture(t)
	f.batch.Pods[0].Containers[0].RestartCount = 20
	if operationalSignal(t, f.step(0, nil), RuleRestarts).State != SignalNormal {
		t.Fatal("initial counter became a rate")
	}
	change := func(count int32) func(*PodObservation) {
		return func(p *PodObservation) { p.Containers[0].RestartCount = count }
	}
	if operationalSignal(t, f.step(time.Second, change(22)), RuleRestarts).State != SignalNormal {
		t.Fatal("below warning")
	}
	warning := operationalSignal(t, f.step(2*time.Second, change(23)), RuleRestarts)
	if warning.State != SignalActive || warning.Severity != SignalWarning || warning.Evidence.WindowRestarts != 3 || warning.Preexisting {
		t.Fatal("warning delta incorrect")
	}
	high := operationalSignal(t, f.step(3*time.Second, change(30)), RuleRestarts)
	if high.Severity != SignalHigh || high.Evidence.WindowRestarts != 10 {
		t.Fatal("high delta incorrect")
	}
	if operationalSignal(t, f.step(5*time.Minute+3*time.Second-time.Nanosecond, nil), RuleRestarts).State == SignalResolved {
		t.Fatal("early restart quiet recovery")
	}
	if operationalSignal(t, f.step(5*time.Minute+3*time.Second, nil), RuleRestarts).State != SignalResolved {
		t.Fatal("quiet boundary not resolved")
	}
	reset := operationalSignal(t, f.step(6*time.Minute, change(1)), RuleRestarts)
	if reset.State != SignalSuspended || reset.Suspension != SuspensionCounterReset || reset.Coverage.State != CoveragePartial {
		t.Fatal("counter reset claimed health")
	}
	same := operationalSignal(t, f.step(7*time.Minute, nil), RuleRestarts)
	if same.State != SignalSuspended {
		t.Fatal("replay resumed reset counter")
	}
	f.step(8*time.Minute, change(1))
	newDelta := operationalSignal(t, f.step(8*time.Minute+time.Second, change(4)), RuleRestarts)
	if newDelta.Evidence.WindowRestarts != 3 || newDelta.State != SignalActive {
		t.Fatal("new counter baseline lost")
	}
}

func TestOperationalOOMIdentityAndQuietRecovery(t *testing.T) {
	f := newOperationalFixture(t)
	f.step(0, nil)
	add := func(p *PodObservation) {
		p.Containers[0].LastTermination = &Termination{Reason: ReasonOOMKilled, ExitCode: 137, FinishedAt: f.origin.Add(time.Second)}
		p.Containers[0].RestartCount = 1
	}
	opened := operationalSignal(t, f.step(time.Second, add), RuleOOM)
	if opened.Severity != SignalHigh || opened.Preexisting {
		t.Fatalf("new OOM %+v", opened)
	}
	repeat := operationalSignal(t, f.step(2*time.Second, func(p *PodObservation) { p.Containers[0].RestartCount = 2 }), RuleOOM)
	if repeat.ID != opened.ID || repeat.Evidence.EventAt != opened.Evidence.EventAt {
		t.Fatal("unchanged last termination re-alerted")
	}
	if operationalSignal(t, f.step(5*time.Minute+time.Second-time.Nanosecond, nil), RuleOOM).State == SignalResolved {
		t.Fatal("early OOM recovery")
	}
	if operationalSignal(t, f.step(5*time.Minute+time.Second, nil), RuleOOM).State != SignalResolved {
		t.Fatal("OOM quiet recovery missing")
	}
	f2 := newOperationalFixture(t)
	f2.batch.Pods[0].Containers[0].LastTermination = &Termination{Reason: ReasonOOMKilled, FinishedAt: f2.origin.Add(-time.Hour)}
	if !operationalSignal(t, f2.step(0, nil), RuleOOM).Preexisting {
		t.Fatal("old initial OOM called new")
	}
}

func TestOperationalEvictedAndNormalJob(t *testing.T) {
	f := newOperationalFixture(t)
	f.step(0, nil)
	evict := func(p *PodObservation) { p.Phase = PodFailed; p.Reason = ReasonEvicted; p.NodeName = "node-a" }
	opened := operationalSignal(t, f.step(time.Second, evict), RuleEvicted)
	if opened.State != SignalActive || opened.Preexisting || opened.Severity != SignalWarning {
		t.Fatal("eviction transition missing")
	}
	if operationalSignal(t, f.step(5*time.Minute+time.Second-time.Nanosecond, nil), RuleEvicted).State == SignalResolved {
		t.Fatal("early eviction quiet recovery")
	}
	if operationalSignal(t, f.step(5*time.Minute+time.Second, nil), RuleEvicted).State != SignalResolved {
		t.Fatal("terminal eviction never resolved")
	}
	if operationalSignal(t, f.step(7*time.Minute, func(*PodObservation) {}), RuleEvicted).State != SignalResolved {
		t.Fatal("same Evicted state reopened")
	}
	first := newOperationalFixture(t)
	evict(&first.batch.Pods[0])
	if !operationalSignal(t, first.step(0, nil), RuleEvicted).Preexisting {
		t.Fatal("initial eviction called transition")
	}
	job := newOperationalFixture(t)
	job.batch.Pods[0].Phase = PodSucceeded
	job.batch.Pods[0].Containers[0].Phase = ContainerTerminated
	job.batch.Pods[0].Containers[0].CurrentTermination = &Termination{Reason: ReasonCompleted, ExitCode: 0, FinishedAt: job.origin}
	for _, signal := range job.step(time.Hour, nil).Signals {
		if signal.State != SignalNormal {
			t.Fatalf("normal Job triggered %+v", signal)
		}
	}
}

func TestOperationalGapsAndStaleDoNotResolve(t *testing.T) {
	for _, gap := range []bool{false, true} {
		t.Run(fmt.Sprint(gap), func(t *testing.T) {
			f := newOperationalFixture(t)
			f.step(0, nil)
			f.step(time.Second, func(p *PodObservation) { p.Containers[0].RestartCount = 3 })
			f.batch.Cursor.Revisions.Inventory++
			if gap {
				f.batch.Cursor.Revisions.Inventory++
				f.batch.SkippedInventoryRevisions = 1
			} else {
				f.batch.Coverage.Inventory.State = CoverageStale
			}
			suspended := operationalSignal(t, f.step(2*time.Second, nil), RuleRestarts)
			if suspended.State != SignalSuspended {
				t.Fatal("gap not suspended")
			}
			f.clock.now = f.origin.Add(time.Hour)
			if operationalSignal(t, f.engine.Advance(), RuleRestarts).State != SignalSuspended {
				t.Fatal("stale advanced into recovery")
			}
			f.batch.Coverage.Inventory.State = CoverageComplete
			f.batch.SkippedInventoryRevisions = 0
			f.step(time.Hour, func(*PodObservation) {})
			if operationalSignal(t, f.step(time.Hour+time.Second, nil), RuleRestarts).State == SignalResolved {
				t.Fatal("gap counted as quiet time")
			}
		})
	}
}

func TestOperationalDeterminismUIDAndConfig(t *testing.T) {
	a, b := newOperationalFixture(t), newOperationalFixture(t)
	for i := range 5 {
		change := func(p *PodObservation) { p.Containers[0].RestartCount = int32(i * 3) }
		left := a.step(time.Duration(i)*time.Second, change)
		right := b.step(time.Duration(i)*time.Second, change)
		if !reflect.DeepEqual(left, right) {
			t.Fatal("same inputs differ")
		}
		if !reflect.DeepEqual(left, a.step(time.Duration(i)*time.Second, nil)) {
			t.Fatal("replay changed result")
		}
	}
	old := operationalSignal(t, a.engine.Snapshot(), RuleRestarts)
	replaced := operationalSignal(t, a.step(time.Minute, func(p *PodObservation) { p.Entity.UID = "new"; p.Containers[0].RestartCount = 99 }), RuleRestarts)
	if replaced.ID == old.ID || replaced.State != SignalNormal || replaced.Evidence.WindowRestarts != 0 {
		t.Fatal("UID reused baseline")
	}
	config := DefaultOperationalConfig()
	config.RestartWarning = 4
	replacementEngine, err := NewEngine(a.clock, config)
	if err != nil {
		t.Fatal(err)
	}
	a.engine = replacementEngine
	after := operationalSignal(t, a.step(2*time.Minute, nil), RuleRestarts)
	if after.ConfigHash == old.ConfigHash || after.State != SignalNormal {
		t.Fatal("config change did not baseline")
	}
	shuffled := newOperationalFixture(t)
	for i := range 10 {
		p := clonePod(shuffled.batch.Pods[0])
		p.Entity.UID = fmt.Sprint(i)
		p.Entity.Name = fmt.Sprint(i)
		shuffled.batch.Pods = append(shuffled.batch.Pods, p)
	}
	first := shuffled.step(0, nil)
	rand.New(rand.NewSource(42)).Shuffle(len(shuffled.batch.Pods), func(i, j int) {
		shuffled.batch.Pods[i], shuffled.batch.Pods[j] = shuffled.batch.Pods[j], shuffled.batch.Pods[i]
	})
	other := newOperationalFixture(t)
	other.batch = shuffled.batch
	if !reflect.DeepEqual(first, other.step(0, nil)) {
		t.Fatal("input order affected output")
	}
}

func TestOperationalNewTransitionAndInterruptedRecovery(t *testing.T) {
	f := newOperationalFixture(t)
	f.step(0, nil)
	first := operationalSignal(t, f.step(time.Second, operationalWaiting(ReasonCrashLoopBackOff)), RuleCrashLoop)
	if first.Preexisting {
		t.Fatal("observed new transition marked preexisting")
	}
	f.step(31*time.Second, func(*PodObservation) {})
	f.step(32*time.Second, operationalHealthy)
	again := operationalSignal(t, f.step(40*time.Second, operationalWaiting(ReasonCrashLoopBackOff)), RuleCrashLoop)
	if again.State != SignalActive || again.ID != first.ID {
		t.Fatal("interrupted recovery changed episode or claimed recovery")
	}
	f.step(41*time.Second, operationalHealthy)
	if operationalSignal(t, f.step(100*time.Second, nil), RuleCrashLoop).State == SignalResolved {
		t.Fatal("separate healthy intervals combined")
	}
	if operationalSignal(t, f.step(101*time.Second, nil), RuleCrashLoop).State != SignalResolved {
		t.Fatal("continuous recovery missing")
	}
	next := operationalSignal(t, f.step(102*time.Second, operationalWaiting(ReasonCrashLoopBackOff)), RuleCrashLoop)
	if next.ID == first.ID || next.Preexisting {
		t.Fatal("new episode identity missing")
	}
}

func TestOperationalMissingStatusInvalidCountersAndOOMTimes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*PodObservation)
		rule   Rule
		reason SuspensionReason
	}{
		{"missing", func(p *PodObservation) { p.Containers[0].StatusObserved = false }, RuleCrashLoop, SuspensionMissingStatus},
		{"unknown phase", func(p *PodObservation) { p.Phase = PodUnknown }, RulePending, SuspensionMissingStatus},
		{"negative counter", func(p *PodObservation) { p.Containers[0].RestartCount = -1 }, RuleRestarts, SuspensionInvalidCounter},
		{"missing OOM time", func(p *PodObservation) { p.Containers[0].LastTermination = &Termination{Reason: ReasonOOMKilled} }, RuleOOM, SuspensionInvalidTimestamp},
		{"future OOM", func(p *PodObservation) {
			p.Containers[0].LastTermination = &Termination{Reason: ReasonOOMKilled, FinishedAt: time.Date(2026, 10, 16, 12, 0, 31, 0, time.UTC)}
		}, RuleOOM, SuspensionInvalidTimestamp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOperationalFixture(t)
			tc.change(&f.batch.Pods[0])
			signal := operationalSignal(t, f.step(0, nil), tc.rule)
			if signal.State != SignalSuspended || signal.Suspension != tc.reason || signal.Coverage.State != CoveragePartial {
				t.Fatalf("invalid data %+v", signal)
			}
		})
	}
	f := newOperationalFixture(t)
	f.batch.Pods[0].Containers[0].LastTermination = &Termination{Reason: ReasonOther, ExitCode: 137, FinishedAt: f.origin}
	if operationalSignal(t, f.step(0, nil), RuleOOM).State != SignalNormal {
		t.Fatal("exit 137 inferred OOM")
	}
	f.batch.Pods[0].Containers[0].LastTermination = &Termination{Reason: ReasonOOMKilled, FinishedAt: f.origin.Add(30 * time.Second)}
	if operationalSignal(t, f.step(0, func(*PodObservation) {}), RuleOOM).Suspension != SuspensionNone {
		t.Fatal("future tolerance boundary rejected")
	}
}

func TestOperationalRestartBufferCapAndExpiry(t *testing.T) {
	f := newOperationalFixture(t)
	config := DefaultOperationalConfig()
	config.MaxRestartDeltas = 1
	f.engine, _ = NewEngine(f.clock, config)
	f.step(0, nil)
	f.step(time.Second, func(p *PodObservation) { p.Containers[0].RestartCount = 3 })
	at := operationalSignal(t, f.step(2*time.Second, func(p *PodObservation) { p.Containers[0].RestartCount = 4 }), RuleRestarts)
	if at.State != SignalSuspended || at.Suspension != SuspensionBufferLimit {
		t.Fatal("buffer overflow silently lost evidence")
	}
	f = newOperationalFixture(t)
	f.engine, _ = NewEngine(f.clock, config)
	f.step(0, nil)
	f.step(time.Second, func(p *PodObservation) { p.Containers[0].RestartCount = 3 })
	at = operationalSignal(t, f.step(5*time.Minute+time.Second, func(p *PodObservation) { p.Containers[0].RestartCount = 6 }), RuleRestarts)
	if at.State != SignalActive || at.Evidence.WindowRestarts != 3 {
		t.Fatal("expired entries consumed buffer budget")
	}
}

func TestOperationalOOMRecoveryNeedsHealthyMinute(t *testing.T) {
	f := newOperationalFixture(t)
	p := &f.batch.Pods[0]
	p.Containers[0].Phase = ContainerTerminated
	p.Containers[0].Ready = TruthFalse
	p.Containers[0].CurrentTermination = &Termination{Reason: ReasonOOMKilled, FinishedAt: f.origin}
	f.step(0, nil)
	if operationalSignal(t, f.step(10*time.Minute, nil), RuleOOM).State == SignalResolved {
		t.Fatal("quiet alone resolved unhealthy OOM")
	}
	f.step(10*time.Minute, operationalHealthy)
	if operationalSignal(t, f.step(11*time.Minute-time.Nanosecond, nil), RuleOOM).State == SignalResolved {
		t.Fatal("OOM recovery minute missing")
	}
	if operationalSignal(t, f.step(11*time.Minute, nil), RuleOOM).State != SignalResolved {
		t.Fatal("OOM healthy recovery missing")
	}
}

func TestOperationalSourceResetAndOldReplay(t *testing.T) {
	f := newOperationalFixture(t)
	firstBatch := f.batch
	f.step(0, nil)
	f.step(time.Second, func(p *PodObservation) { p.Containers[0].RestartCount = 3 })
	active := f.engine.Snapshot()
	if got := f.engine.Step(firstBatch); !reflect.DeepEqual(got, active) {
		t.Fatal("old revision rewound state")
	}
	f.batch.Cursor.Source.Generation++
	f.batch.Cursor.Revisions.Inventory = 1
	f.batch.Pods[0].Entity.Source = f.batch.Cursor.Source
	got := operationalSignal(t, f.step(time.Minute, nil), RuleRestarts)
	if got.State != SignalNormal || got.Evidence.WindowRestarts != 0 {
		t.Fatal("source baseline leaked")
	}
}

func TestOperationalConfigValidation(t *testing.T) {
	if _, err := NewEngine(nil, DefaultOperationalConfig()); err == nil {
		t.Fatal("nil clock accepted")
	}
	if _, err := NewEngine(&observationClock{}, OperationalConfig{}); err == nil {
		t.Fatal("empty thresholds accepted")
	}
	config := DefaultOperationalConfig()
	engine, err := NewEngine(&observationClock{now: time.Unix(1000, 0)}, config)
	if err != nil {
		t.Fatal(err)
	}
	config.RestartWarning = 100
	if engine.config.RestartWarning != 3 {
		t.Fatal("caller mutated configuration")
	}
}

func TestOperationalSignalLimitIsVisible(t *testing.T) {
	f := newOperationalFixture(t)
	for i := range 3400 {
		p := clonePod(f.batch.Pods[0])
		p.Entity.Name = fmt.Sprint(i)
		p.Entity.UID = fmt.Sprint(i)
		f.batch.Pods = append(f.batch.Pods, p)
	}
	got := f.step(0, nil)
	if len(got.Signals) != MaxOperationalSignals || got.TotalSignals != len(f.batch.Pods)*6 || got.OmittedSignals != got.TotalSignals-MaxOperationalSignals {
		t.Fatal("signal cap not visible")
	}
	total, omitted := 0, 0
	for _, count := range got.Counts {
		total += count.Total
		omitted += count.Omitted
	}
	if total != got.TotalSignals || omitted != got.OmittedSignals {
		t.Fatal("rule summary lost omitted states")
	}
}

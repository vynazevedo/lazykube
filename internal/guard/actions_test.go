package guard

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestObserveRespectsPolicyBeforeIO(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes bool
		env    Environment
		change func(*Target)
		action Action
		want   Reason
	}{
		{"readonly", false, Environment{Valid: true}, nil, Delete, ReadOnly},
		{"context", true, Environment{Valid: true, ReadOnly: true}, nil, Delete, ReadOnly},
		{"protected", true, Environment{Valid: true, Protected: true}, nil, Delete, ReadOnly},
		{"config", true, Environment{}, nil, Delete, InvalidConfig},
		{"uid", true, Environment{Valid: true}, func(t *Target) { t.UID = "" }, Delete, Invalid},
		{"version", true, Environment{Valid: true}, func(t *Target) { t.ResourceVersion = "" }, Delete, Invalid},
		{"namespace", true, Environment{Valid: true}, func(t *Target) { t.Namespace = "other" }, Delete, Invalid},
		{"allowlist", true, Environment{Valid: true}, func(t *Target) { t.Resource = "secrets" }, Delete, Unsupported},
		{"action", true, Environment{Valid: true}, nil, Scale, Unsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, f, _ := guardExecutor(t)
			e.policy, e.session.Environment = NewPolicy(tc.writes), tc.env
			target := guardRequest().Target
			if tc.change != nil {
				tc.change(&target)
			}
			if _, err := e.Observe(context.Background(), tc.action, target); !errors.Is(err, tc.want) || f.observes != 0 || len(f.accesses) != 0 {
				t.Fatalf("err %v observes %d", err, f.observes)
			}
		})
	}
}

func TestObserveMapsErrorsAndRecreation(t *testing.T) {
	e, f, _ := guardExecutor(t)
	target := guardRequest().Target
	for _, tc := range []struct {
		err  error
		want Reason
	}{{Denied, Denied}, {Stale, Stale}, {errors.New("private server response"), Unavailable}} {
		f.observeErr = tc.err
		if _, err := e.Observe(context.Background(), Delete, target); !errors.Is(err, tc.want) {
			t.Fatal(err)
		}
	}
	f.observeErr, f.observeUID = nil, "recreated"
	if _, err := e.Observe(context.Background(), Delete, target); !errors.Is(err, Stale) {
		t.Fatal(err)
	}
	f.observeUID = ""
	e.Close()
	if _, err := e.Observe(context.Background(), Delete, target); !errors.Is(err, Canceled) {
		t.Fatal(err)
	}
}

func TestDiscardConsumesPreviewWithoutSending(t *testing.T) {
	e, f, _ := guardExecutor(t)
	p, err := e.Prepare(context.Background(), guardRequest())
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = e.Discard(p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.events) != 2 || f.events[1].Phase != "canceled" || f.events[1].OperationID != p.ID || len(f.sends) != 1 {
		t.Fatal(f.events, f.sends)
	}
	if _, err = e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"}); !errors.Is(err, Invalid) || len(f.sends) != 1 {
		t.Fatal(err, f.sends)
	}
}

func TestDiscardKeepsCapacityForRepeatedPreviews(t *testing.T) {
	e, _, _ := guardExecutor(t)
	for i := range 40 {
		p, err := e.Prepare(context.Background(), guardRequest())
		if err != nil {
			t.Fatal(i, err)
		}
		if err = e.Discard(p.ID); err != nil {
			t.Fatal(i, err)
		}
	}
}

func TestDiscardAuditFailureBlocksActions(t *testing.T) {
	e, f, _ := guardExecutor(t)
	p, err := e.Prepare(context.Background(), guardRequest())
	if err != nil || e.AuditBlocked() {
		t.Fatal(err)
	}
	f.failPhase = "canceled"
	if err = e.Discard(p.ID); !errors.Is(err, AuditFailed) || !e.AuditBlocked() {
		t.Fatal(err)
	}
	if _, err = e.Prepare(context.Background(), guardRequest()); !errors.Is(err, AuditFailed) {
		t.Fatal(err)
	}
}

func TestConcurrentExecuteSendsOnce(t *testing.T) {
	e, f, _ := guardExecutor(t)
	p, err := e.Prepare(context.Background(), guardRequest())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for range 16 {
		wg.Go(func() {
			r, _ := e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"})
			if r.Outcome == Succeeded {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if succeeded != 1 || len(f.sends) != 2 {
		t.Fatal(succeeded, f.sends)
	}
}

func TestResultAuditFailureKeepsConfirmedOutcome(t *testing.T) {
	for _, tc := range []struct {
		sendErr error
		want    Reason
	}{{nil, Succeeded}, {Denied, Denied}, {Stale, Stale}, {errors.New("reset"), Unknown}} {
		e, f, _ := guardExecutor(t)
		p, err := e.Prepare(context.Background(), guardRequest())
		if err != nil {
			t.Fatal(err)
		}
		f.sendErr, f.failPhase = tc.sendErr, "result"
		r, err := e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"})
		if r.Outcome != tc.want || !errors.Is(err, AuditFailed) || !e.AuditBlocked() {
			t.Fatal(tc.want, r, err)
		}
	}
}

func TestCloseWaitsForInFlightResultAudit(t *testing.T) {
	e, h, _ := reviewExecutor(t)
	p, err := e.Prepare(context.Background(), guardRequest())
	if err != nil {
		t.Fatal(err)
	}
	sending := make(chan struct{})
	e.backend = blockingSend{Backend: h, started: sending}
	done := make(chan Result, 1)
	go func() {
		r, _ := e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"})
		done <- r
	}()
	<-sending
	started := time.Now()
	e.Close()
	if time.Since(started) >= closeWait {
		t.Fatal("close did not observe the canceled operation")
	}
	if last := h.events[len(h.events)-1]; last.Phase != "result" || last.Outcome != Unknown {
		t.Fatal(last)
	}
	if r := <-done; r.Outcome != Unknown {
		t.Fatal(r)
	}
}

type blockingSend struct {
	Backend
	started chan struct{}
}

func (b blockingSend) Send(ctx context.Context, _ Request, dry bool) error {
	if dry {
		return nil
	}
	close(b.started)
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)
	return ctx.Err()
}

func TestNamespacedTargetsRequireNamespace(t *testing.T) {
	s := guardSession()
	s.Namespace = ""
	r := guardRequest()
	r.Target.Namespace = ""
	if _, err := NewPolicy(true).Evaluate(s, r); !errors.Is(err, Invalid) {
		t.Fatal(err)
	}
}

func TestEveryConsumedPlanLeavesOneOutcome(t *testing.T) {
	approved := Confirmation{Approved: true, Resource: "pod"}
	for _, tc := range []struct {
		name    string
		change  func(*Executor, *guardFake, *time.Time)
		confirm Confirmation
		want    Reason
		phase   string
		sends   int
	}{
		{"not approved", nil, Confirmation{Resource: "pod"}, ConfirmationRequired, "denied", 1},
		{"empty", nil, Confirmation{}, ConfirmationRequired, "denied", 1},
		{"wrong name", nil, Confirmation{Approved: true, Resource: "other"}, ConfirmationRequired, "denied", 1},
		{"read only", func(e *Executor, _ *guardFake, _ *time.Time) { e.policy = NewPolicy(false) }, approved, ReadOnly, "denied", 1},
		{"protected", func(e *Executor, _ *guardFake, _ *time.Time) { e.session.Environment.Protected = true }, approved, ReadOnly, "denied", 1},
		{"config", func(e *Executor, _ *guardFake, _ *time.Time) { e.session.Environment.Valid = false }, approved, InvalidConfig, "denied", 1},
		{"session identity", func(e *Executor, _ *guardFake, _ *time.Time) { e.session.Identity = "" }, approved, Invalid, "denied", 1},
		{"revoked", func(_ *Executor, f *guardFake, _ *time.Time) { f.allowed = false }, approved, Denied, "denied", 1},
		{"stale", func(_ *Executor, f *guardFake, _ *time.Time) { f.invalid = true }, approved, Stale, "denied", 1},
		{"expired", func(_ *Executor, _ *guardFake, n *time.Time) { *n = n.Add(time.Hour) }, approved, Canceled, "canceled", 1},
		{"closed", func(e *Executor, _ *guardFake, _ *time.Time) { e.Close() }, approved, Canceled, "canceled", 1},
		{"unknown", func(_ *Executor, f *guardFake, _ *time.Time) { f.sendErr = errors.New("private") }, approved, Unknown, "result", 2},
		{"succeeded", nil, approved, Succeeded, "result", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, f, now := guardExecutor(t)
			p, err := e.Prepare(context.Background(), guardRequest())
			if err != nil {
				t.Fatal(err)
			}
			if tc.change != nil {
				tc.change(e, f, now)
			}
			before := len(f.events)
			r, err := e.Execute(context.Background(), p.ID, tc.confirm)
			if r.Outcome != tc.want || tc.want != Succeeded && !errors.Is(err, tc.want) || len(f.sends) != tc.sends {
				t.Fatal(r, err, f.sends)
			}
			outcomes := 0
			for _, ev := range f.events[before:] {
				if ev.Outcome != "" {
					outcomes++
				}
				if ev.OperationID != p.ID || ev.Name != "pod" || ev.UID != "uid" {
					t.Fatalf("%+v", ev)
				}
			}
			if last := f.events[len(f.events)-1]; outcomes != 1 || last.Phase != tc.phase || last.Outcome != tc.want {
				t.Fatalf("%d outcomes, last %+v", outcomes, last)
			}
			after := len(f.events)
			if _, err = e.Execute(context.Background(), p.ID, tc.confirm); !errors.Is(err, Invalid) || len(f.events) != after {
				t.Fatal("a consumed id has no second outcome", err)
			}
		})
	}
}

func TestUnknownPlanIDInventsNoAuditTarget(t *testing.T) {
	e, f, _ := guardExecutor(t)
	for _, id := range []string{"", "missing"} {
		if r, err := e.Execute(context.Background(), id, Confirmation{Approved: true}); !errors.Is(err, Invalid) || r.Outcome != "" {
			t.Fatal(r, err)
		}
		if err := e.Discard(id); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.events) != 0 {
		t.Fatal(f.events)
	}
}

func TestEarlyRefusalWithBrokenAuditStaysClosed(t *testing.T) {
	e, f, _ := guardExecutor(t)
	p, err := e.Prepare(context.Background(), guardRequest())
	if err != nil {
		t.Fatal(err)
	}
	f.failPhase = "denied"
	r, err := e.Execute(context.Background(), p.ID, Confirmation{})
	if r.Outcome != ConfirmationRequired || !errors.Is(err, AuditFailed) || !e.AuditBlocked() || len(f.sends) != 1 {
		t.Fatal(r, err, f.sends)
	}
	f.failPhase = ""
	recorded := len(f.events)
	if _, err = e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"}); !errors.Is(err, Invalid) {
		t.Fatal("the plan stays consumed", err)
	}
	if _, err = e.Prepare(context.Background(), guardRequest()); !errors.Is(err, AuditFailed) || len(f.events) != recorded || len(f.sends) != 1 {
		t.Fatal(err, f.events)
	}
}

func TestPrunedPlansAreAudited(t *testing.T) {
	e, f, now := guardExecutor(t)
	first, err := e.Prepare(context.Background(), guardRequest())
	if err != nil {
		t.Fatal(err)
	}
	*now = first.ExpiresAt
	if _, err = e.Prepare(context.Background(), guardRequest()); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, ev := range f.events {
		if ev.OperationID == first.ID && ev.Phase == "canceled" && ev.Outcome == Canceled {
			found++
		}
	}
	if found != 1 {
		t.Fatal(f.events)
	}
}

func TestPrunedPlanAuditFailureReleasesNoPreview(t *testing.T) {
	e, f, now := guardExecutor(t)
	first, err := e.Prepare(context.Background(), guardRequest())
	if err != nil {
		t.Fatal(err)
	}
	*now = first.ExpiresAt
	f.failPhase = "canceled"
	if p, err := e.Prepare(context.Background(), guardRequest()); !errors.Is(err, AuditFailed) || p.ID != "" || len(e.plans) != 0 {
		t.Fatal(p, err, len(e.plans))
	}
}

func TestOnlyProvenUnsentFailuresLeaveUnknown(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want Reason
	}{
		{NotSent{Unavailable}, Unavailable}, {NotSent{Denied}, Denied}, {NotSent{Stale}, Stale}, {NotSent{Invalid}, Invalid},
		{NotSent{Succeeded}, Unavailable}, {NotSent{"private server response"}, Unavailable}, {NotSent{Unknown}, Unknown},
		{Unavailable, Unknown}, {Invalid, Unknown}, {Canceled, Unknown}, {errors.New("reset"), Unknown}, {context.Canceled, Unknown},
	} {
		e, f, _ := guardExecutor(t)
		p, err := e.Prepare(context.Background(), guardRequest())
		if err != nil {
			t.Fatal(err)
		}
		f.sendErr = tc.err
		r, err := e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"})
		if r.Outcome != tc.want || !errors.Is(err, tc.want) {
			t.Fatal(tc.err, r, err)
		}
		if last := f.events[len(f.events)-1]; last.Phase != "result" || last.Outcome != tc.want {
			t.Fatal(last)
		}
	}
}

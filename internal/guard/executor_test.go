package guard

import (
	"context"
	"errors"
	"testing"
	"time"
)

type guardFake struct {
	allowed   bool
	accesses  []Access
	sends     []bool
	events    []AuditEvent
	invalid   bool
	failPhase string
	sendErr   error
}

func (f *guardFake) Allowed(_ context.Context, a Access) (bool, error) {
	f.accesses = append(f.accesses, a)
	return f.allowed, nil
}
func (f *guardFake) Supported(Action) bool { return true }
func (f *guardFake) Validate(context.Context, Request) error {
	if f.invalid {
		return Stale
	}
	return nil
}
func (f *guardFake) Send(_ context.Context, _ Request, dry bool) error {
	f.sends = append(f.sends, dry)
	return f.sendErr
}
func (f *guardFake) Record(e AuditEvent) error {
	f.events = append(f.events, e)
	if e.Phase == f.failPhase {
		return AuditFailed
	}
	return nil
}
func guardExecutor(t *testing.T) (*Executor, *guardFake, *time.Time) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := &guardFake{allowed: true}
	e, err := NewExecutor(context.Background(), guardSession(), NewPolicy(true), f, f, f, ClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e, f, &now
}
func TestExecutorDryRunAndFreshAuthorization(t *testing.T) {
	e, f, _ := guardExecutor(t)
	p, err := e.Prepare(context.Background(), guardRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.sends) != 1 || !f.sends[0] {
		t.Fatal(f.sends)
	}
	result, err := e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"})
	if err != nil || result.Outcome != Succeeded {
		t.Fatal(result, err)
	}
	if len(f.accesses) != 4 || len(f.sends) != 2 || f.sends[1] {
		t.Fatal(f)
	}
	if len(f.events) != 3 || f.events[1].Phase != "intent" || f.events[2].Phase != "result" {
		t.Fatal(f.events)
	}
	if _, err = e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"}); !errors.Is(err, Invalid) {
		t.Fatal(err)
	}
}
func TestExecutorBlocksAtEveryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Executor, *guardFake, *time.Time)
		want   Reason
	}{
		{"revoked", func(_ *Executor, f *guardFake, _ *time.Time) { f.allowed = false }, Denied},
		{"recreated", func(_ *Executor, f *guardFake, _ *time.Time) { f.invalid = true }, Stale},
		{"session", func(e *Executor, _ *guardFake, _ *time.Time) { e.Close() }, Canceled},
		{"expired", func(_ *Executor, _ *guardFake, n *time.Time) { *n = n.Add(3 * time.Minute) }, Canceled},
		{"audit", func(_ *Executor, f *guardFake, _ *time.Time) { f.failPhase = "intent" }, AuditFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, f, n := guardExecutor(t)
			p, err := e.Prepare(context.Background(), guardRequest())
			if err != nil {
				t.Fatal(err)
			}
			tc.change(e, f, n)
			_, err = e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"})
			if !errors.Is(err, tc.want) || len(f.sends) != 1 {
				t.Fatal(err, f.sends)
			}
		})
	}
}
func TestExecutorDryRunFailureAndUnknownOutcome(t *testing.T) {
	e, f, _ := guardExecutor(t)
	f.sendErr = errors.New("private server response")
	if _, err := e.Prepare(context.Background(), guardRequest()); !errors.Is(err, DryRunFailed) {
		t.Fatal(err)
	}
	f.sendErr = nil
	p, err := e.Prepare(context.Background(), guardRequest())
	if err != nil {
		t.Fatal(err)
	}
	f.sendErr = errors.New("transport failed")
	r, err := e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"})
	if !errors.Is(err, Unknown) || r.Outcome != Unknown {
		t.Fatal(r, err)
	}
}
func TestExecutorAuditFailureIsSticky(t *testing.T) {
	e, f, _ := guardExecutor(t)
	f.failPhase = "preview"
	if _, err := e.Prepare(context.Background(), guardRequest()); !errors.Is(err, AuditFailed) {
		t.Fatal(err)
	}
	f.failPhase = ""
	if _, err := e.Prepare(context.Background(), guardRequest()); !errors.Is(err, AuditFailed) || len(f.sends) != 0 {
		t.Fatal(err, f.sends)
	}
}

package guard

import (
	"context"
	"errors"
	"testing"
)

type checkedAudit struct {
	*guardFake
	err    error
	checks []bool
}

func (a *checkedAudit) Check(create bool) error {
	a.checks = append(a.checks, create)
	return a.err
}

func checkedExecutor(t *testing.T) (*Executor, *checkedAudit) {
	t.Helper()
	e, f, _ := guardExecutor(t)
	a := &checkedAudit{guardFake: f}
	e.audit = a
	return e, a
}

var auditRefusals = []AuditRefusal{RefusedPlatform, RefusedFilesystem, RefusedLink, RefusedOwner, RefusedPrivate, RefusedForm}

func TestRefusedAuditReachesNoBackend(t *testing.T) {
	for _, refusal := range auditRefusals {
		for stage, call := range map[string]func(*Executor) error{
			"observe": func(e *Executor) error {
				_, err := e.Observe(context.Background(), Delete, guardRequest().Target)
				return err
			},
			"prepare": func(e *Executor) error {
				_, err := e.Prepare(context.Background(), guardRequest())
				return err
			},
			"check": func(e *Executor) error { return e.CheckAudit(false) },
		} {
			e, a := checkedExecutor(t)
			a.err = refusal
			for range 3 {
				if err := call(e); !errors.Is(err, AuditFailed) {
					t.Fatal(stage, err)
				}
			}
			if a.observes != 0 || len(a.accesses) != 0 || len(a.sends) != 0 || len(a.events) != 0 || len(a.checks) != 1 {
				t.Fatalf("%s %q: %+v checks %v", stage, refusal, a.guardFake, a.checks)
			}
			if !e.AuditBlocked() || e.AuditRefusal() != refusal {
				t.Fatal(stage, e.AuditRefusal())
			}
		}
	}
}

func TestAuditRefusedAfterPreviewStopsBeforeRequests(t *testing.T) {
	e, a := checkedExecutor(t)
	p, err := e.Prepare(context.Background(), guardRequest())
	if err != nil {
		t.Fatal(err)
	}
	accesses, sends := len(a.accesses), len(a.sends)
	a.err = RefusedOwner
	r, err := e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"})
	if !errors.Is(err, AuditFailed) || r.Outcome != "" || len(a.accesses) != accesses || len(a.sends) != sends || e.Dispatch(p.ID).Sent || len(e.plans) != 0 {
		t.Fatal(r, err, a.accesses, a.sends)
	}
}

func TestReadOnlyNeverTouchesTheAuditPath(t *testing.T) {
	for _, env := range []Environment{{Valid: true, ReadOnly: true}, {Valid: true, Protected: true}, {}} {
		e, a := checkedExecutor(t)
		e.session.Environment = env
		_, _ = e.Observe(context.Background(), Delete, guardRequest().Target)
		if len(a.checks) != 0 {
			t.Fatal(env, a.checks)
		}
	}
}

func TestVerifyStillReadsWhenAuditIsBlocked(t *testing.T) {
	e, a := checkedExecutor(t)
	a.err = RefusedPrivate
	_ = e.CheckAudit(true)
	if _, err := e.Verify(context.Background(), Delete, guardRequest().Target); err != nil || a.observes != 1 || len(a.accesses) != 0 {
		t.Fatal(err, a.observes)
	}
}

func TestGenericAuditFailureHasNoRefusal(t *testing.T) {
	e, a := checkedExecutor(t)
	a.err = errors.New("private path detail")
	if err := e.CheckAudit(true); !errors.Is(err, AuditFailed) || err.Error() != string(AuditFailed) || e.AuditRefusal() != "" || !e.AuditBlocked() {
		t.Fatal(err, e.AuditRefusal())
	}
}

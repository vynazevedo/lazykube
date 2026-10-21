package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestReviewPrepareDenialsAreAudited(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*Executor, *reviewHooks)
		phase  string
		reason Reason
	}{
		{"readonly", func(e *Executor, _ *reviewHooks) { e.policy = NewPolicy(false) }, "denied", ReadOnly},
		{"protected", func(e *Executor, _ *reviewHooks) { e.session.Environment.Protected = true }, "denied", ReadOnly},
		{"denied", func(_ *Executor, h *reviewHooks) { h.allowed = false }, "denied", Denied},
		{"unavailable", func(_ *Executor, h *reviewHooks) {
			h.allowedHook = func(context.Context) error { return errors.New("private server details") }
		}, "unavailable", Unavailable},
		{"canceled", func(e *Executor, _ *reviewHooks) { e.Close() }, "canceled", Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, h, _ := reviewExecutor(t)
			tc.setup(e, h)
			_, err := e.Prepare(context.Background(), guardRequest())
			if !errors.Is(err, tc.reason) || len(h.events) != 1 {
				t.Fatalf("err %v events %+v", err, h.events)
			}
			event := h.events[0]
			if event.OperationID == "" || event.Phase != tc.phase || event.Outcome != tc.reason || len(h.sends) != 0 {
				t.Fatal(event, h.sends)
			}
		})
	}
}
func TestReviewDeniedAuditFailureBlocks(t *testing.T) {
	e, h, _ := reviewExecutor(t)
	e.policy = NewPolicy(false)
	h.failPhase = "denied"
	_, err := e.Prepare(context.Background(), guardRequest())
	if !errors.Is(err, AuditFailed) {
		t.Fatal(err)
	}
}
func TestReviewPreviewExpiresInsidePreflight(t *testing.T) {
	for _, stage := range []string{"authorization", "validation", "intent"} {
		t.Run(stage, func(t *testing.T) {
			e, h, now := reviewExecutor(t)
			p, err := e.Prepare(context.Background(), guardRequest())
			if err != nil {
				t.Fatal(err)
			}
			advance := func() { *now = p.ExpiresAt }
			switch stage {
			case "authorization":
				h.allowedHook = func(context.Context) error { advance(); return nil }
			case "validation":
				h.validateHook = func(context.Context) error { advance(); return nil }
			case "intent":
				h.recordHook = func(event AuditEvent) error {
					if event.Phase == "intent" {
						advance()
					}
					return nil
				}
			}
			result, err := e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"})
			if !errors.Is(err, Canceled) || result.Outcome != Canceled || len(h.sends) != 1 {
				t.Fatal(result, err, h.sends)
			}
			if last := h.events[len(h.events)-1]; last.Outcome != Canceled {
				t.Fatal(last)
			}
		})
	}
}
func TestReviewPreflightContextHasRemainingDeadline(t *testing.T) {
	e, h, now := reviewExecutor(t)
	p, err := e.Prepare(context.Background(), guardRequest())
	if err != nil {
		t.Fatal(err)
	}
	*now = p.ExpiresAt.Add(-time.Minute)
	h.allowedHook = func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > time.Minute {
			t.Error("preflight has no bounded deadline")
		}
		return nil
	}
	_, err = e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: "pod"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReviewDeniedAuditContainsNoPayload(t *testing.T) {
	e, h, _ := reviewExecutor(t)
	h.allowed = false
	r := guardRequest()
	r.Parameters.Command = []string{"private-command-sentinel"}
	r.Parameters.Document = []byte("private-payload-sentinel")
	_, err := e.Prepare(context.Background(), r)
	if !errors.Is(err, Denied) {
		t.Fatal(err)
	}
	raw, err := json.Marshal(h.events)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-command-sentinel", "private-payload-sentinel"} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("audit exposed request payload")
		}
	}
}

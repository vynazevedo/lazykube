package guard

import (
	"context"
	"testing"
	"time"
)

type reviewHooks struct {
	*guardFake
	allowedHook  func(context.Context) error
	validateHook func(context.Context) error
	recordHook   func(AuditEvent) error
}

func (h *reviewHooks) Allowed(ctx context.Context, a Access) (bool, error) {
	if h.allowedHook != nil {
		if err := h.allowedHook(ctx); err != nil {
			return false, err
		}
	}
	return h.guardFake.Allowed(ctx, a)
}
func (h *reviewHooks) Validate(ctx context.Context, r Request) error {
	if h.validateHook != nil {
		if err := h.validateHook(ctx); err != nil {
			return err
		}
	}
	return h.guardFake.Validate(ctx, r)
}
func (h *reviewHooks) Record(e AuditEvent) error {
	if h.recordHook != nil {
		if err := h.recordHook(e); err != nil {
			return err
		}
	}
	return h.guardFake.Record(e)
}
func reviewExecutor(t *testing.T) (*Executor, *reviewHooks, *time.Time) {
	t.Helper()
	e, f, now := guardExecutor(t)
	h := &reviewHooks{guardFake: f}
	e.auth = h
	e.backend = h
	e.audit = h
	return e, h, now
}

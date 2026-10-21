package guard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"sync"
	"time"
)

type Preview struct {
	ID        string
	Action    Action
	Target    Target
	Decision  Decision
	ExpiresAt time.Time
}
type Confirmation struct {
	Approved          bool
	Context, Resource string
}
type Result struct {
	OperationID string
	Outcome     Reason
}
type plan struct {
	preview Preview
	request Request
}
type Executor struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	session Session
	policy  Policy
	auth    Authorizer
	backend Backend
	audit   Auditor
	clock   Clock
	plans   map[string]plan
	failed  bool
}

func NewExecutor(ctx context.Context, s Session, p Policy, a Authorizer, b Backend, audit Auditor, clock Clock) (*Executor, error) {
	if ctx == nil || a == nil || b == nil || audit == nil || clock == nil {
		return nil, Invalid
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	return &Executor{ctx: sessionCtx, cancel: cancel, session: s, policy: p, auth: a, backend: b, audit: audit, clock: clock, plans: make(map[string]plan)}, nil
}
func (e *Executor) Close()     { e.cancel() }
func (e *Executor) Mode() Mode { return e.policy.Mode(e.session) }
func (e *Executor) operationContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(e.ctx, cancel)
	if e.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}
func (e *Executor) access(ctx context.Context, r Request) error {
	for _, a := range Requirements(r) {
		ok, err := e.auth.Allowed(ctx, a)
		if err != nil {
			return Unavailable
		}
		if !ok {
			return Denied
		}
	}
	return nil
}
func (e *Executor) event(id string, r Request, d Decision, phase string, outcome Reason) error {
	if e.failed {
		return AuditFailed
	}
	err := e.audit.Record(AuditEvent{SchemaVersion: 1, Timestamp: e.clock.Now().UTC(), OperationID: id, Phase: phase, Action: r.Action, Context: e.session.Context, Identity: e.session.Identity, Namespace: r.Target.Namespace, Group: r.Target.Group, Resource: r.Target.Resource, Name: r.Target.Name, UID: r.Target.UID, ReadOnly: d.Mode.ReadOnly, Protected: d.Mode.Protected, Outcome: outcome})
	if err != nil {
		e.failed = true
		return AuditFailed
	}
	return nil
}
func (e *Executor) Prepare(parent context.Context, r Request) (Preview, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ctx, cancel := e.operationContext(parent)
	defer cancel()
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return Preview{}, Unavailable
	}
	id := hex.EncodeToString(token[:])
	d, err := e.policy.Evaluate(e.session, r)
	reject := func(reason Reason) (Preview, error) {
		if auditErr := e.rejection(id, r, d, reason); auditErr != nil {
			return Preview{}, auditErr
		}
		return Preview{}, reason
	}
	if ctx.Err() != nil {
		return reject(Canceled)
	}
	if err != nil {
		return reject(safeReason(err, Invalid))
	}
	if e.failed {
		return Preview{}, AuditFailed
	}
	if !e.backend.Supported(r.Action) {
		return reject(Unsupported)
	}
	if err = e.access(ctx, r); err != nil {
		if ctx.Err() != nil {
			return reject(Canceled)
		}
		return reject(safeReason(err, Unavailable))
	}
	if ctx.Err() != nil {
		return reject(Canceled)
	}
	r.Parameters.Command = slices.Clone(r.Parameters.Command)
	r.Parameters.Document = slices.Clone(r.Parameters.Document)
	if r.Action == Restart {
		r.Parameters.Document = []byte(e.clock.Now().UTC().Format(time.RFC3339Nano))
	}
	if err = e.backend.Validate(ctx, r); err != nil {
		if ctx.Err() != nil {
			return reject(Canceled)
		}
		return reject(safeReason(err, Stale))
	}
	if ctx.Err() != nil {
		return reject(Canceled)
	}
	if err = e.event(id, r, d, "preview", ""); err != nil {
		return Preview{}, err
	}
	if d.DryRun {
		if err = e.backend.Send(ctx, r, true); err != nil {
			if ctx.Err() != nil {
				return reject(Canceled)
			}
			return reject(DryRunFailed)
		}
	}
	if ctx.Err() != nil {
		return reject(Canceled)
	}
	now := e.clock.Now()
	for id, p := range e.plans {
		if !now.Before(p.preview.ExpiresAt) {
			delete(e.plans, id)
		}
	}
	if len(e.plans) >= 16 {
		return reject(Unavailable)
	}
	out := Preview{ID: id, Action: r.Action, Target: r.Target, Decision: d, ExpiresAt: now.Add(2 * time.Minute)}
	e.plans[out.ID] = plan{preview: out, request: r}
	return out, nil
}
func (e *Executor) Execute(parent context.Context, id string, c Confirmation) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := Result{OperationID: id}
	p, ok := e.plans[id]
	if !ok {
		return result, Invalid
	}
	delete(e.plans, id)
	ctx, cancel := e.operationContext(parent)
	defer cancel()
	ctx, deadlineCancel := context.WithTimeout(ctx, max(0, p.preview.ExpiresAt.Sub(e.clock.Now())))
	defer deadlineCancel()
	d, err := e.policy.Evaluate(e.session, p.request)
	if err != nil {
		return result, err
	}
	if !c.Approved || d.HighRisk && c.Resource != p.request.Target.Name || d.Mode.Protected && c.Context != e.session.Context {
		return result, ConfirmationRequired
	}
	if e.planExpired(ctx, p) {
		result.Outcome = Canceled
		if err = e.event(id, p.request, d, "canceled", Canceled); err != nil {
			return result, err
		}
		return result, Canceled
	}
	cancelBeforeSend := func() (Result, error) {
		result.Outcome = Canceled
		if auditErr := e.rejection(id, p.request, d, Canceled); auditErr != nil {
			return result, auditErr
		}
		return result, Canceled
	}
	err = e.access(ctx, p.request)
	if e.planExpired(ctx, p) {
		return cancelBeforeSend()
	}
	if err != nil {
		result.Outcome = safeReason(err, Unavailable)
		if auditErr := e.rejection(id, p.request, d, result.Outcome); auditErr != nil {
			return result, auditErr
		}
		return result, err
	}
	err = e.backend.Validate(ctx, p.request)
	if e.planExpired(ctx, p) {
		return cancelBeforeSend()
	}
	if err != nil {
		result.Outcome = safeReason(err, Stale)
		if auditErr := e.rejection(id, p.request, d, result.Outcome); auditErr != nil {
			return result, auditErr
		}
		return result, result.Outcome
	}
	if err = e.event(id, p.request, d, "intent", ""); err != nil {
		return result, err
	}
	if e.planExpired(ctx, p) {
		result.Outcome = Canceled
	} else {
		err = e.backend.Send(ctx, p.request, false)
		switch {
		case err == nil:
			result.Outcome = Succeeded
		case errors.Is(err, Denied):
			result.Outcome = Denied
		case errors.Is(err, Stale):
			result.Outcome = Stale
		default:
			result.Outcome = Unknown
		}
	}
	if auditErr := e.event(id, p.request, d, "result", result.Outcome); auditErr != nil {
		return result, auditErr
	}
	if result.Outcome != Succeeded {
		return result, result.Outcome
	}
	return result, nil
}

func (e *Executor) planExpired(ctx context.Context, p plan) bool {
	return ctx.Err() != nil || !e.clock.Now().Before(p.preview.ExpiresAt)
}
func safeReason(err error, fallback Reason) Reason {
	var reason Reason
	if errors.As(err, &reason) {
		switch reason {
		case Invalid, ReadOnly, InvalidConfig, Denied, Unavailable, Unsupported, Stale, Canceled, AuditFailed, DryRunFailed, ConfirmationRequired, Unknown:
			return reason
		}
	}
	return fallback
}
func (e *Executor) rejection(id string, r Request, d Decision, reason Reason) error {
	phase := "denied"
	switch reason {
	case Canceled:
		phase = "canceled"
	case Unavailable:
		phase = "unavailable"
	}
	return e.event(id, r, d, phase, reason)
}

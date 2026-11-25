package guard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
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
type Dispatch struct {
	Sent     bool
	Outcome  Reason
	Recorded bool
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
	failed  atomic.Bool

	sendMu sync.Mutex
	sends  map[string]Dispatch
}

func NewExecutor(ctx context.Context, s Session, p Policy, a Authorizer, b Backend, audit Auditor, clock Clock) (*Executor, error) {
	if ctx == nil || a == nil || b == nil || audit == nil || clock == nil {
		return nil, Invalid
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	return &Executor{ctx: sessionCtx, cancel: cancel, session: s, policy: p, auth: a, backend: b, audit: audit, clock: clock, plans: make(map[string]plan), sends: make(map[string]Dispatch)}, nil
}

const closeWait = 2 * time.Second

func (e *Executor) Close() {
	e.cancel()
	idle := make(chan struct{})
	go func() {
		defer close(idle)
		e.mu.Lock()
		defer e.mu.Unlock()
		_ = e.cancelPlans(e.plansWhere(func(plan) bool { return true }))
	}()
	timer := time.NewTimer(closeWait)
	defer timer.Stop()
	select {
	case <-idle:
	case <-timer.C:
	}
}

func (e *Executor) Dispatch(id string) Dispatch {
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	return e.sends[id]
}

func (e *Executor) dispatched(id string, d Dispatch) {
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	if !d.Sent {
		delete(e.sends, id)
		return
	}
	e.sends[id] = d
}

func (e *Executor) Mode() Mode         { return e.policy.Mode(e.session) }
func (e *Executor) AuditBlocked() bool { return e.failed.Load() }

func (e *Executor) Observe(parent context.Context, action Action, t Target) (Observation, error) {
	mode := e.Mode()
	switch {
	case !mode.ConfigValid:
		return Observation{}, InvalidConfig
	case mode.ReadOnly:
		return Observation{}, ReadOnly
	case !SupportsAction(action, t) || !e.backend.Supported(action):
		return Observation{}, Unsupported
	case t.Name == "" || t.UID == "" || t.ResourceVersion == "" || !inScope(e.session, t):
		return Observation{}, Invalid
	}
	ctx, cancel := e.operationContext(parent)
	defer cancel()
	out, err := e.backend.Observe(ctx, action, t)
	switch {
	case ctx.Err() != nil:
		return Observation{}, Canceled
	case err != nil:
		return Observation{}, safeReason(err, Unavailable)
	case out.Target.UID != t.UID:
		return Observation{}, Stale
	}
	return out, nil
}

func (e *Executor) Discard(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cancelPlans(e.plansWhere(func(p plan) bool { return p.preview.ID == id }))
}
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
	if e.failed.Load() {
		return AuditFailed
	}
	err := e.audit.Record(AuditEvent{SchemaVersion: 1, Timestamp: e.clock.Now().UTC(), OperationID: id, Phase: phase, Action: r.Action, Context: e.session.Context, Identity: e.session.Identity, Namespace: r.Target.Namespace, Group: r.Target.Group, Resource: r.Target.Resource, Name: r.Target.Name, UID: r.Target.UID, ReadOnly: d.Mode.ReadOnly, Protected: d.Mode.Protected, Outcome: outcome})
	if err != nil {
		e.failed.Store(true)
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
	if e.failed.Load() {
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
	expired := e.plansWhere(func(p plan) bool { return !now.Before(p.preview.ExpiresAt) })
	if err = e.cancelPlans(expired); err != nil {
		return Preview{}, err
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
		if e.ctx.Err() != nil {
			result.Outcome = Canceled
			return result, Canceled
		}
		return result, Invalid
	}
	delete(e.plans, id)
	ctx, cancel := e.operationContext(parent)
	defer cancel()
	ctx, deadlineCancel := context.WithTimeout(ctx, max(0, p.preview.ExpiresAt.Sub(e.clock.Now())))
	defer deadlineCancel()
	refuse := func(d Decision, reason Reason) (Result, error) {
		result.Outcome = reason
		if auditErr := e.rejection(id, p.request, d, reason); auditErr != nil {
			return result, auditErr
		}
		return result, reason
	}
	d, err := e.policy.Evaluate(e.session, p.request)
	if err != nil {
		return refuse(d, safeReason(err, Invalid))
	}
	if !c.Approved || d.HighRisk && c.Resource != p.request.Target.Name || d.Mode.Protected && c.Context != e.session.Context {
		return refuse(d, ConfirmationRequired)
	}
	if e.planExpired(ctx, p) {
		return refuse(d, Canceled)
	}
	err = e.access(ctx, p.request)
	if e.planExpired(ctx, p) {
		return refuse(d, Canceled)
	}
	if err != nil {
		return refuse(d, safeReason(err, Unavailable))
	}
	err = e.backend.Validate(ctx, p.request)
	if e.planExpired(ctx, p) {
		return refuse(d, Canceled)
	}
	if err != nil {
		return refuse(d, safeReason(err, Stale))
	}
	if err = e.event(id, p.request, d, "intent", ""); err != nil {
		return result, err
	}
	var state Dispatch
	if e.planExpired(ctx, p) {
		result.Outcome = Canceled
	} else {
		state.Sent = true
		e.dispatched(id, state)
		err = e.backend.Send(ctx, p.request, false)
		var unsent NotSent
		switch {
		case err == nil:
			result.Outcome = Succeeded
		case errors.As(err, &unsent):
			result.Outcome, state.Sent = safeReason(unsent.Reason, Unavailable), false
		case errors.Is(err, Denied):
			result.Outcome = Denied
		case errors.Is(err, Stale):
			result.Outcome = Stale
		default:
			result.Outcome = Unknown
		}
	}
	auditErr := e.event(id, p.request, d, "result", result.Outcome)
	state.Outcome, state.Recorded = result.Outcome, auditErr == nil
	e.dispatched(id, state)
	if auditErr != nil {
		return result, auditErr
	}
	if result.Outcome != Succeeded {
		return result, result.Outcome
	}
	return result, nil
}

func (e *Executor) plansWhere(match func(plan) bool) []plan {
	var out []plan
	for _, p := range e.plans {
		if match(p) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b plan) int { return a.preview.ExpiresAt.Compare(b.preview.ExpiresAt) })
	return out
}

func (e *Executor) cancelPlans(plans []plan) error {
	var failed error
	for _, p := range plans {
		delete(e.plans, p.preview.ID)
		if err := e.event(p.preview.ID, p.request, p.preview.Decision, "canceled", Canceled); err != nil {
			failed = err
		}
	}
	return failed
}

func (e *Executor) planExpired(ctx context.Context, p plan) bool {
	return ctx.Err() != nil || !e.clock.Now().Before(p.preview.ExpiresAt)
}
func safeReason(err error, fallback Reason) Reason {
	var reason Reason
	if errors.As(err, &reason) {
		switch reason {
		case Invalid, ReadOnly, InvalidConfig, Denied, Unavailable, Unsupported, Stale, Canceled, AuditFailed, DryRunFailed, ConfirmationRequired, Unknown, Unchanged:
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

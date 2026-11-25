package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type actionBackendFake struct {
	mu         sync.Mutex
	denied     bool
	authErr    error
	obs        guard.Observation
	obsErr     error
	obsUID     string
	obsVersion string
	validate   error
	dryErr     error
	sendErr    error
	failPhase  string
	onRecord   func(guard.AuditEvent)
	checkErr   error
	checks     int
	block      chan struct{}
	blockSend  chan struct{}
	sending    chan struct{}

	calls    int
	observes int
	dry      []guard.Request
	real     []guard.Request
	events   []guard.AuditEvent
}

func (f *actionBackendFake) Allowed(ctx context.Context, _ guard.Access) (bool, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return !f.denied, f.authErr
}

func (f *actionBackendFake) Supported(a guard.Action) bool {
	return a != guard.Exec && a != guard.PortForward
}

func (f *actionBackendFake) Observe(_ context.Context, _ guard.Action, t guard.Target) (guard.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.observes++
	out := f.obs
	out.Target = t
	if f.obsUID != "" {
		out.Target.UID = f.obsUID
	}
	if f.obsVersion != "" {
		out.Target.ResourceVersion = f.obsVersion
	}
	return out, f.obsErr
}

func (f *actionBackendFake) Validate(context.Context, guard.Request) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.validate
}

func (f *actionBackendFake) Send(ctx context.Context, r guard.Request, dry bool) error {
	f.mu.Lock()
	f.calls++
	if dry {
		f.dry = append(f.dry, r)
		defer f.mu.Unlock()
		return f.dryErr
	}
	f.real = append(f.real, r)
	err, block := f.sendErr, f.blockSend
	f.mu.Unlock()
	if block != nil {
		close(f.sending)
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *actionBackendFake) Check(bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.checkErr
}

func (f *actionBackendFake) Record(e guard.AuditEvent) error {
	if f.onRecord != nil {
		f.onRecord(e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	if f.failPhase == e.Phase {
		return guard.AuditFailed
	}
	return nil
}

type actionLive struct {
	*data.Demo
	exec  *guard.Executor
	stamp *actionRows
}

type actionRows struct {
	uid    string
	gone   bool
	noUID  bool
	cordon bool
}

func (s actionLive) Actions() *guard.Executor { return s.exec }

func (s actionLive) GuardSession() guard.Session {
	return guard.Session{Context: s.Demo.Snapshot().Cluster.Context, Namespace: s.Demo.Snapshot().Cluster.Namespace, Identity: "session-1", Generation: 1, Environment: guard.Environment{Valid: true}}
}

func (s actionLive) AccessMode() guard.Mode { return s.exec.Mode() }

func (s actionLive) Snapshot() data.Snapshot {
	snap := s.Demo.Snapshot()
	snap.Synthetic = false
	suffix := s.stamp.uid
	pods := append([]data.Pod(nil), snap.Pods...)
	for i := range pods {
		pods[i].UID, pods[i].ResourceVersion = "uid-"+pods[i].Name+suffix, "41"
		if s.stamp.noUID {
			pods[i].UID, pods[i].ResourceVersion = "", ""
		}
	}
	deps := append([]data.Deployment(nil), snap.Deployments...)
	for i := range deps {
		deps[i].UID, deps[i].ResourceVersion = "uid-"+deps[i].Name+suffix, "42"
	}
	if s.stamp.gone {
		pods, deps = pods[1:], deps[1:]
		pods = append([]data.Pod(nil), pods...)
		for i, p := range pods {
			if p.Key() == livePod {
				pods = append(pods[:i], pods[i+1:]...)
				break
			}
		}
	}
	snap.Pods, snap.Deployments = pods, deps
	snap.Node.UID, snap.Node.ResourceVersion, snap.Node.Unschedulable = "uid-node"+suffix, "43", s.stamp.cordon
	return snap
}

type actionClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *actionClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

type actionRig struct {
	t     *testing.T
	m     Model
	back  *actionBackendFake
	exec  *guard.Executor
	rows  *actionRows
	clock *actionClock
}

func newActionRig(t *testing.T, w, h int, env guard.Environment, writes bool) *actionRig {
	t.Helper()
	rig := &actionRig{t: t, back: &actionBackendFake{obs: guard.Observation{Replicas: 3, GraceSeconds: 45, GraceKnown: true}}, rows: &actionRows{}, clock: &actionClock{now: time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)}}
	session := guard.Session{Context: "laboratorio", Identity: "session-1", Generation: 12, Environment: env}
	exec, err := guard.NewExecutor(context.Background(), session, guard.NewPolicy(writes), rig.back, rig.back, rig.back, rig.clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(exec.Close)
	rig.exec = exec
	src := actionLive{Demo: data.NewDemo(), exec: exec, stamp: rig.rows}
	rig.m = New(Options{Source: src, Palette: theme.DefaultPalette(), InitialPod: 1, NoAnim: true, Now: rig.clock.Now, Session: data.SessionRef{Context: "laboratorio", Generation: 12, Mode: sessionMode(exec)}})
	rig.m.after = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	rig.m.actTick = func(context.Context, time.Duration, tea.Msg) tea.Cmd { return nil }
	next, _ := rig.m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	rig.m = next.(Model)
	return rig
}

func sessionMode(exec *guard.Executor) data.EffectiveMode {
	mode := exec.Mode()
	return data.EffectiveMode{ReadOnly: mode.ReadOnly, Protected: mode.Protected, Valid: mode.ConfigValid}
}

func writableRig(t *testing.T) *actionRig {
	t.Helper()
	return newActionRig(t, 130, 40, guard.Environment{Valid: true}, true)
}

func (r *actionRig) send(msg tea.Msg) tea.Cmd {
	next, cmd := r.m.Update(msg)
	r.m = next.(Model)
	return cmd
}

func (r *actionRig) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			r.run(c)
		}
	default:
		r.run(r.send(msg))
	}
}

func (r *actionRig) keys(keys ...string) {
	for _, k := range keys {
		r.run(r.send(keyPress(k)))
	}
}

func (r *actionRig) text(s string) {
	for _, c := range s {
		r.run(r.send(tea.KeyPressMsg{Code: c, Text: string(c)}))
	}
}

func (r *actionRig) onDeployment() {
	r.t.Helper()
	r.keys("3")
	r.m.setFocus(FocusDeployments)
	r.m.depCursor = 0
}

func (r *actionRig) choose(action guard.Action) {
	r.t.Helper()
	r.keys("o")
	for i, s := range r.m.act.items {
		if s.action == action {
			for range i {
				r.keys("down")
			}
			r.keys("enter")
			return
		}
	}
	r.t.Fatalf("action %d is not offered for %+v", action, r.m.act.target)
}

func (r *actionRig) toButton(c actionControl) {
	r.t.Helper()
	for range 12 {
		if i := r.m.act.focus - len(r.m.actionFields()); i >= 0 && r.m.actionButtons()[i].control == c {
			return
		}
		r.keys("tab")
	}
	r.t.Fatalf("control %d is not reachable in stage %d", c, r.m.act.stage)
}

func (r *actionRig) scalePreview(n string) {
	r.t.Helper()
	r.onDeployment()
	r.choose(guard.Scale)
	r.text(n)
	r.toButton(controlPrepare)
	r.keys("enter")
	r.stage(actionPreview)
}

func (r *actionRig) stage(want actionStage) {
	r.t.Helper()
	if r.m.act.stage != want {
		r.t.Fatalf("stage %d, want %d: %+v %q", r.m.act.stage, want, r.m.act.lines, r.m.act.err)
	}
}

func (r *actionRig) sent(dry, live int) {
	r.t.Helper()
	r.back.mu.Lock()
	defer r.back.mu.Unlock()
	if len(r.back.dry) != dry || len(r.back.real) != live {
		r.t.Fatalf("dry-run %d, real %d; want %d and %d", len(r.back.dry), len(r.back.real), dry, live)
	}
}

func (r *actionRig) plain() string { return r.m.Render().Plain() }

func (r *actionRig) dialog() string { return dialogText(r.m) }

func dialogText(m Model) string {
	c := draw.NewCanvas(m.w, m.h, theme.BgApp)
	ui.ActionDialog(c.Region(), m.actionDialog(), ui.Ctx{Pal: m.pal, Clock: m.clock})
	return flat(c.Plain())
}

func flat(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("│", " ", "\n", " ").Replace(s)), " ")
}

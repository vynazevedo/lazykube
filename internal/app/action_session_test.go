package app

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/goleak"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/theme"
)

func (f *actionBackendFake) locked(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

type sessionBackends struct {
	byContext map[string]*actionBackendFake
}

func (s *sessionBackends) connect(t data.Target) Connector {
	return func(context.Context, func(data.Progress)) (data.Source, func(), error) {
		back := s.byContext[t.Context]
		session := guard.Session{Context: t.Context, Identity: "session-" + t.Context, Generation: 1, Environment: guard.Environment{Valid: true}}
		exec, err := guard.NewExecutor(context.Background(), session, guard.NewPolicy(true), back, back, back, guard.ClockFunc(time.Now))
		if err != nil {
			return nil, nil, err
		}
		return actionLive{Demo: data.NewDemoScoped(t.Context, t.Namespace), exec: exec, stamp: &actionRows{}}, exec.Close, nil
	}
}

func sessionDriver(t *testing.T) (*rootDriver, *sessionBackends) {
	t.Helper()
	newBackend := func() *actionBackendFake {
		return &actionBackendFake{obs: guard.Observation{Replicas: 3}, blockSend: nil}
	}
	s := &sessionBackends{byContext: map[string]*actionBackendFake{"alpha": newBackend(), "beta": newBackend()}}
	r := NewRoot(RootOptions{
		Options: Options{Palette: theme.DefaultPalette(), FPS: 30, NoAnim: true, Now: time.Now},
		Initial: data.Target{Context: "alpha"},
		Connect: s.connect,
		Catalog: fakeCatalog,
	})
	d := &rootDriver{t: t, msgs: make(chan tea.Msg, 64), done: make(chan struct{})}
	next, _ := r.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	d.r = next.(Root)
	d.run(d.r.Init())
	t.Cleanup(func() {
		d.r.Shutdown()
		close(d.done)
	})
	d.until("alpha", connectedTo("alpha", ""))
	return d, s
}

func inStage(want actionStage) func(Root) bool {
	return func(r Root) bool {
		m, ok := r.model()
		return ok && m.act.stage == want
	}
}

func (d *rootDriver) focusDeployment() {
	m, _ := d.r.model()
	m.setFocus(FocusDeployments)
	m.depCursor = 0
	d.r.cur = m
}

func (d *rootDriver) restartPreview() {
	d.press("3")
	d.focusDeployment()
	d.press("o", "down", "enter")
	d.until("preview", inStage(actionPreview))
}

func (d *rootDriver) switchTo(ctx string) {
	next, cmd := d.r.begin(data.Target{Context: ctx})
	d.r = next.(Root)
	d.run(cmd)
	d.until(ctx, connectedTo(ctx, ""))
	d.drain()
}

func TestSessionSwitchCancelsEveryStageBeforeSend(t *testing.T) {
	for name, setup := range map[string]func(*rootDriver, *actionBackendFake){
		"choosing": func(d *rootDriver, _ *actionBackendFake) {
			d.press("3")
			d.focusDeployment()
			d.press("o")
		},
		"editing": func(d *rootDriver, _ *actionBackendFake) {
			d.press("3")
			d.focusDeployment()
			d.press("o", "enter")
			d.until("editing", inStage(actionEditing))
		},
		"preparing": func(d *rootDriver, f *actionBackendFake) {
			f.block = make(chan struct{})
			d.press("3")
			d.focusDeployment()
			d.press("o", "down", "enter")
			d.until("preparing", inStage(actionPreparing))
		},
		"preview": func(d *rootDriver, _ *actionBackendFake) { d.restartPreview() },
	} {
		t.Run(name, func(t *testing.T) {
			d, s := sessionDriver(t)
			old, _ := d.r.model()
			exec := old.executor()
			setup(d, s.byContext["alpha"])
			m, _ := d.r.model()
			id := m.act.preview.ID
			d.switchTo("beta")
			if m, _ = d.r.model(); m.act.stage != actionClosed || len(m.act.late) != 0 {
				t.Fatalf("the new session starts without a dialog: %+v", m.act)
			}
			if id != "" {
				if _, err := exec.Execute(context.Background(), id, guard.Confirmation{Approved: true}); err == nil {
					t.Fatal("an old preview must not execute")
				}
			}
			for ctx, f := range s.byContext {
				f.locked(func() {
					if len(f.real) != 0 {
						t.Errorf("%s sent %d requests", ctx, len(f.real))
					}
				})
			}
		})
	}
}

func TestSessionSwitchDuringExecutionKeepsResultOnOldContext(t *testing.T) {
	d, s := sessionDriver(t)
	alpha, beta := s.byContext["alpha"], s.byContext["beta"]
	d.restartPreview()
	alpha.blockSend, alpha.sending = make(chan struct{}), make(chan struct{})
	d.press("tab", "enter")
	d.until("executing", inStage(actionExecuting))
	<-alpha.sending
	d.switchTo("beta")
	d.until("late result", inStage(actionResult))
	m, _ := d.r.model()
	text := dialogText(m)
	for _, want := range []string{"Contexto: alpha", "Operacao da sessao anterior", textUnknown} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, restartAction.success) || len(m.actionButtons()) != 1 {
		t.Fatalf("the old operation offers no read or retry on the new session: %s", text)
	}
	beta.locked(func() {
		if beta.calls != 0 {
			t.Errorf("the new session received %d calls", beta.calls)
		}
	})
	alpha.locked(func() {
		last := alpha.events[len(alpha.events)-1]
		if len(alpha.real) != 1 || last.Phase != "result" || last.Outcome != guard.Unknown {
			t.Errorf("%d sends, last event %+v", len(alpha.real), last)
		}
	})
	d.press("esc")
	if m, _ = d.r.model(); m.act.stage != actionClosed {
		t.Fatal("esc closes the late result")
	}
}

func TestLateResultWaitsForOpenDialog(t *testing.T) {
	d, _ := sessionDriver(t)
	d.press("3")
	d.focusDeployment()
	d.press("o")
	late := actionDone{session: 99, context: "antigo", action: guard.Restart, target: guard.Target{Group: "apps", Version: "v1", Resource: "deployments", Namespace: "x", Name: "y"}, result: guard.Result{OperationID: "op", Outcome: guard.Unknown}, err: guard.Unknown}
	d.send(actionDoneMsg{done: late})
	m, _ := d.r.model()
	if m.act.stage != actionChoosing || len(m.act.late) != 1 {
		t.Fatalf("%+v", m.act)
	}
	d.press("esc")
	m, _ = d.r.model()
	if m.act.stage != actionResult || !strings.Contains(dialogText(m), "Contexto: antigo") {
		t.Fatal(dialogText(m))
	}
}

func TestShutdownDuringExecutionRecordsUnknown(t *testing.T) {
	baseline := goleak.IgnoreCurrent()
	d, s := sessionDriver(t)
	alpha := s.byContext["alpha"]
	d.restartPreview()
	alpha.blockSend, alpha.sending = make(chan struct{}), make(chan struct{})
	d.press("tab", "enter")
	d.until("executing", inStage(actionExecuting))
	<-alpha.sending
	d.r.Shutdown()
	alpha.locked(func() {
		last := alpha.events[len(alpha.events)-1]
		if last.Phase != "result" || last.Outcome != guard.Unknown {
			t.Errorf("shutdown waits for the result record: %+v", last)
		}
	})
	d.drain()
	goleak.VerifyNone(t, baseline)
}

func TestPendingPreviewIsCanceledOnShutdownAndSwitch(t *testing.T) {
	for _, switching := range []bool{false, true} {
		d, s := sessionDriver(t)
		d.restartPreview()
		m, _ := d.r.model()
		id, exec, f := m.act.preview.ID, m.executor(), s.byContext["alpha"]
		if switching {
			d.switchTo("beta")
			exec.Close()
		} else {
			d.r.Shutdown()
		}
		f.locked(func() {
			var outcomes []guard.Reason
			for _, e := range f.events {
				if e.OperationID == id && e.Outcome != "" {
					outcomes = append(outcomes, e.Outcome)
				}
			}
			if len(outcomes) != 1 || outcomes[0] != guard.Canceled || len(f.real) != 0 {
				t.Errorf("switching %v: %v, %d sends", switching, outcomes, len(f.real))
			}
		})
	}
}

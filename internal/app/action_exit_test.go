package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func (d *rootDriver) holdSend(f *actionBackendFake) string {
	d.restartPreview()
	f.blockSend, f.sending = make(chan struct{}), make(chan struct{})
	d.press("tab", "enter")
	d.until("executing", inStage(actionExecuting))
	<-f.sending
	m, _ := d.r.model()
	return m.act.opID
}

func (d *rootDriver) leaveWith(msg tea.Msg) {
	d.t.Helper()
	next, cmd := d.r.Update(msg)
	d.r = next.(Root)
	for _, out := range drainMsgs(d.t, cmd) {
		if _, ok := out.(tea.QuitMsg); ok {
			return
		}
	}
	d.t.Fatal("the key must quit at once")
}

func outcomeOf(f *actionBackendFake, id string) (string, int) {
	last, n := "", 0
	f.locked(func() {
		for _, e := range f.events {
			if e.OperationID == id && e.Outcome != "" {
				last = string(e.Outcome)
				n++
			}
		}
	})
	return last, n
}

func (d *rootDriver) wantLine(f *actionBackendFake, id string) {
	d.t.Helper()
	outcome, n := outcomeOf(f, id)
	got := Unfinished(d.r)
	if len(got) != 1 || got[0] != (PendingOperation{Context: "alpha", OperationID: id, Outcome: outcome}) || n > 1 {
		d.t.Fatalf("line %+v, audit %q (%d)", got, outcome, n)
	}
}

func TestCtrlCWhileSendingReportsTheOperation(t *testing.T) {
	d, s := sessionDriver(t)
	f := s.byContext["alpha"]
	id := d.holdSend(f)
	m, _ := d.r.model()
	if text := dialogText(m); !strings.Contains(text, textExitWhileSending) {
		t.Fatal(text)
	}
	d.leaveWith(keyPress("ctrl+c"))
	d.wantLine(f, id)
	d.r.Shutdown()
	d.drain()
	d.wantLine(f, id)
	if outcome, n := outcomeOf(f, id); outcome != string(guard.Unknown) || n != 1 || len(f.real) != 1 {
		t.Fatal(outcome, n, len(f.real))
	}
}

func TestExternalQuitWhileSendingReportsTheOperation(t *testing.T) {
	d, s := sessionDriver(t)
	id := d.holdSend(s.byContext["alpha"])
	d.wantLine(s.byContext["alpha"], id)
}

func TestSmallTerminalQuitWhileSendingReportsTheOperation(t *testing.T) {
	d, s := sessionDriver(t)
	id := d.holdSend(s.byContext["alpha"])
	d.send(tea.WindowSizeMsg{Width: 60, Height: 15})
	d.leaveWith(keyPress("q"))
	d.wantLine(s.byContext["alpha"], id)
}

func TestExitAfterSessionSwitchReportsUntilResultIsSeen(t *testing.T) {
	d, s := sessionDriver(t)
	id := d.holdSend(s.byContext["alpha"])
	next, cmd := d.r.begin(d.r.pending)
	d.r = next.(Root)
	d.wantLine(s.byContext["alpha"], id)
	d.run(cmd)
	d.until("late result", inStage(actionResult))
	if got := Unfinished(d.r); len(got) != 0 {
		t.Fatalf("a displayed result needs no line: %+v", got)
	}
}

func TestQueuedLateResultSurvivesAnotherSwitch(t *testing.T) {
	d, s := sessionDriver(t)
	d.restartPreview()
	d.press("tab", "enter")
	d.until("result", inStage(actionResult))
	m, _ := d.r.model()
	id, exec := m.act.opID, m.executor()
	d.press("esc")
	if got := Unfinished(d.r); len(got) != 0 {
		t.Fatalf("a displayed result needs no line: %+v", got)
	}
	d.focusDeployment()
	d.press("o")
	d.send(actionDoneMsg{done: actionDone{session: 99, context: "alpha", action: guard.Restart, result: guard.Result{OperationID: id, Outcome: guard.Succeeded}, exec: exec}})
	d.wantLine(s.byContext["alpha"], id)
	d.switchTo("beta")
	if m, _ = d.r.model(); m.act.stage != actionResult || m.act.opID != id {
		t.Fatalf("%+v", m.act)
	}
	if got := Unfinished(d.r); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestExitBeforeExecuteStartsPrintsNothing(t *testing.T) {
	r := writableRig(t)
	r.scalePreview("5")
	id := r.m.act.preview.ID
	r.toButton(controlConfirm)
	execute := r.send(keyPress("enter"))
	r.stage(actionExecuting)
	r.send(keyPress("ctrl+c"))
	r.exec.Close()
	if got := Unfinished(r.m); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	r.run(execute)
	if got := Unfinished(r.m); len(got) != 0 || len(r.back.real) != 0 {
		t.Fatalf("%+v after %d sends", got, len(r.back.real))
	}
	if outcome, n := outcomeOf(r.back, id); outcome != string(guard.Canceled) || n != 1 {
		t.Fatal("the audit tells the same story", outcome, n)
	}
}

func TestExitDuringPreflightPrintsNothing(t *testing.T) {
	r := writableRig(t)
	r.scalePreview("5")
	id := r.m.act.preview.ID
	r.back.block = make(chan struct{})
	r.toButton(controlConfirm)
	execute := r.send(keyPress("enter"))
	done := make(chan tea.Msg, 1)
	go func() { done <- execute() }()
	r.send(keyPress("ctrl+c"))
	r.exec.Close()
	<-done
	if got := Unfinished(r.m); len(got) != 0 || len(r.back.real) != 0 {
		t.Fatalf("%+v after %d sends", got, len(r.back.real))
	}
	if outcome, n := outcomeOf(r.back, id); outcome != string(guard.Canceled) || n != 1 {
		t.Fatal(outcome, n)
	}
}

func TestExitLineFollowsTheRecordedOutcome(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*actionBackendFake)
		want  []PendingOperation
	}{
		{"succeeded", func(*actionBackendFake) {}, []PendingOperation{{Outcome: string(guard.Succeeded)}}},
		{"denied", func(f *actionBackendFake) { f.sendErr = guard.Denied }, []PendingOperation{{Outcome: string(guard.Denied)}}},
		{"unknown", func(f *actionBackendFake) { f.sendErr = errors.New("reset") }, []PendingOperation{{Outcome: string(guard.Unknown)}}},
		{"audit failed", func(f *actionBackendFake) { f.failPhase = "result" }, []PendingOperation{{}}},
		{"not sent", func(f *actionBackendFake) { f.sendErr = guard.NotSent{Reason: guard.Unavailable} }, nil},
		{"intent failed", func(f *actionBackendFake) { f.failPhase = "intent" }, nil},
		{"revoked", func(f *actionBackendFake) { f.denied = true }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := writableRig(t)
			r.scalePreview("5")
			id := r.m.act.preview.ID
			tc.setup(r.back)
			r.toButton(controlConfirm)
			execute := r.send(keyPress("enter"))
			r.send(keyPress("ctrl+c"))
			execute()
			r.exec.Close()
			got := Unfinished(r.m)
			if len(got) != len(tc.want) {
				t.Fatalf("%+v", got)
			}
			for i, want := range tc.want {
				want.Context, want.OperationID = "laboratorio", id
				if got[i] != want {
					t.Fatalf("%+v, want %+v", got[i], want)
				}
				if outcome, _ := outcomeOf(r.back, id); want.Outcome != "" && outcome != want.Outcome {
					t.Fatalf("line %q, audit %q", want.Outcome, outcome)
				}
			}
		})
	}
}

func TestExitWithoutOperationPrintsNothing(t *testing.T) {
	d, _ := sessionDriver(t)
	d.restartPreview()
	d.leaveWith(keyPress("ctrl+c"))
	if got := Unfinished(d.r); len(got) != 0 {
		t.Fatalf("a preview sent nothing: %+v", got)
	}
	if Unfinished(nil) != nil || Unfinished(Loader{}) != nil {
		t.Fatal("other models have no operation")
	}
}

type hostileModel struct{ tea.Model }

func (hostileModel) PendingOperations() []PendingOperation {
	return []PendingOperation{{Context: "c\x1b]52;c;eA==\x07\n\u202e" + strings.Repeat("x", 500), Outcome: "r\x1b[31m\n"}}
}

func TestUnfinishedIsSanitizedAndBounded(t *testing.T) {
	got := Unfinished(hostileModel{})
	if len(got) != 1 || got[0].OperationID != "desconhecido" {
		t.Fatalf("%+v", got)
	}
	for _, bad := range []string{"\x1b", "\x07", "\n", "\u202e"} {
		if strings.Contains(got[0].Context+got[0].Outcome, bad) {
			t.Fatalf("%q in %+v", bad, got[0])
		}
	}
	if n := len([]rune(got[0].Context)); n > exitTextLimit {
		t.Fatal(n)
	}
}

func TestPasteAndResizeNeverConfirm(t *testing.T) {
	for _, a := range []guard.Action{guard.Scale, guard.Restart, guard.Delete, guard.Cordon, guard.Uncordon} {
		r := newActionRig(t, 80, 24, guard.Environment{Valid: true}, true)
		switch a {
		case guard.Scale, guard.Restart:
			r.onDeployment()
		case guard.Cordon, guard.Uncordon:
			r.keys("4")
			r.back.obs.Unschedulable = a == guard.Uncordon
		}
		r.choose(a)
		if r.m.act.stage == actionEditing {
			if a == guard.Scale {
				r.text("0")
			}
			r.toButton(controlPrepare)
			r.keys("enter")
		}
		r.stage(actionPreview)
		r.run(r.send(tea.PasteMsg{Content: r.m.act.plan.Name + "\t\r\n"}))
		if r.m.act.typed != "" {
			t.Fatal("paste populated the confirmation")
		}
		r.run(r.send(tea.WindowSizeMsg{Width: 160, Height: 48}))
		r.run(r.send(tea.WindowSizeMsg{Width: 80, Height: 24}))
		for range 8 {
			r.keys("enter")
		}
		r.sent(1, 0)
	}
}

func TestExitWhileAuditIsSlowPrintsOnlyPossibleSends(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want []PendingOperation
	}{
		{"not sent", guard.NotSent{Reason: guard.Unavailable}, nil},
		{"unknown", errors.New("reset"), []PendingOperation{{}}},
		{"succeeded", nil, []PendingOperation{{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := writableRig(t)
			r.scalePreview("5")
			id := r.m.act.preview.ID
			recording, release := make(chan struct{}), make(chan struct{})
			r.back.sendErr = tc.err
			r.back.onRecord = func(e guard.AuditEvent) {
				if e.Phase == "result" {
					close(recording)
					<-release
				}
			}
			r.toButton(controlConfirm)
			execute := r.send(keyPress("enter"))
			done := make(chan struct{})
			go func() { defer close(done); execute() }()
			<-recording
			r.send(keyPress("ctrl+c"))
			got := Unfinished(r.m)
			close(release)
			<-done
			for i := range tc.want {
				tc.want[i].Context, tc.want[i].OperationID = "laboratorio", id
			}
			if len(got) != len(tc.want) || len(got) == 1 && got[0] != tc.want[0] {
				t.Fatalf("%+v, want %+v", got, tc.want)
			}
		})
	}
}

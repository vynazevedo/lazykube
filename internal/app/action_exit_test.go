package app

import (
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

func TestCtrlCWhileSendingReportsTheOperation(t *testing.T) {
	d, s := sessionDriver(t)
	f := s.byContext["alpha"]
	id := d.holdSend(f)
	m, _ := d.r.model()
	if text := dialogText(m); !strings.Contains(text, textExitWhileSending) {
		t.Fatal(text)
	}
	d.leaveWith(keyPress("ctrl+c"))
	want := []PendingOperation{{Context: "alpha", OperationID: id}}
	if got := Unfinished(d.r); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("%+v", got)
	}
	d.r.Shutdown()
	d.drain()
	if got := Unfinished(d.r); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("a result nobody saw keeps the line: %+v", got)
	}
	f.locked(func() {
		last := f.events[len(f.events)-1]
		if len(f.real) != 1 || last.Phase != "result" || last.Outcome != guard.Unknown || last.OperationID != id {
			t.Errorf("sends=%d last=%+v", len(f.real), last)
		}
	})
}

func TestExternalQuitWhileSendingReportsTheOperation(t *testing.T) {
	d, s := sessionDriver(t)
	id := d.holdSend(s.byContext["alpha"])
	if got := Unfinished(d.r); len(got) != 1 || got[0].OperationID != id {
		t.Fatalf("%+v", got)
	}
}

func TestSmallTerminalQuitWhileSendingReportsTheOperation(t *testing.T) {
	d, s := sessionDriver(t)
	id := d.holdSend(s.byContext["alpha"])
	d.send(tea.WindowSizeMsg{Width: 60, Height: 15})
	d.leaveWith(keyPress("q"))
	if got := Unfinished(d.r); len(got) != 1 || got[0].OperationID != id {
		t.Fatalf("%+v", got)
	}
}

func TestExitAfterSessionSwitchReportsUntilResultIsSeen(t *testing.T) {
	d, s := sessionDriver(t)
	id := d.holdSend(s.byContext["alpha"])
	next, cmd := d.r.begin(d.r.pending)
	d.r = next.(Root)
	if got := Unfinished(d.r); len(got) != 1 || got[0] != (PendingOperation{Context: "alpha", OperationID: id}) {
		t.Fatalf("%+v", got)
	}
	d.run(cmd)
	d.until("late result", inStage(actionResult))
	if got := Unfinished(d.r); len(got) != 0 {
		t.Fatalf("a displayed result needs no line: %+v", got)
	}
}

func TestQueuedLateResultSurvivesAnotherSwitch(t *testing.T) {
	d, _ := sessionDriver(t)
	d.press("3")
	d.focusDeployment()
	d.press("o")
	late := actionDone{session: 99, context: "antigo", action: guard.Restart, result: guard.Result{OperationID: "op", Outcome: guard.Unknown}, err: guard.Unknown}
	d.send(actionDoneMsg{done: late})
	if got := Unfinished(d.r); len(got) != 1 || got[0].OperationID != "op" {
		t.Fatalf("%+v", got)
	}
	d.switchTo("beta")
	m, _ := d.r.model()
	if m.act.stage != actionResult || m.act.opID != "op" {
		t.Fatalf("%+v", m.act)
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

func TestUnfinishedIsSanitizedAndBounded(t *testing.T) {
	r := writableRig(t)
	r.m.act = actionState{stage: actionExecuting, context: "c\x1b]52;c;eA==\x07\n\u202e" + strings.Repeat("x", 500), target: guard.Target{Name: "alvo-privado", Namespace: "ns-privado"}}
	got := Unfinished(r.m)
	if len(got) != 1 || got[0].OperationID != "desconhecido" {
		t.Fatalf("%+v", got)
	}
	for _, bad := range []string{"\x1b", "\x07", "\n", "\u202e", "privado"} {
		if strings.Contains(got[0].Context, bad) {
			t.Fatalf("%q in %q", bad, got[0].Context)
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

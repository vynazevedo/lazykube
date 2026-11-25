package app

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func deleteEditor(t *testing.T) *actionRig {
	t.Helper()
	r := writableRig(t)
	r.choose(guard.Delete)
	r.stage(actionEditing)
	return r
}

func TestDeleteRequiresExactNameAndConfirmation(t *testing.T) {
	r := deleteEditor(t)
	name := r.m.act.target.Name
	if !strings.Contains(r.dialog(), "Esta acao nao pode ser desfeita pela TUI.") || r.m.act.inputs[1].value != "45" {
		t.Fatal(r.dialog())
	}
	r.toButton(controlPrepare)
	r.keys("enter")
	r.stage(actionPreview)
	for _, wrong := range []string{"", name[:len(name)-1], name + "x", strings.ToUpper(name)} {
		r.m.act.typed = wrong
		r.toButton(controlConfirm)
		r.keys("enter")
		r.stage(actionPreview)
		if r.m.act.err != textNameMismatch {
			t.Fatalf("%q: %q", wrong, r.m.act.err)
		}
	}
	r.sent(1, 0)
	r.m.act.typed, r.m.act.focus = "", 0
	r.text(name)
	r.keys("enter", "enter", "enter")
	r.stage(actionPreview)
	r.sent(1, 0)
	r.toButton(controlConfirm)
	r.keys("enter")
	r.stage(actionResult)
	r.sent(1, 1)
	got := r.back.real[0]
	if got.Parameters.Propagation != "Background" || got.Parameters.GraceSeconds != 45 || got.Target.UID != "uid-"+name || got.Target.ResourceVersion != "41" || got.Target.Resource != "pods" {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(r.dialog(), deleteAction.success) {
		t.Fatal(r.dialog())
	}
}

func TestDeleteParametersAreRestricted(t *testing.T) {
	for _, grace := range []string{"", "0", "-1", "1.5", "2147483648", "abc"} {
		r := deleteEditor(t)
		r.m.act.inputs[1].value = grace
		r.toButton(controlPrepare)
		r.keys("enter")
		r.stage(actionEditing)
		if r.m.act.err == "" {
			t.Fatalf("%q must be refused", grace)
		}
		r.sent(0, 0)
	}
	r := deleteEditor(t)
	r.m.act.focus = 0
	seen := map[string]bool{}
	for range 5 {
		seen[r.m.act.inputs[0].value] = true
		r.keys("right")
	}
	if len(seen) != 2 || !seen["Background"] || !seen["Foreground"] {
		t.Fatal(seen)
	}
}

func TestDeleteStartsFocusedOnCancel(t *testing.T) {
	r := deleteEditor(t)
	if i := r.m.act.focus - len(r.m.actionFields()); i < 0 || r.m.actionButtons()[i].control != controlClose {
		t.Fatalf("focus %d", r.m.act.focus)
	}
	r.keys("enter")
	r.stage(actionClosed)
	r.sent(0, 0)
}

func TestBackToParametersDiscardsPreview(t *testing.T) {
	r := deleteEditor(t)
	r.toButton(controlPrepare)
	r.keys("enter")
	id := r.m.act.preview.ID
	r.toButton(controlBack)
	r.keys("enter")
	r.stage(actionEditing)
	last := r.back.events[len(r.back.events)-1]
	if last.Phase != "canceled" || last.OperationID != id {
		t.Fatalf("%+v", last)
	}
	r.m.act.inputs[1].value = "10"
	r.toButton(controlPrepare)
	r.keys("enter")
	r.stage(actionPreview)
	if r.m.act.preview.ID == id || r.m.act.typed != "" || r.back.dry[1].Parameters.GraceSeconds != 10 {
		t.Fatal("new parameters need a new preview")
	}
}

func TestDeleteOwnerAndTerminatingAreShown(t *testing.T) {
	r := writableRig(t)
	r.back.obs.Owner, r.back.obs.Terminating, r.back.obs.Finalizers = "ReplicaSet/api", true, 2
	r.choose(guard.Delete)
	plain := r.dialog()
	if !strings.Contains(plain, "Controlado por ReplicaSet/api. O controlador pode criar outro objeto.") || !strings.Contains(plain, "finalizers observados: 2") {
		t.Fatal(plain)
	}
}

func TestDeleteOfRecreatedPodIsStale(t *testing.T) {
	r := deleteEditor(t)
	r.toButton(controlPrepare)
	r.keys("enter")
	r.back.validate = guard.Stale
	r.text(r.m.act.plan.Name)
	r.toButton(controlConfirm)
	r.keys("enter")
	r.stage(actionResult)
	if !strings.Contains(r.dialog(), reasonText(guard.Stale)) {
		t.Fatal(r.dialog())
	}
	r.sent(1, 0)
}

func TestLongNameKeepsControlsVisible(t *testing.T) {
	r := deleteEditor(t)
	r.toButton(controlPrepare)
	r.keys("enter")
	r.m.act.plan.Name = strings.Repeat("a", 250) + "end"
	r.m.act.focus = 0
	r.text(r.m.act.plan.Name)
	plain := r.plain()
	if !strings.Contains(plain, "end_]") || !strings.Contains(plain, "[Confirmar exclusao]") {
		t.Fatal(plain)
	}
}

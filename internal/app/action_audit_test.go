package app

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
)

var actionRefusals = []guard.AuditRefusal{guard.RefusedPlatform, guard.RefusedFilesystem, guard.RefusedLink, guard.RefusedOwner, guard.RefusedPrivate, guard.RefusedForm}

func TestRefusedAuditExplainsBeforeAnyCall(t *testing.T) {
	for _, refusal := range actionRefusals {
		for _, size := range [][2]int{{80, 24}, {130, 40}, {160, 48}} {
			r := newActionRig(t, size[0], size[1], guard.Environment{Valid: true}, true)
			r.back.checkErr = refusal
			r.onDeployment()
			r.keys("o")
			r.stage(actionChoosing)
			text := r.dialog()
			if !strings.Contains(text, string(refusal)) || !strings.Contains(text, "--audit-file") || !strings.Contains(text, "indisponivel") || strings.Contains(text, "audit_failed") {
				t.Fatalf("%v %q: %s", size, refusal, text)
			}
			if platform := refusal == guard.RefusedPlatform; platform != strings.Contains(text, "--audit-file nao muda esta recusa") {
				t.Fatal(text)
			}
			want := "[ACOES BLOQUEADAS: AUDIT]"
			if size[0] < 100 {
				want = "[AUDIT]"
			}
			if !strings.Contains(r.plain(), want) {
				t.Fatal(r.plain())
			}
			for range len(r.m.act.items) {
				r.keys("enter", "down")
			}
			r.stage(actionChoosing)
			if r.back.calls != 0 || len(r.back.events) != 0 {
				t.Fatalf("%d calls, %d events", r.back.calls, len(r.back.events))
			}
		}
	}
}

func TestAuditRefusedWhileMenuIsOpenMakesNoCall(t *testing.T) {
	r := writableRig(t)
	r.onDeployment()
	r.keys("o")
	r.back.checkErr = guard.RefusedOwner
	r.keys("enter")
	r.stage(actionResult)
	if text := r.dialog(); !strings.Contains(text, string(guard.RefusedOwner)) || !strings.Contains(text, "--audit-file") || r.back.calls != 0 {
		t.Fatal(text, r.back.calls)
	}
}

func TestAuditRefusedAfterPreviewMakesNoCall(t *testing.T) {
	r := writableRig(t)
	r.scalePreview("5")
	calls := r.back.calls
	r.back.checkErr = guard.RefusedPrivate
	r.toButton(controlConfirm)
	r.keys("enter")
	r.stage(actionResult)
	if text := r.dialog(); !strings.Contains(text, string(guard.RefusedPrivate)) || r.back.calls != calls || len(r.back.real) != 0 {
		t.Fatal(text, r.back.calls-calls)
	}
}

func TestReadOnlyMenuDoesNotCheckTheAudit(t *testing.T) {
	r := newActionRig(t, 130, 40, guard.Environment{Valid: true}, false)
	r.onDeployment()
	r.keys("o")
	if r.back.checks != 0 {
		t.Fatal(r.back.checks)
	}
}

package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func TestScaleFlowFreezesTargetAndParameters(t *testing.T) {
	r := writableRig(t)
	r.back.obsVersion = "99"
	r.scalePreview("5")
	r.sent(1, 0)
	if plain := r.dialog(); !strings.Contains(plain, "Dry-run server-side aprovado.") || !strings.Contains(plain, "Alterar replicas de 3 para 5.") || !strings.Contains(plain, "get deployments/scale e update deployments/scale") || !strings.Contains(plain, "Preview valido por 120s.") {
		t.Fatal(plain)
	}
	r.toButton(controlConfirm)
	r.keys("enter")
	r.stage(actionResult)
	r.sent(1, 1)
	if !reflect.DeepEqual(r.back.dry[0], r.back.real[0]) {
		t.Fatal("the request sent must be the one that passed the dry-run")
	}
	want := guard.Target{Group: "apps", Version: "v1", Resource: "deployments", Namespace: "bilibili-raffle", Name: "bilibili-live", UID: "uid-bilibili-live", ResourceVersion: "99"}
	if got := r.back.real[0]; got.Target != want || got.Parameters.Replicas != 5 {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(r.dialog(), scaleAction.success) {
		t.Fatal(r.plain())
	}
}

func TestScaleValidatesLocallyBeforePrepare(t *testing.T) {
	for _, tc := range []struct{ typed, want string }{
		{"", "Informe um inteiro de 0 a 2147483647."}, {"-1", "Informe um inteiro de 0 a 2147483647."}, {"1e3", "Informe um inteiro de 0 a 2147483647."},
		{"2147483648", "Informe um inteiro de 0 a 2147483647."}, {"99999999999999999999", "Informe um inteiro de 0 a 2147483647."}, {"3", "Nenhuma alteracao de replicas."},
	} {
		r := writableRig(t)
		r.onDeployment()
		r.choose(guard.Scale)
		r.text(tc.typed)
		r.toButton(controlPrepare)
		r.keys("enter")
		r.stage(actionEditing)
		if r.m.act.err != tc.want {
			t.Fatalf("%q: %q", tc.typed, r.m.act.err)
		}
		r.sent(0, 0)
	}
}

func TestScaleToZeroRequiresExactName(t *testing.T) {
	r := writableRig(t)
	r.scalePreview("0")
	for _, wrong := range []string{"", "bilibili", "Bilibili-live", "bilibili-live "} {
		r.keys("ctrl+u")
		r.text(wrong)
		r.keys("shift+tab", "enter")
		r.stage(actionPreview)
		if r.m.act.err != textNameMismatch {
			t.Fatalf("%q: %q", wrong, r.m.act.err)
		}
		r.keys("tab")
		for range len(r.m.act.typed) {
			r.keys("backspace")
		}
	}
	r.sent(1, 0)
	r.text("bilibili-live")
	r.keys("enter", "enter")
	r.stage(actionPreview)
	r.sent(1, 0)
	r.toButton(controlConfirm)
	r.keys("enter")
	r.sent(1, 1)
}

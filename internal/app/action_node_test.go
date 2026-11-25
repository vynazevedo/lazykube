package app

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func TestCordonAndUncordonNode(t *testing.T) {
	for _, tc := range []struct {
		action   guard.Action
		observed bool
		text     string
	}{
		{guard.Cordon, false, "Impedir novos agendamentos neste node. Pods existentes nao serao removidos."},
		{guard.Uncordon, true, "Permitir novos agendamentos neste node; taints e demais restricoes continuam valendo."},
	} {
		r := writableRig(t)
		r.back.obs.Unschedulable = tc.observed
		r.keys("4")
		r.choose(tc.action)
		r.stage(actionPreview)
		if plain := r.dialog(); !strings.Contains(plain, tc.text) || !strings.Contains(plain, "get nodes e patch nodes") || !strings.Contains(plain, "Escopo: cluster") {
			t.Fatal(plain)
		}
		r.toButton(controlConfirm)
		r.keys("enter")
		r.stage(actionPreview)
		r.sent(1, 0)
		r.m.act.focus = 0
		r.text("demo-node-01")
		r.toButton(controlConfirm)
		r.keys("enter")
		r.sent(1, 1)
		got := r.back.real[0]
		if got.Action != tc.action || got.Target != (guard.Target{Version: "v1", Resource: "nodes", Name: "demo-node-01", UID: "uid-node", ResourceVersion: "43"}) {
			t.Fatalf("%+v", got)
		}
		if !strings.Contains(r.dialog(), "Configuracao de agendamento aceita pelo servidor.") {
			t.Fatal(r.dialog())
		}
	}
}

func TestCordonNoOpDoesNotPrepare(t *testing.T) {
	for action, observed := range map[guard.Action]bool{guard.Cordon: true, guard.Uncordon: false} {
		r := writableRig(t)
		r.back.obs.Unschedulable = observed
		r.keys("4")
		r.choose(action)
		r.stage(actionResult)
		if !strings.Contains(r.dialog(), textNoChange) || len(r.back.events) != 0 {
			t.Fatal(r.dialog())
		}
		r.sent(0, 0)
	}
}

func TestNodeRecreatedOrChangedIsStale(t *testing.T) {
	for _, change := range []func(*actionBackendFake){func(f *actionBackendFake) { f.obsUID = "new" }, func(f *actionBackendFake) { f.obsVersion = "44" }} {
		r := writableRig(t)
		change(r.back)
		r.keys("4")
		r.choose(guard.Cordon)
		r.stage(actionResult)
		if !strings.Contains(r.dialog(), reasonText(guard.Stale)) {
			t.Fatal(r.dialog())
		}
		r.sent(0, 0)
	}
}

func TestNodeSummaryShowsCordon(t *testing.T) {
	r := writableRig(t)
	r.rows.cordon = true
	r.run(r.send(dataMsg{session: 12}))
	r.keys("4")
	if !strings.Contains(r.plain(), "Agendamento: impedido (cordon)") {
		t.Fatal(r.plain())
	}
}

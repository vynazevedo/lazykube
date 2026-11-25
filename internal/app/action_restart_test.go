package app

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func TestRestartPreviewAndConfirm(t *testing.T) {
	r := writableRig(t)
	r.back.obs.Strategy = "OnDelete"
	r.onDeployment()
	r.choose(guard.Restart)
	r.stage(actionPreview)
	plain := r.dialog()
	if !strings.Contains(plain, "Estrategia OnDelete: atualizar o template nao substitui pods automaticamente.") || !strings.Contains(plain, "get deployments e patch deployments") || len(r.m.actionFields()) != 0 {
		t.Fatal(plain)
	}
	if r.m.actionButtons()[r.m.act.focus].control != controlClose {
		t.Fatal("the initial focus is Cancelar")
	}
	r.toButton(controlConfirm)
	r.keys("enter")
	r.sent(1, 1)
	dry, live := r.back.dry[0], r.back.real[0]
	if string(dry.Parameters.Document) == "" || string(dry.Parameters.Document) != string(live.Parameters.Document) || live.Target.ResourceVersion != "42" {
		t.Fatalf("%+v %+v", dry, live)
	}
	if !strings.Contains(r.dialog(), restartAction.success) {
		t.Fatal(r.dialog())
	}
}

func TestPodsCannotScaleOrRestart(t *testing.T) {
	r := writableRig(t)
	r.keys("o")
	for _, s := range r.m.act.items {
		if s.action == guard.Scale || s.action == guard.Restart {
			t.Fatal(s.label)
		}
	}
}

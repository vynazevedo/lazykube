package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func TestHeaderKeepsTargetOnEveryScreenAndDialog(t *testing.T) {
	previous := ui.Version
	t.Cleanup(func() { ui.Version = previous })
	ui.Version = "dev-6f9b0fd-dirty"
	const long = "arn:aws:eks:sa-east-1:123456789012:cluster/prod-eu-1"
	open := map[string]func(*actionRig){
		"dashboard": func(r *actionRig) { r.keys("2") },
		"radar":     func(r *actionRig) { r.keys("7") },
		"security":  func(r *actionRig) { r.keys("5") },
		"nodes":     func(r *actionRig) { r.keys("4") },
		"menu":      func(r *actionRig) { r.onDeployment(); r.keys("o") },
		"preview":   func(r *actionRig) { r.scalePreview("5") },
		"audit": func(r *actionRig) {
			r.back.checkErr = guard.RefusedOwner
			r.onDeployment()
			r.keys("o")
		},
		"unknown": func(r *actionRig) {
			r.scalePreview("5")
			r.back.sendErr, r.back.failPhase = errors.New("reset"), "result"
			r.toButton(controlConfirm)
			r.keys("enter")
		},
	}
	for name, setup := range open {
		r := newActionRig(t, 160, 48, guard.Environment{Valid: true}, true)
		setup(r)
		blocked := r.exec.AuditBlocked()
		if want := name == "audit" || name == "unknown"; blocked != want {
			t.Fatalf("%s: audit blocked %v", name, blocked)
		}
		for w := 80; w <= 200; w += 5 {
			h := 24 + w%3*8
			r.run(r.send(tea.WindowSizeMsg{Width: w, Height: h}))
			r.m.snap.Cluster.Context, r.m.snap.Cluster.Namespace = long, "observability-producao-principal"
			row := strings.Split(r.plain(), "\n")[0]
			if !strings.Contains(row, "-1 ") || !strings.Contains(row, "◎") || !strings.Contains(row, "ns ") && !strings.Contains(row, "namespace ") || !strings.Contains(row, "[RW") || blocked && !strings.Contains(row, "AUDIT]") {
				t.Fatalf("%s %dx%d: %q", name, w, h, row)
			}
			if r.m.act.stage != actionClosed {
				if g := ui.ActionLayout(w, h, r.m.actionDialog()); g.Y < 1 {
					t.Fatalf("%s %dx%d: the dialog covers the header", name, w, h)
				}
			}
		}
	}
}

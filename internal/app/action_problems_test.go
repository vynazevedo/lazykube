package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func problemRig(t *testing.T, w, h int) *actionRig {
	t.Helper()
	r := newActionRig(t, w, h, guard.Environment{Valid: true}, true)
	r.rows.keep = true
	r.m.reload()
	return r
}

func radarRig(t *testing.T, w, h int) *actionRig {
	t.Helper()
	r := problemRig(t, w, h)
	r.m.syncRadar()
	for i, step := range []time.Duration{40 * time.Second, 6 * time.Minute} {
		r.clock.advance(step)
		r.demo.AdvanceObservations((i + 1) * 600)
		r.m.syncRadar()
	}
	r.keys("7", "home")
	if s := r.m.selectedSignal(); s == nil || s.Entity.Kind != "Pod" {
		t.Fatalf("the fixture needs a pod signal: %+v", s)
	}
	return r
}

func helpHas(help []ui.KeyHelp, key string) bool {
	for _, h := range help {
		if h.Key == key {
			return true
		}
	}
	return false
}

func (r *actionRig) wantNoAction(what string) {
	r.t.Helper()
	s := r.m.screen()
	if helpHas(s.Help(r.m), "o") || helpHas(s.FullHelp(r.m), "o") {
		r.t.Fatalf("%s: the help must not promise o", what)
	}
	before, notice := r.m.nav, r.m.notice.text
	r.keys("o")
	if r.m.act.stage != actionClosed || r.m.nav != before || r.m.notice.text != notice || r.back.calls != 0 || r.back.checks != 0 {
		r.t.Fatalf("%s: o must do nothing, stage %d target %+v", what, r.m.act.stage, r.m.act.target)
	}
}

func TestRadarActionsUseTheSignalPod(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {130, 40}, {160, 48}} {
		r := radarRig(t, size[0], size[1])
		sig := r.m.selectedSignal()
		s := r.m.screen()
		if !helpHas(s.Help(r.m), "o") || !helpHas(s.FullHelp(r.m), "o") {
			t.Fatal("an exact target is announced")
		}
		r.keys("o")
		want := guard.Target{Version: "v1", Resource: "pods", Namespace: sig.Entity.Namespace, Name: sig.Entity.Name, UID: sig.Entity.UID, ResourceVersion: "41"}
		if r.m.act.target != want {
			t.Fatalf("%+v, want %+v", r.m.act.target, want)
		}
		if want.Namespace+"/"+want.Name == r.m.selName {
			t.Fatal("the fixture must differ from the global pod")
		}
		r.keys("enter")
		r.stage(actionEditing)
		r.toButton(controlPrepare)
		r.keys("enter")
		r.text(want.Name)
		r.toButton(controlConfirm)
		r.keys("enter")
		r.sent(1, 1)
		if r.back.real[0].Target != want {
			t.Fatalf("%+v", r.back.real[0].Target)
		}
	}
}

func TestRadarWithoutExactTargetOffersNothing(t *testing.T) {
	for name, change := range map[string]func(*actionRig){
		"recreated": func(r *actionRig) { r.rows.uid = "-new" },
		"no uid":    func(r *actionRig) { r.rows.noUID = true },
		"no rv":     func(r *actionRig) { r.rows.noRV = true },
		"posture": func(r *actionRig) {
			s := r.m.selectedSignal()
			key := data.PodKey(s.Entity.Namespace, s.Entity.Name)
			r.m.security = r.m.security.Clone()
			p := r.m.security.Pods[key]
			p.Target.UID = "other"
			r.m.security.Pods[key] = p
		},
		"gone":      func(r *actionRig) { r.m.radar.gone = true },
		"no signal": func(r *actionRig) { r.m.radar.id = "" },
	} {
		r := radarRig(t, 130, 40)
		change(r)
		r.m.snap = r.m.src.Snapshot()
		r.wantNoAction(name)
	}
}

func TestRadarChangeCancelsTheAction(t *testing.T) {
	r := radarRig(t, 130, 40)
	r.keys("o", "enter")
	r.toButton(controlPrepare)
	r.keys("enter")
	r.stage(actionPreview)
	r.rows.uid = "-new"
	r.run(r.send(dataMsg{session: 12}))
	r.stage(actionResult)
	if !strings.Contains(r.dialog(), textSelection) || len(r.back.real) != 0 {
		t.Fatal(r.dialog())
	}
}

func TestPodOpenedFromRadarKeepsItsIdentity(t *testing.T) {
	r := radarRig(t, 130, 40)
	sig := r.m.selectedSignal()
	r.keys("enter")
	if route := r.m.nav.Active(); route.Kind != RoutePod || route.Ref.UID != sig.Entity.UID {
		t.Fatalf("%+v", route)
	}
	r.keys("o")
	if r.m.act.target.UID != sig.Entity.UID {
		t.Fatalf("%+v", r.m.act.target)
	}
	r.keys("esc")
	r.rows.uid = "-new"
	r.run(r.send(dataMsg{session: 12}))
	r.keys("o")
	r.stage(actionClosed)
	if r.m.notice.text != textNoAction {
		t.Fatal("a recreated pod must not inherit the route", r.m.notice.text)
	}
}

func securityRig(t *testing.T, w, h int, standalone bool) (*actionRig, string) {
	t.Helper()
	return securityRigOwned(t, w, h, standalone, nil)
}

func securityRigOwned(t *testing.T, w, h int, standalone bool, owner *data.OwnerRef) (*actionRig, string) {
	t.Helper()
	r := problemRig(t, w, h)
	key := r.m.snap.Pods[2].Key()
	if standalone {
		r.m.security = r.m.security.Clone()
		p := r.m.security.Pods[key]
		p.Owner, p.Target.UID = owner, r.m.snap.Pods[2].UID
		r.m.security.Pods[key] = p
		r.m.security.Revision++
		r.m.securitySession.Update(r.m.security)
	}
	r.keys("5")
	r.m = settleSecurity(deliver(t, r.m, tea.FocusMsg{}))
	for i, w := range r.m.sec.result.workloads {
		if w.Key.Pod == key || !standalone && w.Key.Pod == "" {
			r.m.adoptSecurityWorkload(i)
			break
		}
	}
	return r, key
}

func TestSecurityActionsUseTheStandalonePod(t *testing.T) {
	r, key := securityRig(t, 130, 40, true)
	if r.m.sec.Selected.Pod != key || key == r.m.selName {
		t.Fatalf("fixture: %+v", r.m.sec.Selected)
	}
	if s := r.m.screen(); !helpHas(s.Help(r.m), "o") || !helpHas(s.FullHelp(r.m), "o") {
		t.Fatal("an exact target is announced")
	}
	r.keys("o")
	got := r.m.act.target
	if got.Namespace+"/"+got.Name != key || got.UID != r.m.sec.Selected.UID || got.ResourceVersion != "41" || got.Resource != "pods" {
		t.Fatalf("%+v", got)
	}
}

func TestSecurityActionsUseThePodWithUnresolvedOwner(t *testing.T) {
	r, key := securityRigOwned(t, 130, 40, true, &data.OwnerRef{Kind: "Thing"})
	r.keys("o")
	if got := r.m.act.target; got.Namespace+"/"+got.Name != key || got.UID == "" || got.Resource != "pods" {
		t.Fatalf("%+v", got)
	}
}

func TestSecurityWithoutExactTargetOffersNothing(t *testing.T) {
	r, _ := securityRig(t, 130, 40, false)
	if r.m.sec.Selected.Kind == "" {
		t.Fatalf("fixture: %+v", r.m.sec.Selected)
	}
	r.wantNoAction("controller row has no resourceVersion")
	for name, change := range map[string]func(*actionRig){
		"audit tab": func(r *actionRig) { r.keys("[") },
		"recreated": func(r *actionRig) { r.rows.uid = "-new"; r.m.snap = r.m.src.Snapshot() },
		"no uid":    func(r *actionRig) { r.rows.noUID = true; r.m.snap = r.m.src.Snapshot() },
		"no rv":     func(r *actionRig) { r.rows.noRV = true; r.m.snap = r.m.src.Snapshot() },
		"pending":   func(r *actionRig) { r.m.sec.have = false },
		"selection": func(r *actionRig) { r.m.sec.Selected = data.WorkloadKey{} },
	} {
		r, _ := securityRig(t, 130, 40, true)
		change(r)
		r.wantNoAction(name)
	}
}

func TestDemoRadarAndSecurityOfferNoAction(t *testing.T) {
	for _, m := range []Model{newRadarHarness(t).active(130, 40), onSecurity(t, demoModel(t, 130, 40))} {
		before := m.notice.text
		next, cmd := m.Update(keyPress("o"))
		got := next.(Model)
		if got.act.stage != actionClosed || got.notice.text != before || helpHas(got.screen().FullHelp(got), "o") {
			t.Fatal("the demo rows carry no resourceVersion")
		}
		_ = cmd
	}
}

func TestDialogIsDrawnOnTheModelCanvas(t *testing.T) {
	r := writableRig(t)
	r.scalePreview("5")
	c := r.m.Render()
	g := ui.ActionLayout(r.m.w, r.m.h, r.m.actionDialog())
	alone := strings.Split(dialogCanvas(r.m).Plain(), "\n")
	for y, line := range strings.Split(c.Plain(), "\n") {
		if y < g.Y || y >= g.Y+g.H {
			continue
		}
		if got, want := string([]rune(line)[g.X:g.X+g.W]), string([]rune(alone[y])[g.X:g.X+g.W]); got != want {
			t.Fatalf("row %d %q, want %q", y, got, want)
		}
	}
	if r.m.View().Content != c.String() {
		t.Fatal("nothing is drawn after the canvas")
	}
}

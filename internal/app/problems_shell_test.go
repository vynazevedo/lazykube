package app

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/insight"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func activeRadarAt(t *testing.T, w, h int) Model {
	t.Helper()
	return newRadarHarness(t).active(w, h)
}

func TestProblemScreensUseTheCompactShell(t *testing.T) {
	r := activeRadarAt(t, 80, 24)
	if !r.layout.Compact || r.layout.TooSmall || r.layout.Events.Empty() || !r.layout.Message.Empty() {
		t.Fatalf("80x24 Radar shows the list: %+v", r.layout)
	}
	id := r.radar.id
	detail := press(t, r, "tab")
	if detail.layout.Message.Empty() || !detail.layout.Events.Empty() || detail.radar.id != id {
		t.Fatal("tab shows the evidence of the same signal")
	}
	if down := press(t, detail, "down"); down.radar.id != id || down.radar.scroll != 1 {
		t.Fatal("arrows scroll the visible evidence")
	}
	if back := press(t, detail, "shift+tab"); back.layout.Events.Empty() {
		t.Fatal("shift+tab returns to the list")
	}
	for _, want := range []string{"Cobertura: inventário de pods completa 13/13", "Ativos 4", "Saúde: indisponível", "7 Radar]"} {
		if !strings.Contains(r.Render().Plain(), want) {
			t.Fatalf("compact Radar keeps %q", want)
		}
	}

	s := onSecurity(t, demoModel(t, 80, 24))
	if !s.layout.Compact || s.layout.SecList.Empty() || !s.layout.SecEvidence.Empty() {
		t.Fatalf("80x24 Security shows the list: %+v", s.layout)
	}
	for _, want := range []string{"Todos: Segurança", "Sem sistema: Segurança", "Cobertura 13/13", "Nota não garante segurança", "7 Radar", "[Workloads]", "[Audit]", "PSS v1.34"} {
		if !strings.Contains(s.Render().Plain(), want) {
			t.Fatalf("compact Security keeps %q", want)
		}
	}
	key := s.sec.Selected
	ev := press(t, s, "tab")
	if ev.layout.SecEvidence.Empty() || !ev.layout.SecList.Empty() || ev.sec.Selected != key {
		t.Fatal("tab shows the evidence of the same workload")
	}
	if down := press(t, ev, "down"); down.sec.Scroll != 1 || down.sec.Selected != key {
		t.Fatal("arrows scroll the visible evidence")
	}
	if rbac := press(t, ev, "]"); rbac.layout.SecList.Empty() || !strings.Contains(rbac.Render().Plain(), "nenhuma leitura foi feita") {
		t.Fatal("changing tab shows its panel again")
	}
	if enter := press(t, s, "enter"); enter.layout.SecEvidence.Empty() {
		t.Fatal("enter opens the evidence")
	}
	big := deliver(t, ev, tea.WindowSizeMsg{Width: 160, Height: 48})
	if big.layout.Compact || big.layout.SecList.Empty() || big.layout.SecEvidence.Empty() || big.sec.Selected != key {
		t.Fatal("resizing up restores both panels and the selection")
	}
}

func TestProblemScreensFitTheNavigationTree(t *testing.T) {
	for _, size := range [][2]int{{160, 48}, {130, 40}, {80, 24}} {
		for k, dest := range map[string]DestinationID{"5": DestSecurity, "7": DestRadar} {
			m := press(t, demoModel(t, size[0], size[1]), k, "b")
			l := m.layout
			if !m.tree.visible || l.Tree.Empty() || m.nav.Active().Dest != dest {
				t.Fatalf("%v %s opens the tree", size, dest)
			}
			for _, r := range []Rect{l.Scope, l.Events, l.Message, l.Coverage, l.SecSummary, l.SecTabs, l.SecList, l.SecEvidence, l.SecNotes} {
				if r.Empty() {
					continue
				}
				if !l.TreeOverlay && (r.X < l.Tree.X+l.Tree.W || r.X+r.W > size[0]) {
					t.Fatalf("%v %s panel %+v overlaps the docked tree %+v", size, dest, r, l.Tree)
				}
			}
			m.Render()
		}
	}
	m := press(t, demoModel(t, 160, 48), "b")
	for _, d := range []DestinationID{DestSecurity, DestRadar} {
		if got := press(t, treeTo(t, m, d), "enter"); got.nav.Active().Dest != d {
			t.Fatalf("the tree opens %s", d)
		}
	}
}

func TestRadarFollowsMarkedNamespaces(t *testing.T) {
	m := activeRadarAt(t, 160, 48)
	all := len(m.radar.rows)
	ns := m.radar.held.Entity.Namespace
	set := markNamespaces(t, m, ns)
	if len(set.radar.rows) == 0 || len(set.radar.rows) >= all {
		t.Fatalf("a mark filters the listed signals: %d of %d", len(set.radar.rows), all)
	}
	for _, s := range set.radar.rows {
		if s.Entity.Namespace != ns {
			t.Fatalf("signal outside the scope: %+v", s.Entity)
		}
	}
	if set.radar.counts == m.radar.counts || len(set.radar.snap.Signals) != len(m.radar.snap.Signals) {
		t.Fatal("counts follow the scope and the engine snapshot stays whole")
	}
	if plain := set.Render().Plain(); !strings.Contains(plain, "Radar | Namespace "+ns) || !strings.Contains(plain, "fora dele") {
		t.Fatal("the scope and what it hides are visible")
	}
	two := markNamespaces(t, m, ns, "ys-odoo")
	if len(two.radar.rows) < len(set.radar.rows) || !strings.Contains(two.Render().Plain(), "Namespaces (2)") {
		t.Fatal("a set keeps the signals of every marked namespace")
	}
	none := markNamespaces(t, m, "missing")
	if len(none.radar.rows) != 0 || none.radar.gone || none.selectedSignal() != nil {
		t.Fatal("hiding the selected signal is not a vanished target")
	}
	if !strings.Contains(none.Render().Plain(), fmt.Sprintf("Nenhum sinal no escopo; %d fora dele.", all)) {
		t.Fatal("an empty scope is not a healthy cluster")
	}
	if back := press(t, none, "esc"); !back.onScreen(ScreenRadar) || back.nsFilter != "" || len(back.radar.rows) != all {
		t.Fatal("esc clears the marks before leaving Radar")
	}
	s := settleSecurity(markNamespaces(t, onSecurity(t, demoModel(t, 160, 48)), "kube-system"))
	if back := press(t, s, "esc"); !back.onScreen(ScreenSecurity) || back.nsFilter != "" || back.sec.result.key.Scope != "" {
		t.Fatal("esc clears the marks before leaving Security")
	}
}

func TestYankOnProblemScreens(t *testing.T) {
	r := activeRadarAt(t, 160, 48)
	r.after = instantTimer
	_, msgs := pressYank(t, r)
	if got, ok := clipboardOf(msgs); !ok || got != r.radar.held.Entity.Namespace+"/"+r.radar.held.Entity.Name {
		t.Fatalf("y copies the selected signal target, got %q", got)
	}
	s := onSecurity(t, demoModel(t, 160, 48))
	s.after = instantTimer
	w := s.sec.result.workloads[s.sec.Cursor]
	if _, msgs = pressYank(t, s); len(msgs) == 0 {
		t.Fatal("y answers on Security")
	}
	if got, ok := clipboardOf(msgs); !ok || got != w.Key.Namespace+"/"+w.Name {
		t.Fatalf("y copies the selected workload, got %q", got)
	}
	for _, keys := range [][]string{{"]"}, {"["}} {
		tab := press(t, s, keys...)
		_, msgs = pressYank(t, tab)
		if got, ok := clipboardOf(msgs); ok {
			t.Fatalf("tab %s has nothing to copy, got %q", tab.sec.Tab, got)
		}
	}
	hostile := r
	hostile.radar.held.Entity.Name = "api\x1b]52;c;x\x07\nrm"
	if _, msgs = pressYank(t, hostile); len(msgs) == 0 {
		t.Fatal("y answers")
	}
	if got, ok := clipboardOf(msgs); ok {
		t.Fatalf("control characters are refused, not copied: %q", got)
	}
}

func TestRadarStopsWithTheSession(t *testing.T) {
	m := activeRadarAt(t, 160, 48)
	life, cancel := newSession()
	defer life.cancel()
	m.life = cancel
	cmd := m.radarCmd()
	if cmd == nil {
		t.Fatal("radar step expected")
	}
	life.cancel()
	if msg := cmd(); msg != nil || m.radar.feed.busy.Load() {
		t.Fatalf("a canceled session reads nothing and frees the feed: %v", msg)
	}
	before := len(m.radar.rows)
	next, follow := m.Update(radarMsg{session: m.session.Generation, snap: insight.SignalSnapshot{}})
	if follow != nil || len(next.(Model).radar.rows) != before {
		t.Fatal("a result after the session ends is discarded")
	}
}

func TestProblemScreenGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {130, 40}, {160, 48}} {
		suffix := fmt.Sprintf("_%dx%d.txt", size[0], size[1])
		r, s := activeRadarAt(t, size[0], size[1]), onSecurity(t, demoModel(t, size[0], size[1]))
		ns := r.radar.held.Entity.Namespace
		golden(t, "radar_marks"+suffix, markNamespaces(t, r, ns, "ys-odoo").Render().Plain())
		golden(t, "security_marks"+suffix, settleSecurity(markNamespaces(t, s, "bilibili-raffle", "kube-system")).Render().Plain())
		golden(t, "radar_tree"+suffix, press(t, r, "b").Render().Plain())
		golden(t, "security_tree"+suffix, press(t, s, "b").Render().Plain())
	}
	for name, m := range map[string]Model{
		"radar_80x24":             activeRadarAt(t, 80, 24),
		"radar_evidence_80x24":    press(t, activeRadarAt(t, 80, 24), "tab"),
		"security_80x24":          onSecurity(t, demoModel(t, 80, 24)),
		"security_evidence_80x24": press(t, onSecurity(t, demoModel(t, 80, 24)), "tab"),
		"security_rbac_80x24":     press(t, onSecurity(t, demoModel(t, 80, 24)), "]"),
	} {
		c := m.Render()
		golden(t, name+".txt", c.Plain())
		golden(t, name+".ansi", c.String())
	}
	pending := pendingSecurity(t)
	if pending.sec.Tab != ui.SecTabWorkloads {
		t.Fatal("workloads is the first tab")
	}
	golden(t, "security_calculating_160x48.txt", pending.Render().Plain())
}

package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func visiblePanels(l Layout) []string {
	var out []string
	for name, r := range map[string]Rect{"node": l.Node, "namespaces": l.Namespaces, "deployments": l.Deployments, "pods": l.Pods, "heatmap": l.Heatmap, "details": l.Details, "events": l.Events, "message": l.Message} {
		if !r.Empty() {
			out = append(out, name)
		}
	}
	return out
}

func onePanel(t *testing.T, step string, m Model, want string) {
	t.Helper()
	got := visiblePanels(m.layout)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("%s: compact shows one panel %q, got %v", step, want, got)
	}
}

func TestCompactScreensShowOnePanelAtATime(t *testing.T) {
	m := demoModel(t, 80, 24)
	if m.layout.TooSmall || !m.layout.Compact {
		t.Fatal("80x24 is a compact shell, not too small")
	}
	onePanel(t, "dashboard", m, "pods")
	onePanel(t, "dashboard tab", press(t, m, "tab"), "heatmap")
	onePanel(t, "dashboard tab tab", press(t, m, "tab", "tab"), "details")
	if back := press(t, m, "tab", "tab", "tab"); back.focus != FocusPods {
		t.Fatal("tab cycles back to pods")
	}
	left := press(t, m, "1")
	if !left.layout.ShowLeft {
		t.Fatal("1 makes the left panels available")
	}
	onePanel(t, "dashboard left", press(t, left, "shift+tab"), "deployments")
	onePanel(t, "dashboard left namespaces", press(t, left, "tab", "tab", "tab"), "namespaces")
	sel := m.selName
	if moved := press(t, m, "down"); moved.selName == sel {
		t.Fatal("the compact pods list still moves the selection")
	}

	w := press(t, m, "3")
	onePanel(t, "workloads", w, "pods")
	onePanel(t, "workloads tab", press(t, w, "tab"), "deployments")
	onePanel(t, "workloads preview", press(t, w, "tab", "tab"), "details")

	n := press(t, m, "4")
	onePanel(t, "nodes", n, "pods")
	onePanel(t, "nodes tab", press(t, n, "tab"), "node")
	if !strings.Contains(press(t, n, "?").Render().Plain(), "summary / pods") {
		t.Fatal("the compact nodes help announces tab")
	}

	e := press(t, m, "6")
	onePanel(t, "events", e, "events")
	reading := press(t, e, "enter")
	onePanel(t, "events reading", reading, "message")
	onePanel(t, "events back", press(t, reading, "esc"), "events")
	if back := press(t, reading, "esc"); !back.onScreen(ScreenEvents) {
		t.Fatal("the first esc leaves the message, not the screen")
	}

	d := press(t, m, "enter")
	if !d.inDetail() || d.layout.Details != d.layout.Body {
		t.Fatal("pod detail uses the whole compact body")
	}
}

func TestCompactResizeKeepsState(t *testing.T) {
	m := press(t, demoModel(t, 160, 48), "3", "tab", "down")
	focus, sel, dep := m.focus, m.selName, m.depCursor
	resize := func(m Model, w, h int) Model {
		next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
		return next.(Model)
	}
	small := resize(m, 80, 24)
	onePanel(t, "resize to compact", small, "deployments")
	big := resize(small, 160, 48)
	if big.layout.Compact || big.focus != focus || big.selName != sel || big.depCursor != dep || big.nav != m.nav {
		t.Fatal("resizing down and up keeps route, focus and selection")
	}
}

func TestCompactGoldens(t *testing.T) {
	src := newEventsSource("", nil)
	for name, keys := range map[string][]string{
		"l2_dashboard_compact_80x24": nil,
		"l2_workloads_compact_80x24": {"3"},
		"l2_nodes_compact_80x24":     {"4"},
		"l2_poddetail_compact_80x24": {"enter"},
		"l2_events_compact_80x24":    {"6"},
		"l2_events_reading_80x24":    {"6", "enter"},
	} {
		m := press(t, nodeModel(t, src, 80, 24), keys...)
		c := m.Render()
		golden(t, name+".txt", c.Plain())
		golden(t, name+".ansi", c.String())
	}
}

func TestCompactFilterFollowsTheVisiblePanel(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
	}{{"workloads deployments", []string{"3", "tab"}}, {"nodes summary", []string{"4", "tab"}}, {"dashboard heatmap", []string{"tab"}}, {"nodes summary after log", []string{"l", "4", "tab"}}} {
		m := press(t, demoModel(t, 80, 24), tc.keys...)
		if m.focus == FocusPods {
			t.Fatalf("%s: setup leaves pods", tc.name)
		}
		m = press(t, m, "/", "c", "o", "r", "e")
		onePanel(t, tc.name+" filtering", m, "pods")
		if !m.filtering || m.dg.searching || m.focus != FocusPods || m.filter.Value() != "core" {
			t.Fatalf("%s: the filter edits the visible pods panel, not a hidden log search", tc.name)
		}
		if !strings.Contains(m.Render().Plain(), "core") {
			t.Fatalf("%s: the typed text is visible", tc.name)
		}
		m = press(t, m, "enter")
		onePanel(t, tc.name+" confirmed", m, "pods")
		row := m.layout.Pods.Y + 1 + ui.PodsRowsTop
		next, _ := m.Update(tea.MouseClickMsg{X: m.layout.Pods.X + 5, Y: row, Button: tea.MouseLeft})
		clicked := next.(Model)
		if clicked.focus != FocusPods || clicked.selectedPod() == nil || !strings.Contains(clicked.selectedPod().Name, "core") {
			t.Fatalf("%s: the click lands on the filtered pods drawn on screen", tc.name)
		}
		if opened := press(t, clicked, "enter"); !opened.inDetail() || opened.nav.Active().Pod != clicked.selectedPod().Key() {
			t.Fatalf("%s: enter opens the pod shown", tc.name)
		}
	}
}

func TestCompactMessagePagesByVisibleRows(t *testing.T) {
	long := strings.Repeat("linha longa da mensagem do evento ", 60)
	src := newEventsSource("", func(q data.EventQuery) data.EventFeed {
		return data.EventFeed{Scope: q.Scope, Items: []data.Event{{UID: "u1", Age: "1m", Type: "Warning", Reason: "BackOff", Object: "Pod/a", Namespace: "kube-system", Count: 1, Message: long}}}
	})
	m := press(t, events(t, nodeModel(t, src, 80, 24)), "enter")
	page := ui.EventMessageRows(m.layout.Message.H)
	if m.layout.Message.Empty() || page < 5 || m.maxMsgScroll() <= page {
		t.Fatalf("setup needs a message longer than the panel: page %d max %d", page, m.maxMsgScroll())
	}
	down := press(t, m, "pgdn")
	if down.ev.msgScroll != page {
		t.Fatalf("pgdn moves one visible page (%d), got %d", page, down.ev.msgScroll)
	}
	if up := press(t, down, "pgup"); up.ev.msgScroll != 0 {
		t.Fatal("pgup returns one page")
	}
	next, _ := down.Update(tea.MouseWheelMsg{X: m.layout.Message.X + 3, Y: m.layout.Message.Y + 3, Button: tea.MouseWheelDown})
	if next.(Model).ev.msgScroll != page+1 {
		t.Fatal("the wheel on the visible message scrolls it")
	}
}

func TestCompactHeatmapIsReachable(t *testing.T) {
	m := demoModel(t, 80, 24)
	sel := m.selName
	heat := press(t, m, "tab")
	onePanel(t, "heatmap", heat, "heatmap")
	if heat.selName != sel || !strings.Contains(heat.Render().Plain(), "CPU heatmap") {
		t.Fatal("tab opens the heatmap keeping the selection")
	}
	if heat.heatCols() <= 36 {
		t.Fatalf("the heatmap uses the width of the visible panel, got %d columns", heat.heatCols())
	}
	moved := press(t, heat, "down")
	if moved.selName == sel {
		t.Fatal("arrows on the heatmap move the pod selection")
	}
	back := press(t, moved, "shift+tab")
	onePanel(t, "back to pods", back, "pods")
	if back.selName != moved.selName {
		t.Fatal("returning to pods keeps the selection made on the heatmap")
	}
	if help := press(t, heat, "?"); !strings.Contains(help.Render().Plain(), "pods heatmap details") {
		t.Fatal("the compact help announces the heatmap panel")
	}
	golden(t, "l2_dashboard_heatmap_80x24.txt", heat.Render().Plain())
}

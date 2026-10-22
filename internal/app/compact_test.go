package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	onePanel(t, "dashboard tab", press(t, m, "tab"), "details")
	if back := press(t, m, "tab", "tab"); back.focus != FocusPods {
		t.Fatal("tab cycles back to pods")
	}
	left := press(t, m, "1")
	if !left.layout.ShowLeft {
		t.Fatal("1 makes the left panels available")
	}
	onePanel(t, "dashboard left", press(t, left, "shift+tab"), "deployments")
	onePanel(t, "dashboard left namespaces", press(t, left, "tab", "tab"), "namespaces")
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

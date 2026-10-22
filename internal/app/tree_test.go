package app

import (
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type treeProbeSource struct {
	*data.Demo
	calls *atomic.Int64
}

func (s treeProbeSource) Snapshot() data.Snapshot {
	s.calls.Add(1)
	return s.Demo.Snapshot()
}

func (s treeProbeSource) Metrics(tick int, pod string, cols int) data.Metrics {
	s.calls.Add(1)
	return s.Demo.Metrics(tick, pod, cols)
}

func (s treeProbeSource) Logs(pod string, tick, n int) []data.LogLine {
	s.calls.Add(1)
	return s.Demo.Logs(pod, tick, n)
}

func (s treeProbeSource) Events(pod string) []data.Event {
	s.calls.Add(1)
	return s.Demo.Events(pod)
}

func (s treeProbeSource) Manifest(pod string) []string {
	s.calls.Add(1)
	return s.Demo.Manifest(pod)
}

func (s treeProbeSource) Describe(pod string) []data.Field {
	s.calls.Add(1)
	return s.Demo.Describe(pod)
}

func (s treeProbeSource) ScopedEvents(q data.EventQuery) data.EventFeed {
	s.calls.Add(1)
	return s.Demo.ScopedEvents(q)
}

func (s treeProbeSource) Containers(pod string) []data.Container {
	s.calls.Add(1)
	return s.Demo.Containers(pod)
}

func (s treeProbeSource) PodLogs(req data.LogRequest, tick, n int) data.LogView {
	s.calls.Add(1)
	return s.Demo.PodLogs(req, tick, n)
}

func (s treeProbeSource) Security() data.SecuritySnapshot {
	s.calls.Add(1)
	return s.Demo.Security()
}

func treeRowY(t *testing.T, m Model, id DestinationID) int {
	t.Helper()
	st := m.sidebarState()
	for i, r := range m.tree.rows() {
		if r.dest.ID == id {
			if i < st.Offset || i >= st.Offset+ui.SidebarRowsVisible(m.layout.Tree.H) {
				t.Fatalf("%s is scrolled out of view", id)
			}
			return m.layout.Tree.Y + i - st.Offset
		}
	}
	t.Fatalf("tree has no row %s", id)
	return 0
}

func treeTo(t *testing.T, m Model, id DestinationID) Model {
	t.Helper()
	for range 60 {
		rows := m.tree.rows()
		if rows[m.treeCursorIndex(rows)].dest.ID == id {
			return m
		}
		m = press(t, m, "down")
	}
	t.Fatalf("cursor never reached %s", id)
	return m
}

func TestTreeKeyCyclesVisibilityAndFocus(t *testing.T) {
	m := demoModel(t, 160, 48)
	plain := m.Render().Plain()
	m = press(t, m, "b")
	if !m.tree.visible || !m.tree.focused || m.tree.cursor != DestDashboard || !m.tree.isExpanded(DestCluster) {
		t.Fatalf("b shows and focuses the tree on the active leaf: %+v", m.tree)
	}
	if m.layout.TreeOverlay || m.layout.Tree != (Rect{X: 1, Y: 3, W: 26, H: 43}) || m.layout.ContentW != 133 {
		t.Fatalf("160x48 docks the tree: %+v content %d", m.layout.Tree, m.layout.ContentW)
	}
	base := demoModel(t, 133, 48).layout
	if m.layout.Pods.X != base.Pods.X+27 || m.layout.Pods.W != base.Pods.W || m.layout.Details.X != base.Details.X+27 {
		t.Fatal("the body is the same screen layout shifted right of the tree")
	}
	if m.layout.Menu.W != 158 || m.layout.Header.W != 158 {
		t.Fatal("header and bar keep the full width")
	}
	body := press(t, m, "esc")
	if !body.tree.visible || body.tree.focused {
		t.Fatal("esc returns focus to the body and keeps the docked tree")
	}
	if refocus := press(t, body, "b"); !refocus.tree.visible || !refocus.tree.focused {
		t.Fatal("b on a visible unfocused tree focuses it")
	}
	if hidden := press(t, m, "b"); hidden.tree.visible || hidden.Render().Plain() != plain {
		t.Fatal("b on the focused tree closes it and restores the screen")
	}
	if typed := press(t, demoModel(t, 160, 48), "/", "b"); typed.tree.visible || typed.filter.Value() != "b" {
		t.Fatal("b is text inside the filter")
	}
}

func TestTreeOverlayWhenTheBodyDoesNotFit(t *testing.T) {
	docked := press(t, demoModel(t, 130, 40), "b")
	if docked.layout.TreeOverlay || docked.layout.Tree != (Rect{X: 1, Y: 3, W: 24, H: 35}) || !docked.layout.Compact || docked.layout.ContentW != 105 {
		t.Fatalf("130x40 docks the tree next to a compact body: %+v", docked.layout.Tree)
	}
	m := press(t, demoModel(t, 80, 24), "b")
	full := demoModel(t, 80, 24).layout
	if !m.layout.TreeOverlay || m.layout.Tree != (Rect{X: 1, Y: 3, W: 24, H: 19}) || m.layout.Pods != full.Pods {
		t.Fatalf("80x24 opens the tree as an overlay over the unchanged body: %+v", m.layout.Tree)
	}
	sel := m.selName
	next, _ := m.Update(tea.MouseClickMsg{X: 60, Y: 10, Button: tea.MouseLeft})
	closed := next.(Model)
	if closed.tree.visible || closed.selName != sel || closed.focus != m.focus {
		t.Fatal("a click outside the overlay closes it and is consumed")
	}
	next, _ = m.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelDown})
	if w := next.(Model); w.selName != sel || !w.tree.visible {
		t.Fatal("the wheel outside the overlay does not reach the body")
	}
	opened := treeTo(t, press(t, m, "end"), DestRadar)
	opened = press(t, treeTo(t, press(t, opened, "home", "right"), DestNodes), "enter")
	if opened.tree.visible || !opened.onScreen(ScreenNodes) {
		t.Fatal("a leaf closes the overlay and opens the screen")
	}
}

func TestTreeNavigatesWithoutTouchingTheSource(t *testing.T) {
	calls := &atomic.Int64{}
	m := nodeModel(t, treeProbeSource{Demo: data.NewDemo(), calls: calls}, 160, 48)
	m = press(t, m, "b")
	calls.Store(0)
	m = treeTo(t, m, DestWorkloads)
	m = press(t, m, "left", "right", "enter", "enter", "right", "down", "up", "pgdn", "pgup", "end", "home")
	m = treeTo(t, press(t, m, "right"), DestJobs)
	m = press(t, m, "enter")
	st := m.sidebarState()
	_ = ui.SidebarRowText(st.Rows[0])
	if n := calls.Load(); n != 0 {
		t.Fatalf("moving, expanding and collapsing the tree made %d source calls", n)
	}
	if m.nav.Depth() != 0 || !strings.Contains(m.tree.note, "Jobs: tela ainda não implementada") {
		t.Fatalf("a planned leaf explains itself without navigating: %q", m.tree.note)
	}
	plain := m.Render().Plain()
	for _, want := range []string{"Jobs (futuro)", "Overview (futuro)", "Pods", "Deployments", "Jobs: tela ainda não", "implementada"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("tree shows %q", want)
		}
	}
	if strings.Contains(plain, "Pods (futuro)") || strings.Contains(plain, "Jobs (0)") {
		t.Fatal("ready leaves are not planned and planned leaves never claim zero")
	}
}

func TestTreeOpensReadyLeaves(t *testing.T) {
	m := press(t, demoModel(t, 160, 48), "b")
	m = press(t, treeTo(t, m, DestWorkloads), "enter")
	m = press(t, treeTo(t, m, DestDeployments), "enter")
	if r := m.nav.Active(); r.Dest != DestDeployments || m.focus != FocusDeployments || !m.tree.visible || m.tree.focused {
		t.Fatalf("Deployments opens Workloads focused on deployments and gives focus to the body: %+v", r)
	}
	if !strings.Contains(m.Render().Plain(), "›  Deployments") {
		t.Fatal("the active leaf is marked in the tree")
	}
	m = press(t, m, "b", "left")
	if m.tree.cursor != DestWorkloads {
		t.Fatal("left on a leaf selects its parent")
	}
	m = press(t, m, "left")
	if m.tree.isExpanded(DestWorkloads) {
		t.Fatal("left on an open group collapses it")
	}
	if menu := press(t, m, "m"); !menu.menu.open || menu.tree.focused {
		t.Fatal("m leaves the tree for the bar")
	}
	if ev := press(t, m, "6"); !ev.onScreen(ScreenEvents) || ev.tree.focused {
		t.Fatal("numbers activate registered screens from the tree")
	}
	if _, cmd := m.Update(keyPress("c")); cmd == nil {
		t.Fatal("c still opens the context picker while the tree has focus")
	}
	if help := press(t, m, "?"); !help.helpOpen || !strings.Contains(help.Render().Plain(), "expand or first child") {
		t.Fatal("? shows the tree help")
	}
}

func TestTreeMouse(t *testing.T) {
	m := press(t, demoModel(t, 160, 48), "b", "esc")
	click := func(m Model, x, y int) Model {
		next, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		return next.(Model)
	}
	y := treeRowY(t, m, DestWorkloads)
	opened := click(m, m.layout.Tree.X+2, y)
	if !opened.tree.isExpanded(DestWorkloads) || opened.nav != m.nav {
		t.Fatal("clicking [+] only expands")
	}
	label := click(opened, m.layout.Tree.X+8, treeRowY(t, opened, DestPods))
	if label.nav.Active().Dest != DestPods || !label.tree.visible {
		t.Fatal("clicking a ready leaf opens it and keeps the docked tree")
	}
	planned := click(opened, m.layout.Tree.X+8, treeRowY(t, opened, DestCronJobs))
	if planned.nav != opened.nav || !strings.Contains(planned.tree.note, "CronJobs") {
		t.Fatal("clicking a planned leaf explains instead of navigating")
	}
	toggle := click(opened, m.layout.Tree.X+8, treeRowY(t, opened, DestWorkloads))
	if toggle.tree.isExpanded(DestWorkloads) {
		t.Fatal("clicking a group name toggles it")
	}
	sel := opened.selName
	wheel, _ := opened.Update(tea.MouseWheelMsg{X: m.layout.Tree.X + 4, Y: 10, Button: tea.MouseWheelDown})
	if wheel.(Model).selName != sel {
		t.Fatal("the wheel over the tree does not move the pod selection")
	}
	body := click(opened, m.layout.Pods.X+5, m.layout.Pods.Y+5)
	if body.tree.focused || !body.tree.visible {
		t.Fatal("a click in the docked body moves focus out of the tree")
	}
}

func TestTreeGoldens(t *testing.T) {
	src := newEventsSource("", nil)
	for _, size := range [][2]int{{160, 48}, {130, 40}, {80, 24}} {
		m := press(t, events(t, nodeModel(t, src, size[0], size[1])), "b")
		m = press(t, treeTo(t, m, DestWorkloads), "right")
		c := m.Render()
		name := "l2_tree_events_" + map[int]string{160: "160x48", 130: "130x40", 80: "overlay_80x24"}[size[0]]
		golden(t, name+".txt", c.Plain())
		golden(t, name+".ansi", c.String())
	}
}

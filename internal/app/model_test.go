package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/TNK-Studio/lazykube/internal/anim"
	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/ui"
)

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "pgdn":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "ctrl+p":
		return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		next, _ := m.Update(keyPress(k))
		m = next.(Model)
	}
	return m
}

func TestInitialState(t *testing.T) {
	m := demoModel(t, 160, 48)
	if m.focus != FocusPods || m.tab != ui.TabTop {
		t.Fatal("pods focused and Top tab by default")
	}
	if m.selName != "bilibili-live-7f6b8d9c5-mn4tz" {
		t.Fatalf("initial pod got %s", m.selName)
	}
}

func TestFocusCycle(t *testing.T) {
	m := demoModel(t, 160, 48)
	m = press(t, m, "tab")
	if m.focus != FocusDetails {
		t.Fatalf("tab from pods goes to details, got %d", m.focus)
	}
	m = press(t, m, "tab")
	if m.focus != FocusNamespaces {
		t.Fatalf("tab wraps to namespaces, got %d", m.focus)
	}
	m = press(t, m, "shift+tab")
	if m.focus != FocusDetails {
		t.Fatalf("shift+tab goes back, got %d", m.focus)
	}
	narrow := press(t, demoModel(t, 130, 40), "tab", "tab")
	if narrow.focus != FocusPods {
		t.Fatalf("hidden left column is skipped, got %d", narrow.focus)
	}
}

func TestSelectionMovesAndScrolls(t *testing.T) {
	m := demoModel(t, 160, 48)
	m = press(t, m, "j", "j")
	if m.selName != "psql-6bc7744f9-r7zlc" {
		t.Fatalf("got %s", m.selName)
	}
	for i := 0; i < 20; i++ {
		m = press(t, m, "j")
	}
	if m.selName != "svclb-traefik-8c2f1-6n2vb" {
		t.Fatalf("selection must clamp at the end, got %s", m.selName)
	}
	m = press(t, m, "k")
	if m.selName != "local-path-provisioner-84db5d-h4" {
		t.Fatalf("got %s", m.selName)
	}
}

func TestFuzzyFilter(t *testing.T) {
	m := demoModel(t, 160, 48)
	m = press(t, m, "/", "o", "d", "o", "o")
	pods := m.visiblePods()
	if len(pods) == 0 {
		t.Fatal("filter must match ys-odoo pods")
	}
	for _, p := range pods {
		if p.Namespace != "ys-odoo" {
			t.Fatalf("unexpected match %s/%s", p.Namespace, p.Name)
		}
	}
	if m.selectedIndex(pods) < 0 {
		t.Fatal("selection must move into the filtered set")
	}
	m = press(t, m, "esc")
	if m.filtering || m.filter.Value() != "" || len(m.visiblePods()) != 13 {
		t.Fatal("esc must leave and clear the filter")
	}
}

func TestFilterKeepsTypedLetters(t *testing.T) {
	m := press(t, demoModel(t, 160, 48), "/", "k", "j", "q")
	if m.filter.Value() != "kjq" {
		t.Fatalf("letters must go to the filter, got %q", m.filter.Value())
	}
}

func TestNamespaceFilter(t *testing.T) {
	m := demoModel(t, 160, 48)
	m = press(t, m, "tab", "tab")
	if m.focus != FocusNamespaces {
		t.Fatal("expected namespaces focus")
	}
	m = press(t, m, "j", "enter")
	if m.nsFilter != "kube-system" {
		t.Fatalf("got ns filter %q", m.nsFilter)
	}
	if n := len(m.visiblePods()); n != 5 {
		t.Fatalf("kube-system has 5 pods, got %d", n)
	}
	m = press(t, m, "esc")
	if m.nsFilter != "" {
		t.Fatal("esc must clear namespace filter")
	}
}

func TestTabsAndPalette(t *testing.T) {
	m := demoModel(t, 160, 48)
	m = press(t, m, "l")
	if m.tab != ui.TabLog {
		t.Fatal("l opens Log")
	}
	m = press(t, m, "[")
	if m.tab != ui.TabEvents {
		t.Fatal("[ wraps to Events")
	}
	m = press(t, m, "]", "]")
	if m.tab != ui.TabConfig {
		t.Fatal("] advances")
	}
	m = press(t, m, "p")
	if m.pal.Name != "ocean" {
		t.Fatalf("p cycles palette, got %s", m.pal.Name)
	}
}

func TestToggleLeftColumn(t *testing.T) {
	m := press(t, demoModel(t, 160, 48), "1")
	if m.layout.ShowLeft {
		t.Fatal("1 hides left column")
	}
	m = press(t, m, "1")
	if !m.layout.ShowLeft {
		t.Fatal("1 shows left column again")
	}
	n := press(t, demoModel(t, 130, 40), "1")
	if !n.layout.ShowLeft {
		t.Fatal("1 forces left column on narrow terminals")
	}
}

func TestMouseSelectsPodAndTab(t *testing.T) {
	m := demoModel(t, 160, 48)
	rowY := m.layout.Pods.Y + 1 + ui.PodsRowsTop + 3
	next, _ := m.Update(tea.MouseClickMsg{X: m.layout.Pods.X + 5, Y: rowY, Button: tea.MouseLeft})
	m = next.(Model)
	if m.selName != "psql-6bc7744f9-r7zlc" {
		t.Fatalf("click selects 4th row, got %s", m.selName)
	}
	span := ui.TabSpans()[4]
	next, _ = m.Update(tea.MouseClickMsg{X: m.layout.Details.X + 2 + span.X + 1, Y: m.layout.Details.Y + 1, Button: tea.MouseLeft})
	m = next.(Model)
	if m.tab != ui.TabEvents || m.focus != FocusDetails {
		t.Fatalf("click on Events tab, got tab %d focus %d", m.tab, m.focus)
	}
	next, _ = m.Update(tea.MouseWheelMsg{X: m.layout.Pods.X + 5, Y: rowY, Button: tea.MouseWheelDown})
	if next.(Model).selName != "odoo-7575bf6d8-9wjvk" {
		t.Fatal("wheel over pods moves selection")
	}
}

func TestHoverAndHelp(t *testing.T) {
	m := demoModel(t, 160, 48)
	next, _ := m.Update(tea.MouseMotionMsg{X: m.layout.Pods.X + 5, Y: m.layout.Pods.Y + 1 + ui.PodsRowsTop})
	if next.(Model).hover != 0 {
		t.Fatal("motion over first row sets hover")
	}
	m = press(t, m, "?")
	if !m.helpOpen {
		t.Fatal("? opens help")
	}
	m = press(t, m, "?")
	if m.helpOpen {
		t.Fatal("? closes help")
	}
}

func TestPauseAndFocusStopFrames(t *testing.T) {
	m := demoModel(t, 160, 48)
	m.animated = true
	gen := m.gen
	m = press(t, m, "ctrl+p")
	if !m.paused || m.gen == gen {
		t.Fatal("ctrl+p pauses and restarts the tick chain")
	}
	stale, _ := m.Update(frameMsg{gen: gen})
	if stale.(Model).clock.T != m.clock.T {
		t.Fatal("stale frames are ignored")
	}
	before := m.clock.T
	next, _ := m.Update(dataTickMsg{gen: m.gen})
	if next.(Model).clock.T != before+1 {
		t.Fatal("data ticks keep advancing while paused")
	}
}

func TestQuit(t *testing.T) {
	_, cmd := demoModel(t, 160, 48).Update(keyPress("q"))
	if cmd == nil {
		t.Fatal("q must quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q must return tea.Quit")
	}
}

func spriteCells(c *draw.Canvas, l Layout) []draw.Cell {
	var cells []draw.Cell
	for y := l.Node.Y + 2; y < l.Node.Y+l.Node.H-1; y++ {
		for x := l.Node.X + 2; x < l.Node.X+2+ui.SpriteW; x++ {
			cells = append(cells, c.At(x, y))
		}
	}
	return cells
}

func TestNoAnimFreezesEffects(t *testing.T) {
	m := New(Options{Source: data.NewDemo(), NoAnim: true, InitialPod: 1})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	m = next.(Model)
	before := m.Render()
	for i := 0; i < 23; i++ {
		next, _ = m.Update(dataTickMsg{gen: m.gen})
		m = next.(Model)
	}
	after := m.Render()
	if !strings.Contains(before.Plain(), "⠋ metrics") || !strings.Contains(after.Plain(), "⠋ metrics") {
		t.Fatal("header spinner must stay on the first frame with --no-anim")
	}
	b, a := spriteCells(before, m.layout), spriteCells(after, m.layout)
	for i := range b {
		if b[i] != a[i] {
			t.Fatalf("sprite cell %d changed with --no-anim", i)
		}
	}
}

func TestScrollOrientationPerTab(t *testing.T) {
	m := press(t, demoModel(t, 160, 36), "tab", "c")
	if m.focus != FocusDetails || m.tab != ui.TabConfig {
		t.Fatal("setup failed")
	}
	for i := 0; i < 40; i++ {
		m.manifest = append(m.manifest, "extra: line")
	}
	rows := ui.ScrollRows(ui.TabConfig, m.layout.Details.H)
	if len(m.manifest) <= rows {
		t.Fatalf("test needs overflowing config: %d lines, %d rows", len(m.manifest), rows)
	}
	m = press(t, m, "pgdn")
	if m.scroll == 0 {
		t.Fatal("pgdown at the top must advance in Config")
	}
	m = press(t, m, "end")
	if m.scroll != len(m.manifest)-rows {
		t.Fatalf("end must stop at the last page, got %d", m.scroll)
	}
	m = press(t, m, "home")
	if m.scroll != 0 {
		t.Fatal("home goes to the first line")
	}
	m = press(t, m, "j")
	if m.scroll != 1 {
		t.Fatal("down scrolls forward")
	}

	short := press(t, demoModel(t, 160, 48), "tab", "d", "end")
	if short.scroll != max(0, len(short.describe)-ui.ScrollRows(ui.TabDescribe, short.layout.Details.H)) {
		t.Fatal("content that fits must not scroll")
	}

	l := press(t, longLogModel(t, 160, 48), "tab", "l")
	if !l.follow || l.scroll != l.maxScroll() {
		t.Fatal("log opens following the tail")
	}
	l = press(t, l, "k")
	if l.follow || l.scroll != l.maxScroll()-1 {
		t.Fatal("scrolling up in Log stops follow")
	}
	l = press(t, l, "G")
	if !l.follow {
		t.Fatal("G resumes follow")
	}
	l = press(t, l, "g")
	if l.scroll != 0 || l.follow {
		t.Fatal("g goes to the oldest line")
	}
}

func TestLogNewLinesWhileScrolledUp(t *testing.T) {
	m := longLogModel(t, 160, 48)
	m.animated = false
	m = press(t, m, "tab", "l", "k")
	top := m.scroll
	for i := 0; i < 16; i++ {
		next, _ := m.Update(dataTickMsg{gen: m.gen})
		m = next.(Model)
	}
	if m.newLines != 2 {
		t.Fatalf("two lines arrive in 16 ticks, got %d", m.newLines)
	}
	if m.scroll != top {
		t.Fatal("view must stay anchored while scrolled up")
	}
}

func TestWrapTogglesVisualRows(t *testing.T) {
	m := press(t, longLogModel(t, 120, 40), "tab", "l")
	plain := len(ui.LogRows(m.logs, ui.LogWidth(m.layout.Details.W), false))
	m = press(t, m, "w")
	if !m.wrap {
		t.Fatal("w toggles wrap")
	}
	wrapped := len(ui.LogRows(m.logs, ui.LogWidth(m.layout.Details.W), true))
	if wrapped <= plain {
		t.Fatalf("narrow details must wrap long lines: %d vs %d", wrapped, plain)
	}
	screen := m.Render().Plain()
	if !strings.Contains(screen, "id=883117: already") || !strings.Contains(screen, " ended ") || strings.Contains(screen, "already e…") {
		t.Fatal("wrapped log must show the tail of long messages")
	}
}

func TestFilterAcceptsPaste(t *testing.T) {
	m := press(t, demoModel(t, 160, 48), "/")
	next, _ := m.Update(tea.PasteMsg{Content: "odoo"})
	m = next.(Model)
	if m.filter.Value() != "odoo" {
		t.Fatalf("paste must reach the filter, got %q", m.filter.Value())
	}
	for _, p := range m.visiblePods() {
		if p.Namespace != "ys-odoo" {
			t.Fatal("paste must filter the list")
		}
	}
	idle, _ := demoModel(t, 160, 48).Update(tea.PasteMsg{Content: "x"})
	if idle.(Model).filter.Value() != "" {
		t.Fatal("paste outside the filter is ignored")
	}
}

func TestDeploymentClickHonorsScroll(t *testing.T) {
	m := demoModel(t, 160, 36)
	m.focus = FocusDeployments
	for i := 0; i < 10; i++ {
		m = press(t, m, "j")
	}
	if m.depCursor != 7 {
		t.Fatalf("cursor got %d", m.depCursor)
	}
	top, rows := ui.DeploymentsRows(m.layout.Deployments.H)
	if rows != 2 {
		t.Fatalf("expected 2 visible rows at 160x36, got %d", rows)
	}
	next, _ := m.Update(tea.MouseClickMsg{X: 5, Y: m.layout.Deployments.Y + top, Button: tea.MouseLeft})
	if got := next.(Model).depCursor; got != 6 {
		t.Fatalf("first visible row is index 6, got %d", got)
	}
	next, _ = m.Update(tea.MouseClickMsg{X: 5, Y: m.layout.Deployments.Y + top - 1, Button: tea.MouseLeft})
	if next.(Model).depCursor != 7 {
		t.Fatal("click on padding must not change the cursor")
	}
}

func TestEmptyFilterClearsDetails(t *testing.T) {
	m := press(t, demoModel(t, 160, 48), "/", "z", "z", "z")
	if len(m.visiblePods()) != 0 {
		t.Fatal("setup: filter must match nothing")
	}
	if m.selectedPod() != nil || m.logs != nil || m.manifest != nil || m.metrics.CPUNow != 0 {
		t.Fatal("details must be cleared without visible pods")
	}
	if !strings.Contains(m.Render().Plain(), "no pod selected") {
		t.Fatal("details panel must say nothing is selected")
	}
	m = press(t, m, "esc")
	if m.selectedPod() == nil || m.selectedPod().Name != "bilibili-live-7f6b8d9c5-mn4tz" {
		t.Fatal("selection is restored when pods are visible again")
	}

	n := press(t, demoModel(t, 160, 48), "tab", "tab", "enter")
	if n.nsFilter != "default" || n.selectedPod() != nil {
		t.Fatal("empty namespace must clear the selection")
	}
}

func longLogModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := demoModel(t, w, h)
	m.clock = anim.Fixed(400, goldenPhase, 30)
	m.refresh()
	return m
}

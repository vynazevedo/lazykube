package app

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/TNK-Studio/lazykube/internal/ui"
)

type equivalenceStep struct {
	name string
	msg  func(m Model) tea.Msg
}

func equivalenceKey(k string) func(Model) tea.Msg {
	return func(Model) tea.Msg { return keyPress(k) }
}

func equivalenceScript(w, h int) []equivalenceStep {
	ow, oh := 160, 48
	if w == 160 {
		ow, oh = 130, 40
	}
	podRow := func(m Model, row int) (int, int) {
		return m.layout.Pods.X + 5, m.layout.Pods.Y + 1 + ui.PodsRowsTop + row
	}
	return []equivalenceStep{
		{"initial", nil},
		{"down", equivalenceKey("down")},
		{"down-again", equivalenceKey("j")},
		{"tab-focus", equivalenceKey("tab")},
		{"tab-log", equivalenceKey("l")},
		{"tab-next", equivalenceKey("]")},
		{"tab-security", equivalenceKey("S")},
		{"security-namespace", equivalenceKey("S")},
		{"tab-events", equivalenceKey("e")},
		{"events-scope", equivalenceKey("E")},
		{"tab-describe", equivalenceKey("d")},
		{"page-down", equivalenceKey("pgdn")},
		{"filter-open", equivalenceKey("/")},
		{"filter-b", equivalenceKey("b")},
		{"filter-i", equivalenceKey("i")},
		{"filter-confirm", equivalenceKey("enter")},
		{"filter-clear", equivalenceKey("esc")},
		{"click-pod", func(m Model) tea.Msg {
			x, y := podRow(m, 3)
			return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
		}},
		{"wheel-pod", func(m Model) tea.Msg {
			x, y := podRow(m, 3)
			return tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}
		}},
		{"hover-pod", func(m Model) tea.Msg {
			x, y := podRow(m, 0)
			return tea.MouseMotionMsg{X: x, Y: y}
		}},
		{"click-tab", func(m Model) tea.Msg {
			span := ui.TabSpans()[0]
			return tea.MouseClickMsg{X: m.layout.Details.X + 2 + span.X + 1, Y: m.layout.Details.Y + 1, Button: tea.MouseLeft}
		}},
		{"toggle-left", equivalenceKey("1")},
		{"palette", equivalenceKey("p")},
		{"data", func(Model) tea.Msg { return dataMsg{} }},
		{"resize-other", func(Model) tea.Msg { return tea.WindowSizeMsg{Width: ow, Height: oh} }},
		{"resize-back", func(Model) tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} }},
		{"help-open", equivalenceKey("?")},
		{"help-close", equivalenceKey("esc")},
		{"focus-namespaces", equivalenceKey("tab")},
		{"namespace-enter", equivalenceKey("enter")},
		{"namespace-clear", equivalenceKey("esc")},
	}
}

func equivalenceTranscript(t *testing.T, w, h int) string {
	t.Helper()
	m := demoModel(t, w, h)
	var b strings.Builder
	for _, step := range equivalenceScript(w, h) {
		if step.msg != nil {
			next, _ := m.Update(step.msg(m))
			m = next.(Model)
		}
		c := m.Render()
		fmt.Fprintf(&b, "== %s ansi=%x\n%s\n", step.name, sha256.Sum256([]byte(c.String())), c.Plain())
	}
	return b.String()
}

func TestRouterMatchesLegacyDashboard(t *testing.T) {
	for _, size := range []struct{ w, h int }{{130, 40}, {160, 48}} {
		name := fmt.Sprintf("router_equivalence_%dx%d.txt", size.w, size.h)
		t.Run(name, func(t *testing.T) {
			golden(t, name, equivalenceTranscript(t, size.w, size.h))
		})
	}
}

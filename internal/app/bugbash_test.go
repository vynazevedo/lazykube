package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestWheelBehindHelpChangesNothing(t *testing.T) {
	for _, screen := range []string{"2", "3", "4", "6"} {
		m := press(t, demoModel(t, 160, 48), screen, "?")
		if !m.helpOpen {
			t.Fatalf("%s: help opens", screen)
		}
		for _, r := range []Rect{m.layout.Pods, m.layout.Namespaces, m.layout.Deployments, m.layout.Details, m.layout.Events, m.layout.Message} {
			if r.Empty() {
				continue
			}
			next, _ := m.Update(tea.MouseWheelMsg{X: r.X + 2, Y: r.Y + 3, Button: tea.MouseWheelDown})
			got := next.(Model)
			if got.selName != m.selName || got.nsCursor != m.nsCursor || got.depCursor != m.depCursor || got.scroll != m.scroll || got.ev.Cursor != m.ev.Cursor || got.ev.msgScroll != m.ev.msgScroll {
				t.Fatalf("screen %s: the wheel behind the help changed the state under %+v", screen, r)
			}
		}
	}
}

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

func TestLogSearchDoesNotSwallowEscElsewhere(t *testing.T) {
	m := press(t, detailsModel(t, 1), "l", "/", "h", "e", "a", "r", "t", "enter")
	if m.dg.search.Value() != "heart" {
		t.Fatal("setup confirms a Log search")
	}
	for _, screen := range []string{"4", "6"} {
		on := press(t, m, screen)
		back := press(t, on, "esc")
		if back.onScreen(on.nav.Active().Screen) && back.nav.Depth() == on.nav.Depth() {
			t.Fatalf("screen %s: esc must act on the screen, not on the hidden Log search", screen)
		}
	}
	if cleared := press(t, m, "esc"); cleared.dg.search.Value() != "" {
		t.Fatal("on the Dashboard esc still clears the visible Log search")
	}
}

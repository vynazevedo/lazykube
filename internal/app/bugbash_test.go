package app

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
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

func TestSmallTerminalIsClearAndUsable(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 30}, {44, 12}} {
		m := demoModel(t, size[0], size[1])
		if !m.layout.TooSmall {
			t.Fatalf("%dx%d is below the minimum", size[0], size[1])
		}
		plain := m.Render().Plain()
		for _, want := range []string{"lazykube", "120×36", fmt.Sprintf("%d×%d", size[0], size[1]), "q sair"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("%dx%d small screen misses %q:\n%s", size[0], size[1], want, plain)
			}
		}
		if size[0] >= 80 {
			for _, want := range []string{"context demo", "namespace Todos", "faltam 40 colunas e 12 linhas", "c contexto", "n namespace"} {
				if size[0] == 100 {
					want = strings.Replace(want, "faltam 40 colunas e 12 linhas", "faltam 20 colunas e 6 linhas", 1)
				}
				if !strings.Contains(plain, want) {
					t.Fatalf("%dx%d small screen misses %q:\n%s", size[0], size[1], want, plain)
				}
			}
		}
		for _, line := range strings.Split(plain, "\n") {
			if n := len([]rune(line)); n != size[0] {
				t.Fatalf("%dx%d: line has %d cells", size[0], size[1], n)
			}
		}
		if _, cmd := m.Update(keyPress("c")); cmd == nil {
			t.Fatalf("%dx%d: c still opens the context picker", size[0], size[1])
		}
		if _, cmd := m.Update(keyPress("q")); cmd == nil {
			t.Fatalf("%dx%d: q quits", size[0], size[1])
		}
	}
	golden(t, "too_small_80x24.txt", demoModel(t, 80, 24).Render().Plain())
	grown, _ := demoModel(t, 80, 24).Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	if grown.(Model).layout.TooSmall || !strings.Contains(grown.(Model).Render().Plain(), "Pods") {
		t.Fatal("growing the terminal restores the full interface")
	}
}

func TestDetailsEventsTabReleasesClusterFeed(t *testing.T) {
	src := newEventsSource("bilibili-raffle", nil)
	m := press(t, nodeModel(t, src, 160, 48), "e")
	for range 3 {
		if m.dg.scope == data.ScopeCluster {
			break
		}
		m = press(t, m, "E")
	}
	if m.dg.scope != data.ScopeCluster || src.clusterQueries() == 0 {
		t.Fatal("setup reads the cluster feed in the Events tab")
	}
	if *src.released != 0 {
		t.Fatal("the feed stays while the tab shows it")
	}
	m = press(t, m, "l")
	if *src.released != 1 {
		t.Fatalf("leaving the Events tab releases the cluster feed, released %d", *src.released)
	}
	m = press(t, m, "e")
	if m.dg.scope != data.ScopeCluster || *src.released != 1 {
		t.Fatal("returning to the tab keeps the explicit cluster scope")
	}
	if press(t, m, "4"); *src.released != 2 {
		t.Fatalf("a screen without details releases the feed, released %d", *src.released)
	}
}

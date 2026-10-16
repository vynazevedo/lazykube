package app

import (
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func TestRouterHasSingleDashboardRoute(t *testing.T) {
	r := newRouter()
	if r.Active() != (Route{Screen: ScreenDashboard}) {
		t.Fatalf("initial route = %+v", r.Active())
	}
	if got := registeredScreens(); !reflect.DeepEqual(got, []ScreenID{ScreenDashboard}) {
		t.Fatalf("registered screens = %v", got)
	}
	s, ok := screenFor(ScreenDashboard)
	if !ok || s.ID() != ScreenDashboard {
		t.Fatalf("dashboard screen = %v %v", s, ok)
	}
	if _, ok := screenFor(ScreenDashboard + 1); ok {
		t.Fatal("no other route may be registered yet")
	}
	m := demoModel(t, 160, 48)
	if m.nav.Active().Screen != ScreenDashboard || m.screen().ID() != ScreenDashboard {
		t.Fatalf("model route = %+v", m.nav.Active())
	}
}

func routerHoldsReferences(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface, reflect.UnsafePointer:
		return true
	case reflect.Struct:
		for i := range t.NumField() {
			if routerHoldsReferences(t.Field(i).Type) {
				return true
			}
		}
	case reflect.Array:
		return routerHoldsReferences(t.Elem())
	}
	return false
}

func TestRouterAndScreenDoNotAliasModelState(t *testing.T) {
	if n := reflect.TypeOf(dashboardScreen{}).NumField(); n != 0 {
		t.Fatalf("dashboard adapter must not hold state, fields = %d", n)
	}
	if routerHoldsReferences(reflect.TypeOf(router{})) {
		t.Fatal("router must be a plain value without references into the model")
	}
	base := demoModel(t, 160, 48)
	before := base.Render().String()
	copied := press(t, base, "tab", "l", "down", "S")
	if copied.focus == base.focus || copied.tab == base.tab {
		t.Fatal("script must change the copy")
	}
	if base.Render().String() != before || base.focus != FocusPods || base.tab != ui.TabTop || base.nav != newRouter() {
		t.Fatal("updating a copy through the router must not change the original model")
	}
}

func routerDirect(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := dashboardScreen{}.Update(m, msg)
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseClickMsg, tea.MouseWheelMsg:
		out, wcmd := wake(!m.busy(), next, cmd)
		return out.(Model), wcmd
	}
	return next, cmd
}

func TestRootDelegatesToDashboard(t *testing.T) {
	for _, size := range []struct{ w, h int }{{130, 40}, {160, 48}} {
		base := demoModel(t, size.w, size.h)
		podX, podY := base.layout.Pods.X+5, base.layout.Pods.Y+1+ui.PodsRowsTop+2
		filtering := press(t, base, "/")
		cases := []struct {
			name string
			m    Model
			msg  tea.Msg
		}{
			{"key-tab", base, keyPress("tab")},
			{"key-log", base, keyPress("l")},
			{"key-down", base, keyPress("down")},
			{"key-filter", base, keyPress("/")},
			{"key-security", base, keyPress("S")},
			{"key-help", base, keyPress("?")},
			{"filter-typing", filtering, keyPress("o")},
			{"filter-paste", filtering, tea.PasteMsg{Content: "odoo"}},
			{"mouse-click", base, tea.MouseClickMsg{X: podX, Y: podY, Button: tea.MouseLeft}},
			{"mouse-wheel", base, tea.MouseWheelMsg{X: podX, Y: podY, Button: tea.MouseWheelDown}},
			{"mouse-motion", base, tea.MouseMotionMsg{X: podX, Y: podY}},
			{"resize", base, tea.WindowSizeMsg{Width: 200, Height: 60}},
			{"resize-small", base, tea.WindowSizeMsg{Width: 100, Height: 30}},
		}
		for _, c := range cases {
			next, rootCmd := c.m.Update(c.msg)
			root := next.(Model)
			direct, directCmd := routerDirect(c.m, c.msg)
			if root.Render().String() != direct.Render().String() {
				t.Fatalf("%dx%d %s: root render differs from dashboard", size.w, size.h, c.name)
			}
			if root.focus != direct.focus || root.tab != direct.tab || root.selName != direct.selName || root.layout != direct.layout || root.filter.Value() != direct.filter.Value() || root.hover != direct.hover || root.helpOpen != direct.helpOpen {
				t.Fatalf("%dx%d %s: root state differs from dashboard", size.w, size.h, c.name)
			}
			if (rootCmd == nil) != (directCmd == nil) {
				t.Fatalf("%dx%d %s: command presence differs, root %v direct %v", size.w, size.h, c.name, rootCmd != nil, directCmd != nil)
			}
		}
	}
}

type routerLiveSource struct {
	*data.Demo
	changes chan struct{}
}

func (s routerLiveSource) Changes() <-chan struct{} {
	return s.changes
}

func routerRun(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	out := make(chan tea.Msg, len(batch))
	for _, c := range batch {
		go func() { out <- c() }()
	}
	var msgs []tea.Msg
	for range batch {
		select {
		case m := <-out:
			msgs = append(msgs, m)
		case <-time.After(2 * time.Second):
			t.Fatal("init command never resolved")
		}
	}
	return msgs
}

func TestRootInitKeepsClockAndChanges(t *testing.T) {
	if cmd := (dashboardScreen{}).Init(Model{}); cmd != nil {
		t.Fatal("dashboard has no commands of its own")
	}
	src := routerLiveSource{Demo: data.NewDemo(), changes: make(chan struct{}, 1)}
	src.changes <- struct{}{}
	start := time.Unix(1000, 0)
	m := New(Options{Source: src, InitialPod: 1, Now: func() time.Time { return start }})
	var frames, changes int
	for _, msg := range routerRun(t, m.Init()) {
		switch msg.(type) {
		case frameMsg:
			frames++
		case dataMsg:
			changes++
		}
	}
	if frames != 1 || changes != 1 {
		t.Fatalf("init must keep exactly one frame tick and one Changes consumer, frames %d changes %d", frames, changes)
	}
	next, cmd := m.Update(dataMsg{})
	if cmd == nil || next.(Model).src != m.src {
		t.Fatal("data messages keep the same source and rearm its Changes consumer")
	}
	static := demoModel(t, 160, 48)
	if cmd := static.Init(); cmd != nil {
		t.Fatal("static demo has neither frames nor Changes")
	}
}

func TestRootKeepsInputCommands(t *testing.T) {
	m, _ := animModel(t)
	paused, cmd := m.Update(keyPress("ctrl+p"))
	if cmd != nil || !paused.(Model).paused {
		t.Fatal("pausing stops the tick")
	}
	if _, cmd = paused.Update(keyPress("ctrl+p")); cmd == nil {
		t.Fatal("resuming must keep its tick command")
	}
	_, cmd = m.Update(keyPress("/"))
	if cmd == nil {
		t.Fatal("filter focus must keep its command")
	}
	for _, k := range []tea.KeyPressMsg{{Code: 'c', Mod: tea.ModCtrl}, keyPress("q")} {
		_, cmd = demoModel(t, 160, 48).Update(k)
		if cmd == nil {
			t.Fatalf("%s must quit", k.String())
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%s must quit", k.String())
		}
	}
	help := press(t, demoModel(t, 160, 48), "?")
	if _, cmd := help.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("ctrl+c stays global with help open")
	}
}

func TestDashboardKeysMatchKeyMap(t *testing.T) {
	m := demoModel(t, 160, 48)
	keys := m.screen().Keys(m)
	if want := reflect.TypeOf(KeyMap{}).NumField(); len(keys) != want {
		t.Fatalf("dashboard bindings = %d, key map fields = %d", len(keys), want)
	}
	seen := map[string]bool{}
	for _, b := range keys {
		if !b.Enabled() {
			t.Fatalf("binding %v disabled", b.Keys())
		}
		for _, k := range b.Keys() {
			if seen[k] {
				t.Fatalf("key %q bound twice", k)
			}
			seen[k] = true
		}
	}
	for _, k := range []string{"m", "2", "3", "4", "5", "6"} {
		if seen[k] {
			t.Fatalf("navigation key %q belongs to a later PR", k)
		}
	}
}

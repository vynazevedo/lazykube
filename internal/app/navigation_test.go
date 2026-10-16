package app

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

var _ = Frame{} == Frame{}

func navCrumbs(m Model) string {
	return strings.Join(crumbLabels(m.crumbs()), " > ")
}

func navFocusNamespaces(t *testing.T, m Model) Model {
	t.Helper()
	top, _ := ui.NamespacesRows(m.layout.Namespaces.H)
	next, _ := m.Update(tea.MouseClickMsg{X: m.layout.Namespaces.X + 3, Y: m.layout.Namespaces.Y + top + 1, Button: tea.MouseLeft})
	m = next.(Model)
	if m.focus != FocusNamespaces || m.nsCursor != 1 {
		t.Fatalf("click must focus the kube-system row, focus %d cursor %d", m.focus, m.nsCursor)
	}
	return m
}

func navNamespace(t *testing.T, m Model) Model {
	t.Helper()
	m = press(t, navFocusNamespaces(t, m), "enter")
	if r := m.nav.Active(); r.Kind != RouteNamespace || r.Namespace != "kube-system" {
		t.Fatalf("enter on namespace opens its scope, got %+v", r)
	}
	return m
}

func TestRouterStackLimitAndDedup(t *testing.T) {
	r := newRouter()
	if r.push(Frame{Route: r.Active()}, r.Active()) || r.Depth() != 0 {
		t.Fatal("activating the same route must not push")
	}
	for i := range 40 {
		to := Route{Kind: RouteNamespace, Screen: ScreenDashboard, Namespace: fmt.Sprintf("ns-%d", i)}
		if !r.push(Frame{Route: r.Active()}, to) {
			t.Fatalf("push %d refused", i)
		}
		if r.push(Frame{Route: r.Active()}, to) {
			t.Fatalf("repeated route %d pushed twice", i)
		}
	}
	if r.Depth() != maxFrames {
		t.Fatalf("depth = %d, want %d", r.Depth(), maxFrames)
	}
	if f, _ := r.Frame(0); f.Route.Namespace != "ns-7" {
		t.Fatalf("oldest frames must be discarded first, bottom = %+v", f.Route)
	}
	f, ok := r.pop()
	if !ok || f.Route.Namespace != "ns-38" || r.Active().Namespace != "ns-38" || r.Depth() != maxFrames-1 {
		t.Fatalf("pop = %+v depth %d", f.Route, r.Depth())
	}
	if _, ok := r.popTo(r.Depth()); ok {
		t.Fatal("popping to the current depth is not a move")
	}
	if _, ok := r.popTo(0); !ok || r.Depth() != 0 || r.Active().Namespace != "ns-7" {
		t.Fatalf("popTo(0) = %+v depth %d", r.Active(), r.Depth())
	}
	if _, ok := r.pop(); ok {
		t.Fatal("empty stack cannot pop")
	}
}

func TestFramesHoldNoModelReferences(t *testing.T) {
	if routerHoldsReferences(reflect.TypeOf(Frame{})) || routerHoldsReferences(reflect.TypeOf(router{})) {
		t.Fatal("frames must be plain values without pointers, maps, slices, funcs, channels or interfaces")
	}
	m := navNamespace(t, demoModel(t, 160, 48))
	m = press(t, m, "tab", "tab", "enter")
	before := m.nav
	m.motion.fades["intruder"] = m.motion.fades[m.selName]
	m.securitySession.Acknowledge()
	m.snap.Pods = nil
	m.logs = append(m.logs, data.LogLine{Message: "mutated"})
	if m.nav != before {
		t.Fatal("mutating shared model state must not reach the navigation stack")
	}
	copied := m
	copied.nav.push(copied.frame(), Route{Kind: RouteNamespace, Screen: ScreenDashboard, Namespace: "other"})
	if m.nav != before {
		t.Fatal("a pushed copy must not alias the original stack")
	}
}

func TestEscConsumesOneLayer(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		m := press(t, navNamespace(t, demoModel(t, 160, 48)), "?", "esc")
		if m.helpOpen || m.nav.Depth() != 1 {
			t.Fatal("esc closes help without popping")
		}
	})
	t.Run("menu", func(t *testing.T) {
		m := press(t, navNamespace(t, demoModel(t, 160, 48)), "m", "esc")
		if m.menu.open || m.nav.Depth() != 1 {
			t.Fatal("esc closes the menu without popping")
		}
	})
	t.Run("filter input", func(t *testing.T) {
		m := press(t, navNamespace(t, demoModel(t, 160, 48)), "/", "o", "esc")
		if m.filtering || m.filter.Value() != "" || m.nav.Depth() != 1 {
			t.Fatal("esc leaves and clears the filter input without popping")
		}
	})
	t.Run("confirmed log search", func(t *testing.T) {
		m := press(t, demoModel(t, 160, 48), "enter", "l", "/", "w", "s", "enter")
		if m.dg.search.Value() != "ws" || m.dg.searching || m.nav.Depth() != 1 {
			t.Fatalf("search confirmed inside the detail, got %q", m.dg.search.Value())
		}
		m = press(t, m, "esc")
		if m.dg.search.Value() != "" || m.nav.Depth() != 1 {
			t.Fatal("esc clears the confirmed search first")
		}
		m = press(t, m, "esc")
		if m.nav.Depth() != 0 || m.inDetail() {
			t.Fatal("next esc pops the detail")
		}
	})
	t.Run("filter applied after entering", func(t *testing.T) {
		m := press(t, navNamespace(t, demoModel(t, 160, 48)), "/", "o", "enter")
		m = press(t, m, "esc")
		if m.filter.Value() != "" || m.nav.Depth() != 1 || m.nsFilter != "kube-system" {
			t.Fatal("esc clears the filter applied inside the route first")
		}
		m = press(t, m, "esc")
		if m.nav.Depth() != 0 || m.nsFilter != "" {
			t.Fatal("next esc pops the namespace scope")
		}
	})
	t.Run("root never quits", func(t *testing.T) {
		m := press(t, demoModel(t, 160, 48), "/", "o", "enter", "esc")
		if m.filter.Value() != "" {
			t.Fatal("esc at the root clears the filter like before")
		}
		for range 3 {
			next, cmd := m.Update(keyPress("esc"))
			m = next.(Model)
			if cmd != nil {
				if _, quit := cmd().(tea.QuitMsg); quit {
					t.Fatal("esc must never quit")
				}
			}
		}
		if m.nav.Depth() != 0 || m.nav.Active().Kind != RouteScreen {
			t.Fatal("root without history stays put")
		}
	})
}

func TestNamespacePodRoundTripRestoresState(t *testing.T) {
	m := navFocusNamespaces(t, press(t, demoModel(t, 160, 48), "/", "e", "enter"))
	root := m.frame()
	m = press(t, m, "enter")
	if navCrumbs(m) != "Dashboard > kube-system" || m.filter.Value() != "e" {
		t.Fatalf("namespace scope keeps the filter, crumbs %q filter %q", navCrumbs(m), m.filter.Value())
	}
	m = press(t, m, "tab", "tab", "j")
	scope := m.frame()
	m = press(t, m, "enter")
	pod := m.nav.Active().Pod
	if !m.inDetail() || pod != scope.Selected || navCrumbs(m) != "Dashboard > kube-system > "+strings.SplitN(pod, "/", 2)[1] {
		t.Fatalf("enter opens the selected pod, crumbs %q", navCrumbs(m))
	}
	m = press(t, m, "l", "pgup")
	m = press(t, m, "esc")
	if got := m.frame(); got.Route != scope.Route || got.Selected != scope.Selected || got.Focus != scope.Focus || got.PodOffset != scope.PodOffset || got.Tab != scope.Tab || got.NSFilter != "kube-system" || got.Filter != "e" {
		t.Fatalf("first esc restores the namespace scope:\n got %+v\nwant %+v", got, scope)
	}
	m = press(t, m, "esc")
	if got := m.frame(); got.Route != root.Route || got.Selected != root.Selected || got.Focus != root.Focus || got.NSCursor != root.NSCursor || got.NSFilter != "" || got.Filter != "e" || got.Tab != root.Tab {
		t.Fatalf("second esc restores the dashboard:\n got %+v\nwant %+v", got, root)
	}
	if navCrumbs(m) != "Dashboard" || m.nav.Depth() != 0 {
		t.Fatalf("back at the root, crumbs %q", navCrumbs(m))
	}
}

func TestDetailRestoresScrollOnReturn(t *testing.T) {
	m := press(t, demoModel(t, 160, 48), "tab", "S", "pgdn", "shift+tab")
	scroll := m.scroll
	if scroll == 0 {
		t.Fatal("security tab must scroll for this check")
	}
	m = press(t, m, "enter", "d", "esc")
	if m.tab != ui.TabSecurity || m.scroll != scroll {
		t.Fatalf("return restores tab and scroll, got tab %v scroll %d want %d", m.tab, m.scroll, scroll)
	}
}

func TestMenuByKeyboard(t *testing.T) {
	m := navNamespace(t, demoModel(t, 160, 48))
	focus, sel := m.focus, m.selName
	m = press(t, m, "m")
	if !m.menu.open || !strings.Contains(m.Render().Plain(), "enter open") {
		t.Fatal("m focuses the menu and shows its help")
	}
	for _, k := range []string{"right", "end", "left", "home", "j", "q", "l", "/"} {
		m = press(t, m, k)
		if !m.menu.open || m.menu.cursor != 0 || m.selName != sel || m.tab != ui.TabTop || m.filtering {
			t.Fatalf("menu consumes %q without touching the content", k)
		}
	}
	m = press(t, m, "esc")
	for _, closer := range []string{"m", "esc", "tab", "shift+tab"} {
		m = press(t, m, "m", closer)
		if m.menu.open || m.nav.Active().Kind != RouteNamespace || m.focus != focus {
			t.Fatalf("%q closes the menu, keeps the screen and the previous focus", closer)
		}
	}
	m = press(t, m, "m", "enter")
	if m.menu.open || m.nav.Active() != (Route{Kind: RouteScreen, Screen: ScreenDashboard}) || m.nav.Depth() != 2 || navCrumbs(m) != "Dashboard" || m.nsFilter != "" {
		t.Fatalf("enter activates Dashboard, pushes the return and restarts the crumbs, got %+v depth %d %q", m.nav.Active(), m.nav.Depth(), navCrumbs(m))
	}
	m = press(t, m, "esc")
	if m.nav.Active().Kind != RouteNamespace || m.nsFilter != "kube-system" {
		t.Fatal("esc returns to the previous screen")
	}
	m = press(t, m, "2")
	if m.nav.Active().Kind != RouteScreen || m.nav.Depth() != 2 {
		t.Fatal("2 activates Dashboard")
	}
	if again := press(t, m, "2"); again.nav.Depth() != 2 {
		t.Fatal("activating the same screen does not push")
	}
	if menu := press(t, m, "esc", "m", "2"); menu.menu.open || menu.nav.Active().Kind != RouteScreen {
		t.Fatal("2 works inside the menu")
	}
}

func TestNumbersStayTextInInputs(t *testing.T) {
	m := press(t, navNamespace(t, demoModel(t, 160, 48)), "/", "2", "m", "3")
	if m.filter.Value() != "2m3" || m.nav.Active().Kind != RouteNamespace || m.menu.open {
		t.Fatalf("navigation keys are text inside the filter, got %q", m.filter.Value())
	}
	s := press(t, demoModel(t, 160, 48), "enter", "l", "/", "2", "m")
	if s.dg.search.Value() != "2m" || !s.inDetail() || s.menu.open {
		t.Fatalf("navigation keys are text inside the log search, got %q", s.dg.search.Value())
	}
	if one := press(t, demoModel(t, 160, 48), "enter", "1"); one.layout.ShowLeft || one.leftMode != LeftAuto {
		t.Fatal("1 has no function outside the Dashboard")
	}
	if one := press(t, demoModel(t, 160, 48), "1"); one.layout.ShowLeft {
		t.Fatal("1 still toggles the left column on the Dashboard")
	}
}

func TestMenuAndCrumbsByMouse(t *testing.T) {
	m := navNamespace(t, demoModel(t, 160, 48))
	m = press(t, m, "tab", "tab", "enter")
	root := m.nav.Frames(0)
	span := ui.MenuSpans(m.menuItems())[0]
	click := func(m Model, x, y int) Model {
		next, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		return next.(Model)
	}
	for _, msg := range []tea.Msg{
		tea.MouseMotionMsg{X: m.layout.Menu.X + span.X + 1, Y: m.layout.Menu.Y},
		tea.MouseWheelMsg{X: m.layout.Menu.X + span.X + 1, Y: m.layout.Menu.Y, Button: tea.MouseWheelDown},
	} {
		next, _ := m.Update(msg)
		if next.(Model).nav != m.nav {
			t.Fatalf("%T over the menu must not navigate", msg)
		}
	}
	labels := crumbLabels(m.crumbs())
	spans := ui.CrumbSpans(labels, m.layout.Crumbs.W)
	if same := click(m, m.layout.Crumbs.X+spans[2].X, m.layout.Crumbs.Y); same.nav != m.nav {
		t.Fatal("clicking the current crumb does nothing")
	}
	scope := click(m, m.layout.Crumbs.X+spans[1].X+1, m.layout.Crumbs.Y)
	if scope.nav.Active().Kind != RouteNamespace || scope.nav.Depth() != 1 {
		t.Fatalf("clicking the namespace crumb pops to it, got %+v", scope.nav.Active())
	}
	back := click(m, m.layout.Crumbs.X+spans[0].X, m.layout.Crumbs.Y)
	if back.nav.Depth() != 0 || back.frame().Route != root.Route || back.selName != root.Selected || back.nsFilter != "" {
		t.Fatal("clicking the root crumb pops every frame and restores the dashboard")
	}
	dash := click(m, m.layout.Menu.X+span.X+1, m.layout.Menu.Y)
	if dash.nav.Active() != (Route{Kind: RouteScreen, Screen: ScreenDashboard}) || dash.nav.Depth() != 3 {
		t.Fatal("clicking a menu title activates the screen")
	}
	if gap := click(m, m.layout.Menu.X+span.X+span.W+1, m.layout.Menu.Y); gap.nav != m.nav {
		t.Fatal("clicking outside the menu titles does nothing")
	}
	open := press(t, m, "m")
	if closed := click(open, 10, 20); closed.menu.open || closed.nav != open.nav {
		t.Fatal("clicking the body closes the menu without other effects")
	}
}

func (r router) Frames(i int) Frame {
	f, _ := r.Frame(i)
	return f
}

func TestResizeKeepsStack(t *testing.T) {
	m := navNamespace(t, demoModel(t, 160, 48))
	m = press(t, m, "tab", "tab", "enter")
	nav, crumbs := m.nav, navCrumbs(m)
	for _, size := range [][2]int{{130, 40}, {100, 30}, {200, 60}, {160, 48}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(Model)
		if m.nav != nav || navCrumbs(m) != crumbs {
			t.Fatalf("resize to %v must not touch the stack", size)
		}
	}
	if m.layout.Details != m.layout.Body || !m.inDetail() {
		t.Fatal("detail keeps the full body after resizes")
	}
}

type navSource struct {
	*data.Demo
	mu      sync.Mutex
	gone    string
	open    map[context.Context]context.CancelFunc
	current context.Context
	wide    int
	calls   int
}

func newNavSource() *navSource {
	return &navSource{Demo: data.NewDemo(), open: map[context.Context]context.CancelFunc{}}
}

func (s *navSource) Snapshot() data.Snapshot {
	snap := s.Demo.Snapshot()
	s.mu.Lock()
	gone := s.gone
	s.mu.Unlock()
	if gone == "" {
		return snap
	}
	pods := snap.Pods[:0:0]
	for _, p := range snap.Pods {
		if p.Key() != gone {
			pods = append(pods, p)
		}
	}
	snap.Pods = pods
	return snap
}

func (s *navSource) Metrics(tick int, pod string, cols int) data.Metrics {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.Demo.Metrics(tick, pod, cols)
}

func (s *navSource) PodLogs(req data.LogRequest, tick, n int) data.LogView {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.current != nil {
		s.open[s.current]()
		delete(s.open, s.current)
		s.current = nil
	}
	if req.Pod == "" {
		return data.LogView{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.open[ctx], s.current = cancel, ctx
	return s.Demo.PodLogs(req, tick, n)
}

func (s *navSource) ScopedEvents(q data.EventQuery) data.EventFeed {
	s.mu.Lock()
	if q.Scope == data.ScopeCluster {
		s.wide++
	}
	s.mu.Unlock()
	return s.Demo.ScopedEvents(q)
}

func (s *navSource) subscriptions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.open)
}

func navStart(t *testing.T, src *navSource) Model {
	t.Helper()
	m := New(Options{Source: src, NoAnim: true, InitialPod: 1})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	return next.(Model)
}

func TestPodDetailKeepsMissingPod(t *testing.T) {
	src := newNavSource()
	m := press(t, navStart(t, src), "enter", "l")
	target := m.nav.Active().Pod
	if src.subscriptions() != 1 {
		t.Fatal("detail streams the pod logs")
	}
	src.mu.Lock()
	src.gone = target
	src.mu.Unlock()
	next, _ := m.Update(dataMsg{})
	m = next.(Model)
	if !m.inDetail() || m.nav.Active().Pod != target || m.selName != target || m.selectedPod() != nil {
		t.Fatalf("a vanished pod keeps the detail on the same target, got %+v sel %s", m.nav.Active(), m.selName)
	}
	plain := m.Render().Plain()
	if !strings.Contains(plain, "recurso não observado neste escopo: "+target) || strings.Contains(plain, "Log   Config") {
		t.Fatal("detail explains the missing resource instead of showing another pod")
	}
	if src.subscriptions() != 0 || len(m.logs) != 0 {
		t.Fatal("missing pod releases its log subscription")
	}
	m = press(t, m, "esc")
	if m.inDetail() || m.selName == target || m.selectedPod() == nil {
		t.Fatal("back on the dashboard the list keeps a visible neighbour selected")
	}
}

func TestNavigationCyclesDoNotAccumulateConsumers(t *testing.T) {
	src := newNavSource()
	m := press(t, navStart(t, src), "l")
	baseline := runtime.NumGoroutine()
	for i := range 100 {
		m = press(t, m, "enter", "e", "l", "esc")
		if i%10 == 0 {
			m = navNamespace(t, m)
			m = press(t, m, "tab", "tab", "enter", "l", "esc", "esc", "esc", "tab", "tab")
		}
		if open := src.subscriptions(); open > 1 {
			t.Fatalf("cycle %d holds %d log subscriptions", i, open)
		}
	}
	if m.nav.Depth() != 0 || m.inDetail() {
		t.Fatalf("cycles must return to the root, depth %d", m.nav.Depth())
	}
	if src.wide != 0 {
		t.Fatalf("navigation must not start cluster-wide event reads, got %d", src.wide)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline+2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > baseline+2 {
		t.Fatalf("goroutines grew from %d to %d", baseline, n)
	}
}

func (s *navSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestWheelIsConsumedByOpenMenu(t *testing.T) {
	src := newNavSource()
	m := press(t, navStart(t, src), "tab", "S", "shift+tab", "m")
	if !m.menu.open {
		t.Fatal("menu must be open")
	}
	sel, scroll, offset, calls := m.selName, m.scroll, m.podOffset, src.callCount()
	targets := [][2]int{
		{m.layout.Pods.X + 5, m.layout.Pods.Y + 1 + ui.PodsRowsTop + 2},
		{m.layout.Details.X + 5, m.layout.Details.Y + 10},
		{m.layout.Namespaces.X + 3, m.layout.Namespaces.Y + 3},
	}
	for _, p := range targets {
		for _, button := range []tea.MouseButton{tea.MouseWheelDown, tea.MouseWheelUp, tea.MouseWheelDown} {
			next, _ := m.Update(tea.MouseWheelMsg{X: p[0], Y: p[1], Button: button})
			m = next.(Model)
		}
	}
	if !m.menu.open || m.selName != sel || m.scroll != scroll || m.podOffset != offset || m.nsCursor != 0 {
		t.Fatalf("wheel with the menu open must not reach the body: sel %s scroll %d", m.selName, m.scroll)
	}
	if got := src.callCount(); got != calls {
		t.Fatalf("wheel with the menu open must not query the source, calls %d -> %d", calls, got)
	}
	closed := press(t, m, "esc")
	next, _ := closed.Update(tea.MouseWheelMsg{X: targets[0][0], Y: targets[0][1], Button: tea.MouseWheelDown})
	if next.(Model).selName == sel {
		t.Fatal("wheel works again once the menu is closed")
	}
}

func navClick(m Model, x, y int) Model {
	next, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return next.(Model)
}

func navCrumbX(m Model, i int) int {
	return m.layout.Crumbs.X + ui.CrumbSpans(crumbLabels(m.crumbs()), m.layout.Crumbs.W)[i].X
}

func navMenuX(m Model) int {
	return m.layout.Menu.X + ui.MenuSpans(m.menuItems())[0].X + 1
}

func navAssertKeysWork(t *testing.T, m Model) {
	t.Helper()
	if m.filtering || m.dg.searching || m.filter.Focused() || m.dg.search.Focused() {
		t.Fatalf("navigation must end the edition, filtering %v searching %v", m.filtering, m.dg.searching)
	}
	if !press(t, m, "m").menu.open {
		t.Fatal("m must open the menu after the navigation")
	}
	scoped := navNamespace(t, m)
	if back := press(t, scoped, "2"); back.nav.Active().Kind != RouteScreen || back.nav.Depth() != scoped.nav.Depth()+1 {
		t.Fatal("2 must navigate after the navigation")
	}
	_, cmd := m.Update(keyPress("q"))
	if cmd == nil {
		t.Fatal("q must quit after the navigation")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q must quit after the navigation")
	}
}

func TestNavigationEndsActiveInputs(t *testing.T) {
	searching := func(t *testing.T) Model {
		m := press(t, navNamespace(t, demoModel(t, 160, 48)), "tab", "tab", "enter", "l", "/", "w", "s")
		if !m.dg.searching || m.dg.search.Value() != "ws" {
			t.Fatal("log search must be in edition")
		}
		return m
	}
	filtering := func(t *testing.T) Model {
		m := press(t, navNamespace(t, demoModel(t, 160, 48)), "/", "x", "m", "2")
		if !m.filtering || m.filter.Value() != "xm2" || m.menu.open || m.nav.Depth() != 1 {
			t.Fatal("keyboard navigation keys are text while the filter is in edition")
		}
		return m
	}
	t.Run("search then root crumb", func(t *testing.T) {
		m := navClick(searching(t), navCrumbX(searching(t), 0), searching(t).layout.Crumbs.Y)
		if m.nav.Depth() != 0 || m.tab != ui.TabTop || m.focus != FocusNamespaces {
			t.Fatalf("root crumb restores the dashboard, depth %d tab %v focus %d", m.nav.Depth(), m.tab, m.focus)
		}
		if m.dg.search.Value() != "" {
			t.Fatal("an unconfirmed search belongs to the left target and is discarded")
		}
		navAssertKeysWork(t, m)
	})
	t.Run("search then namespace crumb", func(t *testing.T) {
		s := searching(t)
		m := navClick(s, navCrumbX(s, 1), s.layout.Crumbs.Y)
		if m.nav.Active().Kind != RouteNamespace {
			t.Fatal("namespace crumb pops to the scope")
		}
		navAssertKeysWork(t, m)
	})
	t.Run("search then menu title", func(t *testing.T) {
		s := searching(t)
		m := navClick(s, navMenuX(s), s.layout.Menu.Y)
		if m.nav.Active() != (Route{Kind: RouteScreen, Screen: ScreenDashboard}) {
			t.Fatal("menu title activates the Dashboard")
		}
		navAssertKeysWork(t, m)
	})
	t.Run("filter then root crumb", func(t *testing.T) {
		f := filtering(t)
		m := navClick(f, navCrumbX(f, 0), f.layout.Crumbs.Y)
		if m.nav.Depth() != 0 || m.filter.Value() != "" || m.nsFilter != "" {
			t.Fatalf("root crumb restores the saved filter, got %q", m.filter.Value())
		}
		navAssertKeysWork(t, m)
	})
	t.Run("filter then menu title", func(t *testing.T) {
		f := filtering(t)
		m := navClick(f, navMenuX(f), f.layout.Menu.Y)
		if m.nav.Active() != (Route{Kind: RouteScreen, Screen: ScreenDashboard}) || m.nav.Depth() != 2 {
			t.Fatal("menu title activates the Dashboard")
		}
		if back := press(t, m, "esc"); back.filter.Value() != "xm2" || back.filtering {
			t.Fatal("the typed filter is kept in the saved frame without reopening the edition")
		}
		navAssertKeysWork(t, m)
	})
	t.Run("filter then current crumb", func(t *testing.T) {
		f := filtering(t)
		m := navClick(f, navCrumbX(f, 1), f.layout.Crumbs.Y)
		if m.nav != f.nav || m.filter.Value() != "xm2" {
			t.Fatal("clicking the current crumb keeps the route and the typed filter")
		}
		navAssertKeysWork(t, m)
	})
}

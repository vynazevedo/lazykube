package app

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/ui"
)

type menuState struct {
	open   bool
	cursor int
}

type crumb struct {
	label string
	depth int
}

func (m Model) inDetail() bool {
	return m.nav.Active().Kind == RoutePod
}

func (m Model) frame() Frame {
	return Frame{
		Route:       m.nav.Active(),
		EntryFilter: m.nav.entryFilter,
		Selected:    m.selName,
		Focus:       m.focus,
		Filter:      m.filter.Value(),
		NSFilter:    m.nsFilter,
		PodOffset:   m.podOffset,
		NSCursor:    m.nsCursor,
		DepCursor:   m.depCursor,
		Scroll:      m.scroll,
		Follow:      m.follow,
		Tab:         m.tab,
	}
}

func (m *Model) navigate(to Route) bool {
	if !m.nav.push(m.frame(), to) {
		return false
	}
	m.nav.entryFilter = m.filter.Value()
	switch to.Kind {
	case RouteNamespace:
		m.nsFilter = to.Namespace
	case RouteScreen:
		m.nsFilter = ""
	case RoutePod:
		m.focus = FocusDetails
	}
	m.relayout()
	m.syncSelection()
	return true
}

func (m *Model) restore(f Frame) {
	if f.Selected != m.selName {
		m.selName = f.Selected
		m.newLines, m.logs = 0, nil
		m.resetLogTarget()
	}
	m.focus = f.Focus
	m.filter.SetValue(f.Filter)
	m.nsFilter = f.NSFilter
	m.podOffset, m.nsCursor, m.depCursor = f.PodOffset, f.NSCursor, f.DepCursor
	m.tab, m.scroll, m.follow = f.Tab, f.Scroll, f.Follow
	m.relayout()
	m.fixSelection()
	m.refresh()
}

func (m *Model) back() bool {
	f, ok := m.nav.pop()
	if ok {
		m.restore(f)
	}
	return ok
}

func (m *Model) backTo(depth int) bool {
	f, ok := m.nav.popTo(depth)
	if ok {
		m.restore(f)
	}
	return ok
}

func (m *Model) mainScreen() ScreenID {
	trail := m.nav.trail()
	return trail[m.nav.crumbStart()].Screen
}

func (m *Model) openNamespace(name string) {
	m.navigate(Route{Kind: RouteNamespace, Screen: m.mainScreen(), Namespace: name})
}

func (m *Model) openPod() {
	if p := m.selectedPod(); p != nil {
		m.navigate(Route{Kind: RoutePod, Screen: ScreenPodDetail, Pod: p.Key()})
	}
}

func (m *Model) activateScreen(id ScreenID) {
	for _, s := range registeredScreens() {
		if s == id {
			m.navigate(Route{Kind: RouteScreen, Screen: id})
			return
		}
	}
}

func (m *Model) handleEsc() {
	switch {
	case m.tab == ui.TabLog && m.dg.search.Value() != "":
		m.dg.search.SetValue("")
		m.updateMatches()
	case m.filter.Value() != m.nav.entryFilter:
		m.filter.SetValue(m.nav.entryFilter)
		m.syncSelection()
	case m.back():
	default:
		m.nsFilter = ""
		m.filter.SetValue("")
		m.syncSelection()
	}
}

func (m Model) menuItems() []ui.MenuItem {
	screens := registeredScreens()
	items := make([]ui.MenuItem, len(screens))
	for i, id := range screens {
		s, _ := screenFor(id)
		items[i] = ui.MenuItem{Key: menuKey(id), Title: s.Title()}
	}
	return items
}

func menuKey(id ScreenID) string {
	switch id {
	case ScreenDashboard:
		return "2"
	}
	return ""
}

func (m Model) menuActive() int {
	main := m.mainScreen()
	for i, id := range registeredScreens() {
		if id == main {
			return i
		}
	}
	return -1
}

func (m *Model) openMenu() {
	m.menu.open = true
	m.menu.cursor = max(0, m.menuActive())
}

func (m Model) handleMenuKey(msg tea.KeyPressMsg) Model {
	k := m.keys
	last := len(registeredScreens()) - 1
	switch {
	case key.Matches(msg, k.MenuPrev):
		m.menu.cursor = max(0, m.menu.cursor-1)
	case key.Matches(msg, k.MenuNext):
		m.menu.cursor = min(last, m.menu.cursor+1)
	case key.Matches(msg, k.Home):
		m.menu.cursor = 0
	case key.Matches(msg, k.End):
		m.menu.cursor = last
	case key.Matches(msg, k.Enter):
		m.menu.open = false
		m.activateScreen(registeredScreens()[m.menu.cursor])
	case key.Matches(msg, k.Dashboard):
		m.menu.open = false
		m.activateScreen(ScreenDashboard)
	case key.Matches(msg, k.Esc), key.Matches(msg, k.Menu), key.Matches(msg, k.NextPanel), key.Matches(msg, k.PrevPanel):
		m.menu.open = false
	}
	return m
}

func (m Model) routeKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.menu.open {
		return m.handleMenuKey(msg), nil
	}
	if !m.helpOpen && !m.filtering && !m.dg.searching && !m.layout.TooSmall {
		k := m.keys
		switch {
		case key.Matches(msg, k.Menu):
			m.openMenu()
			return m, nil
		case key.Matches(msg, k.Dashboard):
			m.activateScreen(ScreenDashboard)
			return m, nil
		case key.Matches(msg, k.Esc):
			m.handleEsc()
			return m, nil
		}
	}
	return m.screen().Update(m, msg)
}

func (m Model) crumbs() []crumb {
	trail := m.nav.trail()
	start := m.nav.crumbStart()
	out := make([]crumb, 0, len(trail)-start)
	for i := start; i < len(trail); i++ {
		out = append(out, crumb{label: routeLabel(trail[i]), depth: i})
	}
	return out
}

func routeLabel(r Route) string {
	switch r.Kind {
	case RouteNamespace:
		return r.Namespace
	case RoutePod:
		if _, name, ok := strings.Cut(r.Pod, "/"); ok {
			return name
		}
		return r.Pod
	}
	return r.screen().Title()
}

func crumbLabels(cs []crumb) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.label
	}
	return out
}

func (m Model) handleShellClick(ms tea.Mouse) (Model, bool) {
	if ms.Button != tea.MouseLeft || m.layout.TooSmall || m.helpOpen {
		return m, false
	}
	switch m.layout.Hit(ms.X, ms.Y) {
	case AreaMenu:
		m.menu.open = false
		if i, ok := ui.MenuItemAt(m.menuItems(), ms.X-m.layout.Menu.X); ok {
			m.activateScreen(registeredScreens()[i])
		}
		return m, true
	case AreaCrumbs:
		m.menu.open = false
		cs := m.crumbs()
		if i, ok := ui.CrumbAt(crumbLabels(cs), ms.X-m.layout.Crumbs.X, m.layout.Crumbs.W); ok && cs[i].depth < m.nav.Depth() {
			m.backTo(cs[i].depth)
		}
		return m, true
	}
	if m.menu.open {
		m.menu.open = false
		return m, true
	}
	return m, false
}

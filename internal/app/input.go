package app

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := m.keys
	if m.helpOpen {
		switch {
		case key.Matches(msg, k.Help), key.Matches(msg, k.Esc):
			m.helpOpen = false
		case key.Matches(msg, k.Quit):
			return m, tea.Quit
		}
		return m, nil
	}
	if m.filtering {
		return m.handleFilterKey(msg)
	}
	if m.dg.searching {
		return m.handleSearchKey(msg)
	}

	switch {
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case key.Matches(msg, k.Help):
		m.helpOpen = true
	case key.Matches(msg, k.NextPanel) && !m.inDetail():
		m.cycleFocus(1)
	case key.Matches(msg, k.PrevPanel) && !m.inDetail():
		m.cycleFocus(-1)
	case key.Matches(msg, k.Filter) && m.searchActive():
		cmd := m.startSearch()
		return m, cmd
	case key.Matches(msg, k.Filter) && !m.inDetail():
		m.focus = FocusPods
		m.filtering = true
		cmd := m.filter.Focus()
		return m, cmd
	case key.Matches(msg, k.Enter) && !m.inDetail():
		switch {
		case m.focus == FocusNamespaces && m.nsCursor < len(m.snap.Namespaces):
			m.openNamespace(m.snap.Namespaces[m.nsCursor].Name)
		case m.focus == FocusPods:
			m.openPod()
		case m.focus == FocusDeployments && m.onScreen(ScreenWorkloads):
			m.openDeployment()
		}
	case key.Matches(msg, k.TabLog):
		m.setTab(ui.TabLog)
	case key.Matches(msg, k.TabTop):
		m.setTab(ui.TabTop)
	case key.Matches(msg, k.TabDescribe):
		m.setTab(ui.TabDescribe)
	case key.Matches(msg, k.TabSecurity):
		if m.tab == ui.TabSecurity {
			m.securityNamespace = !m.securityNamespace
		}
		m.setTab(ui.TabSecurity)
		m.scroll = 0
		m.refreshSecurity()
	case key.Matches(msg, k.SecurityAck):
		m.acknowledgeSecurity()
	case key.Matches(msg, k.TabEvents):
		m.setTab(ui.TabEvents)
	case key.Matches(msg, k.TabPrev):
		m.setTab(m.tab.Next(-1))
	case key.Matches(msg, k.TabNext):
		m.setTab(m.tab.Next(1))
	case key.Matches(msg, k.Container):
		m.cycleContainer()
	case key.Matches(msg, k.Previous):
		m.togglePrevious()
	case key.Matches(msg, k.SearchNext) && m.tab == ui.TabLog:
		m.stepMatch(1)
	case key.Matches(msg, k.SearchPrev) && m.tab == ui.TabLog:
		m.stepMatch(-1)
	case key.Matches(msg, k.EventScope):
		m.cycleScope()
	case key.Matches(msg, k.Palette):
		m.pal = theme.NextPalette(m.pal)
	case key.Matches(msg, k.ToggleLeft) && m.onScreen(ScreenDashboard):
		if LeftVisible(m.layout.ContentW, m.leftMode) {
			m.leftMode = LeftHidden
		} else {
			m.leftMode = LeftShown
		}
		m.setLayout(m.w, m.h)
		m.refresh()
	case key.Matches(msg, k.Pause):
		cmd := m.togglePause()
		return m, cmd
	case key.Matches(msg, k.Follow):
		m.follow = !m.follow
		if m.follow {
			m.scrollTo(m.maxScroll())
		}
	case key.Matches(msg, k.Wrap):
		m.wrap = !m.wrap
		m.clampScroll()
	case key.Matches(msg, k.Bottom), key.Matches(msg, k.End):
		m.scrollTo(m.maxScroll())
	case key.Matches(msg, k.Top), key.Matches(msg, k.Home):
		m.scrollTo(0)
	case key.Matches(msg, k.PageUp):
		m.scrollTo(m.scroll - m.pageSize())
	case key.Matches(msg, k.PageDown):
		m.scrollTo(m.scroll + m.pageSize())
	case key.Matches(msg, k.Up):
		m.moveSelection(-1)
	case key.Matches(msg, k.Down):
		m.moveSelection(1)
	}
	return m, nil
}

func (m Model) handleFilterKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter.Blur()
		m.filter.SetValue("")
		m.syncSelection()
		return m, nil
	case "enter":
		m.filtering = false
		m.filter.Blur()
		return m, nil
	case "up":
		m.moveSelection(-1)
		return m, nil
	case "down":
		m.moveSelection(1)
		return m, nil
	}
	var cmd tea.Cmd
	before := m.filter.Value()
	m.filter, cmd = m.filter.Update(msg)
	if m.filter.Value() != before {
		m.syncSelection()
	}
	return m, cmd
}

func (m *Model) moveSelection(delta int) {
	switch m.focus {
	case FocusNamespaces:
		m.nsCursor = max(0, min(len(m.snap.Namespaces)-1, m.nsCursor+delta))
	case FocusDeployments:
		m.depCursor = max(0, min(len(m.visibleDeployments())-1, m.depCursor+delta))
	case FocusDetails:
		m.scrollTo(m.scroll + delta)
	default:
		m.selectPod(m.selectedIndex(m.visiblePods()) + delta)
	}
}

func (m Model) podRowAt(y int) (int, bool) {
	top := m.layout.Pods.Y + 1 + ui.PodsRowsTop
	row := y - top
	if row < 0 || row >= ui.PodsVisibleRows(m.layout.Pods.H) {
		return 0, false
	}
	idx := m.podOffset + row
	if idx >= len(m.visiblePods()) {
		return 0, false
	}
	return idx, true
}

func (m Model) tabAt(x, y int) (ui.Tab, bool) {
	d := m.layout.Details
	if d.Empty() || y != d.Y+1+ui.DetailsTabsRow {
		return 0, false
	}
	return ui.TabAt(x-(d.X+2), len(m.securitySession.Alerts))
}

func (m Model) handleClick(ms tea.Mouse) (Model, tea.Cmd) {
	if ms.Button != tea.MouseLeft || m.layout.TooSmall {
		return m, nil
	}
	if m.helpOpen {
		m.helpOpen = false
		return m, nil
	}
	switch m.layout.Hit(ms.X, ms.Y) {
	case AreaNamespaces:
		m.focus = FocusNamespaces
		top, rows := ui.NamespacesRows(m.layout.Namespaces.H)
		if idx, ok := ui.ListRowAt(ms.Y-m.layout.Namespaces.Y, top, rows, m.nsCursor, len(m.snap.Namespaces)); ok {
			m.nsCursor = idx
		}
	case AreaDeployments:
		m.focus = FocusDeployments
		top, rows := ui.DeploymentsRows(m.layout.Deployments.H)
		if idx, ok := ui.ListRowAt(ms.Y-m.layout.Deployments.Y, top, rows, m.depCursor, len(m.visibleDeployments())); ok {
			m.depCursor = idx
		}
	case AreaPods:
		m.focus = FocusPods
		if idx, ok := m.podRowAt(ms.Y); ok {
			m.selectPod(idx)
		}
	case AreaDetails:
		m.focus = FocusDetails
		d := m.layout.Details
		if t, ok := m.tabAt(ms.X, ms.Y); ok {
			m.setTab(t)
		} else if m.tab == ui.TabLog && ui.LogNextHit(ms.X-d.X-m.slideOffset(), ms.Y-d.Y, d.W, d.H, m.logInfo(), ui.Ctx{Pal: m.pal, Clock: m.clock}) {
			m.stepMatch(1)
		}
	}
	return m, nil
}

func (m Model) handleWheel(ms tea.Mouse) (Model, tea.Cmd) {
	delta := 0
	switch ms.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return m, nil
	}
	switch m.layout.Hit(ms.X, ms.Y) {
	case AreaPods:
		m.selectPod(m.selectedIndex(m.visiblePods()) + delta)
	case AreaNamespaces:
		m.nsCursor = max(0, min(len(m.snap.Namespaces)-1, m.nsCursor+delta))
	case AreaDeployments:
		m.depCursor = max(0, min(len(m.visibleDeployments())-1, m.depCursor+delta))
	case AreaDetails:
		m.scrollTo(m.scroll + delta*3)
	}
	return m, nil
}

func (m *Model) handleMotion(ms tea.Mouse) {
	m.hover = -1
	m.hoverTab = -1
	if idx, ok := m.podRowAt(ms.Y); ok && m.layout.Hit(ms.X, ms.Y) == AreaPods {
		m.hover = idx
	}
	if t, ok := m.tabAt(ms.X, ms.Y); ok {
		m.hoverTab = int(t)
	}
}

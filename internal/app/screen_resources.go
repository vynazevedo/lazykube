package app

import (
	"math"
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type resourcesScreen struct{}

type overviewScreen struct{}

func tableKeys(m Model) []key.Binding {
	k := m.keys
	return []key.Binding{
		k.Up, k.Down, k.PageUp, k.PageDown, k.Home, k.End, k.Enter, k.Esc, k.Refresh,
		k.SecurityAck, k.Palette, k.Pause, k.Help, k.Quit,
		k.Menu, k.MenuPrev, k.MenuNext, k.Dashboard, k.Workloads, k.Nodes, k.Events,
		k.Context, k.Namespace, k.Tree,
	}
}

func screenKey(s Screen, m Model, msg tea.KeyPressMsg) bool {
	return slices.ContainsFunc(s.Keys(m), func(b key.Binding) bool { return key.Matches(msg, b) })
}

func (resourcesScreen) ID() ScreenID       { return ScreenResources }
func (resourcesScreen) Title() string      { return "Workloads" }
func (resourcesScreen) ShowsDetails() bool { return false }
func (resourcesScreen) Init(Model) tea.Cmd { return nil }

func (s resourcesScreen) Keys(m Model) []key.Binding { return tableKeys(m) }

func (resourcesScreen) Help(Model) []ui.KeyHelp { return ui.ResourceHelp() }

func (resourcesScreen) FullHelp(Model) []ui.KeyHelp { return ui.FullHelpResources() }

func (s resourcesScreen) Update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.helpOpen {
			return updateBody(m, msg)
		}
		if !screenKey(s, m, msg) {
			return m, nil
		}
		return m.handleTableKey(msg)
	case tea.MouseClickMsg:
		if !m.helpOpen && msg.Button == tea.MouseLeft && m.layout.Table.Contains(msg.X, msg.Y) {
			row := msg.Y - (m.layout.Table.Y + 1 + ui.ResourceTableTop)
			st := m.tableState()
			items := len(st.Page.Items)
			rows := ui.ResourceTableRows(m.layout.Table.H)
			if row >= 0 && row < rows {
				if idx := ui.ScrollOffset(st.Cursor, items, rows) + row; idx < items {
					m.res.cursor = st.Offset + idx
				}
			}
		}
		return m, nil
	case tea.MouseWheelMsg:
		if m.layout.Table.Contains(msg.X, msg.Y) {
			m.moveTable(map[tea.MouseButton]int{tea.MouseWheelUp: -1, tea.MouseWheelDown: 1}[msg.Button])
		}
		return m, nil
	case tea.MouseMotionMsg:
		return m, nil
	}
	return updateBody(m, msg)
}

func (resourcesScreen) View(m Model, body draw.Region, ctx ui.Ctx) {
	ui.ResourceTable(region(body, m.layout.Table), m.tableState(), ctx)
}

func (m Model) scopeLabel() string {
	if ns := m.resourceNamespace(); ns != "" {
		return "namespace " + ns
	}
	return "todos os namespaces"
}

func (m Model) leafTable(w workloadLeaf) ui.ResourceTableState {
	_, supported := m.browser()
	st := ui.ResourceTableState{Label: w.label, Kind: w.kind, Supported: supported, Scope: m.scopeLabel(), Cursor: m.res.cursor, AllNamespaces: m.resourceNamespace() == ""}
	if !supported {
		return st
	}
	if m.res.catalog == nil {
		st.CatalogLoading = true
		return st
	}
	st.Availability = m.res.catalog.Lookup(w.gvr).Availability
	if s, ok := m.res.subs[w.gvr]; ok {
		st.Loading, st.Err, st.Page, st.Offset, st.Restarted = s.loading, s.err, s.page, s.offset, s.restarted
		st.Cursor = m.res.cursor - s.offset
	} else {
		st.Loading = true
	}
	return st
}

func (m Model) tableState() ui.ResourceTableState {
	w, _ := workloadLeafFor(m.nav.Active().Dest)
	return m.leafTable(w)
}

func (m *Model) moveTable(delta int) {
	m.seekTable(m.res.cursor + delta)
}

func (m Model) handleTableKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := m.keys
	page := max(1, ui.ResourceTableRows(m.layout.Table.H))
	switch {
	case key.Matches(msg, k.Up):
		m.moveTable(-1)
	case key.Matches(msg, k.Down):
		m.moveTable(1)
	case key.Matches(msg, k.PageUp):
		m.moveTable(-page)
	case key.Matches(msg, k.PageDown):
		m.moveTable(page)
	case key.Matches(msg, k.Home):
		m.seekTable(0)
	case key.Matches(msg, k.End):
		m.seekTable(math.MaxInt)
	case key.Matches(msg, k.Refresh):
		cmd := m.refreshResources()
		return m, cmd
	case key.Matches(msg, k.Enter):
	default:
		return updateBody(m, msg)
	}
	return m, nil
}

func (overviewScreen) ID() ScreenID       { return ScreenOverview }
func (overviewScreen) Title() string      { return "Workloads" }
func (overviewScreen) ShowsDetails() bool { return false }
func (overviewScreen) Init(Model) tea.Cmd { return nil }

func (overviewScreen) Keys(m Model) []key.Binding { return tableKeys(m) }

func (overviewScreen) Help(Model) []ui.KeyHelp { return ui.OverviewHelp() }

func (overviewScreen) FullHelp(Model) []ui.KeyHelp { return ui.FullHelpOverview() }

func (m Model) overviewState() ui.OverviewState {
	st := ui.OverviewState{Cursor: m.res.card, Scope: m.scopeLabel()}
	for _, w := range workloadLeaves {
		st.Cards = append(st.Cards, ui.OverviewCard{Label: w.label, Table: m.leafTable(w)})
	}
	return st
}

func (overviewScreen) View(m Model, body draw.Region, ctx ui.Ctx) {
	ui.WorkloadOverview(region(body, m.layout.Table), m.overviewState(), ctx)
}

func (s overviewScreen) Update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.helpOpen {
			return updateBody(m, msg)
		}
		if !screenKey(s, m, msg) {
			return m, nil
		}
		k := m.keys
		cols := ui.OverviewColumns(m.layout.Table.W)
		last := len(workloadLeaves) - 1
		switch {
		case key.Matches(msg, k.Up):
			m.res.card = max(0, m.res.card-cols)
		case key.Matches(msg, k.Down):
			m.res.card = min(last, m.res.card+cols)
		case key.Matches(msg, k.MenuPrev):
			m.res.card = max(0, m.res.card-1)
		case key.Matches(msg, k.MenuNext):
			m.res.card = min(last, m.res.card+1)
		case key.Matches(msg, k.Home):
			m.res.card = 0
		case key.Matches(msg, k.End):
			m.res.card = last
		case key.Matches(msg, k.Refresh):
			cmd := m.refreshResources()
			return m, cmd
		case key.Matches(msg, k.Enter):
			m.activateDestination(workloadLeaves[m.res.card].dest)
		default:
			return updateBody(m, msg)
		}
		return m, nil
	case tea.MouseClickMsg:
		t := m.layout.Table
		if !m.helpOpen && msg.Button == tea.MouseLeft && t.Contains(msg.X, msg.Y) {
			if idx, ok := ui.OverviewCardAt(t.W, t.H, len(workloadLeaves), m.res.card, msg.X-t.X, msg.Y-t.Y); ok {
				m.res.card = idx
				m.activateDestination(workloadLeaves[idx].dest)
			}
		}
		return m, nil
	case tea.MouseWheelMsg:
		if m.layout.Table.Contains(msg.X, msg.Y) {
			step := ui.OverviewColumns(m.layout.Table.W) * map[tea.MouseButton]int{tea.MouseWheelUp: -1, tea.MouseWheelDown: 1}[msg.Button]
			m.res.card = max(0, min(len(workloadLeaves)-1, m.res.card+step))
		}
		return m, nil
	case tea.MouseMotionMsg:
		return m, nil
	}
	return updateBody(m, msg)
}

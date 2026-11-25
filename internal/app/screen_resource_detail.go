package app

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type resourceDetailScreen struct{}

func (resourceDetailScreen) ID() ScreenID       { return ScreenResourceDetail }
func (resourceDetailScreen) Title() string      { return "Detalhe" }
func (resourceDetailScreen) ShowsDetails() bool { return false }
func (resourceDetailScreen) Init(Model) tea.Cmd { return nil }

func (resourceDetailScreen) Keys(m Model) []key.Binding {
	k := m.keys
	return []key.Binding{
		k.Up, k.Down, k.PageUp, k.PageDown, k.Home, k.End, k.Esc, k.Refresh, k.NextURL, k.Yank, k.Actions,
		k.SecurityAck, k.Palette, k.Pause, k.Help, k.Quit,
		k.Menu, k.MenuPrev, k.MenuNext, k.Dashboard, k.Workloads, k.Nodes, k.Security, k.Events, k.Radar,
		k.Context, k.Namespace, k.Tree,
	}
}

func (resourceDetailScreen) Help(m Model) []ui.KeyHelp {
	return m.actionHelp(ui.ResourceDetailHelp(), "actions")
}

func (resourceDetailScreen) FullHelp(m Model) []ui.KeyHelp {
	return m.actionHelp(ui.FullHelpResourceDetail(), "actions on the object")
}

func (m Model) resourceDetailState() ui.ResourceDetailState {
	w, ref, ok := m.activeResource()
	if !ok {
		return ui.ResourceDetailState{}
	}
	st := ui.ResourceDetailState{Kind: w.label, Net: w.net, Workload: w.net == "", Scroll: m.acc.scroll, Access: m.accessView()}
	row, found := m.openedRow(w, ref)
	if found {
		st.Row = &row
	} else {
		st.Row = &data.ResourceRow{Ref: data.ResourceMetadataRef{Namespace: ref.Namespace, Name: ref.Name}}
	}
	if s, ok := m.res.subs[w.gvr]; ok {
		st.Coverage, st.ObservedAt, st.Synthetic = s.page.Coverage, s.page.ObservedAt, s.page.Synthetic
	}
	if m.snap.Synthetic {
		st.Synthetic = true
	}
	return st
}

func (s resourceDetailScreen) Update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.helpOpen {
			return updateBody(m, msg)
		}
		if !screenKey(s, m, msg) {
			return m, nil
		}
		return m.handleDetailKey(msg)
	case tea.MouseWheelMsg:
		if m.layout.Table.Contains(msg.X, msg.Y) {
			m.acc.scroll += map[tea.MouseButton]int{tea.MouseWheelUp: -3, tea.MouseWheelDown: 3}[msg.Button]
			m.clampAccess()
		}
		return m, nil
	case tea.MouseClickMsg, tea.MouseMotionMsg:
		return m, nil
	}
	return updateBody(m, msg)
}

func (m Model) handleDetailKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := m.keys
	page := max(1, ui.ResourceDetailRows(m.layout.Table.H)-1)
	switch {
	case key.Matches(msg, k.Up):
		m.acc.scroll--
	case key.Matches(msg, k.Down):
		m.acc.scroll++
	case key.Matches(msg, k.PageUp):
		m.acc.scroll -= page
	case key.Matches(msg, k.PageDown):
		m.acc.scroll += page
	case key.Matches(msg, k.Home):
		m.acc.scroll = 0
	case key.Matches(msg, k.End):
		m.acc.scroll = len(m.resourceDetailState().Rows())
	case key.Matches(msg, k.NextURL):
		return m.openURLs()
	case key.Matches(msg, k.Refresh):
		return m, m.refreshResources()
	default:
		return updateBody(m, msg)
	}
	m.clampAccess()
	return m, nil
}

func (resourceDetailScreen) View(m Model, body draw.Region, ctx ui.Ctx) {
	ui.ResourceDetail(region(body, m.layout.Table), m.resourceDetailState(), ctx)
}

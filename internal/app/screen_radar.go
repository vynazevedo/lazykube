package app

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type radarScreen struct{}

func (radarScreen) ID() ScreenID {
	return ScreenRadar
}

func (radarScreen) Title() string {
	return "Radar"
}

func (radarScreen) ShowsDetails() bool {
	return false
}

func (radarScreen) Init(Model) tea.Cmd {
	return nil
}

func (s radarScreen) Update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.helpOpen {
			return updateBody(m, msg)
		}
		if !slices.ContainsFunc(s.Keys(m), func(b key.Binding) bool { return key.Matches(msg, b) }) {
			return m, nil
		}
		return m.handleRadarKey(msg)
	case tea.MouseClickMsg:
		if !m.helpOpen && msg.Button == tea.MouseLeft {
			m.handleRadarClick(tea.Mouse(msg))
			return m, nil
		}
	case tea.MouseWheelMsg:
		m.handleRadarWheel(tea.Mouse(msg))
		return m, nil
	case tea.MouseMotionMsg:
		return m, nil
	}
	return updateBody(m, msg)
}

func (radarScreen) Keys(m Model) []key.Binding {
	k := m.keys
	keys := []key.Binding{
		k.Up, k.Down, k.PageUp, k.PageDown, k.Home, k.End, k.Enter, k.Esc,
		k.SecurityAck, k.Palette, k.Pause, k.Help, k.Quit,
		k.Menu, k.MenuPrev, k.MenuNext, k.Dashboard, k.Workloads, k.Nodes, k.Security, k.Events, k.Radar,
		k.Context, k.Namespace, k.Tree, k.Yank,
	}
	if m.layout.Compact {
		keys = append(keys, k.NextPanel, k.PrevPanel)
	}
	return keys
}

func (radarScreen) Help(m Model) []ui.KeyHelp {
	return compactPanels(m, "signals / evidence", ui.RadarHelp())
}

func (radarScreen) FullHelp(m Model) []ui.KeyHelp {
	return compactPanels(m, "signals / evidence", ui.FullHelpRadar())
}

func compactPanels(m Model, desc string, help []ui.KeyHelp) []ui.KeyHelp {
	if !m.layout.Compact {
		return help
	}
	return append([]ui.KeyHelp{{Key: "tab", Desc: desc}}, help...)
}

func (radarScreen) View(m Model, body draw.Region, ctx ui.Ctx) {
	st := m.radarViewState()
	ui.RadarSummary(region(body, m.layout.Scope), st, ctx)
	ui.RadarList(region(body, m.layout.Events), st, ctx)
	ui.RadarDetail(region(body, m.layout.Message), st, ctx)
	ui.RadarCoverage(region(body, m.layout.Coverage), st, ctx)
}

func (m Model) maxRadarScroll() int {
	lines := ui.RadarDetailLines(m.radarViewState(), max(1, m.layout.Message.W-4))
	return max(0, len(lines)-ui.RadarDetailRows(m.layout.Message.H))
}

func (m *Model) scrollRadar(delta int) {
	m.radar.scroll = max(0, min(m.radar.scroll+delta, m.maxRadarScroll()))
}

func (m *Model) stepRadar(delta int) {
	if m.layout.Compact && m.focus == FocusDetails {
		m.scrollRadar(delta)
		return
	}
	m.moveRadar(delta)
}

func (m Model) handleRadarKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Up):
		m.stepRadar(-1)
	case key.Matches(msg, k.Down):
		m.stepRadar(1)
	case key.Matches(msg, k.PageUp):
		m.scrollRadar(-ui.RadarDetailRows(m.layout.Message.H))
	case key.Matches(msg, k.PageDown):
		m.scrollRadar(ui.RadarDetailRows(m.layout.Message.H))
	case key.Matches(msg, k.Home):
		m.moveRadar(-len(m.radar.rows))
	case key.Matches(msg, k.End):
		m.moveRadar(len(m.radar.rows))
	case key.Matches(msg, k.Enter):
		m.openSignalPod()
	default:
		return updateBody(m, msg)
	}
	return m, nil
}

func (m *Model) handleRadarClick(ms tea.Mouse) {
	switch {
	case m.layout.Events.Contains(ms.X, ms.Y):
		row := ms.Y - (m.layout.Events.Y + 1 + ui.RadarListTop)
		rows := ui.RadarListRows(m.layout.Events.H)
		if row < 0 || row >= rows {
			return
		}
		idx := ui.ScrollOffset(m.radar.cursor, len(m.radar.rows), rows) + row
		if idx < len(m.radar.rows) {
			m.moveRadar(idx - m.radar.cursor)
		}
	case m.layout.Message.Contains(ms.X, ms.Y):
		if m.radarButtonAt(ms) {
			m.openSignalPod()
		}
	}
}

func (m Model) radarButtonAt(ms tea.Mouse) bool {
	st := m.radarViewState()
	w := max(1, m.layout.Message.W-4)
	span, line, ok := ui.RadarPodButton(st, w)
	if !ok {
		return false
	}
	scroll := max(0, min(st.Scroll, len(ui.RadarDetailLines(st, w))-ui.RadarDetailRows(m.layout.Message.H)))
	y := ms.Y - (m.layout.Message.Y + 1) + scroll
	x := ms.X - (m.layout.Message.X + 2)
	return y == line && x >= span.X && x < span.X+span.W
}

func (m *Model) handleRadarWheel(ms tea.Mouse) {
	delta := 0
	switch ms.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return
	}
	switch {
	case m.layout.Events.Contains(ms.X, ms.Y):
		m.moveRadar(delta)
	case m.layout.Message.Contains(ms.X, ms.Y):
		m.scrollRadar(delta)
	}
}

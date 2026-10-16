package app

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type diagState struct {
	diag         data.Diagnostics
	containers   []data.Container
	logView      data.LogView
	logContainer string
	previous     bool
	scope        data.EventScope
	full         bool
	search       textinput.Model
	searching    bool
	matches      []int
	matchIdx     int
}

func newDiagState(src data.Source) diagState {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "buscar no log…"
	d := diagState{search: ti}
	if src != nil {
		d.diag = data.DiagnosticsFor(src)
		_, d.full = src.(data.Diagnostics)
	}
	return d
}

func (m *Model) resetLogTarget() {
	m.dg.logContainer, m.dg.previous = "", false
	m.dg.logView = data.LogView{}
	m.dg.matches, m.dg.matchIdx = nil, 0
}

func (m *Model) refreshLogs(pod string) {
	if pod == "" {
		if m.dg.diag != nil {
			m.dg.diag.PodLogs(data.LogRequest{}, m.clock.T, 0)
		}
		m.dg.logView = data.LogView{}
		m.applyLogs(nil)
		return
	}
	view := m.dg.diag.PodLogs(data.LogRequest{Pod: pod, Container: m.dg.logContainer, Previous: m.dg.previous}, m.clock.T, logBuffer)
	lines := view.Lines
	view.Lines = nil
	m.dg.logView = view
	m.applyLogs(lines)
}

func (m Model) eventNamespace() string {
	if m.nsFilter != "" {
		return m.nsFilter
	}
	if p := m.selectedPod(); p != nil {
		return p.Namespace
	}
	return ""
}

func (m *Model) refreshEvents(pod string) {
	if m.tab != ui.TabEvents || m.dg.diag == nil || !m.screen().ShowsDetails() {
		m.events = data.EventFeed{Scope: m.dg.scope}
		return
	}
	m.events = m.dg.diag.ScopedEvents(data.EventQuery{Scope: m.dg.scope, Pod: pod, Namespace: m.eventNamespace()})
}

func (m Model) logInfo() ui.LogInfo {
	info := ui.LogInfo{
		Container: m.dg.logView.Container,
		Previous:  m.dg.logView.Previous,
		State:     m.dg.logView.State,
		Reason:    m.dg.logView.Reason,
		Query:     m.dg.search.Value(),
		Searching: m.dg.searching,
		Matches:   len(m.dg.matches),
		Match:     m.dg.matchIdx,
		MatchRow:  -1,
	}
	if len(m.dg.matches) > 0 {
		info.MatchRow = m.dg.matches[m.dg.matchIdx]
	}
	info.Containers = len(m.dg.containers)
	return info
}

func (m *Model) logRows() []ui.LogRow {
	return ui.LogRows(m.logs, ui.LogWidth(m.layout.Details.W), m.wrap)
}

func (m *Model) updateMatches() {
	q := m.dg.search.Value()
	if q == "" {
		m.dg.matches, m.dg.matchIdx = nil, 0
		return
	}
	m.dg.matches = ui.MatchRows(m.logRows(), q)
	m.dg.matchIdx = max(0, min(m.dg.matchIdx, len(m.dg.matches)-1))
}

func (m *Model) revealMatch() {
	if len(m.dg.matches) == 0 {
		return
	}
	row := m.dg.matches[m.dg.matchIdx]
	visible := ui.ScrollRows(ui.TabLog, m.layout.Details.H)
	if row >= m.scroll && row < m.scroll+visible {
		m.follow = false
		return
	}
	m.scrollTo(row - visible/2)
	m.follow = false
}

func (m *Model) firstMatchFromScroll() {
	m.dg.matchIdx = 0
	for i, row := range m.dg.matches {
		if row >= m.scroll {
			m.dg.matchIdx = i
			break
		}
	}
}

func (m *Model) stepMatch(delta int) {
	n := len(m.dg.matches)
	if n == 0 {
		return
	}
	m.dg.matchIdx = ((m.dg.matchIdx+delta)%n + n) % n
	m.revealMatch()
}

func (m *Model) startSearch() tea.Cmd {
	m.dg.searching = true
	return m.dg.search.Focus()
}

func (m Model) handleSearchKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.dg.searching = false
		m.dg.search.Blur()
		m.dg.search.SetValue("")
		m.updateMatches()
		return m, nil
	case "enter":
		m.dg.searching = false
		m.dg.search.Blur()
		m.revealMatch()
		return m, nil
	}
	var cmd tea.Cmd
	before := m.dg.search.Value()
	m.dg.search, cmd = m.dg.search.Update(msg)
	if m.dg.search.Value() != before {
		m.updateMatches()
		m.firstMatchFromScroll()
		m.revealMatch()
	}
	return m, cmd
}

func (m *Model) cycleContainer() {
	names := make([]string, 0, len(m.dg.containers))
	for _, c := range m.dg.containers {
		names = append(names, c.Name)
	}
	if len(names) < 2 {
		return
	}
	current := m.dg.logView.Container
	next := names[0]
	for i, n := range names {
		if n == current {
			next = names[(i+1)%len(names)]
			break
		}
	}
	m.dg.logContainer, m.dg.previous = next, false
	m.restartLogs()
}

func (m *Model) togglePrevious() {
	m.dg.previous = !m.dg.previous
	if m.dg.logContainer == "" {
		m.dg.logContainer = m.dg.logView.Container
	}
	m.restartLogs()
	if m.tab != ui.TabLog && m.tab != ui.TabTop {
		m.setTab(ui.TabLog)
	}
}

func (m *Model) restartLogs() {
	m.logs, m.newLines, m.follow = nil, 0, true
	m.dg.matchIdx = 0
	m.refresh()
	if m.tab == ui.TabLog {
		m.scrollTo(m.maxScroll())
	}
}

func (m *Model) cycleScope() {
	if m.tab != ui.TabEvents {
		m.setTab(ui.TabEvents)
		return
	}
	m.dg.scope = m.dg.scope.Next()
	m.scroll = 0
	m.refreshEvents(m.detailTarget())
}

func (m Model) searchActive() bool {
	return m.focus == FocusDetails && m.tab == ui.TabLog
}

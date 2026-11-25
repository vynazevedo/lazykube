package app

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type securityScreen struct{}

type securityFrame struct {
	Tab      ui.SecurityPageTab
	Focus    int
	NoSystem bool
	Selected data.WorkloadKey
	Cursor   int
	Scroll   int
}

type securityPage struct {
	securityFrame
	have     bool
	result   securityResult
	rules    []data.WorkloadRule
	rulesFor data.WorkloadKey
	rulesKey securityKey
	audit    auditView
}

func (securityScreen) ID() ScreenID {
	return ScreenSecurity
}

func (securityScreen) Title() string {
	return "Security"
}

func (securityScreen) ShowsDetails() bool {
	return false
}

func (securityScreen) Init(Model) tea.Cmd {
	return nil
}

func (s securityScreen) Update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.helpOpen {
			return updateBody(m, msg)
		}
		if !slices.ContainsFunc(s.Keys(m), func(b key.Binding) bool { return key.Matches(msg, b) }) {
			return m, nil
		}
		next, cmd := m.handleSecurityKey(msg)
		next.syncCompactSecurity(m.sec.Focus)
		return next, cmd
	case tea.MouseClickMsg:
		if !m.helpOpen && msg.Button == tea.MouseLeft {
			focus := m.sec.Focus
			m.handleSecurityClick(tea.Mouse(msg))
			m.syncCompactSecurity(focus)
			return m, nil
		}
	case tea.MouseWheelMsg:
		m.handleSecurityWheel(tea.Mouse(msg))
		return m, nil
	case tea.MouseMotionMsg:
		return m, nil
	}
	return updateBody(m, msg)
}

func (securityScreen) Keys(m Model) []key.Binding {
	k := m.keys
	return []key.Binding{
		k.Up, k.Down, k.NextPanel, k.PrevPanel, k.Enter, k.Esc, k.TabPrev, k.TabNext, k.Cohort,
		k.PageUp, k.PageDown, k.Home, k.End,
		k.SecurityAck, k.Palette, k.Pause, k.Help, k.Quit,
		k.Menu, k.MenuPrev, k.MenuNext, k.Dashboard, k.Workloads, k.Nodes, k.Security, k.Events, k.Radar,
		k.Context, k.Namespace, k.Tree, k.Yank,
	}
}

func (securityScreen) Help(Model) []ui.KeyHelp {
	return ui.SecurityHelp()
}

func (securityScreen) FullHelp(Model) []ui.KeyHelp {
	return ui.FullHelpSecurity()
}

func (securityScreen) View(m Model, body draw.Region, ctx ui.Ctx) {
	st := m.securityPageState()
	ui.SecuritySummary(region(body, m.layout.SecSummary), st, ctx)
	ui.SecurityTabs(region(body, m.layout.SecTabs), st.Tab, ctx)
	switch st.Tab {
	case ui.SecTabWorkloads:
		ui.SecurityWorkloadsPanel(region(body, m.layout.SecList), st, ctx)
		ui.SecurityEvidencePanel(region(body, m.layout.SecEvidence), st, ctx)
	case ui.SecTabAudit:
		ui.SecurityAuditList(region(body, m.layout.SecList), st, ctx)
		ui.SecurityAuditDetail(region(body, m.layout.SecEvidence), st, ctx)
	default:
		r := m.layout.SecList
		if !m.layout.Compact {
			r.W += 1 + m.layout.SecEvidence.W
		}
		ui.SecurityPendingPanel(region(body, r), st.Tab, ctx)
	}
	ui.SecurityNotes(region(body, m.layout.SecNotes), st)
}

func (m Model) securityScope() nsSet {
	return m.nsFilter
}

func (m *Model) syncCompactSecurity(focus int) {
	if m.layout.Compact && m.sec.Focus != focus {
		m.relayout()
	}
}

func (m Model) securityHistory(scope nsSet) []float64 {
	names := scope.names()
	switch len(names) {
	case 0:
		return m.securitySession.History[""]
	case 1:
		return m.securitySession.History[names[0]]
	}
	return nil
}

func (m Model) securityPageState() ui.SecurityPageState {
	scope := m.securityScope()
	cohort := "Todos"
	if m.sec.NoSystem && scope == "" {
		cohort = "Sem sistema"
	}
	return ui.SecurityPageState{
		Synthetic:   m.snap.Synthetic,
		Namespaces:  scope.names(),
		Compact:     m.layout.Compact,
		All:         m.sec.result.all,
		NoSystem:    m.sec.result.noSystem,
		NoSystemCut: scope != "",
		Alerts:      m.securityAlerts(scope),
		History:     m.securityHistory(scope),
		RuleVersion: m.security.RuleVersion,
		Loading:     !m.sec.have,
		Stale:       m.sec.have && m.sec.result.key != m.wantedSecurityKey(),
		RulesReady:  m.sec.have && len(m.sec.result.workloads) > 0 && m.sec.rulesFor == m.sec.Selected,
		Tab:         m.sec.Tab,
		Focus:       m.sec.Focus,
		Cohort:      cohort,
		Workloads:   m.sec.result.workloads,
		Cursor:      m.sec.Cursor,
		Rules:       m.sec.rules,
		Scroll:      m.sec.Scroll,
		Audit:       m.auditState(),
	}
}

func (m Model) yankWorkload() (yank, bool) {
	if m.sec.Tab != ui.SecTabWorkloads || m.sec.Cursor >= len(m.sec.result.workloads) {
		return yank{}, false
	}
	w := m.sec.result.workloads[m.sec.Cursor]
	return exact(w.Key.Namespace + "/" + w.Name)
}

func (m Model) securityAlerts(scope nsSet) int {
	if scope == "" {
		return len(m.securitySession.Alerts)
	}
	allowed, n := scope.matcher(), 0
	for _, f := range m.securitySession.Alerts {
		if allowed(f.Target.Namespace) {
			n++
		}
	}
	return n
}

func (m *Model) adoptSecurityWorkload(i int) {
	m.sec.Cursor = max(0, min(i, len(m.sec.result.workloads)-1))
	m.sec.Selected = data.WorkloadKey{}
	if m.sec.Cursor < len(m.sec.result.workloads) {
		m.sec.Selected = m.sec.result.workloads[m.sec.Cursor].Key
	}
}

func (m Model) securityEvidenceMax() int {
	r := m.layout.SecEvidence
	if r.Empty() {
		return 0
	}
	lines := ui.SecurityEvidenceLines(m.securityPageState(), max(1, r.W-4))
	return max(0, len(lines)-ui.SecurityEvidenceRows(r.H))
}

func (m *Model) clampSecurityScroll() {
	if m.layout.SecEvidence.Empty() {
		return
	}
	m.sec.Scroll = max(0, min(m.sec.Scroll, m.securityEvidenceMax()))
}

func (m *Model) moveSecurity(delta int) {
	if m.sec.Tab == ui.SecTabAudit {
		if m.sec.Focus == ui.SecurityFocusList {
			m.moveAudit(delta)
		}
		return
	}
	if m.sec.Tab != ui.SecTabWorkloads || !m.sec.have {
		return
	}
	if m.sec.Focus == ui.SecurityFocusEvidence {
		m.sec.Scroll += delta
		m.clampSecurityScroll()
		return
	}
	before := m.sec.Selected
	m.adoptSecurityWorkload(m.sec.Cursor + delta)
	if m.sec.Selected != before {
		m.sec.Scroll = 0
	}
}

func (m Model) securityPageSize() int {
	if m.sec.Tab == ui.SecTabAudit {
		return max(1, ui.SecurityAuditRows(m.layout.SecList.H))
	}
	if m.sec.Focus == ui.SecurityFocusEvidence {
		return max(1, ui.SecurityEvidenceRows(m.layout.SecEvidence.H))
	}
	return max(1, ui.SecurityListRows(m.layout.SecList.H))
}

func (m Model) handleSecurityKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Up):
		m.moveSecurity(-1)
	case key.Matches(msg, k.Down):
		m.moveSecurity(1)
	case key.Matches(msg, k.PageUp):
		m.moveSecurity(-m.securityPageSize())
	case key.Matches(msg, k.PageDown):
		m.moveSecurity(m.securityPageSize())
	case key.Matches(msg, k.Home):
		m.moveSecurity(-len(m.sec.result.workloads) - len(m.sec.audit.events) - m.securityEvidenceMax())
	case key.Matches(msg, k.End):
		m.moveSecurity(len(m.sec.result.workloads) + len(m.sec.audit.events) + m.securityEvidenceMax())
	case key.Matches(msg, k.NextPanel), key.Matches(msg, k.PrevPanel):
		if m.sec.Tab == ui.SecTabWorkloads || m.sec.Tab == ui.SecTabAudit {
			m.sec.Focus = 1 - m.sec.Focus
		}
	case key.Matches(msg, k.Enter):
		switch {
		case m.sec.Tab == ui.SecTabAudit && m.sec.Focus == ui.SecurityFocusEvidence:
			m.toggleAuditSessions()
		case m.sec.Tab == ui.SecTabWorkloads && len(m.sec.result.workloads) > 0:
			m.sec.Focus = ui.SecurityFocusEvidence
		}
	case key.Matches(msg, k.TabPrev):
		m.setSecurityTab(m.sec.Tab.Next(-1))
	case key.Matches(msg, k.TabNext):
		m.setSecurityTab(m.sec.Tab.Next(1))
	case key.Matches(msg, k.Cohort):
		if m.securityScope() == "" {
			m.sec.NoSystem = !m.sec.NoSystem
			m.sec.Scroll = 0
		}
	case key.Matches(msg, k.SecurityAck):
		m.acknowledgeSecurity()
	default:
		return updateBody(m, msg)
	}
	return m, nil
}

func (m *Model) setSecurityTab(t ui.SecurityPageTab) {
	if t != m.sec.Tab {
		m.sec.Focus = ui.SecurityFocusList
	}
	m.sec.Tab = t
}

func (m *Model) handleSecurityClick(ms tea.Mouse) {
	l := m.layout
	switch {
	case l.SecTabs.Contains(ms.X, ms.Y):
		if t, ok := ui.SecurityTabAt(l.SecTabs.W-2, ms.X-l.SecTabs.X-1, ms.Y-l.SecTabs.Y); ok {
			m.setSecurityTab(t)
		}
	case m.sec.Tab == ui.SecTabAudit:
		m.handleAuditClick(ms)
	case m.sec.Tab != ui.SecTabWorkloads:
	case l.SecList.Contains(ms.X, ms.Y):
		m.sec.Focus = ui.SecurityFocusList
		rows := ui.SecurityListRows(l.SecList.H)
		row := ms.Y - (l.SecList.Y + 2)
		if row < 0 || row >= rows {
			return
		}
		idx := ui.ScrollOffset(m.sec.Cursor, len(m.sec.result.workloads), rows) + row
		if idx < len(m.sec.result.workloads) && idx != m.sec.Cursor {
			m.moveSecurity(idx - m.sec.Cursor)
		}
	case l.SecEvidence.Contains(ms.X, ms.Y):
		m.sec.Focus = ui.SecurityFocusEvidence
	}
}

func (m *Model) handleSecurityWheel(ms tea.Mouse) {
	delta := 0
	switch ms.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return
	}
	if m.sec.Tab == ui.SecTabAudit {
		if m.layout.SecList.Contains(ms.X, ms.Y) {
			m.moveAudit(delta)
		}
		return
	}
	if m.sec.Tab != ui.SecTabWorkloads {
		return
	}
	focus := m.sec.Focus
	switch {
	case m.layout.SecList.Contains(ms.X, ms.Y):
		m.sec.Focus = ui.SecurityFocusList
	case m.layout.SecEvidence.Contains(ms.X, ms.Y):
		m.sec.Focus = ui.SecurityFocusEvidence
		delta *= 3
	default:
		return
	}
	m.moveSecurity(delta)
	m.sec.Focus = focus
}

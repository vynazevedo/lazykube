package app

import (
	"fmt"
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
	key       string
	all       data.SecurityAggregate
	noSystem  data.SecurityAggregate
	workloads []data.WorkloadPosture
	rules     []data.WorkloadRule
	rulesFor  data.WorkloadKey
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
		return m.handleSecurityKey(msg)
	case tea.MouseClickMsg:
		if !m.helpOpen && msg.Button == tea.MouseLeft {
			m.handleSecurityClick(tea.Mouse(msg))
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
		k.Menu, k.MenuPrev, k.MenuNext, k.Dashboard, k.Workloads, k.Nodes, k.Security, k.Events,
		k.Context, k.Namespace,
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
	if st.Tab == ui.SecTabWorkloads {
		ui.SecurityWorkloadsPanel(region(body, m.layout.SecList), st, ctx)
		ui.SecurityEvidencePanel(region(body, m.layout.SecEvidence), st, ctx)
	} else {
		r := m.layout.SecList
		r.W = m.layout.SecList.W + 1 + m.layout.SecEvidence.W
		ui.SecurityPendingPanel(region(body, r), st.Tab, ctx)
	}
	ui.SecurityNotes(region(body, m.layout.SecNotes), st)
}

func (m Model) securityScope() string {
	return m.nsFilter
}

func (m Model) securityPageState() ui.SecurityPageState {
	scope := m.securityScope()
	cohort := "Todos"
	if m.sec.NoSystem && scope == "" {
		cohort = "Sem sistema"
	}
	return ui.SecurityPageState{
		Synthetic:   m.snap.Synthetic,
		Namespace:   scope,
		All:         m.sec.all,
		NoSystem:    m.sec.noSystem,
		NoSystemCut: scope != "",
		Alerts:      m.securityAlerts(scope),
		History:     m.securitySession.History[scope],
		RuleVersion: m.security.RuleVersion,
		Tab:         m.sec.Tab,
		Focus:       m.sec.Focus,
		Cohort:      cohort,
		Workloads:   m.sec.workloads,
		Cursor:      m.sec.Cursor,
		Rules:       m.sec.rules,
		Scroll:      m.sec.Scroll,
	}
}

func (m Model) securityAlerts(scope string) int {
	if scope == "" {
		return len(m.securitySession.Alerts)
	}
	n := 0
	for _, f := range m.securitySession.Alerts {
		if f.Target.Namespace == scope {
			n++
		}
	}
	return n
}

func (m *Model) refreshSecurityPage() {
	if !m.onScreen(ScreenSecurity) {
		return
	}
	scope := m.securityScope()
	key := fmt.Sprintf("%d|%s|%s|%t|%d", m.security.Revision, m.security.RuleVersion, scope, m.sec.NoSystem, len(m.security.Pods))
	if key != m.sec.key {
		m.sec.key = key
		m.rebuildSecurityPage(scope)
	}
	m.syncSecurityRules()
}

func (m *Model) rebuildSecurityPage(scope string) {
	d := m.securitySession.Dashboard
	m.sec.all = d.Cluster
	if scope != "" {
		a, ok := d.Namespaces[scope]
		if !ok {
			a = data.SecurityAggregate{Name: scope, Score: data.PostureScore{Coverage: data.Coverage{State: data.CoverageUnavailable, Reason: "namespace não observado"}}}
		}
		m.sec.all = a
	}
	m.sec.noSystem = data.AggregateSecurity(data.SubsetSecurity(m.security, func(p data.PodPosture) bool {
		return !data.IsSystemNamespace(p.Target.Namespace)
	})).Cluster
	noSystem := m.sec.NoSystem && scope == ""
	m.sec.workloads = data.AggregateWorkloads(m.security, func(p data.PodPosture) bool {
		if scope != "" {
			return p.Target.Namespace == scope
		}
		return !noSystem || !data.IsSystemNamespace(p.Target.Namespace)
	})
	m.sec.rules = nil
	if i := slices.IndexFunc(m.sec.workloads, func(w data.WorkloadPosture) bool { return w.Key == m.sec.Selected }); i >= 0 {
		m.sec.Cursor = i
	}
	m.adoptSecurityWorkload(m.sec.Cursor)
}

func (m *Model) adoptSecurityWorkload(i int) {
	m.sec.Cursor = max(0, min(i, len(m.sec.workloads)-1))
	m.sec.Selected = data.WorkloadKey{}
	if m.sec.Cursor < len(m.sec.workloads) {
		m.sec.Selected = m.sec.workloads[m.sec.Cursor].Key
	}
}

func (m *Model) syncSecurityRules() {
	if m.sec.rules != nil && m.sec.rulesFor == m.sec.Selected {
		return
	}
	m.sec.rules, m.sec.rulesFor = []data.WorkloadRule{}, m.sec.Selected
	if m.sec.Cursor < len(m.sec.workloads) {
		m.sec.rules = data.WorkloadRules(m.security, m.sec.workloads[m.sec.Cursor])
	}
	m.clampSecurityScroll()
}

func (m Model) securityEvidenceMax() int {
	r := m.layout.SecEvidence
	lines := ui.SecurityEvidenceLines(m.securityPageState(), max(1, r.W-4))
	return max(0, len(lines)-ui.SecurityEvidenceRows(r.H))
}

func (m *Model) clampSecurityScroll() {
	m.sec.Scroll = max(0, min(m.sec.Scroll, m.securityEvidenceMax()))
}

func (m *Model) moveSecurity(delta int) {
	if m.sec.Tab != ui.SecTabWorkloads {
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
		m.syncSecurityRules()
	}
}

func (m Model) securityPageSize() int {
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
		m.moveSecurity(-len(m.sec.workloads) - m.securityEvidenceMax())
	case key.Matches(msg, k.End):
		m.moveSecurity(len(m.sec.workloads) + m.securityEvidenceMax())
	case key.Matches(msg, k.NextPanel), key.Matches(msg, k.PrevPanel):
		if m.sec.Tab == ui.SecTabWorkloads {
			m.sec.Focus = 1 - m.sec.Focus
		}
	case key.Matches(msg, k.Enter):
		if m.sec.Tab == ui.SecTabWorkloads && len(m.sec.workloads) > 0 {
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
			m.refreshSecurityPage()
		}
	case key.Matches(msg, k.SecurityAck):
		m.acknowledgeSecurity()
	default:
		return updateBody(m, msg)
	}
	return m, nil
}

func (m *Model) setSecurityTab(t ui.SecurityPageTab) {
	m.sec.Tab = t
	if t != ui.SecTabWorkloads {
		m.sec.Focus = ui.SecurityFocusList
	}
}

func (m *Model) handleSecurityClick(ms tea.Mouse) {
	l := m.layout
	switch {
	case l.SecTabs.Contains(ms.X, ms.Y):
		if t, ok := ui.SecurityTabAt(l.SecTabs.W-2, ms.X-l.SecTabs.X-1, ms.Y-l.SecTabs.Y); ok {
			m.setSecurityTab(t)
		}
	case m.sec.Tab != ui.SecTabWorkloads:
	case l.SecList.Contains(ms.X, ms.Y):
		m.sec.Focus = ui.SecurityFocusList
		rows := ui.SecurityListRows(l.SecList.H)
		row := ms.Y - (l.SecList.Y + 2)
		if row < 0 || row >= rows {
			return
		}
		idx := ui.ScrollOffset(m.sec.Cursor, len(m.sec.workloads), rows) + row
		if idx < len(m.sec.workloads) && idx != m.sec.Cursor {
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

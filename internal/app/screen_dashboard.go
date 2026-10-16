package app

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type dashboardScreen struct{}

func (dashboardScreen) ID() ScreenID {
	return ScreenDashboard
}

func (dashboardScreen) Title() string {
	return "Dashboard"
}

func (dashboardScreen) Help(Model) []ui.KeyHelp {
	return ui.ShortHelp()
}

func (dashboardScreen) FullHelp(Model) []ui.KeyHelp {
	return ui.FullHelp()
}

func (dashboardScreen) Init(Model) tea.Cmd {
	return nil
}

func (dashboardScreen) Update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	return updateBody(m, msg)
}

func updateBody(m Model, msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.setLayout(msg.Width, msg.Height)
		m.refresh()
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseClickMsg:
		return m.handleClick(tea.Mouse(msg))
	case tea.MouseWheelMsg:
		return m.handleWheel(tea.Mouse(msg))
	case tea.MouseMotionMsg:
		m.handleMotion(tea.Mouse(msg))
		return m, nil
	}
	if m.filtering {
		var cmd tea.Cmd
		before := m.filter.Value()
		m.filter, cmd = m.filter.Update(msg)
		if m.filter.Value() != before {
			m.syncSelection()
		}
		return m, cmd
	}
	return m, nil
}

func (dashboardScreen) Keys(m Model) []key.Binding {
	return m.keys.Bindings()
}

func (dashboardScreen) View(m Model, body draw.Region, ctx ui.Ctx) {
	pod := m.selectedPod()
	if m.layout.ShowLeft {
		ui.NodePanel(region(body, m.layout.Node), ui.NodeState{
			Node:    m.snap.Node,
			CPU:     m.metrics.NodeCPU,
			CPUFrac: m.motion.nodeCPU.Pos,
			MemFrac: m.motion.nodeMem.Pos,
		}, ctx)
		highlight := ""
		if pod != nil {
			highlight = pod.Namespace
		}
		ui.NamespacesPanel(region(body, m.layout.Namespaces), ui.NamespacesState{
			Items:     m.snap.Namespaces,
			Highlight: highlight,
			Filter:    m.nsFilter,
			Cursor:    m.nsCursor,
			Focused:   m.focus == FocusNamespaces,
		}, ctx)
		ui.DeploymentsPanel(region(body, m.layout.Deployments), ui.DeploymentsState{
			Coverage: m.snap.DeploymentCoverage,
			Items:    m.snap.Deployments,
			Cursor:   m.depCursor,
			Focused:  m.focus == FocusDeployments,
		}, ctx)
	}

	ui.PodsPanel(region(body, m.layout.Pods), m.podsPanelState(m.visiblePods()), ctx)

	ui.HeatmapPanel(region(body, m.layout.Heatmap), ui.HeatmapState{
		Labels:    m.metrics.HeatLabels,
		Heat:      m.metrics.Heat,
		Available: m.metrics.Available,
		Reason:    m.metrics.Reason,
	}, ctx)

	ui.DetailsPanel(region(body, m.layout.Details), m.detailsState(pod), ctx)
}

func (m Model) detailsState(pod *data.Pod) ui.DetailsState {
	return ui.DetailsState{
		Security:       m.securityView,
		SecurityAlerts: len(m.securitySession.Alerts),
		Tab:            m.tab,
		HoverTab:       m.hoverTab,
		Pod:            pod,
		Metrics:        m.metrics,
		Logs:           m.logs,
		Events:         m.events,
		Manifest:       m.manifest,
		Describe:       m.describe,
		Diag:           ui.Diagnosis{Containers: m.dg.containers, LogContainer: m.dg.logView.Container},
		LogInfo:        m.logInfo(),
		Focused:        m.focus == FocusDetails,
		Scroll:         m.scroll,
		Follow:         m.follow,
		NewLines:       m.newLines,
		Wrap:           m.wrap,
		MemBar:         m.motion.memBar.Pos,
		Slide:          m.slideOffset(),
	}
}

package app

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type workloadsScreen struct{}

type depOverlay struct {
	open bool
	key  string
}

func (workloadsScreen) ID() ScreenID {
	return ScreenWorkloads
}

func (workloadsScreen) Title() string {
	return "Workloads"
}

func (workloadsScreen) Init(Model) tea.Cmd {
	return nil
}

func (workloadsScreen) Update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	return updateBody(m, msg)
}

func (workloadsScreen) Help(Model) []ui.KeyHelp {
	return ui.ShortHelp()
}

func (workloadsScreen) FullHelp(Model) []ui.KeyHelp {
	return ui.FullHelpWorkloads()
}

func (workloadsScreen) Keys(m Model) []key.Binding {
	all := m.keys.Bindings()
	out := make([]key.Binding, 0, len(all))
	for _, b := range all {
		if b.Help().Key != m.keys.ToggleLeft.Help().Key {
			out = append(out, b)
		}
	}
	return out
}

func (workloadsScreen) View(m Model, body draw.Region, ctx ui.Ctx) {
	pods := m.visiblePods()
	ui.PodsPanel(region(body, m.layout.Pods), m.podsPanelState(pods), ctx)
	ui.DeploymentsPanel(region(body, m.layout.Deployments), ui.DeploymentsState{
		Coverage: m.snap.DeploymentCoverage,
		Items:    m.visibleDeployments(),
		Cursor:   m.depCursor,
		Focused:  m.focus == FocusDeployments,
	}, ctx)
	st := m.detailsState(m.selectedPod())
	st.Title = "Preview"
	ui.DetailsPanel(region(body, m.layout.Details), st, ctx)
}

func (m Model) podsPanelState(pods []data.Pod) ui.PodsState {
	return ui.PodsState{
		Pods:       pods,
		Selected:   m.selectedIndex(pods),
		Offset:     m.podOffset,
		Hover:      m.hover,
		CPU:        m.metrics.PodCPU,
		Filter:     m.filter.Value(),
		FilterPos:  m.filter.Position(),
		Filtering:  m.filtering,
		Focused:    m.focus == FocusPods,
		SelectionK: m.selectionKs(pods),
		NSFilter:   m.nsFilter,
	}
}

func (m Model) visibleDeployments() []data.Deployment {
	if !m.onScreen(ScreenWorkloads) || m.nsFilter == "" {
		return m.snap.Deployments
	}
	out := make([]data.Deployment, 0, len(m.snap.Deployments))
	for _, d := range m.snap.Deployments {
		if d.Namespace == m.nsFilter {
			out = append(out, d)
		}
	}
	return out
}

func deploymentKey(d data.Deployment) string {
	return d.Namespace + "/" + d.Name
}

func deploymentListed(c data.Coverage) bool {
	return c.State == "" || c.State == data.CoverageComplete
}

func (m *Model) openDeployment() {
	deps := m.visibleDeployments()
	if !deploymentListed(m.snap.DeploymentCoverage) || m.depCursor < 0 || m.depCursor >= len(deps) {
		return
	}
	m.dep = depOverlay{open: true, key: deploymentKey(deps[m.depCursor])}
}

func (m Model) handleOverlayKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Esc), key.Matches(msg, k.Enter):
		m.dep.open = false
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) deploymentOverlayState() ui.DeploymentOverlayState {
	st := ui.DeploymentOverlayState{Key: m.dep.key, Coverage: m.snap.DeploymentCoverage}
	for _, d := range m.snap.Deployments {
		if deploymentKey(d) == m.dep.key {
			st.Deployment = &d
			break
		}
	}
	return st
}

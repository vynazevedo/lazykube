package app

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type nodesScreen struct{}

func (nodesScreen) ID() ScreenID {
	return ScreenNodes
}

func (nodesScreen) Title() string {
	return "Nodes"
}

func (nodesScreen) ShowsDetails() bool {
	return false
}

func (nodesScreen) Init(Model) tea.Cmd {
	return nil
}

func (s nodesScreen) Update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && !m.helpOpen && !m.filtering && !s.handles(m, k) {
		return m, nil
	}
	return updateBody(m, msg)
}

func (s nodesScreen) handles(m Model, msg tea.KeyPressMsg) bool {
	if !slices.ContainsFunc(m.keys.Bindings(), func(b key.Binding) bool { return key.Matches(msg, b) }) {
		return true
	}
	return slices.ContainsFunc(s.Keys(m), func(b key.Binding) bool { return key.Matches(msg, b) })
}

func (nodesScreen) Keys(m Model) []key.Binding {
	k := m.keys
	keys := []key.Binding{
		k.Up, k.Down, k.Filter, k.Enter, k.Esc, k.SecurityAck,
		k.Palette, k.Pause, k.Help, k.Quit,
		k.Menu, k.MenuPrev, k.MenuNext, k.Dashboard, k.Workloads, k.Nodes, k.Events,
		k.Context, k.Namespace, k.Tree,
	}
	if m.layout.Compact {
		keys = append(keys, k.NextPanel, k.PrevPanel)
	}
	return keys
}

func (nodesScreen) Help(m Model) []ui.KeyHelp {
	if m.layout.Compact {
		return append([]ui.KeyHelp{{Key: "tab", Desc: "summary / pods"}}, ui.NodesHelp()...)
	}
	return ui.NodesHelp()
}

func (nodesScreen) FullHelp(m Model) []ui.KeyHelp {
	if m.layout.Compact {
		return append([]ui.KeyHelp{{Key: "tab", Desc: "summary / pods"}}, ui.FullHelpNodes()...)
	}
	return ui.FullHelpNodes()
}

func (nodesScreen) View(m Model, body draw.Region, ctx ui.Ctx) {
	ui.NodeSummary(region(body, m.layout.Node), m.nodeSummaryState(), ctx)
	ui.PodsPanel(region(body, m.layout.Pods), m.podsPanelState(m.visiblePods()), ctx)
}

func (m Model) selectedNode() string {
	if p := m.selectedPod(); p != nil {
		return p.Node
	}
	return ""
}

func (m Model) nodeScope() (string, bool) {
	if !m.onScreen(ScreenNodes) {
		return "", false
	}
	return m.nav.Active().Node, true
}

func (m Model) nodeSummaryState() ui.NodeSummaryState {
	name, _ := m.nodeScope()
	st := ui.NodeSummaryState{Name: name, Visible: len(m.visiblePods())}
	if name == "" {
		return st
	}
	for _, p := range m.snap.Pods {
		if p.Node == name {
			st.Observed++
		}
	}
	if m.snap.Node.Name == name {
		n := m.snap.Node
		st.Node = &n
	}
	if st.Node != nil && m.selectedPod() != nil {
		st.CPU, st.CPUKnown = m.metrics.NodeCPU, m.metrics.NodeCPUKnown
	}
	return st
}

package app

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type podScreen struct{}

func (podScreen) ID() ScreenID {
	return ScreenPodDetail
}

func (podScreen) Title() string {
	return "Pod"
}

func (podScreen) FullHelp(Model) []ui.KeyHelp {
	return ui.FullHelpDetail()
}

func (podScreen) ShowsDetails() bool {
	return true
}

func (podScreen) Init(Model) tea.Cmd {
	return nil
}

func (podScreen) Update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	return updateBody(m, msg)
}

func (podScreen) Help(Model) []ui.KeyHelp {
	return ui.PodDetailHelp()
}

func (podScreen) Keys(m Model) []key.Binding {
	k := m.keys
	return []key.Binding{
		k.Up, k.Down, k.Esc, k.Filter,
		k.TabLog, k.TabTop, k.TabDescribe, k.TabEvents, k.TabSecurity, k.SecurityAck,
		k.TabPrev, k.TabNext, k.Container, k.Previous, k.SearchNext, k.SearchPrev, k.EventScope,
		k.Palette, k.Pause, k.Help, k.Quit,
		k.PageUp, k.PageDown, k.Home, k.End, k.Follow, k.Wrap, k.Top, k.Bottom,
		k.Menu, k.MenuPrev, k.MenuNext, k.Dashboard, k.Workloads, k.Nodes, k.Events,
		k.Context, k.Namespace, k.Tree,
	}
}

func (podScreen) View(m Model, body draw.Region, ctx ui.Ctx) {
	pod := m.selectedPod()
	if pod == nil {
		ui.PodMissing(region(body, m.layout.Details), m.nav.Active().Pod, ctx)
		return
	}
	st := m.detailsState(pod)
	st.Title, st.Standalone = "Pod "+pod.Key(), true
	ui.DetailsPanel(region(body, m.layout.Details), st, ctx)
}

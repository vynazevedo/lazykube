package app

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type ScreenID int

const (
	ScreenDashboard ScreenID = iota
	ScreenPodDetail
	ScreenWorkloads
	ScreenNodes
	ScreenEvents
)

type Screen interface {
	ID() ScreenID
	Title() string
	Init(m Model) tea.Cmd
	Update(m Model, msg tea.Msg) (Model, tea.Cmd)
	View(m Model, body draw.Region, ctx ui.Ctx)
	Keys(m Model) []key.Binding
	ShowsDetails() bool
	Help(m Model) []ui.KeyHelp
	FullHelp(m Model) []ui.KeyHelp
}

func screenFor(id ScreenID) (Screen, bool) {
	switch id {
	case ScreenDashboard:
		return dashboardScreen{}, true
	case ScreenPodDetail:
		return podScreen{}, true
	case ScreenWorkloads:
		return workloadsScreen{}, true
	case ScreenNodes:
		return nodesScreen{}, true
	case ScreenEvents:
		return eventsScreen{}, true
	}
	return nil, false
}

func (r Route) screen() Screen {
	if r.Kind == RoutePod {
		return podScreen{}
	}
	if s, ok := screenFor(r.Screen); ok {
		return s
	}
	return dashboardScreen{}
}

func (m Model) screen() Screen {
	return m.nav.Active().screen()
}

func (m Model) detailTarget() string {
	if !m.screen().ShowsDetails() {
		return ""
	}
	return m.activeName()
}

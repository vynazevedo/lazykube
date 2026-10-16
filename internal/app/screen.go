package app

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type ScreenID int

const ScreenDashboard ScreenID = iota

type Screen interface {
	ID() ScreenID
	Init(m Model) tea.Cmd
	Update(m Model, msg tea.Msg) (Model, tea.Cmd)
	View(m Model, body draw.Region, ctx ui.Ctx)
	Keys(m Model) []key.Binding
}

func screenFor(id ScreenID) (Screen, bool) {
	switch id {
	case ScreenDashboard:
		return dashboardScreen{}, true
	}
	return nil, false
}

func (m Model) screen() Screen {
	if s, ok := screenFor(m.nav.Active().Screen); ok {
		return s
	}
	return dashboardScreen{}
}

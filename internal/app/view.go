package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func region(c draw.Region, r Rect) draw.Region {
	return c.Sub(r.X, r.Y, r.W, r.H)
}

func (m Model) Render() *draw.Canvas {
	c := draw.NewCanvas(m.w, m.h, theme.BgApp)
	screen := c.Region()
	if m.layout.TooSmall {
		ui.TooSmall(screen, m.w, m.h)
		return c
	}
	ctx := ui.Ctx{Pal: m.pal, Clock: m.clock}
	ui.Header(region(screen, m.layout.Header), m.headerState(), ctx)

	ui.Menu(region(screen, m.layout.Menu), ui.MenuState{Items: m.menuItems(), Active: m.menuActive(), Cursor: m.menu.cursor, Focused: m.menu.open}, ctx)
	ui.Breadcrumbs(region(screen, m.layout.Crumbs), crumbLabels(m.crumbs()), ctx)

	active := m.screen()
	active.View(m, screen, ctx)

	if m.dep.open {
		ui.DeploymentOverlay(screen, m.deploymentOverlayState(), ctx)
	}

	keys := active.Help(m)
	switch {
	case m.menu.open:
		keys = ui.MenuHelp()
	case m.dep.open:
		keys = ui.OverlayHelp()
	}
	ui.Footer(screen.Sub(0, m.layout.DividerY, m.w, 1), screen.Sub(0, m.layout.FooterY, m.w, 1), ui.FooterState{FPS: m.displayFPS(), Keys: keys})

	if m.helpOpen {
		ui.HelpOverlay(screen, active.FullHelp(m), ctx)
	}
	return c
}

func (m Model) displayFPS() float64 {
	if m.animated && !m.paused && !m.blurred && m.clock.FPS > 0 {
		return m.clock.FPS
	}
	return 0
}

func (m Model) View() tea.View {
	content := ""
	if m.w > 0 && m.h > 0 {
		content = m.Render().String()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = true
	v.BackgroundColor = theme.BgApp
	v.WindowTitle = "lazykube"
	return v
}

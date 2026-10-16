package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
	"github.com/TNK-Studio/lazykube/internal/ui"
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
	ui.Header(region(screen, m.layout.Header), ui.HeaderState{Cluster: m.snap.Cluster, MetricsOK: m.metrics.Available, SecurityAlerts: len(m.securitySession.Alerts)}, ctx)

	m.screen().View(m, screen, ctx)

	ui.Footer(screen.Sub(0, m.layout.DividerY, m.w, 1), screen.Sub(0, m.layout.FooterY, m.w, 1), ui.FooterState{FPS: m.displayFPS()})

	if m.helpOpen {
		ui.HelpOverlay(screen, ctx)
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

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
	pod := m.selectedPod()

	ui.Header(region(screen, m.layout.Header), ui.HeaderState{Cluster: m.snap.Cluster, MetricsOK: m.metrics.Available}, ctx)

	if m.layout.ShowLeft {
		ui.NodePanel(region(screen, m.layout.Node), ui.NodeState{
			Node:    m.snap.Node,
			CPU:     m.metrics.NodeCPU,
			CPUFrac: m.motion.nodeCPU.Pos,
			MemFrac: m.motion.nodeMem.Pos,
		}, ctx)
		highlight := ""
		if pod != nil {
			highlight = pod.Namespace
		}
		ui.NamespacesPanel(region(screen, m.layout.Namespaces), ui.NamespacesState{
			Items:     m.snap.Namespaces,
			Highlight: highlight,
			Filter:    m.nsFilter,
			Cursor:    m.nsCursor,
			Focused:   m.focus == FocusNamespaces,
		}, ctx)
		ui.DeploymentsPanel(region(screen, m.layout.Deployments), ui.DeploymentsState{
			Items:   m.snap.Deployments,
			Cursor:  m.depCursor,
			Focused: m.focus == FocusDeployments,
		}, ctx)
	}

	pods := m.visiblePods()
	sel := m.selectedIndex(pods)
	ui.PodsPanel(region(screen, m.layout.Pods), ui.PodsState{
		Pods:       pods,
		Selected:   sel,
		Offset:     m.podOffset,
		Hover:      m.hover,
		CPU:        m.metrics.PodCPU,
		Filter:     m.filter.Value(),
		FilterPos:  m.filter.Position(),
		Filtering:  m.filtering,
		Focused:    m.focus == FocusPods,
		SelectionK: m.selectionKs(pods),
		NSFilter:   m.nsFilter,
	}, ctx)

	ui.HeatmapPanel(region(screen, m.layout.Heatmap), ui.HeatmapState{
		Labels:    m.metrics.HeatLabels,
		Heat:      m.metrics.Heat,
		Available: m.metrics.Available,
		Reason:    m.metrics.Reason,
	}, ctx)

	ui.DetailsPanel(region(screen, m.layout.Details), ui.DetailsState{
		Tab:      m.tab,
		HoverTab: m.hoverTab,
		Pod:      pod,
		Metrics:  m.metrics,
		Logs:     m.logs,
		Events:   m.events,
		Manifest: m.manifest,
		Describe: m.describe,
		Focused:  m.focus == FocusDetails,
		Scroll:   m.scroll,
		Follow:   m.follow,
		NewLines: m.newLines,
		Wrap:     m.wrap,
		MemBar:   m.motion.memBar.Pos,
		Slide:    m.slideOffset(),
	}, ctx)

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

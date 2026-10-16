package ui

import (
	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

const (
	heatLabelW  = 14
	metricsDown = "metrics-server indisponível"
)

type HeatmapState struct {
	Labels    []string
	Heat      [][]float64
	Available bool
	Reason    string
}

func HeatCols(panelWidth int) int {
	return max(0, panelWidth-4-heatLabelW)
}

func HeatmapPanel(r draw.Region, st HeatmapState, ctx Ctx) {
	inner := Panel(r, "CPU heatmap · last 2 min", false, ctx)
	c := inner.Inset(1, 1, 1, 0)
	if c.Empty() {
		return
	}
	if !st.Available {
		centered(c, reasonOr(st.Reason, metricsDown))
		return
	}
	if len(st.Labels) == 0 {
		centered(c, "coletando amostras")
		return
	}
	grid := c.Sub(heatLabelW, 0, c.W-heatLabelW, c.H)
	maxRows := (c.H - 1) / 2
	for row, label := range st.Labels {
		if row >= maxRows {
			break
		}
		y := row * 2
		c.Text(0, y, draw.Truncate(label, heatLabelW-1), draw.Fg(theme.Muted))
		for col, k := range st.Heat[row] {
			if col >= grid.W {
				break
			}
			color := theme.Heat(ctx.Pal, k)
			grid.Put(col, y, '█', draw.Fg(color))
			grid.Put(col, y+1, '▀', draw.Fg(color).WithBg(theme.BgPanel))
		}
	}
	legendY := c.H - 1
	x := grid.Text(0, legendY, "-2m ", draw.Fg(theme.Muted))
	barW := grid.W - x - 4
	for i := 0; i < barW; i++ {
		k := 0.0
		if barW > 1 {
			k = float64(i) / float64(barW-1)
		}
		grid.Put(x+i, legendY, '█', draw.Fg(theme.Heat(ctx.Pal, k)))
	}
	grid.Text(x+max(0, barW), legendY, " now", draw.Fg(theme.Muted))
}

func centered(r draw.Region, msg string) {
	msg = draw.Truncate(msg, r.W)
	x := (r.W - draw.Width(msg)) / 2
	r.Text(max(0, x), r.H/2, msg, draw.Fg(theme.Muted))
}

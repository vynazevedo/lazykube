package ui

import (
	"fmt"
	"math"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

const (
	chartRows   = 9
	chartAxisW  = 6
	chartMaxCPU = 20.0
	cardHeight  = 5
	memScale    = 12.0
	rxScale     = 10.0
	txScale     = 6.0
)

func TopTab(r draw.Region, st DetailsState, ctx Ctx) {
	m := st.Metrics
	p := st.Pod
	title := draw.Line{
		muted("CPU"),
		bold(fmt.Sprintf(" %dm", m.CPUNow), ctx.Pal.C),
		muted(fmt.Sprintf(" req %dm · lim %dm", p.CPURequest, p.CPULimit)),
	}
	r.Line(0, 0, title)
	hint := draw.Line{muted("streamline · braille 2×4")}
	if r.W-hint.Width() > title.Width()+2 {
		r.LineRight(0, hint)
	}
	if !m.Available {
		centered(r.Sub(0, 1, r.W, chartRows+1), "metrics-server indisponível")
	} else {
		CPUChart(r.Sub(0, 1, r.W, chartRows+1), m.CPU, float64(p.CPURequest), ctx)
	}

	cardsY := 1 + chartRows + 2
	if r.H >= cardsY+cardHeight {
		left := (r.W - 1) / 2
		memoryCard(r.Sub(0, cardsY, left, cardHeight), m, p, ctx)
		networkCard(r.Sub(left+1, cardsY, r.W-left-1, cardHeight), m)
	}

	logsY := cardsY + cardHeight + 1
	if r.H-logsY >= 3 {
		miniLogs(r.Sub(0, logsY, r.W, r.H-logsY), st.Logs, ctx)
	}
}

func cpuToDot(v float64, dotH int) int {
	py := int(math.Round((1 - theme.Clamp(v, 0, chartMaxCPU)/chartMaxCPU) * float64(dotH-1)))
	return max(0, min(dotH-1, py))
}

func CPUChart(r draw.Region, samples []float64, request float64, ctx Ctx) {
	labels := map[int]string{0: "20m", 2: "15m", 4: "10m", 6: "5m", 8: "0"}
	axis := draw.Fg(theme.Border)
	for row := 0; row < chartRows; row++ {
		if l, ok := labels[row]; ok {
			r.Text(0, row, draw.PadLeft(l, chartAxisW-2), draw.Fg(theme.Muted))
		}
		r.Put(chartAxisW-1, row, '│', axis)
	}
	r.Put(chartAxisW-1, chartRows, '└', axis)
	r.HLine(chartAxisW, chartRows, r.W-chartAxisW, '─', axis)

	plot := r.Sub(chartAxisW, 0, r.W-chartAxisW, chartRows)
	cols := plot.W
	if cols <= 0 || len(samples) < 2 {
		return
	}
	b := draw.NewBraille(cols, chartRows)
	dw, dh := b.DotWidth(), b.DotHeight()
	n := len(samples)
	xs := make([]int, n)
	ys := make([]int, n)
	topRow := chartRows - 1
	for i, v := range samples {
		xs[i] = int(math.Round(float64(i) * float64(dw-1) / float64(n-1)))
		ys[i] = cpuToDot(v, dh)
		topRow = min(topRow, ys[i]/4)
	}
	areaColor := func(row int) theme.RGB {
		depth := 0.0
		if span := chartRows - 1 - topRow; span > 0 {
			depth = theme.Clamp(float64(row-topRow)/float64(span), 0, 1)
		}
		return theme.Mix(theme.BgPanel, ctx.Pal.C, 0.45*(1-depth))
	}
	for i := 0; i+1 < n; i++ {
		x0, x1 := xs[i], xs[i+1]
		for x := x0; x <= x1; x++ {
			k := 0.0
			if x1 > x0 {
				k = float64(x-x0) / float64(x1-x0)
			}
			top := int(math.Round(float64(ys[i]) + (float64(ys[i+1])-float64(ys[i]))*k))
			for y := top + 1; y < dh; y++ {
				b.Set(x, y, areaColor(y/4))
			}
		}
	}
	lineColor := func(px int) theme.RGB {
		return theme.Linear(ctx.Pal.B, ctx.Pal.C, px/2, cols)
	}
	for i := 0; i+1 < n; i++ {
		b.Line(xs[i], ys[i], xs[i+1], ys[i+1], lineColor)
	}

	requestRow := cpuToDot(request, dh) / 4
	for row := 0; row < chartRows; row++ {
		for col := 0; col < cols; col++ {
			switch {
			case b.Has(col, row):
				plot.Put(col, row, b.Rune(col, row), draw.Fg(b.Color(col, row)))
			case row == requestRow:
				plot.Put(col, row, '╌', draw.Fg(ctx.Pal.A))
			case row == 2 || row == 6:
				plot.Put(col, row, '─', draw.Fg(theme.BorderSoft))
			}
		}
	}
	plot.Put(xs[n-1]/2, ys[n-1]/4, '●', draw.Fg(ctx.Pal.C))
	if requestRow > 0 {
		label := fmt.Sprintf("request %dm", int(request))
		plot.Text(plot.W-len(label), requestRow-1, label, draw.Fg(ctx.Pal.A).WithBg(theme.BgPanel))
	}
}

func cardBox(r draw.Region) draw.Region {
	return Box(r, theme.BgCard, theme.BorderSoft, theme.BgPanel, nil, nil).Inset(1, 0, 1, 0)
}

func tail(values []float64, n int) []float64 {
	if n <= 0 {
		return nil
	}
	if len(values) <= n {
		return values
	}
	return values[len(values)-n:]
}

func memoryCard(r draw.Region, m data.Metrics, p *data.Pod, ctx Ctx) {
	c := cardBox(r)
	if c.Empty() {
		return
	}
	limit := p.MemLimit
	c.Line(0, 0, draw.Fit(c.W,
		draw.Line{muted("Memory"), bold(fmt.Sprintf(" %dMi", m.MemNow), ctx.Pal.A), muted(fmt.Sprintf(" / %dMi", limit))},
		draw.Line{muted("Mem"), bold(fmt.Sprintf(" %dMi", m.MemNow), ctx.Pal.A), muted(fmt.Sprintf("/%dMi", limit))},
	))
	frac := 0.0
	if limit > 0 {
		frac = float64(m.MemNow) / float64(limit)
	}
	Bar(c, 0, 1, c.W, frac, ctx.Pal.B, ctx.Pal.A)
	c.Text(0, 2, draw.Sparkline(tail(m.Mem, c.W), memScale), draw.Fg(ctx.Pal.A))
}

func networkCard(r draw.Region, m data.Metrics) {
	c := cardBox(r)
	if c.Empty() {
		return
	}
	if len(m.RX) == 0 {
		c.Line(0, 0, draw.Line{muted("Network "), muted("n/a")})
		return
	}
	rx := fmt.Sprintf("%.1f kB/s", m.RXNow)
	tx := fmt.Sprintf("%.1f kB/s", m.TXNow)
	c.Line(0, 0, draw.Fit(c.W,
		draw.Line{muted("Network"), colored(" ↓ "+rx, theme.OK), colored(" ↑ "+tx, theme.Warn)},
		draw.Line{muted("Network"), colored(" ↓"+rx, theme.OK), colored(" ↑"+tx, theme.Warn)},
		draw.Line{muted("Net"), colored(" ↓"+rx, theme.OK), colored(" ↑"+tx, theme.Warn)},
	))
	c.Text(0, 1, draw.Sparkline(tail(m.RX, c.W), rxScale), draw.Fg(theme.OK))
	c.Text(0, 2, draw.Sparkline(tail(m.TX, c.W), txScale), draw.Fg(theme.Warn))
}

func miniLogs(r draw.Region, logs []data.LogLine, ctx Ctx) {
	c := Box(r, theme.BgInset, theme.BorderSoft, theme.BgPanel,
		draw.Line{muted("logs · tail -f")}, draw.Line{muted("viewport")}).Inset(1, 0, 1, 0)
	start := max(0, len(logs)-c.H)
	for i := 0; i < c.H && start+i < len(logs); i++ {
		logLine(c, i, logs[start+i], ctx)
	}
}

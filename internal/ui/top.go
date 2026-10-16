package ui

import (
	"fmt"
	"math"
	"strconv"

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
	if collecting(m) {
		hint = draw.Line{muted(fmt.Sprintf("coletando %d/%d", m.Samples, m.Window))}
	}
	if r.W-hint.Width() > title.Width()+2 {
		r.LineRight(0, hint)
	}
	chart := r.Sub(0, 1, r.W, chartRows+1)
	switch {
	case !m.Available:
		centered(chart, reasonOr(m.Reason, metricsDown))
	default:
		CPUChart(chart, m.CPU, float64(p.CPURequest), ctx)
		if len(m.CPU) < 2 {
			centered(chart.Sub(chartAxisW, 0, chart.W-chartAxisW, chartRows), fmt.Sprintf("coletando amostras %d/%d", m.Samples, m.Window))
		}
	}

	cardsY := 1 + chartRows + 2
	if r.H >= cardsY+cardHeight {
		left := (r.W - 1) / 2
		memoryCard(r.Sub(0, cardsY, left, cardHeight), m, p, st.MemBar, ctx)
		networkCard(r.Sub(left+1, cardsY, r.W-left-1, cardHeight), m)
	}

	logsY := cardsY + cardHeight + 1
	if r.H-logsY >= 3 {
		miniLogs(r.Sub(0, logsY, r.W, r.H-logsY), st.Logs, ctx)
	}
}

func collecting(m data.Metrics) bool {
	return m.Available && m.Window > 0 && m.Samples < m.Window
}

func reasonOr(reason, fallback string) string {
	if reason != "" {
		return reason
	}
	return fallback
}

func cpuToDot(v, top float64, dotH int) int {
	py := int(math.Round((1 - theme.Clamp(v, 0, top)/top) * float64(dotH-1)))
	return max(0, min(dotH-1, py))
}

func chartScale(samples []float64, request float64) float64 {
	top := request
	for _, v := range samples {
		top = math.Max(top, v)
	}
	if top <= chartMaxCPU {
		return chartMaxCPU
	}
	exp := math.Pow(10, math.Floor(math.Log10(top)))
	for _, k := range []float64{1, 2, 4, 6, 8} {
		if k*exp >= top {
			return k * exp
		}
	}
	return 10 * exp
}

func milliLabel(v float64) string {
	if v == 0 {
		return "0"
	}
	if v >= 1000 {
		return strconv.FormatFloat(v/1000, 'g', 2, 64) + "c"
	}
	return fmt.Sprintf("%dm", int(math.Round(v)))
}

func scaleOr(v, fallback float64) float64 {
	if v > 0 {
		return v
	}
	return fallback
}

func CPUChart(r draw.Region, samples []float64, request float64, ctx Ctx) {
	top := chartScale(samples, request)
	labels := map[int]string{}
	for row := 0; row < chartRows; row += 2 {
		labels[row] = milliLabel(top * float64(chartRows-1-row) / float64(chartRows-1))
	}
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
		ys[i] = cpuToDot(v, top, dh)
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

	requestRow := cpuToDot(request, top, dh) / 4
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

func memoryCard(r draw.Region, m data.Metrics, p *data.Pod, bar float64, ctx Ctx) {
	c := cardBox(r)
	if c.Empty() {
		return
	}
	limit := "no limit"
	if p.MemLimit > 0 {
		limit = fmt.Sprintf("%dMi", p.MemLimit)
	}
	c.Line(0, 0, draw.Fit(c.W,
		draw.Line{muted("Memory"), bold(fmt.Sprintf(" %dMi", m.MemNow), ctx.Pal.A), muted(" / " + limit)},
		draw.Line{muted("Mem"), bold(fmt.Sprintf(" %dMi", m.MemNow), ctx.Pal.A), muted("/" + limit)},
	))
	Bar(c, 0, 1, c.W, bar, ctx.Pal.B, ctx.Pal.A)
	c.Text(0, 2, draw.Sparkline(tail(m.Mem, c.W), scaleOr(m.MemMax, memScale)), draw.Fg(ctx.Pal.A))
}

func networkCard(r draw.Region, m data.Metrics) {
	c := cardBox(r)
	if c.Empty() {
		return
	}
	if len(m.RX) == 0 {
		c.Line(0, 0, draw.Line{muted("Network "), muted(draw.Truncate(reasonOr(m.NetReason, "n/a"), max(0, c.W-8)))})
		return
	}
	rxColor, txColor := theme.OK, theme.Warn
	if m.NetStale {
		rxColor, txColor = theme.Muted, theme.Muted
		reason := "obsoleto: " + reasonOr(m.NetReason, "sem coleta recente")
		c.Line(0, 0, draw.Line{muted("Network "), colored(draw.Truncate(reason, max(0, c.W-8)), theme.Warn)})
	} else {
		rx := fmt.Sprintf("%.1f kB/s", m.RXNow)
		tx := fmt.Sprintf("%.1f kB/s", m.TXNow)
		c.Line(0, 0, draw.Fit(c.W,
			draw.Line{muted("Network"), colored(" ↓ "+rx, theme.OK), colored(" ↑ "+tx, theme.Warn)},
			draw.Line{muted("Network"), colored(" ↓"+rx, theme.OK), colored(" ↑"+tx, theme.Warn)},
			draw.Line{muted("Net"), colored(" ↓"+rx, theme.OK), colored(" ↑"+tx, theme.Warn)},
		))
	}
	c.Text(0, 1, draw.Sparkline(tail(m.RX, c.W), scaleOr(m.RXMax, rxScale)), draw.Fg(rxColor))
	c.Text(0, 2, draw.Sparkline(tail(m.TX, c.W), scaleOr(m.TXMax, txScale)), draw.Fg(txColor))
}

func miniLogs(r draw.Region, logs []data.LogLine, ctx Ctx) {
	c := Box(r, theme.BgInset, theme.BorderSoft, theme.BgPanel,
		draw.Line{muted("logs · tail -f")}, draw.Line{muted("viewport")}).Inset(1, 0, 1, 0)
	start := max(0, len(logs)-c.H)
	for i := 0; i < c.H && start+i < len(logs); i++ {
		logLine(c, i, logs[start+i], ctx)
	}
}

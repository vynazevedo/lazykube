package ui

import (
	"fmt"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

const (
	PodsRowsTop  = 4
	podsFixedW   = 37
	podsNSW      = 11
	podsRSTW     = 4
	podsAgeW     = 5
	podsCPUW     = 7
	podsStateW   = 11
	podsCPUScale = 20.0
)

type PodsState struct {
	Security   map[string]data.PostureScore
	Pods       []data.Pod
	Selected   int
	Offset     int
	Hover      int
	CPU        map[string]int
	Filter     string
	FilterPos  int
	Filtering  bool
	Focused    bool
	SelectionK map[int]float64
	NSFilter   string
}

func StatusGlyph(s data.PodStatus, ctx Ctx) (rune, theme.RGB) {
	switch s {
	case data.StatusRunning:
		return '●', theme.OK
	case data.StatusCompleted:
		return '○', theme.DimGlyph
	case data.StatusPending, data.StatusContainerCreating:
		return ctx.Clock.Spinner(), theme.Warn
	case data.StatusFailed:
		return '●', theme.Err
	default:
		return '●', theme.Muted
	}
}

func StatusColor(s data.PodStatus) theme.RGB {
	switch s {
	case data.StatusRunning, data.StatusCompleted:
		return theme.OK
	case data.StatusPending, data.StatusContainerCreating:
		return theme.Warn
	case data.StatusFailed:
		return theme.Err
	default:
		return theme.Muted
	}
}

func PodReason(p data.Pod) string {
	if p.Reason != "" {
		return p.Reason
	}
	return p.Status.String()
}

func PodStateLabel(p data.Pod) (string, theme.RGB, bool) {
	switch {
	case p.Status == data.StatusRunning && p.Containers > 0 && p.Ready < p.Containers:
		return fmt.Sprintf("ready %d/%d", p.Ready, p.Containers), theme.Warn, true
	case p.Status == data.StatusRunning:
		return "", theme.OK, false
	case p.Status == data.StatusCompleted:
		return PodReason(p), theme.DimGlyph, true
	default:
		return PodReason(p), StatusColor(p.Status), true
	}
}

func PodsVisibleRows(panelHeight int) int {
	return max(0, panelHeight-2-PodsRowsTop)
}

func PodsPanel(r draw.Region, st PodsState, ctx Ctx) {
	title := "Pods"
	if st.Focused {
		title = "Pods · focus"
	}
	inner := Panel(r, title, st.Focused, ctx)
	c := inner.Inset(1, 1, 1, 0)
	if c.Empty() {
		return
	}
	filterLine(c.Sub(0, 0, c.W, 1), st, ctx)

	nameW := max(4, c.W-podsFixedW)
	nsX := 2 + nameW + 1
	rstX := nsX + podsNSW + 1
	ageX := rstX + podsRSTW + 1
	cpuX := ageX + podsAgeW + 1
	hdr := draw.Fg(theme.Muted)
	c.Text(2, 2, "NAME", hdr)
	c.Text(nsX, 2, "NAMESPACE", hdr)
	c.Text(rstX, 2, draw.PadLeft("RST", podsRSTW), hdr)
	c.Text(ageX, 2, draw.PadLeft("SEC", podsAgeW), hdr)
	c.Text(cpuX, 2, "CPU/STATUS", hdr)

	rows := c.H - 3
	for row := 0; row < rows; row++ {
		i := st.Offset + row
		if i >= len(st.Pods) {
			break
		}
		p := st.Pods[i]
		y := 3 + row
		k := st.SelectionK[i]
		switch {
		case k > 0:
			inner.Sub(0, 1+y, inner.W, 1).FillBg(SelectionBg(ctx.Pal, k))
		case i == st.Hover:
			inner.Sub(0, 1+y, inner.W, 1).FillBg(theme.Hover)
		}
		glyph, gc := StatusGlyph(p.Status, ctx)
		c.Put(0, y, glyph, draw.Fg(gc))
		nameStyle := draw.Fg(theme.Muted)
		if p.Status == data.StatusRunning {
			nameStyle = draw.Fg(theme.Text)
		}
		if i == st.Selected {
			nameStyle = draw.Fg(theme.White).WithBold()
		}
		c.Text(2, y, draw.Truncate(p.Name, nameW), nameStyle)
		c.Text(nsX, y, draw.Truncate(p.Namespace, podsNSW), draw.Fg(theme.Muted))
		rstColor := theme.Muted
		if p.Restarts > 0 {
			rstColor = theme.Warn
		}
		c.Text(rstX, y, draw.PadLeft(fmt.Sprint(p.Restarts), podsRSTW), draw.Fg(rstColor))
		score := "-"
		if s := st.Security[p.Key()]; s.Available {
			score = fmt.Sprintf("%d %s", s.Value, s.Grade)
			if s.Partial {
				score = fmt.Sprintf("%d%sp", s.Value, s.Grade)
			}
		}
		c.Text(ageX, y, draw.PadLeft(score, podsAgeW), draw.Fg(theme.Muted))
		if label, color, ok := PodStateLabel(p); ok {
			c.Text(cpuX, y, draw.Truncate(label, podsStateW), draw.Fg(color))
			continue
		}
		cpu, known := st.CPU[p.Key()]
		if !known {
			c.Text(cpuX, y, "n/a", draw.Fg(theme.Muted))
			continue
		}
		full := draw.Filled(float64(cpu), podsCPUScale, podsCPUW)
		c.Text(cpuX, y, strings.Repeat("█", full)+strings.Repeat("░", podsCPUW-full), draw.Fg(ctx.Pal.C))
	}
	if len(st.Pods) == 0 {
		c.Text(2, 3, "no pods match the filter", draw.Fg(theme.Muted))
	}
}

func filterLine(r draw.Region, st PodsState, ctx Ctx) {
	r.FillBg(theme.BgChip)
	x := r.Text(0, 0, "/", draw.Fg(ctx.Pal.A))
	x += r.Text(x, 0, " ", draw.Fg(theme.Muted))
	cursorOn := (st.Filtering || st.Focused) && ctx.Clock.CursorVisible()
	count := fmt.Sprintf("%d items", len(st.Pods))
	if st.NSFilter != "" {
		count = "ns:" + st.NSFilter + "  " + count
	}
	maxText := r.W - x - draw.Width(count) - 2
	if st.Filter == "" && !st.Filtering {
		x += r.Text(x, 0, draw.Truncate("filter pods…", maxText), draw.Fg(theme.Muted))
		if cursorOn {
			r.Put(x, 0, '▌', draw.Fg(ctx.Pal.A))
		}
	} else {
		filterValue(r, x, maxText, st, cursorOn, ctx)
	}
	r.LineRight(0, draw.Line{muted(count)})
}

func filterValue(r draw.Region, x, maxWidth int, st PodsState, cursorOn bool, ctx Ctx) int {
	gs := draw.Graphemes(st.Filter)
	cursor := len(gs)
	runes := 0
	for i, g := range gs {
		if runes >= st.FilterPos {
			cursor = i
			break
		}
		runes += len([]rune(g.Text))
	}
	start, used := len(gs), 1
	for start > 0 && used+gs[start-1].Width <= maxWidth {
		start--
		used += gs[start].Width
	}
	if cursor < start {
		start = cursor
	}
	n := 0
	for i := start; i < len(gs) && n+gs[i].Width < maxWidth; i++ {
		cs := draw.Fg(theme.Text)
		if i == cursor && cursorOn {
			cs = draw.Fg(theme.BgApp).WithBg(ctx.Pal.A)
		}
		n += r.Text(x+n, 0, gs[i].Text, cs)
	}
	if cursor >= len(gs) && cursorOn {
		r.Put(x+n, 0, '▌', draw.Fg(ctx.Pal.A))
		n++
	}
	return n
}

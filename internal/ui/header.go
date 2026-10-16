package ui

import (
	"fmt"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

const Version = "v2.0"

type HeaderState struct {
	Cluster   data.Cluster
	MetricsOK bool
}

func Header(r draw.Region, st HeaderState, ctx Ctx) {
	r.Fill(theme.BgApp)
	logo := "lazykube"
	colors := theme.TextGradient(ctx.Pal, len(logo), ctx.Clock.Phase)
	x := 0
	for i, ch := range logo {
		r.Put(x+i, 0, ch, draw.Fg(colors[i]).WithBold())
	}
	x += len(logo)
	x += r.Line(x, 0, draw.Line{muted("  " + Version + "  ")})

	pill := statusPill(st, ctx)
	right := r.W - pill.Width()

	chip := theme.BgChip
	ctxPill := draw.Line{
		draw.S(" ◎ ", draw.Fg(ctx.Pal.C).WithBg(chip)),
		draw.S("context ", draw.Fg(theme.Muted).WithBg(chip)),
		draw.S(st.Cluster.Context+" ", draw.Fg(ctx.Pal.C).WithBg(chip).WithBold()),
	}
	ctxPill = ctxPill.Truncate(max(0, right-x-2))
	x += r.Line(x, 0, ctxPill)

	c := st.Cluster
	stat := func(label, value string) draw.Line {
		return draw.Line{muted("  " + label + " "), text(value)}
	}
	var stats draw.Line
	for _, s := range []draw.Line{
		stat("k8s", c.Version),
		stat("nodes", fmt.Sprint(c.Nodes)),
		stat("ns", fmt.Sprint(c.Namespaces)),
		stat("pods", fmt.Sprint(c.Pods)),
		stat("uptime", c.Uptime),
	} {
		if x+stats.Width()+s.Width() > right-1 {
			break
		}
		stats = append(stats, s...)
	}
	r.Line(x, 0, stats)
	r.Line(right, 0, pill)
}

func statusPill(st HeaderState, ctx Ctx) draw.Line {
	bg := theme.BgChip2
	dot, label := theme.OK, "live"
	if !st.MetricsOK {
		dot, label = theme.Err, "stale"
	}
	return draw.Line{
		draw.S(" "+string(ctx.Clock.Spinner()), draw.Fg(ctx.Pal.A).WithBg(bg)),
		draw.S(" metrics", draw.Fg(theme.TextMid).WithBg(bg)),
		draw.S(" every 5s", draw.Fg(theme.Muted).WithBg(bg)),
		draw.S(" ●", draw.Fg(theme.Pulse(dot, ctx.Clock.Phase)).WithBg(bg)),
		draw.S(" "+label+" ", draw.Fg(dot).WithBg(bg)),
	}
}

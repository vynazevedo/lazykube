package ui

import (
	"fmt"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

var Version = "v2.0"

type HeaderState struct {
	ReadOnly       bool
	Writable       bool
	Protected      bool
	Synthetic      bool
	Cluster        data.Cluster
	MetricsOK      bool
	SecurityAlerts int
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
	x += r.Line(x, 0, headerBadges(st))
	x += r.Line(x, 0, draw.Line{muted("  " + Version + "  ")})

	pill := headerPill(st, ctx)
	g := HeaderLayout(st, r.W)
	right := g.Right
	if g.End > g.CtxStart {
		r.Line(g.CtxStart, 0, headerTarget(g, ctx))
	}
	x = max(x, g.End)

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

const (
	HeaderNone = iota
	HeaderContext
	HeaderNamespace
)

func headerNamespace(c data.Cluster) string {
	if c.Namespace == "" {
		return data.AllNamespacesLabel
	}
	return draw.Sanitize(c.Namespace)
}

const (
	headerCtxLabel = " ◎ context "
	headerNsLabel  = "namespace "
	headerMinNs    = 6
)

type HeaderGeometry struct {
	CtxStart  int
	NsStart   int
	End       int
	Right     int
	Context   string
	Namespace string
}

func headerPill(st HeaderState, ctx Ctx) draw.Line {
	pill := statusPill(st, ctx)
	if st.SecurityAlerts > 0 {
		pill = append(draw.Line{draw.S(fmt.Sprintf("! %d high new  ", st.SecurityAlerts), draw.Fg(theme.Err).WithBold())}, pill...)
	}
	return pill
}

func headerBadges(st HeaderState) draw.Line {
	var l draw.Line
	if st.Synthetic {
		l = append(l, draw.S(" [DEMO]", draw.Fg(theme.Warn).WithBold()))
	}
	switch {
	case st.ReadOnly:
		l = append(l, draw.S(" [RO]", draw.Fg(theme.Warn).WithBold()))
	case st.Writable:
		l = append(l, draw.S(" [RW]", draw.Fg(theme.Err).WithBold()))
	}
	if st.Protected {
		l = append(l, draw.S(" [PROTECTED]", draw.Fg(theme.Err).WithBold()))
	}
	return l
}

func HeaderLayout(st HeaderState, w int) HeaderGeometry {
	start := len("lazykube") + headerBadges(st).Width() + draw.Width("  "+Version+"  ")
	g := HeaderGeometry{CtxStart: start, NsStart: start, End: start}
	g.Right = w - headerPill(st, Ctx{Pal: theme.DefaultPalette(), Clock: anim.Fixed(0, 0, 0)}).Width()
	room := g.Right - start - 2 - draw.Width(headerCtxLabel) - draw.Width(headerNsLabel) - 2
	if room < 2 {
		return g
	}
	name, ns := draw.Sanitize(st.Cluster.Context), headerNamespace(st.Cluster)
	cw, nw := draw.Width(name), draw.Width(ns)
	if cw+nw > room {
		nsW := min(nw, max(headerMinNs, room*2/5), room-1)
		ctxW := room - nsW
		if cw < ctxW {
			ctxW, nsW = cw, min(nw, room-cw)
		}
		name, ns = draw.Truncate(name, ctxW), draw.Truncate(ns, nsW)
	}
	g.Context, g.Namespace = name, ns
	g.NsStart = start + draw.Width(headerCtxLabel+name+" ")
	g.End = g.NsStart + draw.Width(headerNsLabel+ns+" ")
	return g
}

func headerTarget(g HeaderGeometry, ctx Ctx) draw.Line {
	chip := theme.BgChip
	return draw.Line{
		draw.S(headerCtxLabel[:len(" ◎ ")], draw.Fg(ctx.Pal.C).WithBg(chip)),
		draw.S("context ", draw.Fg(theme.Muted).WithBg(chip)),
		draw.S(g.Context+" ", draw.Fg(ctx.Pal.C).WithBg(chip).WithBold()),
		draw.S(headerNsLabel, draw.Fg(theme.Muted).WithBg(chip)),
		draw.S(g.Namespace+" ", draw.Fg(ctx.Pal.C).WithBg(chip).WithBold()),
	}
}

func HeaderTargetAt(st HeaderState, w, x int) int {
	g := HeaderLayout(st, w)
	switch {
	case x >= g.CtxStart && x < g.NsStart:
		return HeaderContext
	case x >= g.NsStart && x < g.End:
		return HeaderNamespace
	}
	return HeaderNone
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

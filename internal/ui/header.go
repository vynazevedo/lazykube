package ui

import (
	"fmt"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

type HeaderState struct {
	ReadOnly       bool
	Writable       bool
	Protected      bool
	ConfigInvalid  bool
	AuditBlocked   bool
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
	g := HeaderLayout(st, r.W)
	x += r.Line(x, 0, headerBadges(st, g.form))
	x += r.Line(x, 0, draw.Line{muted(g.form.versionText(r.W))})

	pill := headerPill(st, ctx, g.form)
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
	headerCtxMark  = " ◎ "
	headerCtxLabel = headerCtxMark + "context "
	headerNsLabel  = "namespace "
	headerNsShort  = "ns "
	headerMinCtx   = 8
	headerMinNs    = 6
	headerMaxAlert = 99
)

type headerForm struct {
	version     bool
	shortBadges bool
	shortPill   bool
	shortAlerts bool
	shortLabels bool
}

func headerForms(w int) []headerForm {
	if w < headerVersionFrom {
		return []headerForm{
			{shortBadges: true, shortPill: true},
			{shortBadges: true, shortPill: true, shortAlerts: true},
		}
	}
	return []headerForm{
		{version: true},
		{},
		{shortBadges: true},
		{shortBadges: true, shortPill: true},
		{shortBadges: true, shortPill: true, shortAlerts: true},
	}
}

func (f headerForm) versionText(w int) string {
	if !f.version {
		return " "
	}
	return headerVersion(w)
}

func (f headerForm) labels() (string, string) {
	if f.shortLabels {
		return headerCtxMark, headerNsShort
	}
	return headerCtxLabel, headerNsLabel
}

type HeaderGeometry struct {
	CtxStart  int
	NsStart   int
	End       int
	Right     int
	Context   string
	Namespace string
	form      headerForm
}

func headerPill(st HeaderState, ctx Ctx, f headerForm) draw.Line {
	pill := statusPill(st, ctx, f.shortPill)
	if st.SecurityAlerts > 0 {
		pill = append(draw.Line{draw.S(headerAlerts(st.SecurityAlerts, f.shortAlerts), draw.Fg(theme.Err).WithBold())}, pill...)
	}
	return pill
}

func headerAlerts(n int, short bool) string {
	switch {
	case !short:
		return fmt.Sprintf("! %d high new  ", n)
	case n > headerMaxAlert:
		return fmt.Sprintf("!%d+ ", headerMaxAlert)
	}
	return fmt.Sprintf("!%d ", n)
}

func headerBadges(st HeaderState, f headerForm) draw.Line {
	var l draw.Line
	if st.Synthetic {
		l = append(l, draw.S(" [DEMO]", draw.Fg(theme.Warn).WithBold()))
	}
	switch {
	case st.ReadOnly:
		l = append(l, draw.S(" [RO]", draw.Fg(theme.Warn).WithBold()))
	case st.Writable:
		l = append(l, draw.S(f.badge(" [RW: --read-write]", " [RW]"), draw.Fg(theme.Err).WithBold()))
	}
	switch {
	case st.ConfigInvalid:
		l = append(l, draw.S(f.badge(" [CONFIG INVALIDA]", " [CONFIG]"), draw.Fg(theme.Err).WithBold()))
	case st.Protected:
		l = append(l, draw.S(" [PROTECTED]", draw.Fg(theme.Err).WithBold()))
	}
	if st.AuditBlocked {
		l = append(l, draw.S(f.badge(" [ACOES BLOQUEADAS: AUDIT]", " [AUDIT]"), draw.Fg(theme.Err).WithBold()))
	}
	return l
}

const headerVersionFrom = 100

func (f headerForm) badge(full, short string) string {
	if f.shortBadges {
		return short
	}
	return full
}

func headerVersion(w int) string {
	if w < headerVersionFrom {
		return " "
	}
	return "  " + draw.Truncate(Version, versionHeaderMax) + "  "
}

func (f headerForm) frame(st HeaderState, w int) (int, int, int) {
	start := len("lazykube") + headerBadges(st, f).Width() + draw.Width(f.versionText(w))
	right := w - headerPill(st, Ctx{Pal: theme.DefaultPalette(), Clock: anim.Fixed(0, 0, 0)}, f).Width()
	ctxLabel, nsLabel := f.labels()
	return start, right, right - start - 2 - draw.Width(ctxLabel) - draw.Width(nsLabel) - 2
}

func headerFit(st HeaderState, w, need int) headerForm {
	forms := headerForms(w)
	for _, f := range forms {
		if _, _, room := f.frame(st, w); room >= need {
			return f
		}
	}
	last := forms[len(forms)-1]
	if _, _, room := last.frame(st, w); room < headerMinCtx+headerMinNs {
		last.shortLabels = true
	}
	return last
}

func HeaderLayout(st HeaderState, w int) HeaderGeometry {
	name, ns := draw.Sanitize(st.Cluster.Context), headerNamespace(st.Cluster)
	cw, nw := draw.Width(name), draw.Width(ns)
	f := headerFit(st, w, cw+nw)
	start, right, room := f.frame(st, w)
	g := HeaderGeometry{CtxStart: start, NsStart: start, End: start, Right: right, form: f}
	if room < 2 {
		return g
	}
	if cw+nw > room {
		ctxW := min(cw, room-min(nw, headerMinNs, room/2))
		name, ns = truncateMiddle(name, ctxW), truncateMiddle(ns, min(nw, room-ctxW))
	}
	ctxLabel, nsLabel := f.labels()
	g.Context, g.Namespace = name, ns
	g.NsStart = start + draw.Width(ctxLabel+name+" ")
	g.End = g.NsStart + draw.Width(nsLabel+ns+" ")
	return g
}

func truncateMiddle(s string, width int) string {
	if draw.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return draw.Truncate(s, width)
	}
	gs := draw.Graphemes(s)
	head, used := "", 0
	for _, g := range gs {
		if used+g.Width > (width-1)/3 {
			break
		}
		head, used = head+g.Text, used+g.Width
	}
	return head + "…" + fieldTail(s, width-1-used)
}

func headerTarget(g HeaderGeometry, ctx Ctx) draw.Line {
	chip := theme.BgChip
	ctxLabel, nsLabel := g.form.labels()
	line := draw.Line{draw.S(headerCtxMark, draw.Fg(ctx.Pal.C).WithBg(chip))}
	if rest := ctxLabel[len(headerCtxMark):]; rest != "" {
		line = append(line, draw.S(rest, draw.Fg(theme.Muted).WithBg(chip)))
	}
	return append(line,
		draw.S(g.Context+" ", draw.Fg(ctx.Pal.C).WithBg(chip).WithBold()),
		draw.S(nsLabel, draw.Fg(theme.Muted).WithBg(chip)),
		draw.S(g.Namespace+" ", draw.Fg(ctx.Pal.C).WithBg(chip).WithBold()),
	)
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

func statusPill(st HeaderState, ctx Ctx, narrow bool) draw.Line {
	bg := theme.BgChip2
	dot, label := theme.OK, "live"
	if !st.MetricsOK {
		dot, label = theme.Err, "stale"
	}
	line := draw.Line{draw.S(" "+string(ctx.Clock.Spinner()), draw.Fg(ctx.Pal.A).WithBg(bg))}
	if !narrow {
		line = append(line,
			draw.S(" metrics", draw.Fg(theme.TextMid).WithBg(bg)),
			draw.S(" every 5s", draw.Fg(theme.Muted).WithBg(bg)),
		)
	}
	return append(line,
		draw.S(" ●", draw.Fg(theme.Pulse(dot, ctx.Clock.Phase)).WithBg(bg)),
		draw.S(" "+label+" ", draw.Fg(dot).WithBg(bg)),
	)
}

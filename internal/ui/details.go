package ui

import (
	"fmt"
	"strings"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

type Tab int

const (
	TabLog Tab = iota
	TabConfig
	TabTop
	TabDescribe
	TabEvents
)

var tabNames = []string{"Log", "Config", "Top", "Describe", "Events"}

func (t Tab) String() string {
	if t < 0 || int(t) >= len(tabNames) {
		return ""
	}
	return tabNames[t]
}

func Tabs() []Tab {
	return []Tab{TabLog, TabConfig, TabTop, TabDescribe, TabEvents}
}

func (t Tab) Next(delta int) Tab {
	n := len(tabNames)
	return Tab(((int(t)+delta)%n + n) % n)
}

type TabSpan struct {
	Tab  Tab
	X, W int
}

func TabSpans() []TabSpan {
	out := make([]TabSpan, 0, len(tabNames))
	x := 0
	for i, name := range tabNames {
		w := len(name) + 2
		out = append(out, TabSpan{Tab: Tab(i), X: x, W: w})
		x += w + 1
	}
	return out
}

func TabAt(x int) (Tab, bool) {
	for _, s := range TabSpans() {
		if x >= s.X && x < s.X+s.W {
			return s.Tab, true
		}
	}
	return 0, false
}

const (
	DetailsContentTop = 5
	DetailsTabsRow    = 0
)

type DetailsState struct {
	Tab      Tab
	HoverTab int
	Pod      *data.Pod
	Metrics  data.Metrics
	Logs     []data.LogLine
	Events   []data.Event
	Manifest []string
	Describe []data.Field
	Focused  bool
	Scroll   int
	Follow   bool
	NewLines int
	Wrap     bool
}

func DetailsPanel(r draw.Region, st DetailsState, ctx Ctx) {
	inner := Panel(r, "", st.Focused, ctx)
	c := inner.Inset(1, 0, 1, 0)
	if c.Empty() {
		return
	}
	tabBar(c.Sub(0, DetailsTabsRow, c.W, 1), st, ctx)
	c.HLine(0, 1, c.W, '─', draw.Fg(theme.BorderSoft))
	if st.Pod == nil {
		centered(c.Sub(0, 2, c.W, c.H-2), "no pod selected")
		return
	}
	podHeader(c.Sub(0, 2, c.W, 2), *st.Pod, ctx)
	body := c.Sub(0, DetailsContentTop, c.W, c.H-DetailsContentTop)
	switch st.Tab {
	case TabTop:
		TopTab(body, st, ctx)
	case TabLog:
		LogTab(body, st, ctx)
	case TabConfig:
		ConfigTab(body, st, ctx)
	case TabDescribe:
		DescribeTab(body, st)
	case TabEvents:
		EventsTab(body, st)
	}
}

func tabBar(r draw.Region, st DetailsState, ctx Ctx) {
	for _, s := range TabSpans() {
		label := " " + s.Tab.String() + " "
		style := draw.Fg(theme.Muted)
		switch {
		case s.Tab == st.Tab:
			style = draw.Fg(theme.BgApp).WithBg(ctx.Pal.C).WithBold()
		case int(s.Tab) == st.HoverTab:
			style = draw.Fg(theme.Text)
		}
		r.Text(s.X, 0, label, style)
	}
	last := TabSpans()[len(tabNames)-1]
	hint := draw.Line{muted("tab ⇥ / shift+tab")}
	if r.W-hint.Width() > last.X+last.W+1 {
		r.LineRight(0, hint)
	}
}

func podHeader(r draw.Region, p data.Pod, ctx Ctx) {
	glyph, gc := StatusGlyph(p.Status, ctx)
	sc := StatusColor(p.Status)
	chip := "[" + p.Status.String() + "]"
	r.Put(0, 0, glyph, draw.Fg(gc))
	r.Text(2, 0, draw.Truncate(p.Name, r.W-len(chip)-3), draw.Fg(theme.Text).WithBold())
	r.LineRight(0, draw.Line{colored(chip, sc)})
	info := strings.Join([]string{p.Namespace, p.Status.String(), "node " + p.Node, p.IP}, " · ")
	r.Text(2, 1, draw.Truncate(info, r.W-2), draw.Fg(theme.Muted))
}

const logPrefixW = 16

type LogRow struct {
	Line data.LogLine
	Text string
	Cont bool
}

func LogRows(logs []data.LogLine, width int, wrap bool) []LogRow {
	rows := make([]LogRow, 0, len(logs))
	msgW := width - logPrefixW
	for _, l := range logs {
		if !wrap || msgW <= 0 {
			rows = append(rows, LogRow{Line: l, Text: l.Message})
			continue
		}
		for i, part := range draw.Wrap(l.Message, msgW) {
			rows = append(rows, LogRow{Line: l, Text: part, Cont: i > 0})
		}
	}
	return rows
}

func DetailsBody(panelW, panelH int) (w, h int) {
	return max(0, panelW-4), max(0, panelH-2-DetailsContentTop)
}

func LogWidth(panelW int) int {
	w, _ := DetailsBody(panelW, 0)
	return max(0, w-4)
}

func ScrollRows(tab Tab, panelH int) int {
	_, h := DetailsBody(0, panelH)
	switch tab {
	case TabLog, TabEvents:
		return max(0, h-3)
	case TabConfig, TabDescribe:
		return max(0, h-2)
	default:
		return 0
	}
}

func logLine(r draw.Region, y int, l data.LogLine, ctx Ctx) {
	logRow(r, y, LogRow{Line: l, Text: l.Message}, ctx)
}

func logRow(r draw.Region, y int, row LogRow, ctx Ctx) {
	if !row.Cont {
		r.Text(0, y, row.Line.Time, draw.Fg(theme.Faint))
		r.Text(10, y, draw.PadRight(row.Line.Level.String(), 5), draw.Fg(levelColor(row.Line.Level, ctx)))
	}
	r.Text(logPrefixW, y, draw.Truncate(row.Text, r.W-logPrefixW), draw.Fg(theme.TextSoft))
}

func levelColor(l data.Level, ctx Ctx) theme.RGB {
	switch l {
	case data.LevelDebug:
		return theme.DimGlyph
	case data.LevelWarn:
		return theme.Warn
	case data.LevelError:
		return theme.Err
	default:
		return ctx.Pal.C
	}
}

func insetBox(r draw.Region) draw.Region {
	return Box(r, theme.BgInset, theme.BorderSoft, theme.BgPanel, nil, nil).Inset(1, 0, 1, 0)
}

func LogTab(r draw.Region, st DetailsState, ctx Ctx) {
	c := insetBox(r)
	if c.Empty() {
		return
	}
	rows := LogRows(st.Logs, c.W, st.Wrap)
	visible := c.H - 1
	for i := 0; i < visible && st.Scroll+i < len(rows); i++ {
		logRow(c, i, rows[st.Scroll+i], ctx)
	}
	prompt := c.H - 1
	c.Put(0, prompt, '❯', draw.Fg(ctx.Pal.A))
	if ctx.Clock.CursorVisible() {
		c.Put(2, prompt, '▌', draw.Fg(ctx.Pal.A))
	}
	if !st.Follow && st.NewLines > 0 {
		c.LineRight(prompt, draw.Line{colored(fmt.Sprintf("↓ %d new", st.NewLines), ctx.Pal.A)})
	}
}

func ConfigTab(r draw.Region, st DetailsState, ctx Ctx) {
	c := insetBox(r)
	for i := 0; i < c.H && st.Scroll+i < len(st.Manifest); i++ {
		n := st.Scroll + i
		x := c.Text(0, i, draw.PadLeft(fmt.Sprint(n+1), 2), draw.Fg(theme.Faint))
		x += 2
		yamlLine(c.Sub(x, i, c.W-x, 1), st.Manifest[n], ctx)
	}
}

func yamlLine(r draw.Region, line string, ctx Ctx) {
	trimmed := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(trimmed)]
	x := r.Text(0, 0, indent, draw.Fg(theme.TextSoft))
	if strings.HasPrefix(trimmed, "- ") {
		x += r.Text(x, 0, "- ", draw.Fg(theme.TextSoft))
		trimmed = trimmed[2:]
	}
	key, value, ok := strings.Cut(trimmed, ":")
	if !ok {
		r.Text(x, 0, draw.Truncate(trimmed, r.W-x), draw.Fg(theme.TextSoft))
		return
	}
	x += r.Text(x, 0, draw.Truncate(key, r.W-x), draw.Fg(ctx.Pal.B))
	x += r.Text(x, 0, ":", draw.Fg(theme.TextSoft))
	r.Text(x, 0, draw.Truncate(value, r.W-x), draw.Fg(theme.TextSoft))
}

func DescribeTab(r draw.Region, st DetailsState) {
	c := insetBox(r)
	const keyW = 18
	for i := 0; i < c.H && st.Scroll+i < len(st.Describe); i++ {
		f := st.Describe[st.Scroll+i]
		c.Text(0, i, draw.PadRight(f.Key, keyW), draw.Fg(theme.Muted))
		c.Text(keyW, i, draw.Truncate(f.Value, c.W-keyW), draw.Fg(theme.Text))
	}
}

func EventsTab(r draw.Region, st DetailsState) {
	c := insetBox(r)
	const ageW, typeW, reasonW = 6, 8, 14
	typeX := ageW + 1
	reasonX := typeX + typeW + 1
	msgX := reasonX + reasonW + 1
	hdr := draw.Fg(theme.Muted)
	c.Text(0, 0, "AGE", hdr)
	c.Text(typeX, 0, "TYPE", hdr)
	c.Text(reasonX, 0, "REASON", hdr)
	c.Text(msgX, 0, "MESSAGE", hdr)
	for i := 0; i+1 < c.H && st.Scroll+i < len(st.Events); i++ {
		e := st.Events[st.Scroll+i]
		y := i + 1
		typeColor := theme.OK
		if e.Type == "Warning" {
			typeColor = theme.Warn
		}
		c.Text(0, y, draw.Truncate(e.Age, ageW), draw.Fg(theme.Muted))
		c.Text(typeX, y, draw.Truncate(e.Type, typeW), draw.Fg(typeColor))
		c.Text(reasonX, y, draw.Truncate(e.Reason, reasonW), draw.Fg(theme.Text))
		c.Text(msgX, y, draw.Truncate(e.Message, c.W-msgX), draw.Fg(theme.TextSoft))
	}
}

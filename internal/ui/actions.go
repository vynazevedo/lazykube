package ui

import (
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

type ActionTone int

const (
	ActionPlain ActionTone = iota
	ActionMuted
	ActionOK
	ActionWarn
	ActionErr
)

func (t ActionTone) color() theme.RGB {
	switch t {
	case ActionMuted:
		return theme.Muted
	case ActionOK:
		return theme.OK
	case ActionWarn:
		return theme.Warn
	case ActionErr:
		return theme.Err
	}
	return theme.Text
}

type ActionLine struct {
	Text string
	Tone ActionTone
}

type ActionItem struct {
	Label  string
	Reason string
}

type ActionField struct {
	Label   string
	Value   string
	Focused bool
	Choice  bool
}

type ActionButton struct {
	Label    string
	Disabled bool
	Focused  bool
}

type ActionDialogState struct {
	Title   string
	Lines   []ActionLine
	Items   []ActionItem
	Cursor  int
	Fields  []ActionField
	Error   string
	Buttons []ActionButton
	Hint    string
	Scroll  int
}

type ActionHitKind int

const (
	ActionHitNone ActionHitKind = iota
	ActionHitItem
	ActionHitField
	ActionHitButton
)

const (
	actionMaxW = 104
	actionMinW = 40

	actionValueMin = 24
)

type ActionGeometry struct {
	X, Y, W, H int
	BodyRows   int
	MaxScroll  int
	body       []ActionLine
	errors     []string
	itemsTop   int
	fieldsTop  int
	errorTop   int
	buttonsTop int
	hintTop    int
	buttons    []span
}

type span struct{ x, w int }

func wrapAction(lines []ActionLine, width int) []ActionLine {
	var out []ActionLine
	for _, l := range lines {
		clean := draw.Sanitize(l.Text)
		if clean == "" {
			out = append(out, ActionLine{})
			continue
		}
		for _, part := range draw.Wrap(clean, width) {
			out = append(out, ActionLine{Text: part, Tone: l.Tone})
		}
	}
	return out
}

func (b ActionButton) text() string {
	return "[" + b.Label + "]"
}

func ActionLayout(w, h int, st ActionDialogState) ActionGeometry {
	g := ActionGeometry{W: min(actionMaxW, w-4)}
	if g.W < actionMinW {
		return ActionGeometry{}
	}
	inner := g.W - 4
	g.body = wrapAction(st.Lines, inner)
	if st.Error != "" {
		g.errors = draw.Wrap(draw.Sanitize(st.Error), inner)
	}
	pinned := 0
	section := func(rows int) int {
		if rows == 0 {
			return pinned
		}
		top := pinned + 1
		pinned += rows + 1
		return top
	}
	items, fields, errs := section(len(st.Items)), section(len(st.Fields)), section(len(g.errors))
	buttonRows := 0
	if len(st.Buttons) > 0 {
		buttonRows = 1
	}
	buttons := section(buttonRows)
	hint := pinned
	if st.Hint != "" {
		pinned++
	}
	g.H = min(h-2, len(g.body)+pinned+2)
	g.BodyRows = max(0, g.H-2-pinned)
	g.MaxScroll = max(0, len(g.body)-g.BodyRows)
	g.X, g.Y = max(0, (w-g.W)/2), max(0, (h-g.H)/2)
	top := g.Y + 1 + g.BodyRows
	g.itemsTop, g.fieldsTop, g.errorTop, g.buttonsTop, g.hintTop = top+items, top+fields, top+errs, top+buttons, top+hint
	x := g.X + 2
	for _, b := range st.Buttons {
		bw := draw.Width(b.text())
		g.buttons = append(g.buttons, span{x: x, w: bw})
		x += bw + 2
	}
	return g
}

func (g ActionGeometry) Hit(x, y int, st ActionDialogState) (ActionHitKind, int) {
	if x < g.X+2 || x >= g.X+g.W-2 {
		return ActionHitNone, 0
	}
	if i := y - g.itemsTop; i >= 0 && i < len(st.Items) {
		return ActionHitItem, i
	}
	if i := y - g.fieldsTop; i >= 0 && i < len(st.Fields) {
		return ActionHitField, i
	}
	if y == g.buttonsTop {
		for i, b := range g.buttons {
			if x >= b.x && x < b.x+b.w {
				return ActionHitButton, i
			}
		}
	}
	return ActionHitNone, 0
}

func ActionDialog(r draw.Region, st ActionDialogState, ctx Ctx) {
	g := ActionLayout(r.W, r.H, st)
	if g.W == 0 {
		return
	}
	inner := Panel(r.Sub(g.X, g.Y, g.W, g.H), draw.Sanitize(st.Title), true, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	at := func(y int) int { return y - g.Y - 1 }
	off := max(0, min(st.Scroll, g.MaxScroll))
	for i := 0; i < g.BodyRows && off+i < len(g.body); i++ {
		inner.Line(0, i, draw.Line{colored(g.body[off+i].Text, g.body[off+i].Tone.color())}.Truncate(inner.W))
	}
	focused := func(c theme.RGB, on bool) draw.Style {
		if on {
			return draw.Fg(c).WithBg(theme.BorderFaint).WithBold()
		}
		return draw.Fg(c)
	}
	for i, it := range st.Items {
		mark, on := "  ", i == st.Cursor
		if on {
			mark = "> "
		}
		c := theme.Text
		if it.Reason != "" {
			c = theme.Faint
		}
		line := draw.Line{draw.S(mark+draw.Sanitize(it.Label), focused(c, on))}
		if it.Reason != "" {
			line = append(line, colored("  "+draw.Sanitize(it.Reason), theme.Warn))
		}
		inner.Line(0, at(g.itemsTop)+i, line.Truncate(inner.W))
	}
	for i, f := range st.Fields {
		label := draw.Truncate(draw.Sanitize(f.Label), max(8, inner.W-actionValueMin))
		value := fieldTail(draw.Sanitize(f.Value), inner.W-draw.Width(label)-8)
		switch {
		case f.Choice:
			value = "< " + value + " >"
		case f.Focused:
			value = "[" + value + "_]"
		default:
			value = "[" + value + "]"
		}
		mark := "  "
		if f.Focused {
			mark = "> "
		}
		line := draw.Line{muted(mark + label + ": "), draw.S(value, focused(theme.Text, f.Focused))}
		inner.Line(0, at(g.fieldsTop)+i, line.Truncate(inner.W))
	}
	for i, e := range g.errors {
		inner.Line(0, at(g.errorTop)+i, draw.Line{colored(e, theme.Err)}.Truncate(inner.W))
	}
	for i, b := range st.Buttons {
		c := theme.Text
		if b.Disabled {
			c = theme.Faint
		}
		inner.Text(g.buttons[i].x-g.X-2, at(g.buttonsTop), draw.Truncate(b.text(), max(0, inner.W-(g.buttons[i].x-g.X-2))), focused(c, b.Focused))
	}
	if st.Hint != "" {
		hint := st.Hint
		if g.MaxScroll > 0 {
			hint = "pgup/pgdn rola | " + hint
		}
		inner.Line(0, at(g.hintTop), draw.Line{faint(draw.Sanitize(hint))}.Truncate(inner.W))
	}
}

func fieldTail(s string, width int) string {
	gs := draw.Graphemes(s)
	from := len(gs)
	for used := 0; from > 0 && used+gs[from-1].Width <= width; from-- {
		used += gs[from-1].Width
	}
	out := ""
	for _, g := range gs[from:] {
		out += g.Text
	}
	return out
}

func ActionHelp() []KeyHelp {
	return []KeyHelp{
		{"tab", "foco"},
		{"enter", "controle focado"},
		{"esc", "cancelar etapa"},
	}
}

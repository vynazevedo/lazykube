package ui

import (
	"github.com/TNK-Studio/lazykube/internal/anim"
	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

type Ctx struct {
	Pal   theme.Palette
	Clock anim.Clock
}

func (c Ctx) Pulse(base theme.RGB) theme.RGB {
	return theme.Pulse(base, c.Clock.Phase)
}

func Panel(r draw.Region, title string, focused bool, ctx Ctx) draw.Region {
	if r.W < 2 || r.H < 2 {
		return r.Sub(0, 0, 0, 0)
	}
	r.Fill(theme.BgPanel)
	perimeter := 2*r.W + 2*r.H - 4
	color := func(j int) theme.RGB {
		if focused {
			return theme.BorderGradient(ctx.Pal, j, perimeter, ctx.Clock.Phase)
		}
		return theme.Border
	}
	put := func(x, y, j int, ch rune) {
		r.Put(x, y, ch, draw.Fg(color(j)).WithBg(theme.BgApp))
	}
	j := 0
	for x := 0; x < r.W; x++ {
		ch := '─'
		switch x {
		case 0:
			ch = '╭'
		case r.W - 1:
			ch = '╮'
		}
		put(x, 0, j, ch)
		j++
	}
	for y := 1; y < r.H; y++ {
		ch := '│'
		if y == r.H-1 {
			ch = '╯'
		}
		put(r.W-1, y, j, ch)
		j++
	}
	for x := r.W - 2; x >= 0; x-- {
		ch := '─'
		if x == 0 {
			ch = '╰'
		}
		put(x, r.H-1, j, ch)
		j++
	}
	for y := r.H - 2; y >= 1; y-- {
		put(0, y, j, '│')
		j++
	}
	if title != "" && r.W > 6 {
		titleColor := ctx.Pal.C
		if focused {
			titleColor = ctx.Pal.A
		}
		label := " " + draw.Truncate(title, r.W-6) + " "
		r.Text(2, 0, label, draw.Fg(titleColor).WithBg(theme.BgApp).WithBold())
	}
	return r.Inset(1, 1, 1, 1)
}

func Box(r draw.Region, bg, border theme.RGB, outer theme.RGB, left, right draw.Line) draw.Region {
	if r.W < 2 || r.H < 2 {
		return r.Sub(0, 0, 0, 0)
	}
	r.Fill(bg)
	st := draw.Fg(border).WithBg(outer)
	r.HLine(1, 0, r.W-2, '─', st)
	r.HLine(1, r.H-1, r.W-2, '─', st)
	for y := 1; y < r.H-1; y++ {
		r.Put(0, y, '│', st)
		r.Put(r.W-1, y, '│', st)
	}
	r.Put(0, 0, '╭', st)
	r.Put(r.W-1, 0, '╮', st)
	r.Put(0, r.H-1, '╰', st)
	r.Put(r.W-1, r.H-1, '╯', st)
	inner := r.Inset(1, 1, 1, 1)
	if len(left) > 0 {
		lw := min(left.Width(), r.W-4)
		r.Text(1, 0, " ", st)
		r.Line(2, 0, withBg(left.Truncate(lw), outer))
		r.Text(2+lw, 0, " ", st)
	}
	if len(right) > 0 {
		rw := right.Width()
		x := r.W - 3 - rw
		if x > 2+left.Width()+2 {
			r.Text(x-1, 0, " ", st)
			r.Line(x, 0, withBg(right, outer))
			r.Text(x+rw, 0, " ", st)
		}
	}
	return inner
}

func withBg(l draw.Line, bg theme.RGB) draw.Line {
	out := make(draw.Line, len(l))
	for i, s := range l {
		st := s.Style
		st = st.WithBg(bg)
		out[i] = draw.Span{Text: s.Text, Style: st}
	}
	return out
}

func Bar(r draw.Region, x, y, width int, fraction float64, from, to theme.RGB) {
	if width <= 0 {
		return
	}
	filled := draw.Filled(fraction, 1, width)
	for i := 0; i < width; i++ {
		if i < filled {
			r.Put(x+i, y, '█', draw.Fg(theme.Linear(from, to, i, width)))
			continue
		}
		r.Put(x+i, y, '█', draw.Fg(theme.BorderSoft))
	}
}

func muted(s string) draw.Span { return draw.S(s, draw.Fg(theme.Muted)) }
func text(s string) draw.Span  { return draw.S(s, draw.Fg(theme.Text)) }
func faint(s string) draw.Span { return draw.S(s, draw.Fg(theme.Faint)) }
func colored(s string, c theme.RGB) draw.Span {
	return draw.S(s, draw.Fg(c))
}
func bold(s string, c theme.RGB) draw.Span {
	return draw.S(s, draw.Fg(c).WithBold())
}

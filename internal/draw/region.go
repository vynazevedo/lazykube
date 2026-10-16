package draw

import (
	"unicode/utf8"

	"github.com/vynazevedo/lazykube/internal/theme"
)

type Region struct {
	c          *Canvas
	X, Y, W, H int
}

type Style struct {
	Fg   theme.RGB
	Bg   *theme.RGB
	Bold bool
}

func Fg(c theme.RGB) Style {
	return Style{Fg: c}
}

func (s Style) WithBg(bg theme.RGB) Style {
	s.Bg = &bg
	return s
}

func (s Style) WithBold() Style {
	s.Bold = true
	return s
}

func (r Region) Sub(x, y, w, h int) Region {
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > r.W {
		w = r.W - x
	}
	if y+h > r.H {
		h = r.H - y
	}
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return Region{c: r.c, X: r.X + x, Y: r.Y + y, W: w, H: h}
}

func (r Region) Inset(left, top, right, bottom int) Region {
	return r.Sub(left, top, r.W-left-right, r.H-top-bottom)
}

func (r Region) Empty() bool {
	return r.W <= 0 || r.H <= 0
}

func (r Region) cell(x, y int) *Cell {
	if x < 0 || y < 0 || x >= r.W || y >= r.H || r.c == nil {
		return nil
	}
	return r.c.cell(r.X+x, r.Y+y)
}

func (r Region) Fill(bg theme.RGB) {
	for y := 0; y < r.H; y++ {
		for x := 0; x < r.W; x++ {
			if cl := r.cell(x, y); cl != nil {
				*cl = Cell{Rune: ' ', Fg: theme.Text, Bg: bg}
			}
		}
	}
}

func (r Region) SetBg(x, y int, bg theme.RGB) {
	if cl := r.cell(x, y); cl != nil {
		cl.Bg = bg
	}
}

func (r Region) FillBg(bg theme.RGB) {
	for y := 0; y < r.H; y++ {
		for x := 0; x < r.W; x++ {
			r.SetBg(x, y, bg)
		}
	}
}

func (r Region) Put(x, y int, ch rune, s Style) {
	r.putCell(x, y, ch, "", false, s)
}

func (r Region) putCell(x, y int, ch rune, extra string, cont bool, s Style) {
	cl := r.cell(x, y)
	if cl == nil {
		return
	}
	if cl.Cont && !cont {
		if head := r.cell(x-1, y); head != nil {
			head.Rune, head.Extra = ' ', ""
		}
	} else if next := r.cell(x+1, y); next != nil && next.Cont && !cont {
		next.Cont = false
		next.Rune, next.Extra = ' ', ""
	}
	cl.Rune = ch
	cl.Extra = extra
	cl.Cont = cont
	cl.Fg = s.Fg
	cl.Bold = s.Bold
	if s.Bg != nil {
		cl.Bg = *s.Bg
	}
}

func (r Region) Text(x, y int, s string, st Style) int {
	if isSimpleASCII(s) {
		for i := 0; i < len(s); i++ {
			r.putCell(x+i, y, rune(s[i]), "", false, st)
		}
		return len(s)
	}
	n := 0
	for _, g := range Graphemes(s) {
		head, size := utf8.DecodeRuneInString(g.Text)
		extra := g.Text[size:]
		if g.Width == 2 {
			if x+n+1 >= r.W {
				r.putCell(x+n, y, ' ', "", false, st)
				n++
				continue
			}
			r.putCell(x+n, y, head, extra, false, st)
			r.putCell(x+n+1, y, ' ', "", true, st)
			n += 2
			continue
		}
		r.putCell(x+n, y, head, extra, false, st)
		n++
	}
	return n
}

func (r Region) HLine(x, y, w int, ch rune, st Style) {
	for i := 0; i < w; i++ {
		r.Put(x+i, y, ch, st)
	}
}

func (r Region) Cell(x, y int) Cell {
	if cl := r.cell(x, y); cl != nil {
		return *cl
	}
	return Cell{}
}

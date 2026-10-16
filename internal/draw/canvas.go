package draw

import (
	"strconv"
	"strings"

	"github.com/vynazevedo/lazykube/internal/theme"
)

type Cell struct {
	Rune  rune
	Extra string
	Cont  bool
	Fg    theme.RGB
	Bg    theme.RGB
	Bold  bool
}

func (cl Cell) write(b *strings.Builder) {
	if cl.Cont {
		return
	}
	b.WriteRune(cl.Rune)
	b.WriteString(cl.Extra)
}

type Canvas struct {
	w, h  int
	cells []Cell
}

func NewCanvas(w, h int, bg theme.RGB) *Canvas {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	c := &Canvas{w: w, h: h, cells: make([]Cell, w*h)}
	for i := range c.cells {
		c.cells[i] = Cell{Rune: ' ', Fg: theme.Text, Bg: bg}
	}
	return c
}

func (c *Canvas) Width() int  { return c.w }
func (c *Canvas) Height() int { return c.h }

func (c *Canvas) Region() Region {
	return Region{c: c, X: 0, Y: 0, W: c.w, H: c.h}
}

func (c *Canvas) At(x, y int) Cell {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return Cell{}
	}
	return c.cells[y*c.w+x]
}

func (c *Canvas) cell(x, y int) *Cell {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return nil
	}
	return &c.cells[y*c.w+x]
}

func (c *Canvas) Plain() string {
	var b strings.Builder
	b.Grow((c.w + 1) * c.h)
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			c.cells[y*c.w+x].write(&b)
		}
		if y < c.h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (c *Canvas) String() string {
	var b strings.Builder
	b.Grow(c.w * c.h * 4)
	for y := 0; y < c.h; y++ {
		row := c.cells[y*c.w : (y+1)*c.w]
		var prev Cell
		for x, cl := range row {
			if x == 0 || cl.Fg != prev.Fg || cl.Bg != prev.Bg || cl.Bold != prev.Bold {
				writeSGR(&b, cl)
				prev = cl
			}
			cl.write(&b)
		}
		b.WriteString("\x1b[0m")
		if y < c.h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func writeSGR(b *strings.Builder, cl Cell) {
	b.WriteString("\x1b[0")
	if cl.Bold {
		b.WriteString(";1")
	}
	b.WriteString(";38;2;")
	writeRGB(b, cl.Fg)
	b.WriteString(";48;2;")
	writeRGB(b, cl.Bg)
	b.WriteByte('m')
}

func writeRGB(b *strings.Builder, c theme.RGB) {
	b.WriteString(strconv.Itoa(int(c.R)))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(c.G)))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(c.B)))
}

func (c *Canvas) Fade(to theme.RGB, k float64) {
	if k <= 0 {
		return
	}
	for i := range c.cells {
		c.cells[i].Fg = theme.Mix(c.cells[i].Fg, to, k)
		c.cells[i].Bg = theme.Mix(c.cells[i].Bg, to, k)
	}
}

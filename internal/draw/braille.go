package draw

import "github.com/vynazevedo/lazykube/internal/theme"

var brailleBits = [2][4]uint8{
	{0x01, 0x02, 0x04, 0x40},
	{0x08, 0x10, 0x20, 0x80},
}

type Braille struct {
	w, h  int
	bits  []uint8
	color []theme.RGB
}

func NewBraille(cols, rows int) *Braille {
	if cols < 0 {
		cols = 0
	}
	if rows < 0 {
		rows = 0
	}
	return &Braille{w: cols, h: rows, bits: make([]uint8, cols*rows), color: make([]theme.RGB, cols*rows)}
}

func (b *Braille) DotWidth() int  { return b.w * 2 }
func (b *Braille) DotHeight() int { return b.h * 4 }

func (b *Braille) Set(px, py int, c theme.RGB) {
	if px < 0 || py < 0 || px >= b.w*2 || py >= b.h*4 {
		return
	}
	i := (py/4)*b.w + px/2
	b.bits[i] |= brailleBits[px%2][py%4]
	b.color[i] = c
}

func (b *Braille) Has(col, row int) bool {
	if col < 0 || row < 0 || col >= b.w || row >= b.h {
		return false
	}
	return b.bits[row*b.w+col] != 0
}

func (b *Braille) Rune(col, row int) rune {
	if col < 0 || row < 0 || col >= b.w || row >= b.h {
		return ' '
	}
	return rune(0x2800 + int(b.bits[row*b.w+col]))
}

func (b *Braille) Color(col, row int) theme.RGB {
	if col < 0 || row < 0 || col >= b.w || row >= b.h {
		return theme.RGB{}
	}
	return b.color[row*b.w+col]
}

func (b *Braille) Line(x0, y0, x1, y1 int, color func(px int) theme.RGB) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	e := dx + dy
	for {
		b.Set(x0, y0, color(x0))
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * e
		if e2 >= dy {
			e += dy
			x0 += sx
		}
		if e2 <= dx {
			e += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

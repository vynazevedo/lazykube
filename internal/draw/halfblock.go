package draw

import "github.com/TNK-Studio/lazykube/internal/theme"

type Pixels [][]theme.RGB

func NewPixels(w, h int, fill theme.RGB) Pixels {
	px := make(Pixels, h)
	for y := range px {
		px[y] = make([]theme.RGB, w)
		for x := range px[y] {
			px[y][x] = fill
		}
	}
	return px
}

func (r Region) HalfBlock(x, y int, px Pixels) {
	for py := 0; py+1 < len(px); py += 2 {
		top, bot := px[py], px[py+1]
		for px := 0; px < len(top) && px < len(bot); px++ {
			if top[px] == bot[px] {
				r.Put(x+px, y+py/2, ' ', Fg(top[px]).WithBg(top[px]))
				continue
			}
			r.Put(x+px, y+py/2, '▀', Fg(top[px]).WithBg(bot[px]))
		}
	}
}

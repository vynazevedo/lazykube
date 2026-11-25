package draw

import "github.com/vynazevedo/lazykube/internal/weburl"

func linkRun(row []Cell, x int) (int, bool) {
	u := row[x].Link
	end := x
	for end < len(row) && row[end].Link == u {
		end++
	}
	text := u.String()
	if end-x != len(text) {
		return end, false
	}
	for i := range len(text) {
		cl := row[x+i]
		if cl.Cont || cl.Extra != "" || cl.Rune != rune(text[i]) || cl.Fg == cl.Bg {
			return end, false
		}
	}
	return end, true
}

func (c *Canvas) SealLinks() {
	for y := range c.h {
		row := c.cells[y*c.w : (y+1)*c.w]
		for x := 0; x < len(row); {
			if row[x].Link.IsZero() {
				x++
				continue
			}
			end, whole := linkRun(row, x)
			for ; x < end; x++ {
				if !whole {
					row[x].Link = weburl.URL{}
				}
			}
		}
	}
}

func (c *Canvas) DropLinks() {
	for i := range c.cells {
		c.cells[i].Link = weburl.URL{}
	}
}

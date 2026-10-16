package draw

type Span struct {
	Text  string
	Style Style
}

type Line []Span

func S(text string, st Style) Span {
	return Span{Text: text, Style: st}
}

func (l Line) Width() int {
	n := 0
	for _, s := range l {
		n += Width(s.Text)
	}
	return n
}

func (r Region) Line(x, y int, l Line) int {
	n := 0
	for _, s := range l {
		n += r.Text(x+n, y, s.Text, s.Style)
	}
	return n
}

func (r Region) LineRight(y int, l Line) int {
	x := r.W - l.Width()
	r.Line(x, y, l)
	return x
}

func (l Line) Truncate(width int) Line {
	if width <= 0 {
		return nil
	}
	if l.Width() <= width {
		return l
	}
	out := make(Line, 0, len(l))
	left := width - 1
	var last Style
	for _, s := range l {
		if left <= 0 {
			break
		}
		var b []byte
		for _, g := range Graphemes(s.Text) {
			if g.Width > left {
				left = 0
				break
			}
			b = append(b, g.Text...)
			left -= g.Width
		}
		out = append(out, Span{Text: string(b), Style: s.Style})
		last = s.Style
	}
	return append(out, Span{Text: "…", Style: last})
}

func Fit(width int, variants ...Line) Line {
	for _, v := range variants {
		if v.Width() <= width {
			return v
		}
	}
	if len(variants) == 0 {
		return nil
	}
	return variants[len(variants)-1].Truncate(width)
}

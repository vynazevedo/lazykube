package draw

import (
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

type Grapheme struct {
	Text  string
	Width int
}

func isSimpleASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}

func Sanitize(s string) string {
	if isSimpleASCII(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r), r == unicode.ReplacementChar:
			return '?'
		case unicode.Is(unicode.Cf, r) && r != '‍':
			return -1
		}
		return r
	}, s)
}

func Graphemes(s string) []Grapheme {
	s = Sanitize(s)
	if isSimpleASCII(s) {
		out := make([]Grapheme, len(s))
		for i := 0; i < len(s); i++ {
			out[i] = Grapheme{Text: s[i : i+1], Width: 1}
		}
		return out
	}
	out := make([]Grapheme, 0, len(s))
	state := -1
	for len(s) > 0 {
		var cluster string
		var w int
		cluster, s, w, state = uniseg.FirstGraphemeClusterInString(s, state)
		if w <= 0 {
			if len(out) > 0 {
				out[len(out)-1].Text += cluster
			}
			continue
		}
		out = append(out, Grapheme{Text: cluster, Width: min(w, 2)})
	}
	return out
}

func Width(s string) int {
	s = Sanitize(s)
	if isSimpleASCII(s) {
		return len(s)
	}
	n := 0
	for _, g := range Graphemes(s) {
		n += g.Width
	}
	return n
}

func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if Width(s) <= width {
		return Sanitize(s)
	}
	var b strings.Builder
	used := 0
	for _, g := range Graphemes(s) {
		if used+g.Width > width-1 {
			break
		}
		b.WriteString(g.Text)
		used += g.Width
	}
	b.WriteString("…")
	return b.String()
}

func PadRight(s string, width int) string {
	s = Truncate(s, width)
	if pad := width - Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func PadLeft(s string, width int) string {
	s = Truncate(s, width)
	if pad := width - Width(s); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}

func Wrap(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	var cur []Grapheme
	used := 0
	for _, g := range Graphemes(s) {
		if used+g.Width > width && len(cur) > 0 {
			cut := len(cur)
			for i := len(cur) - 1; i > 0; i-- {
				if cur[i].Text == " " {
					cut = i
					break
				}
			}
			lines = append(lines, joinGraphemes(cur[:cut]))
			rest := cur[cut:]
			if len(rest) > 0 && rest[0].Text == " " {
				rest = rest[1:]
			}
			cur = append([]Grapheme(nil), rest...)
			used = 0
			for _, r := range cur {
				used += r.Width
			}
		}
		cur = append(cur, g)
		used += g.Width
	}
	return append(lines, joinGraphemes(cur))
}

func joinGraphemes(gs []Grapheme) string {
	var b strings.Builder
	for _, g := range gs {
		b.WriteString(g.Text)
	}
	return b.String()
}

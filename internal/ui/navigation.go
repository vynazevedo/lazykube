package ui

import (
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

type MenuItem struct {
	Key   string
	Title string
}

type MenuState struct {
	Items   []MenuItem
	Active  int
	Cursor  int
	Focused bool
}

type Span struct {
	X, W int
}

const menuSeparator = " | "

func menuLabel(it MenuItem, active bool) string {
	if active {
		return "[" + it.Key + " " + it.Title + "]"
	}
	return " " + it.Key + " " + it.Title + " "
}

func MenuSpans(items []MenuItem) []Span {
	out := make([]Span, len(items))
	x := 0
	for i, it := range items {
		w := draw.Width(menuLabel(it, false))
		out[i] = Span{X: x, W: w}
		x += w + len(menuSeparator)
	}
	return out
}

func MenuItemAt(items []MenuItem, x int) (int, bool) {
	for i, s := range MenuSpans(items) {
		if x >= s.X && x < s.X+s.W {
			return i, true
		}
	}
	return 0, false
}

func Menu(r draw.Region, st MenuState, ctx Ctx) {
	r.Fill(theme.BgApp)
	spans := MenuSpans(st.Items)
	for i, it := range st.Items {
		s := spans[i]
		if i > 0 {
			r.Text(s.X-len(menuSeparator), 0, menuSeparator, draw.Fg(theme.Faint))
		}
		style := draw.Fg(theme.Muted)
		if i == st.Active {
			style = draw.Fg(ctx.Pal.C).WithBold()
		}
		if st.Focused && i == st.Cursor {
			style = draw.Fg(theme.BgApp).WithBg(ctx.Pal.A).WithBold()
		}
		r.Text(s.X, 0, menuLabel(it, i == st.Active), style)
	}
	hint := draw.Line{faint("m menu")}
	if st.Focused {
		hint = draw.Line{muted("← → move  home end  enter open  esc cancel")}
	}
	end := 0
	if n := len(spans); n > 0 {
		end = spans[n-1].X + spans[n-1].W
	}
	if r.W-hint.Width() > end+2 {
		r.Sub(0, 0, r.W-1, 1).LineRight(0, hint)
	}
}

const crumbSeparator = " > "

func CrumbSpans(labels []string, width int) []Span {
	out := make([]Span, 0, len(labels))
	x := 0
	for _, l := range labels {
		w := min(draw.Width(draw.Sanitize(l)), max(0, width-x))
		out = append(out, Span{X: x, W: w})
		x += w + len(crumbSeparator)
	}
	return out
}

func CrumbAt(labels []string, x, width int) (int, bool) {
	for i, s := range CrumbSpans(labels, width) {
		if s.W > 0 && x >= s.X && x < s.X+s.W {
			return i, true
		}
	}
	return 0, false
}

func Breadcrumbs(r draw.Region, labels []string, ctx Ctx) {
	r.Fill(theme.BgApp)
	spans := CrumbSpans(labels, r.W)
	for i, l := range labels {
		s := spans[i]
		if s.W <= 0 {
			break
		}
		if i > 0 {
			r.Text(s.X-len(crumbSeparator), 0, crumbSeparator, draw.Fg(theme.Faint))
		}
		style := draw.Fg(ctx.Pal.B)
		if i == len(labels)-1 {
			style = draw.Fg(theme.Text).WithBold()
		}
		r.Text(s.X, 0, draw.Truncate(draw.Sanitize(l), s.W), style)
	}
}

func PodMissing(r draw.Region, key string, ctx Ctx) {
	inner := Panel(r, "Pod", true, ctx)
	if inner.Empty() {
		return
	}
	mid := max(0, inner.H/2-1)
	centered(inner.Sub(0, mid, inner.W, 1), "recurso não observado neste escopo: "+draw.Sanitize(key))
	centeredColor(inner.Sub(0, mid+1, inner.W, 1), "esc voltar · nenhum outro pod foi selecionado", theme.Muted)
}

func MenuHelp() []KeyHelp {
	return []KeyHelp{
		{"←→", "move"},
		{"home end", "first / last"},
		{"enter", "open"},
		{"2", "Dashboard"},
		{"esc m", "cancel"},
	}
}

func PodDetailHelp() []KeyHelp {
	return []KeyHelp{
		{"esc", "back"},
		{"l c t d e", "tabs"},
		{"S", "security"},
		{"[ ]", "tab"},
		{"/ n N", "search"},
		{"C", "container"},
		{"P", "previous"},
		{"m", "menu"},
		{"?", "help"},
		{"q", "quit"},
	}
}

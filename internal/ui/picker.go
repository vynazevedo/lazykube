package ui

import (
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

type PickerItem struct {
	Label   string
	Detail  string
	Current bool
	Problem string
}

type PickerState struct {
	Title     string
	Items     []PickerItem
	Cursor    int
	Filter    string
	Filtering bool
	Input     string
	Inputting bool
	Note      string
	Error     string
	Loading   bool
}

type PickerGeometry struct {
	X, Y, W, H int
	RowsTop    int
	Rows       int
}

const (
	pickerMaxW    = 96
	pickerChrome  = 6
	pickerMinRows = 3
)

func PickerLayout(w, h int, st PickerState) PickerGeometry {
	pw := min(pickerMaxW, max(0, w-4))
	rows := max(pickerMinRows, min(len(st.Items), h-pickerChrome-6))
	ph := min(h-2, rows+pickerChrome)
	rows = max(0, ph-pickerChrome)
	g := PickerGeometry{X: max(0, (w-pw)/2), Y: max(0, (h-ph)/2), W: pw, H: ph, Rows: rows}
	g.RowsTop = g.Y + 3
	return g
}

func (g PickerGeometry) RowAt(x, y, cursor, total int) (int, bool) {
	if x < g.X || x >= g.X+g.W || y < g.RowsTop || y >= g.RowsTop+g.Rows {
		return 0, false
	}
	idx := ScrollOffset(cursor, total, g.Rows) + y - g.RowsTop
	return idx, idx < total
}

func (g PickerGeometry) Contains(x, y int) bool {
	return x >= g.X && x < g.X+g.W && y >= g.Y && y < g.Y+g.H
}

func Picker(r draw.Region, st PickerState, ctx Ctx) {
	g := PickerLayout(r.W, r.H, st)
	if g.W < 20 || g.H < pickerChrome {
		return
	}
	inner := Panel(r.Sub(g.X, g.Y, g.W, g.H), draw.Sanitize(st.Title), true, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	filter := draw.Line{colored("/", ctx.Pal.A), text(draw.Sanitize(st.Filter))}
	switch {
	case st.Inputting:
		filter = draw.Line{muted("namespace: "), text(draw.Sanitize(st.Input))}
		if ctx.Clock.CursorVisible() {
			filter = append(filter, colored("▌", ctx.Pal.A))
		}
	case st.Filtering && ctx.Clock.CursorVisible():
		filter = append(filter, colored("▌", ctx.Pal.A))
	case st.Filter == "" && !st.Filtering:
		filter = draw.Line{faint("/ filtra a lista")}
	}
	inner.Line(0, 0, filter.Truncate(inner.W))
	inner.HLine(0, 1, inner.W, '─', draw.Fg(theme.BorderSoft))
	body := inner.Sub(0, 2, inner.W, g.Rows)
	switch {
	case st.Loading:
		centered(body, "lendo os contextos do kubeconfig…")
	case len(st.Items) == 0:
		centeredColor(body, reasonOr(st.Error, "nenhum item corresponde ao filtro"), theme.Warn)
	}
	off := ScrollOffset(st.Cursor, len(st.Items), g.Rows)
	for i := 0; i < g.Rows && off+i < len(st.Items); i++ {
		idx := off + i
		it := st.Items[idx]
		style := func(c theme.RGB) draw.Style { return draw.Fg(c) }
		mark := "  "
		if idx == st.Cursor && !st.Inputting {
			style = func(c theme.RGB) draw.Style { return draw.Fg(c).WithBg(theme.BorderFaint).WithBold() }
			mark = "› "
		}
		current := "  "
		if it.Current {
			current = "● "
		}
		line := draw.Line{
			draw.S(mark, style(theme.Text)),
			draw.S(current, style(ctx.Pal.C)),
			draw.S(draw.PadRight(draw.Truncate(draw.Sanitize(it.Label), 28), 28), style(theme.Text)),
			draw.S("  "+draw.Sanitize(it.Detail), style(theme.Muted)),
		}
		if it.Problem != "" {
			line = append(line, draw.S("  "+draw.Sanitize(it.Problem), style(theme.Warn)))
		}
		body.Line(0, i, draw.Line(line).Truncate(inner.W))
	}
	foot := inner.H - 1
	if st.Note != "" {
		inner.Line(0, foot-1, draw.Line{faint(draw.Sanitize(st.Note))}.Truncate(inner.W))
	}
	hint := "enter confirma · esc cancela · / filtra · clique escolhe"
	if st.Inputting {
		hint = "enter confirma o namespace · esc volta à lista"
	}
	if st.Error != "" && len(st.Items) > 0 {
		inner.Line(0, foot, draw.Line{colored(draw.Sanitize(st.Error), theme.Err)}.Truncate(inner.W))
		return
	}
	inner.Line(0, foot, draw.Line{faint(hint)}.Truncate(inner.W))
}

package ui

import (
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

type SidebarRow struct {
	Depth    int
	Label    string
	Category bool
	Expanded bool
	Active   bool
	Planned  bool
	Shortcut string
}

type SidebarState struct {
	Rows    []SidebarRow
	Cursor  int
	Offset  int
	Focused bool
	Overlay bool
	Note    string
}

const (
	sidebarToggleW = 4
	plannedSuffix  = " (futuro)"
)

func SidebarRowsVisible(h int) int {
	return max(0, h-3)
}

func SidebarToggleAt(r SidebarRow, x int) bool {
	start := 1 + 2*r.Depth
	return r.Category && x >= start && x < start+sidebarToggleW-1
}

func SidebarRowText(r SidebarRow) string {
	text := ""
	for range r.Depth {
		text += "  "
	}
	if r.Category {
		if r.Expanded {
			text += "[-] "
		} else {
			text += "[+] "
		}
	}
	text += r.Label
	switch {
	case r.Planned:
		text += plannedSuffix
	case r.Shortcut != "":
		text += " (" + r.Shortcut + ")"
	}
	return text
}

func SidebarScroll(cursor, total, rows int) int {
	return ScrollOffset(cursor, total, rows)
}

func Sidebar(r draw.Region, st SidebarState, ctx Ctx) {
	if r.Empty() {
		return
	}
	bg := theme.BgApp
	if st.Overlay {
		bg = theme.BgPanel
	}
	r.Fill(bg)
	sep := theme.BorderFaint
	if st.Focused {
		sep = ctx.Pal.A
	}
	for y := 0; y < r.H; y++ {
		r.Put(r.W-1, y, '│', draw.Fg(sep).WithBg(bg))
	}
	inner := r.Sub(0, 0, r.W-1, r.H)
	rows := SidebarRowsVisible(r.H)
	for i := 0; i < rows && st.Offset+i < len(st.Rows); i++ {
		idx := st.Offset + i
		row := st.Rows[idx]
		style := draw.Fg(theme.TextMid).WithBg(bg)
		switch {
		case row.Planned:
			style = draw.Fg(theme.Faint).WithBg(bg)
		case row.Category:
			style = draw.Fg(theme.Text).WithBg(bg).WithBold()
		}
		if row.Active {
			style = draw.Fg(ctx.Pal.C).WithBg(bg).WithBold()
		}
		if st.Focused && idx == st.Cursor {
			style = style.WithBg(theme.BorderFaint)
			inner.Line(0, i, draw.Line{draw.S(draw.PadRight("", inner.W), style)})
		}
		text := SidebarRowText(row)
		if row.Active {
			text = "›" + text
		} else {
			text = " " + text
		}
		inner.Line(0, i, draw.Line{draw.S(draw.Truncate(text, inner.W), style)})
	}
	note := st.Note
	if note == "" && st.Focused {
		note = "enter abre · b fecha"
	}
	if note == "" {
		note = "b árvore"
	}
	lines := draw.Wrap(draw.Sanitize(note), max(1, inner.W-1))
	if len(lines) > 2 {
		lines = lines[:2]
	}
	for i, l := range lines {
		inner.Line(0, r.H-len(lines)+i, draw.Line{draw.S(" "+l, draw.Fg(theme.Muted).WithBg(bg))})
	}
}

func SidebarRowAt(st SidebarState, h, y int) (int, bool) {
	if y < 0 || y >= SidebarRowsVisible(h) {
		return 0, false
	}
	idx := st.Offset + y
	return idx, idx < len(st.Rows)
}

func TreeHelp() []KeyHelp {
	return []KeyHelp{
		{"↑↓", "move"},
		{"←→", "collapse / expand"},
		{"enter", "open"},
		{"b", "close tree"},
		{"esc", "body"},
		{"?", "help"},
		{"q", "quit"},
	}
}

func FullHelpTree() []KeyHelp {
	return []KeyHelp{
		{"↑/↓ k/j", "move in the tree"},
		{"pgup/pgdn", "page"},
		{"home/end", "first / last"},
		{"→", "expand or first child"},
		{"←", "collapse or parent"},
		{"enter", "open screen or toggle group"},
		{"b", "close tree"},
		{"esc tab", "focus the body"},
		{"m", "screens menu"},
		{"2 3 4 6", "Dashboard / Workloads / Nodes / Events"},
		{"c n", "switch context / namespace"},
		{"?", "toggle help"},
		{"q ctrl+c", "quit"},
	}
}

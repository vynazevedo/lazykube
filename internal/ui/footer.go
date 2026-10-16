package ui

import (
	"fmt"
	"math"

	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

type KeyHelp struct {
	Key  string
	Desc string
}

func ShortHelp() []KeyHelp {
	return []KeyHelp{
		{"↑↓", "move"},
		{"tab", "panel"},
		{"/", "filter"},
		{"l", "logs"},
		{"s", "shell"},
		{"d", "describe"},
		{"ctrl+d", "delete"},
		{"?", "help"},
		{"q", "quit"},
	}
}

func FullHelp() []KeyHelp {
	return []KeyHelp{
		{"↑/↓ k/j", "move selection"},
		{"tab", "next panel"},
		{"shift+tab", "previous panel"},
		{"/", "filter pods"},
		{"enter", "confirm / filter namespace"},
		{"esc", "leave filter / clear"},
		{"l c t d e", "Log Config Top Describe Events"},
		{"S", "Security / namespace posture"},
		{"[ ]", "previous / next tab"},
		{"pgup/pgdn", "scroll viewport"},
		{"home/end", "top / bottom"},
		{"f", "toggle follow (Log)"},
		{"s", "shell in pod"},
		{"ctrl+d", "delete pod"},
		{"ctrl+k", "switch context"},
		{"p", "cycle palette"},
		{"1", "toggle left column"},
		{"ctrl+p", "pause animations"},
		{"?", "toggle help"},
		{"q ctrl+c", "quit"},
	}
}

type FooterState struct {
	FPS float64
}

func Footer(divider, help draw.Region, st FooterState) {
	divider.Fill(theme.BgApp)
	divider.HLine(0, 0, divider.W, '─', draw.Fg(theme.BorderFaint))
	help.Fill(theme.BgApp)
	right := draw.Line{faint(fmt.Sprintf("bubbletea · %d fps", int(math.Round(st.FPS))))}
	limit := help.W - right.Width() - 2
	x := 1
	for i, k := range ShortHelp() {
		pair := draw.Line{
			draw.S(" "+k.Key+" ", draw.Fg(theme.Text).WithBg(theme.BorderFaint)),
			muted(" " + k.Desc),
		}
		if i > 0 {
			pair = append(draw.Line{muted("  ")}, pair...)
		}
		if x+pair.Width() > limit {
			break
		}
		x += help.Line(x, 0, pair)
	}
	help.Sub(0, 0, help.W-1, 1).LineRight(0, right)
}

func HelpOverlay(screen draw.Region, ctx Ctx) {
	items := FullHelp()
	half := (len(items) + 1) / 2
	const keyW, descW = 11, 30
	w := min(screen.W-4, 2*(keyW+descW)+7)
	h := min(screen.H-2, half+4)
	box := screen.Sub((screen.W-w)/2, (screen.H-h)/2, w, h)
	inner := Panel(box, "Help", true, ctx).Inset(2, 1, 2, 1)
	colW := (inner.W - 1) / 2
	for i, k := range items {
		col, row := i/half, i%half
		if row >= inner.H {
			continue
		}
		x := col * (colW + 1)
		inner.Text(x, row, draw.PadRight(k.Key, keyW), draw.Fg(ctx.Pal.A).WithBold())
		inner.Text(x+keyW, row, draw.Truncate(k.Desc, colW-keyW), draw.Fg(theme.Muted))
	}
}

func TooSmall(screen draw.Region, w, h int) {
	screen.Fill(theme.BgApp)
	centered(screen, fmt.Sprintf("terminal muito pequeno - mínimo 120×36 (atual %d×%d)", w, h))
}

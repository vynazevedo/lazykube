package ui

import (
	"fmt"
	"math"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
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
		{"enter", "open"},
		{"m", "menu"},
		{"l", "logs"},
		{"d", "describe"},
		{"e", "events"},
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
		{"enter", "open pod / namespace scope"},
		{"esc", "clear search/filter, back"},
		{"m", "screens menu ← → enter"},
		{"2", "Dashboard screen"},
		{"3", "Workloads screen"},
		{"4", "Nodes screen"},
		{"6", "Events screen"},
		{"c n", "switch context / namespace"},
		{"b", "navigation tree"},
		{"l t d e", "Log Top Describe Events"},
		{"S", "Security / namespace posture"},
		{"A", "ack all Security alerts"},
		{"[ ]", "previous / next tab, Config"},
		{"pgup/pgdn", "scroll viewport"},
		{"home/end", "top / bottom"},
		{"g G", "top / bottom (details)"},
		{"f", "toggle follow (Log)"},
		{"w", "toggle wrap (Log)"},
		{"C", "next log container"},
		{"P", "current / previous logs"},
		{"/ alt+n N", "search Log (details focus)"},
		{"E", "events pod / ns / cluster"},
		{"p", "cycle palette"},
		{"1", "toggle left column"},
		{"ctrl+p", "pause animations"},
		{"?", "toggle help"},
		{"q ctrl+c", "quit"},
	}
}

type FooterState struct {
	FPS  float64
	Keys []KeyHelp
}

func Footer(divider, help draw.Region, st FooterState) {
	divider.Fill(theme.BgApp)
	divider.HLine(0, 0, divider.W, '─', draw.Fg(theme.BorderFaint))
	help.Fill(theme.BgApp)
	right := draw.Line{faint(fmt.Sprintf("bubbletea · %d fps", int(math.Round(st.FPS))))}
	limit := help.W - right.Width() - 2
	x := 1
	keys := st.Keys
	if keys == nil {
		keys = ShortHelp()
	}
	for i, k := range keys {
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

func FullHelpDetail() []KeyHelp {
	dashboardOnly := map[string]bool{"tab": true, "shift+tab": true, "/": true, "enter": true, "1": true}
	var out []KeyHelp
	for _, h := range FullHelp() {
		if dashboardOnly[h.Key] {
			continue
		}
		if h.Key == "esc" {
			h.Desc = "clear search, back"
		}
		out = append(out, h)
	}
	return out
}

func FullHelpWorkloads() []KeyHelp {
	var out []KeyHelp
	for _, h := range FullHelp() {
		switch h.Key {
		case "1":
			continue
		case "enter":
			h.Desc = "open pod / deployment replicas"
		}
		out = append(out, h)
	}
	return out
}

func NodesHelp() []KeyHelp {
	return []KeyHelp{
		{"↑↓", "move"},
		{"/", "filter"},
		{"enter", "pod detail"},
		{"esc", "back"},
		{"m", "menu"},
		{"?", "help"},
		{"q", "quit"},
	}
}

func FullHelpNodes() []KeyHelp {
	return []KeyHelp{
		{"↑/↓ k/j", "move selection"},
		{"/", "filter pods on this node"},
		{"enter", "open pod detail"},
		{"esc", "clear filter, back"},
		{"m", "screens menu ← → enter"},
		{"2", "Dashboard screen"},
		{"3", "Workloads screen"},
		{"4", "Nodes screen"},
		{"6", "Events screen"},
		{"c n", "switch context / namespace"},
		{"b", "navigation tree"},
		{"A", "ack all Security alerts"},
		{"p", "cycle palette"},
		{"ctrl+p", "pause animations"},
		{"?", "toggle help"},
		{"q ctrl+c", "quit"},
	}
}

func HelpOverlay(screen draw.Region, items []KeyHelp, ctx Ctx) {
	half := (len(items) + 1) / 2
	const keyW, descW = 11, 30
	w := min(screen.W-4, 2*(keyW+descW)+7)
	extra := 0
	for _, item := range items {
		if item.Key == "S" {
			extra = 4
			break
		}
	}
	h := min(screen.H-2, half+4+extra)
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
	if extra > 0 {
		for i, line := range []string{"Security score v" + data.SecurityScoreVersion + ": max(0,100-40H-6M-I), per pod and dimension.", "Each rule once at worst severity; Security and Hygiene are separate.", "Scope: mean, median, lowest ceil(N/10) mean. SEC = Security; p = partial.", "Unknown is not a penalty. Score is not safety or PSS compliance."} {
			inner.Text(0, half+i, draw.Truncate(line, inner.W), draw.Fg(theme.Muted))
		}
	}
}

type SmallState struct {
	W, H       int
	MinW, MinH int
	Synthetic  bool
	Cluster    data.Cluster
	Pods       int
	Namespaces int
}

func missingText(st SmallState) string {
	cols, rows := max(0, st.MinW-st.W), max(0, st.MinH-st.H)
	switch {
	case cols > 0 && rows > 0:
		return fmt.Sprintf("faltam %d colunas e %d linhas", cols, rows)
	case cols > 0:
		return fmt.Sprintf("faltam %d colunas", cols)
	}
	return fmt.Sprintf("faltam %d linhas", rows)
}

func TooSmall(screen draw.Region, st SmallState, ctx Ctx) {
	screen.Fill(theme.BgApp)
	if screen.Empty() {
		return
	}
	logo := draw.Line{}
	colors := theme.TextGradient(ctx.Pal, len("lazykube"), ctx.Clock.Phase)
	for i, ch := range "lazykube" {
		logo = append(logo, draw.S(string(ch), draw.Fg(colors[i]).WithBold()))
	}
	if st.Synthetic {
		logo = append(logo, draw.S(" [DEMO]", draw.Fg(theme.Warn).WithBold()))
	}
	target := draw.Line{muted("context "), text(draw.Sanitize(st.Cluster.Context)), muted("  namespace "), text(headerNamespace(st.Cluster))}
	summary := draw.Line{faint(fmt.Sprintf("%d pods · %d namespaces observados", st.Pods, st.Namespaces))}
	keys := draw.Line{bold("q", ctx.Pal.A), muted(" sair  "), bold("c", ctx.Pal.A), muted(" contexto  "), bold("n", ctx.Pal.A), muted(" namespace")}
	type row struct {
		line draw.Line
		keep int
	}
	rows := []row{
		{logo, 3},
		{target, 1},
		{nil, 0},
		{draw.Line{colored(fmt.Sprintf("terminal pequeno: %d×%d", st.W, st.H), theme.Warn)}, 3},
		{draw.Line{text(fmt.Sprintf("a interface completa precisa de %d×%d", st.MinW, st.MinH))}, 3},
		{draw.Line{muted(missingText(st))}, 2},
		{nil, 0},
		{summary, 1},
		{nil, 0},
		{keys, 3},
	}
	for level := 0; len(rows) > screen.H && level <= 2; level++ {
		kept := rows[:0]
		for _, r := range rows {
			if r.keep > level {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	y := max(0, (screen.H-len(rows))/2)
	for i, r := range rows {
		l := r.line.Truncate(screen.W)
		screen.Line(max(0, (screen.W-l.Width())/2), y+i, l)
	}
}

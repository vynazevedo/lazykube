package ui

import (
	"fmt"
	"strings"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

type LogInfo struct {
	Container  string
	Containers int
	Previous   bool
	State      data.FeedState
	Reason     string
	Query      string
	Searching  bool
	Matches    int
	Match      int
	MatchRow   int
}

func IndexFold(s, q string) int {
	if q == "" {
		return -1
	}
	ls, lq := strings.ToLower(s), strings.ToLower(q)
	if len(ls) != len(s) || len(lq) != len(q) {
		return strings.Index(s, q)
	}
	return strings.Index(ls, lq)
}

func MatchRows(rows []LogRow, q string) []int {
	if q == "" {
		return nil
	}
	var out []int
	for i, r := range rows {
		if IndexFold(r.Text, q) >= 0 {
			out = append(out, i)
		}
	}
	return out
}

func highlighted(r draw.Region, x, y int, text, q string, current bool, ctx Ctx) {
	base := draw.Fg(theme.TextSoft)
	hit := draw.Fg(theme.BgApp).WithBg(theme.Mix(ctx.Pal.B, theme.BgInset, 0.25))
	if current {
		hit = draw.Fg(theme.BgApp).WithBg(theme.Warn).WithBold()
	}
	limit := r.W
	for text != "" && x < limit {
		i := IndexFold(text, q)
		if i < 0 {
			r.Text(x, y, draw.Truncate(text, limit-x), base)
			return
		}
		x += r.Text(x, y, draw.Truncate(text[:i], limit-x), base)
		if x >= limit {
			return
		}
		x += r.Text(x, y, draw.Truncate(text[i:i+len(q)], limit-x), hit)
		text = text[i+len(q):]
	}
}

func logSource(info LogInfo) string {
	if info.Container == "" {
		return ""
	}
	mode := "atual"
	if info.Previous {
		mode = "previous"
	}
	src := info.Container + " · " + mode
	if info.Containers > 1 {
		src += fmt.Sprintf(" · %d containers", info.Containers)
	}
	return src
}

func searchLine(info LogInfo, ctx Ctx) draw.Line {
	line := draw.Line{colored("/", ctx.Pal.A), text(info.Query)}
	if info.Searching && ctx.Clock.CursorVisible() {
		line = append(line, colored("▌", ctx.Pal.A))
	}
	switch {
	case info.Query == "":
	case info.Matches == 0:
		line = append(line, colored("  sem ocorrências", theme.Warn))
	default:
		line = append(line, muted(fmt.Sprintf("  %d/%d", info.Match+1, info.Matches)), faint("  n N"))
	}
	return line
}

func LogTab(r draw.Region, st DetailsState, ctx Ctx) {
	c := insetBox(r)
	if c.Empty() {
		return
	}
	info := st.LogInfo
	rows := LogRows(st.Logs, c.W, st.Wrap)
	visible := c.H - 1
	switch {
	case len(rows) == 0 && info.State == data.FeedUnavailable:
		centered(c.Sub(0, 0, c.W, visible), reasonOr(info.Reason, "logs indisponíveis"))
	case len(rows) == 0 && info.State == data.FeedLoading:
		centered(c.Sub(0, 0, c.W, visible), "conectando ao stream de logs…")
	case len(rows) == 0:
		centered(c.Sub(0, 0, c.W, visible), "sem linhas de log")
	}
	for i := 0; i < visible && st.Scroll+i < len(rows); i++ {
		n := st.Scroll + i
		row := rows[n]
		if info.Query == "" {
			logRow(c, i, row, ctx)
			continue
		}
		if !row.Cont {
			c.Text(0, i, row.Line.Time, draw.Fg(theme.Faint))
			c.Text(10, i, draw.PadRight(row.Line.Level.String(), 5), draw.Fg(levelColor(row.Line.Level, ctx)))
		}
		highlighted(c.Sub(logPrefixW, i, c.W-logPrefixW, 1), 0, 0, row.Text, info.Query, info.Matches > 0 && n == info.MatchRow, ctx)
	}
	prompt := c.H - 1
	if info.Query != "" || info.Searching {
		c.Line(0, prompt, searchLine(info, ctx).Truncate(c.W/2))
	} else {
		c.Put(0, prompt, '❯', draw.Fg(ctx.Pal.A))
		if ctx.Clock.CursorVisible() {
			c.Put(2, prompt, '▌', draw.Fg(ctx.Pal.A))
		}
	}
	right := draw.Line{}
	if !st.Follow && st.NewLines > 0 {
		right = append(right, colored(fmt.Sprintf("↓ %d new", st.NewLines), ctx.Pal.A), muted("  "))
	}
	if len(rows) > 0 && info.Reason != "" {
		right = append(right, colored("reconectando  ", theme.Warn))
	}
	if src := logSource(info); src != "" {
		color := theme.Muted
		if info.Previous {
			color = theme.Warn
		}
		right = append(right, colored(src, color))
	}
	if len(right) > 0 {
		c.LineRight(prompt, right.Truncate(c.W/2))
	}
}

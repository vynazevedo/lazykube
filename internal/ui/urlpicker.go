package ui

import (
	"fmt"
	"strings"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/netpath"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/weburl"
)

const (
	urlPickerW     = 110
	urlPickerH     = 24
	urlPickerFrame = 5
)

type URLPickerState struct {
	Title  string
	Items  []netpath.Address
	Cursor int
}

func (st URLPickerState) Selected() (netpath.Address, bool) {
	if st.Cursor < 0 || st.Cursor >= len(st.Items) {
		return netpath.Address{}, false
	}
	return st.Items[st.Cursor], true
}

func urlPickerRect(screenW, screenH int) Rect {
	w, h := max(0, min(screenW-4, urlPickerW)), max(0, min(screenH-4, urlPickerH))
	return Rect{X: (screenW - w) / 2, Y: (screenH - h) / 2, W: w, H: h}
}

func urlPickerInner(screenW, screenH int) (int, int) {
	r := urlPickerRect(screenW, screenH)
	return max(0, r.W-6), max(0, r.H-4)
}

func URLPickerLines(screenW, screenH int, u weburl.URL) []string {
	w, h := urlPickerInner(screenW, screenH)
	text := u.String()
	if w == 0 || text == "" {
		return nil
	}
	var lines []string
	for len(text) > w {
		lines = append(lines, text[:w])
		text = text[w:]
	}
	lines = append(lines, text)
	if len(lines)+urlPickerFrame+1 > h {
		return nil
	}
	return lines
}

func hostSpan(u weburl.URL) (int, int) {
	at := strings.Index(u.String(), "://") + len("://")
	start := at + strings.Index(u.String()[at:], u.Host())
	return start, start + len(u.Host())
}

func urlLines(r draw.Region, y int, lines []string, u weburl.URL, ctx Ctx) {
	from, to := hostSpan(u)
	at := 0
	for i, line := range lines {
		for x := range len(line) {
			style := draw.Fg(theme.TextSoft)
			if at >= from && at < to {
				style = draw.Fg(ctx.Pal.A).WithBold()
			}
			r.Put(x, y+i, rune(line[x]), style)
			at++
		}
	}
}

func URLPicker(screen draw.Region, st URLPickerState, ctx Ctx) {
	rect := urlPickerRect(screen.W, screen.H)
	box := screen.Sub(rect.X, rect.Y, rect.W, rect.H)
	inner := Panel(box, "Copiar URL · "+draw.Sanitize(st.Title), true, ctx).Inset(2, 1, 2, 1)
	item, ok := st.Selected()
	if inner.Empty() || !ok {
		return
	}
	lines := URLPickerLines(screen.W, screen.H, item.URL)
	body := max(1, len(lines))
	rows := max(1, inner.H-body-urlPickerFrame)
	offset := ScrollOffset(st.Cursor, len(st.Items), rows)
	for i := 0; i < rows && offset+i < len(st.Items); i++ {
		a := st.Items[offset+i]
		mark, style := "  ", draw.Fg(theme.TextMid)
		if offset+i == st.Cursor {
			mark, style = "› ", draw.Fg(theme.Text).WithBold()
		}
		glyph, color := healthGlyph(stateHealth(a.State), ctx)
		inner.Line(0, i, draw.Line{draw.S(mark, style), colored(glyph+" ", color), muted(draw.PadRight(a.Origin.String(), originW-2)), draw.S(draw.Sanitize(a.Display), style)}.Truncate(inner.W))
	}
	y := rows
	inner.HLine(0, y, inner.W, '─', draw.Fg(theme.BorderSoft))
	inner.Line(0, y+1, draw.Line{muted(fmt.Sprintf("URL completa, %d de %d; host em destaque", st.Cursor+1, len(st.Items)))}.Truncate(inner.W))
	copyable := len(lines) > 0
	switch {
	case copyable:
		urlLines(inner, y+2, lines, item.URL, ctx)
	case item.URL.IsZero():
		inner.Line(0, y+2, draw.Line{colored("sem URL copiável: "+draw.Sanitize(item.Refused), theme.Warn)}.Truncate(inner.W))
	default:
		inner.Line(0, y+2, draw.Line{colored("a URL inteira não cabe neste terminal; aumente a janela", theme.Warn)}.Truncate(inner.W))
	}
	inner.Line(0, y+2+body, draw.Line{muted(item.State.String() + " · " + item.Reach.String())}.Truncate(inner.W))
	if item.URL.Punycode() {
		inner.Line(0, y+3+body, draw.Line{colored("host em punycode (xn--): confira o domínio", theme.Warn)}.Truncate(inner.W))
	}
	hint := "↑↓ escolhe · enter copia o texto acima · esc fecha"
	if !copyable {
		hint = "↑↓ escolhe · esc fecha"
	}
	inner.Line(0, inner.H-1, draw.Line{faint(hint)}.Truncate(inner.W))
}

func URLPickerHelp() []KeyHelp {
	return []KeyHelp{{"↑↓", "choose"}, {"enter", "copy"}, {"esc", "close"}, {"q", "quit"}}
}

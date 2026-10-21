package ui

import (
	"fmt"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

const (
	EventsTableTop  = 1
	eventsFeedLimit = 200
)

type EventsScreenState struct {
	Feed       data.EventFeed
	Scope      data.EventScope
	Pod        string
	Namespace  string
	Connected  string
	Full       bool
	Cursor     int
	Reading    bool
	MsgScroll  int
	Selectable bool
	Held       *data.Event
	Gone       bool
}

func (st EventsScreenState) Selected() *data.Event {
	if st.Gone && st.Held != nil {
		return st.Held
	}
	if !st.Selectable || st.Cursor < 0 || st.Cursor >= len(st.Feed.Items) {
		return nil
	}
	return &st.Feed.Items[st.Cursor]
}

func EventsTableRows(panelH int) int {
	return max(0, panelH-2-EventsTableTop-1)
}

func EventMessageRows(panelH int) int {
	return max(0, panelH-2)
}

func eventsTarget(st EventsScreenState) string {
	switch st.Scope {
	case data.ScopePod:
		if st.Pod == "" {
			return "pod: nenhum selecionado"
		}
		return "pod " + draw.Sanitize(st.Pod)
	case data.ScopeNamespace:
		return "namespace " + draw.Sanitize(st.Namespace)
	}
	return "cluster"
}

func feedStateText(st EventsScreenState) (string, theme.RGB) {
	if !st.Selectable {
		return "sem alvo", theme.Warn
	}
	switch st.Feed.State {
	case data.FeedLoading:
		return "carregando", theme.Muted
	case data.FeedForbidden:
		return "sem permissão", theme.Err
	case data.FeedUnavailable:
		return "indisponível", theme.Warn
	}
	return "disponível", theme.OK
}

func EventsScopeLines(st EventsScreenState) []draw.Line {
	state, color := feedStateText(st)
	lines := []draw.Line{{muted("Escopo: "), text(eventsTarget(st)), faint(" | "), muted("estado: "), colored(state, color)}}
	if st.Selectable && st.Feed.Reason != "" {
		lines = append(lines, draw.Line{muted("  "), text(draw.Sanitize(st.Feed.Reason))})
	}
	if st.Connected == "" {
		lines = append(lines, draw.Line{muted("Conexão: "), text("todos os namespaces")})
	} else {
		lines = append(lines, draw.Line{muted("Conexão: "), text("restrita a " + draw.Sanitize(st.Connected)), faint("; cluster só com E, amplia a leitura")})
	}
	if st.Full {
		lines = append(lines, append(draw.Line{muted("Escolha explícita: ")}, scopeChips(st.Scope)...))
	} else {
		lines = append(lines, draw.Line{colored("Fonte sem Diagnostics: apenas eventos do pod", theme.Warn)})
	}
	return append(lines,
		draw.Line{faint(fmt.Sprintf("Feed limitado: até %d eventos de namespace/cluster; não é histórico durável", eventsFeedLimit))},
		draw.Line{faint("Acesso negado e feed vazio são estados diferentes")},
	)
}

func EventsScopePanel(r draw.Region, st EventsScreenState, ctx Ctx) {
	inner := Panel(r, "Escopo e cobertura", false, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	for y, l := range EventsScopeLines(st) {
		if y >= inner.H {
			break
		}
		inner.Line(0, y, l.Truncate(inner.W))
	}
}

type eventColumns struct {
	ns, kind, obj, count, age int
}

func eventsColumns(w int) eventColumns {
	c := eventColumns{ns: 16, kind: 8, obj: min(30, max(12, w/5)), count: 5, age: 6}
	if w < 140 {
		c.age = 0
	}
	return c
}

func (c eventColumns) message(w int) int {
	fixed := 2 + c.ns + c.kind + c.obj + c.count + 4
	if c.age > 0 {
		fixed += c.age + 1
	}
	return max(8, w-fixed-1)
}

func eventsRow(c eventColumns, w int, e data.Event, selected bool, style func(theme.RGB) draw.Style) draw.Line {
	msgW := c.message(w)
	mark := "  "
	if selected {
		mark = "› "
	}
	typeColor := theme.OK
	if e.Type == "Warning" {
		typeColor = theme.Warn
	}
	cell := func(s string, width int, color theme.RGB) draw.Span {
		return draw.S(draw.PadRight(draw.Truncate(draw.Sanitize(s), width), width), style(color))
	}
	line := draw.Line{
		draw.S(mark, style(theme.Text)),
		cell(e.Message, msgW, theme.TextSoft), draw.S(" ", style(theme.Text)),
		cell(e.Namespace, c.ns, theme.TextMid), draw.S(" ", style(theme.Text)),
		cell(e.Type, c.kind, typeColor), draw.S(" ", style(theme.Text)),
		cell(e.Object, c.obj, theme.Text), draw.S(" ", style(theme.Text)),
		draw.S(draw.PadLeft(fmt.Sprintf("%d", e.Count), c.count), style(theme.Text)),
	}
	if c.age > 0 {
		line = append(line, draw.S(" ", style(theme.Text)), draw.S(draw.PadLeft(draw.Truncate(draw.Sanitize(e.Age), c.age), c.age), style(theme.Muted)))
	}
	return line
}

func eventsHeader(c eventColumns, w int) draw.Line {
	hdr := func(s string, width int) draw.Span { return muted(draw.PadRight(s, width)) }
	line := draw.Line{
		muted("  "),
		hdr("MENSAGEM", c.message(w)), muted(" "),
		hdr("NAMESPACE", c.ns), muted(" "),
		hdr("TIPO", c.kind), muted(" "),
		hdr("OBJETO", c.obj), muted(" "),
		muted(draw.PadLeft("COUNT", c.count)),
	}
	if c.age > 0 {
		line = append(line, muted(" "), muted(draw.PadLeft("IDADE", c.age)))
	}
	return line
}

func EventsTable(r draw.Region, st EventsScreenState, ctx Ctx) {
	title := "Eventos"
	if !st.Reading {
		title = "Eventos · focus"
	}
	inner := Panel(r, title, !st.Reading, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	cols := eventsColumns(inner.W)
	inner.Line(0, 0, eventsHeader(cols, inner.W).Truncate(inner.W))
	rows := EventsTableRows(r.H)
	body := inner.Sub(0, EventsTableTop, inner.W, rows)
	feed := st.Feed
	switch {
	case !st.Selectable:
		centeredColor(body, "nenhum pod selecionado: esta fonte oferece apenas eventos do pod", theme.Warn)
	case feed.State == data.FeedForbidden:
		centeredColor(body, draw.Sanitize(reasonOr(feed.Reason, "sem permissão: events")), theme.Err)
	case feed.State == data.FeedLoading:
		centered(body, draw.Sanitize(reasonOr(feed.Reason, "carregando eventos…")))
	case feed.State == data.FeedUnavailable:
		centeredColor(body, draw.Sanitize(reasonOr(feed.Reason, "eventos indisponíveis")), theme.Warn)
	case len(feed.Items) == 0:
		centered(body, "nenhum evento neste escopo")
	default:
		off := ScrollOffset(st.Cursor, len(feed.Items), rows)
		for i := 0; i < rows && off+i < len(feed.Items); i++ {
			idx := off + i
			style := func(c theme.RGB) draw.Style { return draw.Fg(c) }
			if !st.Gone && idx == st.Cursor {
				style = func(c theme.RGB) draw.Style { return draw.Fg(c).WithBg(theme.BorderFaint).WithBold() }
			}
			body.Line(0, i, eventsRow(cols, inner.W, feed.Items[idx], !st.Gone && idx == st.Cursor, style).Truncate(inner.W))
		}
	}
	if inner.H > rows+EventsTableTop {
		inner.Line(0, inner.H-1, draw.Line{faint(fmt.Sprintf("%d eventos · enter abre a mensagem completa, sem resolver o objeto pelo texto", len(feed.Items)))}.Truncate(inner.W))
	}
}

func EventMessageLines(st EventsScreenState, width int) []draw.Line {
	e := st.Selected()
	if e == nil {
		return []draw.Line{{faint("nenhum evento selecionado")}}
	}
	var lines []draw.Line
	if st.Gone {
		lines = append(lines, draw.Line{colored("evento não está mais no feed; exibindo a última leitura", theme.Warn)})
	}
	lines = append(lines, []draw.Line{
		{muted("Evento: "), text(draw.Sanitize(e.Type + " " + e.Reason)), faint(" · "), text(draw.Sanitize(e.Object))},
		{muted("Namespace: "), text(draw.Sanitize(e.Namespace)), faint(" · "), muted("count "), text(fmt.Sprintf("%d", e.Count)), faint(" · "), muted("idade "), text(draw.Sanitize(e.Age))},
		nil,
	}...)
	for _, l := range draw.Wrap(draw.Sanitize(e.Message), max(1, width)) {
		lines = append(lines, draw.Line{text(l)})
	}
	return append(lines, nil, draw.Line{faint("Navegação ao recurso por referência tipada: fase futura")})
}

func EventMessagePanel(r draw.Region, st EventsScreenState, ctx Ctx) {
	title := "Mensagem completa"
	if st.Reading {
		title = "Mensagem completa · focus"
	}
	inner := Panel(r, title, st.Reading, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	lines := EventMessageLines(st, inner.W)
	scroll := max(0, min(st.MsgScroll, len(lines)-inner.H))
	for y := 0; y < inner.H && scroll+y < len(lines); y++ {
		inner.Line(0, y, lines[scroll+y].Truncate(inner.W))
	}
}

func EventsHelp(full bool) []KeyHelp {
	out := []KeyHelp{{"↑↓", "move"}}
	if full {
		out = append(out, KeyHelp{"E", "scope"})
	}
	return append(out,
		KeyHelp{"enter", "full message"},
		KeyHelp{"esc", "back"},
		KeyHelp{"m", "menu"},
		KeyHelp{"?", "help"},
		KeyHelp{"q", "quit"},
	)
}

func FullHelpEvents(full bool) []KeyHelp {
	out := []KeyHelp{
		{"↑/↓ k/j", "move / scroll message"},
		{"pgup/pgdn", "page"},
		{"home/end", "first / last"},
		{"enter", "read full message"},
		{"esc", "table, back"},
	}
	if full {
		out = append(out, KeyHelp{"E", "scope pod / ns / cluster"})
	}
	return append(out,
		KeyHelp{"m", "screens menu ← → enter"},
		KeyHelp{"2", "Dashboard screen"},
		KeyHelp{"3", "Workloads screen"},
		KeyHelp{"4", "Nodes screen"},
		KeyHelp{"6", "Events screen"},
		KeyHelp{"A", "ack all Security alerts"},
		KeyHelp{"p", "cycle palette"},
		KeyHelp{"ctrl+p", "pause animations"},
		KeyHelp{"?", "toggle help"},
		KeyHelp{"q ctrl+c", "quit"},
	)
}

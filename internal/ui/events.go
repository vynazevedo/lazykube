package ui

import (
	"fmt"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

func scopeChips(active data.EventScope) draw.Line {
	line := draw.Line{faint("E ")}
	for i, s := range data.EventScopes() {
		if i > 0 {
			line = append(line, faint(" "))
		}
		label := s.String()
		if s == data.ScopeNamespace {
			label = "ns"
		}
		if s == active {
			line = append(line, draw.S(" "+label+" ", draw.Fg(theme.BgApp).WithBg(theme.TextMid).WithBold()))
		} else {
			line = append(line, muted(" "+label+" "))
		}
	}
	return line
}

func eventMessage(e data.Event) string {
	if e.Count > 1 {
		return fmt.Sprintf("×%d %s", e.Count, e.Message)
	}
	return e.Message
}

func eventObject(e data.Event, scope data.EventScope, width int) string {
	if scope == data.ScopeCluster && e.Namespace != "" && draw.Width(e.Namespace)+draw.Width(e.Object)+1 <= width {
		return e.Namespace + "/" + e.Object
	}
	return e.Object
}

func EventsTab(r draw.Region, st DetailsState) {
	c := insetBox(r)
	if c.Empty() {
		return
	}
	feed := st.Events
	const ageW, typeW, reasonW = 6, 8, 14
	typeX := ageW + 1
	reasonX := typeX + typeW + 1
	msgX := reasonX + reasonW + 1
	objW := 0
	if feed.Scope != data.ScopePod {
		objW = min(30, max(12, c.W/4))
	}
	objX := msgX
	msgX += objW + min(objW, 1)
	chips := scopeChips(feed.Scope)
	hdr := draw.Fg(theme.Muted)
	c.Text(0, 0, "AGE", hdr)
	c.Text(typeX, 0, "TYPE", hdr)
	c.Text(reasonX, 0, "REASON", hdr)
	if objW > 0 {
		c.Text(objX, 0, "OBJECT", hdr)
	}
	if c.W-chips.Width() > msgX+8 {
		c.Text(msgX, 0, "MESSAGE", hdr)
		c.LineRight(0, chips)
	} else {
		c.Text(msgX, 0, "MESSAGE", hdr)
	}
	body := c.Sub(0, 1, c.W, c.H-1)
	switch {
	case feed.State == data.FeedForbidden:
		centeredColor(body, reasonOr(feed.Reason, "sem permissão: events"), theme.Err)
		return
	case feed.State == data.FeedLoading:
		centered(body, reasonOr(feed.Reason, "carregando eventos…"))
		return
	case feed.State == data.FeedUnavailable:
		centeredColor(body, reasonOr(feed.Reason, "eventos indisponíveis"), theme.Warn)
		return
	case len(feed.Items) == 0:
		centered(body, "nenhum evento neste escopo")
		return
	}
	for i := 0; i < body.H && st.Scroll+i < len(feed.Items); i++ {
		e := feed.Items[st.Scroll+i]
		typeColor := theme.OK
		if e.Type == "Warning" {
			typeColor = theme.Warn
		}
		body.Text(0, i, draw.Truncate(e.Age, ageW), draw.Fg(theme.Muted))
		body.Text(typeX, i, draw.Truncate(e.Type, typeW), draw.Fg(typeColor))
		body.Text(reasonX, i, draw.Truncate(e.Reason, reasonW), draw.Fg(theme.Text))
		if objW > 0 {
			body.Text(objX, i, draw.Truncate(eventObject(e, feed.Scope, objW), objW), draw.Fg(theme.TextMid))
		}
		body.Text(msgX, i, draw.Truncate(eventMessage(e), max(0, body.W-msgX)), draw.Fg(theme.TextSoft))
	}
}

func centeredColor(r draw.Region, msg string, color theme.RGB) {
	msg = draw.Truncate(msg, r.W)
	r.Text(max(0, (r.W-draw.Width(msg))/2), r.H/2, msg, draw.Fg(color))
}

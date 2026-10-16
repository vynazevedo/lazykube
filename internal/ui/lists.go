package ui

import (
	"fmt"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

const selectionMix = 0.22

func SelectionBg(pal theme.Palette, k float64) theme.RGB {
	return theme.Mix(theme.BgPanel, pal.B, k)
}

type NamespacesState struct {
	Items     []data.Namespace
	Highlight string
	Filter    string
	Cursor    int
	Focused   bool
}

func NamespacesPanel(r draw.Region, st NamespacesState, ctx Ctx) {
	inner := Panel(r, "Namespaces", st.Focused, ctx)
	top, _ := NamespacesRows(r.H)
	c := inner.Inset(1, top-1, 1, 1)
	const countW, barW = 3, 5
	nameW := c.W - barW - countW - 2
	offset := ScrollOffset(st.Cursor, len(st.Items), c.H)
	for row := 0; row < c.H && offset+row < len(st.Items); row++ {
		i := offset + row
		ns := st.Items[i]
		if st.Focused && i == st.Cursor {
			inner.Sub(0, top-1+row, inner.W, 1).FillBg(SelectionBg(ctx.Pal, selectionMix))
		}
		nameStyle := draw.Fg(theme.Text)
		if ns.Name == st.Highlight || ns.Name == st.Filter {
			nameStyle = draw.Fg(ctx.Pal.A)
		}
		if ns.Name == st.Filter {
			nameStyle = nameStyle.WithBold()
		}
		c.Text(0, row, draw.Truncate(ns.Name, nameW), nameStyle)
		bar := "·····"
		if ns.Pods > 0 {
			full := draw.Filled(float64(ns.Pods), 5, barW)
			bar = strings.Repeat("█", full) + strings.Repeat("░", barW-full)
		}
		c.Text(c.W-countW-1-barW, row, bar, draw.Fg(ctx.Pal.B))
		c.Text(c.W-countW, row, draw.PadLeft(fmt.Sprint(ns.Pods), countW), draw.Fg(theme.Muted))
	}
}

type DeploymentsState struct {
	Coverage data.Coverage
	Items    []data.Deployment
	Cursor   int
	Focused  bool
}

func DeploymentsPanel(r draw.Region, st DeploymentsState, ctx Ctx) {
	inner := Panel(r, "Deployments", st.Focused, ctx)
	top, rows := DeploymentsRows(r.H)
	padY := top - 1
	c := inner.Sub(1, padY, inner.W-2, rows)
	if st.Coverage.State != "" && st.Coverage.State != data.CoverageComplete {
		c.Text(0, 0, draw.Truncate("Deployments: "+string(st.Coverage.State), c.W), draw.Fg(theme.Warn))
		return
	}
	offset := ScrollOffset(st.Cursor, len(st.Items), c.H)
	for row := 0; row < c.H && offset+row < len(st.Items); row++ {
		i := offset + row
		d := st.Items[i]
		if st.Focused && i == st.Cursor {
			inner.Sub(0, padY+row, inner.W, 1).FillBg(SelectionBg(ctx.Pal, selectionMix))
		}
		dot := theme.OK
		switch {
		case d.Ready == 0 && d.Desired > 0:
			dot = theme.Err
		case d.Ready < d.Desired:
			dot = theme.Warn
		}
		right := draw.Line{colored("●", dot), muted(" " + fmt.Sprintf("%d/%d", d.Ready, d.Desired))}
		name := draw.Line{muted(d.Namespace + "/"), text(d.Name)}.Truncate(c.W - right.Width() - 1)
		c.Line(0, row, name)
		c.LineRight(row, right)
	}
}

func ScrollOffset(cursor, total, visible int) int {
	if visible <= 0 || total <= visible {
		return 0
	}
	off := cursor - visible + 1
	if off < 0 {
		off = 0
	}
	if off > total-visible {
		off = total - visible
	}
	return off
}

func NamespacesRows(panelH int) (top, rows int) {
	return 2, max(0, panelH-4)
}

func DeploymentsRows(panelH int) (top, rows int) {
	padY := 1
	if panelH-2 < 4 {
		padY = 0
	}
	return 1 + padY, max(0, panelH-2-2*padY)
}

func ListRowAt(relY, top, rows, cursor, total int) (int, bool) {
	row := relY - top
	if row < 0 || row >= rows {
		return 0, false
	}
	idx := ScrollOffset(cursor, total, rows) + row
	if idx >= total {
		return 0, false
	}
	return idx, true
}

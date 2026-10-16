package ui

import (
	"fmt"
	"strings"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

type Diagnosis struct {
	Containers   []data.Container
	LogContainer string
}

type RowKind int

const (
	RowSection RowKind = iota
	RowContainer
	RowDetail
	RowHint
	RowField
	RowBlank
)

type Health int

const (
	HealthOK Health = iota
	HealthPending
	HealthWarn
	HealthFailed
)

type DescribeRow struct {
	Kind     RowKind
	Health   Health
	Label    string
	Value    string
	Selected bool
}

const (
	diagKindW  = 10
	diagNameW  = 18
	diagLabelW = 10
	fieldKeyW  = 18
)

var benignWaiting = map[string]bool{
	"":                     true,
	"ContainerCreating":    true,
	"PodInitializing":      true,
	"sem status reportado": true,
}

func ContainerHealth(c data.Container) Health {
	switch c.Phase {
	case data.PhaseRunning:
		if c.Ready || c.Kind == data.ContainerInit || c.Kind == data.ContainerEphemeral {
			return HealthOK
		}
		return HealthWarn
	case data.PhaseTerminated:
		if c.Current != nil && c.Current.ExitCode == 0 {
			return HealthOK
		}
		return HealthFailed
	default:
		if benignWaiting[c.Reason] {
			return HealthPending
		}
		return HealthFailed
	}
}

func mark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func containerSummary(c data.Container) string {
	switch c.Phase {
	case data.PhaseRunning:
		parts := []string{"running"}
		if c.Since != "" {
			parts[0] += " " + c.Since
		}
		parts = append(parts, "ready "+mark(c.Ready), "started "+mark(c.Started))
		return strings.Join(append(parts, fmt.Sprintf("restarts %d", c.Restarts)), " · ")
	case data.PhaseTerminated:
		if c.Current != nil {
			return c.Current.Summary()
		}
		return "terminated"
	default:
		reason := c.Reason
		if reason == "" {
			reason = "waiting"
		}
		return fmt.Sprintf("%s · restarts %d", reason, c.Restarts)
	}
}

func DescribeRows(d Diagnosis, fields []data.Field) []DescribeRow {
	var rows []DescribeRow
	if len(d.Containers) > 0 {
		ready, apps := 0, 0
		for _, c := range d.Containers {
			if c.Kind == data.ContainerApp {
				apps++
				if c.Ready {
					ready++
				}
			}
		}
		rows = append(rows, DescribeRow{Kind: RowSection, Label: "Containers", Value: fmt.Sprintf("%d · ready %d/%d", len(d.Containers), ready, apps)})
		for _, c := range d.Containers {
			rows = append(rows, DescribeRow{
				Kind:     RowContainer,
				Health:   ContainerHealth(c),
				Label:    c.Kind.String() + " " + c.Name,
				Value:    containerSummary(c),
				Selected: c.Name == d.LogContainer,
			})
			if c.Message != "" {
				rows = append(rows, DescribeRow{Kind: RowDetail, Label: "message", Value: c.Message})
			}
			if c.Current != nil && c.Current.Message != "" {
				rows = append(rows, DescribeRow{Kind: RowDetail, Label: "message", Value: c.Current.Message})
			}
			if c.Last != nil {
				rows = append(rows, DescribeRow{Kind: RowDetail, Label: "last", Value: c.Last.Summary()})
				if c.Last.Message != "" {
					rows = append(rows, DescribeRow{Kind: RowDetail, Label: "last msg", Value: c.Last.Message})
				}
			}
			if hint := nextStep(c); hint != "" {
				rows = append(rows, DescribeRow{Kind: RowHint, Value: hint})
			}
		}
		rows = append(rows, DescribeRow{Kind: RowBlank})
	}
	for _, f := range fields {
		rows = append(rows, DescribeRow{Kind: RowField, Label: f.Key, Value: f.Value})
	}
	return rows
}

func nextStep(c data.Container) string {
	switch {
	case c.Last != nil:
		return "P logs anteriores · C troca container · E eventos"
	case ContainerHealth(c) == HealthFailed:
		return "E eventos do pod · l logs"
	case ContainerHealth(c) == HealthWarn:
		return "E eventos (probes) · l logs"
	default:
		return ""
	}
}

func healthGlyph(h Health, ctx Ctx) (string, theme.RGB) {
	switch h {
	case HealthOK:
		return "●", theme.OK
	case HealthPending:
		return string(ctx.Clock.Spinner()), theme.Warn
	case HealthWarn:
		return "●", theme.Warn
	default:
		return "✗", theme.Err
	}
}

func healthColor(h Health) theme.RGB {
	switch h {
	case HealthOK:
		return theme.Text
	case HealthPending, HealthWarn:
		return theme.Warn
	default:
		return theme.Err
	}
}

func describeRow(r draw.Region, y int, row DescribeRow, ctx Ctx) {
	switch row.Kind {
	case RowSection:
		x := r.Text(0, y, row.Label, draw.Fg(ctx.Pal.B).WithBold())
		r.Text(x+2, y, draw.Truncate(row.Value, max(0, r.W-x-2)), draw.Fg(theme.Muted))
	case RowContainer:
		glyph, gc := healthGlyph(row.Health, ctx)
		r.Text(0, y, glyph, draw.Fg(gc))
		kind, name, _ := strings.Cut(row.Label, " ")
		r.Text(2, y, draw.PadRight(kind, diagKindW-2), draw.Fg(theme.Muted))
		r.Text(diagKindW, y, draw.Truncate(name, diagNameW-1), draw.Fg(theme.Text).WithBold())
		x := diagKindW + diagNameW
		limit := r.W
		if row.Selected {
			limit = r.LineRight(y, draw.Line{colored("◂ logs", ctx.Pal.A)}) - 1
		}
		r.Text(x, y, draw.Truncate(row.Value, max(0, limit-x)), draw.Fg(healthColor(row.Health)))
	case RowDetail:
		r.Text(diagKindW, y, draw.PadRight(row.Label, diagLabelW), draw.Fg(theme.Muted))
		x := diagKindW + diagLabelW
		r.Text(x, y, draw.Truncate(row.Value, max(0, r.W-x)), draw.Fg(theme.TextSoft))
	case RowHint:
		r.Text(diagKindW, y, draw.Truncate("↳ "+row.Value, max(0, r.W-diagKindW)), draw.Fg(theme.Faint))
	case RowField:
		r.Text(0, y, draw.PadRight(row.Label, fieldKeyW), draw.Fg(theme.Muted))
		r.Text(fieldKeyW, y, draw.Truncate(row.Value, max(0, r.W-fieldKeyW)), draw.Fg(theme.Text))
	}
}

func DescribeTab(r draw.Region, st DetailsState, ctx Ctx) {
	c := insetBox(r)
	rows := DescribeRows(st.Diag, st.Describe)
	for i := 0; i < c.H && st.Scroll+i < len(rows); i++ {
		describeRow(c, i, rows[st.Scroll+i], ctx)
	}
}

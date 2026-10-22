package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

type ResourceTableState struct {
	Label          string
	Kind           data.WorkloadKind
	Supported      bool
	CatalogLoading bool
	Availability   data.ResourceAvailability
	Loading        bool
	Err            string
	Page           data.ResourcePage
	Cursor         int
	Scope          string
}

const ResourceTableTop = 2

func ResourceTableRows(panelH int) int {
	return max(0, panelH-2-ResourceTableTop-1)
}

type resourceColumn struct {
	title string
	width int
	right bool
	value func(data.ResourceRow, time.Time) string
}

func count(c data.ResourceCount) string {
	if !c.Known {
		return "?"
	}
	return fmt.Sprint(c.Value)
}

func ratio(a, b data.ResourceCount) string {
	return count(a) + "/" + count(b)
}

func since(at, ref time.Time) string {
	if at.IsZero() || ref.IsZero() || ref.Before(at) {
		return "-"
	}
	d := ref.Sub(at)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func resourceColumns(kind data.WorkloadKind, w int) []resourceColumn {
	num := func(title string, f func(data.WorkloadSummary) data.ResourceCount) resourceColumn {
		return resourceColumn{title, max(len(title), 4), true, func(r data.ResourceRow, _ time.Time) string { return count(f(r.Workload)) }}
	}
	var cols []resourceColumn
	switch kind {
	case data.WorkloadStatefulSet, data.WorkloadDeployment:
		cols = []resourceColumn{{"READY", 7, true, func(r data.ResourceRow, _ time.Time) string { return ratio(r.Workload.Ready, r.Workload.Desired) }}}
	case data.WorkloadDaemonSet:
		cols = []resourceColumn{
			num("DESIRED", func(w data.WorkloadSummary) data.ResourceCount { return w.Desired }),
			num("CURRENT", func(w data.WorkloadSummary) data.ResourceCount { return w.Current }),
			num("READY", func(w data.WorkloadSummary) data.ResourceCount { return w.Ready }),
			num("UPDATED", func(w data.WorkloadSummary) data.ResourceCount { return w.Updated }),
			num("AVAILABLE", func(w data.WorkloadSummary) data.ResourceCount { return w.Available }),
		}
	case data.WorkloadReplicaSet:
		cols = []resourceColumn{
			num("DESIRED", func(w data.WorkloadSummary) data.ResourceCount { return w.Desired }),
			num("CURRENT", func(w data.WorkloadSummary) data.ResourceCount { return w.Current }),
			num("READY", func(w data.WorkloadSummary) data.ResourceCount { return w.Ready }),
		}
	case data.WorkloadJob:
		cols = []resourceColumn{
			{"COMPLETIONS", 11, true, func(r data.ResourceRow, _ time.Time) string { return ratio(r.Workload.Succeeded, r.Workload.Desired) }},
			num("ACTIVE", func(w data.WorkloadSummary) data.ResourceCount { return w.Active }),
			num("FAILED", func(w data.WorkloadSummary) data.ResourceCount { return w.Failed }),
		}
	case data.WorkloadCronJob:
		cols = []resourceColumn{
			{"SCHEDULE", 14, false, func(r data.ResourceRow, _ time.Time) string { return draw.Sanitize(r.Workload.Schedule) }},
			num("ACTIVE", func(w data.WorkloadSummary) data.ResourceCount { return w.Active }),
			{"LAST", 6, true, func(r data.ResourceRow, ref time.Time) string { return since(r.Workload.LastScheduleAt, ref) }},
		}
	}
	compact := w < 100
	if compact && len(cols) > 2 {
		cols = cols[:2]
	}
	cols = append(cols, resourceColumn{"STATE", 14, false, func(r data.ResourceRow, _ time.Time) string { return string(r.Workload.State) }})
	if !compact {
		cols = append(cols, resourceColumn{"AGE", 5, true, func(r data.ResourceRow, ref time.Time) string { return since(r.Ref.CreatedAt, ref) }})
	}
	return cols
}

func resourceLayout(kind data.WorkloadKind, w int) (int, int, []resourceColumn) {
	cols := resourceColumns(kind, w)
	fixed := 3
	for _, c := range cols {
		fixed += c.width + 1
	}
	ns := 0
	if w >= 100 {
		ns = 16
		fixed += ns + 1
	}
	return max(10, w-fixed), ns, cols
}

func stateColor(s data.WorkloadState) theme.RGB {
	switch s {
	case data.WorkloadReady, data.WorkloadCompleted, data.WorkloadScheduled:
		return theme.OK
	case data.WorkloadFailed, data.WorkloadDegraded:
		return theme.Err
	case data.WorkloadProgressing, data.WorkloadWaiting:
		return theme.Warn
	}
	return theme.Muted
}

func resourceCoverageText(c data.Coverage) (string, theme.RGB) {
	reason := ""
	if c.Reason != "" {
		reason = ": " + draw.Sanitize(c.Reason)
	}
	switch c.State {
	case data.CoverageComplete:
		return "cobertura completa no escopo", theme.OK
	case data.CoverageListOnly:
		return "lista pontual, sem acompanhamento" + reason, theme.Warn
	case data.CoverageDenied:
		return "sem permissão" + reason, theme.Err
	case "":
		return "cobertura desconhecida", theme.Muted
	}
	return string(c.State) + reason, theme.Warn
}

func (st ResourceTableState) blocked() (string, theme.RGB, bool) {
	label := draw.Sanitize(st.Label)
	switch {
	case !st.Supported:
		return "esta fonte não oferece o catálogo de recursos", theme.Warn, true
	case st.CatalogLoading:
		return "lendo o catálogo de recursos…", theme.Muted, true
	case st.Availability == data.ResourceDenied:
		return "sem permissão para descobrir " + label, theme.Err, true
	case st.Availability == data.ResourceAbsent:
		return label + " não é servido por este cluster", theme.Warn, true
	case st.Availability == data.ResourcePartial, st.Availability == data.ResourceUnknown:
		return "descoberta parcial: disponibilidade de " + label + " desconhecida", theme.Warn, true
	case st.Err != "":
		return draw.Sanitize(st.Err), theme.Err, true
	case st.Loading:
		return "carregando " + label + "…", theme.Muted, true
	case st.Page.Coverage.State == data.CoverageDenied:
		return "sem permissão para listar " + label, theme.Err, true
	case len(st.Page.Items) == 0 && st.Page.Coverage.State == data.CoverageComplete:
		return "nenhum " + label + " neste escopo", theme.Muted, true
	case len(st.Page.Items) == 0:
		text, color := resourceCoverageText(st.Page.Coverage)
		return "sem itens observados; " + text, color, true
	}
	return "", theme.Text, false
}

func ResourceTable(r draw.Region, st ResourceTableState, ctx Ctx) {
	inner := Panel(r, draw.Sanitize(st.Label)+" · focus", true, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	page := st.Page
	status := draw.Line{muted("escopo "), text(draw.Sanitize(st.Scope))}
	if _, _, blocked := st.blocked(); !blocked || page.Coverage.State != "" {
		cov, color := resourceCoverageText(page.Coverage)
		status = append(status, faint("  ·  "), colored(cov, color), faint("  ·  "), muted(fmt.Sprintf("%d itens", page.Total)))
		if page.Truncated {
			status = append(status, colored("  ·  lista limitada", theme.Warn))
		}
		if page.Synthetic {
			status = append(status, colored("  ·  [DEMO]", theme.Warn))
		}
	}
	inner.Line(0, 0, status.Truncate(inner.W))
	nameW, nsW, cols := resourceLayout(st.Kind, inner.W)
	hdr := draw.Line{muted("  "), muted(draw.PadRight("NAME", nameW)), muted(" ")}
	if nsW > 0 {
		hdr = append(hdr, muted(draw.PadRight("NAMESPACE", nsW)), muted(" "))
	}
	for _, c := range cols {
		pad := draw.PadRight
		if c.right {
			pad = draw.PadLeft
		}
		hdr = append(hdr, muted(pad(c.title, c.width)), muted(" "))
	}
	inner.Line(0, 1, hdr.Truncate(inner.W))
	rows := ResourceTableRows(r.H)
	body := inner.Sub(0, ResourceTableTop, inner.W, rows)
	if msg, color, blocked := st.blocked(); blocked {
		centeredColor(body, msg, color)
		return
	}
	ref := page.ObservedAt
	off := ScrollOffset(st.Cursor, len(page.Items), rows)
	for i := 0; i < rows && off+i < len(page.Items); i++ {
		idx := off + i
		row := page.Items[idx]
		style := func(c theme.RGB) draw.Style { return draw.Fg(c) }
		mark := "  "
		if idx == st.Cursor {
			style = func(c theme.RGB) draw.Style { return draw.Fg(c).WithBg(theme.BorderFaint).WithBold() }
			mark = "› "
		}
		line := draw.Line{draw.S(mark, style(theme.Text)), draw.S(draw.PadRight(draw.Truncate(draw.Sanitize(row.Ref.Name), nameW), nameW), style(theme.Text)), draw.S(" ", style(theme.Text))}
		if nsW > 0 {
			line = append(line, draw.S(draw.PadRight(draw.Truncate(draw.Sanitize(row.Ref.Namespace), nsW), nsW), style(theme.TextMid)), draw.S(" ", style(theme.Text)))
		}
		for _, c := range cols {
			v := draw.Truncate(c.value(row, ref), c.width)
			pad := draw.PadRight
			if c.right {
				pad = draw.PadLeft
			}
			color := theme.TextSoft
			if c.title == "STATE" {
				color = stateColor(row.Workload.State)
			}
			line = append(line, draw.S(pad(v, c.width), style(color)), draw.S(" ", style(theme.Text)))
		}
		body.Line(0, i, line.Truncate(inner.W))
	}
	if inner.H > rows+ResourceTableTop {
		foot := "detalhe, YAML e relações: contrato futuro · r recarrega"
		if sel := st.selected(); sel != nil && sel.Workload.Reason != "" {
			foot = "motivo: " + draw.Sanitize(sel.Workload.Reason)
		}
		inner.Line(0, inner.H-1, draw.Line{faint(foot)}.Truncate(inner.W))
	}
}

func (st ResourceTableState) selected() *data.ResourceRow {
	if st.Cursor < 0 || st.Cursor >= len(st.Page.Items) {
		return nil
	}
	return &st.Page.Items[st.Cursor]
}

type OverviewCard struct {
	Label string
	Table ResourceTableState
}

type OverviewState struct {
	Cards  []OverviewCard
	Cursor int
	Scope  string
}

const overviewCardH = 5

func OverviewColumns(w int) int {
	switch {
	case w >= 150:
		return 3
	case w >= 90:
		return 2
	}
	return 1
}

func OverviewCardAt(w, h, total, x, y int) (int, bool) {
	cols := OverviewColumns(w)
	cw := w / cols
	row, col := (y-1)/overviewCardH, x/max(1, cw)
	if y < 1 || col >= cols {
		return 0, false
	}
	idx := row*cols + col
	return idx, idx < total
}

func distributionParts(d data.WorkloadDistribution) []struct {
	label string
	n     int
	color theme.RGB
} {
	return []struct {
		label string
		n     int
		color theme.RGB
	}{
		{"ready", d.Ready, theme.OK}, {"completed", d.Completed, theme.OK}, {"scheduled", d.Scheduled, theme.OK},
		{"progressing", d.Progressing, theme.Warn}, {"waiting", d.Waiting, theme.Warn},
		{"degraded", d.Degraded, theme.Err}, {"failed", d.Failed, theme.Err},
		{"suspended", d.Suspended, theme.Muted}, {"scaled to zero", d.ScaledToZero, theme.Muted},
		{"no targets", d.NoTargets, theme.Muted}, {"unknown", d.Unknown, theme.Muted},
	}
}

func overviewCard(r draw.Region, c OverviewCard, selected bool, ctx Ctx) {
	inner := Panel(r, draw.Sanitize(c.Label), selected, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	st := c.Table
	emptyComplete := st.Supported && st.Err == "" && !st.Loading && st.Page.Coverage.State == data.CoverageComplete
	if msg, color, blocked := st.blocked(); blocked && !emptyComplete {
		inner.Line(0, 0, draw.Line{colored(msg, color)}.Truncate(inner.W))
		return
	}
	d := st.Page.Workloads
	total := draw.Line{bold(fmt.Sprint(d.Total), theme.Text), muted(" objetos")}
	cov, color := resourceCoverageText(st.Page.Coverage)
	if st.Page.Coverage.State != data.CoverageComplete {
		total = append(total, faint("  ·  "), colored(cov, color))
	}
	inner.Line(0, 0, total.Truncate(inner.W))
	bar := inner.Sub(0, 1, inner.W, 1)
	x := 0
	var legend draw.Line
	for _, p := range distributionParts(d) {
		if p.n == 0 {
			continue
		}
		cells := max(1, p.n*inner.W/max(1, d.Total))
		for i := 0; i < cells && x < inner.W; i++ {
			bar.Put(x, 0, '█', draw.Fg(p.color))
			x++
		}
		if len(legend) > 0 {
			legend = append(legend, faint(" · "))
		}
		legend = append(legend, colored(fmt.Sprintf("%s %d", p.label, p.n), p.color))
	}
	if d.Total == 0 {
		bar.Text(0, 0, strings.Repeat("░", inner.W), draw.Fg(theme.BorderFaint))
	}
	inner.Line(0, 2, legend.Truncate(inner.W))
}

func WorkloadOverview(r draw.Region, st OverviewState, ctx Ctx) {
	r.Line(1, 0, draw.Line{muted("Workloads · escopo "), text(draw.Sanitize(st.Scope)), faint("  ·  contagens por objeto, não por réplica; enter abre a lista")}.Truncate(r.W-2))
	cols := OverviewColumns(r.W)
	cw := r.W / cols
	for i, c := range st.Cards {
		row, col := i/cols, i%cols
		y := 1 + row*overviewCardH
		if y+overviewCardH > r.H {
			break
		}
		overviewCard(r.Sub(col*cw, y, cw, overviewCardH), c, i == st.Cursor, ctx)
	}
}

func ResourceHelp() []KeyHelp {
	return []KeyHelp{{"↑↓", "move"}, {"r", "reload"}, {"esc", "back"}, {"b", "tree"}, {"m", "menu"}, {"?", "help"}, {"q", "quit"}}
}

func FullHelpResources() []KeyHelp {
	return []KeyHelp{
		{"↑/↓ k/j", "move selection"},
		{"pgup/pgdn", "page"},
		{"home/end", "first / last"},
		{"r", "reload list (retry denied or list-only)"},
		{"esc", "back"},
		{"b", "navigation tree"},
		{"m", "screens menu ← → enter"},
		{"2 3 4 6", "Dashboard / Workloads / Nodes / Events"},
		{"c n", "switch context / namespace"},
		{"A", "ack all Security alerts"},
		{"p", "cycle palette"},
		{"ctrl+p", "pause animations"},
		{"?", "toggle help"},
		{"q ctrl+c", "quit"},
	}
}

func OverviewHelp() []KeyHelp {
	return []KeyHelp{{"↑↓←→", "card"}, {"enter", "open list"}, {"r", "reload"}, {"esc", "back"}, {"b", "tree"}, {"?", "help"}, {"q", "quit"}}
}

func FullHelpOverview() []KeyHelp {
	return []KeyHelp{
		{"↑↓←→", "move between cards"},
		{"home/end", "first / last card"},
		{"enter", "open the list of the card"},
		{"r", "reload counts"},
		{"esc", "back"},
		{"b", "navigation tree"},
		{"m", "screens menu"},
		{"2 3 4 6", "Dashboard / Workloads / Nodes / Events"},
		{"c n", "switch context / namespace"},
		{"p", "cycle palette"},
		{"?", "toggle help"},
		{"q ctrl+c", "quit"},
	}
}

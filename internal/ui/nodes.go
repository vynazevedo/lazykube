package ui

import (
	"fmt"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

const (
	NodeSummaryTitle = "Node do pod selecionado"
	nodeUnknown      = "desconhecido"
	nodeUnavailable  = "indisponível"
)

type NodeSummaryState struct {
	Name     string
	Node     *data.Node
	CPU      int
	CPUKnown bool
	Observed int
	Visible  int
}

func (st NodeSummaryState) coverage() data.Coverage {
	switch {
	case st.Name == "":
		return data.Coverage{State: data.CoverageUnavailable, Reason: "nenhum pod com node selecionado"}
	case st.Node == nil:
		return data.Coverage{State: data.CoverageUnavailable, Reason: "Snapshot.Node é de outro node"}
	}
	return st.Node.Coverage
}

func (st NodeSummaryState) observed() bool {
	return st.Node != nil && st.Node.Coverage.State == data.CoverageComplete
}

func (st NodeSummaryState) ready() (string, theme.RGB) {
	switch {
	case !st.observed() || !st.Node.ReadyKnown:
		return nodeUnknown, theme.Muted
	case st.Node.Ready:
		return "True", theme.OK
	}
	return "False", theme.Err
}

func (st NodeSummaryState) cpu() string {
	if !st.observed() || !st.CPUKnown {
		return nodeUnavailable
	}
	return fmt.Sprintf("%d%%", st.CPU)
}

func (st NodeSummaryState) memory() string {
	if !st.observed() || st.Node.MemTotal == "" || st.Node.MemTotal == "n/a" {
		return nodeUnavailable
	}
	used := st.Node.MemUsed
	if used == "" || used == "n/a" {
		used = nodeUnavailable
	}
	return draw.Sanitize(used + " / " + st.Node.MemTotal)
}

func NodeSummaryLines(st NodeSummaryState) []draw.Line {
	name := draw.Line{colored("sem node", theme.Warn)}
	if st.Name != "" {
		name = draw.Line{bold(draw.Sanitize(st.Name), theme.Text)}
	}
	ready, readyColor := st.ready()
	cov := st.coverage()
	state := string(cov.State)
	if state == "" {
		state = nodeUnknown
	}
	lines := []draw.Line{
		name,
		{muted("Origem: "), text("Snapshot.Node")},
		{muted("Cobertura: "), text(state)},
	}
	if cov.Reason != "" {
		lines = append(lines, draw.Line{muted("  "), text(draw.Sanitize(cov.Reason))})
	}
	return append(lines, []draw.Line{
		{muted("Ready: "), colored(ready, readyColor)},
		{muted("CPU: "), text(st.cpu())},
		{muted("Memória: "), text(st.memory())},
		{muted("Pods observados: "), text(fmt.Sprintf("%d", st.Observed))},
		{muted("Pods na lista: "), text(fmt.Sprintf("%d", st.Visible))},
		{muted("Escopo dos pods: "), text("conectado")},
		nil,
		{faint("Lista derivada de Snapshot.Pods,")},
		{faint("pode ser parcial")},
		{faint("Inventário completo: fase futura")},
		{faint("Sem seleção independente de node")},
	}...)
}

func NodeSummary(r draw.Region, st NodeSummaryState, ctx Ctx) {
	inner := Panel(r, NodeSummaryTitle, false, ctx).Inset(1, 1, 1, 0)
	if inner.Empty() {
		return
	}
	for y, l := range NodeSummaryLines(st) {
		if y >= inner.H {
			break
		}
		inner.Line(0, y, l.Truncate(inner.W))
	}
}

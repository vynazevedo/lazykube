package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/draw"
)

const exitTextLimit = 128

type PendingOperation struct {
	Context     string
	OperationID string
}

func (m *Model) leave() tea.Cmd {
	m.left = m.PendingOperations()
	return tea.Quit
}

func (m Model) PendingOperations() []PendingOperation {
	out := slices.Clone(m.left)
	if p, ok := m.sending(); ok {
		out = append(out, p)
	}
	for _, d := range m.act.late {
		out = append(out, d.pending())
	}
	return out
}

func (m Model) sending() (PendingOperation, bool) {
	if m.act.stage != actionExecuting {
		return PendingOperation{}, false
	}
	return PendingOperation{Context: m.act.context, OperationID: m.act.opID}, true
}

func (d actionDone) pending() PendingOperation {
	return PendingOperation{Context: d.context, OperationID: d.result.OperationID}
}

func (r Root) PendingOperations() []PendingOperation {
	out := slices.Clone(r.unresolved)
	for _, d := range r.late {
		out = append(out, d.pending())
	}
	if m, ok := r.model(); ok {
		out = append(out, m.PendingOperations()...)
	}
	return out
}

func (r *Root) resolve(d actionDone) {
	r.unresolved = slices.DeleteFunc(slices.Clone(r.unresolved), func(p PendingOperation) bool { return p == d.pending() })
}

func Unfinished(final tea.Model) []PendingOperation {
	src, ok := final.(interface{ PendingOperations() []PendingOperation })
	if !ok {
		return nil
	}
	var out []PendingOperation
	for _, p := range src.PendingOperations() {
		safe := PendingOperation{Context: exitText(p.Context), OperationID: exitText(p.OperationID)}
		if !slices.Contains(out, safe) {
			out = append(out, safe)
		}
	}
	return out
}

func exitText(s string) string {
	s = draw.Truncate(draw.Sanitize(s), exitTextLimit)
	if s == "" {
		return "desconhecido"
	}
	return s
}

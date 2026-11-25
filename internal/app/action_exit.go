package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/guard"
)

const exitTextLimit = 128

type operation struct {
	context string
	id      string
	exec    *guard.Executor
}

type PendingOperation struct {
	Context     string
	OperationID string
	Outcome     string
}

func (m *Model) leave() tea.Cmd {
	m.left = m.operations()
	return tea.Quit
}

func (m Model) operations() []operation {
	out := slices.Clone(m.left)
	if op, ok := m.sending(); ok {
		out = append(out, op)
	}
	for _, d := range m.act.late {
		out = append(out, d.operation())
	}
	return out
}

func (m Model) sending() (operation, bool) {
	if m.act.stage != actionExecuting {
		return operation{}, false
	}
	return operation{context: m.act.context, id: m.act.opID, exec: m.executor()}, true
}

func (d actionDone) operation() operation {
	return operation{context: d.context, id: d.result.OperationID, exec: d.exec}
}

func (r Root) operations() []operation {
	out := slices.Clone(r.unresolved)
	for _, d := range r.late {
		out = append(out, d.operation())
	}
	if m, ok := r.model(); ok {
		out = append(out, m.operations()...)
	}
	return out
}

func (r *Root) resolve(d actionDone) {
	r.unresolved = slices.DeleteFunc(slices.Clone(r.unresolved), func(op operation) bool { return op == d.operation() })
}

func (m Model) PendingOperations() []PendingOperation { return pendingOperations(m.operations()) }
func (r Root) PendingOperations() []PendingOperation  { return pendingOperations(r.operations()) }

func pendingOperations(ops []operation) []PendingOperation {
	var out []PendingOperation
	for _, op := range ops {
		if op.exec == nil {
			continue
		}
		d := op.exec.Dispatch(op.id)
		if !d.Sent {
			continue
		}
		outcome := ""
		if d.Recorded {
			outcome = string(d.Outcome)
		}
		out = append(out, PendingOperation{Context: op.context, OperationID: op.id, Outcome: outcome})
	}
	return out
}

func Unfinished(final tea.Model) []PendingOperation {
	src, ok := final.(interface{ PendingOperations() []PendingOperation })
	if !ok {
		return nil
	}
	var out []PendingOperation
	for _, p := range src.PendingOperations() {
		safe := PendingOperation{Context: exitText(p.Context), OperationID: exitText(p.OperationID)}
		if p.Outcome != "" {
			safe.Outcome = exitText(p.Outcome)
		}
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

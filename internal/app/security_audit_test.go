//go:build linux || darwin

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type auditSource struct {
	*data.Demo
	path string
}

func (s auditSource) Snapshot() data.Snapshot {
	snap := s.Demo.Snapshot()
	snap.Synthetic = false
	snap.Cluster.Context = "dev"
	return snap
}

func (s auditSource) AuditPath() string {
	return s.path
}

func auditFixture(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit", "audit.jsonl")
	a := guard.NewFileAudit(path)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for i := range n {
		ctx := "dev"
		if i%3 == 2 {
			ctx = "prod"
		}
		e := guard.AuditEvent{SchemaVersion: 1, Timestamp: at.Add(time.Duration(i) * time.Minute), OperationID: fmt.Sprintf("op-%03d", i), Phase: "result", Action: guard.Scale, Context: ctx, Identity: "session-1", Namespace: "apps", Group: "apps", Resource: "deployments", Name: "api", UID: "u-1", ReadOnly: true, Outcome: guard.ReadOnly}
		if i == n-1 {
			e.Outcome = guard.Unknown
		}
		if err := a.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range batch {
				out = append(out, runCmd(c)...)
			}
			return out
		}
		return []tea.Msg{msg}
	case <-time.After(500 * time.Millisecond):
		return nil
	}
}

func settle(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for range 50 {
		var pending []tea.Msg
		for _, msg := range runCmd(cmd) {
			if _, ok := msg.(auditPageMsg); ok {
				pending = append(pending, msg)
			}
		}
		if len(pending) == 0 {
			return m
		}
		var cmds []tea.Cmd
		for _, msg := range pending {
			next, c := m.Update(msg)
			m = next.(Model)
			cmds = append(cmds, c)
		}
		cmd = tea.Batch(cmds...)
	}
	t.Fatal("audit paging did not settle")
	return m
}

func step(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		next, cmd := m.Update(keyPress(k))
		m = settleSecurity(settle(t, next.(Model), cmd))
	}
	return m
}

func auditModel(t *testing.T, path string, w, h int) Model {
	t.Helper()
	m := nodeModel(t, auditSource{Demo: data.NewDemo(), path: path}, w, h)
	return step(t, m, "5", "[")
}

func TestSecurityAuditTabReadsLocalAudit(t *testing.T) {
	m := auditModel(t, auditFixture(t, 9), 160, 48)
	if m.sec.Tab != ui.SecTabAudit || m.sec.audit.state != ui.AuditReady || len(m.sec.audit.events) != 9 {
		t.Fatalf("audit tab loads the local audit: %+v", m.sec.audit)
	}
	rows := m.auditRows()
	if len(rows) != 6 || rows[0].OperationID != "op-007" || m.auditState().Hidden != 3 {
		t.Fatalf("only the current context by default, newest first: %d %+v", len(rows), rows[0])
	}
	plain := m.Render().Plain()
	for _, want := range []string{"Audit local (6)", "3 de outros contextos ocultos", "[Mostrar outros contextos]", "Sem payload, comando ou credenciais.", "session-1", "apps/deployments.apps/api uid u-1", "read_only", "não é a identidade autenticada no Kubernetes"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("audit tab missing %q", want)
		}
	}
	all := step(t, m, "tab", "enter")
	if !all.sec.audit.all || len(all.auditRows()) != 9 || all.auditRows()[0].Outcome != "outcome_unknown" {
		t.Fatal("explicit filter shows other contexts with outcome preserved")
	}
	if !strings.Contains(all.Render().Plain(), "sem reuso para ação") {
		t.Fatal("other contexts are labelled as not actionable")
	}
	down := step(t, m, "down", "down")
	if down.sec.audit.cursor != 2 || !strings.Contains(down.Render().Plain(), "op-004") {
		t.Fatal("arrows move the audit selection and the detail follows")
	}
	if away := step(t, m, "]"); away.sec.audit.state != "" || away.sec.audit.events != nil {
		t.Fatal("leaving the tab drops the loaded audit")
	}
	if back := step(t, m, "esc"); back.sec.audit.state != "" {
		t.Fatal("leaving the page cancels the audit view")
	}
}

func TestSecurityAuditPagesAndDropsStale(t *testing.T) {
	m := auditModel(t, auditFixture(t, 450), 160, 48)
	if len(m.sec.audit.events) != guard.AuditPageSize || !m.sec.audit.more {
		t.Fatalf("first page only: %d", len(m.sec.audit.events))
	}
	end := step(t, m, "end")
	if len(end.sec.audit.events) <= guard.AuditPageSize {
		t.Fatal("reaching the end loads the next page")
	}
	next, cmd := m.Update(keyPress("end"))
	moved := next.(Model)
	msgs := runCmd(cmd)
	stale := step(t, moved, "]", "[")
	for _, msg := range msgs {
		if _, ok := msg.(auditPageMsg); ok {
			got, _ := stale.Update(msg)
			if len(got.(Model).sec.audit.events) != len(stale.sec.audit.events) {
				t.Fatal("a page requested before reopening the tab is discarded")
			}
		}
	}
	other := msgs
	for _, msg := range other {
		if p, ok := msg.(auditPageMsg); ok {
			p.session++
			got, _ := moved.Update(p)
			if len(got.(Model).sec.audit.events) != len(moved.sec.audit.events) {
				t.Fatal("a page from another session is discarded")
			}
		}
	}
}

func TestSecurityAuditStates(t *testing.T) {
	demo := step(t, demoModel(t, 160, 48), "5", "[")
	if demo.sec.audit.state != ui.AuditUnavailable || !strings.Contains(demo.Render().Plain(), "Audit local indisponível; nenhum registro foi carregado.") {
		t.Fatal("demo never reads the real audit file")
	}
	missing := auditModel(t, filepath.Join(t.TempDir(), "none", "audit.jsonl"), 160, 48)
	if missing.sec.audit.state != ui.AuditMissing || !strings.Contains(missing.Render().Plain(), "Nenhum registro de audit local encontrado.") {
		t.Fatal("absent audit is distinct from a failure")
	}
	path := auditFixture(t, 2)
	if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if bad := auditModel(t, path, 160, 48); bad.sec.audit.state != ui.AuditUnavailable || len(bad.sec.audit.events) != 0 {
		t.Fatal("non private audit directory is not read")
	}
}

func TestSecurityAuditGoldens(t *testing.T) {
	path := auditFixture(t, 9)
	for _, size := range [][2]int{{160, 48}, {130, 40}} {
		m := auditModel(t, path, size[0], size[1])
		golden(t, fmt.Sprintf("security_audit_%dx%d.txt", size[0], size[1]), m.Render().Plain())
	}
}

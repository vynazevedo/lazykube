//go:build linux

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

const (
	auditSelf  = "session-1-0123456789abcdef0123456789abcdef"
	auditOther = "session-1-fedcba9876543210fedcba9876543210"
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
		e := guard.AuditEvent{SchemaVersion: 1, Timestamp: at.Add(time.Duration(i) * time.Minute), OperationID: fmt.Sprintf("op-%03d", i), Phase: "result", Action: guard.Scale, Context: ctx, Identity: auditSelf, Namespace: "apps", Group: "apps", Resource: "deployments", Name: "api", UID: "u-1", ReadOnly: true, Outcome: guard.ReadOnly}
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
	m.session.Identity = auditSelf
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
	for _, want := range []string{"Audit local (6)", "Ocultos de outras sessões: 3", "[Mostrar outras sessões]", "Sem payload, comando ou credenciais.", auditSelf, "apps/deployments.apps/api uid u-1", "read_only", "não é a identidade autenticada no Kubernetes"} {
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
	for _, size := range [][2]int{{160, 48}, {130, 40}, {80, 24}} {
		m := auditModel(t, path, size[0], size[1])
		golden(t, fmt.Sprintf("security_audit_%dx%d.txt", size[0], size[1]), m.Render().Plain())
		golden(t, fmt.Sprintf("security_audit_all_%dx%d.txt", size[0], size[1]), step(t, m, "tab", "enter").Render().Plain())
	}
}

func TestSecurityAuditCompactPanels(t *testing.T) {
	m := auditModel(t, auditFixture(t, 9), 80, 24)
	if m.layout.SecList.Empty() || !m.layout.SecEvidence.Empty() {
		t.Fatal("the compact audit opens on the list")
	}
	detail := step(t, m, "tab")
	if detail.layout.SecEvidence.Empty() || !strings.Contains(detail.Render().Plain(), "[Mostrar outras sessões]") {
		t.Fatal("tab shows the record and the session filter")
	}
	l := detail.layout.SecEvidence
	if all := deliver(t, detail, tea.MouseClickMsg{X: l.X + 3, Y: l.Y + l.H - 2, Button: tea.MouseLeft}); !all.sec.audit.all {
		t.Fatal("clicking the filter in the record widens it")
	}
	if all := step(t, detail, "enter"); !all.sec.audit.all {
		t.Fatal("enter on the filter widens it")
	}
}

func auditFile(t *testing.T, events ...guard.AuditEvent) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit", "audit.jsonl")
	a := guard.NewFileAudit(path)
	for i, e := range events {
		e.SchemaVersion, e.Phase, e.Action, e.Resource, e.Name = 1, "result", guard.Scale, "deployments", "api"
		e.OperationID = fmt.Sprintf("op-%d", i)
		if err := a.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestSecurityAuditIsolatesHomonymousContexts(t *testing.T) {
	at := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	path := auditFile(t,
		guard.AuditEvent{Timestamp: at.Add(-time.Hour), Context: "dev", Identity: auditSelf, Outcome: guard.Succeeded},
		guard.AuditEvent{Timestamp: at.Add(time.Minute), Context: "dev", Identity: auditOther, Outcome: guard.Succeeded},
		guard.AuditEvent{Timestamp: at.Add(time.Minute), Context: "prod", Identity: auditSelf, Outcome: guard.Succeeded},
		guard.AuditEvent{Timestamp: at.Add(2 * time.Minute), Context: "dev", Identity: auditSelf, Outcome: guard.Unknown},
	)
	open := func(identity string) Model {
		m := nodeModel(t, auditSource{Demo: data.NewDemo(), path: path}, 160, 48)
		m.session.Identity, m.started = identity, at
		return step(t, m, "5", "[")
	}
	m := open(auditSelf)
	rows := m.auditRows()
	if len(rows) != 1 || rows[0].OperationID != "op-3" || rows[0].Outcome != "outcome_unknown" || m.auditState().Hidden != 3 {
		t.Fatalf("the default filter isolates context, identity and session start: %+v", rows)
	}
	if other := open(auditOther); len(other.auditRows()) != 1 || other.auditRows()[0].OperationID != "op-1" {
		t.Fatal("the same context name under another identity is another session")
	}
	for _, ambiguous := range []string{"", "session-1", "session-1-", auditSelf + "0"} {
		if none := open(ambiguous); len(none.auditRows()) != 0 || none.auditState().Hidden != 4 {
			t.Fatalf("a session without a unique identity (%q) shows nothing by default", ambiguous)
		}
	}
	all := step(t, m, "tab", "enter")
	if len(all.auditRows()) != 4 {
		t.Fatal("other sessions only appear through the explicit filter")
	}
	plain := all.Render().Plain()
	for _, want := range []string{"CONTEXTO", "prod        12:01:00Z", "dev         12:02:00Z", "Todas as sessões; sem reuso para ação", "[Somente esta sessão]"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("every row names its context, missing %q", want)
		}
	}
	nav := all.nav
	for _, k := range []string{"down", "up", "shift+tab", "enter"} {
		all = step(t, all, k)
	}
	if all.nav != nav {
		t.Fatal("audit rows never become a route or an action target")
	}
}

func TestSecurityAuditSessionSwitchAndOrder(t *testing.T) {
	path := auditFixture(t, 450)
	m := step(t, auditModel(t, path, 160, 48), "tab", "enter")
	if !m.sec.audit.all {
		t.Fatal("filter widened")
	}
	next := New(Options{Source: auditSource{Demo: data.NewDemo(), path: path}, NoAnim: true, Session: data.SessionRef{Identity: auditOther, Generation: m.session.Generation + 1}})
	if next.sec.audit.all || next.sec.audit.events != nil {
		t.Fatal("a new session starts without the widened filter or rows")
	}
	if got := step(t, m, "]", "["); got.sec.audit.all {
		t.Fatal("reopening the tab restores the session filter")
	}

	page := func(op string) guard.AuditPage {
		return guard.AuditPage{Events: []guard.AuditEvent{{OperationID: op, Context: "dev", Identity: auditSelf}}}
	}
	waiting := m
	waiting.sec.audit.loading = true
	before := len(waiting.sec.audit.events)
	for _, msg := range []auditPageMsg{
		{session: m.session.Generation, seq: m.sec.audit.seq - 1, page: page("older")},
		{session: m.session.Generation, seq: m.sec.audit.seq + 1, page: page("newer")},
		{session: m.session.Generation + 1, seq: m.sec.audit.seq, page: page("other")},
	} {
		if got := deliver(t, waiting, msg); len(got.sec.audit.events) != before {
			t.Fatalf("out of order page applied: %+v", msg)
		}
	}

	failed := waiting
	if failed = deliver(t, failed, auditPageMsg{session: m.session.Generation, seq: m.sec.audit.seq, err: guard.ErrAuditUnreadable}); failed.sec.audit.state != ui.AuditUnavailable || len(failed.sec.audit.events) != 0 || len(failed.auditRows()) != 0 {
		t.Fatal("an unavailable audit keeps no residual rows")
	}
}

func TestSecurityAuditReaderEndsWithTabAndSession(t *testing.T) {
	path := auditFixture(t, 450)
	for name, leave := range map[string]func(Model) Model{
		"tab":    func(m Model) Model { return step(t, m, "]") },
		"screen": func(m Model) Model { return step(t, m, "2") },
		"back":   func(m Model) Model { return step(t, m, "esc") },
	} {
		m := auditModel(t, path, 160, 48)
		next, cmd := m.Update(keyPress("end"))
		m = next.(Model)
		if cmd == nil || !m.sec.audit.loading || m.sec.audit.cancel == nil {
			t.Fatal("the next page is requested")
		}
		left := leave(m)
		if left.sec.audit.cancel != nil || left.sec.audit.loading {
			t.Fatalf("leaving by %s keeps the reader", name)
		}
		for _, msg := range runCmd(cmd) {
			if p, ok := msg.(auditPageMsg); ok {
				if p.err == nil {
					t.Fatalf("leaving by %s must cancel the read", name)
				}
				if got := deliver(t, left, p); got.sec.audit.events != nil {
					t.Fatal("a canceled page is discarded")
				}
			}
		}
	}

	sess, life := newSession()
	m := auditModel(t, path, 160, 48)
	m.life = life
	next, cmd := m.Update(keyPress("end"))
	sess.cancel()
	m = next.(Model)
	for _, msg := range runCmd(cmd) {
		if p, ok := msg.(auditPageMsg); ok && p.err == nil {
			t.Fatal("ending the session cancels the read")
		}
	}
}

func TestSecurityAuditSeparatesSimultaneousProcesses(t *testing.T) {
	at := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	first, err := guard.NewSessionIdentity(1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := guard.NewSessionIdentity(1)
	if err != nil || first == second {
		t.Fatalf("two processes share %q: %v", first, err)
	}
	path := auditFile(t,
		guard.AuditEvent{Timestamp: at.Add(time.Minute), Context: "dev", Identity: first, Outcome: guard.Succeeded},
		guard.AuditEvent{Timestamp: at.Add(time.Minute), Context: "dev", Identity: second, Outcome: guard.Succeeded},
		guard.AuditEvent{Timestamp: at.Add(time.Minute), Context: "dev", Identity: "session-1", Outcome: guard.Succeeded},
	)
	for op, identity := range map[string]string{"op-0": first, "op-1": second, "": "session-1"} {
		m := nodeModel(t, auditSource{Demo: data.NewDemo(), path: path}, 160, 48)
		m.session.Identity, m.started = identity, at
		m = step(t, m, "5", "[")
		rows := m.auditRows()
		if op == "" && len(rows) != 0 {
			t.Fatalf("a legacy identity is ambiguous: %+v", rows)
		}
		if op != "" && (len(rows) != 1 || rows[0].OperationID != op) {
			t.Fatalf("%s sees only its own process: %+v", identity, rows)
		}
	}
}

func TestSecurityAuditShowsWhyThePathIsRefused(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "real", "audit"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(base, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{160, 48}, {130, 40}, {80, 24}} {
		m := auditModel(t, filepath.Join(base, "alias", "audit", "audit.jsonl"), size[0], size[1])
		if m.sec.audit.state != ui.AuditUnavailable || len(m.auditRows()) != 0 {
			t.Fatalf("a refused path loads nothing: %+v", m.sec.audit.state)
		}
		if want := string(guard.RefusedLink) + "; ações bloqueadas"; m.sec.audit.reason != want {
			t.Fatalf("reason %q, want %q", m.sec.audit.reason, want)
		}
		plain := m.Render().Plain()
		l := m.layout.SecList
		var list strings.Builder
		for _, line := range strings.Split(plain, "\n")[l.Y+1 : l.Y+l.H-1] {
			list.WriteString(strings.TrimSpace(strings.Trim(string([]rune(line)[l.X:l.X+l.W]), "│ ")) + " ")
		}
		if text := strings.Join(strings.Fields(list.String()), " "); !strings.Contains(text, "link não confiável; ações bloqueadas") {
			t.Fatalf("%v the list says why: %q", size, text)
		}
		if strings.Contains(plain, base) {
			t.Fatal("the reason never shows the raw path")
		}
	}
}

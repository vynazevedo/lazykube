package app

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const auditRetained = 2000

type auditLocated interface {
	AuditPath() string
}

type auditView struct {
	seq     uint64
	state   string
	reason  string
	events  []guard.AuditEvent
	next    guard.AuditCursor
	more    bool
	pending bool
	invalid int
	loading bool
	capped  bool
	cursor  int
	all     bool
	cancel  context.CancelFunc
}

func (a *auditView) stop() {
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
}

type auditPageMsg struct {
	session uint64
	seq     uint64
	page    guard.AuditPage
	err     error
}

func (m Model) auditPath() (string, string) {
	if m.snap.Synthetic {
		return "", "fonte demo não lê o audit local"
	}
	src, ok := m.src.(auditLocated)
	if !ok || src.AuditPath() == "" {
		return "", "caminho do audit local não configurado"
	}
	return src.AuditPath(), ""
}

func (m Model) auditWanted() bool {
	return m.onScreen(ScreenSecurity) && m.sec.Tab == ui.SecTabAudit
}

func (m *Model) followAudit() tea.Cmd {
	a := &m.sec.audit
	if !m.auditWanted() {
		if a.state != "" || a.loading {
			a.stop()
			m.sec.audit = auditView{}
		}
		return nil
	}
	switch {
	case a.state == "":
		return m.startAudit()
	case a.state == ui.AuditReady && a.more && !a.loading && !a.capped && a.cursor >= m.auditVisible()-1:
		return m.requestAudit()
	}
	return nil
}

func (m *Model) startAudit() tea.Cmd {
	all := m.sec.audit.all
	m.sec.audit.stop()
	m.sec.audit = auditView{all: all, state: ui.AuditLoading}
	path, reason := m.auditPath()
	if path == "" {
		m.sec.audit.state, m.sec.audit.reason = ui.AuditUnavailable, reason
		return nil
	}
	return m.requestAudit()
}

func (m *Model) requestAudit() tea.Cmd {
	path, _ := m.auditPath()
	if path == "" {
		return nil
	}
	*m.auditSeq++
	a := &m.sec.audit
	a.seq, a.loading = *m.auditSeq, true
	a.stop()
	ctx, cancel := context.WithCancel(m.life)
	a.cancel = cancel
	session, seq, cursor := m.session.Generation, a.seq, a.next
	return func() tea.Msg {
		page, err := guard.ReadAudit(ctx, path, cursor, guard.AuditPageSize)
		return auditPageMsg{session: session, seq: seq, page: page, err: err}
	}
}

func (m Model) updateAudit(msg tea.Msg) (Model, tea.Cmd, bool) {
	page, ok := msg.(auditPageMsg)
	if !ok {
		return m, nil, false
	}
	m.applyAudit(page)
	return m, nil, true
}

func (m *Model) applyAudit(msg auditPageMsg) {
	a := &m.sec.audit
	if msg.session != m.session.Generation || msg.seq != a.seq || !a.loading || !m.auditWanted() || m.life.Err() != nil {
		return
	}
	a.loading = false
	a.stop()
	switch {
	case msg.err != nil:
		*a = auditView{all: a.all, state: ui.AuditUnavailable}
		var refusal guard.AuditRefusal
		if errors.As(msg.err, &refusal) {
			a.reason = refusal.Error() + "; ações bloqueadas"
		}
		return
	case msg.page.Rotated:
		*a = auditView{all: a.all, state: ui.AuditRotated}
		return
	case msg.page.Missing:
		a.state = ui.AuditMissing
		return
	}
	first := !a.next.Started()
	a.events = append(a.events, msg.page.Events...)
	a.next, a.more = msg.page.Next, msg.page.More
	a.invalid += msg.page.Invalid
	if first {
		a.pending = msg.page.Pending
	}
	if len(a.events) >= auditRetained {
		a.events, a.capped = a.events[:auditRetained], true
	}
	a.state = ui.AuditReady
	if len(a.events) == 0 && !a.more {
		a.state = ui.AuditMissing
	}
}

func (m Model) auditContext() string {
	if m.session.Context != "" {
		return m.session.Context
	}
	return m.snap.Cluster.Context
}

func (m Model) auditOwn(e guard.AuditEvent) bool {
	s := m.session
	return guard.UniqueSessionIdentity(s.Identity) && e.Identity == s.Identity && e.Context == m.auditContext() && !e.Timestamp.Before(m.started)
}

func auditAction(a guard.Action) string {
	switch a {
	case guard.Delete:
		return "delete"
	case guard.Scale:
		return "scale"
	case guard.Restart:
		return "restart"
	case guard.Edit:
		return "edit"
	case guard.Exec:
		return "exec"
	case guard.PortForward:
		return "port-forward"
	case guard.Cordon:
		return "cordon"
	case guard.Uncordon:
		return "uncordon"
	}
	return "desconhecida"
}

func auditTarget(e guard.AuditEvent) string {
	resource := e.Resource
	if e.Group != "" {
		resource += "." + e.Group
	}
	target := strings.Trim(e.Namespace+"/"+resource+"/"+e.Name, "/")
	if e.UID != "" {
		target += " uid " + e.UID
	}
	return target
}

func (m Model) auditVisible() int {
	if m.sec.audit.all {
		return len(m.sec.audit.events)
	}
	n := 0
	for _, e := range m.sec.audit.events {
		if m.auditOwn(e) {
			n++
		}
	}
	return n
}

func (m Model) auditRows() []ui.SecurityAuditRow {
	a := m.sec.audit
	rows := make([]ui.SecurityAuditRow, 0, len(a.events))
	for _, e := range a.events {
		if !a.all && !m.auditOwn(e) {
			continue
		}
		rows = append(rows, ui.SecurityAuditRow{
			Time:        e.Timestamp.UTC().Format("15:04:05Z"),
			When:        e.Timestamp.UTC().Format("2006-01-02 15:04:05Z"),
			Context:     e.Context,
			Identity:    e.Identity,
			Action:      auditAction(e.Action),
			Phase:       e.Phase,
			Outcome:     string(e.Outcome),
			OperationID: e.OperationID,
			Target:      auditTarget(e),
			ReadOnly:    e.ReadOnly,
			Protected:   e.Protected,
		})
	}
	return rows
}

func (m Model) auditState() ui.SecurityAuditState {
	a := m.sec.audit
	rows := m.auditRows()
	st := ui.SecurityAuditState{State: a.state, Reason: a.reason, Context: m.auditContext(), Rows: rows, Cursor: a.cursor, More: a.more || a.loading, Pending: a.pending, Invalid: a.invalid, Hidden: len(a.events) - len(rows), AllSessions: a.all}
	if st.State == "" {
		st.State = ui.AuditLoading
	}
	if a.capped {
		st.More = false
	}
	return st
}

func (m *Model) moveAudit(delta int) {
	n := len(m.auditRows())
	m.sec.audit.cursor = max(0, min(n-1, m.sec.audit.cursor+delta))
}

func (m *Model) toggleAuditSessions() {
	m.sec.audit.all = !m.sec.audit.all
	m.sec.audit.cursor = 0
}

func (m *Model) handleAuditClick(ms tea.Mouse) {
	l, detail := m.layout.SecList, m.layout.SecEvidence
	if m.layout.Compact && detail.Contains(ms.X, ms.Y) {
		if ms.Y == detail.Y+detail.H-2 {
			m.toggleAuditSessions()
		}
		return
	}
	if !l.Contains(ms.X, ms.Y) {
		return
	}
	if ms.Y == l.Y+ui.SecurityAuditButtonRow(l.H) {
		m.sec.Focus = ui.SecurityFocusEvidence
		m.toggleAuditSessions()
		return
	}
	m.sec.Focus = ui.SecurityFocusList
	rows := ui.SecurityAuditRows(l.H)
	row := ms.Y - (l.Y + 2)
	n := len(m.auditRows())
	if row < 0 || row >= rows {
		return
	}
	if idx := ui.ScrollOffset(m.sec.audit.cursor, n, rows) + row; idx < n {
		m.sec.audit.cursor = idx
	}
}

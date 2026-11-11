package app

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/ui"
)

const (
	noticeTTL      = 2 * time.Second
	clipboardLimit = 100000
)

type yank struct {
	label string
	text  string
}

type notice struct {
	text string
	seq  int
}

type noticeExpiredMsg struct {
	seq     int
	session uint64
}

func (m Model) yankTarget() (yank, bool) {
	switch {
	case m.inDetail():
		return m.yankDetails()
	case m.onScreen(ScreenEvents):
		return m.yankEvent()
	case m.onScreen(ScreenResources):
		return m.yankTableRow()
	case m.onScreen(ScreenOverview):
		return yank{}, false
	}
	switch m.focus {
	case FocusNamespaces:
		if m.nsCursor >= 0 && m.nsCursor < len(m.snap.Namespaces) {
			return named(m.snap.Namespaces[m.nsCursor].Name)
		}
		return yank{}, false
	case FocusDeployments:
		deps := m.visibleDeployments()
		if m.depCursor >= 0 && m.depCursor < len(deps) {
			return named(deploymentKey(deps[m.depCursor]))
		}
		return yank{}, false
	case FocusDetails:
		return m.yankDetails()
	}
	if p := m.selectedPod(); p != nil {
		return named(p.Key())
	}
	return yank{}, false
}

func named(s string) (yank, bool) {
	if s == "" {
		return yank{}, false
	}
	return yank{label: s, text: s}, true
}

func (m Model) yankDetails() (yank, bool) {
	if m.tab == ui.TabLog {
		return m.yankLogLine()
	}
	if len(m.manifest) == 0 {
		return yank{}, false
	}
	text := strings.Join(m.manifest, "\n") + "\n"
	return yank{label: fmt.Sprintf("YAML de %s (%d linhas)", m.detailTarget(), len(m.manifest)), text: text}, true
}

func (m Model) yankLogLine() (yank, bool) {
	rows := m.logRows()
	if len(rows) == 0 {
		return yank{}, false
	}
	idx := min(m.scroll+ui.ScrollRows(ui.TabLog, m.layout.Details.H)-1, len(rows)-1)
	if len(m.dg.matches) > 0 {
		idx = m.dg.matches[m.dg.matchIdx]
	}
	idx = max(0, min(idx, len(rows)-1))
	line := rows[idx].Line.Message
	if line == "" {
		return yank{}, false
	}
	return yank{label: "linha do log: " + line, text: line}, true
}

func (m Model) yankEvent() (yank, bool) {
	e := m.eventsState().Selected()
	if e == nil {
		return yank{}, false
	}
	if m.ev.reading {
		msg := e.Message
		if d := m.eventDetail(); d != nil && d.Text != "" {
			msg = d.Text
		}
		if msg == "" {
			return yank{}, false
		}
		return yank{label: "mensagem do evento " + e.Reason, text: msg}, true
	}
	if e.Namespace == "" {
		return named(e.Object)
	}
	return named(e.Namespace + "/" + e.Object)
}

func (m Model) yankTableRow() (yank, bool) {
	st := m.tableState()
	if st.Cursor < 0 || st.Cursor >= len(st.Page.Items) {
		return yank{}, false
	}
	ref := st.Page.Items[st.Cursor].Ref
	if ref.Namespace == "" {
		return named(ref.Name)
	}
	return named(ref.Namespace + "/" + ref.Name)
}

func (m Model) yank() (Model, tea.Cmd) {
	y, ok := m.yankTarget()
	if !ok {
		return m, m.showNotice("nada para copiar")
	}
	if size := base64.StdEncoding.EncodedLen(len(y.text)); size > clipboardLimit {
		return m, m.showNotice(fmt.Sprintf("grande demais para copiar (%d KiB, limite %d KiB)", (size+1023)/1024, clipboardLimit/1024))
	}
	return m, tea.Batch(tea.SetClipboard(y.text), m.showNotice("enviado ao clipboard: "+y.label))
}

func (m *Model) showNotice(text string) tea.Cmd {
	m.notice.seq++
	m.notice.text = text
	seq, session := m.notice.seq, m.session.Generation
	after := m.after
	if after == nil {
		after = tea.Tick
	}
	return after(noticeTTL, func(time.Time) tea.Msg { return noticeExpiredMsg{seq: seq, session: session} })
}

func (m *Model) expireNotice(msg noticeExpiredMsg) {
	if msg.session == m.session.Generation && msg.seq == m.notice.seq {
		m.notice.text = ""
	}
}

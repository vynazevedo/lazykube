package app

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/ui"
)

func instantTimer(_ time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	return func() tea.Msg { return fn(time.Unix(0, 0)) }
}

func yankModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := demoModel(t, w, h)
	m.after = instantTimer
	return m
}

func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drain(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func pressYank(t *testing.T, m Model) (Model, []tea.Msg) {
	t.Helper()
	next, cmd := m.Update(keyPress("y"))
	return next.(Model), drain(cmd)
}

var clipboardType = reflect.TypeOf(tea.SetClipboard("")())

func clipboardOf(msgs []tea.Msg) (string, bool) {
	for _, msg := range msgs {
		if v := reflect.ValueOf(msg); v.IsValid() && v.Type() == clipboardType {
			return v.String(), true
		}
	}
	return "", false
}

func TestYankCopiesTheListSelection(t *testing.T) {
	m := yankModel(t, 160, 48)
	pod := m.selectedPod()
	if pod == nil {
		t.Fatal("demo starts with a selected pod")
	}
	got, msgs := pressYank(t, m)
	text, ok := clipboardOf(msgs)
	if !ok || text != pod.Key() {
		t.Fatalf("clipboard = %q (%v), want %q", text, ok, pod.Key())
	}
	if got.notice.text != "enviado ao clipboard: "+pod.Key() {
		t.Fatalf("notice = %q", got.notice.text)
	}

	ns := yankModel(t, 160, 48)
	ns.focus = FocusNamespaces
	ns.nsCursor = 1
	_, msgs = pressYank(t, ns)
	if text, _ := clipboardOf(msgs); text != ns.snap.Namespaces[1].Name {
		t.Fatalf("namespace yank = %q, want %q", text, ns.snap.Namespaces[1].Name)
	}

	wl := press(t, yankModel(t, 160, 48), "3")
	wl.focus = FocusDeployments
	deps := wl.visibleDeployments()
	if len(deps) == 0 {
		t.Fatal("demo has deployments")
	}
	_, msgs = pressYank(t, wl)
	if text, _ := clipboardOf(msgs); text != deps[0].Namespace+"/"+deps[0].Name {
		t.Fatalf("deployment yank = %q", text)
	}
}

func TestYankCopiesTheWholeManifest(t *testing.T) {
	for _, tc := range []struct {
		name string
		prep func(*testing.T, Model) Model
	}{
		{"details panel", func(t *testing.T, m Model) Model {
			m.focus = FocusDetails
			return press(t, m, "]")
		}},
		{"pod detail", func(t *testing.T, m Model) Model {
			return press(t, m, "enter", "d")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.prep(t, yankModel(t, 160, 48))
			if m.tab == ui.TabLog || len(m.manifest) == 0 {
				t.Fatalf("tab=%v manifest=%d", m.tab, len(m.manifest))
			}
			got, msgs := pressYank(t, m)
			text, ok := clipboardOf(msgs)
			want := strings.Join(m.manifest, "\n") + "\n"
			if !ok || text != want {
				t.Fatalf("clipboard must hold the whole manifest, got %d bytes", len(text))
			}
			if !strings.HasPrefix(got.notice.text, "enviado ao clipboard: YAML de ") {
				t.Fatalf("notice = %q", got.notice.text)
			}
		})
	}
}

func TestYankCopiesTheLogLine(t *testing.T) {
	m := press(t, detailsModel(t, 1), "l")
	m.after = instantTimer
	rows := m.logRows()
	if len(rows) == 0 {
		t.Fatal("demo pod has logs")
	}
	_, msgs := pressYank(t, m)
	bottom := min(m.scroll+ui.ScrollRows(ui.TabLog, m.layout.Details.H)-1, len(rows)-1)
	if text, _ := clipboardOf(msgs); text != rows[bottom].Line.Message {
		t.Fatalf("without a match the last visible line is copied, got %q", text)
	}

	m = press(t, m, "/", "h", "e", "a", "r", "t", "enter", "alt+n")
	if len(m.dg.matches) == 0 {
		t.Fatal("demo log has heartbeat lines")
	}
	match := rows[m.dg.matches[m.dg.matchIdx]].Line.Message
	got, msgs := pressYank(t, m)
	if text, _ := clipboardOf(msgs); text != match {
		t.Fatalf("with a search the current match is copied, got %q want %q", text, match)
	}
	if got.notice.text != "enviado ao clipboard: linha do log: "+match {
		t.Fatalf("notice = %q", got.notice.text)
	}
}

func TestYankOnEventsScreen(t *testing.T) {
	m := press(t, yankModel(t, 160, 48), "6")
	e := m.eventsState().Selected()
	if e == nil {
		t.Fatal("demo events screen selects an event")
	}
	want := e.Object
	if e.Namespace != "" {
		want = e.Namespace + "/" + e.Object
	}
	_, msgs := pressYank(t, m)
	if text, _ := clipboardOf(msgs); text != want {
		t.Fatalf("event yank = %q, want %q", text, want)
	}
	reading := press(t, m, "enter")
	if !reading.ev.reading {
		t.Fatal("enter opens the message")
	}
	_, msgs = pressYank(t, reading)
	if text, _ := clipboardOf(msgs); text != e.Message {
		t.Fatalf("reading yank = %q, want the message", text)
	}
}

func TestYankWithoutTargetOnlyWarns(t *testing.T) {
	m := yankModel(t, 160, 48)
	m.activateDestination(DestWorkloadsOver)
	got, msgs := pressYank(t, m)
	if _, ok := clipboardOf(msgs); ok {
		t.Fatal("overview has nothing to copy")
	}
	if got.notice.text != "nada para copiar" {
		t.Fatalf("notice = %q", got.notice.text)
	}
}

func manifestModel(t *testing.T, payload int) Model {
	t.Helper()
	m := yankModel(t, 160, 48)
	m.focus = FocusDetails
	m.tab = ui.TabConfig
	m.manifest = []string{strings.Repeat("x", payload-1)}
	return m
}

func TestYankAcceptsPayloadAtTheLimit(t *testing.T) {
	raw := clipboardLimit / 4 * 3
	if base64.StdEncoding.EncodedLen(raw) != clipboardLimit {
		t.Fatalf("fixture must encode to exactly the limit, got %d", base64.StdEncoding.EncodedLen(raw))
	}
	got, msgs := pressYank(t, manifestModel(t, raw))
	text, ok := clipboardOf(msgs)
	if !ok || len(text) != raw {
		t.Fatalf("payload at the limit must be sent whole, got %d bytes (%v)", len(text), ok)
	}
	if !strings.HasPrefix(got.notice.text, "enviado ao clipboard: YAML de ") {
		t.Fatalf("notice = %q", got.notice.text)
	}
}

func TestYankRefusesPayloadAboveTheLimit(t *testing.T) {
	for _, raw := range []int{clipboardLimit/4*3 + 1, 1 << 20} {
		got, msgs := pressYank(t, manifestModel(t, raw))
		if text, ok := clipboardOf(msgs); ok {
			t.Fatalf("payload of %d bytes must not be sent, got %d bytes", raw, len(text))
		}
		size := (base64.StdEncoding.EncodedLen(raw) + 1023) / 1024
		want := fmt.Sprintf("grande demais para copiar (%d KiB, limite %d KiB)", size, clipboardLimit/1024)
		if got.notice.text != want {
			t.Fatalf("notice = %q, want %q", got.notice.text, want)
		}
	}
}

func TestYankIsTextWhileFiltering(t *testing.T) {
	m := press(t, yankModel(t, 160, 48), "/", "y")
	if m.filter.Value() != "y" || m.notice.text != "" {
		t.Fatalf("filter=%q notice=%q", m.filter.Value(), m.notice.text)
	}
}

func TestNoticeExpiresOnlyForItsOwnSeq(t *testing.T) {
	m := yankModel(t, 160, 48)
	m, first := pressYank(t, m)
	m, _ = pressYank(t, m)
	var stale noticeExpiredMsg
	for _, msg := range first {
		if e, ok := msg.(noticeExpiredMsg); ok {
			stale = e
		}
	}
	if stale.seq == 0 {
		t.Fatal("yank schedules the notice expiry")
	}
	next, _ := m.Update(stale)
	m = next.(Model)
	if m.notice.text == "" {
		t.Fatal("an older expiry must not clear a newer notice")
	}
	next, _ = m.Update(noticeExpiredMsg{seq: m.notice.seq})
	if next.(Model).notice.text != "" {
		t.Fatal("the current expiry clears the notice")
	}
}

func TestYankFeedbackGoldens(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{
		{"yank_notice_160x48", 160, 48},
		{"yank_notice_130x40", 130, 40},
	} {
		t.Run(size.name, func(t *testing.T) {
			m, _ := pressYank(t, yankModel(t, size.w, size.h))
			c := m.Render()
			golden(t, size.name+".txt", c.Plain())
			golden(t, size.name+".ansi", c.String())
		})
	}
}

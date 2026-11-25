package main

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/app"
)

var osc8 = regexp.MustCompile("\x1b\\]8;[^;]*;([^\x07\x1b]*)(\x07|\x1b\\\\)")

type terminalTape struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	want string
	seen chan struct{}
}

func (w *terminalTape) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if w.want != "" && strings.Contains(w.buf.String(), w.want) {
		w.want = ""
		close(w.seen)
	}
	return n, err
}

func (w *terminalTape) expect(s string) <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.want, w.seen = s, make(chan struct{})
	return w.seen
}

func (w *terminalTape) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

type leavingDemo struct{ tea.Model }

func (m leavingDemo) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.Model.Update(msg)
	return leavingDemo{next}, cmd
}

func (leavingDemo) PendingOperations() []app.PendingOperation {
	return []app.PendingOperation{{Context: "al\x1b]8;;http://evil.example.com\x1b\\pha\n", OperationID: "0123\x1b]8;;http://x\x07", Outcome: "outcome_unknown\x1b[31m"}}
}

func TestExitLeavesNoLinkOpenInTheTerminal(t *testing.T) {
	saved := buildModel
	defer func() { buildModel = saved }()
	buildModel = func(f flags, connect connector) (tea.Model, func(), error) {
		m, shutdown, err := saved(f, connect)
		return leavingDemo{m}, shutdown, err
	}
	for name, quit := range map[string]string{"q": "q", "ctrl+c": "\x03"} {
		tape := &terminalTape{}
		r, w := io.Pipe()
		await := func(ready <-chan struct{}) {
			t.Helper()
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: the screen never arrived", name)
			}
		}
		var stderr bytes.Buffer
		done := make(chan int, 1)
		loaded := tape.expect("Dashboard")
		go func() {
			done <- run([]string{"--demo", "--no-anim"}, &stderr, nil, tea.WithInput(r), tea.WithOutput(tape), tea.WithWindowSize(160, 48), tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "CLICOLOR_FORCE=1"}))
		}()
		await(loaded)
		_, _ = w.Write([]byte("\r"))
		linked := tape.expect("\x1b]8;;http")
		_, _ = w.Write([]byte("d"))
		await(linked)
		_, _ = w.Write([]byte(quit))
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the program did not exit", name)
		}
		_ = w.Close()
		_ = r.Close()
		out := tape.String()
		leave := strings.LastIndex(out, "\x1b[?1049l")
		if leave < 0 {
			t.Fatalf("%s: the alternate screen stays on", name)
		}
		for what, stream := range map[string]string{"before leaving the screen": out[:leave], "at the end": out + stderr.String()} {
			all := osc8.FindAllStringSubmatch(stream, -1)
			if len(all) == 0 || all[len(all)-1][1] != "" {
				t.Fatalf("%s: a link is open %s: %q", name, what, all)
			}
		}
		if got := strings.Count(out, "\x1b]8;"); got != len(osc8.FindAllString(out, -1)) {
			t.Fatalf("%s: an unterminated link sequence", name)
		}
		line := stderr.String()
		if strings.Count(line, "\n") != 1 || strings.ContainsAny(line, "\x1b\x07") || !strings.Contains(line, "operacao enviada sem resultado exibido") {
			t.Fatalf("%s: %q", name, line)
		}
	}
}

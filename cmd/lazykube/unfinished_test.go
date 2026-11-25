package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/app"
)

type leavingModel struct{}

func (leavingModel) Init() tea.Cmd                         { return tea.Quit }
func (m leavingModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (leavingModel) View() tea.View                        { return tea.NewView("") }
func (leavingModel) PendingOperations() []app.PendingOperation {
	return []app.PendingOperation{{Context: "al\x1b[31mpha\n", OperationID: "0123abcd"}}
}

func TestLeavingWhileSendingPrintsAfterShutdown(t *testing.T) {
	savedModel, savedQuiet := buildModel, quietStderr
	defer func() { buildModel, quietStderr = savedModel, savedQuiet }()
	var stderr, screen bytes.Buffer
	restored := false
	quietStderr = func() (func(), error) { return func() { restored = true }, nil }
	buildModel = func(flags, connector) (tea.Model, func(), error) {
		return leavingModel{}, func() {
			if stderr.Len() != 0 {
				t.Error("the line comes after the session shutdown")
			}
		}, nil
	}
	code := run([]string{"--no-anim"}, &stderr, nil, tea.WithInput(strings.NewReader("")), tea.WithOutput(&screen), tea.WithWindowSize(120, 36))
	if code != 0 || !restored {
		t.Fatal(code, restored)
	}
	line := stderr.String()
	if strings.Count(line, "\n") != 1 || strings.Contains(line, "\x1b") || !strings.Contains(line, "operacao 0123abcd.") || strings.Contains(screen.String(), "0123abcd") {
		t.Fatalf("%q", line)
	}
}

func TestUnfinishedOperationsPrintOneSafeLineEach(t *testing.T) {
	var out bytes.Buffer
	writeUnfinished(&out, nil)
	if out.Len() != 0 {
		t.Fatalf("%q", out.String())
	}
	writeUnfinished(&out, []app.PendingOperation{{Context: "alpha", OperationID: "0123abcd"}, {Context: "beta", OperationID: "4567ef"}})
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("%q", lines)
	}
	for i, want := range []string{"contexto alpha, operacao 0123abcd.", "contexto beta, operacao 4567ef."} {
		if !strings.HasPrefix(lines[i], "lazykube: ") || !strings.Contains(lines[i], want) || !strings.Contains(lines[i], "Sair nao desfaz a operacao; confira o alvo e o audit antes de repetir.") {
			t.Fatalf("%q", lines[i])
		}
	}
	writeUnfinished(failingWriter{}, []app.PendingOperation{{Context: "alpha"}})
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("stderr fechado") }

var _ io.Writer = failingWriter{}

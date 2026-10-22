package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	crashCaseEnv  = "LAZYKUBE_CRASH_CASE"
	pluginOutput  = "SENTINELA-PLUGIN"
	crashExitCode = 2
)

type crashModel struct{ mode string }

type boomMsg struct{}

func pluginLikeChild() {
	cmd := exec.Command("/bin/sh", "-c", "echo "+pluginOutput+" >&2")
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "echo "+pluginOutput+" 1>&2")
	}
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

func (c crashModel) Init() tea.Cmd {
	pluginLikeChild()
	switch c.mode {
	case "update":
		return func() tea.Msg { return boomMsg{} }
	case "cmd":
		return func() tea.Msg { panic("PANICO-CMD") }
	case "batch":
		return tea.Batch(func() tea.Msg { return nil }, func() tea.Msg { panic("PANICO-BATCH") })
	case "goroutine":
		go func() { panic("PANICO-SOLTO") }()
		return nil
	case "final":
		return tea.Quit
	}
	return nil
}

func (c crashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(boomMsg); ok {
		panic("PANICO-UPDATE")
	}
	return c, nil
}

func (c crashModel) View() tea.View {
	if c.mode == "view" {
		panic("PANICO-VIEW")
	}
	return tea.NewView("")
}

func TestCrashChild(t *testing.T) {
	mode := os.Getenv(crashCaseEnv)
	if mode == "" {
		t.Skip("runs only as a subprocess")
	}
	buildModel = func(flags, connector) (tea.Model, func(), error) {
		if mode == "init" {
			return nil, nil, errors.New("falha de inicializacao sintetica")
		}
		return crashModel{mode: mode}, func() {}, nil
	}
	in, w := io.Pipe()
	defer func() { _ = w.Close() }()
	os.Exit(run([]string{"--no-anim"}, os.Stderr, nil, tea.WithInput(in), tea.WithOutput(io.Discard), tea.WithWindowSize(120, 36)))
}

func runCrashCase(t *testing.T, mode string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), crashCaseEnv+"="+mode)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("%s: child did not exit", mode)
	}
	return cmd.ProcessState.ExitCode(), stderr.String()
}

func TestPanicsKeepDiagnosticsAndPluginStderrStaysHidden(t *testing.T) {
	for _, tc := range []struct {
		mode, want string
		code       int
		stack      bool
	}{
		{"update", "PANICO-UPDATE", 1, true},
		{"cmd", "PANICO-CMD", 1, true},
		{"batch", "PANICO-BATCH", 1, true},
		{"view", "PANICO-VIEW", 1, true},
		{"goroutine", "PANICO-SOLTO", crashExitCode, true},
		{"init", "falha de inicializacao sintetica", 1, false},
		{"final", "", 0, false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			code, stderr := runCrashCase(t, tc.mode)
			if code != tc.code {
				t.Fatalf("exit code %d, want %d, stderr %q", code, tc.code, stderr)
			}
			if tc.want != "" && !strings.Contains(stderr, tc.want) {
				t.Fatalf("the diagnostic reaches the real stderr: %q", stderr)
			}
			if tc.stack && !strings.Contains(stderr, "goroutine ") {
				t.Fatalf("the diagnostic keeps the stack: %q", stderr)
			}
			if tc.code == 1 && tc.stack && !strings.Contains(stderr, "lazykube: falha interna: "+tc.want) {
				t.Fatalf("recovered panics use the controlled report: %q", stderr)
			}
			if strings.Contains(stderr, pluginOutput) {
				t.Fatalf("the plugin stderr stays isolated: %q", stderr)
			}
		})
	}
}

func TestIsolationFailureStopsBeforeTheProgram(t *testing.T) {
	saved := quietStderr
	quietStderr = func() (func(), error) { return nil, errIsolation }
	defer func() { quietStderr = saved }()
	started := false
	savedModel := buildModel
	buildModel = func(f flags, c connector) (tea.Model, func(), error) {
		return crashModel{mode: "final"}, func() { started = true }, nil
	}
	defer func() { buildModel = savedModel }()
	var stderr bytes.Buffer
	if code := run(nil, &stderr, nil, tea.WithInput(strings.NewReader("")), tea.WithOutput(io.Discard)); code != 1 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stderr.String(), errIsolation.Error()) || !started {
		t.Fatalf("a failed isolation is reported and the session shut down: %q", stderr.String())
	}
}

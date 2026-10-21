package app

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

var epoch = time.Unix(0, 0)

func newTestLoader(connect Connector, noAnim bool) Loader {
	l := NewLoader(LoaderOptions{
		Options: Options{Palette: theme.DefaultPalette(), FPS: 30, NoAnim: noAnim, Now: func() time.Time { return epoch }},
		Context: "dev",
		Connect: connect,
	})
	next, _ := l.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	return next.(Loader)
}

func loaderStep(t *testing.T, l Loader, msg tea.Msg) (Loader, tea.Cmd) {
	t.Helper()
	next, cmd := l.Update(msg)
	nl, ok := next.(Loader)
	if !ok {
		t.Fatalf("expected loader, got %T", next)
	}
	return nl, cmd
}

func states(l Loader) []ui.StepState {
	out := make([]ui.StepState, len(l.steps))
	for i, s := range l.steps {
		out[i] = s.state
	}
	return out
}

func sameStates(got []ui.StepState, want ...ui.StepState) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func progress(stage data.Stage, done bool, detail string) progressMsg {
	return progressMsg{p: data.Progress{Stage: stage, Done: done, Context: "dev", Detail: detail}}
}

func midwayLoader(t *testing.T, noAnim bool) Loader {
	t.Helper()
	l := newTestLoader(nil, noAnim)
	for _, msg := range []progressMsg{
		progress(data.StageKubeconfig, true, "3 contextos"),
		progress(data.StageConnect, false, ""),
		progress(data.StageConnect, true, "v1.31.2"),
		progress(data.StagePermissions, false, ""),
		progress(data.StagePermissions, true, "sem acesso: nodes"),
		progress(data.StageSync, false, "pods, deployments, events"),
	} {
		l, _ = loaderStep(t, l, msg)
	}
	return l
}

func TestLoaderTracksStages(t *testing.T) {
	l := newTestLoader(nil, true)
	if !sameStates(states(l), ui.StepRunning, ui.StepPending, ui.StepPending, ui.StepPending) {
		t.Fatalf("initial states = %v", states(l))
	}
	l, cmd := loaderStep(t, l, progress(data.StageKubeconfig, true, "3 contextos"))
	if cmd == nil {
		t.Fatal("loader must keep listening for progress")
	}
	if !sameStates(states(l), ui.StepDone, ui.StepRunning, ui.StepPending, ui.StepPending) {
		t.Fatalf("after kubeconfig = %v", states(l))
	}
	if got := l.stageTitle(data.StageConnect); got != "conectando ao contexto dev" {
		t.Fatalf("connect title = %q", got)
	}
	l = midwayLoader(t, true)
	if !sameStates(states(l), ui.StepDone, ui.StepDone, ui.StepDone, ui.StepRunning) {
		t.Fatalf("midway = %v", states(l))
	}
	if l.steps[data.StageConnect].detail != "v1.31.2" || l.bar.Pos != 0.75 {
		t.Fatalf("detail=%q bar=%v", l.steps[data.StageConnect].detail, l.bar.Pos)
	}
}

func TestLoaderShowsFailedStage(t *testing.T) {
	l := newTestLoader(nil, false)
	l, _ = loaderStep(t, l, progress(data.StageKubeconfig, true, ""))
	l, _ = loaderStep(t, l, progress(data.StageConnect, false, ""))
	l, cmd := loaderStep(t, l, connectedMsg{err: errors.New("cannot reach the API server:\n\x1b]0;pwn\x07dial tcp 10.0.0.1:6443: i/o timeout")})
	if cmd != nil {
		t.Fatal("failure must not schedule more work")
	}
	if !sameStates(states(l), ui.StepDone, ui.StepFailed, ui.StepPending, ui.StepPending) {
		t.Fatalf("states = %v", states(l))
	}
	stage, msg, failed := l.Failure()
	if !failed || stage != "conectando ao contexto dev" || strings.ContainsAny(msg, "\x1b\x07\n") {
		t.Fatalf("failure = %q %q %v", stage, msg, failed)
	}
	if msg != "cannot reach the API server: dial tcp 10.0.0.1:6443: i/o timeout" {
		t.Fatalf("message = %q", msg)
	}
	if l, cmd = loaderStep(t, l, loaderFrameMsg{at: epoch.Add(time.Second)}); cmd != nil {
		t.Fatal("failed screen must stop animating")
	}
	if _, cmd = loaderStep(t, l, keyPress("j")); cmd != nil {
		t.Fatal("other keys are ignored on the error screen")
	}
	_, cmd = loaderStep(t, l, keyPress("q"))
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatal("q must quit on the error screen")
	}
	if ExitCode(l) != ExitFailure {
		t.Fatalf("exit code = %d", ExitCode(l))
	}
	if _, cmd := loaderStep(t, newTestLoader(nil, true), keyPress("q")); cmd != nil {
		t.Fatal("q must not quit while connecting")
	}
}

func TestLoaderFailureBeforeAnyProgress(t *testing.T) {
	l, _ := loaderStep(t, newTestLoader(nil, true), connectedMsg{err: errors.New("cannot load kubeconfig")})
	if stage, _, _ := l.Failure(); stage != "carregando kubeconfig" {
		t.Fatalf("stage = %q", stage)
	}
}

func TestLoaderCtrlCCancelsConnection(t *testing.T) {
	l := newTestLoader(nil, false)
	l, cmd := loaderStep(t, l, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatal("ctrl+c must quit")
	}
	if l.ctx.Err() == nil {
		t.Fatal("ctrl+c must cancel the connection context")
	}
	if ExitCode(l) != ExitCancelled {
		t.Fatalf("exit code = %d", ExitCode(l))
	}
	if l, _ = loaderStep(t, l, connectedMsg{err: context.Canceled}); l.err != nil {
		t.Fatal("results after cancel must be ignored")
	}
}

func TestLoaderSwitchesToMainWithFade(t *testing.T) {
	l := midwayLoader(t, false)
	l, _ = loaderStep(t, l, tea.WindowSizeMsg{Width: 130, Height: 40})
	l, cmd := loaderStep(t, l, connectedMsg{src: data.NewDemo()})
	if cmd == nil || l.main == nil {
		t.Fatal("success must build the main model and start it")
	}
	if l.main.w != 130 || l.main.h != 40 {
		t.Fatalf("main model must inherit the size, got %dx%d", l.main.w, l.main.h)
	}
	if !sameStates(states(l), ui.StepDone, ui.StepDone, ui.StepDone, ui.StepDone) {
		t.Fatalf("states = %v", states(l))
	}
	start := l.Render().Plain()
	if !strings.Contains(start, "sincronizando cache") {
		t.Fatal("fade must start from the loading screen")
	}
	l, _ = loaderStep(t, l, loaderFrameMsg{at: epoch.Add(300 * time.Millisecond)})
	if plain := l.Render().Plain(); !strings.Contains(plain, "Pods") {
		t.Fatal("second half of the fade must show the main layout")
	}
	before := l.main.selName
	l, _ = loaderStep(t, l, keyPress("j"))
	if l.main.selName == before {
		t.Fatal("keys must reach the main model during the fade")
	}
	next, _ := l.Update(loaderFrameMsg{at: epoch.Add(time.Second)})
	if _, ok := next.(Model); !ok {
		t.Fatalf("after the fade the main model takes over, got %T", next)
	}
	if ExitCode(next) != 0 {
		t.Fatal("main model exits cleanly")
	}
}

func TestLoaderStaticSwitchesImmediately(t *testing.T) {
	l := midwayLoader(t, true)
	if l.tick() != nil {
		t.Fatal("--no-anim must not schedule frames")
	}
	next, cmd := l.Update(connectedMsg{src: data.NewDemo()})
	m, ok := next.(Model)
	if !ok {
		t.Fatalf("static loader must hand over at once, got %T", next)
	}
	if cmd != nil {
		t.Fatal("static main model schedules no frames")
	}
	if m.w != 100 || m.animated {
		t.Fatalf("main model state: w=%d animated=%v", m.w, m.animated)
	}
}

func TestLoaderConnectCommandStreamsInOrder(t *testing.T) {
	var closed atomic.Int32
	connect := func(_ context.Context, report func(data.Progress)) (data.Source, func(), error) {
		for _, s := range data.Stages() {
			report(data.Progress{Stage: s})
			report(data.Progress{Stage: s, Done: true})
		}
		return data.NewDemo(), func() { closed.Add(1) }, nil
	}
	l := newTestLoader(connect, true)
	if msg := l.connectCmd()(); msg != nil {
		t.Fatalf("connect command returns through the event stream, got %T", msg)
	}
	var got []tea.Msg
	for {
		msg := waitEvent(l.events)()
		if msg == nil {
			break
		}
		got = append(got, msg)
	}
	if len(got) != 9 {
		t.Fatalf("expected 8 progress events and the result, got %d", len(got))
	}
	if _, ok := got[8].(connectedMsg); !ok {
		t.Fatalf("result must arrive last, got %T", got[8])
	}
	l.Shutdown()
	l.Shutdown()
	if closed.Load() != 1 {
		t.Fatalf("source closed %d times", closed.Load())
	}
}

func TestLoaderClosesLateSourceAfterShutdown(t *testing.T) {
	var closed atomic.Int32
	release := make(chan struct{})
	connect := func(context.Context, func(data.Progress)) (data.Source, func(), error) {
		<-release
		return data.NewDemo(), func() { closed.Add(1) }, nil
	}
	l := newTestLoader(connect, true)
	done := make(chan struct{})
	go func() {
		l.connectCmd()()
		close(done)
	}()
	l.Shutdown()
	close(release)
	<-done
	if closed.Load() != 1 {
		t.Fatal("a source that arrives after shutdown must be closed")
	}
}

func loadingGoldenLoader(t *testing.T, failed bool) Loader {
	l := midwayLoader(t, true)
	if failed {
		l, _ = loaderStep(t, l, connectedMsg{err: errors.New("timed out waiting for the cluster cache to sync")})
	}
	l.clock = anim.Fixed(goldenT, goldenPhase, 30)
	return l
}

func TestLoadingGolden(t *testing.T) {
	for name, failed := range map[string]bool{"loading": false, "loading_error": true} {
		c := loadingGoldenLoader(t, failed).Render()
		golden(t, name+"_100x32.txt", c.Plain())
		golden(t, name+"_100x32.ansi", c.String())
	}
}

func TestLoadingIsResponsive(t *testing.T) {
	for _, size := range [][2]int{{100, 32}, {60, 18}, {40, 12}, {20, 6}, {1, 1}} {
		l := loadingGoldenLoader(t, true)
		l.w, l.h = size[0], size[1]
		plain := l.Render().Plain()
		lines := strings.Split(plain, "\n")
		if len(lines) != size[1] {
			t.Fatalf("%v: rendered %d rows", size, len(lines))
		}
		if size[0] >= 60 && !strings.Contains(plain, "falhou") {
			t.Fatalf("%v: error missing:\n%s", size, plain)
		}
	}
	small := loadingGoldenLoader(t, false)
	small.w, small.h = 60, 18
	if plain := small.Render().Plain(); !strings.Contains(plain, "█▀▄") || !strings.Contains(plain, "ctrl+c cancelar") {
		t.Fatalf("compact layout must keep the logo and the hint:\n%s", plain)
	}
}

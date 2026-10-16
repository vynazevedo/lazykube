package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/TNK-Studio/lazykube/internal/app"
	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/kube"
)

func TestParseFlagsNamespaceShorthand(t *testing.T) {
	f, err := parseFlags([]string{"-n", "apps", "--context", "dev"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if f.namespace != "apps" || f.context != "dev" || f.demo {
		t.Fatalf("unexpected flags: %+v", f)
	}
}

func TestParseFlagsRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{{"--fps", "0"}, {"--fps", "61"}, {"--palette", "nope"}, {"extra"}} {
		if _, err := parseFlags(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
}

func TestDemoSkipsConnectorAndLoader(t *testing.T) {
	called := false
	connect := func(context.Context, kube.ConnectOptions) (data.Source, func(), error) {
		called = true
		return nil, nil, nil
	}
	model, shutdown, err := newModel(flags{demo: true, palette: "charm", fps: 30}, connect)
	if err != nil || called || shutdown == nil {
		t.Fatalf("demo build: err=%v called=%v", err, called)
	}
	if _, ok := model.(app.Model); !ok {
		t.Fatalf("demo must start on the main model, got %T", model)
	}
}

func TestRealModeStartsOnLoader(t *testing.T) {
	model, shutdown, err := newModel(flags{palette: "charm", fps: 30}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()
	if _, ok := model.(app.Loader); !ok {
		t.Fatalf("real mode must open the loading screen, got %T", model)
	}
}

func TestConnectorPassesOptionsAndProgress(t *testing.T) {
	var got kube.ConnectOptions
	connect := func(_ context.Context, o kube.ConnectOptions) (data.Source, func(), error) {
		got = o
		o.Progress(data.Progress{Stage: data.StageSync})
		return data.NewDemo(), func() {}, nil
	}
	f := flags{context: "dev", kubeconfig: "/k", namespace: "apps", palette: "charm", fps: 30}
	var reported []data.Progress
	src, _, err := clusterConnector(f, connect)(context.Background(), func(p data.Progress) { reported = append(reported, p) })
	if err != nil || src == nil {
		t.Fatalf("err=%v src=%v", err, src)
	}
	if got.Kubeconfig != "/k" || got.Context != "dev" || got.Namespace != "apps" || got.Progress == nil {
		t.Fatalf("connect options = %+v", got)
	}
	if len(reported) != 1 || reported[0].Stage != data.StageSync {
		t.Fatalf("progress not forwarded: %+v", reported)
	}
}

type keyFeeder struct {
	r    *io.PipeReader
	w    *io.PipeWriter
	stop chan struct{}
	wg   sync.WaitGroup
}

func feedKeys(key string) *keyFeeder {
	r, w := io.Pipe()
	k := &keyFeeder{r: r, w: w, stop: make(chan struct{})}
	k.wg.Add(1)
	go func() {
		defer k.wg.Done()
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-k.stop:
				return
			case <-t.C:
				if _, err := w.Write([]byte(key)); err != nil {
					return
				}
			}
		}
	}()
	return k
}

func (k *keyFeeder) close() {
	close(k.stop)
	_ = k.w.Close()
	_ = k.r.Close()
	k.wg.Wait()
}

func runWith(t *testing.T, connect connector, key string) (int, string) {
	t.Helper()
	keys := feedKeys(key)
	defer keys.close()
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(nil, &stderr, connect, tea.WithInput(keys.r), tea.WithOutput(io.Discard), tea.WithWindowSize(100, 30))
	}()
	select {
	case code := <-done:
		return code, stderr.String()
	case <-time.After(10 * time.Second):
		t.Fatal("program did not exit")
		return 0, ""
	}
}

func TestRunReportsConnectError(t *testing.T) {
	connect := func(_ context.Context, o kube.ConnectOptions) (data.Source, func(), error) {
		o.Progress(data.Progress{Stage: data.StageKubeconfig, Done: true, Context: "dev"})
		o.Progress(data.Progress{Stage: data.StageConnect, Context: "dev"})
		return nil, nil, errors.New("cannot reach the API server:\n\x1b[31mdial tcp: refused")
	}
	code, stderr := runWith(t, connect, "q")
	if code != app.ExitFailure {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr, "conectando ao contexto dev: cannot reach the API server: dial tcp: refused") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestRunCtrlCCancelsConnection(t *testing.T) {
	cancelled := make(chan struct{})
	connect := func(ctx context.Context, _ kube.ConnectOptions) (data.Source, func(), error) {
		<-ctx.Done()
		close(cancelled)
		return nil, nil, ctx.Err()
	}
	code, _ := runWith(t, connect, "\x03")
	if code != app.ExitCancelled {
		t.Fatalf("exit code = %d", code)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("connection context was not cancelled")
	}
}

func TestRunSigintDuringLoadingExitsCancelled(t *testing.T) {
	cancelled := make(chan struct{})
	connect := func(ctx context.Context, _ kube.ConnectOptions) (data.Source, func(), error) {
		<-ctx.Done()
		close(cancelled)
		return nil, nil, ctx.Err()
	}
	interrupt := tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(tea.KeyPressMsg); ok {
			return tea.InterruptMsg{}
		}
		return msg
	})
	keys := feedKeys("x")
	defer keys.close()
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(nil, &stderr, connect, tea.WithInput(keys.r), tea.WithOutput(io.Discard), tea.WithWindowSize(100, 30), interrupt)
	}()
	select {
	case code := <-done:
		if code != app.ExitCancelled {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("program did not exit on SIGINT")
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("deferred shutdown must cancel the connection")
	}
}

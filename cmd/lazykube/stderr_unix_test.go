//go:build unix

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/sys/unix"

	"github.com/vynazevedo/lazykube/internal/app"
)

const sentinel = "SENTINELA"

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureFD2(t *testing.T) func() string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "fd2")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := unix.Dup(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := redirect(int(f.Fd()), 2); err != nil {
		t.Fatal(err)
	}
	return func() string {
		_ = redirect(saved, 2)
		_ = unix.Close(saved)
		_ = f.Close()
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}

func TestRunHidesPluginAndServerSecrets(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"%s-CORPO","reason":"Unauthorized","code":401}`, sentinel)
	}))
	defer srv.Close()
	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugin.sh")
	script := "#!/bin/sh\necho " + sentinel + "-STDERR >&2\necho '{\"apiVersion\":\"client.authentication.k8s.io/v1\",\"kind\":\"ExecCredential\",\"status\":{\"token\":\"" + sentinel + "-TOKEN\"}}'\n"
	if err := os.WriteFile(plugin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	kubeconfig := filepath.Join(dir, "config")
	cfg := fmt.Sprintf("apiVersion: v1\nkind: Config\ncurrent-context: alvo\nclusters:\n- name: alvo\n  cluster:\n    server: %s/%s-URL\n    insecure-skip-tls-verify: true\nusers:\n- name: alvo\n  user:\n    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: %s\n      interactiveMode: Never\ncontexts:\n- name: alvo\n  context:\n    cluster: alvo\n    user: alvo\n", srv.URL, sentinel, plugin)
	if err := os.WriteFile(kubeconfig, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kubeconfig)

	release := captureFD2(t)
	keys := feedKeys("q")
	var stderr, screen lockedBuffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"--kubeconfig", kubeconfig, "--no-anim"}, &stderr, connectCluster, tea.WithInput(keys.r), tea.WithOutput(&screen), tea.WithWindowSize(120, 36))
	}()
	var code int
	select {
	case code = <-done:
	case <-time.After(20 * time.Second):
		keys.close()
		_ = release()
		t.Fatal("program did not exit")
	}
	keys.close()
	fd2 := release()
	if code != app.ExitFailure {
		t.Fatalf("exit code = %d, stderr %q", code, stderr.String())
	}
	for name, out := range map[string]string{"process stderr": fd2, "final message": stderr.String(), "screen": screen.String()} {
		if strings.Contains(out, sentinel) {
			t.Fatalf("%s leaks a sentinel: %q", name, out)
		}
	}
	if !strings.Contains(stderr.String(), "401") {
		t.Fatalf("the final message still explains the failure: %q", stderr.String())
	}
}

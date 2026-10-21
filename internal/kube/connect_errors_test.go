//go:build unix

package kube

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sentinel = "SENTINELA"

func sentinelPlugin(t *testing.T, dir string, fail bool) string {
	t.Helper()
	body := "#!/bin/sh\necho " + sentinel + "-STDERR >&2\n"
	if fail {
		body += "exit 3\n"
	} else {
		body += `echo '{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":"` + sentinel + `-TOKEN"}}'` + "\n"
	}
	path := filepath.Join(dir, "plugin.sh")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func sentinelKubeconfig(t *testing.T, server, plugin string) string {
	t.Helper()
	cfg := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: alvo
clusters:
- name: alvo
  cluster:
    server: %s/%s-URL
    insecure-skip-tls-verify: true
users:
- name: alvo
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: %s
      interactiveMode: Never
      installHint: %s-HINT
contexts:
- name: alvo
  context:
    cluster: alvo
    user: alvo
`, server, sentinel, plugin, sentinel)
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func closedAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "https://" + addr
}

func TestConnectErrorsAreRedacted(t *testing.T) {
	SilenceClientLogs()
	t.Setenv("KUBECONFIG", "/nonexistent/lazykube-test")
	status := func(code int, reason string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"%s-CORPO","reason":"%s","code":%d}`, sentinel, reason, code)
		}
	}
	unauthorized := httptest.NewTLSServer(status(http.StatusUnauthorized, "Unauthorized"))
	defer unauthorized.Close()
	forbidden := httptest.NewTLSServer(status(http.StatusForbidden, "Forbidden"))
	defer forbidden.Close()
	broken := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, "%s-500", sentinel)
	}))
	defer broken.Close()
	dir := t.TempDir()
	ok, failing := sentinelPlugin(t, dir, false), sentinelPlugin(t, t.TempDir(), true)
	bad := filepath.Join(t.TempDir(), "bad")
	if err := os.WriteFile(bad, []byte("users:\n- name: "+sentinel+"\n  : ["), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, kubeconfig, context, want string
	}{
		{"unauthorized", sentinelKubeconfig(t, unauthorized.URL, ok), "", "401"},
		{"forbidden", sentinelKubeconfig(t, forbidden.URL, ok), "", "403"},
		{"server error", sentinelKubeconfig(t, broken.URL, ok), "", "servidor"},
		{"plugin failure", sentinelKubeconfig(t, unauthorized.URL, failing), "", "plugin de autenticação"},
		{"unreachable", sentinelKubeconfig(t, closedAddress(t), ok), "", "inacessível"},
		{"missing context", sentinelKubeconfig(t, unauthorized.URL, ok), "outro", "não existe"},
		{"bad kubeconfig", bad, "", "kubeconfig"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			src, err := Connect(ctx, ConnectOptions{Kubeconfig: tc.kubeconfig, Context: tc.context})
			if src != nil {
				src.Close()
			}
			if err == nil {
				t.Fatal("connection must fail")
			}
			if strings.Contains(err.Error(), sentinel) || strings.Contains(err.Error(), "127.0.0.1") {
				t.Fatalf("connection error leaks remote or local secrets: %q", err.Error())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("connection error is not specific: %q", err.Error())
			}
		})
	}
}

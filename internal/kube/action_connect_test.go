//go:build linux

package kube

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func connectedConfig(t *testing.T, server string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	body := fmt.Sprintf("apiVersion: v1\nkind: Config\ncurrent-context: alvo\nclusters:\n- name: alvo\n  cluster:\n    server: %s\nusers:\n- name: alvo\n  user: {}\ncontexts:\n- name: alvo\n  context:\n    cluster: alvo\n    user: alvo\n", server)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func connectedAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/version":
		_, _ = w.Write([]byte(`{"gitVersion":"v1.36.0"}`))
	case strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews"):
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(wireAllowed))
	case r.URL.Path == "/apis/apps/v1/namespaces/apps/deployments/api/scale":
		_, _ = w.Write([]byte(`{"apiVersion":"autoscaling/v1","kind":"Scale","metadata":{"name":"api","namespace":"apps","uid":"uid","resourceVersion":"7"},"spec":{"replicas":1}}`))
	case r.URL.Path == "/api/v1/namespaces/apps/pods" && r.URL.Query().Get("watch") == "":
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"1"},"items":[]}`))
	case r.URL.Path == "/api/v1/namespaces/apps/pods":
		_, _ = w.Write([]byte(`{"type":"BOOKMARK","object":{"apiVersion":"v1","kind":"Pod","metadata":{"resourceVersion":"1","annotations":{"k8s.io/initial-events-end":"true"}}}}` + "\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	default:
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`))
	}
}

func TestConnectedExecutorRefusesRedirects(t *testing.T) {
	var mu sync.Mutex
	var writes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews") {
			connectedAPI(w, r)
			return
		}
		mu.Lock()
		writes = append(writes, r.Method+" "+r.URL.RequestURI())
		first := len(writes) == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if first {
			w.Header().Set("Location", r.URL.Path)
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte(wireObject))
	}))
	defer server.Close()
	dir := t.TempDir()
	s, err := Connect(context.Background(), ConnectOptions{Kubeconfig: connectedConfig(t, server.URL), Namespace: "apps", AllowWrites: true, AuditPath: filepath.Join(dir, "audit", "audit.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.Actions().Prepare(context.Background(), scaleRequest(guard.Scale))
	if !errors.Is(err, guard.DryRunFailed) || p.ID != "" {
		t.Fatal(p, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 1 || writes[0] != "PUT /apis/apps/v1/namespaces/apps/deployments/api/scale?dryRun=All" {
		t.Fatal(writes)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "audit", "audit.jsonl"))
	if err != nil || strings.Contains(string(raw), `"intent"`) || strings.Contains(string(raw), "Location") {
		t.Fatal(string(raw), err)
	}
}

func TestConnectedExecutorWithRefusedAuditMakesNoRequest(t *testing.T) {
	var mu sync.Mutex
	watching := false
	var after []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if watching {
			after = append(after, r.Method+" "+r.URL.Path)
		}
		mu.Unlock()
		connectedAPI(w, r)
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	s, err := Connect(context.Background(), ConnectOptions{Kubeconfig: connectedConfig(t, server.URL), Namespace: "apps", AllowWrites: true, AuditPath: filepath.Join(dir, "audit", "audit.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mu.Lock()
	watching = true
	mu.Unlock()
	if _, err = s.Actions().Observe(context.Background(), guard.Scale, scaleRequest(guard.Scale).Target); !errors.Is(err, guard.AuditFailed) {
		t.Fatal(err)
	}
	if _, err = s.Actions().Prepare(context.Background(), scaleRequest(guard.Scale)); !errors.Is(err, guard.AuditFailed) {
		t.Fatal(err)
	}
	if s.Actions().AuditRefusal() != guard.RefusedOwner {
		t.Fatal(s.Actions().AuditRefusal())
	}
	mu.Lock()
	defer mu.Unlock()
	for _, r := range after {
		if strings.Contains(r, "selfsubjectaccessreviews") || strings.Contains(r, "/deployments/api") {
			t.Fatal(after)
		}
	}
	if !guard.UniqueSessionIdentity(s.GuardSession().Identity) {
		t.Fatal(s.GuardSession().Identity)
	}
}

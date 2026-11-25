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

func TestConnectedExecutorRefusesRedirects(t *testing.T) {
	var mu sync.Mutex
	var writes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/version":
			_, _ = w.Write([]byte(`{"gitVersion":"v1.36.0"}`))
		case strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(wireAllowed))
		case r.Method != http.MethodGet:
			mu.Lock()
			writes = append(writes, r.Method+" "+r.URL.RequestURI())
			first := len(writes) == 1
			mu.Unlock()
			if first {
				w.Header().Set("Location", r.URL.Path)
				w.WriteHeader(http.StatusTemporaryRedirect)
				return
			}
			_, _ = w.Write([]byte(wireObject))
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
	}))
	defer server.Close()
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	body := fmt.Sprintf("apiVersion: v1\nkind: Config\ncurrent-context: alvo\nclusters:\n- name: alvo\n  cluster:\n    server: %s\nusers:\n- name: alvo\n  user: {}\ncontexts:\n- name: alvo\n  context:\n    cluster: alvo\n    user: alvo\n", server.URL)
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Connect(context.Background(), ConnectOptions{Kubeconfig: config, Namespace: "apps", AllowWrites: true, AuditPath: filepath.Join(dir, "audit", "audit.jsonl")})
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

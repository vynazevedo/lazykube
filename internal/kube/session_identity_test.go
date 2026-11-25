//go:build linux || darwin

package kube

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func emptyCluster(t *testing.T) string {
	t.Helper()
	kinds := map[string]string{"pods": "v1 PodList", "namespaces": "v1 NamespaceList", "nodes": "v1 NodeList", "events": "v1 EventList", "deployments": "apps/v1 DeploymentList"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/version" {
			_, _ = fmt.Fprint(w, `{"major":"1","minor":"34","gitVersion":"v1.34.0"}`)
			return
		}
		api, kind, ok := strings.Cut(kinds[filepath.Base(r.URL.Path)], " ")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
			return
		}
		if r.URL.Query().Get("sendInitialEvents") != "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Invalid","code":422}`)
			return
		}
		if r.URL.Query().Get("watch") != "" {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = fmt.Fprintf(w, `{"kind":%q,"apiVersion":%q,"metadata":{"resourceVersion":"1"},"items":[]}`, kind, api)
	}))
	t.Cleanup(server.Close)
	config := fmt.Sprintf("apiVersion: v1\nkind: Config\ncurrent-context: dev\nclusters:\n- name: c\n  cluster:\n    server: %s\nusers:\n- name: u\n  user: {}\ncontexts:\n- name: dev\n  context:\n    cluster: c\n    user: u\n", server.URL)
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConnectGivesWriterAndReaderOneUniqueIdentity(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	audit := filepath.Join(base, "audit", "audit.jsonl")
	seen := map[string]bool{}
	for range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		src, err := Connect(ctx, ConnectOptions{Kubeconfig: emptyCluster(t), Context: "dev", Namespace: "apps", AuditPath: audit})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		identity := src.GuardSession().Identity
		nonce := identity[strings.LastIndex(identity, "-")+1:]
		if !guard.UniqueSessionIdentity(identity) || seen[nonce] {
			t.Fatalf("homonymous sessions need distinct opaque identities: %q", identity)
		}
		seen[nonce] = true
		target := guard.Target{Version: "v1", Group: "apps", Resource: "deployments", Namespace: "apps", Name: "api", UID: "u", ResourceVersion: "1"}
		if _, err := src.Actions().Prepare(t.Context(), guard.Request{Action: guard.Restart, Target: target}); err == nil {
			t.Fatal("read-only session must refuse")
		}
		src.Close()
		page, err := guard.ReadAudit(t.Context(), audit, guard.AuditCursor{}, 0)
		if err != nil || len(page.Events) == 0 || page.Events[0].Identity != identity || page.Events[0].Context != "dev" {
			t.Fatalf("the writer records the identity the reader filters by: %+v %v", page.Events, err)
		}
	}
}

package kube

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/vynazevedo/lazykube/internal/data"
)

const kubeconfigA = `apiVersion: v1
kind: Config
current-context: alpha
clusters:
- name: alpha-cluster
  cluster:
    server: https://127.0.0.1:1
- name: shared-cluster
  cluster:
    server: https://127.0.0.1:2
users:
- name: alpha-user
  user:
    token: segredo-alpha-nao-vazar
contexts:
- name: alpha
  context:
    cluster: alpha-cluster
    user: alpha-user
    namespace: apps
- name: shared
  context:
    cluster: shared-cluster
    user: alpha-user
    namespace: primeiro
- name: orphan
  context:
    cluster: missing-cluster
    user: alpha-user
`

const kubeconfigB = `apiVersion: v1
kind: Config
current-context: beta
clusters:
- name: beta-cluster
  cluster:
    server: https://127.0.0.1:3
users:
- name: beta-user
  user:
    token: segredo-beta-nao-vazar
contexts:
- name: beta
  context:
    cluster: beta-cluster
    user: beta-user
- name: shared
  context:
    cluster: beta-cluster
    user: beta-user
    namespace: segundo
- name: nouser
  context:
    cluster: beta-cluster
    user: ghost
`

func writeKubeconfigs(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml")
	for path, body := range map[string]string{a: kubeconfigA, b: kubeconfigB} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return a, b
}

func names(c data.ContextCatalog) string {
	out := make([]string, len(c.Contexts))
	for i, k := range c.Contexts {
		out[i] = k.Name
	}
	return strings.Join(out, ",")
}

func TestContextsMergeKubeconfigListWithNativePrecedence(t *testing.T) {
	a, b := writeKubeconfigs(t)
	t.Setenv("KUBECONFIG", a+string(os.PathListSeparator)+b)
	before := [][]byte{read(t, a), read(t, b)}

	cat, err := Contexts("")
	if err != nil {
		t.Fatal(err)
	}
	if names(cat) != "alpha,beta,nouser,orphan,shared" || cat.Current != "alpha" || cat.Synthetic {
		t.Fatalf("merged catalog = %s current %q", names(cat), cat.Current)
	}
	alpha, _ := cat.Find("alpha")
	if !alpha.Current || alpha.Cluster != "alpha-cluster" || alpha.User != "alpha-user" || alpha.Namespace != "apps" || alpha.Problem != "" {
		t.Fatalf("alpha = %+v", alpha)
	}
	if shared, _ := cat.Find("shared"); shared.Namespace != "primeiro" || shared.Cluster != "shared-cluster" {
		t.Fatalf("a duplicated context follows the first file, as client-go does: %+v", shared)
	}
	if orphan, _ := cat.Find("orphan"); orphan.Problem == "" {
		t.Fatal("a context without its cluster is flagged")
	}
	if nouser, _ := cat.Find("nouser"); nouser.Problem == "" {
		t.Fatal("a context without its user is flagged")
	}
	if name, err := ResolveContext(ConnectOptions{Context: "beta"}); err != nil || name != "beta" {
		t.Fatalf("contexts of the second file resolve: %q %v", name, err)
	}
	if _, err := restConfig(ConnectOptions{Context: "beta", Namespace: "apps"}, "beta"); err != nil {
		t.Fatalf("client config builds from the merged list in memory: %v", err)
	}
	if !bytes.Equal(before[0], read(t, a)) || !bytes.Equal(before[1], read(t, b)) {
		t.Fatal("kubeconfig files must stay byte for byte unchanged")
	}
}

func TestContextsExplicitKubeconfigWins(t *testing.T) {
	a, b := writeKubeconfigs(t)
	t.Setenv("KUBECONFIG", a)
	cat, err := Contexts(b)
	if err != nil {
		t.Fatal(err)
	}
	if names(cat) != "beta,nouser,shared" || cat.Current != "beta" {
		t.Fatalf("--kubeconfig selects one file: %s current %q", names(cat), cat.Current)
	}
	if shared, _ := cat.Find("shared"); shared.Namespace != "segundo" {
		t.Fatalf("shared = %+v", shared)
	}
	if _, err := ResolveContext(ConnectOptions{Kubeconfig: b, Context: "alpha"}); err == nil {
		t.Fatal("a context of another file is not reachable through --kubeconfig")
	}
}

func TestContextsErrorsAreSafe(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(broken, []byte("users:\n- name: x\n  user:\n    token: segredo-quebrado\n  : ["), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{broken, filepath.Join(dir, "missing.yaml")} {
		_, err := Contexts(path)
		if err == nil || strings.Contains(err.Error(), "segredo") {
			t.Fatalf("%s: unreadable kubeconfig fails without leaking content: %v", filepath.Base(path), err)
		}
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestKubeconfigLoadingNeverMigrates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dest := filepath.Join(home, ".kube", "config")
	saved := clientcmd.RecommendedHomeFile
	clientcmd.RecommendedHomeFile = dest
	t.Cleanup(func() { clientcmd.RecommendedHomeFile = saved })
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".kube", ".kubeconfig")
	if err := os.WriteFile(legacy, []byte(kubeconfigA), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := writeKubeconfigs(t)
	for name, run := range map[string]func(){
		"catalog explicit": func() { _, _ = Contexts(a) },
		"catalog default":  func() { t.Setenv("KUBECONFIG", ""); _, _ = Contexts("") },
		"resolve":          func() { _, _ = ResolveContext(ConnectOptions{Kubeconfig: a, Context: "alpha"}) },
		"rest config":      func() { _, _ = restConfig(ConnectOptions{Kubeconfig: a}, "alpha") },
	} {
		run()
		if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: loading the kubeconfig must never migrate the legacy file", name)
		}
	}
	if len(loadingRules("").MigrationRules) != 0 {
		t.Fatal("migration rules are disabled")
	}
	if !bytes.Equal(read(t, legacy), []byte(kubeconfigA)) {
		t.Fatal("the legacy file stays unchanged")
	}
}

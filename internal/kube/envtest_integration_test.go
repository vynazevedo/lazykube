//go:build integration

package kube_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/vynazevedo/lazykube/internal/kube"
)

func TestEnvtestConnectAndSwitchContexts(t *testing.T) {
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		t.Fatal("execute scripts/test-integration.sh para fornecer os binarios locais")
	}
	for _, name := range []string{"kube-apiserver", "etcd", "kubectl"} {
		path, err := filepath.Abs(filepath.Join(assets, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
		key := map[string]string{"kube-apiserver": "TEST_ASSET_KUBE_APISERVER", "etcd": "TEST_ASSET_ETCD", "kubectl": "TEST_ASSET_KUBECTL"}[name]
		t.Setenv(key, path)
	}
	path := filepath.Join(t.TempDir(), "config")
	t.Setenv("KUBECONFIG", path)
	t.Setenv("USE_EXISTING_CLUSTER", "false")
	config := clientcmdapi.NewConfig()
	for _, name := range []string{"a", "b"} {
		cfg := isolatedEnvtest(t, name, assets)
		config.Clusters[name] = &clientcmdapi.Cluster{Server: cfg.Host, CertificateAuthorityData: cfg.CAData}
		config.AuthInfos[name] = &clientcmdapi.AuthInfo{ClientCertificateData: cfg.CertData, ClientKeyData: cfg.KeyData}
		config.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name, Namespace: "integration"}
	}
	config.CurrentContext = "a"
	contents, err := clientcmd.Write(*config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "a"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			source, err := kube.Connect(ctx, kube.ConnectOptions{Kubeconfig: path, Context: name, Namespace: "integration"})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				snap := source.Snapshot()
				events := source.Events("integration/pod-" + name)
				if snap.Cluster.Context == name && len(snap.Pods) == 1 && snap.Pods[0].Name == "pod-"+name &&
					len(snap.Deployments) == 1 && snap.Deployments[0].Name == "deployment-"+name &&
					len(events) == 1 && events[0].Message == "fixture-"+name {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatalf("contexto %s: snapshot=%+v events=%+v: %v", name, snap, events, ctx.Err())
				case <-ticker.C:
				}
			}
		})
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(contents, after) {
		t.Fatalf("kubeconfig foi alterado: %v", err)
	}
	if os.Getenv("KUBECONFIG") != path {
		t.Fatal("KUBECONFIG saiu do isolamento")
	}
}

func isolatedEnvtest(t *testing.T, name, assets string) *rest.Config {
	t.Helper()
	useExisting := false
	environment := &envtest.Environment{UseExistingCluster: &useExisting, BinaryAssetsDirectory: assets}
	environment.ControlPlane.GetAPIServer().Configure().Set("disable-admission-plugins", "ServiceAccount")
	admin, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Errorf("parar envtest %s: %v", name, err)
		}
	})
	client, err := kubernetes.NewForConfig(admin)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "integration"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{"app": name}
	spec := corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "fixture.invalid/never-run:local"}}}
	pod, err := client.CoreV1().Pods("integration").Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-" + name, Labels: labels}, Spec: spec,
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AppsV1().Deployments("integration").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "deployment-" + name},
		Spec: appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: spec}},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Events("integration").Create(ctx, &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "event-" + name},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "integration", Name: pod.Name, UID: pod.UID},
		Reason:         "Fixture", Message: "fixture-" + name, Type: "Normal", Count: 1,
		FirstTimestamp: metav1.Now(), LastTimestamp: metav1.Now(),
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RbacV1().Roles("integration").Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "reader"},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods", "events"}, Verbs: []string{"get", "list", "watch"}},
			{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "list", "watch"}},
		},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RbacV1().RoleBindings("integration").Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "reader"},
		Subjects:   []rbacv1.Subject{{Kind: "User", Name: "reader-" + name, APIGroup: rbacv1.GroupName}},
		RoleRef:    rbacv1.RoleRef{Kind: "Role", Name: "reader", APIGroup: rbacv1.GroupName},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	user, err := environment.AddUser(envtest.User{Name: "reader-" + name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return user.Config()
}

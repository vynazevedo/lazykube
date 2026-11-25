//go:build integration

package kube_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/kube"
)

type actionEnv struct {
	client kubernetes.Interface
	exec   *guard.Executor
	audit  *memoryAudit
	ctx    context.Context
}

type memoryAudit struct {
	events []guard.AuditEvent
	fail   string
}

func (a *memoryAudit) Record(e guard.AuditEvent) error {
	if a.fail == e.Phase {
		return guard.AuditFailed
	}
	a.events = append(a.events, e)
	return nil
}

func newActionEnv(t *testing.T) actionEnv {
	t.Helper()
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		t.Fatal("execute scripts/test-integration.sh para fornecer os binarios locais")
	}
	for name, env := range map[string]string{"kube-apiserver": "TEST_ASSET_KUBE_APISERVER", "etcd": "TEST_ASSET_ETCD", "kubectl": "TEST_ASSET_KUBECTL"} {
		path, err := filepath.Abs(filepath.Join(assets, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(env, path)
	}
	t.Setenv("KUBECONFIG", os.DevNull)
	useExisting := false
	environment := &envtest.Environment{UseExistingCluster: &useExisting, BinaryAssetsDirectory: assets}
	environment.ControlPlane.GetAPIServer().Configure().Set("disable-admission-plugins", "ServiceAccount")
	cfg, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	auth, dc, err := kube.NewActionClients(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	if _, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "apps"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	audit := &memoryAudit{}
	session := guard.Session{Context: "envtest", Identity: "session-1", Generation: 1, Environment: guard.Environment{Valid: true}}
	exec, err := kube.NewActionExecutor(ctx, session, true, auth, dc, audit, guard.ClockFunc(time.Now), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(exec.Close)
	return actionEnv{client: client, exec: exec, audit: audit, ctx: ctx}
}

func actionPodSpec() corev1.PodSpec {
	return corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "fixture.invalid/never-run:local"}}}
}

func (e actionEnv) deployment(t *testing.T, name string) (*appsv1.Deployment, guard.Target) {
	t.Helper()
	labels := map[string]string{"app": name}
	replicas := int32(1)
	d, err := e.client.AppsV1().Deployments("apps").Create(e.ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: appsv1.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: labels}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: map[string]string{"keep": "me"}}, Spec: actionPodSpec()}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return d, guard.Target{Group: "apps", Version: "v1", Resource: "deployments", Namespace: "apps", Name: name, UID: string(d.UID), ResourceVersion: d.ResourceVersion}
}

func (e actionEnv) run(t *testing.T, action guard.Action, target guard.Target, params guard.Parameters) {
	t.Helper()
	obs, err := e.exec.Observe(e.ctx, action, target)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.exec.Prepare(e.ctx, guard.Request{Action: action, Target: obs.Target, Parameters: params})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.exec.Execute(e.ctx, p.ID, guard.Confirmation{Approved: true, Resource: target.Name})
	if err != nil || r.Outcome != guard.Succeeded {
		t.Fatal(r, err)
	}
}

func (e actionEnv) stale(t *testing.T, action guard.Action, target guard.Target, params guard.Parameters, change func()) {
	t.Helper()
	p, err := e.exec.Prepare(e.ctx, guard.Request{Action: action, Target: target, Parameters: params})
	if err != nil {
		t.Fatal(err)
	}
	change()
	if r, err := e.exec.Execute(e.ctx, p.ID, guard.Confirmation{Approved: true, Resource: target.Name}); !errors.Is(err, guard.Stale) || r.Outcome != guard.Stale {
		t.Fatal(r, err)
	}
}

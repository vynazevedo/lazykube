//go:build integration

package kube_test

import (
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func TestEnvtestActionScale(t *testing.T) {
	e := newActionEnv(t)
	_, target := e.deployment(t, "api")
	e.run(t, guard.Scale, target, guard.Parameters{Replicas: 4})
	d, err := e.client.AppsV1().Deployments("apps").Get(e.ctx, "api", metav1.GetOptions{})
	if err != nil || *d.Spec.Replicas != 4 {
		t.Fatal(err, d.Spec.Replicas)
	}
	target.ResourceVersion = d.ResourceVersion
	if _, err = e.exec.Prepare(e.ctx, guard.Request{Action: guard.Scale, Target: target, Parameters: guard.Parameters{Replicas: 4}}); !errors.Is(err, guard.Unchanged) {
		t.Fatal(err)
	}
	e.stale(t, guard.Scale, target, guard.Parameters{Replicas: 2}, func() {
		d.Labels = map[string]string{"changed": "true"}
		if _, err := e.client.AppsV1().Deployments("apps").Update(e.ctx, d, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	if d, err = e.client.AppsV1().Deployments("apps").Get(e.ctx, "api", metav1.GetOptions{}); err != nil || *d.Spec.Replicas != 4 {
		t.Fatal("dry-run e conflito nao podem persistir", err)
	}
	target.ResourceVersion = d.ResourceVersion
	p, err := e.exec.Prepare(e.ctx, guard.Request{Action: guard.Scale, Target: target, Parameters: guard.Parameters{Replicas: 5}})
	if err != nil {
		t.Fatal(err)
	}
	e.audit.fail = "intent"
	if r, execErr := e.exec.Execute(e.ctx, p.ID, guard.Confirmation{Approved: true}); !errors.Is(execErr, guard.AuditFailed) || r.Outcome != "" {
		t.Fatal(r, execErr)
	}
	if d, err = e.client.AppsV1().Deployments("apps").Get(e.ctx, "api", metav1.GetOptions{}); err != nil || *d.Spec.Replicas != 4 {
		t.Fatal("audit falhando nao pode enviar", err)
	}
}

func TestEnvtestActionRestart(t *testing.T) {
	e := newActionEnv(t)
	_, target := e.deployment(t, "api")
	e.run(t, guard.Restart, target, guard.Parameters{})
	d, err := e.client.AppsV1().Deployments("apps").Get(e.ctx, "api", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	annotations := d.Spec.Template.Annotations
	if annotations["kubectl.kubernetes.io/restartedAt"] == "" || annotations["keep"] != "me" || len(annotations) != 2 || len(d.Annotations) != 0 {
		t.Fatal(annotations, d.Annotations)
	}
	stamped := annotations["kubectl.kubernetes.io/restartedAt"]
	target.ResourceVersion = d.ResourceVersion
	e.stale(t, guard.Restart, target, guard.Parameters{}, func() {
		d.Labels = map[string]string{"changed": "true"}
		if _, err := e.client.AppsV1().Deployments("apps").Update(e.ctx, d, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	if d, err = e.client.AppsV1().Deployments("apps").Get(e.ctx, "api", metav1.GetOptions{}); err != nil || d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] != stamped {
		t.Fatal(err)
	}
}

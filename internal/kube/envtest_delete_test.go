//go:build integration

package kube_test

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func TestEnvtestActionDelete(t *testing.T) {
	e := newActionEnv(t)
	params := guard.Parameters{Propagation: "Background", GraceSeconds: 30}
	pods := e.client.CoreV1().Pods("apps")
	create := func() guard.Target {
		p, err := pods.Create(e.ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker"}, Spec: actionPodSpec()}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return guard.Target{Version: "v1", Resource: "pods", Namespace: "apps", Name: "worker", UID: string(p.UID), ResourceVersion: p.ResourceVersion}
	}
	remove := func() {
		grace := int64(0)
		if err := pods.Delete(e.ctx, "worker", metav1.DeleteOptions{GracePeriodSeconds: &grace}); err != nil {
			t.Fatal(err)
		}
	}
	target := create()
	e.stale(t, guard.Delete, target, params, func() { remove(); create() })
	p, err := pods.Get(e.ctx, "worker", metav1.GetOptions{})
	if err != nil || string(p.UID) == target.UID || p.DeletionTimestamp != nil {
		t.Fatal("o pod recriado com o mesmo nome deve permanecer", err)
	}
	if _, err = e.exec.Observe(e.ctx, guard.Delete, target); !errors.Is(err, guard.Stale) {
		t.Fatal(err)
	}
	remove()
	e.run(t, guard.Delete, create(), params)
	if _, err = pods.Get(e.ctx, "worker", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("pod sem node deve ser removido na hora", err)
	}
	_, deployment := e.deployment(t, "api")
	e.run(t, guard.Delete, deployment, params)
	if _, err = e.client.AppsV1().Deployments("apps").Get(e.ctx, "api", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
}

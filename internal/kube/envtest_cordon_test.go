//go:build integration

package kube_test

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func TestEnvtestActionCordon(t *testing.T) {
	e := newActionEnv(t)
	nodes := e.client.CoreV1().Nodes()
	taint := corev1.Taint{Key: "dedicated", Value: "x", Effect: corev1.TaintEffectNoSchedule}
	n, err := nodes.Create(e.ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Labels: map[string]string{"keep": "me"}}, Spec: corev1.NodeSpec{PodCIDR: "10.0.0.0/24", Taints: []corev1.Taint{taint}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target := func() guard.Target {
		current, getErr := nodes.Get(e.ctx, "node-a", metav1.GetOptions{})
		if getErr != nil {
			t.Fatal(getErr)
		}
		return guard.Target{Version: "v1", Resource: "nodes", Name: "node-a", UID: string(current.UID), ResourceVersion: current.ResourceVersion}
	}
	if _, err = e.exec.Prepare(e.ctx, guard.Request{Action: guard.Uncordon, Target: target()}); !errors.Is(err, guard.Unchanged) {
		t.Fatal(err)
	}
	e.stale(t, guard.Cordon, target(), guard.Parameters{}, func() {
		n.Labels["changed"] = "true"
		if _, err := nodes.Update(e.ctx, n, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	for _, tc := range []struct {
		action guard.Action
		want   bool
	}{{guard.Cordon, true}, {guard.Uncordon, false}} {
		e.run(t, tc.action, target(), guard.Parameters{})
		current, getErr := nodes.Get(e.ctx, "node-a", metav1.GetOptions{})
		if getErr != nil || current.Spec.Unschedulable != tc.want || current.Labels["keep"] != "me" || current.Spec.PodCIDR != "10.0.0.0/24" {
			t.Fatal(getErr, current.Spec)
		}
		found := false
		for _, x := range current.Spec.Taints {
			found = found || x.Key == "dedicated"
		}
		if !found {
			t.Fatal("cordon nao pode alterar taints existentes", current.Spec.Taints)
		}
	}
	old := target()
	if err = nodes.Delete(e.ctx, "node-a", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = nodes.Create(e.ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.exec.Prepare(e.ctx, guard.Request{Action: guard.Cordon, Target: old}); !errors.Is(err, guard.Stale) {
		t.Fatal(err)
	}
}

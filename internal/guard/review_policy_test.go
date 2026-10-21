package guard

import (
	"context"
	"errors"
	"testing"
)

func TestReviewRejectsUnapprovedResourcesBeforeIO(t *testing.T) {
	for _, tc := range []struct {
		action          Action
		group, resource string
	}{
		{Delete, "", "secrets"}, {Delete, "", "persistentvolumes"}, {Edit, "rbac.authorization.k8s.io", "clusterrolebindings"}, {Edit, "admissionregistration.k8s.io", "mutatingwebhookconfigurations"}, {Edit, "", "persistentvolumes"}, {Edit, "example.test", "widgets"}, {Delete, "example.test", "pods"}, {Edit, "example.test", "configmaps"}, {Scale, "apps", "replicasets"},
	} {
		t.Run(tc.group+"/"+tc.resource, func(t *testing.T) {
			e, f, _ := reviewExecutor(t)
			r := guardRequest()
			r.Action = tc.action
			r.Target.Group = tc.group
			r.Target.Resource = tc.resource
			r.Parameters.Document = []byte(`{}`)
			_, err := e.Prepare(context.Background(), r)
			if !errors.Is(err, Unsupported) || len(f.sends) != 0 || len(f.accesses) != 0 {
				t.Fatalf("unsupported resource reached IO: %v %v", err, f.sends)
			}
		})
	}
}
func TestReviewProtectionRequiresExactConfiguration(t *testing.T) {
	for _, name := range []string{"dev-product", "prod", "production"} {
		if DefaultEnvironment(name).Protected {
			t.Fatal(name)
		}
	}
}
func TestReviewAllowlistMatchesActionAndGVR(t *testing.T) {
	for _, tc := range []struct {
		group, resource string
		actions         []Action
	}{
		{"", "pods", []Action{Delete, Edit, Exec, PortForward}},
		{"", "configmaps", []Action{Edit}}, {"", "services", []Action{Edit}},
		{"apps", "deployments", []Action{Delete, Edit, Scale, Restart}},
		{"apps", "statefulsets", []Action{Delete, Edit, Scale, Restart}},
		{"apps", "daemonsets", []Action{Delete, Edit, Restart}},
		{"batch", "jobs", []Action{Delete, Edit}}, {"batch", "cronjobs", []Action{Delete, Edit}},
	} {
		target := Target{Group: tc.group, Version: "v1", Resource: tc.resource}
		for action := ActionUnknown; action <= PortForward; action++ {
			want := false
			for _, allowed := range tc.actions {
				want = want || allowed == action
			}
			if SupportsAction(action, target) != want {
				t.Fatal(action, target)
			}
		}
		target.Version = "v1beta1"
		for _, action := range tc.actions {
			if SupportsAction(action, target) {
				t.Fatal("unsupported version", target)
			}
		}
	}
}

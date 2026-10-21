package guard

import (
	"errors"
	"testing"
)

func guardSession() Session {
	return Session{Context: "dev", Namespace: "apps", Identity: "identity", Generation: 1, Environment: Environment{Valid: true}}
}
func guardRequest() Request {
	return Request{Action: Delete, Target: Target{Version: "v1", Resource: "pods", Namespace: "apps", Name: "pod", UID: "uid", ResourceVersion: "1"}, Parameters: Parameters{Propagation: "Background"}}
}
func TestPolicyCannotOverrideContext(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes bool
		env    Environment
		want   Reason
	}{
		{"default", false, Environment{Valid: true}, ReadOnly},
		{"context", true, Environment{Valid: true, ReadOnly: true}, ReadOnly},
		{"protected", true, Environment{Valid: true, Protected: true}, ReadOnly},
		{"invalid", true, Environment{}, InvalidConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := guardSession()
			s.Environment = tc.env
			_, err := NewPolicy(tc.writes).Evaluate(s, guardRequest())
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v", err)
			}
		})
	}
}
func TestPolicySupportedRequests(t *testing.T) {
	for _, action := range []Action{Delete, Scale, Restart, Edit, Exec, PortForward} {
		r := guardRequest()
		r.Action = action
		if action == Scale || action == Restart {
			r.Target.Group = "apps"
			r.Target.Resource = "deployments"
		}
		r.Parameters.Document = []byte(`{}`)
		r.Parameters.Command = []string{"sh"}
		r.Parameters.Container = "app"
		r.Parameters.Address = "127.0.0.1"
		r.Parameters.LocalPort = 8080
		r.Parameters.RemotePort = 80
		d, err := NewPolicy(true).Evaluate(guardSession(), r)
		if err != nil {
			t.Fatal(action, err)
		}
		if d.DryRun != (action != Exec && action != PortForward) {
			t.Fatal(action, d)
		}
		access := Requirements(r)
		if len(access) != 2 || access[0].Verb != "get" {
			t.Fatal(access)
		}
	}
}
func TestPolicyRejectsUnsupportedTargets(t *testing.T) {
	for _, resource := range []string{"nodes", "nodes/proxy"} {
		r := guardRequest()
		r.Target.Resource = resource
		if _, err := NewPolicy(true).Evaluate(guardSession(), r); err == nil {
			t.Fatal(resource)
		}
	}
	r := guardRequest()
	r.Action = ActionUnknown
	if _, err := NewPolicy(true).Evaluate(guardSession(), r); err == nil {
		t.Fatal("unknown action")
	}
	r.Action = Edit
	r.Target.Resource = "secrets"
	r.Parameters.Document = []byte(`{}`)
	if _, err := NewPolicy(true).Evaluate(guardSession(), r); err == nil {
		t.Fatal("secret edit")
	}
	r = guardRequest()
	r.Action = PortForward
	r.Parameters.Address = "0.0.0.0"
	r.Parameters.LocalPort = 8080
	r.Parameters.RemotePort = 80
	if _, err := NewPolicy(true).Evaluate(guardSession(), r); err == nil {
		t.Fatal("public binding")
	}
}

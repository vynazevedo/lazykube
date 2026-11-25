package guard

import (
	"context"
	"errors"
	"testing"
)

func nodeRequest(a Action) Request {
	return Request{Action: a, Target: Target{Version: "v1", Resource: "nodes", Name: "node-a", UID: "uid", ResourceVersion: "9"}}
}

func TestCordonPolicyAndExactAccess(t *testing.T) {
	for _, action := range []Action{Cordon, Uncordon} {
		for _, ns := range []string{"", "apps"} {
			s := guardSession()
			s.Namespace = ns
			d, err := NewPolicy(true).Evaluate(s, nodeRequest(action))
			if err != nil || !d.HighRisk || !d.DryRun {
				t.Fatal(d, err)
			}
		}
		access := Requirements(nodeRequest(action))
		want := []Access{{Version: "v1", Resource: "nodes", Name: "node-a", Verb: "get"}, {Version: "v1", Resource: "nodes", Name: "node-a", Verb: "patch"}}
		if len(access) != 2 || access[0] != want[0] || access[1] != want[1] {
			t.Fatal(access)
		}
	}
}

func TestNodesAcceptOnlyCordonActions(t *testing.T) {
	for action := ActionUnknown; action <= Uncordon+1; action++ {
		want := action == Cordon || action == Uncordon
		if SupportsAction(action, Target{Version: "v1", Resource: "nodes"}) != want {
			t.Fatal(action)
		}
	}
	for _, target := range []Target{{Group: "metrics.k8s.io", Version: "v1", Resource: "nodes"}, {Version: "v1beta1", Resource: "nodes"}, {Version: "v1", Resource: "nodes/proxy"}, {Version: "v1", Resource: "pods"}, {Group: "apps", Version: "v1", Resource: "deployments"}} {
		if SupportsAction(Cordon, target) || SupportsAction(Uncordon, target) {
			t.Fatal(target)
		}
	}
	r := nodeRequest(Cordon)
	r.Target.Namespace = "apps"
	if _, err := NewPolicy(true).Evaluate(guardSession(), r); !errors.Is(err, Invalid) {
		t.Fatal(err)
	}
}

func TestCordonRequiresNameAndFreshAccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		typed  string
		revoke bool
		want   Reason
	}{{"approved", "node-a", false, ""}, {"name", "node-b", false, ConfirmationRequired}, {"revoked", "node-a", true, Denied}} {
		t.Run(tc.name, func(t *testing.T) {
			e, f, _ := guardExecutor(t)
			p, err := e.Prepare(context.Background(), nodeRequest(Cordon))
			if err != nil {
				t.Fatal(err)
			}
			f.allowed = !tc.revoke
			_, err = e.Execute(context.Background(), p.ID, Confirmation{Approved: true, Resource: tc.typed})
			if tc.want == "" && (err != nil || len(f.sends) != 2 || len(f.accesses) != 4) || tc.want != "" && (!errors.Is(err, tc.want) || len(f.sends) != 1) {
				t.Fatal(err, f.sends, f.accesses)
			}
		})
	}
}

func TestUnchangedTargetIsAuditedAndNeverSent(t *testing.T) {
	e, h, _ := reviewExecutor(t)
	h.validateHook = func(context.Context) error { return Unchanged }
	if _, err := e.Prepare(context.Background(), nodeRequest(Cordon)); !errors.Is(err, Unchanged) || len(h.sends) != 0 {
		t.Fatal(err, h.sends)
	}
	if len(h.events) != 1 || h.events[0].Outcome != Unchanged {
		t.Fatal(h.events)
	}
}

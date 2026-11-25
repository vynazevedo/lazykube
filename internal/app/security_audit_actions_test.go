package app

import (
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func TestAuditNamesEveryAction(t *testing.T) {
	want := map[guard.Action]string{
		guard.ActionUnknown: "desconhecida", 1: "delete", 2: "scale", 3: "restart", 4: "edit", 5: "exec", 6: "port-forward", 7: "cordon", 8: "uncordon",
		guard.Uncordon + 1: "desconhecida",
	}
	for action, name := range want {
		if got := auditAction(action); got != name {
			t.Fatalf("%d: %q, want %q", action, got, name)
		}
	}
	if guard.Cordon != 7 || guard.Uncordon != 8 {
		t.Fatal("the recorded numbers are part of the audit schema")
	}
}

func TestAuditRowShowsNodeTarget(t *testing.T) {
	got := auditTarget(guard.AuditEvent{Action: guard.Cordon, Resource: "nodes", Name: "node-a", UID: "u"})
	if got != "nodes/node-a uid u" {
		t.Fatalf("%q", got)
	}
}

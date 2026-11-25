//go:build !linux

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type unverifiedAuditSource struct {
	*data.Demo
	path string
}

func (s unverifiedAuditSource) Snapshot() data.Snapshot {
	snap := s.Demo.Snapshot()
	snap.Synthetic = false
	return snap
}

func (s unverifiedAuditSource) AuditPath() string {
	return s.path
}

func TestSecurityAuditExplainsUnverifiedPlatform(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "audit", "audit.jsonl")
	for _, size := range [][2]int{{160, 48}, {130, 40}, {80, 24}} {
		m := press(t, nodeModel(t, unverifiedAuditSource{Demo: data.NewDemo(), path: path}, size[0], size[1]), "5", "[")
		if m.sec.Tab != ui.SecTabAudit || !m.sec.audit.loading {
			t.Fatal("opening Audit requests the first page")
		}
		page, err := guard.ReadAudit(t.Context(), path, m.sec.audit.next, guard.AuditPageSize)
		m = deliver(t, m, auditPageMsg{session: m.session.Generation, seq: m.sec.audit.seq, page: page, err: err})
		if want := string(guard.RefusedPlatform) + "; ações bloqueadas"; m.sec.audit.state != ui.AuditUnavailable || m.sec.audit.reason != want || len(m.auditRows()) != 0 {
			t.Fatalf("state %q reason %q", m.sec.audit.state, m.sec.audit.reason)
		}
		plain := m.Render().Plain()
		l := m.layout.SecList
		var list strings.Builder
		for _, line := range strings.Split(plain, "\n")[l.Y+1 : l.Y+l.H-1] {
			list.WriteString(strings.Trim(string([]rune(line)[l.X:l.X+l.W]), "│ ") + " ")
		}
		if text := strings.Join(strings.Fields(list.String()), " "); !strings.Contains(text, "Motivo: "+string(guard.RefusedPlatform)+"; ações bloqueadas") {
			t.Fatalf("%v the list says why: %q", size, text)
		}
		if strings.Contains(plain, base) {
			t.Fatal("the reason never shows the raw path")
		}
	}
	if entries, err := os.ReadDir(base); err != nil || len(entries) != 0 {
		t.Fatalf("reading creates nothing: %v %v", entries, err)
	}
}

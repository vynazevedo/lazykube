package app

import (
	"strings"
	"testing"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/ui"
)

func TestSecurityTabAndNamespaceGolden(t *testing.T) {
	m := demoModel(t, 160, 48)
	m = press(t, m, "S")
	if m.tab != ui.TabSecurity || m.securityNamespace {
		t.Fatal("S must open pod Security")
	}
	golden(t, "tab_security.txt", m.Render().Plain())
	m = press(t, m, "S")
	if !m.securityNamespace || !strings.Contains(m.securityView.Scope, "Namespace: bilibili-raffle") {
		t.Fatal("S must switch to namespace posture")
	}
	golden(t, "tab_security_namespace.txt", m.Render().Plain())
	m.setLayout(130, 36)
	m.refresh()
	m = press(t, m, "end")
	if m.scroll != m.maxScroll() || m.scroll == 0 {
		t.Fatal("security scroll did not reach last evidence")
	}
	m = press(t, m, "S")
	if m.securityNamespace || m.scroll != 0 {
		t.Fatal("scope change must reset scroll")
	}
}

type countedSecuritySource struct {
	*data.Demo
	calls int
}

func (s *countedSecuritySource) Security() data.SecuritySnapshot { s.calls++; return s.Demo.Security() }

func TestSecurityNotFetchedOrBuiltPerFrame(t *testing.T) {
	s := &countedSecuritySource{Demo: data.NewDemo()}
	m := New(Options{Source: s, NoAnim: true})
	m.setLayout(160, 48)
	m.refresh()
	rows := m.securityView.Rows
	for range 20 {
		m.refresh()
		m.Render()
	}
	if s.calls != 1 || &rows[0] != &m.securityView.Rows[0] {
		t.Fatal("posture recomputed per refresh/frame")
	}
	m.reload()
	if s.calls != 2 {
		t.Fatal("data reload did not refresh posture")
	}
}

func TestSecurityEmptyNamespace(t *testing.T) {
	m := demoModel(t, 130, 40)
	m.nsFilter = "default"
	m.syncSelection()
	m = press(t, m, "S")
	out := m.Render().Plain()
	if !strings.Contains(out, "Namespace: default") || !strings.Contains(out, "No findings does not mean safe") || !strings.Contains(m.securityView.Coverage, "0/0") {
		t.Fatalf("empty scope lost: %s", out)
	}
	golden(t, "tab_security_empty.txt", out)
}

type sourceWithoutSecurity struct{ data.Source }

func TestSourceWithoutPosture(t *testing.T) {
	m := New(Options{Source: sourceWithoutSecurity{Source: data.NewDemo()}, NoAnim: true})
	m.setLayout(160, 48)
	m.refresh()
	if !strings.Contains(m.securityView.Coverage, "unsupported") || !strings.Contains(strings.Join(m.securityView.Rows, " "), "source does not provide posture") {
		t.Fatal("missing capability not visible")
	}
}

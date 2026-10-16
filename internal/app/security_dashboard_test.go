package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func TestSecurityDashboardAlertGoldens(t *testing.T) {
	for _, size := range [][2]int{{130, 40}, {160, 48}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := demoModel(t, size[0], size[1])
			if len(m.securitySession.Alerts) != 0 {
				t.Fatal("baseline alert")
			}
			m.clock = anim.Fixed(240, goldenPhase, 30)
			m.refresh()
			out := m.Render().Plain()
			if !strings.Contains(out, "! 1 high new") || !strings.Contains(out, "Security 1") || m.tab == ui.TabSecurity {
				t.Fatal("alert not visible outside Security")
			}
			golden(t, fmt.Sprintf("security_alert_header_%d.txt", size[0]), out)
			m = press(t, m, "S")
			golden(t, fmt.Sprintf("security_dashboard_%d.txt", size[0]), m.Render().Plain())
			m = press(t, m, "end")
			golden(t, fmt.Sprintf("security_dashboard_detail_%d.txt", size[0]), m.Render().Plain())
			m = press(t, m, "A")
			if m.scroll > m.maxScroll() {
				t.Fatal("ack left scroll beyond available rows")
			}
			if len(m.securitySession.Alerts) != 0 || strings.Contains(m.Render().Plain(), "Security 1") {
				t.Fatal("ack did not clear badges")
			}
			m.reload()
			if len(m.securitySession.Alerts) != 0 {
				t.Fatal("ack not retained")
			}
		})
	}
}

func TestSecurityDashboardScopeCacheAndHelp(t *testing.T) {
	m := demoModel(t, 160, 48)
	m = press(t, m, "S")
	if !strings.Contains(strings.Join(m.securityView.Rows, " "), "POSTURE | Cluster") {
		t.Fatal("default dashboard scope not cluster")
	}
	before := m.securitySession.Dashboard.Cluster.Rules
	m.nsFilter = "lgo"
	m.refreshSecurity()
	if !strings.Contains(strings.Join(m.securityView.Rows, " "), "POSTURE | Namespace: lgo") {
		t.Fatal("filter scope not reflected")
	}
	if &before[0] != &m.securitySession.Dashboard.Cluster.Rules[0] {
		t.Fatal("filter recalculated aggregates")
	}
	found := false
	for _, h := range ui.FullHelp() {
		if h.Key == "A" {
			found = true
		}
	}
	if !found {
		t.Fatal("ack missing from help")
	}
	keys := DefaultKeyMap()
	for _, k := range keys.SecurityAck.Keys() {
		if k != "A" {
			t.Fatal("unexpected ack key")
		}
	}
}

func TestSecurityDashboardNoRecalculationOnDataMessages(t *testing.T) {
	s := &revisionSecuritySource{Demo: data.NewDemo(), revision: 1}
	m := New(Options{Source: s, NoAnim: true})
	before := m.securitySession.Dashboard.Cluster.Rules
	for range 10 {
		m.loadSecurity()
	}
	if len(m.securitySession.History[""]) != 1 || &before[0] != &m.securitySession.Dashboard.Cluster.Rules[0] {
		t.Fatal("unchanged revision rebuilt aggregate/history")
	}
	s.revision++
	m.loadSecurity()
	if len(m.securitySession.History[""]) != 2 || &before[0] == &m.securitySession.Dashboard.Cluster.Rules[0] {
		t.Fatal("changed revision not rebuilt")
	}
}

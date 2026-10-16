package ui

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
)

func TestSecurityDashboardCoverage(t *testing.T) {
	for _, state := range []data.CoverageState{data.CoveragePartial, data.CoverageDenied, data.CoverageUnavailable, data.CoverageStale} {
		snapshot := data.NewDemo().Security()
		snapshot.Coverage = data.Coverage{State: state, Evaluated: 40, Total: 55, Reason: "test coverage reason"}
		var session data.SecuritySession
		session.Update(snapshot)
		view := DashboardSecurityView(SecurityView{}, session, "", "", 45)
		text := strings.Join(view.Rows, " ")
		if state == data.CoveragePartial {
			if !strings.Contains(text, "partial | 13/55 pods") {
				t.Fatalf("partial not visible: %s", text)
			}
		} else if !strings.Contains(text, "not rated | test coverage reason") || strings.Contains(text, "/100 (") {
			t.Fatalf("unavailable score not suppressed: %s", text)
		}
		for _, row := range view.Rows {
			if draw.Width(row) > 45 {
				t.Fatal("row exceeds width")
			}
		}
	}
}

func TestSecurityDashboardVisualGolden(t *testing.T) {
	demo := data.NewDemo()
	var session data.SecuritySession
	session.Update(demo.Security())
	demo.AdvanceSecurity(240)
	session.Update(demo.Security())
	for _, width := range []int{45, 65} {
		detail := BuildSecurityView(demo.Security(), selectedPod, "bilibili-raffle", false, width-8)
		detail.Synthetic = demo.Snapshot().Synthetic
		view := DashboardSecurityView(detail, session, "", selectedPod, width-8)
		name := "security_dashboard_narrow"
		if width == 65 {
			name = "security_dashboard_wide"
		}
		check(t, name, canvas(width, 48, func(r draw.Region) {
			DetailsPanel(r, DetailsState{Tab: TabSecurity, HoverTab: -1, Security: view, SecurityAlerts: len(session.Alerts)}, fixedCtx())
		}))
	}
}

func TestSecurityBadgeHitTarget(t *testing.T) {
	spans := TabSpans(123)
	last := spans[len(spans)-1]
	if tab, ok := TabAt(last.X+last.W-1, 123); !ok || tab != TabSecurity {
		t.Fatal("badge is not clickable")
	}
	if last.W != len(" Security 123 ") {
		t.Fatal("badge width mismatch")
	}
}

func TestSecurityDashboardAlertOverflow(t *testing.T) {
	var session data.SecuritySession
	session.Update(data.NewDemo().Security())
	for i := range 8 {
		session.Alerts = append(session.Alerts, data.Finding{Rule: strings.Repeat("x", i+1), Target: data.ResourceRef{Name: "pod", Namespace: "apps"}})
	}
	view := DashboardSecurityView(SecurityView{}, session, "", "", 60)
	text := strings.Join(view.Rows, " ")
	if !strings.Contains(text, "3 more alerts below pod details") || !strings.Contains(text, "MORE ALERTS") || !strings.Contains(text, "xxxxxxxx") {
		t.Fatal("overflow alerts inaccessible")
	}
}

func TestSecurityDashboardSingularFinding(t *testing.T) {
	var session data.SecuritySession
	snapshot := data.NewDemo().Security()
	for key, p := range snapshot.Pods {
		if p.Target.Namespace == "ys-odoo" {
			p.Findings = nil
			if p.Target.Name == "odoo-7575bf6d8-9wjvk" {
				p.Findings = []data.Finding{{Rule: "serviceAccountToken", Severity: data.SeverityInfo, Result: data.ResultFinding}}
			}
			snapshot.Pods[key] = p
		}
	}
	session.Update(snapshot)
	view := DashboardSecurityView(SecurityView{}, session, "ys-odoo", "", 80)
	text := strings.Join(view.Rows, " ")
	if strings.Contains(text, "1 findings") || !strings.Contains(text, "1 finding") {
		t.Fatalf("singular missing: %s", text)
	}
}

func TestSecurityPluralCounts(t *testing.T) {
	for _, tc := range []struct {
		n          int
		word, want string
	}{{0, "finding", "findings"}, {1, "finding", "finding"}, {2, "finding", "findings"}, {1, "alert", "alert"}, {2, "alert", "alerts"}, {1, "pod", "pod"}, {2, "pod", "pods"}} {
		if got := securityPlural(tc.n, tc.word); got != tc.want {
			t.Fatalf("plural %d %s = %s", tc.n, tc.word, got)
		}
	}
}

func TestSecurityDashboardReservesAggregateBudget(t *testing.T) {
	snapshot := data.NewDemo().Security()
	p := snapshot.Pods[selectedPod]
	p.Findings = make([]data.Finding, 1000)
	for i := range p.Findings {
		p.Findings[i] = data.Finding{Rule: "privileged", Severity: data.SeverityHigh, Result: data.ResultFinding, Evidence: "selected evidence", Target: p.Target}
	}
	snapshot.Pods[selectedPod] = p
	var session data.SecuritySession
	session.Update(snapshot)
	session.Alerts = []data.Finding{{Rule: "new-high", Target: p.Target}}
	detail := BuildSecurityView(snapshot, selectedPod, p.Target.Namespace, true, 80)
	if len(detail.Rows) != maxSecurityRows {
		t.Fatalf("fixture has %d rows", len(detail.Rows))
	}
	view := DashboardSecurityView(detail, session, p.Target.Namespace, selectedPod, 80)
	text := strings.Join(view.Rows, "\n")
	for _, want := range []string{"POSTURE", "median", "ALERTS", "new-high", "TOP RULES", "NAMESPACES"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s", want)
		}
	}
	if len(view.Rows) > maxSecurityRows {
		t.Fatal("row budget exceeded")
	}
}

func TestSecurityDashboardOtherPodsAfterSummary(t *testing.T) {
	snapshot := data.NewDemo().Security()
	selected := snapshot.Pods[selectedPod]
	other := selected
	other.Target.Name = "other-pod"
	other.Findings = make([]data.Finding, 1000)
	for i := range other.Findings {
		other.Findings[i] = data.Finding{Rule: "privileged", Severity: data.SeverityHigh, Result: data.ResultFinding, Target: other.Target}
	}
	snapshot.Pods[other.Target.Namespace+"/"+other.Target.Name] = other
	var session data.SecuritySession
	session.Update(snapshot)
	detail := BuildSecurityView(snapshot, selectedPod, selected.Target.Namespace, true, 80)
	if len(detail.Rows) != maxSecurityRows {
		t.Fatal("fixture must exhaust budget")
	}
	view := DashboardSecurityView(detail, session, selected.Target.Namespace, selectedPod, 80)
	text := strings.Join(view.Rows, "\n")
	selectedAt := strings.Index(text, "Pod "+selectedPod+" |")
	summaryAt := strings.Index(text, "POSTURE")
	otherAt := strings.Index(text, "Pod "+other.Target.Namespace+"/other-pod |")
	if selectedAt < 0 || summaryAt <= selectedAt || otherAt <= summaryAt {
		t.Fatal("pod evidence ordering incorrect")
	}
}

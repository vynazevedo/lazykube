package ui

import (
	"strings"
	"testing"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/draw"
)

func TestSecurityCoverageViews(t *testing.T) {
	for _, state := range []data.CoverageState{data.CoverageCollecting, data.CoveragePartial, data.CoverageDenied, data.CoverageUnavailable, data.CoverageStale, data.CoverageUnsupported, data.CoverageDisabled} {
		s := data.SecuritySnapshot{Coverage: data.Coverage{State: state, Reason: "limited coverage"}}
		v := BuildSecurityView(s, "missing", "", false, 40)
		if !strings.Contains(v.Coverage, string(state)) || !strings.Contains(strings.Join(v.Rows, " "), "limited coverage") {
			t.Fatalf("coverage hidden: %+v", v)
		}
	}
	v := BuildSecurityView(data.NewDemo().Security(), "", "missing", true, 40)
	if !strings.Contains(strings.Join(v.Rows, " "), "namespace not observed") {
		t.Fatal("missing namespace is treated as empty")
	}
}

func TestSecurityRowsAndGoldens(t *testing.T) {
	d := data.NewDemo()
	v := BuildSecurityView(d.Security(), selectedPod, "bilibili-raffle", false, 57)
	text := strings.Join(v.Rows, "\n")
	for _, want := range []string{"privileged", "init-permissions", "Field:", "Evidence:", "Observed", "UID", "RV"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	for _, row := range v.Rows {
		if draw.Width(row) > 57 {
			t.Fatalf("row not wrapped: %s", row)
		}
	}
	check(t, "security_pod", canvas(65, 44, func(r draw.Region) {
		DetailsPanel(r, DetailsState{Tab: TabSecurity, HoverTab: -1, Security: v}, fixedCtx())
	}))
	v = BuildSecurityView(d.Security(), "", "bilibili-raffle", true, 57)
	check(t, "security_namespace", canvas(65, 44, func(r draw.Region) {
		DetailsPanel(r, DetailsState{Tab: TabSecurity, HoverTab: -1, Security: v}, fixedCtx())
	}))
	check(t, "deployments_denied", canvas(37, 16, func(r draw.Region) {
		DeploymentsPanel(r, DeploymentsState{Coverage: data.Coverage{State: data.CoverageDenied}}, fixedCtx())
	}))
}

func TestSecurityViewLimitsRows(t *testing.T) {
	p := data.PodPosture{Target: data.ResourceRef{Name: "many", Namespace: "apps"}, Coverage: data.Coverage{State: data.CoveragePartial, Reason: "finding limit reached"}, Findings: make([]data.Finding, maxSecurityRows)}
	s := data.SecuritySnapshot{Pods: map[string]data.PodPosture{"apps/many": p}}
	v := BuildSecurityView(s, "apps/many", "apps", false, 60)
	if len(v.Rows) != maxSecurityRows || !strings.Contains(v.Coverage, "Display truncated") || !strings.Contains(v.Rows[len(v.Rows)-1], "omitted") || !strings.Contains(strings.Join(v.Rows, " "), "finding limit reached") {
		t.Fatal("truncation is unbounded or hidden")
	}
}

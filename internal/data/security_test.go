package data

import (
	"reflect"
	"testing"
	"time"
)

func TestSecuritySummaryAndClone(t *testing.T) {
	at := time.Unix(1, 0)
	pods := map[string]PodPosture{
		"a/p": {Target: ResourceRef{Namespace: "a"}, Coverage: Coverage{State: CoveragePartial, Evaluated: 1, Total: 1}, Findings: []Finding{{Result: ResultFinding}, {Result: ResultUnknown}, {Result: ResultNotApplicable}}},
		"b/p": {Target: ResourceRef{Namespace: "b"}, Coverage: Coverage{State: CoverageComplete, Evaluated: 1, Total: 1}},
	}
	s := SummarizeSecurity(pods, []string{"empty"}, Coverage{State: CoverageComplete, ObservedAt: at})
	if s.Coverage.State != CoveragePartial || s.Coverage.Total != 2 || s.Coverage.Evaluated != 2 || s.Namespaces["a"].Findings != 1 || s.Namespaces["a"].Unknown != 1 || s.Namespaces["b"].Coverage.State != CoverageComplete || s.Namespaces["empty"].Coverage.Total != 0 {
		t.Fatalf("summary: %+v", s)
	}
	copy := s.Clone()
	copy.Pods["a/p"].Findings[0].Rule = "modified"
	delete(copy.Namespaces, "empty")
	if s.Pods["a/p"].Findings[0].Rule != "" || len(s.Namespaces) != 3 {
		t.Fatal("clone aliases state")
	}
	stale := SummarizeSecurity(pods, nil, Coverage{State: CoverageStale})
	if stale.Coverage.State != CoverageStale || stale.Namespaces["a"].Coverage.State != CoverageStale {
		t.Fatal("partial identity hid stale coverage")
	}
}

func TestDemoSecurityDeterministic(t *testing.T) {
	a, b := NewDemo().Security(), NewDemo().Security()
	if !reflect.DeepEqual(a, b) || a.Coverage.Evaluated != 13 {
		t.Fatal("demo must be deterministic and cover all pods")
	}
	seen := map[RuleResult]bool{}
	clean := false
	for _, p := range a.Pods {
		if len(p.Findings) == 0 {
			clean = true
		}
		for _, f := range p.Findings {
			seen[f.Result] = true
		}
	}
	if !clean || !seen[ResultFinding] || !seen[ResultUnknown] {
		t.Fatal("demo lacks varied findings")
	}
}

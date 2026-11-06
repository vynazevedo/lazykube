package data

import (
	"slices"
	"testing"
)

func mergeFixture() SecuritySnapshot {
	finding := func(rule string, severity Severity) Finding {
		return Finding{Rule: rule, Severity: severity, Result: ResultFinding}
	}
	pod := func(ns, name string, findings ...Finding) PodPosture {
		return PodPosture{Target: ResourceRef{Namespace: ns, Name: name, UID: ns + name}, Coverage: Coverage{State: CoverageComplete, Evaluated: 1, Total: 1}, Findings: findings}
	}
	s := SecuritySnapshot{Coverage: Coverage{State: CoverageComplete, Evaluated: 4, Total: 4}, Pods: map[string]PodPosture{}, Namespaces: map[string]NamespacePosture{}}
	for _, p := range []PodPosture{
		pod("a", "one", finding("privileged", SeverityHigh)),
		pod("a", "two"),
		pod("b", "three", finding("privileged", SeverityHigh), finding("no-limits", SeverityMedium)),
		pod("c", "four", finding("no-limits", SeverityMedium)),
	} {
		s.Pods[p.Target.Namespace+"/"+p.Target.Name] = p
	}
	for _, ns := range []string{"a", "b", "c"} {
		s.Namespaces[ns] = NamespacePosture{Coverage: Coverage{State: CoverageComplete, Evaluated: 1, Total: 1}}
	}
	return s
}

func TestMergeNamespacesOverEveryNamespaceMatchesCluster(t *testing.T) {
	d := AggregateSecurity(mergeFixture())
	got := d.MergeNamespaces([]string{"a", "b", "c"})
	c := d.Cluster
	if got.Distribution.Mean != c.Distribution.Mean || got.Distribution.Median != c.Distribution.Median || got.Distribution.WorstDecile != c.Distribution.WorstDecile || got.Distribution.Grades != c.Distribution.Grades {
		t.Fatalf("security distribution %+v, cluster %+v", got.Distribution, c.Distribution)
	}
	if got.HygieneDistribution.Mean != c.HygieneDistribution.Mean || got.Score.Value != c.Score.Value || got.Hygiene.Value != c.Hygiene.Value {
		t.Fatalf("scores differ: %+v cluster %+v", got.Score, c.Score)
	}
	if got.High != c.High || got.Medium != c.Medium || got.Info != c.Info || got.Unknown != c.Unknown {
		t.Fatalf("counts differ: %+v cluster %+v", got, c)
	}
	if !slices.Equal(got.Rules, c.Rules) {
		t.Fatalf("rules %v, cluster %v", got.Rules, c.Rules)
	}
}

func TestMergeNamespacesCountsOnlyTheSet(t *testing.T) {
	d := AggregateSecurity(mergeFixture())
	got := d.MergeNamespaces([]string{"a", "b"})
	if got.Distribution.Count != 3 || got.High != 2 || got.Medium != 1 {
		t.Fatalf("merged set: %+v", got)
	}
	if got.Score.Coverage.Evaluated != 3 {
		t.Fatalf("coverage counts the pods of the set: %+v", got.Score.Coverage)
	}
	if !d.Namespaces["a"].Score.Available {
		t.Fatal("merging never changes the aggregates of the session")
	}
}

func TestMergeNamespacesOfUnobservedSetIsNotRated(t *testing.T) {
	d := AggregateSecurity(mergeFixture())
	got := d.MergeNamespaces([]string{"missing", "other"})
	if got.Score.Available || got.Score.Coverage.State != CoverageUnavailable {
		t.Fatalf("an unobserved set is not rated: %+v", got.Score)
	}
}

func TestMergeNamespacesKeepsWeakestCoverage(t *testing.T) {
	s := mergeFixture()
	s.Namespaces["b"] = NamespacePosture{Coverage: Coverage{State: CoveragePartial, Reason: "list denied", Evaluated: 1, Total: 2}}
	got := AggregateSecurity(s).MergeNamespaces([]string{"a", "b"})
	if got.Score.Coverage.State != CoveragePartial || !got.Score.Partial {
		t.Fatalf("a partial namespace makes the set partial: %+v", got.Score)
	}
}

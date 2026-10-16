package data

import (
	"fmt"
	"testing"
)

func securityChurnSnapshot(uid string, revision uint64) SecuritySnapshot {
	p := securityDashboardFixture().Pods["a/pod"]
	partial := p
	partial.Target.UID = "partial"
	partial.Target.Name = "partial"
	partial.Coverage.State = CoveragePartial
	pods := map[string]PodPosture{"a/partial": partial}
	if uid != "" {
		p.Target.UID = uid
		p.Target.Name = "job"
		pods["a/job"] = p
	}
	s := SummarizeSecurity(pods, nil, Coverage{State: CoverageComplete})
	s.Revision = revision
	return s
}

func TestSecurityPartialEvaluatorDoesNotRetainDeletedJobs(t *testing.T) {
	var session SecuritySession
	session.Update(securityChurnSnapshot("", 1))
	for i := range 1000 {
		session.Update(securityChurnSnapshot(fmt.Sprint(i), uint64(2*i+2)))
		session.Acknowledge()
		session.Update(securityChurnSnapshot("", uint64(2*i+3)))
		if len(session.previous) != 1 {
			t.Fatalf("removed job retained after ack at cycle %d: %d identities", i, len(session.previous))
		}
	}
}

func TestSecurityProlongedGapExpiresIdentities(t *testing.T) {
	var session SecuritySession
	session.Update(securityDashboardFixture())
	for i := range 100 {
		session.Update(SecuritySnapshot{Revision: uint64(i + 2), Coverage: Coverage{State: CoverageDenied}})
	}
	if len(session.previous) != 0 {
		t.Fatalf("gap retained %d identities indefinitely", len(session.previous))
	}
}

func BenchmarkSecurityPartialJobChurn(b *testing.B) {
	var session SecuritySession
	session.Update(securityChurnSnapshot("", 1))
	revision := uint64(1)
	b.ReportAllocs()
	for b.Loop() {
		revision++
		session.Update(securityChurnSnapshot(fmt.Sprint(revision), revision))
		session.Acknowledge()
		revision++
		session.Update(securityChurnSnapshot("", revision))
	}
	if len(session.previous) != 1 {
		b.Fatalf("churn retained %d identities", len(session.previous))
	}
}

func TestSecurityListingCoverageSurvivesSummaryAndClone(t *testing.T) {
	s := securityChurnSnapshot("job", 1)
	if s.Coverage.State != CoveragePartial || s.ListCoverage.State != CoverageComplete || s.Clone().ListCoverage != s.ListCoverage {
		t.Fatal("listing and evaluator coverage conflated")
	}
}

func TestSecurityCompletePodReconcilesDespitePartialListing(t *testing.T) {
	s := securityDashboardFixture()
	var session SecuritySession
	session.Update(s)
	p := s.Pods["a/pod"]
	p.Findings = nil
	s.Pods["a/pod"] = p
	s.Coverage.State = CoveragePartial
	s.ListCoverage.State = CoveragePartial
	s.Revision++
	session.Update(s)
	if len(session.previous) != 0 {
		t.Fatal("complete pod observation retained resolved finding")
	}
}

func TestSecurityUncertainIdentityBudget(t *testing.T) {
	s := securityDashboardFixture()
	p := s.Pods["a/pod"]
	for i := range SecurityUncertainIdentityLimit + 100 {
		p.Target.UID = fmt.Sprint(i)
		s.Pods[fmt.Sprint(i)] = p
	}
	var a, b SecuritySession
	a.Update(s)
	b.Update(s)
	gap := SecuritySnapshot{Revision: 2, Coverage: Coverage{State: CoverageDenied}}
	a.Update(gap)
	b.Update(gap)
	if len(a.previous) != SecurityUncertainIdentityLimit {
		t.Fatalf("uncertain budget: %d", len(a.previous))
	}
	for id := range a.previous {
		if b.previous[id] == 0 {
			t.Fatal("nondeterministic eviction")
		}
	}
	a.Acknowledge()
	if len(a.previous) != SecurityUncertainIdentityLimit {
		t.Fatal("ack discarded still useful baseline")
	}
	for i := range SecurityIdentityRetention {
		gap.Revision++
		a.Update(gap)
		if i == 0 && len(a.previous) == 0 {
			t.Fatal("brief gap lost baseline")
		}
	}
	if len(a.previous) != 0 {
		t.Fatal("uncertain entries never expired")
	}
}

func TestSecurityStalePodDoesNotRefreshRetention(t *testing.T) {
	s := securityDashboardFixture()
	var session SecuritySession
	session.Update(s)
	p := s.Pods["a/pod"]
	p.Coverage.State = CoverageStale
	s.Pods["a/pod"] = p
	s.Coverage.State = CoveragePartial
	for range SecurityIdentityRetention {
		s.Revision++
		session.Update(s)
	}
	if len(session.previous) != 0 {
		t.Fatal("cached observation extended retention indefinitely")
	}
}

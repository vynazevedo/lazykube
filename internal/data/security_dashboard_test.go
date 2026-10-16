package data

import (
	"fmt"
	"reflect"
	"testing"
)

func TestPostureScore(t *testing.T) {
	for _, tc := range []struct {
		high, medium, info, pods, want int
		grade                          string
	}{
		{0, 0, 0, 1, 100, "A"}, {0, 1, 0, 1, 94, "A"}, {0, 2, 0, 1, 88, "B"}, {1, 0, 0, 1, 60, "D"}, {1, 1, 1, 1, 53, "E"}, {2, 0, 0, 1, 20, "F"}, {20, 0, 0, 1, 0, "F"}, {1, 1, 1, 5, 91, "A"},
	} {
		s := ScorePosture(Coverage{State: CoverageComplete, Evaluated: tc.pods, Total: tc.pods}, tc.high, tc.medium, tc.info)
		if !s.Available || s.Value != tc.want || s.Grade != tc.grade || s.Partial {
			t.Fatalf("score %+v: %+v", tc, s)
		}
	}
	partial := ScorePosture(Coverage{State: CoveragePartial, Evaluated: 40, Total: 55}, 40, 0, 0)
	if !partial.Partial || !partial.Available || partial.Value != 60 {
		t.Fatal(partial)
	}
	for _, state := range []CoverageState{CoverageDenied, CoverageUnavailable, CoverageStale, CoverageCollecting, CoverageUnsupported, CoverageDisabled} {
		if ScorePosture(Coverage{State: state, Evaluated: 1, Total: 1}, 1, 0, 0).Available {
			t.Fatalf("rated %s", state)
		}
	}
	if ScorePosture(Coverage{State: CoverageComplete}, 0, 0, 0).Available {
		t.Fatal("empty rated")
	}
}

func securityDashboardFixture() SecuritySnapshot {
	p := PodPosture{Target: ResourceRef{Context: "test", UID: "one", Namespace: "a", Name: "pod"}, Coverage: Coverage{State: CoverageComplete, Evaluated: 1, Total: 1}, Findings: []Finding{{Rule: "privileged", Container: "app", Severity: SeverityHigh, Result: ResultFinding}}}
	s := SummarizeSecurity(map[string]PodPosture{"a/pod": p}, nil, Coverage{State: CoverageComplete})
	s.Revision = 1
	return s
}

func TestSecurityAggregate(t *testing.T) {
	s := securityDashboardFixture()
	p := s.Pods["a/pod"]
	p.Findings = append(p.Findings, Finding{Rule: "unknown", Result: ResultUnknown, Severity: SeverityHigh}, Finding{Rule: "ignore", Result: ResultNotFound, Severity: SeverityHigh})
	s.Pods["a/pod"] = p
	d := AggregateSecurity(s)
	if d.Cluster.High != 1 || d.Cluster.Unknown != 1 || d.Cluster.Score.Value != 60 || len(d.Cluster.Rules) != 1 || d.Pods["a/pod"].Value != 60 || d.Namespaces["a"].Score.Value != 60 {
		t.Fatalf("aggregate %+v", d)
	}
	if !reflect.DeepEqual(d, AggregateSecurity(s)) {
		t.Fatal("nondeterministic aggregate")
	}
	s.Coverage.State = CoverageDenied
	d = AggregateSecurity(s)
	if d.Cluster.Score.Available || d.Pods["a/pod"].Available || d.Namespaces["a"].Score.Available {
		t.Fatal("denied scope rated")
	}
}

func TestSecurityAlertsBaselineIdentityAndAck(t *testing.T) {
	s := securityDashboardFixture()
	var session SecuritySession
	session.Update(s)
	if len(session.Alerts) != 0 {
		t.Fatal("baseline alerted")
	}
	p := s.Pods["a/pod"]
	p.Findings[0].Evidence = "changed text"
	p.Target.ResourceVersion = "2"
	s.Pods["a/pod"] = p
	s.Revision++
	session.Update(s)
	if len(session.Alerts) != 0 {
		t.Fatal("evidence/RV changed identity")
	}
	p.Findings = append(p.Findings, Finding{Rule: "privileged", Container: "init", Severity: SeverityHigh, Result: ResultFinding})
	s.Pods["a/pod"] = p
	s.Revision++
	session.Update(s)
	if len(session.Alerts) != 1 {
		t.Fatal("new container did not alert")
	}
	session.Acknowledge()
	s.Revision++
	session.Update(s)
	if len(session.Alerts) != 0 {
		t.Fatal("ack repeated unchanged finding")
	}
	p.Target.UID = "recreated"
	s.Pods["a/pod"] = p
	s.Revision++
	session.Update(s)
	if len(session.Alerts) != 2 {
		t.Fatal("UID recreation did not alert")
	}
	p.Target.Namespace, p.Target.UID = "b", "other"
	s.Pods["b/pod"] = p
	s.Revision++
	session.Update(s)
	if len(session.Alerts) != 4 {
		t.Fatal("same name in distinct namespaces collided")
	}
}

func TestSecuritySessionCoverageAndHistory(t *testing.T) {
	s := securityDashboardFixture()
	s.Coverage.State = CoverageDenied
	var session SecuritySession
	session.Update(s)
	s.Coverage.State = CoverageComplete
	s.Revision++
	session.Update(s)
	if len(session.Alerts) != 0 {
		t.Fatal("initial usable snapshot alerted")
	}
	before := session.Dashboard.Pods
	if session.Update(s) || reflect.ValueOf(before).Pointer() != reflect.ValueOf(session.Dashboard.Pods).Pointer() {
		t.Fatal("unchanged revision recalculated")
	}
	for range 50 {
		s.Revision++
		session.Update(s)
	}
	if len(session.History[""]) != SecurityHistoryLimit {
		t.Fatal("history not bounded")
	}
	s.Coverage.State = CoverageDenied
	s.Revision++
	session.Update(s)
	if len(session.History[""]) != SecurityHistoryLimit {
		t.Fatal("denied sample added")
	}
}

func TestDemoSecurityIncident(t *testing.T) {
	d := NewDemo()
	var session SecuritySession
	session.Update(d.Security())
	if d.AdvanceSecurity(239) || !d.AdvanceSecurity(240) || d.AdvanceSecurity(241) {
		t.Fatal("incident must happen once at fixed tick")
	}
	session.Update(d.Security())
	if len(session.Alerts) != 1 || session.Alerts[0].Rule != "hostPID" || len(session.History[""]) != 2 {
		t.Fatal("demo incident missing")
	}
}

func BenchmarkSecurityDashboard10000(b *testing.B) {
	s := securityDashboardFixture()
	p := s.Pods["a/pod"]
	for i := range 10000 {
		p.Target.UID = fmt.Sprint(i)
		s.Pods[fmt.Sprint(i)] = p
	}
	s = SummarizeSecurity(s.Pods, nil, Coverage{State: CoverageComplete})
	var session SecuritySession
	b.ReportAllocs()
	for b.Loop() {
		s.Revision++
		session.Update(s)
	}
}

func TestSecurityAlertsCoverageGap(t *testing.T) {
	s := securityDashboardFixture()
	var session SecuritySession
	session.Update(s)
	original := s.Clone()
	s.Pods = nil
	s.Coverage.State = CoverageDenied
	s.Revision++
	session.Update(s)
	original.Revision = s.Revision + 1
	session.Update(original)
	if len(session.Alerts) != 0 {
		t.Fatal("coverage recovery alerted known findings")
	}
	s = original.Clone()
	s.Pods = nil
	s.Coverage.State = CoveragePartial
	s.Revision++
	session.Update(s)
	original.Revision = s.Revision + 1
	session.Update(original)
	if len(session.Alerts) != 0 {
		t.Fatal("partial omission alerted known findings")
	}
	p := original.Pods["a/pod"]
	p.Findings = append(p.Findings, Finding{Rule: "hostPID", Result: ResultFinding, Severity: SeverityHigh})
	original.Pods["a/pod"] = p
	original.Coverage.State = CoveragePartial
	original.Revision++
	session.Update(original)
	if len(session.Alerts) != 1 || session.Alerts[0].Rule != "hostPID" {
		t.Fatal("new high in partial coverage missing")
	}
}

func TestSecurityAggregateOrderAndGrades(t *testing.T) {
	s := securityDashboardFixture()
	p := s.Pods["a/pod"]
	p.Target.Namespace = "b"
	p.Findings = []Finding{{Rule: "z", Result: ResultFinding, Severity: SeverityInfo}, {Rule: "a", Result: ResultFinding, Severity: SeverityInfo}}
	s.Pods["b/pod"] = p
	s = SummarizeSecurity(s.Pods, nil, Coverage{State: CoverageComplete})
	a := AggregateSecurity(s)
	if a.Ranking[0].Name != "a" || a.Ranking[1].Name != "b" || a.Cluster.Rules[0].Rule != "a" || a.Cluster.Rules[2].Rule != "z" {
		t.Fatal("unstable order")
	}
	if got := ScorePosture(Coverage{State: CoverageComplete, Evaluated: 2, Total: 2}, 1, 0, 0).Value; got != 80 {
		t.Fatalf("rounding: %d", got)
	}
}

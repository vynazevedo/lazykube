package data

import "testing"

func TestSecurityV2DedupDimensions(t *testing.T) {
	p := securityDashboardFixture().Pods["a/pod"]
	p.Findings = []Finding{
		{Rule: "capabilitiesAdded", Container: "app", Severity: SeverityMedium, Result: ResultFinding, Assessment: AssessmentUnsafe},
		{Rule: "capabilitiesAdded", Container: "sidecar", Severity: SeverityHigh, Result: ResultFinding, Assessment: AssessmentUnsafe},
		{Rule: "imageDigest", Container: "app", Severity: SeverityMedium, Result: ResultFinding, Assessment: AssessmentUnspecified},
		{Rule: "imageDigest", Container: "sidecar", Severity: SeverityMedium, Result: ResultFinding, Assessment: AssessmentUnspecified},
		{Rule: "runAsRoot", Severity: SeverityInfo, Result: ResultUnknown, Assessment: AssessmentUnknown},
	}
	s := ScorePodPosture(p)
	if s.Security.Value != 60 || s.Hygiene.Value != 94 {
		t.Fatalf("scores %+v", s)
	}
	assessments := AssessPod(p)
	if len(assessments) != 3 {
		t.Fatal("rules not deduplicated")
	}
	if ScoreRuleAssessments(p.Coverage, assessments) != s {
		t.Fatal("assessment scoring differs")
	}
	p.Findings = append(p.Findings, p.Findings...)
	if ScorePodPosture(p) != s {
		t.Fatal("container count changed score")
	}
}

func TestSecurityV2Distribution(t *testing.T) {
	var d ScoreDistribution
	for _, value := range []int{0, 50, 60, 70, 80, 90, 100, 100, 100, 100} {
		s := ScorePosture(Coverage{State: CoverageComplete, Evaluated: 1, Total: 1}, 0, 0, 100-value)
		d.add(s)
	}
	d.finish()
	if d.Mean != 75 || d.Median != 85 || d.WorstDecile != 0 || d.Grades != [6]int{5, 1, 1, 1, 1, 1} {
		t.Fatalf("distribution %+v", d)
	}
	p := securityDashboardFixture().Pods["a/pod"]
	p.Findings = []Finding{{Rule: "a", Severity: SeverityHigh, Result: ResultFinding}, {Rule: "b", Severity: SeverityHigh, Result: ResultFinding}, {Rule: "c", Severity: SeverityHigh, Result: ResultFinding}}
	clean := p
	clean.Target.UID = "clean"
	clean.Findings = nil
	snapshot := SummarizeSecurity(map[string]PodPosture{"a/risky": p, "a/clean": clean}, nil, Coverage{State: CoverageComplete})
	a := AggregateSecurity(snapshot)
	if a.Cluster.Score.Value != 50 || a.Cluster.Distribution.Median != 50 || a.Cluster.Distribution.WorstDecile != 0 {
		t.Fatal("scope must average clamped pod scores")
	}
}

func TestSecurityRuleVersionBaseline(t *testing.T) {
	snapshot := securityDashboardFixture()
	snapshot.RuleVersion = "4"
	var session SecuritySession
	session.Update(snapshot)
	p := snapshot.Pods["a/pod"]
	p.Findings = append(p.Findings, Finding{Rule: "new-high", Severity: SeverityHigh, Result: ResultFinding, RuleVersion: SecurityRuleVersion})
	snapshot.Pods["a/pod"] = p
	snapshot.RuleVersion = SecurityRuleVersion
	session.Update(snapshot)
	if len(session.Alerts) != 0 || len(session.History[""]) != 1 {
		t.Fatal("version transition did not rebaseline")
	}
	snapshot.Revision++
	p.Findings = append(p.Findings, Finding{Rule: "real-change", Severity: SeverityHigh, Result: ResultFinding, RuleVersion: SecurityRuleVersion})
	snapshot.Pods["a/pod"] = p
	session.Update(snapshot)
	if len(session.Alerts) != 1 || session.Alerts[0].Rule != "real-change" {
		t.Fatal("post-upgrade new high not alerted")
	}
}

func TestSecurityAssessmentClone(t *testing.T) {
	p := securityDashboardFixture().Pods["a/pod"]
	p.Assessments = AssessPod(p)
	snapshot := SummarizeSecurity(map[string]PodPosture{"a/pod": p}, nil, Coverage{State: CoverageComplete})
	clone := snapshot.Clone()
	clone.Pods["a/pod"].Assessments[0].Evidence[0].Evidence = "changed"
	if snapshot.Pods["a/pod"].Assessments[0].Evidence[0].Evidence == "changed" {
		t.Fatal("assessment evidence aliases clone")
	}
}

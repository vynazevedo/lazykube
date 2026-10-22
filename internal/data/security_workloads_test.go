package data

import (
	"strconv"
	"strings"
	"testing"
)

func workloadPod(ns, name string, owner *OwnerRef, findings ...Finding) PodPosture {
	ref := ResourceRef{Context: "ctx", Generation: 1, Version: "v1", Resource: "pods", Namespace: ns, Name: name, UID: "uid-" + ns + "-" + name}
	for i := range findings {
		findings[i].Target = ref
	}
	return PodPosture{Owner: owner, Target: ref, Coverage: Coverage{State: CoverageComplete, Evaluated: 1, Total: 1}, Findings: findings}
}

func high(rule, container, field string) Finding {
	return Finding{Rule: rule, RuleVersion: SecurityRuleVersion, Severity: SeverityHigh, Result: ResultFinding, Container: container, Field: field}
}

func workloadSnapshot(pods ...PodPosture) SecuritySnapshot {
	m := make(map[string]PodPosture, len(pods))
	for _, p := range pods {
		m[PodKey(p.Target.Namespace, p.Target.Name)] = p
	}
	return SummarizeSecurity(m, nil, Coverage{State: CoverageComplete})
}

func TestAggregateWorkloadsGroupsByControllerIdentity(t *testing.T) {
	rs := &OwnerRef{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "api-7f", UID: "rs-1"}
	twin := &OwnerRef{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "api-7f", UID: "rs-1"}
	s := workloadSnapshot(
		workloadPod("apps", "api-7f-a", rs, high("privileged", "api", "spec.containers[0].securityContext.privileged")),
		workloadPod("apps", "api-7f-b", rs),
		workloadPod("other", "api-7f-c", twin),
		workloadPod("apps", "loose", nil),
		workloadPod("apps", "orphan", &OwnerRef{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "x"}),
		workloadPod("apps", "rollout-1", &OwnerRef{APIVersion: "argoproj.io/v1alpha1", Kind: "Rollout", Name: "r", UID: "ro-1"}),
		workloadPod("apps", "bad-gv", &OwnerRef{APIVersion: "a/b/c", Kind: "ReplicaSet", Name: "x", UID: "u"}),
	)
	ws := AggregateWorkloads(s, nil)
	if len(ws) != 6 {
		t.Fatalf("want 6 workloads, got %d: %+v", len(ws), ws)
	}
	by := map[string]WorkloadPosture{}
	for _, w := range ws {
		by[w.Key.Namespace+"|"+w.Label()+"|"+string(w.Resolution)] = w
	}
	api, ok := by["apps|ReplicaSet/api-7f|direct controller"]
	if !ok || len(api.Pods) != 2 || api.Key.Group != "apps" {
		t.Fatalf("pods of the same controller UID in one namespace form one workload: %+v", by)
	}
	if _, ok := by["other|ReplicaSet/api-7f|direct controller"]; !ok {
		t.Fatal("same UID in another namespace must not merge")
	}
	if _, ok := by["apps|Pod/loose|standalone"]; !ok {
		t.Fatal("pod without controller is standalone")
	}
	if _, ok := by["apps|Pod/orphan|owner unresolved"]; !ok {
		t.Fatal("owner without UID is unresolved, not standalone")
	}
	if _, ok := by["apps|Pod/bad-gv|owner unresolved"]; !ok {
		t.Fatal("invalid apiVersion is unresolved")
	}
	if _, ok := by["apps|Rollout/r|unrecognized controller"]; !ok {
		t.Fatal("unknown controller kind stays grouped but unrecognized")
	}
	if ws[0].Label() != "ReplicaSet/api-7f" || ws[0].Aggregate.Score.Value != 80 {
		t.Fatalf("worst workload first with canonical mean of pod scores, got %s %d", ws[0].Label(), ws[0].Aggregate.Score.Value)
	}
	if ws[0].Aggregate.Score.Coverage.Total != 2 || ws[0].Aggregate.High != 1 {
		t.Fatalf("workload aggregate scoped to its pods: %+v", ws[0].Aggregate)
	}
}

func TestWorkloadScoreMatchesNamespaceWhenSingleWorkload(t *testing.T) {
	rs := &OwnerRef{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "db", UID: "ss"}
	s := workloadSnapshot(
		workloadPod("db", "db-0", rs, high("hostPID", "", "spec.hostPID")),
		workloadPod("db", "db-1", rs, high("privileged", "db", "spec.containers[0].securityContext.privileged"), Finding{Rule: "seccomp", Severity: SeverityMedium, Result: ResultFinding, Container: "db", Field: "spec.containers[0].securityContext"}),
	)
	ws := AggregateWorkloads(s, nil)
	ns := AggregateSecurity(s).Namespaces["db"]
	if len(ws) != 1 || ws[0].Aggregate.Score != ns.Score || ws[0].Aggregate.Hygiene != ns.Hygiene {
		t.Fatalf("workload and namespace scores diverge: %+v vs %+v", ws[0].Aggregate.Score, ns.Score)
	}
	rules := WorkloadRules(s, ws[0])
	if len(rules) != 3 || rules[0].Severity != SeverityHigh || rules[len(rules)-1].Rule != "seccomp" {
		t.Fatalf("rules ordered by severity: %+v", rules)
	}
}

func TestWorkloadCoverageNeverImproves(t *testing.T) {
	rs := &OwnerRef{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "agent", UID: "ds"}
	p := workloadPod("ops", "agent-1", rs)
	m := map[string]PodPosture{"ops/agent-1": p}
	s := SummarizeSecurity(m, nil, Coverage{State: CoverageDenied, Reason: "denied"})
	ws := AggregateWorkloads(s, nil)
	if ws[0].Aggregate.Score.Available {
		t.Fatal("denied coverage must not produce a score")
	}
	s = SummarizeSecurity(map[string]PodPosture{}, nil, Coverage{State: CoverageComplete})
	if got := AggregateWorkloads(s, nil); len(got) != 0 {
		t.Fatalf("no pods, no workloads: %+v", got)
	}
}

func TestSystemCohortExactExclusion(t *testing.T) {
	s := workloadSnapshot(
		workloadPod("kube-system", "a", nil, high("privileged", "c", "spec.containers[0].securityContext.privileged")),
		workloadPod("kube-system-extra", "b", nil),
		workloadPod("apps", "c", nil),
	)
	sub := SubsetSecurity(s, func(p PodPosture) bool { return !IsSystemNamespace(p.Target.Namespace) })
	if len(sub.Pods) != 2 || sub.Coverage.Total != 2 {
		t.Fatalf("exact exclusion keeps lookalike namespaces: %+v", sub.Coverage)
	}
	if AggregateSecurity(sub).Cluster.Score.Value != 100 {
		t.Fatal("cohort without system namespaces excludes their findings")
	}
}

func TestRemediationTargetsAndContainers(t *testing.T) {
	w := WorkloadPosture{Key: WorkloadKey{Kind: "StatefulSet"}, Name: "db", Resolution: OwnerDirect}
	r := RemediationFor(WorkloadRule{Rule: "privileged", Evidence: high("privileged", "db", "spec.initContainers[0].securityContext.privileged")}, w)
	if strings.Join(r.YAML, "\n") != "initContainers:\n- name: db\n  securityContext:\n    privileged: false" || !strings.Contains(r.Target, "spec.template.spec de StatefulSet/db") {
		t.Fatalf("unexpected remediation: %+v", r)
	}
	r = RemediationFor(WorkloadRule{Rule: "capabilitiesAdded", Evidence: high("capabilitiesAdded", "debug", "spec.ephemeralContainers[0].securityContext")}, w)
	if r.YAML != nil {
		t.Fatal("ephemeral containers get no YAML")
	}
	rs := WorkloadPosture{Key: WorkloadKey{Kind: "ReplicaSet"}, Name: "api", Resolution: OwnerDirect}
	if r := RemediationFor(WorkloadRule{Rule: "hostNetwork"}, rs); !strings.Contains(r.Target, "não observado") || r.YAML[0] != "hostNetwork: false" {
		t.Fatalf("ReplicaSet does not imply a Deployment: %+v", r)
	}
	if r := RemediationFor(WorkloadRule{Rule: "hostPath"}, rs); r.YAML != nil {
		t.Fatal("hostPath has no generic YAML")
	}
}

func BenchmarkAggregateWorkloads10k(b *testing.B) {
	pods := make(map[string]PodPosture, 10000)
	for i := range 10000 {
		ns := "ns" + strconv.Itoa(i%50)
		owner := &OwnerRef{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs" + strconv.Itoa(i/4), UID: "rs-" + strconv.Itoa(i/4)}
		p := workloadPod(ns, "p"+strconv.Itoa(i), owner, high("privileged", "c", "spec.containers[0].securityContext.privileged"))
		pods[PodKey(ns, p.Target.Name)] = p
	}
	s := SummarizeSecurity(pods, nil, Coverage{State: CoverageComplete})
	b.ResetTimer()
	for b.Loop() {
		if got := AggregateWorkloads(s, nil); len(got) == 0 {
			b.Fatal("no workloads")
		}
	}
}

package data

import "time"

func (d *Demo) buildSecurity() SecuritySnapshot {
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	pods := make(map[string]PodPosture, len(d.pods))
	for i, row := range d.pods {
		p := row.pod
		ref := ResourceRef{Context: "demo", Generation: 1, Version: "v1", Resource: "pods", Namespace: p.Namespace, Name: p.Name, UID: "demo-" + p.Key(), ResourceVersion: "1"}
		posture := PodPosture{RuleVersion: SecurityRuleVersion, Target: ref, Coverage: Coverage{State: CoverageComplete, Evaluated: 1, Total: 1, ObservedAt: at}}
		add := func(rule, container, field, evidence, why, recommendation string, severity Severity, result RuleResult) {
			confidence := ConfidenceObserved
			if result == ResultUnknown {
				confidence = ConfidenceUnknown
			}
			posture.Findings = append(posture.Findings, Finding{Rule: rule, RuleVersion: SecurityRuleVersion, Origin: "demo fixture", Severity: severity, Confidence: confidence, Result: result, Target: ref, Container: container, Field: field, Evidence: evidence, Explanation: why, Recommendation: recommendation, ObservedAt: at})
		}
		switch i % 4 {
		case 0:
			for _, r := range []string{"allowPrivilegeEscalation", "runAsNonRoot", "seccomp", "capabilitiesDrop"} {
				add(r, "app", "spec.containers[0].securityContext", "unspecified", "Protection not explicitly configured", "Review and configure this control", SeverityMedium, ResultFinding)
				posture.Findings[len(posture.Findings)-1].Assessment = AssessmentUnspecified
			}
			add("serviceAccountToken", "app", "spec.automountServiceAccountToken", "default token mount enabled", "Credentials available to workload", "Disable automatic mount when unused", SeverityInfo, ResultFinding)
			add("runAsRoot", "app", "spec.containers[0].securityContext.runAsUser", "UID unspecified; runtime user unknown", "Image user is not inspected", "Require a non-root user", SeverityInfo, ResultUnknown)
		case 1:
			add("allowPrivilegeEscalation", "init-permissions", "spec.initContainers[0].securityContext.allowPrivilegeEscalation", "forced by privileged", "Additional privileges possible", "Disable privileged mode", SeverityHigh, ResultFinding)
			add("privileged", "init-permissions", "spec.initContainers[0].securityContext.privileged", "true", "Privileged init container configured", "Disable privileged mode", SeverityHigh, ResultFinding)
			add("imageLatest", "app", "spec.containers[0].image", "latest tag without digest", "Image reference is mutable", "Pin a verified digest", SeverityMedium, ResultFinding)
			add("limit-memory", "app", "spec.containers[0].resources.limits.memory", "absent at container and pod levels", "Availability risk", "Set a suitable memory limit", SeverityMedium, ResultFinding)
		case 2:
			add("hostNetwork", "", "spec.hostNetwork", "true", "Shares host networking", "Disable host network unless required", SeverityHigh, ResultFinding)
			add("capabilitiesAdded", "debugger", "spec.ephemeralContainers[0].securityContext.capabilities.add", "SYS_ADMIN", "Additional Linux capability configured", "Remove unnecessary capabilities", SeverityHigh, ResultFinding)
		}
		pods[p.Key()] = posture
	}
	names := []string{"default", "kube-system", "kube-public", "kube-node-lease", "bilibili-raffle", "lgo", "xcx", "ys-odoo"}
	snapshot := SummarizeSecurity(pods, names, Coverage{State: CoverageComplete, ObservedAt: at})
	snapshot.Revision = 1
	return snapshot
}

func (d *Demo) Security() SecuritySnapshot { return d.security.Clone() }

func (d *Demo) SecurityRevision() uint64 { return d.security.Revision }

func (d *Demo) AdvanceSecurity(tick int) bool {
	if tick < 240 || d.security.Revision != 1 {
		return false
	}
	key := d.pods[0].pod.Key()
	p := d.security.Pods[key]
	p.Target.ResourceVersion = "2"
	p.Findings = append(p.Findings, Finding{Rule: "hostPID", RuleVersion: SecurityRuleVersion, Origin: "demo fixture", Severity: SeverityHigh, Result: ResultFinding, Confidence: ConfidenceObserved, Target: p.Target, Field: "spec.hostPID", Evidence: "true", Explanation: "Simulated host PID access", Recommendation: "Disable host PID sharing", ObservedAt: p.Coverage.ObservedAt})
	d.security.Pods[key] = p
	names := make([]string, 0, len(d.security.Namespaces))
	for name := range d.security.Namespaces {
		names = append(names, name)
	}
	d.security = SummarizeSecurity(d.security.Pods, names, d.security.Coverage)
	d.security.Revision = 2
	return true
}

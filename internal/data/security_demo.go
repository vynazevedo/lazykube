package data

import "time"

func (d *Demo) buildSecurity() SecuritySnapshot {
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	pods := make(map[string]PodPosture, len(d.pods))
	for i, row := range d.pods {
		p := row.pod
		ref := ResourceRef{Context: "huawei_k3s", Generation: 1, Version: "v1", Resource: "pods", Namespace: p.Namespace, Name: p.Name, UID: "demo-" + p.Key(), ResourceVersion: "1"}
		posture := PodPosture{Target: ref, Coverage: Coverage{State: CoverageComplete, Evaluated: 1, Total: 1, ObservedAt: at}}
		add := func(rule, container, field, evidence, why, recommendation string, severity Severity, result RuleResult) {
			confidence := ConfidenceObserved
			if result == ResultUnknown {
				confidence = ConfidenceUnknown
			}
			posture.Findings = append(posture.Findings, Finding{Rule: rule, RuleVersion: "1", Origin: "demo fixture", Severity: severity, Confidence: confidence, Result: result, Target: ref, Container: container, Field: field, Evidence: evidence, Explanation: why, Recommendation: recommendation, ObservedAt: at})
		}
		switch i % 4 {
		case 0:
			add("runAsRoot", "app", "spec.containers[0].securityContext.runAsUser", "UID unspecified; runtime user unknown", "Image user is not inspected", "Require a non-root user", SeverityInfo, ResultUnknown)
		case 1:
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

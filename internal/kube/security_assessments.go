package kube

import (
	"cmp"
	"slices"

	"github.com/vynazevedo/lazykube/internal/data"
	corev1 "k8s.io/api/core/v1"
)

func (e *podEvaluator) finishAssessments(p *corev1.Pod) {
	assessments := data.AssessPod(e.posture)
	indices := make(map[string]int)
	for i, a := range assessments {
		indices[a.Rule] = i
	}
	linux := map[string]bool{"privileged": true, "allowPrivilegeEscalation": true, "capabilitiesAdded": true, "capabilitiesDrop": true, "runAsRoot": true, "runAsNonRoot": true, "seccomp": true}
	record := func(rule, container string, ephemeral bool) {
		index, exists := indices[rule]
		if exists {
			for _, f := range assessments[index].Evidence {
				if f.Container == container {
					return
				}
			}
		}
		state, result := data.AssessmentConformant, data.ResultNotFound
		evidence := "No violation in the evaluated local rule after inheritance"
		if p.Spec.OS != nil && p.Spec.OS.Name == corev1.Windows && linux[rule] {
			state, result = data.AssessmentNotApplicable, data.ResultNotApplicable
			evidence = "Linux rule does not apply to Windows"
		}
		if ephemeral && (rule == "limit-cpu" || rule == "limit-memory") {
			state, result = data.AssessmentNotApplicable, data.ResultNotApplicable
			evidence = "Ephemeral containers do not support limits"
		}
		confidence := data.ConfidenceObserved
		if e.posture.Coverage.State != data.CoverageComplete {
			state, result = data.AssessmentUnknown, data.ResultUnknown
			confidence = data.ConfidenceUnknown
			evidence = "Local evaluation is incomplete"
		}
		f := data.Finding{Rule: rule, RuleVersion: securityRuleVersion, Origin: "local pod spec", Result: result, Assessment: state, Confidence: confidence, Target: e.posture.Target, Container: sanitize(container), Field: "spec", Evidence: evidence, ObservedAt: e.now}
		if !exists {
			index = len(assessments)
			indices[rule] = index
			assessments = append(assessments, data.RuleAssessment{Rule: rule, Version: securityRuleVersion, Dimension: data.RuleDimension(rule), State: state})
		} else if assessments[index].State == data.AssessmentConformant && state == data.AssessmentUnknown {
			assessments[index].State = state
		}
		assessments[index].Evidence = append(assessments[index].Evidence, f)
	}
	for _, rule := range []string{"hostNetwork", "hostPID", "hostIPC", "hostPath"} {
		record(rule, "", false)
	}
	container := func(name string, ephemeral bool) {
		for _, rule := range []string{"privileged", "allowPrivilegeEscalation", "capabilitiesAdded", "capabilitiesDrop", "runAsRoot", "runAsNonRoot", "seccomp", "imageDigest", "imageLatest", "limit-cpu", "limit-memory"} {
			record(rule, name, ephemeral)
		}
	}
	for _, c := range p.Spec.Containers {
		container(c.Name, false)
	}
	for _, c := range p.Spec.InitContainers {
		container(c.Name, false)
	}
	for _, c := range p.Spec.EphemeralContainers {
		container(c.Name, true)
	}
	slices.SortFunc(assessments, func(a, b data.RuleAssessment) int { return cmp.Compare(a.Rule, b.Rule) })
	e.posture.Assessments = assessments
}

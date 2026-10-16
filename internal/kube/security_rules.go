package kube

import (
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/vynazevedo/lazykube/internal/data"
)

const securityRuleVersion = data.SecurityRuleVersion
const maxPodFindings = 256

type podEvaluator struct {
	posture   data.PodPosture
	now       time.Time
	container string
}

func evaluatePod(p *corev1.Pod, context string, generation uint64, now time.Time) data.PodPosture {
	e := podEvaluator{now: now, posture: data.PodPosture{
		RuleVersion: securityRuleVersion,
		Target:      data.ResourceRef{Context: context, Generation: generation, Version: "v1", Resource: "pods", Namespace: p.Namespace, Name: p.Name, UID: string(p.UID), ResourceVersion: p.ResourceVersion},
		Coverage:    data.Coverage{State: data.CoverageComplete, Evaluated: 1, Total: 1, ObservedAt: now},
	}}
	if p.UID == "" {
		e.posture.Coverage.State = data.CoveragePartial
		e.posture.Coverage.Reason = "UID missing; resource identity is partial"
	}
	for _, v := range []struct {
		name    string
		enabled bool
	}{
		{"hostNetwork", p.Spec.HostNetwork}, {"hostPID", p.Spec.HostPID}, {"hostIPC", p.Spec.HostIPC},
	} {
		if v.enabled {
			e.add(v.name, "spec."+v.name, "true", data.SeverityHigh, data.ResultFinding, "Shares a host namespace", "Disable host namespace sharing unless required")
		}
	}
	for i, v := range p.Spec.Volumes {
		if v.HostPath != nil {
			e.add("hostPath", fmt.Sprintf("spec.volumes[%d].hostPath", i), "hostPath volume present", data.SeverityHigh, data.ResultFinding, "Host filesystem access configured", "Use a scoped storage volume instead")
		}
	}
	for i, c := range p.Spec.Containers {
		e.checkContainer(p, c, fmt.Sprintf("spec.containers[%d]", i), false)
	}
	for i, c := range p.Spec.InitContainers {
		e.checkContainer(p, c, fmt.Sprintf("spec.initContainers[%d]", i), false)
	}
	for i, c := range p.Spec.EphemeralContainers {
		e.checkContainer(p, corev1.Container{Name: c.Name, Image: c.Image, SecurityContext: c.SecurityContext}, fmt.Sprintf("spec.ephemeralContainers[%d]", i), true)
	}
	e.finishAssessments(p)
	return e.posture
}

func (e *podEvaluator) add(rule, field, evidence string, severity data.Severity, result data.RuleResult, why, recommendation string, states ...data.AssessmentState) {
	if len(e.posture.Findings) >= maxPodFindings {
		e.posture.Coverage.State = data.CoveragePartial
		e.posture.Coverage.Reason = "finding limit reached; additional results omitted"
		return
	}
	confidence := data.ConfidenceObserved
	if result == data.ResultUnknown {
		confidence = data.ConfidenceUnknown
	}
	state := data.AssessmentUnsafe
	switch result {
	case data.ResultUnknown:
		state = data.AssessmentUnknown
	case data.ResultNotApplicable:
		state = data.AssessmentNotApplicable
	case data.ResultNotFound:
		state = data.AssessmentConformant
	}
	if len(states) > 0 {
		state = states[0]
	}
	e.posture.Findings = append(e.posture.Findings, data.Finding{
		Assessment: state,
		Rule:       rule, RuleVersion: securityRuleVersion, Origin: "local pod spec", Severity: severity,
		Confidence: confidence, Result: result, Target: e.posture.Target, Container: sanitize(e.container),
		Field: field, Evidence: sanitize(evidence), Explanation: why, Recommendation: recommendation, ObservedAt: e.now,
	})
}

func (e *podEvaluator) checkContainer(p *corev1.Pod, c corev1.Container, path string, ephemeral bool) {
	e.container = c.Name
	sc := c.SecurityContext
	if sc == nil {
		sc = &corev1.SecurityContext{}
	}
	psc := p.Spec.SecurityContext
	if psc == nil {
		psc = &corev1.PodSecurityContext{}
	}
	windows := p.Spec.OS != nil && p.Spec.OS.Name == corev1.Windows
	if windows {
		e.add("linuxSecurityContext", path+".securityContext", "Windows pod", data.SeverityInfo, data.ResultNotApplicable, "Linux privilege checks do not apply", "Review Windows host process policy separately")
	} else {
		e.checkPrivileges(sc, path)
		uid, uidField := psc.RunAsUser, "spec.securityContext.runAsUser"
		if sc.RunAsUser != nil {
			uid, uidField = sc.RunAsUser, path+".securityContext.runAsUser"
		}
		switch {
		case uid == nil:
			e.add("runAsRoot", uidField, "UID unspecified; runtime user unknown", data.SeverityInfo, data.ResultUnknown, "Image user is not inspected", "Set a nonzero runAsUser and runAsNonRoot")
		case *uid == 0:
			e.add("runAsRoot", uidField, "0", data.SeverityHigh, data.ResultFinding, "Root UID explicitly configured", "Set a nonzero runAsUser")
		}
		nonroot, field := psc.RunAsNonRoot, "spec.securityContext.runAsNonRoot"
		if sc.RunAsNonRoot != nil {
			nonroot, field = sc.RunAsNonRoot, path+".securityContext.runAsNonRoot"
		}
		if nonroot == nil || !*nonroot {
			state := data.AssessmentUnspecified
			evidence := "unspecified"
			if nonroot != nil {
				state = data.AssessmentUnsafe
				evidence = "false"
			}
			e.add("runAsNonRoot", field, evidence, data.SeverityMedium, data.ResultFinding, "Non-root execution is not required", "Set runAsNonRoot to true", state)
		}
		seccomp, field := psc.SeccompProfile, "spec.securityContext.seccompProfile"
		if sc.SeccompProfile != nil {
			seccomp, field = sc.SeccompProfile, path+".securityContext.seccompProfile"
		}
		if seccomp == nil || seccomp.Type == corev1.SeccompProfileTypeUnconfined {
			severity, state := data.SeverityMedium, data.AssessmentUnspecified
			evidence := "unspecified; node default unknown"
			if seccomp != nil {
				evidence = "Unconfined"
				severity, state = data.SeverityHigh, data.AssessmentUnsafe
			}
			e.add("seccomp", field, evidence, severity, data.ResultFinding, "No explicit syscall restriction configured", "Set RuntimeDefault or a reviewed Localhost profile", state)
		}
	}
	e.checkImage(c.Image, path+".image")
	if ephemeral {
		e.add("limits", path+".resources", "ephemeral container", data.SeverityInfo, data.ResultNotApplicable, "Ephemeral containers do not support resource limits", "Review limits of the parent pod")
	} else {
		e.checkLimits(p, c, path)
	}
}

func (e *podEvaluator) checkPrivileges(sc *corev1.SecurityContext, path string) {
	if sc.Privileged != nil && *sc.Privileged {
		e.add("privileged", path+".securityContext.privileged", "true", data.SeverityHigh, data.ResultFinding, "Privileged container configured", "Disable privileged mode")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation || (sc.Privileged != nil && *sc.Privileged) || effectiveCapability(sc.Capabilities, "SYS_ADMIN") {
		severity, state := data.SeverityHigh, data.AssessmentUnsafe
		evidence := "true or forced by privileged/SYS_ADMIN"
		if sc.AllowPrivilegeEscalation == nil && (sc.Privileged == nil || !*sc.Privileged) && !effectiveCapability(sc.Capabilities, "SYS_ADMIN") {
			severity, state = data.SeverityMedium, data.AssessmentUnspecified
			evidence = "unspecified; privilege escalation not explicitly disabled"
		}
		e.add("allowPrivilegeEscalation", path+".securityContext.allowPrivilegeEscalation", evidence, severity, data.ResultFinding, "Processes may gain additional privileges", "Disable escalation and remove privileged/SYS_ADMIN", state)
	}
	if hasEffectiveCapabilityAdd(sc.Capabilities) {
		caps := make([]string, len(sc.Capabilities.Add))
		for i, c := range sc.Capabilities.Add {
			caps[i] = string(c)
		}
		severity := data.SeverityMedium
		if effectiveCapability(sc.Capabilities, "SYS_ADMIN") || effectiveCapability(sc.Capabilities, "ALL") {
			severity = data.SeverityHigh
		}
		e.add("capabilitiesAdded", path+".securityContext.capabilities.add", strings.Join(caps, ", ")+"; drop: "+capabilityDropEvidence(sc.Capabilities), severity, data.ResultFinding, "Additional Linux capabilities configured", "Remove capabilities that are not required")
	}
	if !hasCapability(sc.Capabilities, "ALL", true) {
		state := data.AssessmentUnspecified
		if sc.Capabilities != nil && len(sc.Capabilities.Drop) > 0 {
			state = data.AssessmentUnsafe
		}
		e.add("capabilitiesDrop", path+".securityContext.capabilities.drop", "ALL not dropped", data.SeverityMedium, data.ResultFinding, "Default capabilities remain available", "Drop ALL and add only required capabilities", state)
	}
}

func hasCapability(c *corev1.Capabilities, want corev1.Capability, drop bool) bool {
	if c == nil {
		return false
	}
	values := c.Add
	if drop {
		values = c.Drop
	}
	for _, v := range values {
		if normalizeCapability(v) == normalizeCapability(want) {
			return true
		}
	}
	return false
}

func (e *podEvaluator) checkImage(image, field string) {
	name, digest, pinned := strings.Cut(image, "@")
	if pinned {
		if validImageDigest(digest) {
			return
		}
		e.add("imageReference", field, "digest syntax not supported or invalid", data.SeverityInfo, data.ResultUnknown, "Image pin cannot be verified from this reference", "Use a valid sha256 or sha512 digest")
	}
	e.add("imageDigest", field, "no digest configured", data.SeverityMedium, data.ResultFinding, "Image reference can change without a spec change", "Pin a verified image digest", data.AssessmentUnspecified)
	last := name[strings.LastIndex(name, "/")+1:]
	_, tag, tagged := strings.Cut(last, ":")
	if !tagged || tag == "" || tag == "latest" {
		state := data.AssessmentUnspecified
		evidence := "tag omitted (latest default)"
		if tagged && tag == "latest" {
			evidence = "latest tag"
			state = data.AssessmentUnsafe
		}
		e.add("imageLatest", field, evidence, data.SeverityMedium, data.ResultFinding, "Latest reference is mutable", "Use an immutable digest", state)
	}
}

func (e *podEvaluator) checkLimits(p *corev1.Pod, c corev1.Container, path string) {
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		q, exists := c.Resources.Limits[name]
		field := path + ".resources.limits." + string(name)
		if !exists && p.Spec.Resources != nil {
			q, exists = p.Spec.Resources.Limits[name]
			if exists {
				field = "spec.resources.limits." + string(name)
			}
		}
		if !exists || q.Sign() <= 0 {
			state := data.AssessmentUnspecified
			evidence := "absent at container and pod levels"
			if exists {
				evidence = "non-positive limit: " + q.String()
				state = data.AssessmentUnsafe
			}
			e.add("limit-"+string(name), field, evidence, data.SeverityMedium, data.ResultFinding, "Availability risk: no positive resource bound configured", "Set a suitable positive limit", state)
		}
	}
}

func validImageDigest(digest string) bool {
	algorithm, value, ok := strings.Cut(digest, ":")
	length := 0
	switch algorithm {
	case "sha256":
		length = 64
	case "sha512":
		length = 128
	}
	if !ok || length == 0 || len(value) != length {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func normalizeCapability(c corev1.Capability) string {
	return strings.TrimPrefix(strings.ToUpper(string(c)), "CAP_")
}

func effectiveCapability(c *corev1.Capabilities, want corev1.Capability) bool {
	if c == nil {
		return false
	}
	all := hasCapability(c, "ALL", false) && !hasCapability(c, "ALL", true)
	if normalizeCapability(want) == "ALL" {
		return all
	}
	return (all || hasCapability(c, want, false)) && !hasCapability(c, want, true)
}

func hasEffectiveCapabilityAdd(c *corev1.Capabilities) bool {
	if c == nil {
		return false
	}
	for _, capability := range c.Add {
		if effectiveCapability(c, capability) {
			return true
		}
	}
	return false
}

func capabilityDropEvidence(c *corev1.Capabilities) string {
	if len(c.Drop) == 0 {
		return "none"
	}
	values := make([]string, len(c.Drop))
	for i, capability := range c.Drop {
		values[i] = string(capability)
	}
	return strings.Join(values, ", ")
}

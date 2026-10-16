package kube

import (
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
	corev1 "k8s.io/api/core/v1"
)

func securityReferencePod() *corev1.Pod {
	p := hardenedPod()
	p.Spec.SecurityContext = nil
	p.Spec.Containers[0].SecurityContext = nil
	p.Spec.Containers[0].Image = "registry/app:v1"
	p.Spec.Containers[0].Resources = corev1.ResourceRequirements{}
	return p
}

func TestSecurityV2Calibration(t *testing.T) {
	tests := []struct {
		name              string
		build             func() *corev1.Pod
		security, hygiene int
		grade             string
	}{
		{"hardened", hardenedPod, 100, 100, "A"},
		{"default", securityReferencePod, 76, 82, "C"},
		{"sidecar", func() *corev1.Pod {
			p := securityReferencePod()
			c := p.Spec.Containers[0].DeepCopy()
			c.Name = "sidecar"
			p.Spec.Containers = append(p.Spec.Containers, *c)
			return p
		}, 76, 82, "C"},
		{"privileged", func() *corev1.Pod {
			p := securityReferencePod()
			p.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{Privileged: ptr(true)}
			return p
		}, 2, 82, "F"},
		{"hostPath", func() *corev1.Pod {
			p := securityReferencePod()
			p.Spec.Volumes = []corev1.Volume{{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/tmp"}}}}
			return p
		}, 36, 82, "F"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := evaluatePod(tc.build(), "test", 1, fixedNow)
			scores := data.ScorePodPosture(p)
			if scores.Security.Value != tc.security || scores.Security.Grade != tc.grade || scores.Hygiene.Value != tc.hygiene {
				t.Fatalf("calibration security=%+v hygiene=%+v", scores.Security, scores.Hygiene)
			}
			if p.RuleVersion != data.SecurityRuleVersion {
				t.Fatal("rule version missing")
			}
			raw := p
			raw.Assessments = nil
			if fromRaw := data.ScorePodPosture(raw); fromRaw != scores {
				t.Fatal("assessment scorer differs from evidence scorer")
			}
			if got := data.ScoreRuleAssessments(p.Coverage, data.AssessPod(p)); got != scores {
				t.Fatal("public pure scorer differs")
			}
		})
	}
}

func TestSecurityV2AssessmentStates(t *testing.T) {
	p := securityReferencePod()
	posture := evaluatePod(p, "test", 1, fixedNow)
	states := make(map[string]data.AssessmentState)
	for _, a := range posture.Assessments {
		states[a.Rule] = a.State
	}
	for rule, want := range map[string]data.AssessmentState{"allowPrivilegeEscalation": data.AssessmentUnspecified, "runAsRoot": data.AssessmentUnknown, "privileged": data.AssessmentConformant} {
		if states[rule] != want {
			t.Fatalf("%s state %s", rule, states[rule])
		}
	}
	p.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{AllowPrivilegeEscalation: ptr(true), Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"SYS_ADMIN"}}}
	posture = evaluatePod(p, "test", 1, fixedNow)
	for _, name := range []string{"allowPrivilegeEscalation", "capabilitiesAdded"} {
		f, ok := rule(posture, name)
		if !ok || f.Severity != data.SeverityHigh || f.Assessment != data.AssessmentUnsafe {
			t.Fatalf("explicit risk: %+v", f)
		}
	}
	p.Spec.OS = &corev1.PodOS{Name: corev1.Windows}
	posture = evaluatePod(p, "test", 1, fixedNow)
	for _, a := range posture.Assessments {
		if a.Rule == "privileged" && a.State != data.AssessmentNotApplicable {
			t.Fatal("Windows rule applied")
		}
	}
}

func TestSecurityV2ExplicitVersusMissing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sc       *corev1.SecurityContext
		rule     string
		severity data.Severity
		state    data.AssessmentState
	}{
		{"escalation missing", nil, "allowPrivilegeEscalation", data.SeverityMedium, data.AssessmentUnspecified},
		{"escalation enabled", &corev1.SecurityContext{AllowPrivilegeEscalation: ptr(true)}, "allowPrivilegeEscalation", data.SeverityHigh, data.AssessmentUnsafe},
		{"forced escalation", &corev1.SecurityContext{Privileged: ptr(true), AllowPrivilegeEscalation: ptr(false)}, "allowPrivilegeEscalation", data.SeverityHigh, data.AssessmentUnsafe},
		{"seccomp missing", nil, "seccomp", data.SeverityMedium, data.AssessmentUnspecified},
		{"seccomp unconfined", &corev1.SecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}}, "seccomp", data.SeverityHigh, data.AssessmentUnsafe},
		{"root explicit", &corev1.SecurityContext{RunAsUser: ptr(int64(0))}, "runAsRoot", data.SeverityHigh, data.AssessmentUnsafe},
		{"root unknown", nil, "runAsRoot", data.SeverityInfo, data.AssessmentUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := securityReferencePod()
			p.Spec.Containers[0].SecurityContext = tc.sc
			f, ok := rule(evaluatePod(p, "test", 1, fixedNow), tc.rule)
			if !ok || f.Severity != tc.severity || f.Assessment != tc.state {
				t.Fatalf("assessment %+v", f)
			}
		})
	}
}

func TestSecurityAssessmentEvidencePerContainer(t *testing.T) {
	p := hardenedPod()
	second := p.Spec.Containers[0].DeepCopy()
	second.Name = "sidecar"
	second.SecurityContext.Privileged = ptr(true)
	p.Spec.Containers = append(p.Spec.Containers, *second)
	posture := evaluatePod(p, "test", 1, fixedNow)
	for _, a := range posture.Assessments {
		if a.Rule != "privileged" {
			continue
		}
		states := map[string]data.AssessmentState{}
		for _, e := range a.Evidence {
			states[e.Container] = e.Assessment
		}
		if a.State != data.AssessmentUnsafe || states[p.Spec.Containers[0].Name] != data.AssessmentConformant || states["sidecar"] != data.AssessmentUnsafe {
			t.Fatalf("per-container evidence %+v", a)
		}
		return
	}
	t.Fatal("privileged assessment missing")
}

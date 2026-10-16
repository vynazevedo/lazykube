package kube

import (
	"github.com/vynazevedo/lazykube/internal/data"
	corev1 "k8s.io/api/core/v1"
	"testing"
)

func TestSecurityEffectiveCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name       string
		add, drop  []corev1.Capability
		severity   data.Severity
		escalation bool
		score      int
	}{
		{"all", []corev1.Capability{"ALL"}, nil, data.SeverityHigh, true, 14},
		{"sysadmin", []corev1.Capability{"SYS_ADMIN"}, nil, data.SeverityHigh, true, 14},
		{"prefix", []corev1.Capability{"CAP_SYS_ADMIN"}, nil, "", false, 94},
		{"all minus sysadmin", []corev1.Capability{"ALL"}, []corev1.Capability{"SYS_ADMIN"}, data.SeverityHigh, false, 54},
		{"drop all add bind", []corev1.Capability{"NET_BIND_SERVICE"}, []corev1.Capability{"ALL"}, data.SeverityMedium, false, 94},
		{"drop all add all", []corev1.Capability{"ALL"}, []corev1.Capability{"ALL"}, "", false, 100},
		{"all prefix drop", []corev1.Capability{"ALL"}, []corev1.Capability{"CAP_SYS_ADMIN"}, data.SeverityHigh, true, 14},
		{"all prefix all drop", []corev1.Capability{"ALL"}, []corev1.Capability{"CAP_ALL"}, data.SeverityHigh, true, 14},
		{"prefix all add", []corev1.Capability{"CAP_ALL"}, []corev1.Capability{"ALL"}, "", false, 100},
		{"drop explicit", []corev1.Capability{"SYS_ADMIN"}, []corev1.Capability{"sys_admin"}, "", false, 94},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := hardenedPod()
			p.Spec.Containers[0].SecurityContext.Capabilities = &corev1.Capabilities{Add: tc.add, Drop: tc.drop}
			posture := evaluatePod(p, "test", 1, fixedNow)
			f, found := rule(posture, "capabilitiesAdded")
			found = found && f.Result == data.ResultFinding
			if found != (tc.severity != "") || found && f.Severity != tc.severity {
				t.Errorf("capabilities: %+v found=%v", f, found)
			}
			ef, escalation := rule(posture, "allowPrivilegeEscalation")
			escalation = escalation && ef.Result == data.ResultFinding
			if escalation != tc.escalation {
				t.Errorf("escalation=%v want %v", escalation, tc.escalation)
			}
			scores := data.ScorePodPosture(posture)
			if scores.Security.Value != tc.score || scores.Hygiene.Value != 100 {
				t.Errorf("scores=%+v want security %d hygiene 100", scores, tc.score)
			}
		})
	}
}

func TestSecurityUnsupportedCapabilityIsUnknown(t *testing.T) {
	for _, name := range []corev1.Capability{"CAP_SYS_ADMIN", "CAP_ALL", "cap_sys_admin", "all", "NOT_A_CAPABILITY"} {
		t.Run(string(name), func(t *testing.T) {
			p := hardenedPod()
			p.Spec.Containers[0].SecurityContext.Capabilities.Add = []corev1.Capability{name}
			posture := evaluatePod(p, "test", 1, fixedNow)
			for _, ruleName := range []string{"capabilitiesAdded", "allowPrivilegeEscalation"} {
				f, ok := rule(posture, ruleName)
				if !ok || f.Result != data.ResultUnknown || f.Assessment != data.AssessmentUnknown {
					t.Errorf("%s must remain unknown: %+v", ruleName, f)
				}
			}
		})
	}
}

func TestSecurityUnsupportedDropCannotMitigate(t *testing.T) {
	for _, drop := range []corev1.Capability{"CAP_SYS_ADMIN", "cap_sys_admin", "CAP_ALL", "all", "NOT_A_CAPABILITY"} {
		t.Run(string(drop), func(t *testing.T) {
			p := hardenedPod()
			p.Spec.Containers[0].SecurityContext.Capabilities = &corev1.Capabilities{Add: []corev1.Capability{"ALL"}, Drop: []corev1.Capability{drop}}
			posture := evaluatePod(p, "test", 1, fixedNow)
			unknown := false
			for _, f := range posture.Findings {
				if f.Rule == "capabilitiesDrop" && f.Result == data.ResultUnknown && f.Assessment == data.AssessmentUnknown {
					unknown = true
				}
			}
			escalation, found := rule(posture, "allowPrivilegeEscalation")
			if !unknown || !found || escalation.Severity != data.SeverityHigh || data.ScorePodPosture(posture).Security.Value != 14 {
				t.Fatalf("unsupported drop changed known risk: %+v", posture)
			}
		})
	}
}

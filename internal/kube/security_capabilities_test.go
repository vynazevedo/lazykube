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
		{"prefix", []corev1.Capability{"cap_sys_admin"}, nil, data.SeverityHigh, true, 14},
		{"all minus sysadmin", []corev1.Capability{"ALL"}, []corev1.Capability{"CAP_SYS_ADMIN"}, data.SeverityHigh, false, 54},
		{"drop all add bind", []corev1.Capability{"CAP_NET_BIND_SERVICE"}, []corev1.Capability{"cap_all"}, data.SeverityMedium, false, 94},
		{"drop all add all", []corev1.Capability{"ALL"}, []corev1.Capability{"ALL"}, "", false, 100},
		{"drop explicit", []corev1.Capability{"SYS_ADMIN"}, []corev1.Capability{"sys_admin"}, "", false, 94},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := hardenedPod()
			p.Spec.Containers[0].SecurityContext.Capabilities = &corev1.Capabilities{Add: tc.add, Drop: tc.drop}
			posture := evaluatePod(p, "test", 1, fixedNow)
			f, found := rule(posture, "capabilitiesAdded")
			if found != (tc.severity != "") || found && f.Severity != tc.severity {
				t.Errorf("capabilities: %+v found=%v", f, found)
			}
			_, escalation := rule(posture, "allowPrivilegeEscalation")
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

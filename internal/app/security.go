package app

import (
	"fmt"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/ui"
)

func (m *Model) loadSecurity() {
	if src, ok := m.src.(data.SecurityRevisionSource); ok && m.security.Coverage.State != "" && src.SecurityRevision() == m.security.Revision {
		return
	}
	m.security = data.SecuritySnapshot{Coverage: data.Coverage{State: data.CoverageUnsupported, Reason: "source does not provide posture"}}
	if src, ok := m.src.(data.SecuritySource); ok {
		m.security = src.Security()
	}
	m.securityKey = ""
}

func (m *Model) refreshSecurity() {
	pod, namespace := m.activeName(), m.nsFilter
	if p := m.selectedPod(); p != nil {
		namespace = p.Namespace
	}
	width, _ := ui.DetailsBody(m.layout.Details.W, m.layout.Details.H)
	key := fmt.Sprintf("%s|%s|%t|%d", pod, namespace, m.securityNamespace, width)
	if m.securityKey == key {
		return
	}
	m.securityKey = key
	m.securityView = ui.BuildSecurityView(m.security, pod, namespace, m.securityNamespace || pod == "", max(1, width-4))
}

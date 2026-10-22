package app

import (
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func (m *Model) applyLogs(logs []data.LogLine) {
	old := m.logs
	m.logs = logs
	if m.follow || len(old) == 0 || len(logs) == 0 {
		return
	}
	last := old[len(old)-1]
	pos := -1
	for i := len(logs) - 1; i >= 0; i-- {
		if logs[i] == last {
			pos = i
			break
		}
	}
	if pos < 0 {
		return
	}
	width := ui.LogWidth(m.layout.Details.W)
	added := len(ui.LogRows(logs[pos+1:], width, m.wrap))
	dropped := len(old) - 1 - pos
	if dropped > 0 {
		m.scroll -= len(ui.LogRows(old[:dropped], width, m.wrap))
	}
	m.newLines += added
}

func (m *Model) setTab(t ui.Tab) {
	if m.tab == t {
		return
	}
	m.tab = t
	m.scroll, m.follow, m.newLines = 0, true, 0
	m.refreshEvents(m.detailTarget())
	m.trackClusterFeed()
	if t == ui.TabLog {
		m.scroll = m.maxScroll()
	}
	m.startSlide()
}

func (m Model) pageSize() int {
	return max(1, ui.ScrollRows(m.tab, m.layout.Details.H)-1)
}

func (m Model) scrollTotal() int {
	switch m.tab {
	case ui.TabSecurity:
		return len(m.securityView.Rows)
	case ui.TabLog:
		return len(ui.LogRows(m.logs, ui.LogWidth(m.layout.Details.W), m.wrap))
	case ui.TabConfig:
		return len(m.manifest)
	case ui.TabDescribe:
		return len(ui.DescribeRows(ui.Diagnosis{Containers: m.dg.containers}, m.describe))
	case ui.TabEvents:
		return len(m.events.Items)
	default:
		return 0
	}
}

func (m Model) maxScroll() int {
	return max(0, m.scrollTotal()-ui.ScrollRows(m.tab, m.layout.Details.H))
}

func (m *Model) scrollTo(v int) {
	limit := m.maxScroll()
	m.scroll = max(0, min(v, limit))
	if m.tab == ui.TabLog {
		m.follow = m.scroll >= limit
		if m.follow {
			m.newLines = 0
		}
	}
}

func (m *Model) clampScroll() {
	if m.tab == ui.TabLog && m.follow {
		m.scroll = m.maxScroll()
		m.newLines = 0
		return
	}
	m.scroll = max(0, min(m.scroll, m.maxScroll()))
}

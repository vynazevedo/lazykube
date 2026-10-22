package app

import (
	"github.com/sahilm/fuzzy"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func (m Model) visiblePods() []data.Pod {
	pods := make([]data.Pod, 0, len(m.snap.Pods))
	node, byNode := m.nodeScope()
	for _, p := range m.snap.Pods {
		if (m.nsFilter == "" || p.Namespace == m.nsFilter) && (!byNode || (node != "" && p.Node == node)) {
			pods = append(pods, p)
		}
	}
	q := m.filter.Value()
	if q == "" {
		return pods
	}
	targets := make([]string, len(pods))
	for i, p := range pods {
		targets[i] = p.Name + " " + p.Namespace
	}
	keep := make([]bool, len(pods))
	for _, match := range fuzzy.Find(q, targets) {
		keep[match.Index] = true
	}
	out := pods[:0]
	for i, p := range pods {
		if keep[i] {
			out = append(out, p)
		}
	}
	return out
}

func (m Model) selectedIndex(pods []data.Pod) int {
	for i, p := range pods {
		if p.Key() == m.selName {
			return i
		}
	}
	return -1
}

func (m Model) selectedPod() *data.Pod {
	if r := m.nav.Active(); r.Kind == RoutePod {
		for _, p := range m.snap.Pods {
			if p.Key() == r.Pod {
				return &p
			}
		}
		return nil
	}
	for _, p := range m.visiblePods() {
		if p.Key() == m.selName {
			return &p
		}
	}
	return nil
}

func (m *Model) activeName() string {
	if p := m.selectedPod(); p != nil {
		return p.Key()
	}
	return ""
}

func (m *Model) selectPod(i int) {
	pods := m.visiblePods()
	if len(pods) == 0 {
		return
	}
	i = max(0, min(len(pods)-1, i))
	if next := pods[i].Key(); next != m.selName {
		previous := m.activeName()
		fromK, toK := m.selectionK(previous), m.selectionK(next)
		m.selName = next
		m.startSelectionFade(previous, fromK, next, toK)
		m.follow = true
		m.newLines = 0
		m.logs = nil
		m.resetLogTarget()
		m.refresh()
	}
	m.ensurePodVisible()
}

func (m *Model) syncSelection() {
	if m.inDetail() {
		m.refresh()
		return
	}
	pods := m.visiblePods()
	if len(pods) > 0 && m.selectedIndex(pods) < 0 {
		m.selectPod(0)
	}
	m.ensurePodVisible()
	m.refresh()
}

func (m *Model) ensurePodVisible() {
	pods := m.visiblePods()
	rows := ui.PodsVisibleRows(m.layout.Pods.H)
	if rows <= 0 {
		m.podOffset = 0
		return
	}
	idx := max(0, m.selectedIndex(pods))
	if idx < m.podOffset {
		m.podOffset = idx
	}
	if idx >= m.podOffset+rows {
		m.podOffset = idx - rows + 1
	}
	m.podOffset = max(0, min(m.podOffset, max(0, len(pods)-rows)))
}

func (m *Model) focusOrder() []Focus {
	if m.onScreen(ScreenWorkloads) {
		return []Focus{FocusPods, FocusDeployments, FocusDetails}
	}
	if m.onScreen(ScreenNodes) && m.layout.Compact {
		return []Focus{FocusPods, FocusDetails}
	}
	if m.onScreen(ScreenNodes) || m.onScreen(ScreenEvents) {
		return []Focus{FocusPods}
	}
	order := []Focus{FocusPods, FocusDetails}
	if m.layout.Compact {
		order = []Focus{FocusPods, FocusHeatmap, FocusDetails}
	}
	if m.layout.ShowLeft {
		return append([]Focus{FocusNamespaces, FocusDeployments}, order...)
	}
	return order
}

func (m *Model) cycleFocus(delta int) {
	order := m.focusOrder()
	idx := 0
	for i, f := range order {
		if f == m.focus {
			idx = i
		}
	}
	m.setFocus(order[((idx+delta)%len(order)+len(order))%len(order)])
}

func (m Model) selectionKs(pods []data.Pod) map[int]float64 {
	out := make(map[int]float64, 2)
	for i, p := range pods {
		if k := m.selectionK(p.Key()); k > 0 {
			out[i] = k
		}
	}
	return out
}

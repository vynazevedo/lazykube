package app

import (
	"github.com/sahilm/fuzzy"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/ui"
)

func (m Model) visiblePods() []data.Pod {
	pods := make([]data.Pod, 0, len(m.snap.Pods))
	for _, p := range m.snap.Pods {
		if m.nsFilter == "" || p.Namespace == m.nsFilter {
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
		m.refresh()
	}
	m.ensurePodVisible()
}

func (m *Model) syncSelection() {
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
	if m.layout.ShowLeft {
		return []Focus{FocusNamespaces, FocusDeployments, FocusPods, FocusDetails}
	}
	return []Focus{FocusPods, FocusDetails}
}

func (m *Model) cycleFocus(delta int) {
	order := m.focusOrder()
	idx := 0
	for i, f := range order {
		if f == m.focus {
			idx = i
		}
	}
	m.focus = order[((idx+delta)%len(order)+len(order))%len(order)]
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

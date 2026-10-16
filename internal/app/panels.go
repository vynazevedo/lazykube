package app

import (
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func (m Model) podsPanelState(pods []data.Pod) ui.PodsState {
	return ui.PodsState{
		Pods:       pods,
		Selected:   m.selectedIndex(pods),
		Offset:     m.podOffset,
		Hover:      m.hover,
		CPU:        m.metrics.PodCPU,
		Filter:     m.filter.Value(),
		FilterPos:  m.filter.Position(),
		Filtering:  m.filtering,
		Focused:    m.focus == FocusPods,
		SelectionK: m.selectionKs(pods),
		NSFilter:   m.nsFilter,
	}
}

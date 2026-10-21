package app

import (
	"math"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/anim"
)

const (
	IdleFPS          = 10
	fadeDuration     = 0.18
	selectionTarget  = 0.22
	slideColumns     = 4
	inputBusySeconds = 2.0
)

type motion struct {
	nodeCPU   anim.Spring
	nodeMem   anim.Spring
	memBar    anim.Spring
	slide     anim.Spring
	fades     map[string]anim.Fade
	lastInput float64
}

func newMotion(fps int) motion {
	return motion{
		nodeCPU:   anim.NewSpring(fps, 0),
		nodeMem:   anim.NewSpring(fps, 0),
		memBar:    anim.NewSpring(fps, 0),
		slide:     anim.NewSpring(fps, 0),
		fades:     map[string]anim.Fade{},
		lastInput: math.Inf(-1),
	}
}

func (m *Model) springs() []*anim.Spring {
	return []*anim.Spring{&m.motion.nodeCPU, &m.motion.nodeMem, &m.motion.memBar, &m.motion.slide}
}

func (m Model) running() bool {
	return m.animated && !m.paused && !m.blurred
}

func (m *Model) setBarTargets() {
	m.motion.nodeCPU.SetTarget(float64(m.metrics.NodeCPU) / 100)
	m.motion.nodeMem.SetTarget(m.snap.Node.MemFraction)
	mem := 0.0
	if p := m.selectedPod(); p != nil && p.MemLimit > 0 {
		mem = float64(m.metrics.MemNow) / float64(p.MemLimit)
	}
	m.motion.memBar.SetTarget(mem)
	if !m.running() {
		m.snapMotion()
	}
}

func (m *Model) snapMotion() {
	for _, s := range m.springs() {
		s.Snap()
	}
	m.motion.fades = map[string]anim.Fade{}
}

func (m *Model) stepMotion() {
	for _, s := range m.springs() {
		if !s.AtRest() {
			s.Step()
		}
	}
	for name, f := range m.motion.fades {
		if f.Done(m.clock.Phase) {
			delete(m.motion.fades, name)
		}
	}
}

func (m Model) busy() bool {
	for _, s := range []anim.Spring{m.motion.nodeCPU, m.motion.nodeMem, m.motion.memBar, m.motion.slide} {
		if !s.AtRest() {
			return true
		}
	}
	if len(m.motion.fades) > 0 {
		return true
	}
	return m.clock.Phase-m.motion.lastInput < inputBusySeconds
}

func (m Model) frameRate() int {
	if m.busy() {
		return m.fps
	}
	return min(m.fps, IdleFPS)
}

func (m Model) scheduleTick() tea.Cmd {
	if !m.running() {
		return nil
	}
	return frameCmd(m.frameRate(), m.gen, m.session.Generation)
}

func (m *Model) restartTick() tea.Cmd {
	m.gen++
	return m.scheduleTick()
}

func (m *Model) noteInput() {
	m.motion.lastInput = m.clock.Phase
}

func (m *Model) selectionK(name string) float64 {
	if f, ok := m.motion.fades[name]; ok {
		return f.At(m.clock.Phase)
	}
	if name != "" && name == m.selName {
		return selectionTarget
	}
	return 0
}

func (m *Model) startSelectionFade(from string, fromK float64, to string, toK float64) {
	if !m.running() {
		return
	}
	if from != "" && from != to {
		m.motion.fades[from] = anim.Fade{From: fromK, To: 0, Start: m.clock.Phase, Duration: fadeDuration}
	}
	if to != "" {
		m.motion.fades[to] = anim.Fade{From: toK, To: selectionTarget, Start: m.clock.Phase, Duration: fadeDuration}
	}
}

func (m *Model) startSlide() {
	if !m.running() {
		return
	}
	m.motion.slide.Pos = slideColumns
	m.motion.slide.Vel = 0
	m.motion.slide.SetTarget(0)
}

func (m Model) slideOffset() int {
	return max(0, int(math.Round(m.motion.slide.Pos)))
}

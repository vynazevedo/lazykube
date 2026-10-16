package app

import (
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const (
	DefaultFPS = 30
	MaxFPS     = 60
	logBuffer  = 500
)

type Focus int

const (
	FocusNamespaces Focus = iota
	FocusDeployments
	FocusPods
	FocusDetails
)

type Options struct {
	Source     data.Source
	Palette    theme.Palette
	FPS        int
	NoAnim     bool
	InitialPod int
	Now        func() time.Time
}

type Model struct {
	nav router

	security          data.SecuritySnapshot
	securitySession   data.SecuritySession
	securityKey       string
	securityNamespace bool
	securityView      ui.SecurityView

	w, h     int
	layout   Layout
	leftMode LeftMode

	focus    Focus
	tab      ui.Tab
	hoverTab int
	pal      theme.Palette
	clock    anim.Clock
	motion   motion
	animated bool
	paused   bool
	blurred  bool
	fps      int
	gen      int
	now      func() time.Time

	keys      KeyMap
	filter    textinput.Model
	filtering bool

	src      data.Source
	snap     data.Snapshot
	metrics  data.Metrics
	logs     []data.LogLine
	events   data.EventFeed
	dg       diagState
	manifest []string
	describe []data.Field

	selName   string
	podOffset int
	hover     int
	nsCursor  int
	nsFilter  string
	depCursor int

	scroll   int
	follow   bool
	wrap     bool
	newLines int
	helpOpen bool
}

func New(opts Options) Model {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	fps := opts.FPS
	if fps <= 0 {
		fps = DefaultFPS
	}
	fps = min(fps, MaxFPS)
	pal := opts.Palette
	if pal.Name == "" {
		pal = theme.DefaultPalette()
	}
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "filter pods…"
	m := Model{
		focus:    FocusPods,
		tab:      ui.TabTop,
		hoverTab: -1,
		hover:    -1,
		pal:      pal,
		animated: !opts.NoAnim,
		fps:      fps,
		now:      now,
		keys:     DefaultKeyMap(),
		filter:   ti,
		src:      opts.Source,
		dg:       newDiagState(opts.Source),
		follow:   true,
		motion:   newMotion(fps),
		nav:      newRouter(),
	}
	if m.animated {
		m.clock = anim.NewClock(now(), fps)
	} else {
		m.clock = anim.Fixed(0, 0, 0)
	}
	m.snap = m.src.Snapshot()
	m.loadSecurity()
	if opts.InitialPod >= 0 && opts.InitialPod < len(m.snap.Pods) {
		m.selName = m.snap.Pods[opts.InitialPod].Key()
	} else if len(m.snap.Pods) > 0 {
		m.selName = m.snap.Pods[0].Key()
	}
	m.refresh()
	m.snapMotion()
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.scheduleTick(), m.waitData(), m.screen().Init(m))
}

func (m Model) waitData() tea.Cmd {
	live, ok := m.src.(data.Live)
	if !ok {
		return nil
	}
	return waitChange(live.Changes())
}

func (m *Model) reload() {
	m.snap = m.src.Snapshot()
	m.loadSecurity()
	m.nsCursor = max(0, min(m.nsCursor, len(m.snap.Namespaces)-1))
	m.depCursor = max(0, min(m.depCursor, len(m.snap.Deployments)-1))
	pods := m.visiblePods()
	if m.selectedIndex(pods) < 0 && len(pods) > 0 {
		m.selName = pods[0].Key()
		m.follow, m.newLines, m.logs = true, 0, nil
		m.resetLogTarget()
	}
	m.ensurePodVisible()
	m.refresh()
}

func (m *Model) setLayout(w, h int) {
	m.w, m.h = w, h
	m.layout = Compute(w, h, m.leftMode)
	if !m.layout.ShowLeft && (m.focus == FocusNamespaces || m.focus == FocusDeployments) {
		m.focus = FocusPods
	}
	m.ensurePodVisible()
}

func (m *Model) heatCols() int {
	if m.layout.Heatmap.W > 0 {
		return ui.HeatCols(m.layout.Heatmap.W)
	}
	return 36
}

func (m *Model) refresh() {
	if demo, ok := m.src.(*data.Demo); ok && demo.AdvanceSecurity(m.clock.T) {
		m.loadSecurity()
	}
	m.refreshSecurity()
	name := m.activeName()
	m.metrics = m.src.Metrics(m.clock.T, name, m.heatCols())
	m.refreshLogs(name)
	m.refreshEvents(name)
	m.manifest, m.describe, m.dg.containers = nil, nil, nil
	if name != "" {
		m.manifest = m.src.Manifest(name)
		m.describe = m.src.Describe(name)
		m.dg.containers = m.dg.diag.Containers(name)
	}
	m.updateMatches()
	m.clampScroll()
	m.setBarTargets()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dataMsg:
		m.reload()
		return m, m.waitData()
	case frameMsg:
		if msg.gen != m.gen || !m.running() {
			return m, nil
		}
		prev := m.clock.T
		m.clock.Advance(msg.at)
		if m.clock.T != prev {
			m.refresh()
		}
		m.stepMotion()
		return m, m.scheduleTick()
	case tea.FocusMsg:
		if !m.blurred {
			return m, nil
		}
		m.blurred = false
		if !m.paused {
			m.clock.Resume(m.now())
		}
		cmd := m.restartTick()
		return m, cmd
	case tea.BlurMsg:
		if m.blurred {
			return m, nil
		}
		m.blurred = true
		m.clock.Pause(m.now())
		m.snapMotion()
		cmd := m.restartTick()
		return m, cmd
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return wake(!m.busy(), m, tea.Quit)
		}
		return m.delegateInput(msg)
	case tea.MouseClickMsg, tea.MouseWheelMsg:
		return m.delegateInput(msg)
	}
	return m.screen().Update(m, msg)
}

func (m Model) delegateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	idle := !m.busy()
	next, cmd := m.screen().Update(m, msg)
	return wake(idle, next, cmd)
}

func wake(wasIdle bool, nm Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	nm.noteInput()
	if wasIdle && nm.running() {
		tick := nm.restartTick()
		return nm, tea.Batch(cmd, tick)
	}
	return nm, cmd
}

func (m *Model) togglePause() tea.Cmd {
	if !m.animated {
		return nil
	}
	m.paused = !m.paused
	if m.paused {
		m.clock.Pause(m.now())
		m.snapMotion()
	} else if !m.blurred {
		m.clock.Resume(m.now())
	}
	return m.restartTick()
}

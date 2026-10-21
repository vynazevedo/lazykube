package app

import (
	"slices"
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
	Session    data.SessionRef
}

type Model struct {
	nav  router
	menu menuState
	dep  depOverlay

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
	session  data.SessionRef
	snap     data.Snapshot
	metrics  data.Metrics
	logs     []data.LogLine
	events   data.EventFeed
	dg       diagState
	ev       eventsView
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
		session:  opts.Session,
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
	return waitChange(live.Changes(), m.session.Generation)
}

func (m *Model) reload() {
	m.snap = m.src.Snapshot()
	m.loadSecurity()
	m.fixSelection()
	m.refresh()
}

func (m *Model) fixSelection() {
	m.nsCursor = max(0, min(m.nsCursor, len(m.snap.Namespaces)-1))
	m.depCursor = max(0, min(m.depCursor, len(m.visibleDeployments())-1))
	if !m.inDetail() {
		pods := m.visiblePods()
		if m.selectedIndex(pods) < 0 && len(pods) > 0 {
			m.selName = pods[0].Key()
			m.follow, m.newLines, m.logs = true, 0, nil
			m.resetLogTarget()
		}
	}
	m.ensurePodVisible()
}

func (m *Model) relayout() {
	m.setLayout(m.w, m.h)
}

func (m *Model) setLayout(w, h int) {
	m.w, m.h = w, h
	switch {
	case m.inDetail():
		m.layout = ComputeDetail(w, h)
	case m.onScreen(ScreenWorkloads):
		m.layout = ComputeWorkloads(w, h)
	case m.onScreen(ScreenNodes):
		m.layout = ComputeNodes(w, h)
	case m.onScreen(ScreenEvents):
		m.layout = ComputeEvents(w, h)
	default:
		m.layout = Compute(w, h, m.leftMode)
	}
	if !slices.Contains(m.focusOrder(), m.focus) {
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
	m.metrics = m.src.Metrics(m.clock.T, m.activeName(), m.heatCols())
	name := m.detailTarget()
	m.refreshLogs(name)
	m.refreshEvents(name)
	m.manifest, m.describe, m.dg.containers = nil, nil, nil
	if name != "" {
		m.manifest = m.src.Manifest(name)
		m.describe = m.src.Describe(name)
		m.dg.containers = m.dg.diag.Containers(name)
	}
	m.refreshEventsScreen()
	m.updateMatches()
	m.clampScroll()
	m.setBarTargets()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dataMsg:
		if msg.session != m.session.Generation {
			return m, nil
		}
		m.reload()
		return m, m.waitData()
	case frameMsg:
		if msg.session != m.session.Generation || msg.gen != m.gen || !m.running() {
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
		idle := !m.busy()
		next, cmd := m.routeKey(msg)
		return wake(idle, next, cmd)
	case tea.MouseClickMsg:
		idle := !m.busy()
		if m.dep.open {
			m.dep.open = false
			return wake(idle, m, nil)
		}
		if cmd := m.headerClick(tea.Mouse(msg)); cmd != nil {
			return wake(idle, m, cmd)
		}
		if next, ok := m.handleShellClick(tea.Mouse(msg)); ok {
			return wake(idle, next, nil)
		}
		next, cmd := m.screen().Update(m, msg)
		return wake(idle, next, cmd)
	case tea.MouseWheelMsg:
		if m.menu.open || m.dep.open {
			return m, nil
		}
		idle := !m.busy()
		next, cmd := m.screen().Update(m, msg)
		return wake(idle, next, cmd)
	}
	return m.screen().Update(m, msg)
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

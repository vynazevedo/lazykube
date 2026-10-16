package app

import (
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/TNK-Studio/lazykube/internal/anim"
	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/theme"
	"github.com/TNK-Studio/lazykube/internal/ui"
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
	events   []data.Event
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
		follow:   true,
		motion:   newMotion(fps),
	}
	if m.animated {
		m.clock = anim.NewClock(now(), fps)
	} else {
		m.clock = anim.Fixed(0, 0, 0)
	}
	m.snap = m.src.Snapshot()
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
	return tea.Batch(m.scheduleTick(), m.waitData())
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
	m.nsCursor = max(0, min(m.nsCursor, len(m.snap.Namespaces)-1))
	m.depCursor = max(0, min(m.depCursor, len(m.snap.Deployments)-1))
	pods := m.visiblePods()
	if m.selectedIndex(pods) < 0 && len(pods) > 0 {
		m.selName = pods[0].Key()
		m.follow, m.newLines, m.logs = true, 0, nil
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
	name := m.activeName()
	m.metrics = m.src.Metrics(m.clock.T, name, m.heatCols())
	var logs []data.LogLine
	if name != "" {
		logs = m.src.Logs(name, m.clock.T, logBuffer)
	}
	m.applyLogs(logs)
	m.events, m.manifest, m.describe = nil, nil, nil
	if name != "" {
		m.events = m.src.Events(name)
		m.manifest = m.src.Manifest(name)
		m.describe = m.src.Describe(name)
	}
	m.clampScroll()
	m.setBarTargets()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.setLayout(msg.Width, msg.Height)
		m.refresh()
		return m, nil
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
		idle := !m.busy()
		next, cmd := m.handleKey(msg)
		return wake(idle, next, cmd)
	case tea.MouseClickMsg:
		idle := !m.busy()
		next, cmd := m.handleClick(tea.Mouse(msg))
		return wake(idle, next, cmd)
	case tea.MouseWheelMsg:
		idle := !m.busy()
		next, cmd := m.handleWheel(tea.Mouse(msg))
		return wake(idle, next, cmd)
	case tea.MouseMotionMsg:
		m.handleMotion(tea.Mouse(msg))
		return m, nil
	}
	if m.filtering {
		var cmd tea.Cmd
		before := m.filter.Value()
		m.filter, cmd = m.filter.Update(msg)
		if m.filter.Value() != before {
			m.syncSelection()
		}
		return m, cmd
	}
	return m, nil
}

func wake(wasIdle bool, next tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	nm, ok := next.(Model)
	if !ok {
		return next, cmd
	}
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

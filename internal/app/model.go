package app

import (
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

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
	}
	if m.animated {
		m.clock = anim.NewClock(now(), fps)
	} else {
		m.clock = anim.Fixed(0, 0, 0)
	}
	m.snap = m.src.Snapshot()
	if opts.InitialPod >= 0 && opts.InitialPod < len(m.snap.Pods) {
		m.selName = m.snap.Pods[opts.InitialPod].Name
	} else if len(m.snap.Pods) > 0 {
		m.selName = m.snap.Pods[0].Name
	}
	m.refresh()
	return m
}

func (m Model) Init() tea.Cmd {
	return m.scheduleTick()
}

func (m Model) scheduleTick() tea.Cmd {
	if m.animated && !m.paused && !m.blurred {
		return frameCmd(m.fps, m.gen)
	}
	return dataTickCmd(m.gen)
}

func (m *Model) restartTick() tea.Cmd {
	m.gen++
	return m.scheduleTick()
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

func (m *Model) activeName() string {
	if p := m.selectedPod(); p != nil {
		return p.Name
	}
	return ""
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
}

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
		if p.Name == m.selName {
			return i
		}
	}
	return -1
}

func (m Model) selectedPod() *data.Pod {
	for _, p := range m.visiblePods() {
		if p.Name == m.selName {
			return &p
		}
	}
	return nil
}

func (m *Model) selectPod(i int) {
	pods := m.visiblePods()
	if len(pods) == 0 {
		return
	}
	i = max(0, min(len(pods)-1, i))
	if pods[i].Name != m.selName {
		m.selName = pods[i].Name
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.setLayout(msg.Width, msg.Height)
		m.refresh()
		return m, nil
	case frameMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		prev := m.clock.T
		m.clock.Advance(msg.at)
		if m.clock.T != prev {
			m.refresh()
		}
		return m, m.scheduleTick()
	case dataTickMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		if !m.animated || m.paused || m.blurred {
			m.clock.Tick()
			m.refresh()
		}
		return m, m.scheduleTick()
	case tea.FocusMsg:
		if m.blurred {
			m.blurred = false
			m.clock.Resume(m.now())
			return m, m.restartTick()
		}
		return m, nil
	case tea.BlurMsg:
		if !m.blurred {
			m.blurred = true
			if m.animated && !m.paused {
				m.clock.Pause(m.now())
			}
			return m, m.restartTick()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseClickMsg:
		return m.handleClick(tea.Mouse(msg))
	case tea.MouseWheelMsg:
		return m.handleWheel(tea.Mouse(msg))
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

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.helpOpen {
		switch {
		case key.Matches(msg, k.Help), key.Matches(msg, k.Esc):
			m.helpOpen = false
		case key.Matches(msg, k.Quit):
			return m, tea.Quit
		}
		return m, nil
	}
	if m.filtering {
		switch {
		case key.Matches(msg, k.Esc):
			m.filtering = false
			m.filter.Blur()
			m.filter.SetValue("")
			m.syncSelection()
			return m, nil
		case key.Matches(msg, k.Enter):
			m.filtering = false
			m.filter.Blur()
			return m, nil
		case key.Matches(msg, k.Up), key.Matches(msg, k.Down):
			if msg.String() == "up" || msg.String() == "down" {
				return m.moveSelection(msg)
			}
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.syncSelection()
		return m, cmd
	}

	switch {
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case key.Matches(msg, k.Help):
		m.helpOpen = true
	case key.Matches(msg, k.NextPanel):
		m.cycleFocus(1)
	case key.Matches(msg, k.PrevPanel):
		m.cycleFocus(-1)
	case key.Matches(msg, k.Filter):
		m.focus = FocusPods
		m.filtering = true
		return m, m.filter.Focus()
	case key.Matches(msg, k.Esc):
		m.nsFilter = ""
		m.filter.SetValue("")
		m.syncSelection()
	case key.Matches(msg, k.Enter):
		if m.focus == FocusNamespaces && m.nsCursor < len(m.snap.Namespaces) {
			m.nsFilter = m.snap.Namespaces[m.nsCursor].Name
			m.syncSelection()
		}
	case key.Matches(msg, k.TabLog):
		m.setTab(ui.TabLog)
	case key.Matches(msg, k.TabConfig):
		m.setTab(ui.TabConfig)
	case key.Matches(msg, k.TabTop):
		m.setTab(ui.TabTop)
	case key.Matches(msg, k.TabDescribe):
		m.setTab(ui.TabDescribe)
	case key.Matches(msg, k.TabEvents):
		m.setTab(ui.TabEvents)
	case key.Matches(msg, k.TabPrev):
		m.setTab(m.tab.Next(-1))
	case key.Matches(msg, k.TabNext):
		m.setTab(m.tab.Next(1))
	case key.Matches(msg, k.Palette):
		m.pal = theme.NextPalette(m.pal)
	case key.Matches(msg, k.ToggleLeft):
		if LeftVisible(m.w, m.leftMode) {
			m.leftMode = LeftHidden
		} else {
			m.leftMode = LeftShown
		}
		m.setLayout(m.w, m.h)
		m.refresh()
	case key.Matches(msg, k.Pause):
		return m, m.togglePause()
	case key.Matches(msg, k.Follow):
		m.follow = !m.follow
		if m.follow {
			m.scrollTo(m.maxScroll())
		}
	case key.Matches(msg, k.Wrap):
		m.wrap = !m.wrap
		if m.tab == ui.TabLog && m.follow {
			m.scrollTo(m.maxScroll())
		}
		m.clampScroll()
	case key.Matches(msg, k.Bottom), key.Matches(msg, k.End):
		m.scrollTo(m.maxScroll())
	case key.Matches(msg, k.Top), key.Matches(msg, k.Home):
		m.scrollTo(0)
	case key.Matches(msg, k.PageUp):
		m.scrollTo(m.scroll - m.pageSize())
	case key.Matches(msg, k.PageDown):
		m.scrollTo(m.scroll + m.pageSize())
	case key.Matches(msg, k.Up), key.Matches(msg, k.Down):
		return m.moveSelection(msg)
	}
	return m, nil
}

func (m *Model) togglePause() tea.Cmd {
	if !m.animated {
		return nil
	}
	m.paused = !m.paused
	if m.paused {
		m.clock.Pause(m.now())
	} else if !m.blurred {
		m.clock.Resume(m.now())
	}
	return m.restartTick()
}

func (m *Model) setTab(t ui.Tab) {
	if m.tab != t {
		m.tab = t
		m.scroll, m.follow, m.newLines = 0, true, 0
		if t == ui.TabLog {
			m.scroll = m.maxScroll()
		}
	}
}

func (m Model) pageSize() int {
	return max(1, ui.ScrollRows(m.tab, m.layout.Details.H)-1)
}

func (m Model) scrollTotal() int {
	switch m.tab {
	case ui.TabLog:
		return len(ui.LogRows(m.logs, ui.LogWidth(m.layout.Details.W), m.wrap))
	case ui.TabConfig:
		return len(m.manifest)
	case ui.TabDescribe:
		return len(m.describe)
	case ui.TabEvents:
		return len(m.events)
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

func (m Model) moveSelection(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	delta := 1
	if key.Matches(msg, m.keys.Up) {
		delta = -1
	}
	switch m.focus {
	case FocusNamespaces:
		m.nsCursor = max(0, min(len(m.snap.Namespaces)-1, m.nsCursor+delta))
	case FocusDeployments:
		m.depCursor = max(0, min(len(m.snap.Deployments)-1, m.depCursor+delta))
	case FocusDetails:
		m.scrollTo(m.scroll + delta)
	default:
		m.selectPod(m.selectedIndex(m.visiblePods()) + delta)
	}
	return m, nil
}

func (m Model) podRowAt(y int) (int, bool) {
	top := m.layout.Pods.Y + 1 + ui.PodsRowsTop
	row := y - top
	if row < 0 || row >= ui.PodsVisibleRows(m.layout.Pods.H) {
		return 0, false
	}
	idx := m.podOffset + row
	if idx >= len(m.visiblePods()) {
		return 0, false
	}
	return idx, true
}

func (m Model) tabAt(x, y int) (ui.Tab, bool) {
	d := m.layout.Details
	if d.Empty() || y != d.Y+1+ui.DetailsTabsRow {
		return 0, false
	}
	return ui.TabAt(x - (d.X + 2))
}

func (m Model) handleClick(ms tea.Mouse) (tea.Model, tea.Cmd) {
	if ms.Button != tea.MouseLeft || m.layout.TooSmall {
		return m, nil
	}
	if m.helpOpen {
		m.helpOpen = false
		return m, nil
	}
	switch m.layout.Hit(ms.X, ms.Y) {
	case AreaNamespaces:
		m.focus = FocusNamespaces
		top, rows := ui.NamespacesRows(m.layout.Namespaces.H)
		if idx, ok := ui.ListRowAt(ms.Y-m.layout.Namespaces.Y, top, rows, m.nsCursor, len(m.snap.Namespaces)); ok {
			m.nsCursor = idx
		}
	case AreaDeployments:
		m.focus = FocusDeployments
		top, rows := ui.DeploymentsRows(m.layout.Deployments.H)
		if idx, ok := ui.ListRowAt(ms.Y-m.layout.Deployments.Y, top, rows, m.depCursor, len(m.snap.Deployments)); ok {
			m.depCursor = idx
		}
	case AreaPods:
		m.focus = FocusPods
		if idx, ok := m.podRowAt(ms.Y); ok {
			m.selectPod(idx)
		}
	case AreaDetails:
		m.focus = FocusDetails
		if t, ok := m.tabAt(ms.X, ms.Y); ok {
			m.setTab(t)
		}
	}
	return m, nil
}

func (m Model) handleWheel(ms tea.Mouse) (tea.Model, tea.Cmd) {
	delta := 0
	switch ms.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return m, nil
	}
	switch m.layout.Hit(ms.X, ms.Y) {
	case AreaPods:
		m.selectPod(m.selectedIndex(m.visiblePods()) + delta)
	case AreaNamespaces:
		m.nsCursor = max(0, min(len(m.snap.Namespaces)-1, m.nsCursor+delta))
	case AreaDeployments:
		m.depCursor = max(0, min(len(m.snap.Deployments)-1, m.depCursor+delta))
	case AreaDetails:
		m.scrollTo(m.scroll + delta*3)
	}
	return m, nil
}

func (m *Model) handleMotion(ms tea.Mouse) {
	m.hover = -1
	m.hoverTab = -1
	if idx, ok := m.podRowAt(ms.Y); ok && m.layout.Hit(ms.X, ms.Y) == AreaPods {
		m.hover = idx
	}
	if t, ok := m.tabAt(ms.X, ms.Y); ok {
		m.hoverTab = int(t)
	}
}

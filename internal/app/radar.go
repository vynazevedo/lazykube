package app

import (
	"cmp"
	"slices"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const radarInterval = time.Second

type radarFeed struct {
	src    insight.ObservationSource
	engine *insight.Engine
	cursor insight.Cursor
	busy   atomic.Bool
}

func (f *radarFeed) run() insight.SignalSnapshot {
	defer f.busy.Store(false)
	batch := f.src.Observations(f.cursor)
	snap := f.engine.Step(batch)
	f.cursor = batch.Cursor
	return snap
}

type radarState struct {
	feed    *radarFeed
	live    bool
	ticking bool
	err     string
	have    bool
	snap    insight.SignalSnapshot
	listed  []insight.Signal
	rows    []insight.Signal
	scope   nsSet
	counts  ui.RadarCounts
	cursor  int
	id      string
	held    insight.Signal
	gone    bool
	scroll  int
}

type radarMsg struct {
	session uint64
	snap    insight.SignalSnapshot
}

type radarTickMsg struct{ session uint64 }

func newRadar(src data.Source, now func() time.Time) radarState {
	obs, ok := src.(insight.ObservationSource)
	if !ok {
		return radarState{}
	}
	clock := insight.ClockFunc(now)
	if demo, ok := src.(*data.Demo); ok {
		demo.ObserveWith(clock)
	}
	engine, err := insight.NewEngine(clock, insight.DefaultOperationalConfig())
	if err != nil {
		return radarState{err: err.Error()}
	}
	_, live := src.(data.Live)
	return radarState{feed: &radarFeed{src: obs, engine: engine}, live: live}
}

func (m Model) radarTick() tea.Cmd {
	session := m.session.Generation
	return m.after(radarInterval, func(time.Time) tea.Msg { return radarTickMsg{session: session} })
}

func (m Model) radarCmd() tea.Cmd {
	if m.radar.feed == nil || !m.radar.feed.busy.CompareAndSwap(false, true) {
		return nil
	}
	feed, session := m.radar.feed, m.session.Generation
	life := m.life
	return func() tea.Msg {
		if life.Err() != nil {
			feed.busy.Store(false)
			return nil
		}
		return radarMsg{session: session, snap: feed.run()}
	}
}

func (m Model) radarInit() tea.Cmd {
	if m.radar.live {
		return m.radarCmd()
	}
	return m.armRadarTick()
}

func timersPending(snap insight.SignalSnapshot) bool {
	return slices.ContainsFunc(snap.Signals, func(s insight.Signal) bool {
		return s.State == insight.SignalCandidate || s.State == insight.SignalActive || s.State == insight.SignalRecovering
	})
}

func (m *Model) armRadarTick() tea.Cmd {
	if m.radar.ticking || m.radar.feed == nil || (!m.radar.live && !m.animated) || !timersPending(m.radar.snap) {
		return nil
	}
	m.radar.ticking = true
	return m.radarTick()
}

func (m Model) updateRadar(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case radarTickMsg:
		if msg.session != m.session.Generation || m.life.Err() != nil {
			return m, nil, true
		}
		m.radar.ticking = false
		return m, m.radarCmd(), true
	case radarMsg:
		if msg.session != m.session.Generation || m.life.Err() != nil {
			return m, nil, true
		}
		m.applyRadar(msg.snap)
		return m, m.armRadarTick(), true
	}
	return m, nil, false
}

func (m *Model) syncRadar() {
	if m.radar.feed == nil || !m.radar.feed.busy.CompareAndSwap(false, true) {
		return
	}
	m.applyRadar(m.radar.feed.run())
}

func (m *Model) applyRadar(snap insight.SignalSnapshot) {
	m.radar.snap, m.radar.have = snap, true
	m.radar.listed = radarRows(snap.Signals)
	m.scopeRadar()
	m.syncRadarSelection()
}

func (m *Model) scopeRadar() {
	m.radar.scope = m.nsFilter
	m.radar.rows = inScope(m.radar.listed, m.nsFilter)
	m.radar.counts = ui.CountSignals(inScope(m.radar.snap.Signals, m.nsFilter))
}

func inScope(signals []insight.Signal, scope nsSet) []insight.Signal {
	if scope == "" {
		return signals
	}
	allowed := scope.matcher()
	out := make([]insight.Signal, 0, len(signals))
	for _, s := range signals {
		if allowed(s.Entity.Namespace) {
			out = append(out, s)
		}
	}
	return out
}

func (m *Model) followRadarScope() {
	if !m.radar.have || m.radar.scope == m.nsFilter {
		return
	}
	m.scopeRadar()
	if !m.nsFilter.matcher()(m.radar.held.Entity.Namespace) {
		m.radar.id, m.radar.cursor, m.radar.scroll = "", 0, 0
	}
	m.syncRadarSelection()
}

func (m Model) yankSignal() (yank, bool) {
	s := m.selectedSignal()
	if s == nil {
		return yank{}, false
	}
	return exact(s.Entity.Namespace + "/" + s.Entity.Name)
}

func radarGroup(s insight.Signal) int {
	switch s.State {
	case insight.SignalActive, insight.SignalRecovering:
		return 0
	case insight.SignalSuspended:
		return 1
	case insight.SignalCandidate:
		return 2
	case insight.SignalResolved:
		return 3
	}
	return -1
}

func radarRows(signals []insight.Signal) []insight.Signal {
	out := make([]insight.Signal, 0, len(signals))
	for _, s := range signals {
		if g := radarGroup(s); g >= 0 && (g != 1 || s.PeakSeverity > insight.SignalInfo) {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b insight.Signal) int {
		if n := cmp.Compare(radarGroup(a), radarGroup(b)); n != 0 {
			return n
		}
		if n := cmp.Compare(b.Severity, a.Severity); n != 0 {
			return n
		}
		if n := a.FirstObservedAt.Compare(b.FirstObservedAt); n != 0 {
			return n
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

func (m *Model) syncRadarSelection() {
	rows := m.radar.rows
	if m.radar.id != "" {
		if i := slices.IndexFunc(rows, func(s insight.Signal) bool { return s.ID == m.radar.id }); i >= 0 {
			m.radar.cursor, m.radar.held, m.radar.gone = i, rows[i], false
			return
		}
		m.radar.gone = true
		return
	}
	m.radar.cursor = max(0, min(m.radar.cursor, len(rows)-1))
	m.adoptRadarRow()
}

func (m *Model) adoptRadarRow() {
	m.radar.id, m.radar.held, m.radar.gone = "", insight.Signal{}, false
	if m.radar.cursor >= 0 && m.radar.cursor < len(m.radar.rows) {
		m.radar.held = m.radar.rows[m.radar.cursor]
		m.radar.id = m.radar.held.ID
	}
}

func (m *Model) moveRadar(delta int) {
	if len(m.radar.rows) == 0 {
		return
	}
	next := max(0, min(len(m.radar.rows)-1, m.radar.cursor+delta))
	if next != m.radar.cursor || m.radar.gone {
		m.radar.cursor, m.radar.scroll = next, 0
		m.adoptRadarRow()
	}
}

func (m Model) selectedSignal() *insight.Signal {
	if m.radar.id == "" {
		return nil
	}
	s := m.radar.held
	return &s
}

func (m Model) signalPod(s *insight.Signal) *data.Pod {
	if s == nil || s.Entity.Kind != "Pod" || m.radar.gone {
		return nil
	}
	key := data.PodKey(s.Entity.Namespace, s.Entity.Name)
	for i := range m.snap.Pods {
		if m.snap.Pods[i].Key() == key {
			return &m.snap.Pods[i]
		}
	}
	return nil
}

func (m *Model) openSignalPod() {
	s := m.selectedSignal()
	p := m.signalPod(s)
	if p == nil {
		return
	}
	ref := ResourceRef{Version: "v1", Resource: "pods", Namespace: s.Entity.Namespace, Name: s.Entity.Name, UID: s.Entity.UID}
	m.navigate(Route{Kind: RoutePod, Screen: ScreenPodDetail, Pod: p.Key(), Ref: ref})
}

func (m Model) radarViewState() ui.RadarState {
	st := ui.RadarState{
		Available: m.radar.feed != nil,
		Error:     m.radar.err,
		Have:      m.radar.have,
		Snapshot:  m.radar.snap,
		Rows:      m.radar.rows,
		Counts:    m.radar.counts,
		Scope:     m.nsFilter.names(),
		Compact:   m.layout.Compact,
		Hidden:    len(m.radar.listed) - len(m.radar.rows),
		Cursor:    m.radar.cursor,
		Scroll:    m.radar.scroll,
		Gone:      m.radar.gone,
		Selected:  m.selectedSignal(),
	}
	st.PodOpen = m.signalPod(st.Selected) != nil
	return st
}

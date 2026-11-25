package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

var radarEpoch = time.Date(2026, 10, 16, 20, 15, 0, 0, time.UTC)

type radarHarness struct {
	t    *testing.T
	now  time.Time
	demo *data.Demo
}

func newRadarHarness(t *testing.T) *radarHarness {
	return &radarHarness{t: t, now: radarEpoch, demo: data.NewDemo()}
}

func (h *radarHarness) model(w, ht int) Model {
	h.t.Helper()
	m := New(Options{Source: h.demo, Palette: theme.DefaultPalette(), InitialPod: 1, NoAnim: true, Now: func() time.Time { return h.now }})
	m.clock = anim.Fixed(goldenT, goldenPhase, 30)
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: ht})
	return next.(Model)
}

func (h *radarHarness) advance(m Model, d time.Duration, restarts int) Model {
	h.now = h.now.Add(d)
	h.demo.AdvanceObservations(restarts * 600)
	m.syncRadar()
	return m
}

func (h *radarHarness) active(w, ht int) Model {
	m := h.model(w, ht)
	m = h.advance(m, 40*time.Second, 1)
	m = h.advance(m, 6*time.Minute, 2)
	return press(h.t, m, "7", "home")
}

func signalState(m Model, rule insight.Rule) (insight.Signal, bool) {
	for _, s := range m.radar.snap.Signals {
		if s.Rule == rule && s.State != insight.SignalNormal {
			return s, true
		}
	}
	return insight.Signal{}, false
}

func TestRadarActivatesFromRegistry(t *testing.T) {
	m := demoModel(t, 160, 48)
	r := press(t, m, "7")
	if !r.onScreen(ScreenRadar) || r.nav.Active().Dest != DestRadar || r.nav.Depth() != 1 {
		t.Fatalf("7 opens Radar through the registry: %+v", r.nav.Active())
	}
	if again := press(t, r, "7"); again.nav != r.nav {
		t.Fatal("activating Radar again does not push")
	}
	if back := press(t, r, "esc"); !back.onScreen(ScreenDashboard) {
		t.Fatal("esc returns to the previous route")
	}
	if menu := press(t, m, "m", "end", "enter"); !menu.onScreen(ScreenRadar) {
		t.Fatal("the menu activates Radar")
	}
	if menu := press(t, m, "m", "7"); !menu.onScreen(ScreenRadar) || menu.menu.open {
		t.Fatal("7 inside the menu activates Radar")
	}
	for _, from := range []Model{press(t, m, "enter"), press(t, m, "4"), press(t, m, "6")} {
		if !press(t, from, "7").onScreen(ScreenRadar) {
			t.Fatal("7 works from every screen")
		}
	}
}

func TestRadarSignalsComeFromTheEngine(t *testing.T) {
	h := newRadarHarness(t)
	m := h.model(160, 48)
	if s, ok := signalState(m, insight.RuleCrashLoop); !ok || s.State != insight.SignalCandidate {
		t.Fatalf("one observation is a candidate, never an active CrashLoop: %+v", s)
	}
	same := m.radar.snap
	m.syncRadar()
	if s, _ := signalState(m, insight.RuleCrashLoop); s.State != insight.SignalCandidate || len(m.radar.snap.Signals) != len(same.Signals) {
		t.Fatal("an unchanged revision does not add an observation")
	}
	m = h.advance(m, 40*time.Second, 1)
	s, ok := signalState(m, insight.RuleCrashLoop)
	if !ok || s.State != insight.SignalActive || s.Severity != insight.SignalHigh || s.Evidence.Observations != 2 {
		t.Fatalf("two distinct versions after 30s activate CrashLoop: %+v", s)
	}
	if s.Entity.UID == "" || s.Entity.Name != "svclb-traefik-8c2f1-6n2vb" {
		t.Fatalf("signal keeps the typed identity: %+v", s.Entity)
	}
	m = press(t, m, "7")
	if len(m.radar.rows) < 2 || radarGroup(m.radar.rows[0]) != 0 || m.radar.rows[0].Severity != insight.SignalHigh {
		t.Fatalf("active High signals come first: %+v", m.radar.rows)
	}
	last := m.radar.rows[len(m.radar.rows)-1]
	if radarGroup(last) < radarGroup(m.radar.rows[0]) {
		t.Fatal("rows are grouped by state")
	}
	for _, r := range m.radar.rows {
		if r.State == insight.SignalNormal {
			t.Fatal("normal baselines are counted, not listed")
		}
	}
}

func TestRadarStatesStayDistinct(t *testing.T) {
	var none Model
	{
		m := demoModel(t, 160, 48)
		m.radar = radarState{}
		none = press(t, m, "7")
	}
	plain := none.Render().Plain()
	if !strings.Contains(plain, "Radar indisponível: esta fonte não publica observações tipadas.") || strings.Contains(plain, "Nenhum sinal ativo") {
		t.Fatal("a source without observations is unavailable, not healthy")
	}
	h := newRadarHarness(t)
	m := h.model(160, 48)
	m.radar.have = false
	if plain := press(t, m, "7").Render().Plain(); !strings.Contains(plain, "Coletando baseline; ainda sem conclusão.") {
		t.Fatal("before the first step the radar is collecting")
	}
	partial := h.model(160, 48)
	partial.applyRadar(insight.SignalSnapshot{Coverage: insight.Coverage{State: insight.CoverageDenied, Reason: "pods: forbidden"}})
	plain = press(t, partial, "7").Render().Plain()
	if !strings.Contains(plain, "Sem conclusão: cobertura negada.") || !strings.Contains(plain, "pods: forbidden") || strings.Contains(plain, "Nenhum sinal ativo") {
		t.Fatal("denied coverage never reads as zero signals")
	}
	empty := h.model(160, 48)
	empty.applyRadar(insight.SignalSnapshot{Coverage: insight.Coverage{State: insight.CoverageComplete, Observed: 3, Total: 3}})
	if plain := press(t, empty, "7").Render().Plain(); !strings.Contains(plain, "Nenhum sinal ativo nos dados observados.") || !strings.Contains(plain, "completa 3/3") {
		t.Fatal("complete coverage without signals says so with its denominator")
	}
	omitted := h.model(160, 48)
	omitted.applyRadar(insight.SignalSnapshot{TotalSignals: 30000, OmittedSignals: 10000, Coverage: insight.Coverage{State: insight.CoveragePartial, Reason: "operational state budget exceeded"}})
	if plain := press(t, omitted, "7").Render().Plain(); !strings.Contains(plain, "omitidos 10000 de 30000") {
		t.Fatal("omitted signals are counted and visible")
	}
}

func TestRadarNeverShowsAHealthNumber(t *testing.T) {
	h := newRadarHarness(t)
	plain := h.active(160, 48).Render().Plain()
	if !strings.Contains(plain, "Saúde: indisponível") || strings.Contains(plain, "/100") {
		t.Fatal("no health number until the engine publishes its formula")
	}
}

func TestRadarSelectionAndPodRoute(t *testing.T) {
	h := newRadarHarness(t)
	m := h.active(160, 48)
	first := m.radar.id
	if first == "" || m.selectedSignal() == nil {
		t.Fatal("the first signal is selected")
	}
	down := press(t, m, "down")
	if down.radar.id == first || down.radar.cursor != 1 {
		t.Fatal("down moves the selection")
	}
	down.syncRadar()
	if down.radar.cursor != 1 {
		t.Fatal("a new snapshot keeps the selected signal by ID")
	}
	open := press(t, m, "enter")
	r := open.nav.Active()
	sig := m.selectedSignal()
	if r.Kind != RoutePod || r.Ref.UID != sig.Entity.UID || r.Pod != sig.Entity.Namespace+"/"+sig.Entity.Name {
		t.Fatalf("enter opens the pod with the signal identity: %+v", r)
	}
	if back := press(t, open, "esc"); !back.onScreen(ScreenRadar) || back.radar.id != first {
		t.Fatal("esc returns to Radar with the same selection")
	}
	gone := m
	gone.radar.gone = true
	if again := press(t, gone, "enter"); again.nav != gone.nav {
		t.Fatal("a vanished target is never opened by name")
	}
	if !strings.Contains(gone.Render().Plain(), "Alvo não está mais no inventário observado") {
		t.Fatal("a vanished target says so")
	}
}

func TestRadarMouseUsesRendererGeometry(t *testing.T) {
	h := newRadarHarness(t)
	m := h.active(160, 48)
	l := m.layout
	next, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: l.Events.X + 3, Y: l.Events.Y + 1 + 1 + 1})
	clicked := next.(Model)
	if clicked.radar.cursor != 1 {
		t.Fatalf("clicking a row selects it, cursor %d", clicked.radar.cursor)
	}
	st := clicked.radarViewState()
	span, line, ok := ui.RadarPodButton(st, l.Message.W-4)
	if !ok {
		t.Fatal("selected signal offers the pod button")
	}
	rows := m.layout.Message.H - 2
	for clicked.radar.scroll+rows <= line {
		clicked.scrollRadar(1)
	}
	y := l.Message.Y + 1 + line - clicked.radar.scroll
	next, _ = clicked.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: l.Message.X + 2 + span.X, Y: y})
	if r := next.(Model).nav.Active(); r.Kind != RoutePod || r.Ref.UID != st.Selected.Entity.UID {
		t.Fatalf("clicking [Abrir pod] opens the same pod as enter: %+v", r)
	}
}

func TestRadarSessionDiscardsStaleResults(t *testing.T) {
	h := newRadarHarness(t)
	m := h.active(160, 48)
	before := m.radar.snap
	next, cmd := m.Update(radarMsg{session: m.session.Generation + 1, snap: insight.SignalSnapshot{}})
	if cmd != nil || len(next.(Model).radar.snap.Signals) != len(before.Signals) {
		t.Fatal("a result from another session is ignored")
	}
	next, _ = m.Update(radarTickMsg{session: m.session.Generation + 1})
	if next.(Model).radar.feed.busy.Load() {
		t.Fatal("a stale tick starts no work")
	}
}

func TestRadarIdlePolicy(t *testing.T) {
	m := demoModel(t, 160, 48)
	if m.Init() != nil {
		t.Fatal("static demo schedules no radar tick")
	}
	if !m.radar.have {
		t.Fatal("a static source is evaluated once on creation")
	}
	m.animated = true
	if cmd := m.armRadarTick(); cmd == nil || !m.radar.ticking {
		t.Fatal("pending timers keep one radar tick while animated")
	}
	if cmd := m.armRadarTick(); cmd != nil {
		t.Fatal("only one radar tick in flight")
	}
	m.radar.ticking = false
	m.radar.snap = insight.SignalSnapshot{}
	if cmd := m.armRadarTick(); cmd != nil {
		t.Fatal("no timers, no tick")
	}
}

func TestRadarGoldens(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"radar_160x48", 160, 48}, {"radar_130x40", 130, 40}} {
		t.Run(size.name, func(t *testing.T) {
			c := newRadarHarness(t).active(size.w, size.h).Render()
			golden(t, size.name+".txt", c.Plain())
			golden(t, size.name+".ansi", c.String())
		})
	}
	h := newRadarHarness(t)
	m := h.active(160, 48)
	golden(t, "radar_help_160x48.txt", press(t, m, "?").Render().Plain())
}

func TestRadarLiveSourceRunsOffTheUpdateLoop(t *testing.T) {
	now := radarEpoch
	demo := data.NewDemo()
	demo.ObserveWith(insight.ClockFunc(func() time.Time { return now }))
	m := New(Options{Source: fakeLive{Demo: demo, changes: make(chan struct{})}, NoAnim: true, InitialPod: 1, Now: func() time.Time { return now }})
	if m.radar.have || !m.radar.live {
		t.Fatal("a live source is not evaluated synchronously on creation")
	}
	cmd := m.radarInit()
	if cmd == nil || m.radarCmd() != nil {
		t.Fatal("init starts exactly one radar step")
	}
	msg, ok := cmd().(radarMsg)
	if !ok || msg.session != m.session.Generation {
		t.Fatal("the step reports to the current session")
	}
	next, tick := m.Update(msg)
	m = next.(Model)
	if !m.radar.have || tick == nil || !m.radar.ticking {
		t.Fatal("pending timers arm the radar tick even without animation")
	}
	next, run := m.Update(radarTickMsg{session: m.session.Generation})
	m = next.(Model)
	if run == nil || m.radar.ticking {
		t.Fatal("a tick starts one step and disarms itself")
	}
	if _, cmd := m.Update(dataMsg{session: m.session.Generation}); cmd == nil {
		t.Fatal("data changes keep their consumer")
	}
	run()
	if m.radarCmd() == nil {
		t.Fatal("a finished step frees the feed")
	}
}

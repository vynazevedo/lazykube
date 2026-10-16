package app

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const (
	livePod  = "bilibili-raffle/bilibili-live-7f6b8d9c5-mn4tz"
	crashPod = 12
	lgoPod   = 2
)

func TestHelpIsHonest(t *testing.T) {
	km := reflect.TypeOf(KeyMap{})
	for _, gone := range []string{"Shell", "Delete", "Context"} {
		if _, ok := km.FieldByName(gone); ok {
			t.Fatalf("KeyMap still declares unimplemented %s", gone)
		}
	}
	all := append(ui.ShortHelp(), ui.FullHelp()...)
	for _, h := range all {
		desc := strings.ToLower(h.Desc)
		for _, word := range []string{"shell", "delete", "context"} {
			if strings.Contains(desc, word) {
				t.Fatalf("help still announces %q: %+v", word, h)
			}
		}
		if h.Key == "s" || strings.Contains(h.Key, "ctrl+d") || strings.Contains(h.Key, "ctrl+k") {
			t.Fatalf("help still lists %q", h.Key)
		}
	}
	keys := map[string]bool{}
	for _, h := range ui.FullHelp() {
		for _, k := range strings.Fields(h.Key) {
			keys[k] = true
		}
	}
	for _, k := range []string{"C", "P", "n", "N", "E", "S", "w", "g", "G", "m", "2", "enter", "esc"} {
		if !keys[k] {
			t.Fatalf("implemented key %q missing from help", k)
		}
	}
	for _, list := range [][]ui.KeyHelp{ui.FullHelp(), ui.ShortHelp(), ui.PodDetailHelp(), ui.MenuHelp()} {
		for _, h := range list {
			for _, k := range strings.Fields(h.Key) {
				if k == "3" || k == "4" || k == "5" || k == "6" {
					t.Fatalf("screen key %q is announced before its screen exists", k)
				}
			}
		}
	}
	for _, h := range ui.PodDetailHelp() {
		if h.Key == "tab" || h.Key == "1" || h.Key == "/" {
			t.Fatalf("detail help must not announce %q, it has no effect there", h.Key)
		}
	}
	m := demoModel(t, 160, 48)
	before := m.tab
	m = press(t, m, "s")
	if m.tab != before {
		t.Fatal("unbound keys must not change state")
	}
	if !press(t, m, "m").menu.open {
		t.Fatal("m opens the screens menu")
	}
	detail := press(t, m, "enter")
	if !detail.inDetail() {
		t.Fatal("enter on a pod opens its detail")
	}
	if back := press(t, detail, "2"); back.inDetail() || back.nav.Active().Screen != ScreenDashboard {
		t.Fatal("2 activates the Dashboard")
	}
	if back := press(t, detail, "esc"); back.inDetail() {
		t.Fatal("esc goes back")
	}
	for _, k := range []string{"3", "4", "5", "6"} {
		if got := press(t, m, k); got.nav != m.nav || got.menu.open {
			t.Fatalf("%s must stay unbound until its screen exists", k)
		}
	}
}

func TestPodListShowsRealCause(t *testing.T) {
	m := demoModel(t, 160, 48)
	plain := m.Render().Plain()
	for _, want := range []string{"Init:0/1", "ready 0/1", "ImagePullB", "CrashLoopB", "Completed", "CPU/STATUS"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("pods list missing %q", want)
		}
	}
	m.selectPod(crashPod)
	if !strings.Contains(m.Render().Plain(), "[CrashLoopBackOff]") {
		t.Fatal("details header must show the kubectl reason")
	}
}

func detailsModel(t *testing.T, pod int) Model {
	t.Helper()
	m := demoModel(t, 160, 48)
	m.selectPod(pod)
	m.focus = FocusDetails
	return m
}

func TestContainerCycleAndPrevious(t *testing.T) {
	m := detailsModel(t, 1)
	m = press(t, m, "l")
	if m.dg.logView.Container != "app" || len(m.dg.containers) != 2 {
		t.Fatalf("default container = %q (%d containers)", m.dg.logView.Container, len(m.dg.containers))
	}
	m = press(t, m, "C")
	if m.dg.logView.Container != "log-shipper" || !strings.Contains(m.logs[len(m.logs)-1].Message, "") || !strings.Contains(joinLogs(m.logs), "sink=loki") {
		t.Fatalf("C must switch the stream to the sidecar: %q", m.dg.logView.Container)
	}
	m = press(t, m, "C")
	if m.dg.logView.Container != "app" {
		t.Fatal("C must cycle back to the first container")
	}
	m = press(t, m, "t", "P")
	if m.tab != ui.TabTop || !m.dg.previous {
		t.Fatal("P keeps Top (mini logs) and enables previous")
	}
	m = press(t, m, "d", "P")
	if m.tab != ui.TabLog || m.dg.previous {
		t.Fatal("P from Describe toggles back and opens Log")
	}
	m = press(t, m, "P")
	if !m.dg.logView.Previous || !strings.Contains(joinLogs(m.logs), "panic: runtime error") {
		t.Fatalf("previous logs not shown: %+v", m.dg.logView)
	}
	if !strings.Contains(m.Render().Plain(), "app · previous") {
		t.Fatal("log source label must flag previous")
	}
	m.selectPod(lgoPod)
	if m.dg.previous || m.dg.logContainer != "" {
		t.Fatal("selecting another pod resets the log target")
	}
	m = press(t, m, "P")
	if m.dg.logView.State != data.FeedUnavailable || !strings.Contains(m.dg.logView.Reason, "sem instância anterior") {
		t.Fatalf("previous without termination must be unavailable: %+v", m.dg.logView)
	}
	if !strings.Contains(m.Render().Plain(), "sem instância anterior do container app") {
		t.Fatal("unavailable previous must be explained on screen")
	}
}

func joinLogs(lines []data.LogLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Message)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestWaitingContainerLogsExplainWhy(t *testing.T) {
	m := detailsModel(t, 11)
	m = press(t, m, "l")
	if m.dg.logView.State != data.FeedUnavailable || !strings.Contains(m.Render().Plain(), "aguardando: ImagePullBackOff") {
		t.Fatalf("waiting container: %+v", m.dg.logView)
	}
}

func TestLogSearch(t *testing.T) {
	m := detailsModel(t, 1)
	m = press(t, m, "l", "/")
	if !m.dg.searching || m.filtering {
		t.Fatal("/ in a focused Log tab searches the log, not the pod list")
	}
	m = press(t, m, "h", "e", "a", "r", "t")
	if len(m.dg.matches) < 2 || m.follow {
		t.Fatalf("matches=%d follow=%v", len(m.dg.matches), m.follow)
	}
	m = press(t, m, "enter")
	if m.dg.searching || m.dg.search.Value() != "heart" {
		t.Fatal("enter keeps the query and leaves edit mode")
	}
	first := m.dg.matchIdx
	m = press(t, m, "n")
	if m.dg.matchIdx != (first+1)%len(m.dg.matches) {
		t.Fatal("n must go to the next match")
	}
	m = press(t, m, "N", "N")
	if m.dg.matchIdx != (first-1+len(m.dg.matches))%len(m.dg.matches) {
		t.Fatal("N must go to the previous match")
	}
	row := m.dg.matches[m.dg.matchIdx]
	if row < m.scroll || row >= m.scroll+ui.ScrollRows(ui.TabLog, m.layout.Details.H) {
		t.Fatal("current match must be visible")
	}
	golden(t, "log_search.txt", m.Render().Plain())
	m = press(t, m, "esc")
	if m.dg.search.Value() != "" || len(m.dg.matches) != 0 {
		t.Fatal("esc clears the search")
	}
	m.focus = FocusPods
	m = press(t, m, "/")
	if !m.filtering || m.dg.searching {
		t.Fatal("/ outside the details keeps filtering pods")
	}
}

func TestSearchKeepsReceivingLines(t *testing.T) {
	m := detailsModel(t, 1)
	m = press(t, m, "l", "/", "w", "s", "enter")
	before := len(m.logs)
	m.clock.T += 8 * 4
	m.refresh()
	if len(m.logs) <= before && len(m.logs) < logBuffer {
		t.Fatal("new lines must keep arriving while a search is active")
	}
	if m.newLines == 0 {
		t.Fatal("new lines must be counted while away from the bottom")
	}
}

func TestEventScopes(t *testing.T) {
	m := detailsModel(t, 1)
	m = press(t, m, "E")
	if m.tab != ui.TabEvents || m.events.Scope != data.ScopePod {
		t.Fatal("E first opens the Events tab")
	}
	if m.events.Items[0].Type != "Warning" {
		t.Fatal("warnings come first")
	}
	m = press(t, m, "E")
	if m.events.Scope != data.ScopeNamespace || m.events.Target != "bilibili-raffle" {
		t.Fatalf("namespace scope = %+v", m.events)
	}
	m = press(t, m, "E")
	if m.events.Scope != data.ScopeCluster {
		t.Fatal("third press shows the cluster")
	}
	seenNode := false
	lastWarning := -1
	for i, e := range m.events.Items {
		if strings.HasPrefix(e.Object, "Node/") {
			seenNode = true
		}
		if e.Type == "Warning" {
			if lastWarning != i-1 {
				t.Fatal("warnings must be grouped first")
			}
			lastWarning = i
		}
	}
	if !seenNode {
		t.Fatal("cluster scope must include non-pod objects")
	}
	golden(t, "events_cluster.txt", m.Render().Plain())
	m = press(t, m, "E")
	if m.events.Scope != data.ScopePod {
		t.Fatal("scope cycles back to pod")
	}
}

func TestNamespaceEventsWithoutPod(t *testing.T) {
	m := demoModel(t, 160, 48)
	m.nsFilter = "default"
	m.syncSelection()
	m.focus = FocusDetails
	m = press(t, m, "E", "E")
	if m.selectedPod() != nil || m.events.Scope != data.ScopeNamespace || m.events.Target != "default" {
		t.Fatalf("namespace events must not require a pod: %+v", m.events)
	}
	if !strings.Contains(m.Render().Plain(), "nenhum evento neste escopo") {
		t.Fatal("empty namespace must show the empty state")
	}
}

func TestCrashDiagnosisGolden(t *testing.T) {
	m := detailsModel(t, crashPod)
	m = press(t, m, "d")
	plain := m.Render().Plain()
	for _, want := range []string{"CrashLoopBackOff", "Error · exit 1", "back-off 5m0s"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("crash describe missing %q", want)
		}
	}
	golden(t, "describe_crash.txt", plain)
	m.selectPod(4)
	if !strings.Contains(m.Render().Plain(), "OOMKilled · exit 137") {
		t.Fatal("OOM evidence must survive with metrics present")
	}
}

type countingSource struct {
	*data.Demo
	events int
}

func (c *countingSource) ScopedEvents(q data.EventQuery) data.EventFeed {
	c.events++
	return c.Demo.ScopedEvents(q)
}

func TestEventsFetchedOnlyForEventsTab(t *testing.T) {
	src := &countingSource{Demo: data.NewDemo()}
	m := New(Options{Source: src, NoAnim: true, InitialPod: 1})
	for i := 0; i < 5; i++ {
		m.refresh()
	}
	if src.events != 0 {
		t.Fatalf("events queried %d times outside the Events tab", src.events)
	}
	m = press(t, m, "e")
	if src.events == 0 || len(m.events.Items) == 0 {
		t.Fatal("events must load when the tab opens")
	}
}

func TestBasicSourceFallsBack(t *testing.T) {
	src := &keyedSource{pods: []data.Pod{{Name: "web-0", Namespace: "apps"}}}
	m := New(Options{Source: src, NoAnim: true})
	m = press(t, m, "P", "E", "E")
	if m.dg.logView.State != data.FeedUnavailable || m.events.State != data.FeedUnavailable {
		t.Fatalf("sources without diagnostics degrade explicitly: %+v %+v", m.dg.logView, m.events)
	}
}

type subscriptionSource struct {
	*data.Demo
	cancel  context.CancelFunc
	current context.Context
	calls   []string
	empty   bool
}

func (s *subscriptionSource) Snapshot() data.Snapshot {
	snap := s.Demo.Snapshot()
	if s.empty {
		snap.Pods = nil
	}
	return snap
}

func (s *subscriptionSource) PodLogs(req data.LogRequest, tick, n int) data.LogView {
	s.calls = append(s.calls, req.Pod)
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if req.Pod == "" {
		return data.LogView{}
	}
	s.current, s.cancel = context.WithCancel(context.Background())
	return s.Demo.PodLogs(req, tick, n)
}

func TestEmptySelectionCancelsLogSubscription(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*testing.T, Model) Model
	}{
		{"filter matches nothing", func(t *testing.T, m Model) Model {
			m = press(t, m, "/", "z", "z", "z", "q")
			return m
		}},
		{"empty namespace", func(t *testing.T, m Model) Model {
			m.nsFilter = "default"
			m.syncSelection()
			return m
		}},
		{"last pod removed", func(t *testing.T, m Model) Model {
			m.src.(*subscriptionSource).empty = true
			next, _ := m.Update(dataMsg{})
			return next.(Model)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := &subscriptionSource{Demo: data.NewDemo()}
			m := New(Options{Source: src, NoAnim: true, InitialPod: 1})
			next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
			m = next.(Model)
			if src.current == nil || src.current.Err() != nil {
				t.Fatal("selected pod must hold a subscription")
			}
			m = c.clear(t, m)
			if m.selectedPod() != nil {
				t.Fatal("selection should be empty")
			}
			if src.current.Err() == nil {
				t.Fatalf("previous subscription still active after empty selection; calls=%v", src.calls)
			}
			if len(m.logs) != 0 || m.dg.logView.Container != "" {
				t.Fatal("empty selection clears the log view")
			}
		})
	}
}

func helpTokens(k string) []string {
	return strings.FieldsFunc(k, func(r rune) bool { return r == ' ' || r == '/' && len(k) > 1 })
}

func TestHelpOverlayMatchesActiveRoute(t *testing.T) {
	dash := demoModel(t, 160, 48)
	scope := press(t, dash, "tab", "tab", "j", "enter")
	detail := press(t, dash, "enter")
	for _, c := range []struct {
		name   string
		m      Model
		absent []string
	}{
		{"dashboard", dash, nil},
		{"namespace", scope, nil},
		{"detail", detail, []string{"next panel", "previous panel", "toggle left column", "filter pods", "open pod"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			bound := map[string]bool{}
			for _, b := range c.m.screen().Keys(c.m) {
				for _, k := range append(b.Keys(), helpTokens(b.Help().Key)...) {
					bound[k] = true
				}
			}
			bound["/"] = bound["/"] || bound["slash"]
			overlay := press(t, c.m, "?").Render().Plain()
			for _, h := range c.m.screen().FullHelp(c.m) {
				for _, tok := range helpTokens(h.Key) {
					if !bound[tok] {
						t.Fatalf("overlay announces %q (%s) but the route does not bind %q", h.Key, h.Desc, tok)
					}
				}
				if !strings.Contains(overlay, h.Desc[:min(len(h.Desc), 12)]) {
					t.Fatalf("overlay must show %q", h.Desc)
				}
			}
			for _, gone := range c.absent {
				if strings.Contains(overlay, gone) {
					t.Fatalf("%s overlay announces %q without effect", c.name, gone)
				}
			}
		})
	}
}

package app

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type eventsSource struct {
	*data.Demo
	connected string
	feed      func(data.EventQuery) data.EventFeed
	queries   *[]data.EventQuery
}

func newEventsSource(connected string, feed func(data.EventQuery) data.EventFeed) eventsSource {
	return eventsSource{Demo: data.NewDemo(), connected: connected, feed: feed, queries: &[]data.EventQuery{}}
}

func (s eventsSource) Snapshot() data.Snapshot {
	snap := s.Demo.Snapshot()
	snap.Cluster.Namespace = s.connected
	return snap
}

func (s eventsSource) ScopedEvents(q data.EventQuery) data.EventFeed {
	*s.queries = append(*s.queries, q)
	if s.feed != nil {
		return s.feed(q)
	}
	return s.Demo.ScopedEvents(q)
}

func (s eventsSource) clusterQueries() int {
	n := 0
	for _, q := range *s.queries {
		if q.Scope == data.ScopeCluster {
			n++
		}
	}
	return n
}

func (s *consumerSource) quietLogs(t *testing.T, step string, m Model, f func(Model) Model) Model {
	t.Helper()
	logs, describe := len(s.logs), s.describe
	m = f(m)
	if s.streaming() {
		t.Fatalf("%s: Events must not keep a log stream open", step)
	}
	for _, p := range s.logs[logs:] {
		if p != "" {
			t.Fatalf("%s: Events opened logs for %q", step, p)
		}
	}
	if s.describe != describe {
		t.Fatalf("%s: Events must not describe pods", step)
	}
	return m
}

type plainSource struct {
	data.Source
}

func events(t *testing.T, m Model) Model {
	t.Helper()
	m = press(t, m, "6")
	if !m.onScreen(ScreenEvents) {
		t.Fatal("6 must activate Events")
	}
	return m
}

func TestEventsActivation(t *testing.T) {
	for _, size := range []struct{ w, h, scope, table, msg int }{{160, 48, 8, 22, 13}, {130, 40, 6, 18, 11}} {
		m := events(t, demoModel(t, size.w, size.h))
		l := m.layout
		if l.Scope.H != size.scope || l.Events.H != size.table || l.Message.H != size.msg || l.Scope.Y != 3 || l.Events.Y != 3+size.scope || l.Message.Y+l.Message.H != size.h-2 {
			t.Fatalf("%dx%d events layout %+v %+v %+v", size.w, size.h, l.Scope, l.Events, l.Message)
		}
		for _, r := range []Rect{l.Scope, l.Events, l.Message} {
			if r.X != 1 || r.W != size.w-2 {
				t.Fatalf("%dx%d events panels span the body: %+v", size.w, size.h, r)
			}
		}
		if !l.Pods.Empty() || !l.Details.Empty() || !l.Node.Empty() {
			t.Fatal("events shows only scope, table and message")
		}
		if navCrumbs(m) != "Events" || m.nav.Depth() != 1 || m.menuActive() != 3 {
			t.Fatalf("events route, crumbs %q", navCrumbs(m))
		}
		plain := m.Render().Plain()
		for _, want := range []string{"[6 Events]", "Escopo e cobertura", "MENSAGEM", "NAMESPACE", "TIPO", "OBJETO", "COUNT", "Mensagem completa", "Feed limitado: até 200"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("%dx%d events screen missing %q", size.w, size.h, want)
			}
		}
		if wide := strings.Contains(plain, "IDADE"); wide != (size.w == 160) {
			t.Fatalf("%dx%d: IDADE column only where it fits, age stays in the message panel", size.w, size.h)
		}
	}
	m := events(t, demoModel(t, 160, 48))
	if again := press(t, m, "6"); again.nav != m.nav {
		t.Fatal("activating the same screen does not push")
	}
	if back := press(t, m, "esc"); !back.onScreen(ScreenDashboard) || back.nav.Depth() != 0 {
		t.Fatal("esc returns to the dashboard")
	}
	if menu := press(t, demoModel(t, 160, 48), "m", "end", "enter"); !menu.onScreen(ScreenEvents) {
		t.Fatal("the menu activates Events")
	}
	if menu := press(t, demoModel(t, 160, 48), "m", "6"); !menu.onScreen(ScreenEvents) || menu.menu.open {
		t.Fatal("6 inside the menu activates Events")
	}
	x := ui.MenuSpans(m.menuItems())[3].X + m.layout.Menu.X + 1
	if click, _ := demoModel(t, 160, 48).Update(tea.MouseClickMsg{X: x, Y: 1, Button: tea.MouseLeft}); !click.(Model).onScreen(ScreenEvents) {
		t.Fatal("clicking the menu item activates Events")
	}
}

func TestEventsOpenInScopedNamespace(t *testing.T) {
	m := events(t, demoModel(t, 160, 48))
	if m.ev.Scope != data.ScopeNamespace || m.ev.Namespace != "bilibili-raffle" || m.ev.Pod != "bilibili-raffle/bilibili-live-7f6b8d9c5-mn4tz" {
		t.Fatalf("events opens in the namespace of the selected pod: %+v", m.ev.eventsFrame)
	}
	for _, e := range m.ev.feed.Items {
		if e.Namespace != "bilibili-raffle" {
			t.Fatalf("namespace feed lists %q", e.Namespace)
		}
	}
	scoped := events(t, navNamespace(t, demoModel(t, 160, 48)))
	if scoped.ev.Scope != data.ScopeNamespace || scoped.ev.Namespace != scoped.nsFilter || navCrumbs(scoped) != "Events > "+scoped.nsFilter {
		t.Fatalf("events keeps the filtered namespace: %+v crumbs %q", scoped.ev.eventsFrame, navCrumbs(scoped))
	}
	none := press(t, demoModel(t, 160, 48), "/", "z", "z", "z", "q", "enter")
	if none.selectedPod() != nil {
		t.Fatal("setup must leave no selected pod")
	}
	if all := events(t, none); all.ev.Scope != data.ScopeCluster || !strings.Contains(all.Render().Plain(), "Escopo: cluster") {
		t.Fatalf("without namespace on an all namespaces connection events uses the cluster feed: %+v", all.ev.eventsFrame)
	}
	src := newEventsSource("apps", nil)
	r := press(t, nodeModel(t, src, 160, 48), "/", "z", "z", "z", "q", "enter")
	restricted := events(t, r)
	if restricted.ev.Scope != data.ScopeNamespace || restricted.ev.Namespace != "apps" || src.clusterQueries() != 0 {
		t.Fatalf("a restricted connection opens in its namespace: %+v", restricted.ev.eventsFrame)
	}
	if plain := restricted.Render().Plain(); !strings.Contains(plain, "Conexão: restrita a apps; cluster só com E, amplia a leitura") {
		t.Fatal("the scope limit is visible")
	}
}

func TestEventsClusterNeedsExplicitIntent(t *testing.T) {
	src := newEventsSource("bilibili-raffle", nil)
	m := nodeModel(t, src, 160, 48)
	m = press(t, m, "m", "right", "right", "right", "left", "esc")
	m = events(t, m)
	m = press(t, m, "down", "up", "enter", "esc", "pgdn", "home")
	next, _ := m.Update(dataMsg{})
	m = next.(Model)
	m = press(t, m, "m", "esc")
	if src.clusterQueries() != 0 {
		t.Fatalf("mounting, the menu, data and movement never query the cluster: %+v", *src.queries)
	}
	m = press(t, m, "E")
	if m.ev.Scope != data.ScopeCluster {
		m = press(t, m, "E")
	}
	if m.ev.Scope != data.ScopeCluster || src.clusterQueries() == 0 {
		t.Fatalf("E chooses the cluster scope explicitly: %+v", m.ev.eventsFrame)
	}
	before := src.clusterQueries()
	dash := press(t, m, "2")
	back := press(t, dash, "esc")
	if !back.onScreen(ScreenEvents) || back.ev.Scope != data.ScopeNamespace || back.ev.Namespace != "bilibili-raffle" || src.clusterQueries() != before {
		t.Fatalf("returning to Events on a restricted connection does not query the cluster again: %+v", back.ev.eventsFrame)
	}
	all := events(t, demoModel(t, 160, 48))
	all = press(t, all, "E", "E", "E")
	if all.ev.Scope != data.ScopeNamespace {
		t.Fatalf("E cycles pod, namespace and cluster: %+v", all.ev.eventsFrame)
	}
}

func TestEventsFeedStates(t *testing.T) {
	for _, c := range []struct {
		feed  data.EventFeed
		state string
		want  string
	}{
		{data.EventFeed{State: data.FeedLoading, Reason: "sincronizando eventos do cluster"}, "carregando", "sincronizando eventos do cluster"},
		{data.EventFeed{State: data.FeedForbidden, Reason: "sem permissão: events"}, "sem permissão", "sem permissão: events"},
		{data.EventFeed{State: data.FeedUnavailable, Reason: "fora do escopo --namespace apps\x1b[31m"}, "indisponível", "fora do escopo --namespace apps"},
		{data.EventFeed{}, "disponível", "nenhum evento neste escopo"},
	} {
		src := newEventsSource("", func(q data.EventQuery) data.EventFeed { f := c.feed; f.Scope = q.Scope; return f })
		m := events(t, nodeModel(t, src, 160, 48))
		out := m.Render()
		plain := out.Plain()
		if !strings.Contains(plain, "estado: "+c.state) || !strings.Contains(plain, c.want) {
			t.Fatalf("%q: missing state %q or %q", c.state, c.state, c.want)
		}
		if strings.Contains(out.String(), "\x1b[31m") {
			t.Fatal("feed reasons are sanitized")
		}
		if m.eventsState().Selected() != nil {
			t.Fatal("no event is selected without items")
		}
		if got := press(t, m, "enter"); got.ev.reading {
			t.Fatal("enter has nothing to open without an event")
		}
	}
}

func TestEventsWithoutDiagnostics(t *testing.T) {
	m := events(t, nodeModel(t, plainSource{Source: data.NewDemo()}, 160, 48))
	if m.dg.full || m.ev.Scope != data.ScopePod || m.ev.Pod == "" {
		t.Fatalf("a source without Diagnostics offers only the pod events: %+v", m.ev.eventsFrame)
	}
	if len(m.ev.feed.Items) == 0 || m.ev.feed.State != data.FeedReady {
		t.Fatal("pod events come from Source.Events")
	}
	plain := m.Render().Plain()
	if !strings.Contains(plain, "Fonte sem Diagnostics: apenas eventos do pod") || strings.Contains(plain, "Escolha explícita") {
		t.Fatal("the scope panel tells the source limit and does not offer E")
	}
	if got := press(t, m, "E"); got.ev.Scope != data.ScopePod {
		t.Fatal("E does not widen a source without Diagnostics")
	}
	for _, h := range append(m.screen().Help(m), m.screen().FullHelp(m)...) {
		if h.Key == "E" {
			t.Fatal("help does not announce E without Diagnostics")
		}
	}
	none := press(t, nodeModel(t, plainSource{Source: data.NewDemo()}, 160, 48), "/", "z", "z", "z", "q", "enter")
	empty := events(t, none)
	if empty.ev.Pod != "" || !strings.Contains(empty.Render().Plain(), "nenhum pod selecionado: esta fonte oferece apenas eventos do pod") {
		t.Fatal("without a pod the screen says so instead of an empty feed")
	}
}

func TestEventsFullMessage(t *testing.T) {
	long := strings.Repeat("container restart loop detected ", 12) + "\x1b[2Jfim"
	src := newEventsSource("", func(q data.EventQuery) data.EventFeed {
		return data.EventFeed{Scope: q.Scope, Items: []data.Event{
			{Age: "8s", Type: "Warning", Reason: "BackOff", Object: "Pod/coredns-abc", Namespace: "kube-system", Count: 3, Message: long},
			{Age: "15s", Type: "Normal", Reason: "Started", Object: "Pod/coredns-def", Namespace: "kube-system", Count: 1, Message: "container iniciado"},
		}}
	})
	m := events(t, nodeModel(t, src, 130, 40))
	nav := m.nav
	m = press(t, m, "enter")
	if !m.ev.reading || m.nav != nav || m.inDetail() {
		t.Fatal("enter reads the full message without resolving the object")
	}
	out := m.Render()
	plain := out.Plain()
	if !strings.Contains(plain, "Mensagem completa · focus") || !strings.Contains(plain, "Evento: Warning BackOff · Pod/coredns-abc") || !strings.Contains(plain, "count 3 · idade 8s") || !strings.Contains(plain, "fim") {
		t.Fatal("the message panel shows the whole event, wrapped")
	}
	if strings.Contains(out.String(), "\x1b[2J") {
		t.Fatal("the message is sanitized")
	}
	if scrolled := press(t, m, "down"); scrolled.ev.Cursor != 0 {
		t.Fatal("arrows scroll the message while reading, not the table")
	}
	table := press(t, m, "esc")
	if table.ev.reading || !table.onScreen(ScreenEvents) {
		t.Fatal("esc returns to the table first")
	}
	if moved := press(t, table, "down"); moved.ev.Cursor != 1 || !strings.Contains(moved.Render().Plain(), "container iniciado") {
		t.Fatal("arrows move the table cursor and the message follows it")
	}
	if back := press(t, table, "esc"); !back.onScreen(ScreenDashboard) {
		t.Fatal("second esc leaves Events")
	}
	row := m.layout.Events.Y + 1 + ui.EventsTableTop + 1
	click, _ := table.Update(tea.MouseClickMsg{X: 10, Y: row, Button: tea.MouseLeft})
	if c := click.(Model); c.ev.Cursor != 1 || c.ev.reading {
		t.Fatal("clicking a row selects it")
	}
	read, _ := table.Update(tea.MouseClickMsg{X: 10, Y: m.layout.Message.Y + 2, Button: tea.MouseLeft})
	if !read.(Model).ev.reading {
		t.Fatal("clicking the message panel reads it")
	}
	wheel, _ := table.Update(tea.MouseWheelMsg{X: 10, Y: row, Button: tea.MouseWheelDown})
	if wheel.(Model).ev.Cursor != 1 {
		t.Fatal("the wheel moves the table")
	}
}

func TestEventsReleasesDetailConsumers(t *testing.T) {
	src := &consumerSource{Demo: data.NewDemo()}
	m := New(Options{Source: src, NoAnim: true, InitialPod: 1})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	m = press(t, next.(Model), "e")
	if !src.streaming() {
		t.Fatal("the dashboard preview consumes logs")
	}
	events := src.events
	m = src.quietLogs(t, "enter Events", m, func(m Model) Model { return press(t, m, "6") })
	if src.events == events {
		t.Fatal("events queries its own feed")
	}
	m = src.quietLogs(t, "move in Events", m, func(m Model) Model { return press(t, m, "down", "enter", "esc") })
	m = src.quietLogs(t, "data in Events", m, func(m Model) Model { next, _ := m.Update(dataMsg{}); return next.(Model) })
	for range 3 {
		m = press(t, m, "2")
		if !src.streaming() {
			t.Fatal("the dashboard reopens its preview")
		}
		m = src.quietLogs(t, "back to Events", m, func(m Model) Model { return press(t, m, "esc") })
	}
}

func TestEventsKeysAreHonest(t *testing.T) {
	m := events(t, demoModel(t, 160, 48))
	bound := map[string]bool{}
	for _, b := range m.screen().Keys(m) {
		for _, k := range append(b.Keys(), helpTokens(b.Help().Key)...) {
			bound[k] = true
		}
	}
	for _, h := range append(m.screen().Help(m), m.screen().FullHelp(m)...) {
		for _, k := range helpTokens(h.Key) {
			if alias := map[string]string{"↑↓": "up", "↑": "up", "↓": "down"}[k]; alias != "" {
				k = alias
			}
			if !bound[k] {
				t.Fatalf("events help announces %q, not bound on Events", k)
			}
		}
	}
	for _, k := range []string{"tab", "shift+tab", "l", "c", "t", "d", "e", "S", "C", "P", "f", "w", "g", "G", "1", "[", "]", "/", "n"} {
		got := press(t, m, k)
		if got.tab != m.tab || got.focus != m.focus || got.ev.eventsFrame != m.ev.eventsFrame || got.ev.reading != m.ev.reading || got.filtering || got.leftMode != m.leftMode || got.nav != m.nav || got.securityNamespace != m.securityNamespace || got.wrap != m.wrap || got.follow != m.follow {
			t.Fatalf("%s has no effect on Events and must not change hidden state", k)
		}
	}
	if help := press(t, m, "?"); !help.helpOpen || !strings.Contains(help.Render().Plain(), "scope pod / ns / cluster") {
		t.Fatal("? opens the Events help")
	}
}

func TestEventsGoldens(t *testing.T) {
	denied := func(q data.EventQuery) data.EventFeed {
		return data.EventFeed{Scope: q.Scope, Target: q.Namespace, State: data.FeedForbidden, Reason: "sem permissão: events"}
	}
	for _, size := range [][2]int{{160, 48}, {130, 40}} {
		name := fmt.Sprintf("events_%dx%d", size[0], size[1])
		c := press(t, demoModel(t, size[0], size[1]), "6", "down").Render()
		golden(t, name+".txt", c.Plain())
		golden(t, name+".ansi", c.String())
		d := events(t, nodeModel(t, newEventsSource("bilibili-raffle", denied), size[0], size[1])).Render()
		golden(t, fmt.Sprintf("events_denied_%dx%d.txt", size[0], size[1]), d.Plain())
	}
}

func TestEventsSelectionFollowsIdentity(t *testing.T) {
	ev := func(uid string) data.Event {
		return data.Event{UID: uid, Age: "1m", Type: "Normal", Reason: "Reason" + uid, Object: "Pod/obj-" + uid, Namespace: "kube-system", Count: 1, Message: "mensagem do evento " + uid}
	}
	items := []data.Event{ev("a"), ev("b"), ev("c")}
	src := newEventsSource("", func(q data.EventQuery) data.EventFeed {
		return data.EventFeed{Scope: q.Scope, Items: append([]data.Event(nil), items...)}
	})
	reload := func(m Model, next ...data.Event) Model {
		items = next
		out, _ := m.Update(dataMsg{})
		return out.(Model)
	}
	reading := func(t *testing.T, m Model, uid string) {
		t.Helper()
		sel := m.eventsState().Selected()
		if !m.ev.reading || sel == nil || sel.UID != uid || !strings.Contains(m.Render().Plain(), "mensagem do evento "+uid) {
			t.Fatalf("reading must stay on event %s, got %+v", uid, sel)
		}
	}
	m := press(t, events(t, nodeModel(t, src, 160, 48)), "down", "enter")
	reading(t, m, "b")

	m = reload(m, ev("x"), ev("a"), ev("b"), ev("c"))
	reading(t, m, "b")
	if m.ev.Cursor != 2 || m.eventsState().Gone {
		t.Fatalf("insertion above keeps the row of the event in reading: cursor %d", m.ev.Cursor)
	}

	m = reload(m, ev("c"), ev("b"), ev("a"), ev("x"))
	reading(t, m, "b")
	if m.ev.Cursor != 1 {
		t.Fatalf("reordering follows the event in reading: cursor %d", m.ev.Cursor)
	}

	m = reload(m, ev("c"), ev("a"), ev("x"))
	reading(t, m, "b")
	plain := m.Render().Plain()
	if !m.eventsState().Gone || !strings.Contains(plain, "evento não está mais no feed") {
		t.Fatal("a removed event stays readable and its absence is signaled")
	}

	back := reload(m, ev("c"), ev("b"), ev("a"))
	reading(t, back, "b")
	if back.eventsState().Gone || back.ev.Cursor != 1 {
		t.Fatal("the event coming back clears the absence")
	}

	table := press(t, m, "esc")
	if table.ev.reading || table.eventsState().Gone {
		t.Fatal("esc leaves the absent event and returns to the table")
	}
	if sel := table.eventsState().Selected(); sel == nil || sel.UID == "b" {
		t.Fatalf("the table selects a present event after esc, got %+v", sel)
	}

	browse := reload(press(t, table, "home", "down"), ev("y"), ev("c"), ev("a"), ev("x"))
	if sel := browse.eventsState().Selected(); sel == nil || sel.UID != "a" || browse.ev.Cursor != 2 {
		t.Fatalf("the table cursor follows identity outside reading too, got %+v at %d", sel, browse.ev.Cursor)
	}
}

type messageSource struct {
	eventsSource
	full  map[string]data.EventMessage
	asked *[]string
}

func (s messageSource) EventMessage(uid string) (data.EventMessage, bool) {
	*s.asked = append(*s.asked, uid)
	msg, ok := s.full[uid]
	return msg, ok
}

type previewSource struct {
	data.Source
	feed eventsSource
}

func (s previewSource) Snapshot() data.Snapshot { return s.feed.Snapshot() }

func (s previewSource) ScopedEvents(q data.EventQuery) data.EventFeed { return s.feed.ScopedEvents(q) }

func (s previewSource) Containers(pod string) []data.Container { return s.feed.Containers(pod) }

func (s previewSource) PodLogs(req data.LogRequest, tick, n int) data.LogView {
	return s.feed.PodLogs(req, tick, n)
}

func TestEventsMessageIsHonestAboutTruncation(t *testing.T) {
	preview := strings.Repeat("prévia ", 40) + "…"
	feed := func(q data.EventQuery) data.EventFeed {
		return data.EventFeed{Scope: q.Scope, Items: []data.Event{
			{UID: "u1", Age: "1m", Type: "Warning", Reason: "Failed", Object: "Pod/a", Namespace: "kube-system", Count: 1, Message: preview, Truncated: true},
			{UID: "u2", Age: "2m", Type: "Warning", Reason: "BackOff", Object: "Pod/b", Namespace: "kube-system", Count: 1, Message: preview, Truncated: true},
		}}
	}
	src := messageSource{eventsSource: newEventsSource("", feed), asked: &[]string{}, full: map[string]data.EventMessage{
		"u1": {Text: "texto integral do evento fim-real"},
		"u2": {Text: "inicio do texto enorme", Truncated: true},
	}}
	m := events(t, nodeModel(t, src, 160, 48))
	plain := m.Render().Plain()
	if len(*src.asked) != 0 || strings.Contains(plain, "Mensagem completa") || !strings.Contains(plain, "prévia truncada; enter carrega o texto completo") {
		t.Fatalf("the preview does not claim to be complete and loads nothing before enter: asked %v", *src.asked)
	}
	m = press(t, m, "enter")
	plain = m.Render().Plain()
	if !slices.Equal(*src.asked, []string{"u1"}) || !strings.Contains(plain, "fim-real") || !strings.Contains(plain, "Mensagem completa · focus") || strings.Contains(plain, "prévia truncada") {
		t.Fatalf("enter loads the full message of the opened event only: asked %v", *src.asked)
	}
	m = press(t, press(t, m, "esc", "down", "enter"))
	plain = m.Render().Plain()
	if !slices.Contains(*src.asked, "u2") || strings.Contains(plain, "Mensagem completa") || !strings.Contains(plain, "truncada no teto de 8 KiB") {
		t.Fatal("a message above the explicit cap says so")
	}

	bare := newEventsSource("", feed)
	plainFeed := events(t, nodeModel(t, previewSource{Source: bare.Demo, feed: bare}, 160, 48))
	if p := plainFeed.Render().Plain(); !strings.Contains(p, "truncada em 300 bytes pela fonte") || strings.Contains(p, "enter carrega") || strings.Contains(p, "mensagem completa") || !strings.Contains(p, "enter abre a mensagem recebida") {
		t.Fatal("without full messages the screen does not promise them")
	}
	if p := press(t, plainFeed, "enter").Render().Plain(); strings.Contains(p, "Mensagem completa") || !strings.Contains(p, "Mensagem truncada · focus") {
		t.Fatal("reading a truncated message without the full text is labeled truncated")
	}
}

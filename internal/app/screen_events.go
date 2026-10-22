package app

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type eventsScreen struct{}

type eventsFrame struct {
	Scope     data.EventScope
	Pod       string
	Namespace string
	Cursor    int
	UID       string
}

type eventsView struct {
	eventsFrame
	feed      data.EventFeed
	reading   bool
	msgScroll int
	held      data.Event
	gone      bool
	detail    data.EventMessage
	detailUID string
}

func (eventsScreen) ID() ScreenID {
	return ScreenEvents
}

func (eventsScreen) Title() string {
	return "Events"
}

func (eventsScreen) ShowsDetails() bool {
	return false
}

func (eventsScreen) Init(Model) tea.Cmd {
	return nil
}

func (s eventsScreen) Update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.helpOpen {
			return updateBody(m, msg)
		}
		if !slices.ContainsFunc(s.Keys(m), func(b key.Binding) bool { return key.Matches(msg, b) }) {
			return m, nil
		}
		return m.handleEventsKey(msg)
	case tea.MouseClickMsg:
		if !m.helpOpen && msg.Button == tea.MouseLeft {
			m.handleEventsClick(tea.Mouse(msg))
			return m, nil
		}
	case tea.MouseWheelMsg:
		m.handleEventsWheel(tea.Mouse(msg))
		return m, nil
	case tea.MouseMotionMsg:
		return m, nil
	}
	return updateBody(m, msg)
}

func (eventsScreen) Keys(m Model) []key.Binding {
	k := m.keys
	out := []key.Binding{k.Up, k.Down, k.PageUp, k.PageDown, k.Home, k.End, k.Enter, k.Esc}
	if m.dg.full {
		out = append(out, k.EventScope)
	}
	return append(out,
		k.SecurityAck, k.Palette, k.Pause, k.Help, k.Quit,
		k.Menu, k.MenuPrev, k.MenuNext, k.Dashboard, k.Workloads, k.Nodes, k.Events,
		k.Context, k.Namespace, k.Tree,
	)
}

func (eventsScreen) Help(m Model) []ui.KeyHelp {
	return ui.EventsHelp(m.dg.full)
}

func (eventsScreen) FullHelp(m Model) []ui.KeyHelp {
	return ui.FullHelpEvents(m.dg.full)
}

func (eventsScreen) View(m Model, body draw.Region, ctx ui.Ctx) {
	st := m.eventsState()
	ui.EventsScopePanel(region(body, m.layout.Scope), st, ctx)
	ui.EventsTable(region(body, m.layout.Events), st, ctx)
	ui.EventMessagePanel(region(body, m.layout.Message), st, ctx)
}

func (m Model) eventsState() ui.EventsScreenState {
	return ui.EventsScreenState{
		Feed:       m.ev.feed,
		Scope:      m.ev.Scope,
		Pod:        m.ev.Pod,
		Namespace:  m.ev.Namespace,
		Connected:  m.snap.Cluster.Namespace,
		Full:       m.dg.full,
		Cursor:     m.ev.Cursor,
		Reading:    m.ev.reading,
		MsgScroll:  m.ev.msgScroll,
		Selectable: m.eventsSelectable(),
		Held:       m.heldEvent(),
		Gone:       m.ev.gone,
		Detail:     m.eventDetail(),
		Expandable: m.eventMessages() != nil,
	}
}

func (m Model) eventMessages() data.EventMessages {
	src, _ := m.dg.diag.(data.EventMessages)
	return src
}

func (m Model) selectedEventUID() string {
	if m.ev.gone {
		return m.ev.held.UID
	}
	if m.ev.Cursor >= 0 && m.ev.Cursor < len(m.ev.feed.Items) && m.eventsSelectable() {
		return m.ev.feed.Items[m.ev.Cursor].UID
	}
	return ""
}

func (m Model) eventDetail() *data.EventMessage {
	if uid := m.selectedEventUID(); !m.ev.reading || uid == "" || uid != m.ev.detailUID {
		return nil
	}
	d := m.ev.detail
	return &d
}

func (m *Model) loadEventDetail() {
	src := m.eventMessages()
	e := m.eventsState().Selected()
	if !m.ev.reading || src == nil || e == nil || !e.Truncated || e.UID == "" {
		m.ev.detail, m.ev.detailUID = data.EventMessage{}, ""
		return
	}
	if msg, ok := src.EventMessage(e.UID); ok {
		m.ev.detail, m.ev.detailUID = msg, e.UID
	} else if m.ev.detailUID != e.UID {
		m.ev.detail, m.ev.detailUID = data.EventMessage{}, ""
	}
}

func (m Model) heldEvent() *data.Event {
	if !m.ev.gone {
		return nil
	}
	e := m.ev.held
	return &e
}

func (m Model) eventsSelectable() bool {
	return m.ev.Scope != data.ScopePod || m.ev.Pod != ""
}

func (m Model) eventsAnchor() eventsFrame {
	f := eventsFrame{Namespace: m.scopeNamespace()}
	if p := m.selectedPod(); p != nil {
		f.Pod = p.Key()
		if f.Namespace == "" {
			f.Namespace = p.Namespace
		}
	}
	if f.Namespace == "" {
		f.Namespace = m.snap.Cluster.Namespace
	}
	switch {
	case !m.dg.full:
		f.Scope = data.ScopePod
	case f.Namespace != "":
		f.Scope = data.ScopeNamespace
	default:
		f.Scope = data.ScopeCluster
	}
	return f
}

func (m Model) eventScopes() []data.EventScope {
	var out []data.EventScope
	if m.ev.Pod != "" || !m.dg.full {
		out = append(out, data.ScopePod)
	}
	if m.dg.full {
		if m.ev.Namespace != "" {
			out = append(out, data.ScopeNamespace)
		}
		out = append(out, data.ScopeCluster)
	}
	return out
}

func (m *Model) cycleEventsScope() {
	scopes := m.eventScopes()
	if len(scopes) < 2 {
		return
	}
	i := slices.Index(scopes, m.ev.Scope)
	m.ev.Scope = scopes[(i+1)%len(scopes)]
	m.ev.Cursor, m.ev.UID, m.ev.msgScroll, m.ev.reading, m.ev.gone = 0, "", 0, false, false
	m.refreshEventsScreen()
	m.trackClusterFeed()
}

func (m *Model) restoreEvents(f eventsFrame) {
	m.ev = eventsView{eventsFrame: f}
}

func (m Model) clusterFeedNeeded() bool {
	onEvents := m.onScreen(ScreenEvents) && m.ev.Scope == data.ScopeCluster && m.eventsSelectable()
	onTab := m.screen().ShowsDetails() && m.tab == ui.TabEvents && m.dg.scope == data.ScopeCluster
	return onEvents || onTab
}

func (m *Model) trackClusterFeed() {
	need := m.clusterFeedNeeded()
	if m.wideFeed && !need {
		if r, ok := m.dg.diag.(data.ClusterEventsReleaser); ok {
			r.ReleaseClusterEvents()
		}
	}
	m.wideFeed = need
}

func (m *Model) refreshEventsScreen() {
	if !m.onScreen(ScreenEvents) || m.dg.diag == nil || !m.eventsSelectable() {
		m.ev.feed = data.EventFeed{Scope: m.ev.Scope}
		return
	}
	m.ev.feed = m.dg.diag.ScopedEvents(data.EventQuery{Scope: m.ev.Scope, Pod: m.ev.Pod, Namespace: m.ev.Namespace})
	m.syncEventSelection()
	m.loadEventDetail()
}

func (m *Model) syncEventSelection() {
	items := m.ev.feed.Items
	if m.ev.UID != "" {
		if i := slices.IndexFunc(items, func(e data.Event) bool { return e.UID == m.ev.UID }); i >= 0 {
			m.ev.Cursor, m.ev.gone = i, false
			m.ev.held = items[i]
			return
		}
		if m.ev.reading {
			m.ev.gone = true
			return
		}
	}
	m.ev.gone = false
	m.ev.Cursor = max(0, min(m.ev.Cursor, len(items)-1))
	m.adoptEvent()
}

func (m *Model) adoptEvent() {
	m.ev.UID, m.ev.held = "", data.Event{}
	if m.ev.Cursor >= 0 && m.ev.Cursor < len(m.ev.feed.Items) {
		m.ev.held = m.ev.feed.Items[m.ev.Cursor]
		m.ev.UID = m.ev.held.UID
	}
}

func (m *Model) selectEvent(i int) {
	m.ev.Cursor, m.ev.msgScroll, m.ev.gone = i, 0, false
	m.adoptEvent()
}

func (m *Model) stopReadingEvent() {
	m.ev.reading = false
	if m.ev.gone {
		m.syncEventSelection()
	}
}

func (m Model) eventsPage() int {
	return max(1, ui.EventsTableRows(m.layout.Events.H))
}

func (m *Model) moveEvent(delta int) {
	if m.ev.reading {
		m.ev.msgScroll = max(0, min(m.ev.msgScroll+delta, m.maxMsgScroll()))
		return
	}
	next := max(0, min(len(m.ev.feed.Items)-1, m.ev.Cursor+delta))
	if next != m.ev.Cursor || m.ev.gone {
		m.selectEvent(next)
	}
}

func (m Model) maxMsgScroll() int {
	lines := ui.EventMessageLines(m.eventsState(), max(1, m.layout.Message.W-4))
	return max(0, len(lines)-ui.EventMessageRows(m.layout.Message.H))
}

func (m Model) handleEventsKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Up):
		m.moveEvent(-1)
	case key.Matches(msg, k.Down):
		m.moveEvent(1)
	case key.Matches(msg, k.PageUp):
		m.moveEvent(-m.eventsPage())
	case key.Matches(msg, k.PageDown):
		m.moveEvent(m.eventsPage())
	case key.Matches(msg, k.Home):
		m.moveEvent(-len(m.ev.feed.Items) - m.maxMsgScroll())
	case key.Matches(msg, k.End):
		m.moveEvent(len(m.ev.feed.Items) + m.maxMsgScroll())
	case key.Matches(msg, k.Enter):
		if m.eventsState().Selected() != nil {
			m.ev.reading, m.ev.msgScroll = true, 0
			m.loadEventDetail()
		}
	case key.Matches(msg, k.EventScope):
		m.cycleEventsScope()
	default:
		return updateBody(m, msg)
	}
	return m, nil
}

func (m *Model) handleEventsClick(ms tea.Mouse) {
	switch {
	case m.layout.Events.Contains(ms.X, ms.Y):
		m.stopReadingEvent()
		row := ms.Y - (m.layout.Events.Y + 1 + ui.EventsTableTop)
		rows := ui.EventsTableRows(m.layout.Events.H)
		if row < 0 || row >= rows {
			return
		}
		idx := ui.ScrollOffset(m.ev.Cursor, len(m.ev.feed.Items), rows) + row
		if idx < len(m.ev.feed.Items) && idx != m.ev.Cursor {
			m.selectEvent(idx)
		}
	case m.layout.Message.Contains(ms.X, ms.Y) && m.eventsState().Selected() != nil:
		m.ev.reading = true
		m.loadEventDetail()
	}
}

func (m *Model) handleEventsWheel(ms tea.Mouse) {
	delta := 0
	switch ms.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return
	}
	switch {
	case m.layout.Events.Contains(ms.X, ms.Y):
		reading := m.ev.reading
		m.ev.reading = false
		m.moveEvent(delta)
		m.ev.reading = reading && !m.ev.gone
		m.loadEventDetail()
	case m.layout.Message.Contains(ms.X, ms.Y):
		m.ev.msgScroll = max(0, min(m.ev.msgScroll+delta, m.maxMsgScroll()))
	}
}

package app

import (
	"context"
	"errors"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type RootOptions struct {
	Options
	Initial data.Target
	Connect func(data.Target) Connector
	Catalog func() (data.ContextCatalog, error)
}

type rootShared struct {
	mu       sync.Mutex
	current  *session
	attempt  *session
	retiring []*session
	ended    bool
}

type Root struct {
	base      Options
	connect   func(data.Target) Connector
	catalog   func() (data.ContextCatalog, error)
	shared    *rootShared
	cur       tea.Model
	active    *data.Target
	pending   data.Target
	attempt   uint64
	gen       uint64
	published uint64
	memory    map[string]string
	pick      *picker
	catalogs  uint64
	w, h      int
}

type catalogMsg struct {
	seq uint64
	cat data.ContextCatalog
	err error
}

func newSession() *session {
	_, cancel := context.WithCancel(context.Background())
	return &session{cancel: cancel}
}

func NewRoot(opts RootOptions) Root {
	r := Root{
		base:    opts.Options,
		connect: opts.Connect,
		catalog: opts.Catalog,
		shared:  &rootShared{},
		memory:  map[string]string{},
	}
	if opts.Source == nil {
		next, _ := r.begin(opts.Initial)
		return next.(Root)
	}
	r.attempt, r.gen = 1, 1
	base := r.base
	base.Session = sessionRef(base.Source, "", r.gen)
	m := New(base)
	m.session.Context, m.session.Namespace = m.snap.Cluster.Context, m.snap.Cluster.Namespace
	r.cur, r.pending = m, data.Target{Context: m.snap.Cluster.Context, Namespace: m.snap.Cluster.Namespace}
	r.shared.current = newSession()
	r.publish()
	return r
}

func (r Root) Init() tea.Cmd {
	return r.cur.Init()
}

func (r Root) Shutdown() {
	s := r.shared
	s.mu.Lock()
	s.ended = true
	all := append([]*session{s.current, s.attempt}, s.retiring...)
	s.current, s.attempt, s.retiring = nil, nil, nil
	s.mu.Unlock()
	for _, x := range all {
		if x != nil {
			x.end()
		}
	}
}

func (r *Root) handover(next *session) []tea.Cmd {
	s := r.shared
	s.mu.Lock()
	var cmds []tea.Cmd
	live := s.retiring[:0]
	for _, x := range s.retiring {
		if !x.over.Load() {
			live = append(live, x)
		}
	}
	s.retiring = live
	for _, old := range []*session{s.current, s.attempt} {
		if old == nil {
			continue
		}
		old.cancel()
		s.retiring = append(s.retiring, old)
		cmds = append(cmds, func() tea.Msg {
			old.end()
			return nil
		})
	}
	s.current, s.attempt = nil, next
	ended := s.ended
	s.mu.Unlock()
	if ended {
		next.end()
	}
	return cmds
}

func (r Root) model() (Model, bool) {
	switch cur := r.cur.(type) {
	case Model:
		return cur, true
	case Loader:
		if cur.main != nil {
			return *cur.main, true
		}
	}
	return Model{}, false
}

func (r Root) begin(t data.Target) (tea.Model, tea.Cmd) {
	r.attempt++
	r.gen++
	opts := r.base
	if m, ok := r.model(); ok {
		opts.Palette = m.pal
		opts.ShowTree = m.tree.visible
	}
	var prev *data.Target
	if r.active != nil {
		a := *r.active
		prev = &a
	}
	var connect Connector
	if r.connect != nil {
		connect = r.connect(t)
	}
	target := t
	l := NewLoader(LoaderOptions{Options: opts, Context: t.Context, Connect: connect, Attempt: r.attempt, Generation: r.gen, Target: &target, Previous: prev, Choices: true})
	l.w, l.h = r.w, r.h
	cmds := r.handover(l.sess)
	r.cur, r.pending, r.pick = l, t, nil
	return r, tea.Batch(append(cmds, l.Init())...)
}

func (r *Root) publish() {
	m, ok := r.model()
	if !ok || r.published == r.attempt {
		return
	}
	r.published = r.attempt
	t := data.Target{Context: m.snap.Cluster.Context, Namespace: r.pending.Namespace}
	r.active = &t
	r.memory[t.Context] = t.Namespace
	s := r.shared
	s.mu.Lock()
	if s.attempt != nil {
		s.current, s.attempt = s.attempt, nil
	}
	s.mu.Unlock()
}

func (r Root) attemptLive(attempt uint64) bool {
	l, ok := r.cur.(Loader)
	return ok && l.attempt == attempt
}

func (r Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.w, r.h = msg.Width, msg.Height
	case pickMsg:
		if !r.pickAllowed(msg) {
			return r, nil
		}
		return r.openPicker(msg.kind)
	case catalogMsg:
		if r.pick == nil || !r.pick.loading || r.pick.seq != msg.seq {
			return r, nil
		}
		r.pick = contextPicker(msg.cat, msg.err, r.active, r.memory)
		r.pick.seq = msg.seq
		return r, nil
	case choiceMsg:
		if !r.attemptLive(msg.attempt) {
			return r, nil
		}
		switch {
		case msg.choice == choiceRetry:
			return r.begin(r.pending)
		case msg.choice == choiceBack && r.active != nil:
			return r.begin(*r.active)
		}
		return r, nil
	case progressMsg:
		if !r.attemptLive(msg.attempt) {
			return r, nil
		}
	case connectedMsg:
		if !r.attemptLive(msg.attempt) {
			return r, nil
		}
	case loaderFrameMsg:
		if !r.attemptLive(msg.attempt) {
			return r, nil
		}
	case tea.KeyPressMsg:
		if r.pick != nil && msg.String() != "ctrl+c" {
			return r.pickKey(msg)
		}
	case tea.MouseClickMsg:
		if r.pick != nil {
			return r.pickClick(tea.Mouse(msg))
		}
	case tea.MouseWheelMsg:
		if r.pick != nil {
			r.pick.move(map[tea.MouseButton]int{tea.MouseWheelUp: -1, tea.MouseWheelDown: 1}[msg.Button])
			return r, nil
		}
	case tea.MouseMotionMsg:
		if r.pick != nil {
			return r, nil
		}
	case tea.PasteMsg:
		if r.pick != nil {
			return r, r.pick.paste(msg)
		}
	}
	next, cmd := r.cur.Update(msg)
	r.cur = next
	if _, ok := msg.(connectedMsg); ok {
		r.publish()
	}
	return r, cmd
}

func (r Root) pickAllowed(msg pickMsg) bool {
	if msg.loader {
		l, ok := r.cur.(Loader)
		return ok && l.attempt == msg.attempt && l.err != nil
	}
	m, ok := r.model()
	return ok && r.published == r.attempt && m.session.Generation == msg.session
}

func (r Root) openPicker(kind pickKind) (Root, tea.Cmd) {
	switch kind {
	case pickContext:
		r.catalogs++
		seq, catalog := r.catalogs, r.catalog
		r.pick = loadingPicker(seq)
		return r, func() tea.Msg {
			if catalog == nil {
				return catalogMsg{seq: seq, err: errors.New("sem fonte de contextos")}
			}
			cat, err := catalog()
			return catalogMsg{seq: seq, cat: cat, err: err}
		}
	case pickNamespace:
		m, ok := r.model()
		if !ok || r.active == nil {
			return r, nil
		}
		r.pick = namespacePicker(*r.active, m.snap)
	}
	return r, nil
}

func (r Root) choose(res pickResult, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	switch {
	case res.close:
		r.pick = nil
		return r, nil
	case !res.ok:
		return r, cmd
	}
	r.pick = nil
	if _, ok := r.model(); ok && r.active != nil && *r.active == res.target {
		return r, nil
	}
	return r.begin(res.target)
}

func (r Root) pickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	res, cmd := r.pick.key(msg)
	return r.choose(res, cmd)
}

func (r Root) pickClick(ms tea.Mouse) (tea.Model, tea.Cmd) {
	if ms.Button != tea.MouseLeft {
		return r, nil
	}
	st := r.pick.state()
	g := ui.PickerLayout(r.w, r.h, st)
	if !g.Contains(ms.X, ms.Y) {
		r.pick = nil
		return r, nil
	}
	if r.pick.inputting {
		return r, nil
	}
	if idx, ok := g.RowAt(ms.X, ms.Y, st.Cursor, len(st.Items)); ok {
		r.pick.cursor = idx
		res, cmd := r.pick.confirm()
		return r.choose(res, cmd)
	}
	return r, nil
}

func (r Root) uiCtx() ui.Ctx {
	switch cur := r.cur.(type) {
	case Model:
		return ui.Ctx{Pal: cur.pal, Clock: cur.clock}
	case Loader:
		return ui.Ctx{Pal: cur.pal, Clock: cur.clock}
	}
	return ui.Ctx{Pal: theme.DefaultPalette(), Clock: anim.Fixed(0, 0, 0)}
}

func (r Root) Render() *draw.Canvas {
	var c *draw.Canvas
	switch cur := r.cur.(type) {
	case Model:
		c = cur.Render()
	case Loader:
		c = cur.Render()
	default:
		c = draw.NewCanvas(r.w, r.h, theme.BgApp)
	}
	if r.pick != nil {
		ui.Picker(c.Region(), r.pick.state(), r.uiCtx())
	}
	return c
}

func (r Root) View() tea.View {
	content := ""
	if r.w > 0 && r.h > 0 {
		content = r.Render().String()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.BackgroundColor = theme.BgApp
	v.WindowTitle = "lazykube"
	l, loading := r.cur.(Loader)
	if _, ok := r.model(); ok || r.pick != nil || (loading && l.choices) {
		v.MouseMode = tea.MouseModeCellMotion
		v.ReportFocus = true
	}
	return v
}

type guarded interface {
	GuardSession() guard.Session
	AccessMode() guard.Mode
}

func sessionRef(src data.Source, context string, gen uint64) data.SessionRef {
	ref := data.SessionRef{Context: context, Generation: gen, Mode: data.ReadOnlyMode}
	if g, ok := src.(guarded); ok {
		s, mode := g.GuardSession(), g.AccessMode()
		ref.Context, ref.Namespace, ref.Identity = s.Context, s.Namespace, s.Identity
		ref.Mode = data.EffectiveMode{ReadOnly: mode.ReadOnly, Protected: mode.Protected, Valid: mode.ConfigValid}
	}
	return ref
}

func (r Root) Connecting() bool {
	_, ok := r.cur.(Loader)
	return ok
}

func unwrap(final tea.Model) tea.Model {
	if r, ok := final.(Root); ok {
		return r.cur
	}
	return final
}

func Failure(final tea.Model) (string, string, bool) {
	if l, ok := unwrap(final).(Loader); ok {
		return l.Failure()
	}
	return "", "", false
}

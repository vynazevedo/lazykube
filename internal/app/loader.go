package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const (
	transitionSeconds = 0.4
	loaderEvents      = 16
	ExitFailure       = 1
	ExitCancelled     = 130
)

type Connector func(ctx context.Context, report func(data.Progress)) (data.Source, func(), error)

type LoaderOptions struct {
	Options
	Context    string
	Connect    Connector
	Attempt    uint64
	Generation uint64
	Target     *data.Target
	Previous   *data.Target
	Choices    bool
}

type progressMsg struct {
	p       data.Progress
	attempt uint64
}

type connectedMsg struct {
	src     data.Source
	err     error
	attempt uint64
}

type loaderFrameMsg struct {
	at      time.Time
	gen     int
	attempt uint64
}

type session struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	closeFn func()
	ended   bool
	over    atomic.Bool
}

func (s *session) adopt(closeFn func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		if closeFn != nil {
			closeFn()
		}
		return false
	}
	s.closeFn = closeFn
	return true
}

func (s *session) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel()
	if s.ended {
		return
	}
	s.ended = true
	if s.closeFn != nil {
		s.closeFn()
		s.closeFn = nil
	}
	s.over.Store(true)
}

type loadStep struct {
	state  ui.StepState
	detail string
}

type Loader struct {
	opts     Options
	connect  Connector
	ctx      context.Context
	sess     *session
	events   chan tea.Msg
	w, h     int
	pal      theme.Palette
	clock    anim.Clock
	animated bool
	fps      int
	gen      int
	now      func() time.Time

	context   string
	steps     [4]loadStep
	bar       anim.Spring
	err       error
	errText   string
	failed    data.Stage
	cancelled bool

	main *Model
	fade anim.Fade

	attempt    uint64
	generation uint64
	target     *data.Target
	previous   *data.Target
	choices    bool
}

func NewLoader(opts LoaderOptions) Loader {
	base := opts.Options
	now := base.Now
	if now == nil {
		now = time.Now
	}
	fps := base.FPS
	if fps <= 0 {
		fps = DefaultFPS
	}
	fps = min(fps, MaxFPS)
	pal := base.Palette
	if pal.Name == "" {
		pal = theme.DefaultPalette()
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := Loader{
		opts:     base,
		connect:  opts.Connect,
		ctx:      ctx,
		sess:     &session{cancel: cancel},
		events:   make(chan tea.Msg, loaderEvents),
		pal:      pal,
		animated: !base.NoAnim,
		fps:      fps,
		now:      now,
		context:  opts.Context,
		bar:      anim.NewSpring(fps, 0),

		attempt:    opts.Attempt,
		generation: opts.Generation,
		target:     opts.Target,
		previous:   opts.Previous,
		choices:    opts.Choices,
	}
	if l.animated {
		l.clock = anim.NewClock(now(), fps)
	} else {
		l.clock = anim.Fixed(0, 0, 0)
	}
	l.steps[data.StageKubeconfig].state = ui.StepRunning
	return l
}

func (l Loader) Init() tea.Cmd {
	return tea.Batch(l.connectCmd(), waitEvent(l.events), l.tick())
}

func (l Loader) Shutdown() {
	l.sess.end()
}

func (l Loader) connectCmd() tea.Cmd {
	ctx, events, sess, connect, attempt := l.ctx, l.events, l.sess, l.connect, l.attempt
	return func() tea.Msg {
		var mu sync.Mutex
		closed := false
		report := func(p data.Progress) {
			mu.Lock()
			defer mu.Unlock()
			if closed {
				return
			}
			select {
			case events <- progressMsg{p: p, attempt: attempt}:
			case <-ctx.Done():
			}
		}
		var src data.Source
		var closeFn func()
		err := errors.New("no connector configured")
		if connect != nil {
			src, closeFn, err = connect(ctx, report)
		}
		mu.Lock()
		closed = true
		mu.Unlock()
		if err == nil && !sess.adopt(closeFn) {
			src, err = nil, context.Canceled
		}
		select {
		case events <- connectedMsg{src: src, err: err, attempt: attempt}:
		case <-ctx.Done():
		}
		close(events)
		return nil
	}
}

func waitEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (l Loader) tick() tea.Cmd {
	if !l.animated || l.err != nil || l.cancelled {
		return nil
	}
	gen, attempt := l.gen, l.attempt
	return tea.Tick(time.Second/time.Duration(l.fps), func(t time.Time) tea.Msg {
		return loaderFrameMsg{at: t, gen: gen, attempt: attempt}
	})
}

func (l Loader) finished() bool {
	return l.main != nil || l.err != nil || l.cancelled
}

func (l Loader) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		l.w, l.h = msg.Width, msg.Height
		if l.main != nil {
			return l.forward(msg)
		}
		return l, nil
	case progressMsg:
		if l.finished() || msg.attempt != l.attempt {
			return l, nil
		}
		l.apply(msg.p)
		return l, waitEvent(l.events)
	case connectedMsg:
		if l.finished() || msg.attempt != l.attempt {
			return l, nil
		}
		if msg.err != nil {
			l.fail(msg.err)
			return l, nil
		}
		return l.start(msg.src)
	case loaderFrameMsg:
		if msg.gen != l.gen || msg.attempt != l.attempt {
			return l, nil
		}
		l.clock.Advance(msg.at)
		l.bar.Step()
		if l.main != nil && l.fade.Done(l.clock.Phase) {
			return *l.main, nil
		}
		return l, l.tick()
	case tea.MouseClickMsg:
		if l.main != nil {
			return l.forward(msg)
		}
		if l.choices && msg.Button == tea.MouseLeft {
			if key, ok := l.hintAt(msg.X, msg.Y); ok {
				if key == "ctrl+c" {
					return l.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
				}
				return l, l.choose(key)
			}
		}
		return l, nil
	case tea.KeyPressMsg:
		if l.main != nil {
			return l.forward(msg)
		}
		if msg.String() == "ctrl+c" {
			l.cancelled = true
			l.sess.cancel()
			return l, tea.Quit
		}
		if l.choices {
			return l, l.choose(msg.String())
		}
		switch msg.String() {
		case "q", "esc":
			if l.err != nil {
				return l, tea.Quit
			}
		}
		return l, nil
	}
	if l.main != nil {
		return l.forward(msg)
	}
	return l, nil
}

func (l Loader) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := l.main.Update(msg)
	if m, ok := next.(Model); ok {
		l.main = &m
	}
	return l, cmd
}

func (l *Loader) apply(p data.Progress) {
	if p.Stage < 0 || int(p.Stage) >= len(l.steps) {
		return
	}
	if p.Context != "" {
		l.context = p.Context
	}
	s := &l.steps[p.Stage]
	s.detail = data.Sanitize(p.Detail)
	if p.Done {
		s.state = ui.StepDone
		if next := p.Stage + 1; int(next) < len(l.steps) && l.steps[next].state == ui.StepPending {
			l.steps[next].state = ui.StepRunning
			l.steps[next].detail = ""
		}
	} else {
		s.state = ui.StepRunning
		for i := data.StageKubeconfig; i < p.Stage; i++ {
			l.steps[i].state = ui.StepDone
		}
	}
	l.setBar()
}

func (l *Loader) setBar() {
	done := 0
	for _, s := range l.steps {
		if s.state == ui.StepDone {
			done++
		}
	}
	l.bar.SetTarget(float64(done) / float64(len(l.steps)))
	if !l.animated {
		l.bar.Snap()
	}
}

func (l *Loader) failingStage() data.Stage {
	for i, s := range l.steps {
		if s.state != ui.StepDone {
			return data.Stage(i)
		}
	}
	return data.StageSync
}

func (l *Loader) fail(err error) {
	l.err = err
	l.failed = l.failingStage()
	l.errText = errorText(data.PublicText(err))
	l.steps[l.failed].state = ui.StepFailed
	for i := l.failed + 1; int(i) < len(l.steps); i++ {
		l.steps[i] = loadStep{}
	}
	l.bar.Snap()
}

func errorText(text string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		part = strings.TrimSpace(data.Sanitize(part))
		if part == "" {
			continue
		}
		switch {
		case b.Len() == 0:
		case strings.HasSuffix(b.String(), ":"):
			b.WriteByte(' ')
		default:
			b.WriteString("; ")
		}
		b.WriteString(part)
	}
	return b.String()
}

func (l Loader) start(src data.Source) (tea.Model, tea.Cmd) {
	for i := range l.steps {
		l.steps[i].state = ui.StepDone
	}
	l.setBar()
	opts := l.opts
	opts.Source = src
	opts.Palette = l.pal
	opts.Session = data.SessionRef{Context: l.context, Generation: l.generation, Mode: data.ModeReadOnly}
	if l.target != nil {
		opts.Session.Namespace = l.target.Namespace
	}
	m := New(opts)
	if l.w > 0 && l.h > 0 {
		next, _ := m.Update(tea.WindowSizeMsg{Width: l.w, Height: l.h})
		m = next.(Model)
	}
	if !l.animated {
		return m, m.Init()
	}
	l.main = &m
	l.fade = anim.Fade{From: 0, To: 1, Start: l.clock.Phase, Duration: transitionSeconds}
	return l, m.Init()
}

func (l Loader) stageTitle(s data.Stage) string {
	switch s {
	case data.StageKubeconfig:
		return "carregando kubeconfig"
	case data.StageConnect:
		if l.context != "" {
			return "conectando ao contexto " + data.Sanitize(l.context)
		}
		return "conectando ao contexto"
	case data.StagePermissions:
		return "verificando permissões"
	default:
		return "sincronizando cache"
	}
}

func (l Loader) state() ui.LoadingState {
	st := ui.LoadingState{Progress: l.bar.Pos, Error: l.errText, Hints: l.hints()}
	if l.target != nil {
		st.Target = targetLabel(*l.target, l.context)
	}
	if l.previous != nil {
		st.Previous = targetLabel(*l.previous, "")
	}
	for i, s := range l.steps {
		st.Steps = append(st.Steps, ui.LoadingStep{Title: l.stageTitle(data.Stage(i)), Detail: s.detail, State: s.state})
	}
	return st
}

func (l Loader) Render() *draw.Canvas {
	k := 0.0
	if l.main != nil {
		k = l.fade.At(l.clock.Phase)
	}
	if l.main != nil && k >= 0.5 {
		c := l.main.Render()
		c.Fade(theme.BgApp, (1-k)*2)
		return c
	}
	c := draw.NewCanvas(l.w, l.h, theme.BgApp)
	ui.Loading(c.Region(), l.state(), ui.Ctx{Pal: l.pal, Clock: l.clock})
	c.Fade(theme.BgApp, k*2)
	return c
}

func (l Loader) View() tea.View {
	content := ""
	if l.w > 0 && l.h > 0 {
		content = l.Render().String()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.BackgroundColor = theme.BgApp
	v.WindowTitle = "lazykube"
	if l.main != nil {
		v.MouseMode = tea.MouseModeCellMotion
		v.ReportFocus = true
	}
	return v
}

func (l Loader) hintAt(x, y int) (string, bool) {
	if l.w <= 0 || l.h <= 0 {
		return "", false
	}
	c := draw.NewCanvas(l.w, l.h, theme.BgApp)
	for _, s := range ui.Loading(c.Region(), l.state(), ui.Ctx{Pal: l.pal, Clock: l.clock}) {
		if y == s.Y && x >= s.X && x < s.X+s.W {
			return s.Key, true
		}
	}
	return "", false
}

func (l Loader) canBack() bool {
	return l.previous != nil && (l.target == nil || *l.previous != *l.target)
}

func (l Loader) choose(key string) tea.Cmd {
	attempt := l.attempt
	emit := func(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }
	switch {
	case key == "esc" && l.err == nil && l.canBack():
		return emit(choiceMsg{choice: choiceBack, attempt: attempt})
	case l.err == nil:
		return nil
	case key == "r":
		return emit(choiceMsg{choice: choiceRetry, attempt: attempt})
	case (key == "b" || key == "esc") && l.canBack():
		return emit(choiceMsg{choice: choiceBack, attempt: attempt})
	case key == "c":
		return emit(pickMsg{kind: pickContext, attempt: attempt, loader: true})
	case key == "q" || key == "esc":
		return tea.Quit
	}
	return nil
}

func (l Loader) hints() []ui.KeyHelp {
	if !l.choices {
		return nil
	}
	if l.err == nil {
		out := []ui.KeyHelp{{Key: "ctrl+c", Desc: "cancelar e sair"}}
		if l.canBack() {
			out = append(out, ui.KeyHelp{Key: "esc", Desc: "voltar ao anterior"})
		}
		return out
	}
	out := []ui.KeyHelp{{Key: "r", Desc: "repetir"}}
	if l.canBack() {
		out = append(out, ui.KeyHelp{Key: "b", Desc: "voltar ao anterior"})
	}
	return append(out, ui.KeyHelp{Key: "c", Desc: "escolher contexto"}, ui.KeyHelp{Key: "q", Desc: "sair"})
}

func (l Loader) Failure() (string, string, bool) {
	if l.err == nil {
		return "", "", false
	}
	return l.stageTitle(l.failed), l.errText, true
}

func ExitCode(final tea.Model) int {
	l, ok := unwrap(final).(Loader)
	switch {
	case !ok:
		return 0
	case l.cancelled:
		return ExitCancelled
	case l.err != nil:
		return ExitFailure
	}
	return 0
}

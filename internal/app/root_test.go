package app

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type fakeLive struct {
	*data.Demo
	changes chan struct{}
}

func (s fakeLive) Changes() <-chan struct{} { return s.changes }

type fakeSource struct {
	target data.Target
	closes atomic.Int32
}

type fakeCluster struct {
	mu        sync.Mutex
	sources   []*fakeSource
	fail      map[string]error
	block     map[string]chan struct{}
	holdClose map[string]chan struct{}
	ignoreCtx bool
	live      atomic.Int32
}

func newFakeCluster() *fakeCluster {
	return &fakeCluster{fail: map[string]error{}, block: map[string]chan struct{}{}, holdClose: map[string]chan struct{}{}}
}

func fakeCatalog() (data.ContextCatalog, error) {
	return data.ContextCatalog{Current: "alpha", Contexts: []data.KubeContext{
		{Name: "alpha", Cluster: "alpha-cluster", User: "alpha-user", Current: true},
		{Name: "beta", Cluster: "beta-cluster", User: "beta-user"},
		{Name: "gamma", Cluster: "gamma-cluster", User: "gamma-user", Namespace: "apps"},
	}}, nil
}

func (f *fakeCluster) setFail(ctx string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[ctx] = err
}

func (f *fakeCluster) opened(ctx string) []*fakeSource {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*fakeSource
	for _, s := range f.sources {
		if s.target.Context == ctx {
			out = append(out, s)
		}
	}
	return out
}

func (f *fakeCluster) all() []*fakeSource {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*fakeSource(nil), f.sources...)
}

func (f *fakeCluster) connect(t data.Target) Connector {
	return func(ctx context.Context, report func(data.Progress)) (data.Source, func(), error) {
		if t.Context == "" {
			t.Context = "alpha"
		}
		f.mu.Lock()
		block, err, hold, ignore := f.block[t.Context], f.fail[t.Context], f.holdClose[t.Context], f.ignoreCtx
		f.mu.Unlock()
		report(data.Progress{Stage: data.StageKubeconfig, Done: true, Context: t.Context})
		if block != nil {
			if ignore {
				<-block
			} else {
				select {
				case <-block:
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				}
			}
		}
		if err != nil {
			return nil, nil, err
		}
		src := &fakeSource{target: t}
		f.mu.Lock()
		f.sources = append(f.sources, src)
		f.mu.Unlock()
		live := fakeLive{Demo: data.NewDemoScoped(t.Context, t.Namespace), changes: make(chan struct{}, 1)}
		stop := make(chan struct{})
		f.live.Add(1)
		go func() { <-stop }()
		var once sync.Once
		return live, func() {
			src.closes.Add(1)
			once.Do(func() {
				if hold != nil {
					<-hold
				}
				close(stop)
				close(live.changes)
				f.live.Add(-1)
			})
		}, nil
	}
}

type rootDriver struct {
	t    *testing.T
	r    Root
	msgs chan tea.Msg
	done chan struct{}
	quit bool
}

func newRootDriver(t *testing.T, f *fakeCluster, initial data.Target) *rootDriver {
	t.Helper()
	return newRootDriverWith(t, f, initial, fakeCatalog)
}

func newRootDriverWith(t *testing.T, f *fakeCluster, initial data.Target, catalog func() (data.ContextCatalog, error)) *rootDriver {
	t.Helper()
	r := NewRoot(RootOptions{
		Options: Options{Palette: theme.DefaultPalette(), FPS: 30, NoAnim: true, Now: func() time.Time { return epoch }},
		Initial: initial,
		Connect: f.connect,
		Catalog: catalog,
	})
	d := &rootDriver{t: t, msgs: make(chan tea.Msg, 64), done: make(chan struct{})}
	next, _ := r.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	d.r = next.(Root)
	d.run(d.r.Init())
	t.Cleanup(func() {
		d.r.Shutdown()
		close(d.done)
	})
	return d
}

func (d *rootDriver) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		select {
		case d.msgs <- msg:
		case <-d.done:
		}
	}()
}

func (d *rootDriver) send(msg tea.Msg) {
	next, cmd := d.r.Update(msg)
	d.r = next.(Root)
	d.run(cmd)
}

func (d *rootDriver) press(keys ...string) {
	for _, k := range keys {
		d.send(keyPress(k))
		d.drain()
	}
}

func (d *rootDriver) dispatch(msg tea.Msg) {
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			d.run(c)
		}
	case tea.QuitMsg:
		d.quit = true
	default:
		d.send(msg)
	}
}

func (d *rootDriver) drain() {
	for {
		select {
		case msg := <-d.msgs:
			d.dispatch(msg)
		case <-time.After(20 * time.Millisecond):
			return
		}
	}
}

func (d *rootDriver) until(what string, ok func(Root) bool) {
	d.t.Helper()
	deadline := time.After(5 * time.Second)
	for !ok(d.r) {
		select {
		case msg := <-d.msgs:
			d.dispatch(msg)
		case <-deadline:
			d.t.Fatalf("timeout waiting for %s", what)
		}
	}
}

func connectedTo(ctx, ns string) func(Root) bool {
	return func(r Root) bool {
		m, ok := r.model()
		return ok && r.active != nil && *r.active == (data.Target{Context: ctx, Namespace: ns}) && m.snap.Cluster.Context == ctx
	}
}

func failed(r Root) bool {
	l, ok := r.cur.(Loader)
	return ok && l.err != nil
}

func picking(r Root) bool { return r.pick != nil && !r.pick.loading }

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func closedOnce(t *testing.T, srcs ...*fakeSource) {
	t.Helper()
	for _, s := range srcs {
		eventually(t, "close of "+s.target.Context, func() bool { return s.closes.Load() >= 1 })
		if n := s.closes.Load(); n != 1 {
			t.Fatalf("source %+v closed %d times", s.target, n)
		}
	}
}

func (d *rootDriver) pickTarget(kind pickKind, label string) {
	d.t.Helper()
	key := map[pickKind]string{pickContext: "c", pickNamespace: "n"}[kind]
	d.press(key)
	d.until("picker", func(r Root) bool { return r.pick != nil && !r.pick.loading })
	if d.r.pick.kind != kind {
		d.t.Fatalf("%s opens the %v picker", key, kind)
	}
	for i, e := range d.r.pick.visible() {
		if e.item.Label == label {
			d.r.pick.cursor = i
			d.press("enter")
			return
		}
	}
	d.t.Fatalf("picker has no %q", label)
}

func TestRootSwitchesContextsAndRestoresNamespaces(t *testing.T) {
	f := newFakeCluster()
	d := newRootDriver(t, f, data.Target{})
	d.until("initial connection", connectedTo("alpha", ""))
	first := d.r.gen
	if plain := d.r.Render().Plain(); !strings.Contains(plain, "context alpha namespace Todos") {
		t.Fatal("the header shows the effective context and namespace, including Todos")
	}
	d.press("6", "/", "x")
	if m, _ := d.r.model(); !m.onScreen(ScreenEvents) {
		t.Fatal("setup reaches Events")
	}
	d.press("esc")

	d.pickTarget(pickContext, "beta")
	d.until("beta", connectedTo("beta", "default"))
	m, _ := d.r.model()
	if d.r.gen <= first || m.session.Generation != d.r.gen || m.session.Context != "beta" {
		t.Fatalf("a switch publishes a new generation: root %d model %+v", d.r.gen, m.session)
	}
	if !m.onScreen(ScreenDashboard) || m.nav.Depth() != 0 || m.filter.Value() != "" || m.selName != "" {
		t.Fatal("the new session starts on the Dashboard without inherited frames, filters or selection")
	}
	if plain := d.r.Render().Plain(); !strings.Contains(plain, "context beta namespace default") || strings.Contains(plain, "context alpha") {
		t.Fatal("the header follows the new session only")
	}
	closedOnce(t, f.opened("alpha")...)

	if p, _ := d.r.model(); len(namespacePicker(*d.r.active, p.snap).entries) != 3 || p.snap.Cluster.Listed {
		t.Fatal("a restricted connection offers Todos, its observed namespace and manual entry")
	}
	d.pickTarget(pickNamespace, data.AllNamespacesLabel)
	d.until("beta widened explicitly", connectedTo("beta", ""))
	closedOnce(t, f.opened("beta")[0])
	d.pickTarget(pickNamespace, "kube-system")
	d.until("beta kube-system", connectedTo("beta", "kube-system"))

	d.pickTarget(pickContext, "alpha")
	d.until("alpha back to Todos", connectedTo("alpha", ""))

	d.pickTarget(pickContext, "beta")
	d.until("beta remembers kube-system", connectedTo("beta", "kube-system"))

	d.pickTarget(pickContext, "gamma")
	d.until("gamma uses its kubeconfig namespace", connectedTo("gamma", "apps"))

	d.pickTarget(pickNamespace, data.AllNamespacesLabel)
	d.until("Todos is an explicit choice", connectedTo("gamma", ""))

	before := d.r.attempt
	d.pickTarget(pickNamespace, data.AllNamespacesLabel)
	d.drain()
	if d.r.attempt != before || !connectedTo("gamma", "")(d.r) {
		t.Fatal("choosing the active target does not reconnect")
	}
	for _, s := range f.all()[:len(f.all())-1] {
		closedOnce(t, s)
	}
}

func TestRootPickerInputAndCancel(t *testing.T) {
	f := newFakeCluster()
	d := newRootDriver(t, f, data.Target{Context: "alpha", Namespace: "apps"})
	d.until("initial", connectedTo("alpha", "apps"))
	attempt := d.r.attempt

	d.press("/", "c", "n")
	if m, _ := d.r.model(); d.r.pick != nil || m.filter.Value() != "cn" {
		t.Fatal("c and n typed into the filter stay text")
	}
	d.press("esc", "esc")

	d.press("c")
	d.until("context picker", picking)
	plain := d.r.Render().Plain()
	for _, want := range []string{"Contextos do kubeconfig", "alpha", "cluster beta-cluster · user beta-user · namespace default", "namespace apps", "nada é escrito no kubeconfig"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("context picker misses %q", want)
		}
	}
	d.press("/", "g", "a", "m")
	if vis := d.r.pick.visible(); len(vis) != 1 || vis[0].item.Label != "gamma" {
		t.Fatalf("/ filters the contexts: %+v", vis)
	}
	d.press("esc", "esc")
	if d.r.pick != nil || d.r.attempt != attempt {
		t.Fatal("esc cancels without switching")
	}

	d.press("n")
	d.until("namespace picker", picking)
	if !strings.Contains(d.r.Render().Plain(), "cobertura parcial") {
		t.Fatal("without list namespaces the picker says the list is partial")
	}
	d.press("end", "enter")
	if !d.r.pick.inputting {
		t.Fatal("the last entry types a namespace")
	}
	d.press("B", "a", "d", "_", "enter")
	if d.r.pick == nil || d.r.pick.err == "" || d.r.attempt != attempt {
		t.Fatal("an invalid namespace is rejected locally")
	}
	for range 4 {
		d.press("backspace")
	}
	d.press("o", "p", "s", "enter")
	d.until("typed namespace", connectedTo("alpha", "ops"))

	d.press("c")
	d.until("picker", picking)
	st := d.r.pick.state()
	g := ui.PickerLayout(160, 48, st)
	d.send(tea.MouseClickMsg{X: g.X + 4, Y: g.RowsTop + 1, Button: tea.MouseLeft})
	d.until("click confirms beta", connectedTo("beta", "default"))

	m, _ := d.r.model()
	click := func(x int) {
		d.send(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft})
		d.drain()
	}
	hs := m.headerState()
	for x := range 160 {
		if ui.HeaderTargetAt(hs, m.layout.Header.W, x) == ui.HeaderNamespace {
			click(x + m.layout.Header.X)
			break
		}
	}
	if d.r.pick == nil || d.r.pick.kind != pickNamespace {
		t.Fatal("clicking the header namespace opens the namespace picker")
	}
	d.send(tea.MouseClickMsg{X: 0, Y: 47, Button: tea.MouseLeft})
	if d.r.pick != nil {
		t.Fatal("clicking outside the picker closes it")
	}
}

func TestRootFailureChoices(t *testing.T) {
	f := newFakeCluster()
	d := newRootDriver(t, f, data.Target{Context: "alpha"})
	d.until("initial", connectedTo("alpha", ""))
	f.setFail("beta", errors.New("forbidden: token\x1b[2J expirado"))

	d.pickTarget(pickContext, "beta")
	d.until("beta fails", failed)
	plain := d.r.Render().Plain()
	for _, want := range []string{"destino beta · default", "anterior alpha · Todos", "r repetir", "b voltar ao anterior", "c escolher contexto", "q sair"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("failure screen misses %q", want)
		}
	}
	if strings.Contains(d.r.Render().String(), "\x1b[2J") || strings.Contains(plain, "context alpha") {
		t.Fatal("the failure is sanitized and no old data is shown under the new destination")
	}
	closedOnce(t, f.opened("alpha")...)

	d.press("r")
	d.until("retry fails again", failed)
	if l := d.r.cur.(Loader); l.target.Context != "beta" {
		t.Fatal("r retries the same destination")
	}

	f.setFail("alpha", errors.New("alpha caiu"))
	d.press("b")
	d.until("back fails", func(r Root) bool { return failed(r) && r.cur.(Loader).target.Context == "alpha" })
	if plain := d.r.Render().Plain(); strings.Contains(plain, "voltar ao anterior") || !strings.Contains(plain, "r repetir") {
		t.Fatal("a failed return offers retry and the picker, with no automatic loop")
	}
	if got := len(f.opened("alpha")); got != 1 {
		t.Fatalf("the closed source is never reused: %d alpha sources", got)
	}

	f.setFail("alpha", nil)
	d.press("c")
	d.until("picker over the failure", picking)
	d.press("esc")
	if d.r.pick != nil || !failed(d.r) {
		t.Fatal("esc in the picker returns to the failure screen")
	}
	d.press("r")
	d.until("alpha reconnected with a new source", connectedTo("alpha", ""))
	if srcs := f.opened("alpha"); len(srcs) != 2 || srcs[0].closes.Load() != 1 || srcs[1].closes.Load() != 0 {
		t.Fatal("return reconnects through a new Source")
	}
}

func TestRootCancelAndLateResult(t *testing.T) {
	f := newFakeCluster()
	d := newRootDriver(t, f, data.Target{Context: "alpha"})
	d.until("initial", connectedTo("alpha", ""))
	release := make(chan struct{})
	f.mu.Lock()
	f.block["beta"], f.ignoreCtx = release, true
	f.mu.Unlock()

	d.pickTarget(pickContext, "beta")
	d.until("beta loading", func(r Root) bool { return r.Connecting() && !failed(r) })
	stale := d.r.attempt
	if !strings.Contains(d.r.Render().Plain(), "esc voltar ao anterior") {
		t.Fatal("the switch can be cancelled back to the previous context")
	}
	d.press("esc")
	d.until("back to alpha", connectedTo("alpha", ""))
	close(release)
	eventually(t, "late beta source", func() bool { return len(f.opened("beta")) == 1 })
	closedOnce(t, f.opened("beta")...)
	d.drain()
	if !connectedTo("alpha", "")(d.r) || d.r.attempt <= stale {
		t.Fatal("a late result of a cancelled attempt is not applied")
	}
	d.send(connectedMsg{src: data.NewDemo(), attempt: stale})
	if !connectedTo("alpha", "")(d.r) {
		t.Fatal("stale messages are dropped")
	}
	m, _ := d.r.model()
	if _, cmd := m.Update(dataMsg{session: m.session.Generation - 1}); cmd != nil {
		t.Fatal("a data message of an older session does not re-arm the new model")
	}
}

func TestRootQuitWhileClosing(t *testing.T) {
	f := newFakeCluster()
	hold := make(chan struct{})
	f.holdClose["alpha"] = hold
	d := newRootDriver(t, f, data.Target{Context: "alpha"})
	d.until("initial", connectedTo("alpha", ""))
	d.pickTarget(pickContext, "beta")
	d.until("beta connects while alpha is still closing", connectedTo("beta", "default"))
	alpha := f.opened("alpha")[0]
	eventually(t, "alpha close started", func() bool { return alpha.closes.Load() == 1 })
	done := make(chan struct{})
	go func() {
		d.r.Shutdown()
		close(done)
	}()
	close(hold)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waits for the closing session and ends")
	}
	closedOnce(t, f.all()...)
	if f.live.Load() != 0 {
		t.Fatalf("%d sources still open", f.live.Load())
	}
}

func TestRootRepeatedSwitchDoesNotLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()
	f := newFakeCluster()
	d := newRootDriver(t, f, data.Target{Context: "alpha"})
	d.until("initial", connectedTo("alpha", ""))
	for i := range 12 {
		next := map[bool]string{true: "beta", false: "alpha"}[i%2 == 0]
		d.pickTarget(pickContext, next)
		ns := map[string]string{"alpha": "", "beta": "default"}[next]
		d.until(next, connectedTo(next, ns))
	}
	eventually(t, "one live source", func() bool { return f.live.Load() == 1 })
	d.r.Shutdown()
	closedOnce(t, f.all()...)
	eventually(t, "goroutines back to the baseline", func() bool { return runtime.NumGoroutine() <= baseline+4 })
}

func TestRootRejectsStalePickRequests(t *testing.T) {
	f := newFakeCluster()
	d := newRootDriver(t, f, data.Target{Context: "alpha"})
	d.until("initial", connectedTo("alpha", ""))
	m, _ := d.r.model()
	_, fromAlpha := m.Update(keyPress("c"))
	_, nsFromAlpha := m.Update(keyPress("n"))
	if fromAlpha == nil || nsFromAlpha == nil {
		t.Fatal("c and n request pickers")
	}
	d.pickTarget(pickContext, "beta")
	d.until("beta", connectedTo("beta", "default"))
	d.dispatch(fromAlpha())
	d.dispatch(nsFromAlpha())
	if d.r.pick != nil {
		t.Fatal("a picker request from an older session is rejected")
	}

	f.setFail("gamma", errors.New("negado"))
	d.pickTarget(pickContext, "gamma")
	d.until("gamma fails", failed)
	_, fromFailure := d.r.cur.(Loader).Update(keyPress("c"))
	d.press("r")
	d.until("retry fails", func(r Root) bool { return failed(r) && r.cur.(Loader).attempt != 0 })
	d.dispatch(fromFailure())
	if d.r.pick != nil {
		t.Fatal("a picker request from an older attempt is rejected")
	}
	d.press("c")
	d.until("current attempt opens the picker", picking)
}

func TestRootCatalogReadDoesNotBlock(t *testing.T) {
	release := make(chan struct{})
	var reads atomic.Int32
	catalog := func() (data.ContextCatalog, error) {
		reads.Add(1)
		<-release
		return fakeCatalog()
	}
	d := newRootDriverWith(t, newFakeCluster(), data.Target{Context: "alpha"}, catalog)
	d.until("initial", connectedTo("alpha", ""))
	pressWithin := func(keys ...string) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			d.press(keys...)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("%v blocked the update loop", keys)
		}
	}
	pressWithin("c")
	if d.r.pick == nil || !d.r.pick.loading || !strings.Contains(d.r.Render().Plain(), "lendo os contextos do kubeconfig") {
		t.Fatal("the picker opens at once in a loading state")
	}
	pressWithin("esc")
	if d.r.pick != nil {
		t.Fatal("esc cancels a loading picker")
	}
	pressWithin("c")
	pressWithin("down", "enter")
	if d.r.pick == nil || d.r.attempt != 1 {
		t.Fatal("a loading picker ignores navigation and confirmation")
	}
	close(release)
	d.until("the current read fills the picker", func(r Root) bool { return r.pick != nil && !r.pick.loading })
	if n := len(d.r.pick.entries); n != 3 || reads.Load() != 2 {
		t.Fatalf("picker entries %d after %d reads", n, reads.Load())
	}
	d.press("esc")
	d.drain()
	if d.r.pick != nil {
		t.Fatal("the stale read of the cancelled picker does not reopen it")
	}
	d.press("ctrl+c")
	if !d.quit {
		t.Fatal("ctrl+c still quits")
	}
}

func TestRootPickerReceivesPaste(t *testing.T) {
	d := newRootDriver(t, newFakeCluster(), data.Target{Context: "alpha", Namespace: "apps"})
	d.until("initial", connectedTo("alpha", "apps"))
	d.press("/")
	d.send(tea.PasteMsg{Content: "under"})
	d.press("enter")
	under, _ := d.r.model()
	if under.filter.Value() != "under" {
		t.Fatal("without a picker paste still reaches the pod filter")
	}

	d.press("c")
	d.until("picker", picking)
	d.press("/")
	d.send(tea.PasteMsg{Content: "gam"})
	if vis := d.r.pick.visible(); len(vis) != 1 || vis[0].item.Label != "gamma" {
		t.Fatalf("paste reaches the picker filter: %q", d.r.pick.filter.Value())
	}
	d.press("esc", "esc")

	d.press("n")
	d.until("picker", picking)
	d.press("end", "enter")
	d.send(tea.PasteMsg{Content: "ops-team"})
	if m, _ := d.r.model(); m.filter.Value() != "under" {
		t.Fatal("paste with the picker open does not reach the model below")
	}
	d.press("enter")
	d.until("pasted namespace", connectedTo("alpha", "ops-team"))
}

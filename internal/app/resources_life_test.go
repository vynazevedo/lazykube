package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/theme"
)

var stsGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}

func resourceSession(t *testing.T, src data.Source, generation uint64, w, h int) Model {
	t.Helper()
	m := New(Options{Source: src, Palette: theme.DefaultPalette(), InitialPod: 1, Now: func() time.Time { return time.Unix(0, 0) }, Session: data.SessionRef{Context: "ctx", Generation: generation, Mode: data.ReadOnlyMode}})
	m.animated = false
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

func drainMsgs(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	select {
	case msg := <-out:
		if batch, ok := msg.(tea.BatchMsg); ok {
			var msgs []tea.Msg
			for _, c := range batch {
				msgs = append(msgs, drainMsgs(t, c)...)
			}
			return msgs
		}
		if msg == nil {
			return nil
		}
		return []tea.Msg{msg}
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

func pendingSubscription(t *testing.T, m Model, id DestinationID) (Model, tea.Msg) {
	t.Helper()
	m.activateDestination(id)
	next, cmd := m.Update(tea.FocusMsg{})
	m = next.(Model)
	for _, msg := range drainMsgs(t, cmd) {
		if _, ok := msg.(resourceCatalogMsg); !ok {
			continue
		}
		after, subscribe := m.Update(msg)
		m = after.(Model)
		for _, sub := range drainMsgs(t, subscribe) {
			if _, ok := sub.(resourceSubMsg); ok {
				return m, sub
			}
		}
	}
	t.Fatal("the leaf never produced a subscription reply")
	return m, nil
}

func TestSubscriptionReplyOfAnotherSessionIsCancelled(t *testing.T) {
	oldSrc, newSrc := newFakeBrowser(nil, nil), newFakeBrowser(nil, nil)
	oldModel, late := pendingSubscription(t, resourceSession(t, oldSrc, 1, 160, 48), DestStatefulSets)
	current := openLeaf(t, resourceSession(t, newSrc, 2, 160, 48), DestStatefulSets)
	handle := current.res.subs[stsGVR].handle
	next, _ := current.Update(late)
	current = next.(Model)
	if oldSrc.cancels.Load() != 1 {
		t.Fatalf("the handle of the older session is cancelled, got %d", oldSrc.cancels.Load())
	}
	if newSrc.cancels.Load() != 0 || current.res.subs[stsGVR].handle != handle {
		t.Fatal("a reply of another session never replaces the handle of the current one")
	}

	after, waiting := oldModel.Update(late)
	oldModel = after.(Model)
	oldHandle := oldModel.res.subs[stsGVR].handle.(*fakeHandle)
	oldHandle.changes <- struct{}{}
	var change tea.Msg
	for _, msg := range drainMsgs(t, waiting) {
		if _, ok := msg.(resourceChangeMsg); ok {
			change = msg
		}
	}
	if change == nil {
		t.Fatal("the older session never produced a change")
	}
	reads := handle.(*fakeHandle).reads.Load()
	next, _ = current.Update(change)
	current = next.(Model)
	if handle.(*fakeHandle).reads.Load() != reads {
		t.Fatal("a change of another session never reads the current page")
	}
}

func TestSubscriptionReplyDuringLoaderIsCancelled(t *testing.T) {
	f := newFakeCluster()
	d := newRootDriver(t, f, data.Target{Context: "alpha"})
	d.until("initial", connectedTo("alpha", ""))
	m, _ := d.r.model()
	_, late := pendingSubscription(t, m, DestStatefulSets)
	next, begin := d.r.begin(data.Target{Context: "beta", Namespace: "default"})
	d.r = next.(Root)
	if l, ok := d.r.cur.(Loader); !ok || l.main != nil {
		t.Fatal("the switch is still on the Loader")
	}
	before := f.cancels.Load()
	d.send(late)
	if f.cancels.Load() != before+1 {
		t.Fatal("a subscription delivered to the Loader is cancelled, not dropped")
	}
	d.run(begin)
	d.until("beta", connectedTo("beta", "default"))
}

func TestLeavingEndsBlockedChangeWaits(t *testing.T) {
	b := newFakeBrowser(nil, nil)
	m := nodeModel(t, b, 160, 48)
	m.activateDestination(DestWorkloadsOver)
	next, cmd := m.Update(tea.FocusMsg{})
	m = next.(Model)
	var waits []<-chan tea.Msg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		out := make(chan tea.Msg, 1)
		go func() { out <- c() }()
		select {
		case msg := <-out:
			if batch, ok := msg.(tea.BatchMsg); ok {
				queue = append(queue, batch...)
				continue
			}
			if msg == nil {
				continue
			}
			after, more := m.Update(msg)
			m = after.(Model)
			queue = append(queue, more)
		case <-time.After(50 * time.Millisecond):
			waits = append(waits, out)
		}
	}
	if len(waits) != len(workloadLeaves) {
		t.Fatalf("the overview waits on every kind, got %d", len(waits))
	}
	press(t, m, "2")
	deadline := time.After(2 * time.Second)
	for _, w := range waits {
		select {
		case <-w:
		case <-deadline:
			t.Fatal("a change wait outlived the subscription it belonged to")
		}
	}
}

type scriptBrowser struct {
	*data.Demo
	mu       sync.Mutex
	snap     data.ResourceCatalogSnapshot
	page     data.ResourcePage
	subErr   error
	block    bool
	refresh  []bool
	subs     int
	canceled atomic.Int32
	blocked  chan struct{}
}

func newScriptBrowser(avail map[string]data.ResourceAvailability) *scriptBrowser {
	return &scriptBrowser{Demo: data.NewDemo(), snap: newFakeBrowser(avail, nil).snap, page: data.ResourcePage{Total: 1, Coverage: data.Coverage{State: data.CoverageComplete}, Items: []data.ResourceRow{workloadRow("db", data.WorkloadSummary{Kind: data.WorkloadStatefulSet, State: data.WorkloadReady})}}, blocked: make(chan struct{}, 8)}
}

func (b *scriptBrowser) set(f func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f()
}

func (b *scriptBrowser) Catalog(ctx context.Context, refresh bool) data.ResourceCatalogSnapshot {
	b.mu.Lock()
	b.refresh = append(b.refresh, refresh)
	block, snap := b.block, b.snap
	b.mu.Unlock()
	if block {
		b.blocked <- struct{}{}
		<-ctx.Done()
		b.canceled.Add(1)
		return data.ResourceCatalogSnapshot{Session: snap.Session, Generation: snap.Generation, Coverage: data.Coverage{State: data.CoveragePartial, Reason: "discovery canceled"}}
	}
	return snap
}

func (b *scriptBrowser) Subscribe(context.Context, data.ResourceSubscriptionKey) (data.ResourceSubscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs++
	if b.subErr != nil {
		return nil, b.subErr
	}
	return &fakeHandle{page: b.page, changes: make(chan struct{}, 1), cancels: &atomic.Int32{}, refreshes: &atomic.Int32{}}, nil
}

func pressPump(t *testing.T, m Model, key string) Model {
	t.Helper()
	next, cmd := m.Update(keyPress(key))
	return pump(t, next.(Model), cmd)
}

func TestReloadRecoversCatalogAndSubscription(t *testing.T) {
	for _, tc := range []struct {
		name  string
		avail data.ResourceAvailability
		want  string
	}{
		{"denied", data.ResourceDenied, "sem permissão para descobrir StatefulSets"},
		{"partial", data.ResourcePartial, "descoberta parcial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newScriptBrowser(map[string]data.ResourceAvailability{"statefulsets": tc.avail})
			m := openLeaf(t, nodeModel(t, b, 160, 48), DestStatefulSets)
			if !strings.Contains(m.Render().Plain(), tc.want) || b.subs != 0 {
				t.Fatalf("%s starts blocked without a subscription", tc.name)
			}
			b.set(func() { b.snap = newFakeBrowser(nil, nil).snap })
			m = pressPump(t, m, "r")
			if len(b.refresh) != 2 || !b.refresh[1] {
				t.Fatalf("r asks for a fresh catalog, got %v", b.refresh)
			}
			if b.subs != 1 || !strings.Contains(m.Render().Plain(), "db") {
				t.Fatal("r reconciles the entry without a handle after the catalog recovers")
			}
		})
	}
	t.Run("subscribe error", func(t *testing.T) {
		b := newScriptBrowser(nil)
		b.subErr = errors.New("budget")
		m := openLeaf(t, nodeModel(t, b, 160, 48), DestStatefulSets)
		if !strings.Contains(m.Render().Plain(), "assinatura indisponível") {
			t.Fatal("a failed subscribe is shown")
		}
		b.set(func() { b.subErr = nil })
		m = pressPump(t, m, "r")
		if b.subs != 2 || !strings.Contains(m.Render().Plain(), "db") {
			t.Fatalf("r retries a failed subscribe, got %d", b.subs)
		}
	})
	t.Run("served then denied", func(t *testing.T) {
		b := newScriptBrowser(nil)
		m := openLeaf(t, nodeModel(t, b, 160, 48), DestStatefulSets)
		handle := m.res.subs[stsGVR].handle.(*fakeHandle)
		cancels := handle.cancels
		b.set(func() {
			b.snap = newFakeBrowser(map[string]data.ResourceAvailability{"statefulsets": data.ResourceDenied}, nil).snap
		})
		m = pressPump(t, m, "r")
		if cancels.Load() != 1 || m.res.subs[stsGVR].handle != nil || !strings.Contains(m.Render().Plain(), "sem permissão para descobrir") {
			t.Fatal("a resource that stops being served releases its handle")
		}
	})
}

func runCatalogBlocked(t *testing.T, m Model, b *scriptBrowser) (Model, <-chan tea.Msg) {
	t.Helper()
	m.activateDestination(DestStatefulSets)
	next, cmd := m.Update(tea.FocusMsg{})
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	select {
	case <-b.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("the catalog query never started")
	}
	return next.(Model), out
}

func catalogReply(t *testing.T, out <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-out:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("the catalog query outlived the route")
		return nil
	}
}

func TestLeavingCancelsPendingCatalog(t *testing.T) {
	b := newScriptBrowser(nil)
	b.block = true
	m, out := runCatalogBlocked(t, nodeModel(t, b, 160, 48), b)
	m = press(t, m, "2")
	late := catalogReply(t, out)
	next, _ := m.Update(late)
	m = next.(Model)
	if m.res.catalog != nil {
		t.Fatal("the reply of a cancelled query is discarded")
	}
	b.set(func() { b.block = false })
	m = openLeaf(t, m, DestStatefulSets)
	if len(b.refresh) != 2 || m.res.catalog == nil || m.res.subs[stsGVR].handle == nil {
		t.Fatalf("coming back starts a new query, got %v", b.refresh)
	}
}

func TestSessionSwitchCancelsPendingCatalog(t *testing.T) {
	b := newScriptBrowser(nil)
	b.block = true
	m, out := runCatalogBlocked(t, nodeModel(t, b, 160, 48), b)
	m.releaseResources()
	catalogReply(t, out)
	if b.canceled.Load() != 1 {
		t.Fatal("releasing the session cancels the catalog query")
	}
}

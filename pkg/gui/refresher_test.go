package gui

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jesseduffield/gocui"
)

type fakeUpdater struct {
	mu        sync.Mutex
	callbacks []func(*gocui.Gui) error
	calls     atomic.Int32
}

func (f *fakeUpdater) update(fn func(*gocui.Gui) error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callbacks = append(f.callbacks, fn)
}

func (f *fakeUpdater) runPending(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	callbacks := f.callbacks
	f.callbacks = nil
	f.mu.Unlock()
	for _, fn := range callbacks {
		if err := fn(nil); err != nil {
			t.Fatalf("callback error: %v", err)
		}
	}
}

func TestRefresherCoalescesWhileLoopIsBlocked(t *testing.T) {
	fake := &fakeUpdater{}
	r := newRefresher(fake.update)

	for i := 0; i < 100; i++ {
		r.tick()
	}
	if got := fake.calls.Load(); got != 1 {
		t.Fatalf("blocked loop must keep a single pending update, got %d", got)
	}

	fake.runPending(t)
	if !r.tick() {
		t.Fatal("tick must schedule again after the pending update ran")
	}
	if got := fake.calls.Load(); got != 2 {
		t.Fatalf("expected 2 updates, got %d", got)
	}
}

func TestRefresherPauseSkipsTicks(t *testing.T) {
	fake := &fakeUpdater{}
	r := newRefresher(fake.update)

	r.pause()
	for i := 0; i < 10; i++ {
		r.tick()
	}
	if got := fake.calls.Load(); got != 0 {
		t.Fatalf("paused refresher must not schedule updates, got %d", got)
	}

	r.resume()
	if !r.tick() {
		t.Fatal("resumed refresher must schedule updates")
	}
}

func TestRefresherStopsAndDoesNotLeakGoroutines(t *testing.T) {
	fake := &fakeUpdater{}
	r := newRefresher(fake.update)
	before := runtime.NumGoroutine()

	r.start(time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	if got := fake.calls.Load(); got != 1 {
		t.Fatalf("blocked loop must keep a single pending update, got %d", got)
	}
	if delta := runtime.NumGoroutine() - before; delta > 1 {
		t.Fatalf("expected at most the ticker goroutine, got %d extra goroutines", delta)
	}

	done := make(chan struct{})
	go func() {
		r.stop()
		r.stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("refresher did not stop")
	}
}

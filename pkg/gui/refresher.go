package gui

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/jesseduffield/gocui"
)

type updateFunc func(func(*gocui.Gui) error)

type refresher struct {
	update   updateFunc
	pending  atomic.Bool
	paused   atomic.Bool
	stopOnce sync.Once
	done     chan struct{}
	stopped  chan struct{}
}

func newRefresher(update updateFunc) *refresher {
	return &refresher{
		update:  update,
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

func (r *refresher) tick() bool {
	if r.paused.Load() {
		return false
	}
	if !r.pending.CompareAndSwap(false, true) {
		return false
	}
	r.update(func(*gocui.Gui) error {
		r.pending.Store(false)
		return nil
	})
	return true
}

func (r *refresher) pause() {
	r.paused.Store(true)
}

func (r *refresher) resume() {
	r.paused.Store(false)
}

func (r *refresher) start(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer close(r.stopped)
		defer ticker.Stop()
		for {
			select {
			case <-r.done:
				return
			case <-ticker.C:
				r.tick()
			}
		}
	}()
}

func (r *refresher) stop() {
	r.stopOnce.Do(func() {
		close(r.done)
	})
	<-r.stopped
}

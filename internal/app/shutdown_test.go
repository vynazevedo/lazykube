package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/goleak"

	"github.com/vynazevedo/lazykube/internal/data"
)

func quitAndShutdown(t *testing.T, r Root, m Model) {
	t.Helper()
	next, cmd := m.Update(keyPress("q"))
	r.cur = next
	quit := false
	for _, msg := range drainMsgs(t, cmd) {
		if _, ok := msg.(tea.QuitMsg); ok {
			quit = true
		}
	}
	if !quit {
		t.Fatal("q did not quit")
	}
	r.Shutdown()
}

func awaitClosed(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("%s survived quit and Root.Shutdown", what)
	}
}

func TestShutdownAfterQuitEndsResourceWaits(t *testing.T) {
	baseline := goleak.IgnoreCurrent()
	r := NewRoot(RootOptions{Options: Options{Source: data.NewDemo(), NoAnim: true}})
	m, ok := r.model()
	if !ok {
		t.Fatal("root has no model")
	}
	m.activateDestination(DestStatefulSets)
	next, cmd := m.Update(tea.FocusMsg{})
	m = drive(t, next.(Model), cmd)
	s := m.res.subs[stsGVR]
	if s == nil || s.handle == nil {
		t.Fatal("statefulsets subscription not open")
	}
	waited := make(chan struct{})
	go func() {
		waitResource(s.handle, s.done, resourceTag{})()
		close(waited)
	}()
	quitAndShutdown(t, r, m)
	awaitClosed(t, s.done, "subscription context")
	awaitClosed(t, waited, "waitResource")
	goleak.VerifyNone(t, baseline)
}

func TestLoaderShutdownCancelsModelSessionContext(t *testing.T) {
	baseline := goleak.IgnoreCurrent()
	l := NewLoader(LoaderOptions{Options: Options{NoAnim: true}})
	next, _ := l.start(data.NewDemo())
	m, ok := next.(Model)
	if !ok {
		t.Fatalf("loader start returned %T", next)
	}
	l.Shutdown()
	select {
	case <-m.life.Done():
	default:
		t.Fatal("loader shutdown did not cancel the model session context")
	}
	goleak.VerifyNone(t, baseline)
}

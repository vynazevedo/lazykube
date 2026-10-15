package gui

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/TNK-Studio/lazykube/pkg/config"
	"github.com/jesseduffield/gocui"
)

func waitFor(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !condition() {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for %s", description)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestHeadlessGuiLifecycle(t *testing.T) {
	var renders atomic.Int32
	list := &View{
		Name: "list",
		DimensionFunc: func(gui *Gui, _ *View) (int, int, int, int) {
			width, height := gui.Size()
			return 0, 0, width - 1, height - 1
		},
		OnRender: func(_ *Gui, view *View) error {
			renders.Add(1)
			view.ReRender()
			return view.SetViewContent("first\nsecond\nthird")
		},
	}

	gui := NewHeadlessGui(config.GuiConfig{FgColor: config.ColorWhite}, 40, 10, list)
	defer gui.Close()
	gui.RefreshInterval = 10 * time.Millisecond
	list.ReRender()

	done := make(chan struct{})
	go func() {
		defer close(done)
		gui.Run()
	}()

	waitFor(t, "periodic renders from refresh ticker", func() bool { return renders.Load() >= 5 })

	selected := make(chan string, 1)
	gui.Update(func(*gocui.Gui) error {
		view, err := gui.GetView("list")
		if err != nil {
			return err
		}
		view.SetOrigin(0, 0)
		view.SetCursor(0, 0)
		view.MoveCursorVertically(1)
		_, cy := view.Cursor()
		line, err := view.Line(cy)
		if err != nil {
			return err
		}
		selected <- line
		return nil
	})

	select {
	case got := <-selected:
		if got != "second" {
			t.Fatalf("selected line = %q, want %q", got, "second")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("update was not processed")
	}

	gui.Update(func(*gocui.Gui) error { return gocui.ErrQuit })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("gui did not stop")
	}
}

package main

import (
	"fmt"
	"io"
	"runtime/debug"
	"sync"

	tea "charm.land/bubbletea/v2"
)

type crash struct {
	value string
	stack []byte
}

type crashBox struct {
	mu    sync.Mutex
	first *crash
	quit  func()
}

func (b *crashBox) record(r any) {
	stack := debug.Stack()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.first == nil {
		b.first = &crash{value: fmt.Sprint(r), stack: stack}
	}
}

func (b *crashBox) report() (crash, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.first == nil {
		return crash{}, false
	}
	return *b.first, true
}

func (b *crashBox) stop() {
	b.mu.Lock()
	quit := b.quit
	b.mu.Unlock()
	if quit != nil {
		go quit()
	}
}

func (b *crashBox) write(w io.Writer) bool {
	c, ok := b.report()
	if ok {
		_, _ = fmt.Fprintf(w, "lazykube: falha interna: %s\n\n%s", c.value, c.stack)
	}
	return ok
}

type guarded struct {
	inner tea.Model
	box   *crashBox
}

func (g guarded) Init() (cmd tea.Cmd) {
	defer func() {
		if r := recover(); r != nil {
			g.box.record(r)
			cmd = tea.Quit
		}
	}()
	return g.wrap(g.inner.Init())
}

func (g guarded) Update(msg tea.Msg) (next tea.Model, cmd tea.Cmd) {
	defer func() {
		if r := recover(); r != nil {
			g.box.record(r)
			next, cmd = g, tea.Quit
		}
	}()
	m, c := g.inner.Update(msg)
	g.inner = m
	return g, g.wrap(c)
}

func (g guarded) View() (v tea.View) {
	defer func() {
		if r := recover(); r != nil {
			g.box.record(r)
			g.box.stop()
			v = tea.NewView("")
		}
	}()
	return g.inner.View()
}

func (g guarded) wrap(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				g.box.record(r)
				msg = tea.QuitMsg{}
			}
		}()
		out := cmd()
		if batch, ok := out.(tea.BatchMsg); ok {
			wrapped := make(tea.BatchMsg, len(batch))
			for i, c := range batch {
				wrapped[i] = g.wrap(c)
			}
			return wrapped
		}
		return out
	}
}

func unwrapGuarded(m tea.Model) tea.Model {
	if g, ok := m.(guarded); ok {
		return g.inner
	}
	return m
}

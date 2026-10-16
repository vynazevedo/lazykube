package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

type dataMsg struct{}

type frameMsg struct {
	at  time.Time
	gen int
}

func frameCmd(fps, gen int) tea.Cmd {
	return tea.Tick(time.Second/time.Duration(max(1, fps)), func(t time.Time) tea.Msg {
		return frameMsg{at: t, gen: gen}
	})
}

func waitChange(ch <-chan struct{}) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		if _, ok := <-ch; !ok {
			return nil
		}
		return dataMsg{}
	}
}

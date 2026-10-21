package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

type dataMsg struct{ session uint64 }

type frameMsg struct {
	at      time.Time
	gen     int
	session uint64
}

func frameCmd(fps, gen int, session uint64) tea.Cmd {
	return tea.Tick(time.Second/time.Duration(max(1, fps)), func(t time.Time) tea.Msg {
		return frameMsg{at: t, gen: gen, session: session}
	})
}

func waitChange(ch <-chan struct{}, session uint64) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		if _, ok := <-ch; !ok {
			return nil
		}
		return dataMsg{session: session}
	}
}

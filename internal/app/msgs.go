package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/TNK-Studio/lazykube/internal/anim"
)

type frameMsg struct {
	at  time.Time
	gen int
}

type dataTickMsg struct {
	gen int
}

func frameCmd(fps, gen int) tea.Cmd {
	return tea.Tick(time.Second/time.Duration(fps), func(t time.Time) tea.Msg {
		return frameMsg{at: t, gen: gen}
	})
}

func dataTickCmd(gen int) tea.Cmd {
	return tea.Tick(anim.TickInterval, func(time.Time) tea.Msg {
		return dataTickMsg{gen: gen}
	})
}

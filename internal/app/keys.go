package app

import "charm.land/bubbles/v2/key"

type KeyMap struct {
	Up          key.Binding
	Down        key.Binding
	NextPanel   key.Binding
	PrevPanel   key.Binding
	Filter      key.Binding
	Enter       key.Binding
	Esc         key.Binding
	TabLog      key.Binding
	TabConfig   key.Binding
	TabTop      key.Binding
	TabDescribe key.Binding
	TabEvents   key.Binding
	TabPrev     key.Binding
	TabNext     key.Binding
	Container   key.Binding
	Previous    key.Binding
	SearchNext  key.Binding
	SearchPrev  key.Binding
	EventScope  key.Binding
	Palette     key.Binding
	ToggleLeft  key.Binding
	Pause       key.Binding
	Help        key.Binding
	Quit        key.Binding
	PageUp      key.Binding
	PageDown    key.Binding
	Home        key.Binding
	End         key.Binding
	Follow      key.Binding
	Wrap        key.Binding
	Top         key.Binding
	Bottom      key.Binding
}

func DefaultKeyMap() KeyMap {
	b := func(help, desc string, keys ...string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, desc))
	}
	return KeyMap{
		Up:          b("↑/k", "up", "up", "k"),
		Down:        b("↓/j", "down", "down", "j"),
		NextPanel:   b("tab", "next panel", "tab"),
		PrevPanel:   b("shift+tab", "previous panel", "shift+tab"),
		Filter:      b("/", "filter", "/"),
		Enter:       b("enter", "confirm", "enter"),
		Esc:         b("esc", "back", "esc"),
		TabLog:      b("l", "logs", "l"),
		TabConfig:   b("c", "config", "c"),
		TabTop:      b("t", "top", "t"),
		TabDescribe: b("d", "describe", "d"),
		TabEvents:   b("e", "events", "e"),
		TabPrev:     b("[", "previous tab", "["),
		TabNext:     b("]", "next tab", "]"),
		Container:   b("C", "log container", "C"),
		Previous:    b("P", "previous logs", "P"),
		SearchNext:  b("n", "next match", "n"),
		SearchPrev:  b("N", "previous match", "N"),
		EventScope:  b("E", "events scope", "E"),
		Palette:     b("p", "palette", "p"),
		ToggleLeft:  b("1", "left column", "1"),
		Pause:       b("ctrl+p", "pause", "ctrl+p"),
		Help:        b("?", "help", "?"),
		Quit:        b("q", "quit", "q", "ctrl+c"),
		PageUp:      b("pgup", "page up", "pgup"),
		PageDown:    b("pgdn", "page down", "pgdown"),
		Home:        b("home", "top", "home"),
		End:         b("end", "bottom", "end"),
		Follow:      b("f", "follow", "f"),
		Wrap:        b("w", "wrap", "w"),
		Top:         b("g", "top", "g"),
		Bottom:      b("G", "bottom", "G"),
	}
}

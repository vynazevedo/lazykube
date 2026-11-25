package app

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/netpath"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type urlPicker struct {
	open   bool
	title  string
	items  []netpath.Address
	cursor int
}

func (m Model) openURLs() (Model, tea.Cmd) {
	list := m.accessAddresses()
	if len(list) == 0 {
		return m, m.showNotice("nenhuma URL para escolher")
	}
	m.endEditing()
	t := m.acc.target
	m.urls = urlPicker{open: true, title: t.namespace + "/" + t.name, items: slices.Clone(list)}
	return m, nil
}

func (m Model) urlPickerState() ui.URLPickerState {
	return ui.URLPickerState{Title: m.urls.title, Items: m.urls.items, Cursor: m.urls.cursor}
}

func (m Model) handleURLKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := m.keys
	last := len(m.urls.items) - 1
	switch {
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case key.Matches(msg, k.Esc):
		m.urls = urlPicker{}
	case key.Matches(msg, k.Up):
		m.urls.cursor = max(0, m.urls.cursor-1)
	case key.Matches(msg, k.Down), key.Matches(msg, k.NextURL):
		m.urls.cursor = min(last, m.urls.cursor+1)
	case key.Matches(msg, k.Home):
		m.urls.cursor = 0
	case key.Matches(msg, k.End):
		m.urls.cursor = last
	case key.Matches(msg, k.Enter):
		return m.copyURL()
	}
	return m, nil
}

func (m Model) copyURL() (Model, tea.Cmd) {
	a, ok := m.urlPickerState().Selected()
	if !ok {
		return m, nil
	}
	if a.URL.IsZero() {
		return m, m.showNotice("sem URL copiável: " + a.Refused)
	}
	if len(ui.URLPickerLines(m.w, m.h, a.URL)) == 0 {
		return m, m.showNotice("a URL inteira não cabe neste terminal")
	}
	m.urls = urlPicker{}
	return m.copyText(yank{label: a.URL.String(), text: a.URL.String()})
}

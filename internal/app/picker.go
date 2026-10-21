package app

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type pickKind int

const (
	pickContext pickKind = iota + 1
	pickNamespace
)

type pickMsg struct{ kind pickKind }

func requestPick(kind pickKind) tea.Cmd {
	return func() tea.Msg { return pickMsg{kind: kind} }
}

type choice int

const (
	choiceRetry choice = iota + 1
	choiceBack
)

type choiceMsg struct {
	choice  choice
	attempt uint64
}

const (
	noteCatalog      = "a troca reconecta com o namespace mostrado; nada é escrito no kubeconfig"
	noteListed       = "lista completa de namespaces desta conexão"
	notePartial      = "cobertura parcial: só os namespaces observados nesta conexão; use outro… para digitar"
	errNamespace     = "nome de namespace inválido: use letras minúsculas, números e hífen (DNS-1123)"
	errCatalog       = "catálogo de contextos indisponível"
	labelManual      = "outro…"
	defaultNamespace = "default"
)

type pickEntry struct {
	item   ui.PickerItem
	target data.Target
	manual bool
}

type picker struct {
	kind      pickKind
	title     string
	context   string
	entries   []pickEntry
	cursor    int
	filter    textinput.Model
	filtering bool
	input     textinput.Model
	inputting bool
	note      string
	err       string
}

type pickResult struct {
	target data.Target
	ok     bool
	close  bool
}

func newPicker(kind pickKind, title string, entries []pickEntry) *picker {
	filter := textinput.New()
	filter.Prompt, filter.Placeholder = "", ""
	input := textinput.New()
	input.Prompt, input.Placeholder, input.CharLimit = "", "", 63
	p := &picker{kind: kind, title: title, entries: entries, filter: filter, input: input}
	for i, e := range entries {
		if e.item.Current {
			p.cursor = i
		}
	}
	return p
}

func targetLabel(t data.Target, resolved string) string {
	name := t.Context
	if name == "" {
		name = resolved
	}
	if name == "" {
		name = "contexto atual"
	}
	return name + " · " + t.NamespaceLabel()
}

func firstNamespace(k data.KubeContext, memory map[string]string) string {
	if ns, ok := memory[k.Name]; ok {
		return ns
	}
	if k.Namespace != "" {
		return k.Namespace
	}
	return defaultNamespace
}

func contextPicker(cat data.ContextCatalog, err error, active *data.Target, memory map[string]string) *picker {
	title := "Contextos do kubeconfig"
	if cat.Synthetic {
		title = "[DEMO] Contextos sintéticos"
	}
	var entries []pickEntry
	for _, k := range cat.Contexts {
		ns := firstNamespace(k, memory)
		current := active != nil && active.Context == k.Name
		if current {
			ns = active.Namespace
		}
		t := data.Target{Context: k.Name, Namespace: ns}
		label := k.Name
		if cat.Synthetic {
			label = "[DEMO] " + label
		}
		entries = append(entries, pickEntry{target: t, item: ui.PickerItem{
			Label:   label,
			Detail:  fmt.Sprintf("cluster %s · user %s · namespace %s", k.Cluster, k.User, t.NamespaceLabel()),
			Current: current,
			Problem: k.Problem,
		}})
	}
	p := newPicker(pickContext, title, entries)
	p.note = noteCatalog
	if err != nil {
		p.err = errCatalog + ": " + err.Error()
	}
	return p
}

func namespacePicker(active data.Target, snap data.Snapshot) *picker {
	entries := []pickEntry{{
		target: data.Target{Context: active.Context},
		item:   ui.PickerItem{Label: data.AllNamespacesLabel, Detail: "ampliação explícita: lê todos os namespaces", Current: active.Namespace == ""},
	}}
	for _, ns := range snap.Namespaces {
		entries = append(entries, pickEntry{
			target: data.Target{Context: active.Context, Namespace: ns.Name},
			item:   ui.PickerItem{Label: ns.Name, Detail: fmt.Sprintf("%d pods observados", ns.Pods), Current: active.Namespace == ns.Name},
		})
	}
	entries = append(entries, pickEntry{manual: true, item: ui.PickerItem{Label: labelManual, Detail: "digitar um namespace; o servidor decide o acesso"}})
	p := newPicker(pickNamespace, "Namespace em "+active.Context, entries)
	p.context = active.Context
	p.note = notePartial
	if snap.Cluster.Listed {
		p.note = noteListed
	}
	return p
}

func (p *picker) visible() []pickEntry {
	q := strings.ToLower(strings.TrimSpace(p.filter.Value()))
	if q == "" {
		return p.entries
	}
	var out []pickEntry
	for _, e := range p.entries {
		if e.manual || strings.Contains(strings.ToLower(e.item.Label+" "+e.item.Detail), q) {
			out = append(out, e)
		}
	}
	return out
}

func (p *picker) state() ui.PickerState {
	vis := p.visible()
	items := make([]ui.PickerItem, len(vis))
	for i, e := range vis {
		items[i] = e.item
	}
	return ui.PickerState{
		Title:     p.title,
		Items:     items,
		Cursor:    p.cursor,
		Filter:    p.filter.Value(),
		Filtering: p.filtering,
		Input:     p.input.Value(),
		Inputting: p.inputting,
		Note:      p.note,
		Error:     p.err,
	}
}

func (p *picker) move(delta int) {
	p.cursor = max(0, min(len(p.visible())-1, p.cursor+delta))
}

func (p *picker) confirm() (pickResult, tea.Cmd) {
	vis := p.visible()
	if p.cursor < 0 || p.cursor >= len(vis) {
		return pickResult{}, nil
	}
	e := vis[p.cursor]
	if e.manual {
		p.inputting, p.err = true, ""
		return pickResult{}, p.input.Focus()
	}
	return pickResult{target: e.target, ok: true}, nil
}

func (p *picker) key(msg tea.KeyPressMsg) (pickResult, tea.Cmd) {
	k := msg.String()
	switch {
	case p.inputting:
		switch k {
		case "esc":
			p.inputting, p.err = false, ""
			p.input.Blur()
			return pickResult{}, nil
		case "enter":
			ns := strings.TrimSpace(p.input.Value())
			if !data.ValidNamespace(ns) {
				p.err = errNamespace
				return pickResult{}, nil
			}
			return pickResult{target: data.Target{Context: p.context, Namespace: ns}, ok: true}, nil
		}
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		p.err = ""
		return pickResult{}, cmd
	case p.filtering:
		switch k {
		case "esc":
			p.filtering = false
			p.filter.SetValue("")
			p.filter.Blur()
		case "enter":
			p.filtering = false
			p.filter.Blur()
		case "up", "down":
			p.move(map[string]int{"up": -1, "down": 1}[k])
		default:
			var cmd tea.Cmd
			p.filter, cmd = p.filter.Update(msg)
			p.cursor = 0
			return pickResult{}, cmd
		}
		p.move(0)
		return pickResult{}, nil
	}
	switch k {
	case "esc":
		return pickResult{close: true}, nil
	case "/":
		p.filtering = true
		return pickResult{}, p.filter.Focus()
	case "up", "k":
		p.move(-1)
	case "down", "j":
		p.move(1)
	case "home", "g":
		p.cursor = 0
	case "end", "G":
		p.move(len(p.entries))
	case "enter":
		return p.confirm()
	}
	return pickResult{}, nil
}

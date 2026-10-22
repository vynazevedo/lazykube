package app

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/ui"
)

type treeState struct {
	visible  bool
	focused  bool
	expanded []DestinationID
	cursor   DestinationID
	offset   int
	note     string
}

type treeRow struct {
	dest  Destination
	depth int
}

func (t treeState) isExpanded(id DestinationID) bool {
	return slices.Contains(t.expanded, id)
}

func (t treeState) withExpanded(id DestinationID, open bool) treeState {
	out := slices.DeleteFunc(slices.Clone(t.expanded), func(e DestinationID) bool { return e == id })
	if open {
		out = append(out, id)
	}
	t.expanded = out
	return t
}

func (t treeState) rows() []treeRow {
	var out []treeRow
	var walk func(parent DestinationID, depth int)
	walk = func(parent DestinationID, depth int) {
		for _, d := range registry.Children(parent) {
			out = append(out, treeRow{dest: d, depth: depth})
			if d.Kind == DestCategory && t.isExpanded(d.ID) {
				walk(d.ID, depth+1)
			}
		}
	}
	walk("", 0)
	return out
}

func (m Model) activeDestination() DestinationID {
	return m.nav.trail()[m.nav.crumbStart()].Dest
}

func ancestors(id DestinationID) []DestinationID {
	var out []DestinationID
	for d, ok := registry.get(id); ok && d.Parent != ""; d, ok = registry.get(d.Parent) {
		out = append(out, d.Parent)
	}
	return out
}

func (m Model) treeCursorIndex(rows []treeRow) int {
	for i, r := range rows {
		if r.dest.ID == m.tree.cursor {
			return i
		}
	}
	for _, a := range ancestors(m.tree.cursor) {
		for i, r := range rows {
			if r.dest.ID == a {
				return i
			}
		}
	}
	return 0
}

func (m Model) sidebarState() ui.SidebarState {
	rows := m.tree.rows()
	active := m.activeDestination()
	out := ui.SidebarState{Focused: m.tree.focused, Overlay: m.layout.TreeOverlay, Note: m.tree.note}
	for _, r := range rows {
		d := r.dest
		out.Rows = append(out.Rows, ui.SidebarRow{
			Depth:    r.depth,
			Label:    d.Label,
			Category: d.Kind == DestCategory,
			Expanded: m.tree.isExpanded(d.ID),
			Active:   d.ID == active,
			Planned:  d.Availability == AvailPlanned,
			Shortcut: d.Shortcut,
		})
	}
	out.Cursor = m.treeCursorIndex(rows)
	out.Offset = m.treeOffset(len(rows), out.Cursor)
	return out
}

func (m Model) treeOffset(total, cursor int) int {
	visible := ui.SidebarRowsVisible(m.layout.Tree.H)
	off := max(0, min(m.tree.offset, total-visible))
	switch {
	case cursor < off:
		off = cursor
	case visible > 0 && cursor >= off+visible:
		off = cursor - visible + 1
	}
	return max(0, off)
}

func (m *Model) showTree() {
	active := m.activeDestination()
	for _, a := range ancestors(active) {
		m.tree = m.tree.withExpanded(a, true)
	}
	m.tree.visible, m.tree.focused, m.tree.cursor, m.tree.note = true, true, active, ""
	m.endEditing()
	m.relayout()
	m.refresh()
}

func (m *Model) hideTree() {
	m.tree.visible, m.tree.focused, m.tree.note = false, false, ""
	m.relayout()
	m.refresh()
}

func (m *Model) blurTree() {
	m.tree.focused, m.tree.note = false, ""
	if m.layout.TreeOverlay {
		m.hideTree()
	}
}

func (m *Model) toggleTreeKey() {
	switch {
	case !m.tree.visible:
		m.showTree()
	case !m.tree.focused:
		m.tree.focused = true
	default:
		m.hideTree()
	}
}

func (m *Model) moveTree(delta int) {
	rows := m.tree.rows()
	if len(rows) == 0 {
		return
	}
	i := max(0, min(len(rows)-1, m.treeCursorIndex(rows)+delta))
	m.tree.cursor, m.tree.note = rows[i].dest.ID, ""
	m.tree.offset = m.treeOffset(len(rows), i)
}

func (m *Model) treeToggle(id DestinationID, open bool) {
	m.tree = m.tree.withExpanded(id, open)
	m.tree.note = ""
}

func (m *Model) treeOpen(d Destination) {
	switch {
	case d.Kind == DestCategory:
		m.treeToggle(d.ID, !m.tree.isExpanded(d.ID))
	case d.ready():
		m.tree.note = ""
		m.activateDestination(d.ID)
		m.tree.cursor = d.ID
		m.blurTree()
	default:
		_, reason := m.activateDestination(d.ID)
		m.tree.note = d.Label + ": " + reason
	}
}

func (m Model) handleTreeKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := m.keys
	rows := m.tree.rows()
	cur := rows[m.treeCursorIndex(rows)].dest
	page := max(1, ui.SidebarRowsVisible(m.layout.Tree.H)-1)
	switch {
	case key.Matches(msg, k.Up):
		m.moveTree(-1)
	case key.Matches(msg, k.Down):
		m.moveTree(1)
	case key.Matches(msg, k.PageUp):
		m.moveTree(-page)
	case key.Matches(msg, k.PageDown):
		m.moveTree(page)
	case key.Matches(msg, k.Home):
		m.moveTree(-len(rows))
	case key.Matches(msg, k.End):
		m.moveTree(len(rows))
	case key.Matches(msg, k.MenuNext):
		switch {
		case cur.Kind == DestCategory && !m.tree.isExpanded(cur.ID):
			m.treeToggle(cur.ID, true)
		case cur.Kind == DestCategory:
			m.moveTree(1)
		}
	case key.Matches(msg, k.MenuPrev):
		switch {
		case cur.Kind == DestCategory && m.tree.isExpanded(cur.ID):
			m.treeToggle(cur.ID, false)
		case cur.Parent != "":
			m.tree.cursor, m.tree.note = cur.Parent, ""
		}
	case key.Matches(msg, k.Enter):
		m.treeOpen(cur)
	case key.Matches(msg, k.Tree):
		m.toggleTreeKey()
	case key.Matches(msg, k.Esc), key.Matches(msg, k.NextPanel), key.Matches(msg, k.PrevPanel):
		m.blurTree()
	case key.Matches(msg, k.Menu):
		m.blurTree()
		m.openMenu()
	case key.Matches(msg, k.Help):
		m.helpOpen = true
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case m.shortcutDestination(msg) != "":
		m.blurTree()
		m.activateDestination(m.shortcutDestination(msg))
	}
	return m, nil
}

func (m Model) handleTreeClick(ms tea.Mouse) Model {
	st := m.sidebarState()
	idx, ok := ui.SidebarRowAt(st, m.layout.Tree.H, ms.Y-m.layout.Tree.Y)
	if !ok {
		m.tree.focused = true
		return m
	}
	rows := m.tree.rows()
	d := rows[idx].dest
	m.tree.focused, m.tree.cursor = true, d.ID
	if d.Kind == DestCategory && ui.SidebarToggleAt(st.Rows[idx], ms.X-m.layout.Tree.X) {
		m.treeToggle(d.ID, !m.tree.isExpanded(d.ID))
		return m
	}
	m.treeOpen(d)
	return m
}

func (m Model) handleTreeWheel(ms tea.Mouse) Model {
	rows := m.tree.rows()
	visible := ui.SidebarRowsVisible(m.layout.Tree.H)
	delta := map[tea.MouseButton]int{tea.MouseWheelUp: -1, tea.MouseWheelDown: 1}[ms.Button]
	m.tree.offset = max(0, min(len(rows)-visible, m.sidebarState().Offset+delta))
	return m
}

func (m Model) treeMouse(ms tea.Mouse) (Model, bool) {
	if !m.tree.visible || ms.Button != tea.MouseLeft || m.helpOpen || m.menu.open {
		return m, false
	}
	switch {
	case m.layout.Hit(ms.X, ms.Y) == AreaTree:
		return m.handleTreeClick(ms), true
	case m.layout.TreeOverlay:
		m.hideTree()
		return m, true
	case m.tree.focused:
		m.tree.focused, m.tree.note = false, ""
	}
	return m, false
}

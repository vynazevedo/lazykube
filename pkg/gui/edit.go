package gui

import "github.com/jesseduffield/gocui"

func NewViewEditor(gui *Gui, view *View) gocui.Editor {
	return gocui.EditorFunc(ViewEditorFunc(gui, view))
}

func ViewEditorFunc(gui *Gui, view *View) func(v *gocui.View, key gocui.Key, ch rune, mod gocui.Modifier) bool {
	return func(v *gocui.View, key gocui.Key, ch rune, mod gocui.Modifier) bool {
		matched := gocui.DefaultEditor.Edit(v, key, ch, mod)
		if matched && view.OnEditedChange != nil {
			view.OnEditedChange(gui, view, key, ch, mod)
		}
		return matched
	}
}

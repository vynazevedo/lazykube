package app

import (
	"fmt"
	"testing"
)

func TestShellAndPodDetailGoldens(t *testing.T) {
	for _, size := range [][2]int{{130, 40}, {160, 48}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			base := demoModel(t, w, h)
			if !base.layout.ShowLeft {
				base = press(t, base, "1")
			}
			menu := press(t, navNamespace(t, base), "m")
			c := menu.Render()
			golden(t, fmt.Sprintf("shell_menu_%dx%d.txt", w, h), c.Plain())
			golden(t, fmt.Sprintf("shell_menu_%dx%d.ansi", w, h), c.String())

			detail := press(t, demoModel(t, w, h), "enter")
			c = detail.Render()
			golden(t, fmt.Sprintf("pod_detail_%dx%d.txt", w, h), c.Plain())
			golden(t, fmt.Sprintf("pod_detail_%dx%d.ansi", w, h), c.String())
			golden(t, fmt.Sprintf("pod_detail_log_%dx%d.txt", w, h), press(t, detail, "l").Render().Plain())
		})
	}
	src := newNavSource()
	m := press(t, navStart(t, src), "enter")
	src.gone = m.nav.Active().Pod
	next, _ := m.Update(dataMsg{})
	golden(t, "pod_missing_160x48.txt", next.(Model).Render().Plain())
}

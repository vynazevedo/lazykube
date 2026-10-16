package app

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type sequenceStep struct {
	name    string
	overlay bool
	msg     func(m Model) tea.Msg
}

var sequenceRestores = map[string]string{"namespace-clear": "namespace-down"}

func sequenceKey(k string) func(Model) tea.Msg {
	return func(Model) tea.Msg { return keyPress(k) }
}

func sequenceScript(w, h, ow, oh int) []sequenceStep {
	podRow := func(m Model, row int) (int, int) {
		return m.layout.Pods.X + 5, m.layout.Pods.Y + 1 + ui.PodsRowsTop + row
	}
	return []sequenceStep{
		{"initial", false, nil},
		{"down", false, sequenceKey("down")},
		{"down-again", false, sequenceKey("j")},
		{"tab-focus", false, sequenceKey("tab")},
		{"tab-log", false, sequenceKey("l")},
		{"tab-next", false, sequenceKey("]")},
		{"tab-security", false, sequenceKey("S")},
		{"security-namespace", false, sequenceKey("S")},
		{"tab-events", false, sequenceKey("e")},
		{"events-scope", false, sequenceKey("E")},
		{"tab-describe", false, sequenceKey("d")},
		{"page-down", false, sequenceKey("pgdn")},
		{"filter-open", false, sequenceKey("/")},
		{"filter-b", false, sequenceKey("b")},
		{"filter-i", false, sequenceKey("i")},
		{"filter-confirm", false, sequenceKey("enter")},
		{"filter-clear", false, sequenceKey("esc")},
		{"click-pod", false, func(m Model) tea.Msg {
			x, y := podRow(m, 3)
			return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
		}},
		{"wheel-pod", false, func(m Model) tea.Msg {
			x, y := podRow(m, 3)
			return tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}
		}},
		{"hover-pod", false, func(m Model) tea.Msg {
			x, y := podRow(m, 0)
			return tea.MouseMotionMsg{X: x, Y: y}
		}},
		{"click-tab", false, func(m Model) tea.Msg {
			span := ui.TabSpans()[0]
			return tea.MouseClickMsg{X: m.layout.Details.X + 2 + span.X + 1, Y: m.layout.Details.Y + 1, Button: tea.MouseLeft}
		}},
		{"toggle-left", false, sequenceKey("1")},
		{"toggle-left-again", false, sequenceKey("1")},
		{"palette", false, sequenceKey("p")},
		{"data", false, func(Model) tea.Msg { return dataMsg{} }},
		{"resize-other", false, func(Model) tea.Msg { return tea.WindowSizeMsg{Width: ow, Height: oh} }},
		{"resize-back", false, func(Model) tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} }},
		{"help-open", true, sequenceKey("?")},
		{"help-close", false, sequenceKey("esc")},
		{"show-left", false, func(m Model) tea.Msg {
			if m.layout.ShowLeft {
				return nil
			}
			return keyPress("1")
		}},
		{"focus-namespaces", false, func(m Model) tea.Msg {
			return tea.MouseClickMsg{X: m.layout.Namespaces.X + 3, Y: m.layout.Namespaces.Y + 2, Button: tea.MouseLeft}
		}},
		{"namespace-down", false, sequenceKey("down")},
		{"namespace-enter", false, sequenceKey("enter")},
		{"namespace-clear", false, sequenceKey("esc")},
	}
}

func regionCrop(c *draw.Canvas, r Rect) string {
	var plain, cells strings.Builder
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			cell := c.At(x, y)
			fmt.Fprintf(&cells, "%v|", cell)
			if !cell.Cont {
				plain.WriteRune(cell.Rune)
				plain.WriteString(cell.Extra)
			}
		}
		cells.WriteByte('\n')
		plain.WriteByte('\n')
	}
	return fmt.Sprintf("cells=%x\n%s", sha256.Sum256([]byte(cells.String())), plain.String())
}

func dashboardBodyTranscript(t *testing.T, w, h, ow, oh int) (string, map[string]string) {
	t.Helper()
	m := demoModel(t, w, h)
	crops := map[string]string{}
	var b strings.Builder
	for _, step := range sequenceScript(w, h, ow, oh) {
		if step.msg != nil {
			if msg := step.msg(m); msg != nil {
				next, _ := m.Update(msg)
				m = next.(Model)
			}
		}
		if step.overlay {
			fmt.Fprintf(&b, "== %s overlay\n", step.name)
			continue
		}
		c := m.Render()
		var entries []string
		for _, area := range []struct {
			name string
			r    Rect
		}{{"node", m.layout.Node}, {"pods", m.layout.Pods}, {"heatmap", m.layout.Heatmap}, {"details", m.layout.Details}} {
			if area.r.Empty() {
				entries = append(entries, area.name+" hidden\n")
				continue
			}
			entries = append(entries, fmt.Sprintf("%s %dx%d %s", area.name, area.r.W, area.r.H, regionCrop(c, area.r)))
		}
		crops[step.name] = strings.Join(entries, "")
		if from, ok := sequenceRestores[step.name]; ok {
			fmt.Fprintf(&b, "== %s restores %s\n", step.name, from)
			continue
		}
		for _, e := range entries {
			fmt.Fprintf(&b, "== %s %s", step.name, e)
		}
	}
	return b.String(), crops
}

func TestDashboardInteractionSequence(t *testing.T) {
	for _, size := range []struct{ w, h, ow, oh int }{{130, 40, 160, 48}, {160, 48, 130, 40}} {
		name := fmt.Sprintf("dashboard_sequence_%dx%d.txt.golden", size.w, size.h-1)
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			got, crops := dashboardBodyTranscript(t, size.w, size.h, size.ow, size.oh)
			for step, from := range sequenceRestores {
				if crops[step] == "" || crops[step] != crops[from] {
					t.Fatalf("%s must restore the body shown at %s", step, from)
				}
			}
			if *update {
				if err := os.WriteFile(filepath.Join("testdata", name), []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			if got == string(want) {
				return
			}
			gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
			for i := range min(len(gl), len(wl)) {
				if gl[i] != wl[i] {
					t.Fatalf("dashboard interaction sequence differs at line %d:\n got %q\nwant %q", i+1, gl[i], wl[i])
				}
			}
			t.Fatalf("dashboard body transcript length differs: got %d lines want %d", len(gl), len(wl))
		})
	}
}

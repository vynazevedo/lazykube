package ui

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

func nodePanelRows(st NodeState, w int) []string {
	ctx := Ctx{Pal: theme.DefaultPalette(), Clock: anim.Fixed(0, 0, 30)}
	canvas := draw.NewCanvas(w, 16, theme.BgApp)
	NodePanel(canvas.Region(), st, ctx)
	return strings.Split(canvas.Plain(), "\n")
}

func cpuRow(t *testing.T, rows []string) int {
	t.Helper()
	for i, r := range rows {
		if strings.Contains(r, "cpu ") {
			return i
		}
	}
	t.Fatal("node panel has no cpu line")
	return 0
}

func TestNodePanelUnknownCPU(t *testing.T) {
	node := data.Node{Name: "node-a", Ready: true, MemUsed: "1Gi", MemTotal: "4Gi", Coverage: data.Coverage{State: data.CoverageComplete}}
	for _, w := range []int{36, 40, 54} {
		known := nodePanelRows(NodeState{Node: node, CPU: 25, CPUKnown: true, CPUFrac: 0.25}, w)
		if i := cpuRow(t, known); !strings.Contains(known[i], "cpu 25%") || !strings.Contains(known[i+1], "█") {
			t.Fatalf("width %d: measured CPU keeps value and bar", w)
		}
		for _, frac := range []float64{0, 0.4} {
			rows := nodePanelRows(NodeState{Node: node, CPU: 0, CPUFrac: frac}, w)
			i := cpuRow(t, rows)
			if strings.Contains(rows[i], "0%") || !strings.Contains(rows[i], "cpu indisp") && !strings.Contains(rows[i], "cpu n/a") {
				t.Fatalf("width %d: unknown CPU must read as unavailable, got %q", w, rows[i])
			}
			if strings.Contains(rows[i+1], "█") {
				t.Fatalf("width %d: unknown CPU must not draw a bar that reads as a value", w)
			}
		}
	}
}

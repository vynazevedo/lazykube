package ui

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
)

func TestBugbashUnknownNodeReadiness(t *testing.T) {
	c := canvas(37, 16, func(r draw.Region) { NodePanel(r, NodeState{Node: data.Node{Name: "node"}}, fixedCtx()) })
	if text := c.Plain(); !strings.Contains(text, "Unknown") || strings.Contains(text, "NotReady") {
		t.Fatal(text)
	}
}
func TestBugbashUnavailableCPUDoesNotShowZeroOrOldValue(t *testing.T) {
	for _, old := range []int{0, 135} {
		st := demoDetails(TabTop)
		st.Metrics.Available = false
		st.Metrics.CPUNow = old
		c := canvas(65, 30, func(r draw.Region) { TopTab(r, st, fixedCtx()) })
		title := strings.Split(c.Plain(), "\n")[0]
		if !strings.Contains(title, "CPU n/a") {
			t.Fatal(title)
		}
	}
}

func TestBugbashReadyStatesAndKnownCPU(t *testing.T) {
	for _, tc := range []struct {
		ready, known bool
		want         string
	}{{true, true, "Ready"}, {false, true, "NotReady"}, {false, false, "Unknown"}} {
		c := canvas(37, 16, func(r draw.Region) {
			NodePanel(r, NodeState{Node: data.Node{Name: "node", Ready: tc.ready, ReadyKnown: tc.known}}, fixedCtx())
		})
		if !strings.Contains(c.Plain(), tc.want) {
			t.Fatal(c.Plain())
		}
	}
	for _, tc := range []struct {
		known bool
		want  string
	}{{true, "CPU 0m"}, {false, "CPU n/a"}} {
		st := demoDetails(TabTop)
		st.Metrics.Available = true
		st.Metrics.CPUKnown = tc.known
		st.Metrics.CPUNow = 0
		c := canvas(65, 30, func(r draw.Region) { TopTab(r, st, fixedCtx()) })
		if !strings.Contains(c.Plain(), tc.want) {
			t.Fatal(c.Plain())
		}
	}
}

func TestBugbashNodePanelRowsUnknown(t *testing.T) {
	for _, width := range []int{36, 40, 54} {
		rows := nodePanelRows(NodeState{Node: data.Node{Name: "node", Coverage: data.Coverage{State: data.CoverageComplete}}}, width)
		if out := strings.Join(rows, "\n"); !strings.Contains(out, "Unknown") || strings.Contains(out, "NotReady") {
			t.Fatal(out)
		}
	}
}

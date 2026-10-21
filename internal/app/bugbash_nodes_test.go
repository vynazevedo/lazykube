package app

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
)

func TestBugbashUnknownReadyOnNodesAndDashboard(t *testing.T) {
	for _, width := range []int{130, 160} {
		source := nodeSource{Demo: data.NewDemo(), cpuKnown: true, node: func(n data.Node) data.Node { n.Ready = false; n.ReadyKnown = false; return n }}
		m := nodeModel(t, source, width, 48)
		if width == 160 {
			if out := m.Render().Plain(); !strings.Contains(out, "Unknown") || strings.Contains(out, "NotReady") {
				t.Fatal(out)
			}
		}
		m = nodes(t, m)
		out := m.Render().Plain()
		if !strings.Contains(out, "Ready: desconhecido") || strings.Contains(out, "Ready: False") {
			t.Fatal(out)
		}
	}
}

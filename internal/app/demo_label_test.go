package app

import (
	"strings"
	"testing"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/ui"
)

func TestDemoLabelEveryTabAndSize(t *testing.T) {
	for _, size := range [][2]int{{130, 40}, {160, 48}} {
		m := demoModel(t, size[0], size[1])
		if !m.snap.Synthetic || m.snap.Cluster.Context != "demo" || m.snap.Cluster.Version != "demo" || m.snap.Node.Name != "demo-node-01" {
			t.Fatal("demo identity missing")
		}
		for _, tab := range ui.Tabs() {
			m.setTab(tab)
			for _, help := range []bool{false, true} {
				m.helpOpen = help
				if header := strings.SplitN(m.Render().Plain(), "\n", 2)[0]; !strings.Contains(header, "[DEMO]") {
					t.Fatalf("demo badge missing at %v tab %v help %v", size, tab, help)
				}
			}
		}
		m.helpOpen = false
		m.setTab(ui.TabSecurity)
		m.refreshSecurity()
		if !strings.Contains(m.Render().Plain(), "Synthetic data (demo)") {
			t.Fatal("synthetic notice missing near score")
		}
	}
}

func TestDemoFindingOrigins(t *testing.T) {
	demo := data.NewDemo()
	for _, tick := range []int{0, 240} {
		demo.AdvanceSecurity(tick)
		count := 0
		for _, p := range demo.Security().Pods {
			if p.Target.Context != "demo" {
				t.Fatal("demo posture context mismatch")
			}
			for _, f := range p.Findings {
				count++
				if f.Origin != "demo fixture" {
					t.Fatalf("demo finding origin %q", f.Origin)
				}
			}
		}
		if count == 0 {
			t.Fatal("demo lacks findings")
		}
	}
}

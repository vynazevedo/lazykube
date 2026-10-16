package kube

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/TNK-Studio/lazykube/internal/app"
)

func TestSecurityRealSourceIsNotDemo(t *testing.T) {
	p := hardenedPod()
	privileged := true
	p.Spec.Containers[0].SecurityContext.Privileged = &privileged
	s := startWith(t, nil, func(s *Source) { s.opts.Context = "demo" }, p)
	waitFor(t, func() bool { return len(s.Security().Pods) > 0 })
	if s.Snapshot().Synthetic {
		t.Fatal("real source declares synthetic data")
	}
	findings := 0
	for _, p := range s.Security().Pods {
		for _, f := range p.Findings {
			findings++
			if f.Origin != "local pod spec" {
				t.Fatalf("unexpected real finding origin: %q", f.Origin)
			}
		}
	}
	if findings == 0 {
		t.Fatal("test needs observed findings")
	}
	for _, size := range [][2]int{{130, 40}, {160, 48}} {
		m := app.New(app.Options{Source: s, NoAnim: true})
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		model := next.(app.Model)
		if header := strings.SplitN(model.Render().Plain(), "\n", 2)[0]; strings.Contains(header, "DEMO") {
			t.Fatal("real source has DEMO badge")
		}
	}
}

package app

import (
	"strings"
	"testing"
)

func TestRadarAndSecurityNeverCarryLinks(t *testing.T) {
	long, _ := longURL()
	seen := 0
	for _, host := range []string{"web.example.com", long} {
		for _, size := range [][2]int{{80, 24}, {130, 40}, {160, 48}, {200, 48}, {240, 48}, {300, 48}} {
			detail, _ := radarPodAccess(t, host, size[0], size[1])
			for _, keys := range [][]string{nil, {"u"}, {"?"}, {"m"}, {"b"}, {"b", "b"}} {
				whole, broken := partialLinks(pressDrive(t, detail, keys...).Render())
				if len(broken) > 0 {
					t.Fatalf("detail from Radar %v %v: %q", keys, size, broken)
				}
				seen += whole
			}
			for _, screen := range []string{"5", "7"} {
				m := settleSecurity(pressDrive(t, detail, screen))
				if m.acc.target.active || len(m.acc.subs) != 0 || m.urls.open {
					t.Fatalf("%s keeps access", screen)
				}
				for _, keys := range [][]string{nil, {"u"}, {"/"}, {"?"}, {"m"}, {"b"}, {"b", "b"}} {
					got := pressDrive(t, m, keys...)
					if whole, broken := partialLinks(got.Render()); whole != 0 || len(broken) > 0 {
						t.Fatalf("screen %s %v %v: %d links %q", screen, keys, size, whole, broken)
					}
					if got.urls.open || got.filtering {
						t.Fatalf("screen %s: %v must not open the URL picker or the filter", screen, keys)
					}
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("the detail must also show intact links")
	}
}

func TestNetworkKeysDoNotChangeRadarOrSecurity(t *testing.T) {
	for name, open := range map[string]func() Model{
		"radar":    func() Model { return newRadarHarness(t).active(160, 48) },
		"security": func() Model { return settleSecurity(onSecurity(t, demoModel(t, 160, 48))) },
	} {
		for _, k := range []string{"u", "/"} {
			before := open()
			after := pressDrive(t, before, k)
			if after.nav != before.nav || after.filter.Value() != before.filter.Value() || after.urls.open || after.filtering {
				t.Fatalf("%s: %s is not bound here", name, k)
			}
		}
		_, msgs := pressYank(t, open())
		if text, ok := clipboardOf(msgs); ok && strings.HasPrefix(text, "http") {
			t.Fatalf("%s: y copied %q", name, text)
		}
	}
	m := openLeaf(t, demoModel(t, 160, 48), DestServices)
	m = pressDrive(t, m, "/", "5", "7")
	if m.filter.Value() != "57" || !m.onScreen(ScreenResources) {
		t.Fatalf("digits are text while filtering: %q", m.filter.Value())
	}
	m = pressDrive(t, pressDrive(t, m, "esc"), "enter", "7")
	if !m.onScreen(ScreenRadar) || len(m.acc.subs) != 0 {
		t.Fatal("7 leaves the detail for the Radar")
	}
}

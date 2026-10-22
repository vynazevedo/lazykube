package app

import (
	"reflect"
	"strings"
	"testing"
)

func TestDestinationRegistryIsValid(t *testing.T) {
	if _, err := newRegistry(buildDestinations()); err != nil {
		t.Fatal(err)
	}
	if got := registeredScreens(); !reflect.DeepEqual(got, []ScreenID{ScreenDashboard, ScreenWorkloads, ScreenNodes, ScreenEvents}) {
		t.Fatalf("the bar keeps the screens of the base: %v", got)
	}
	for id, key := range map[ScreenID]string{ScreenDashboard: "2", ScreenWorkloads: "3", ScreenNodes: "4", ScreenEvents: "6", ScreenPodDetail: ""} {
		if menuKey(id) != key {
			t.Fatalf("screen %d shortcut %q, want %q", id, menuKey(id), key)
		}
	}
	for _, d := range registry.all {
		if d.Availability == AvailPlanned && (d.Implemented || d.Shortcut != "") {
			t.Fatalf("planned %s has no handler and no shortcut", d.ID)
		}
		if d.Kind == DestLeaf && d.Implemented {
			if s, ok := screenFor(d.Screen); !ok || s.ID() != d.Screen {
				t.Fatalf("ready leaf %s resolves its screen", d.ID)
			}
		}
	}
	for reserved, id := range map[string]DestinationID{"5": DestSecurity, "7": DestRadar} {
		if _, ok := registry.shortcut(reserved); ok {
			t.Fatalf("%s stays reserved until its screen exists", reserved)
		}
		if d, _ := registry.get(id); d.Reserved != reserved || d.Availability != AvailPlanned || d.Parent != "" {
			t.Fatalf("%s reserves %s as a root destination", id, reserved)
		}
	}
	first := registry.Children("")
	labels := make([]string, len(first))
	for i, d := range first {
		labels[i] = d.Label
	}
	want := "Cluster Workloads Configuration Network Storage Access Control Custom Resources Helm Security Radar"
	if strings.Join(labels, " ") != want {
		t.Fatalf("roots in a deterministic order: %v", labels)
	}
	if again := mustRegistry(buildDestinations()).Children(""); !reflect.DeepEqual(again, first) {
		t.Fatal("the order does not depend on map iteration")
	}
}

func TestDestinationRegistryRejectsInvalidShapes(t *testing.T) {
	ok := func() []Destination {
		return []Destination{category("a", "A", 0), leaf("a.x", "a", "X", 0, ScreenDashboard, FocusPods)}
	}
	for name, mutate := range map[string]func([]Destination) []Destination{
		"duplicate id": func(d []Destination) []Destination {
			return append(d, leaf("a.x", "a", "Y", 1, ScreenNodes, FocusPods))
		},
		"missing parent": func(d []Destination) []Destination { d[1].Parent = "z"; return d },
		"leaf parent": func(d []Destination) []Destination {
			return append(d, leaf("a.x.y", "a.x", "Y", 0, ScreenNodes, FocusPods))
		},
		"cycle":                func(d []Destination) []Destination { d[0].Parent = "a"; return d },
		"category with screen": func(d []Destination) []Destination { d[0].Implemented = true; return d },
		"ready without screen": func(d []Destination) []Destination { d[1].Implemented = false; return d },
		"planned with screen":  func(d []Destination) []Destination { d[1].Availability = AvailPlanned; return d },
		"planned shortcut": func(d []Destination) []Destination {
			p := planned("a.p", "a", "P", 1)
			p.Shortcut = "9"
			return append(d, p)
		},
		"duplicate shortcut": func(d []Destination) []Destination {
			d[1].Shortcut = "2"
			n := leaf("n", "", "N", 1, ScreenNodes, FocusPods)
			n.Shortcut = "2"
			return append(d, n)
		},
		"reserved clashes with shortcut": func(d []Destination) []Destination {
			d[1].Shortcut = "5"
			p := planned("a.p", "a", "P", 1)
			p.Reserved = "5"
			return append(d, p)
		},
		"reserved on a ready leaf":          func(d []Destination) []Destination { d[1].Reserved = "5"; return d },
		"shortcut category without default": func(d []Destination) []Destination { d[0].Shortcut = "3"; return d },
		"default not a child": func(d []Destination) []Destination {
			d[0].Shortcut, d[0].Default = "3", "n"
			return append(d, leaf("n", "", "N", 1, ScreenNodes, FocusPods))
		},
	} {
		if _, err := newRegistry(mutate(ok())); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	if _, err := newRegistry(ok()); err != nil {
		t.Fatal(err)
	}
}

func TestDestinationRegistryGolden(t *testing.T) {
	var b strings.Builder
	var walk func(parent DestinationID, depth int)
	walk = func(parent DestinationID, depth int) {
		for _, d := range registry.Children(parent) {
			state := map[Availability]string{AvailReady: "pronto", AvailPlanned: "futuro", AvailUnsupported: "sem suporte", AvailDisabled: "desligado"}[d.Availability]
			if d.Kind == DestCategory {
				state = "categoria"
			}
			b.WriteString(strings.Repeat("  ", depth) + string(d.ID) + " | " + d.Label + " | " + state)
			if d.Shortcut != "" {
				b.WriteString(" | atalho " + d.Shortcut)
			}
			if d.Default != "" {
				b.WriteString(" | padrão " + string(d.Default))
			}
			if d.Reserved != "" {
				b.WriteString(" | reservado " + d.Reserved)
			}
			b.WriteString("\n")
			walk(d.ID, depth+1)
		}
	}
	walk("", 0)
	golden(t, "l1_registry_equivalence.txt", b.String())
}

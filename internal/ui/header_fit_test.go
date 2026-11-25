package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
)

type headerCase struct {
	name   string
	st     HeaderState
	badges [][2]string
}

func headerCases() []headerCase {
	ro, rw := [2]string{"[RO]", "[RO]"}, [2]string{"[RW: --read-write]", "[RW]"}
	protected, audit := [2]string{"[PROTECTED]", "[PROTECTED]"}, [2]string{"[ACOES BLOQUEADAS: AUDIT]", "[AUDIT]"}
	config, demo := [2]string{"[CONFIG INVALIDA]", "[CONFIG]"}, [2]string{"[DEMO]", "[DEMO]"}
	return []headerCase{
		{"ro", HeaderState{ReadOnly: true}, [][2]string{ro}},
		{"rw", HeaderState{Writable: true}, [][2]string{rw}},
		{"ro protected", HeaderState{ReadOnly: true, Protected: true}, [][2]string{ro, protected}},
		{"ro config", HeaderState{ReadOnly: true, ConfigInvalid: true}, [][2]string{ro, config}},
		{"rw audit", HeaderState{Writable: true, AuditBlocked: true}, [][2]string{rw, audit}},
		{"ro protected audit", HeaderState{ReadOnly: true, Protected: true, AuditBlocked: true}, [][2]string{ro, protected, audit}},
		{"ro config audit", HeaderState{ReadOnly: true, ConfigInvalid: true, AuditBlocked: true}, [][2]string{ro, config, audit}},
		{"demo ro", HeaderState{Synthetic: true, ReadOnly: true}, [][2]string{demo, ro}},
		{"demo ro protected", HeaderState{Synthetic: true, ReadOnly: true, Protected: true}, [][2]string{demo, ro, protected}},
	}
}

var headerTargets = [][2]string{
	{"dev", "apps"},
	{"laboratorio", "apps"},
	{"laboratorio", ""},
	{"prod-eu-1", strings.Repeat("plataforma-", 5) + "fim"},
	{"arn:aws:eks:sa-east-1:123456789012:cluster/prod-eu-1", "observability-" + strings.Repeat("x", 40)},
	{strings.Repeat("contexto", 30), "a"},
	{"生产集群-欧洲-" + strings.Repeat("节点", 12) + "-一号", "名前空間-" + strings.Repeat("长", 20)},
	{"cluster-é-" + strings.Repeat("é", 40), "default"},
}

func visible(shown, full string, least int) bool {
	if shown == full {
		return true
	}
	return strings.Contains(shown, "…") && draw.Width(shown) >= least-1 && draw.Width(shown) < draw.Width(full)
}

func TestHeaderKeepsModeContextAndNamespaceAtEveryWidth(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })
	for _, v := range []string{"v0.9.0", "dev-6f9b0fd-dirty"} {
		Version = v
		for _, hc := range headerCases() {
			for _, target := range headerTargets {
				for _, alerts := range []int{0, 123} {
					for w := 80; w <= 200; w++ {
						st := hc.st
						st.MetricsOK, st.SecurityAlerts = w%2 == 0, alerts
						st.Cluster = data.Cluster{Context: target[0], Namespace: target[1], Version: "v1.36.2", Nodes: 3, Namespaces: 9, Pods: 40, Uptime: "12d"}
						id := fmt.Sprintf("%s %s %q alerts %d w %d", v, hc.name, target, alerts, w)
						g := HeaderLayout(st, w)
						plain := strings.TrimRight(canvas(w, 1, func(r draw.Region) { Header(r, st, fixedCtx()) }).Plain(), "\n")
						for _, b := range hc.badges {
							if !strings.Contains(plain, b[0]) && !strings.Contains(plain, b[1]) {
								t.Fatalf("%s: %s hidden in %q", id, b, plain)
							}
						}
						if !strings.Contains(plain, "live") && !strings.Contains(plain, "stale") {
							t.Fatalf("%s: state hidden in %q", id, plain)
						}
						if alerts > 0 && !strings.Contains(plain, "!") {
							t.Fatalf("%s: alerts hidden in %q", id, plain)
						}
						ns := target[1]
						if ns == "" {
							ns = data.AllNamespacesLabel
						}
						if !visible(g.Context, target[0], headerMinCtx) || !visible(g.Namespace, ns, headerMinNs) {
							t.Fatalf("%s: context %q namespace %q in %q", id, g.Context, g.Namespace, plain)
						}
						if draw.Width(plain) != w || g.End > g.Right-1 {
							t.Fatalf("%s: geometry %+v in %q", id, g, plain)
						}
						cut := g.Context != target[0] || g.Namespace != ns
						if g.Context != target[0] && draw.Width(g.Namespace) > headerMinNs {
							t.Fatalf("%s: the context yields before the namespace: %q %q", id, g.Context, g.Namespace)
						}
						if cut && (strings.Contains(plain, v) || strings.Contains(plain, "metrics every") || strings.Contains(plain, "k8s ") || strings.Contains(plain, "uptime")) {
							t.Fatalf("%s: lesser data stays while names are cut: %q", id, plain)
						}
						if cut {
							for _, b := range hc.badges {
								if b[0] != b[1] && strings.Contains(plain, b[0]) {
									t.Fatalf("%s: long badge while names are cut: %q", id, plain)
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestHeaderClickMatchesDrawingAtEveryWidth(t *testing.T) {
	for _, hc := range headerCases() {
		for _, target := range headerTargets {
			for w := 80; w <= 200; w++ {
				st := hc.st
				st.Cluster = data.Cluster{Context: target[0], Namespace: target[1]}
				g := HeaderLayout(st, w)
				ctxLabel, nsLabel := g.form.labels()
				if g.NsStart-g.CtxStart != draw.Width(ctxLabel+g.Context+" ") || g.End-g.NsStart != draw.Width(nsLabel+g.Namespace+" ") {
					t.Fatalf("%s %d: %+v", hc.name, w, g)
				}
				for _, x := range []int{0, g.CtxStart - 1, g.CtxStart, g.NsStart - 1, g.NsStart, g.End - 1, g.End, w - 1} {
					want := HeaderNone
					switch {
					case x >= g.CtxStart && x < g.NsStart:
						want = HeaderContext
					case x >= g.NsStart && x < g.End:
						want = HeaderNamespace
					}
					if got := HeaderTargetAt(st, w, x); got != want {
						t.Fatalf("%s w %d x %d: %d, want %d", hc.name, w, x, got, want)
					}
				}
			}
		}
	}
}

func TestHeaderTellsSimilarContextsApart(t *testing.T) {
	for _, hc := range headerCases() {
		for _, prefix := range []string{"arn:aws:eks:sa-east-1:123456789012:cluster/prod-eu-", strings.Repeat("plataforma-", 12) + "prod-eu-"} {
			for w := 80; w <= 200; w++ {
				var seen [2]string
				for i := range seen {
					st := hc.st
					st.SecurityAlerts = 123
					st.Cluster = data.Cluster{Context: fmt.Sprint(prefix, i+1), Namespace: "apps"}
					seen[i] = HeaderLayout(st, w).Context
				}
				if seen[0] == seen[1] || !strings.HasSuffix(seen[0], "-1") || !strings.HasSuffix(seen[1], "-2") {
					t.Fatalf("%s w %d: %q", hc.name, w, seen)
				}
			}
		}
	}
}

func TestHeaderDegradesInTheAgreedOrder(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })
	Version = "dev-6f9b0fd-dirty"
	st := HeaderState{Writable: true, AuditBlocked: true, MetricsOK: true, SecurityAlerts: 7, Cluster: data.Cluster{Context: "laboratorio", Namespace: "apps"}}
	rank := func(f headerForm) int {
		n := 0
		for _, lost := range []bool{!f.version, f.shortBadges, f.shortPill, f.shortAlerts} {
			if lost {
				n++
			}
		}
		return n
	}
	last, seen := 5, map[int]bool{}
	for w := headerVersionFrom; w <= 200; w++ {
		f := HeaderLayout(st, w).form
		n := rank(f)
		if n > last {
			t.Fatalf("w %d: a wider line loses more: %+v", w, f)
		}
		if f.shortBadges && f.version || f.shortPill && !f.shortBadges || f.shortAlerts && !f.shortPill {
			t.Fatalf("w %d: order broken: %+v", w, f)
		}
		last, seen[n] = n, true
	}
	if !seen[0] || len(seen) < 3 {
		t.Fatalf("the sweep must cross the steps: %v", seen)
	}
}

func TestTruncateMiddleKeepsTheSuffix(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  string
	}{{"abc", 3, "abc"}, {"prod-eu-1", 8, "pr…-eu-1"}, {"abcdef", 1, "…"}, {"生产集群一号", 8, "生…一号"}} {
		if got := truncateMiddle(tc.in, tc.width); got != tc.want || draw.Width(got) > tc.width {
			t.Fatalf("%q %d: %q, want %q", tc.in, tc.width, got, tc.want)
		}
	}
}

func TestHeaderStepsAreTheAgreedOnes(t *testing.T) {
	wide := []headerForm{
		{version: true},
		{},
		{shortBadges: true},
		{shortBadges: true, shortPill: true},
		{shortBadges: true, shortPill: true, shortAlerts: true},
	}
	narrow := []headerForm{
		{shortBadges: true, shortPill: true},
		{shortBadges: true, shortPill: true, shortAlerts: true},
	}
	for w := 80; w <= 200; w++ {
		want := wide
		if w < headerVersionFrom {
			want = narrow
		}
		if got := headerForms(w); !slices.Equal(got, want) {
			t.Fatalf("w %d: %+v", w, got)
		}
		for _, hc := range headerCases() {
			st := hc.st
			st.SecurityAlerts = 123
			st.Cluster = data.Cluster{Context: "laboratorio", Namespace: "apps"}
			first := slices.IndexFunc(want, func(f headerForm) bool {
				_, _, room := f.frame(st, w)
				return room >= draw.Width("laboratorioapps")
			})
			if got := HeaderLayout(st, w).form; first >= 0 && got != want[first] {
				t.Fatalf("%s w %d: %+v, want %+v", hc.name, w, got, want[first])
			}
		}
	}
	for n, want := range map[int][2]string{1: {"! 1 high new  ", "!1 "}, 99: {"! 99 high new  ", "!99 "}, 100: {"! 100 high new  ", "!99+ "}, 5000: {"! 5000 high new  ", "!99+ "}} {
		if headerAlerts(n, false) != want[0] || headerAlerts(n, true) != want[1] {
			t.Fatalf("%d: %q %q", n, headerAlerts(n, false), headerAlerts(n, true))
		}
	}
}

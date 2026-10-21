package ui

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
)

func TestHeaderKeepsContextAndNamespaceVisible(t *testing.T) {
	long := "arn:aws:eks:sa-east-1:123456789012:cluster/" + strings.Repeat("plataforma-", 6) + "fim"
	for _, tc := range []struct {
		w      int
		alerts int
		ns     string
	}{{130, 0, ""}, {130, 3, "observability-" + strings.Repeat("x", 40)}, {160, 0, "apps"}, {160, 2, ""}} {
		st := HeaderState{Synthetic: true, Cluster: data.Cluster{Context: long, Namespace: tc.ns, Version: "v1.31.2", Nodes: 3, Namespaces: 9, Pods: 40, Uptime: "12d"}, MetricsOK: true, SecurityAlerts: tc.alerts}
		line := []rune(strings.Split(canvas(tc.w, 1, func(r draw.Region) { Header(r, st, fixedCtx()) }).Plain(), "\n")[0])
		g := HeaderLayout(st, tc.w)
		plain := string(line)
		if !strings.Contains(plain, "context ") || !strings.Contains(plain, "namespace ") {
			t.Fatalf("%d cols: both fields stay visible: %q", tc.w, plain)
		}
		if !strings.HasPrefix(string(line[g.NsStart:]), "namespace ") || !strings.HasPrefix(string(line[g.CtxStart:]), " ◎ context ") {
			t.Fatalf("%d cols: geometry %+v does not match the drawing %q", tc.w, g, plain)
		}
		if g.End > g.Right-2 {
			t.Fatalf("%d cols: the chip runs into the status pill: %+v", tc.w, g)
		}
		for x := range tc.w {
			got := HeaderTargetAt(st, tc.w, x)
			want := HeaderNone
			switch {
			case x >= g.CtxStart && x < g.NsStart:
				want = HeaderContext
			case x >= g.NsStart && x < g.End:
				want = HeaderNamespace
			}
			if got != want {
				t.Fatalf("%d cols: x=%d answers %d, drawing says %d", tc.w, x, got, want)
			}
		}
		if k8s := strings.Index(plain, "k8s"); k8s >= 0 && HeaderTargetAt(st, tc.w, len([]rune(plain[:k8s]))) != HeaderNone {
			t.Fatalf("%d cols: the metrics area does not open the picker", tc.w)
		}
	}
}

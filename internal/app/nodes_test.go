package app

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type nodeSource struct {
	*data.Demo
	node     func(data.Node) data.Node
	podNode  map[string]string
	cpuKnown bool
}

func (s nodeSource) Snapshot() data.Snapshot {
	snap := s.Demo.Snapshot()
	for i, p := range snap.Pods {
		if n, ok := s.podNode[p.Key()]; ok {
			snap.Pods[i].Node = n
		}
	}
	if s.node != nil {
		snap.Node = s.node(snap.Node)
	}
	return snap
}

func (s nodeSource) Metrics(tick int, pod string, heatCols int) data.Metrics {
	m := s.Demo.Metrics(tick, pod, heatCols)
	m.NodeCPUKnown = s.cpuKnown
	if !s.cpuKnown {
		m.NodeCPU = 0
	}
	return m
}

func deniedNode(n data.Node) data.Node {
	return data.Node{Name: n.Name, Address: "-", MemUsed: "n/a", MemTotal: "n/a", Pods: n.Pods, Arch: "-", Ready: true, Coverage: data.Coverage{State: data.CoverageDenied, Reason: "sem permissão: nodes"}}
}

func nodeModel(t *testing.T, src data.Source, w, h int) Model {
	t.Helper()
	m := New(Options{Source: src, Palette: theme.DefaultPalette(), InitialPod: 1, Now: func() time.Time { return time.Unix(0, 0) }})
	m.clock = anim.Fixed(goldenT, goldenPhase, 30)
	m.animated = false
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

func nodes(t *testing.T, m Model) Model {
	t.Helper()
	m = press(t, m, "4")
	if !m.onScreen(ScreenNodes) {
		t.Fatal("4 must activate Nodes")
	}
	return m
}

func TestNodesActivation(t *testing.T) {
	for _, size := range []struct{ w, h, summary, pods int }{{160, 48, 54, 103}, {130, 40, 42, 85}} {
		m := nodes(t, demoModel(t, size.w, size.h))
		l := m.layout
		if l.Node.W != size.summary || l.Pods.W != size.pods || l.Node.X != 1 || l.Pods.X != 1+size.summary+1 || l.Node.Y != 3 || l.Pods.Y != 3 {
			t.Fatalf("%dx%d nodes layout %+v %+v", size.w, size.h, l.Node, l.Pods)
		}
		if l.Node.H != size.h-5 || l.Pods.H != size.h-5 || l.Pods.X+l.Pods.W != size.w-1 || !l.Details.Empty() || !l.Heatmap.Empty() || !l.Namespaces.Empty() || !l.Deployments.Empty() {
			t.Fatalf("%dx%d nodes must show only summary and pods", size.w, size.h)
		}
		if navCrumbs(m) != "Nodes" || m.nav.Depth() != 1 || m.focus != FocusPods || m.menuActive() != 2 || m.nav.Active().Node != "demo-node-01" {
			t.Fatalf("nodes route %+v crumbs %q", m.nav.Active(), navCrumbs(m))
		}
		plain := m.Render().Plain()
		for _, want := range []string{"[4 Nodes]", ui.NodeSummaryTitle, "demo-node-01", "Origem: Snapshot.Node", "Cobertura: complete in scope", "Ready: True", "Memória: 1.4Gi / 3.8Gi", "Inventário completo: fase futura"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("%dx%d nodes screen missing %q", size.w, size.h, want)
			}
		}
	}
	m := nodes(t, demoModel(t, 160, 48))
	if again := press(t, m, "4"); again.nav != m.nav {
		t.Fatal("activating the same screen does not push")
	}
	if back := press(t, m, "esc"); back.nav.Depth() != 0 || !back.onScreen(ScreenDashboard) || !back.layout.ShowLeft {
		t.Fatal("esc returns to the dashboard with its layout")
	}
	if menu := press(t, demoModel(t, 160, 48), "m", "right", "right", "enter"); !menu.onScreen(ScreenNodes) {
		t.Fatal("the menu activates Nodes")
	}
	if menu := press(t, demoModel(t, 160, 48), "m", "4"); !menu.onScreen(ScreenNodes) || menu.menu.open {
		t.Fatal("4 inside the menu activates Nodes")
	}
	x := ui.MenuSpans(m.menuItems())[2].X + m.layout.Menu.X + 1
	if click, _ := demoModel(t, 160, 48).Update(tea.MouseClickMsg{X: x, Y: 1, Button: tea.MouseLeft}); !click.(Model).onScreen(ScreenNodes) {
		t.Fatal("clicking the menu item activates Nodes")
	}
}

func TestNodesListsPodsOfSelectedNode(t *testing.T) {
	demo := data.NewDemo()
	pods := demo.Snapshot().Pods
	other := map[string]string{pods[0].Key(): "node-b", pods[3].Key(): "node-b", pods[5].Key(): ""}
	src := nodeSource{Demo: demo, podNode: other, cpuKnown: true}
	m := nodes(t, nodeModel(t, src, 160, 48))
	visible := m.visiblePods()
	if len(visible) != len(pods)-3 {
		t.Fatalf("nodes lists %d pods, want %d", len(visible), len(pods)-3)
	}
	for _, p := range visible {
		if p.Node != "demo-node-01" {
			t.Fatalf("pod %s from node %q listed on demo-node-01", p.Key(), p.Node)
		}
	}
	st := m.nodeSummaryState()
	if st.Observed != len(pods)-3 || st.Visible != len(visible) || st.Node == nil || !st.CPUKnown {
		t.Fatalf("summary %+v", st)
	}
	end := press(t, m, "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down")
	if p := end.selectedPod(); p == nil || p.Node != "demo-node-01" || end.nav.Active().Node != "demo-node-01" {
		t.Fatal("moving the selection stays on the node of the entry")
	}
	filtered := press(t, m, "/", "c", "o", "r", "e", "d", "n", "s", "enter")
	if len(filtered.visiblePods()) != 1 || filtered.selectedPod() == nil || filtered.selectedPod().Name != "coredns-6799fbcd5-w9t4p" {
		t.Fatalf("filter works inside the node list: %d", len(filtered.visiblePods()))
	}
	sel := m.selName
	detail := press(t, m, "enter")
	if !detail.inDetail() || navCrumbs(detail) != "Nodes > bilibili-live-7f6b8d9c5-mn4tz" {
		t.Fatalf("enter opens the pod detail, crumbs %q", navCrumbs(detail))
	}
	back := press(t, detail, "esc")
	if !back.onScreen(ScreenNodes) || back.nav.Active().Node != "demo-node-01" || back.selName != sel || back.layout.Node.W != 54 {
		t.Fatal("esc returns to the same node with selection and layout")
	}
	b := nodeModel(t, src, 160, 48)
	b.selectPod(0)
	onB := nodes(t, b)
	if onB.nav.Active().Node != "node-b" || len(onB.visiblePods()) != 2 {
		t.Fatalf("a pod on node-b opens node-b: %+v %d", onB.nav.Active(), len(onB.visiblePods()))
	}
	if st := onB.nodeSummaryState(); st.Node != nil || st.CPUKnown {
		t.Fatal("Snapshot.Node describes another node and must not be shown as node-b")
	}
	plain := onB.Render().Plain()
	for _, want := range []string{"node-b", "Ready: desconhecido", "CPU: indisponível", "Memória: indisponível", "Snapshot.Node é de outro node"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("summary of a node without Snapshot.Node missing %q", want)
		}
	}
	if strings.Contains(plain, "demo-node-01") || strings.Contains(plain, "Ready: True") {
		t.Fatal("must not show another node or invent Ready")
	}
	un := nodeModel(t, src, 160, 48)
	un.selectPod(5)
	none := nodes(t, un)
	if none.nav.Active().Node != "" || len(none.visiblePods()) != 0 {
		t.Fatal("a pod without node opens an empty, node-less Nodes screen")
	}
	plain = none.Render().Plain()
	for _, want := range []string{"sem node", "nenhum pod com node selecionado", "Ready: desconhecido", "Pods observados: 0"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("node-less summary missing %q", want)
		}
	}
}

func TestNodesCoverageIsHonest(t *testing.T) {
	src := nodeSource{Demo: data.NewDemo(), node: deniedNode}
	m := nodes(t, nodeModel(t, src, 160, 48))
	plain := m.Render().Plain()
	for _, want := range []string{"demo-node-01", "Cobertura: no permission", "  sem permissão: nodes", "Ready: desconhecido", "CPU: indisponível", "Memória: indisponível"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("denied summary missing %q", want)
		}
	}
	for _, bad := range []string{"Ready: True", "CPU: 0%", "n/a / n/a"} {
		if strings.Contains(plain, bad) {
			t.Fatalf("denied summary must not invent %q", bad)
		}
	}
	unknown := nodeSource{Demo: data.NewDemo(), node: func(n data.Node) data.Node { n.Coverage = data.Coverage{}; return n }, cpuKnown: true}
	if p := nodes(t, nodeModel(t, unknown, 160, 48)).Render().Plain(); !strings.Contains(p, "Cobertura: desconhecido") || !strings.Contains(p, "Ready: desconhecido") || !strings.Contains(p, "CPU: indisponível") {
		t.Fatal("untyped coverage is unknown, never complete")
	}
	quiet := nodeSource{Demo: data.NewDemo()}
	if p := nodes(t, nodeModel(t, quiet, 160, 48)).Render().Plain(); !strings.Contains(p, "CPU: indisponível") || !strings.Contains(p, "Ready: True") {
		t.Fatal("unknown node CPU is unavailable, not 0%")
	}
	hostile := nodeSource{Demo: data.NewDemo(), node: func(n data.Node) data.Node {
		n.Coverage = data.Coverage{State: data.CoverageStale, Reason: "watch\x1b[31m interrompido"}
		return n
	}}
	if p := nodes(t, nodeModel(t, hostile, 160, 48)).Render().String(); strings.Contains(p, "\x1b[31m") {
		t.Fatal("coverage reason must be sanitized")
	}
	dashModel := nodeModel(t, src, 160, 48)
	dash := dashModel.Render().Plain()
	if strings.Contains(dash, "cpu 0%") || !strings.Contains(dash, "cpu indisp") {
		t.Fatal("the dashboard node panel must show unavailable CPU instead of 0% when NodeCPUKnown is false")
	}
	golden(t, "dashboard_node_denied_160x48.txt", dash)
	if measured := demoModel(t, 160, 48).Render().Plain(); !strings.Contains(measured, "cpu 25%") {
		t.Fatal("measured node CPU keeps its value on the dashboard")
	}
	if !strings.Contains(dash, "● Unknown") || strings.Contains(dash, "● Ready") {
		t.Fatal("the dashboard node panel must not invent Ready for a node it cannot observe")
	}
}

func TestNodesKeysAreHonest(t *testing.T) {
	m := nodes(t, demoModel(t, 160, 48))
	keys := nodesScreen{}.Keys(m)
	bound := map[string]bool{}
	for _, b := range keys {
		for _, k := range b.Keys() {
			bound[k] = true
		}
	}
	for _, list := range [][]ui.KeyHelp{ui.NodesHelp(), ui.FullHelpNodes()} {
		for _, h := range list {
			for _, k := range strings.Fields(h.Key) {
				for _, part := range helpTokens(k) {
					if alias := map[string]string{"↑": "up", "↓": "down", "↑↓": "up"}[part]; alias != "" {
						part = alias
					}
					if !bound[part] {
						t.Fatalf("nodes help announces %q, not bound on Nodes", part)
					}
				}
			}
		}
	}
	for _, k := range []string{"tab", "shift+tab", "l", "c", "t", "d", "e", "S", "E", "C", "P", "f", "w", "g", "G", "1", "[", "]", "pgdn", "home"} {
		got := press(t, m, k)
		if got.tab != m.tab || got.focus != m.focus || got.scroll != m.scroll || got.follow != m.follow || got.wrap != m.wrap || got.leftMode != m.leftMode || got.nav != m.nav || got.securityNamespace != m.securityNamespace {
			t.Fatalf("%s has no effect on Nodes and must not change hidden state", k)
		}
		if slices.ContainsFunc(keys, func(b key.Binding) bool { return slices.Contains(b.Keys(), k) }) {
			t.Fatalf("%s must not be bound on Nodes", k)
		}
	}
	filtering := press(t, m, "/", "l", "g")
	if filtering.filter.Value() != "lg" {
		t.Fatalf("typing in the filter still reaches it, got %q", filtering.filter.Value())
	}
	if help := press(t, m, "?"); !help.helpOpen || !strings.Contains(help.Render().Plain(), "filter pods on this node") {
		t.Fatal("? opens the Nodes help")
	}
	if m.screen().Help(m)[2].Desc != "pod detail" {
		t.Fatal("short help belongs to Nodes")
	}
}

func TestNodesGoldens(t *testing.T) {
	for _, size := range [][2]int{{160, 48}, {130, 40}} {
		name := fmt.Sprintf("nodes_%dx%d", size[0], size[1])
		c := nodes(t, demoModel(t, size[0], size[1])).Render()
		golden(t, name+".txt", c.Plain())
		golden(t, name+".ansi", c.String())
		denied := nodes(t, nodeModel(t, nodeSource{Demo: data.NewDemo(), node: deniedNode}, size[0], size[1])).Render()
		golden(t, fmt.Sprintf("nodes_denied_%dx%d", size[0], size[1])+".txt", denied.Plain())
	}
}

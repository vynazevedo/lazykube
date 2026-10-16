package app

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func workloads(t *testing.T, w, h int) Model {
	t.Helper()
	m := press(t, demoModel(t, w, h), "3")
	if !m.onScreen(ScreenWorkloads) {
		t.Fatal("3 must activate Workloads")
	}
	return m
}

func TestWorkloadsActivation(t *testing.T) {
	for _, size := range []struct{ w, h, list, preview int }{{160, 48, 94, 63}, {130, 40, 70, 57}} {
		m := workloads(t, size.w, size.h)
		l := m.layout
		if l.Pods.W != size.list || l.Deployments.W != size.list || l.Details.W != size.preview || l.Pods.Y != 3 || l.Pods.H+l.Deployments.H != size.h-5 || l.Details.H != size.h-5 {
			t.Fatalf("%dx%d workloads layout %+v %+v %+v", size.w, size.h, l.Pods, l.Deployments, l.Details)
		}
		if l.Pods.X+l.Pods.W+1 != l.Details.X || l.Details.X+l.Details.W != size.w-1 || !l.Node.Empty() || !l.Namespaces.Empty() {
			t.Fatalf("%dx%d workloads columns", size.w, size.h)
		}
		if navCrumbs(m) != "Workloads" || m.nav.Depth() != 1 || m.focus != FocusPods || m.menuActive() != 1 {
			t.Fatalf("workloads route, crumbs %q", navCrumbs(m))
		}
		if !strings.Contains(m.Render().Plain(), "[3 Workloads]") {
			t.Fatal("the menu marks Workloads as active")
		}
	}
	m := workloads(t, 160, 48)
	if again := press(t, m, "3"); again.nav != m.nav {
		t.Fatal("activating the same screen does not push")
	}
	dash := press(t, m, "2")
	if !dash.onScreen(ScreenDashboard) || dash.nav.Depth() != 2 || !dash.layout.ShowLeft {
		t.Fatal("2 keeps activating the Dashboard")
	}
	back := press(t, dash, "esc")
	if !back.onScreen(ScreenWorkloads) || back.layout.Pods.W != 94 {
		t.Fatal("esc returns to Workloads with its layout")
	}
	if root := press(t, back, "esc"); root.nav.Depth() != 0 || !root.onScreen(ScreenDashboard) {
		t.Fatal("second esc returns to the dashboard root")
	}
	if menu := press(t, demoModel(t, 160, 48), "m", "right", "enter"); !menu.onScreen(ScreenWorkloads) {
		t.Fatal("menu by keyboard activates Workloads")
	}
	if menu := press(t, demoModel(t, 160, 48), "m", "3"); !menu.onScreen(ScreenWorkloads) || menu.menu.open {
		t.Fatal("3 works inside the menu")
	}
	d := demoModel(t, 160, 48)
	span := ui.MenuSpans(d.menuItems())[1]
	if click := navClick(d, d.layout.Menu.X+span.X+1, d.layout.Menu.Y); !click.onScreen(ScreenWorkloads) {
		t.Fatal("clicking the Workloads title activates it")
	}
}

func TestWorkloadsCarriesNamespaceScope(t *testing.T) {
	scope := navNamespace(t, demoModel(t, 160, 48))
	m := press(t, scope, "3")
	if r := m.nav.Active(); r.Kind != RouteNamespace || r.Screen != ScreenWorkloads || r.Namespace != "kube-system" || navCrumbs(m) != "Workloads > kube-system" {
		t.Fatalf("switching screens keeps the namespace scope, got %+v %q", r, navCrumbs(m))
	}
	deps := m.visibleDeployments()
	if len(deps) == 0 || len(deps) == len(m.snap.Deployments) {
		t.Fatalf("deployments follow the namespace scope, got %d of %d", len(deps), len(m.snap.Deployments))
	}
	for _, d := range deps {
		if d.Namespace != "kube-system" {
			t.Fatalf("deployment %s outside the scope", deploymentKey(d))
		}
	}
	for _, p := range m.visiblePods() {
		if p.Namespace != "kube-system" {
			t.Fatalf("pod %s outside the scope", p.Key())
		}
	}
	if d := press(t, m, "2"); navCrumbs(d) != "Dashboard > kube-system" || d.nsFilter != "kube-system" {
		t.Fatalf("2 from the workloads scope keeps it on the dashboard, got %q", navCrumbs(d))
	}
	if plain := press(t, m, "3"); navCrumbs(plain) != "Workloads" || plain.nsFilter != "" {
		t.Fatal("activating the current screen again clears the scope")
	}
	m = press(t, m, "esc")
	if navCrumbs(m) != "Workloads" || m.nsFilter != "" {
		t.Fatalf("first esc drops the workloads scope, got %q", navCrumbs(m))
	}
	m = press(t, m, "esc")
	if navCrumbs(m) != "Dashboard > kube-system" || m.nsFilter != "kube-system" {
		t.Fatalf("second esc returns to the dashboard scope, got %q", navCrumbs(m))
	}
	if m = press(t, m, "esc"); m.nav.Depth() != 0 {
		t.Fatal("third esc returns to the root")
	}
	if dash := press(t, demoModel(t, 160, 48), "tab", "tab", "tab", "enter"); dash.dep.open || dash.focus != FocusDeployments || dash.nav.Depth() != 0 {
		t.Fatal("enter on the dashboard deployments keeps doing nothing")
	}
}

func TestWorkloadsPodRoundTrip(t *testing.T) {
	m := press(t, workloads(t, 160, 48), "/", "b", "i", "l", "i", "enter", "j", "tab", "tab", "S", "pgdn", "tab")
	if m.focus != FocusPods || m.scroll == 0 || m.filter.Value() != "bili" {
		t.Fatalf("setup: focus %d scroll %d filter %q", m.focus, m.scroll, m.filter.Value())
	}
	before := m.frame()
	m = press(t, m, "enter")
	if !m.inDetail() || m.nav.Active().Pod != before.Selected || navCrumbs(m) != "Workloads > "+strings.SplitN(before.Selected, "/", 2)[1] {
		t.Fatalf("enter on a workloads pod opens the detail, crumbs %q", navCrumbs(m))
	}
	m = press(t, m, "l", "esc")
	got := m.frame()
	if got.Route != before.Route || got.Selected != before.Selected || got.Filter != "bili" || got.Focus != FocusPods || got.PodOffset != before.PodOffset || got.Tab != ui.TabSecurity || got.Scroll != before.Scroll {
		t.Fatalf("esc restores the workloads state:\n got %+v\nwant %+v", got, before)
	}
	if !m.onScreen(ScreenWorkloads) || m.layout.Pods.W != 94 {
		t.Fatal("back on the workloads layout")
	}
}

func TestDeploymentOverlay(t *testing.T) {
	m := press(t, workloads(t, 160, 48), "tab", "j", "j", "j", "j", "j")
	want := m.visibleDeployments()[m.depCursor]
	m = press(t, m, "enter")
	if !m.dep.open || m.dep.key != deploymentKey(want) {
		t.Fatalf("enter opens the deployment overlay, got %+v", m.dep)
	}
	plain := m.Render().Plain()
	box := ui.DeploymentOverlayRect(m.w, m.h)
	lines := strings.Split(plain, "\n")
	var inside strings.Builder
	for y := box.Y; y < box.Y+box.H; y++ {
		inside.WriteString(string([]rune(lines[y])[box.X : box.X+box.W]))
		inside.WriteByte('\n')
	}
	text := inside.String()
	if !strings.Contains(text, fmt.Sprintf("%d/%d", want.Ready, want.Desired)) || !strings.Contains(text, want.Name) || !strings.Contains(text, "Sem relação inferida com pods") {
		t.Fatalf("overlay shows only the replica fields:\n%s", text)
	}
	for _, p := range m.snap.Pods {
		if strings.Contains(text, p.Name) {
			t.Fatalf("overlay must not relate pod %s to the deployment", p.Name)
		}
	}
	nav, cursor, tab, sel := m.nav, m.depCursor, m.tab, m.selName
	for _, k := range []string{"j", "k", "l", "/", "3", "2", "m", "tab", "?", "S"} {
		m = press(t, m, k)
		if !m.dep.open || m.nav != nav || m.depCursor != cursor || m.tab != tab || m.selName != sel || m.menu.open || m.helpOpen || m.filtering {
			t.Fatalf("the overlay consumes %q", k)
		}
	}
	next, _ := m.Update(tea.MouseWheelMsg{X: m.layout.Pods.X + 5, Y: m.layout.Pods.Y + 6, Button: tea.MouseWheelDown})
	if w := next.(Model); w.selName != sel || !w.dep.open {
		t.Fatal("wheel is consumed by the overlay")
	}
	if !strings.Contains(m.Render().Plain(), "enter esc  close") {
		t.Fatal("footer shows the overlay keys")
	}
	for _, closer := range []string{"esc", "enter"} {
		if c := press(t, m, closer); c.dep.open || c.nav != nav || c.depCursor != cursor {
			t.Fatalf("%s closes only the overlay", closer)
		}
	}
	if c := navClick(m, 2, 2); c.dep.open || c.nav != nav {
		t.Fatal("a click closes only the overlay")
	}
	for _, quit := range []tea.KeyPressMsg{keyPress("q"), {Code: 'c', Mod: tea.ModCtrl}} {
		_, cmd := m.Update(quit)
		if cmd == nil {
			t.Fatalf("%s quits from the overlay", quit.String())
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%s quits from the overlay", quit.String())
		}
	}
}

func TestWorkloadsRemovedItems(t *testing.T) {
	src := newNavSource()
	m := press(t, navStart(t, src), "3", "tab", "j", "enter")
	key := m.dep.key
	src.mu.Lock()
	src.goneDep = key
	src.mu.Unlock()
	next, _ := m.Update(dataMsg{})
	m = next.(Model)
	if !m.dep.open || m.dep.key != key || m.deploymentOverlayState().Deployment != nil {
		t.Fatal("a vanished deployment keeps the overlay on the same key")
	}
	if plain := m.Render().Plain(); !strings.Contains(plain, "deployment não observado neste escopo:") || !strings.Contains(plain, key) {
		t.Fatal("overlay explains the missing deployment instead of showing another one")
	}
	m = press(t, m, "esc")
	for _, d := range m.visibleDeployments() {
		if deploymentKey(d) == key {
			t.Fatal("the removed deployment is gone from the list")
		}
	}
	if m.depCursor >= len(m.visibleDeployments()) {
		t.Fatal("deployment cursor stays inside the list")
	}
	src.mu.Lock()
	src.gone = m.selName
	src.mu.Unlock()
	gone := m.selName
	next, _ = m.Update(dataMsg{})
	m = next.(Model)
	if m.selName == gone || m.selectedPod() == nil || !m.onScreen(ScreenWorkloads) {
		t.Fatal("a removed pod moves the workloads selection to a visible pod")
	}
	src.mu.Lock()
	src.partial = true
	src.mu.Unlock()
	next, _ = m.Update(dataMsg{})
	if p := press(t, next.(Model), "tab", "enter"); p.dep.open {
		t.Fatal("deployments hidden by partial coverage cannot be opened")
	}
}

func TestWorkloadsKeys(t *testing.T) {
	m := workloads(t, 160, 48)
	if one := press(t, m, "1"); one.layout != m.layout || one.leftMode != m.leftMode {
		t.Fatal("1 has no function on Workloads")
	}
	order := []Focus{FocusDeployments, FocusDetails, FocusPods}
	for _, want := range order {
		if m = press(t, m, "tab"); m.focus != want {
			t.Fatalf("tab cycles pods, deployments and preview, got %d want %d", m.focus, want)
		}
	}
	for _, b := range m.screen().Keys(m) {
		for _, k := range b.Keys() {
			if k == "1" {
				t.Fatal("workloads must not bind 1")
			}
		}
	}
	nav := m.nav
	for _, size := range [][2]int{{130, 40}, {100, 30}, {160, 48}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(Model)
		if m.nav != nav {
			t.Fatal("resize keeps the stack")
		}
	}
	if m.layout.Pods.W != 94 {
		t.Fatal("workloads layout after resize")
	}
}

func TestWorkloadsGoldens(t *testing.T) {
	for _, size := range [][2]int{{130, 40}, {160, 48}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			m := workloads(t, w, h)
			c := m.Render()
			golden(t, fmt.Sprintf("workloads_%dx%d.txt", w, h), c.Plain())
			golden(t, fmt.Sprintf("workloads_%dx%d.ansi", w, h), c.String())
			o := press(t, m, "tab", "j", "enter").Render()
			golden(t, fmt.Sprintf("deployment_overlay_%dx%d.txt", w, h), o.Plain())
			golden(t, fmt.Sprintf("deployment_overlay_%dx%d.ansi", w, h), o.String())
		})
	}
	golden(t, "workloads_scope_160x48.txt", press(t, navNamespace(t, demoModel(t, 160, 48)), "3").Render().Plain())
	src := newNavSource()
	m := press(t, navStart(t, src), "3", "tab", "enter")
	src.goneDep = m.dep.key
	next, _ := m.Update(dataMsg{})
	golden(t, "deployment_missing_160x48.txt", next.(Model).Render().Plain())
}

func TestWorkloadsScopeClampsDeploymentCursor(t *testing.T) {
	m := press(t, demoModel(t, 160, 48), "tab", "tab", "tab")
	if m.focus != FocusDeployments {
		t.Fatalf("setup focus %d", m.focus)
	}
	m = press(t, m, "G", "j", "j", "j", "j", "j", "j", "j", "j", "j")
	high := m.depCursor
	if high != len(m.snap.Deployments)-1 {
		t.Fatalf("setup cursor %d", high)
	}
	m = press(t, navNamespace(t, m), "3", "tab")
	deps := m.visibleDeployments()
	if m.focus != FocusDeployments || len(deps) == 0 || len(deps) > high {
		t.Fatalf("setup scope: focus %d deployments %d", m.focus, len(deps))
	}
	if m.depCursor < 0 || m.depCursor >= len(deps) {
		t.Fatalf("entering the scope must clamp the cursor, got %d of %d", m.depCursor, len(deps))
	}
	m = press(t, m, "enter")
	if !m.dep.open || m.dep.key != deploymentKey(deps[m.depCursor]) {
		t.Fatalf("enter right after the navigation opens a visible deployment, got %+v", m.dep)
	}
	m = press(t, m, "esc", "esc", "esc", "esc")
	if m.nav.Depth() != 0 || m.depCursor != high {
		t.Fatalf("the return frame keeps the dashboard cursor, depth %d cursor %d want %d", m.nav.Depth(), m.depCursor, high)
	}
}

func podsLiterals(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if sel, ok := lit.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "PodsState" {
				out = append(out, fmt.Sprintf("%s:%d", name, fset.Position(lit.Pos()).Line))
			}
			return true
		})
	}
	return out
}

func renderPods(m Model, r Rect) string {
	c := draw.NewCanvas(r.W, r.H, theme.BgApp)
	ui.PodsPanel(c.Region(), m.podsPanelState(m.visiblePods()), ui.Ctx{Pal: m.pal, Clock: m.clock})
	return c.String()
}

func cropRect(c *draw.Canvas, r Rect) string {
	out := draw.NewCanvas(r.W, r.H, theme.BgApp)
	for y := range r.H {
		for x := range r.W {
			cell := c.At(r.X+x, r.Y+y)
			out.Region().Sub(x, y, 1, 1).Put(0, 0, cell.Rune, draw.Fg(cell.Fg).WithBg(cell.Bg))
		}
	}
	return out.Plain()
}

func TestPodsStateHasSingleConstructor(t *testing.T) {
	if lits := podsLiterals(t); len(lits) != 1 {
		t.Fatalf("ui.PodsState must be built in one place, found %v", lits)
	}
	dash := press(t, demoModel(t, 160, 48), "/", "o", "enter", "j")
	next, _ := dash.Update(tea.MouseMotionMsg{X: dash.layout.Pods.X + 5, Y: dash.layout.Pods.Y + 1 + ui.PodsRowsTop})
	dash = next.(Model)
	work := press(t, dash, "3")
	if dash.podsPanelState(dash.visiblePods()).Selected != work.podsPanelState(work.visiblePods()).Selected {
		t.Fatal("both screens select the same pod")
	}
	for _, c := range []struct {
		name string
		m    Model
	}{{"dashboard", dash}, {"workloads", work}} {
		want := draw.NewCanvas(c.m.layout.Pods.W, c.m.layout.Pods.H, theme.BgApp)
		ui.PodsPanel(want.Region(), c.m.podsPanelState(c.m.visiblePods()), ui.Ctx{Pal: c.m.pal, Clock: c.m.clock})
		if got := cropRect(c.m.Render(), c.m.layout.Pods); got != want.Plain() {
			t.Fatalf("%s pods panel must come from the shared constructor", c.name)
		}
	}
	same := Rect{W: 60, H: 20}
	if renderPods(dash, same) != renderPods(work, same) {
		t.Fatal("dashboard and workloads build the same pods state")
	}
}

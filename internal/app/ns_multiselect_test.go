package app

import (
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func namespacesWithPods(t *testing.T, m Model) (string, string) {
	t.Helper()
	var names []string
	for _, ns := range m.snap.Namespaces {
		if ns.Pods > 0 {
			names = append(names, ns.Name)
		}
	}
	if len(names) < 2 {
		t.Fatal("the fixture needs two namespaces with pods")
	}
	return names[0], names[1]
}

func focusNamespace(t *testing.T, m Model, name string) Model {
	t.Helper()
	idx := slices.IndexFunc(m.snap.Namespaces, func(ns data.Namespace) bool { return ns.Name == name })
	if idx < 0 {
		t.Fatalf("unknown namespace %s", name)
	}
	m.setFocus(FocusNamespaces)
	m.nsCursor = idx
	return m
}

func mark(t *testing.T, m Model, names ...string) Model {
	t.Helper()
	for _, name := range names {
		m = press(t, focusNamespace(t, m, name), "space")
	}
	return m
}

func podNamespaces(m Model) map[string]int {
	out := map[string]int{}
	for _, p := range m.visiblePods() {
		out[p.Namespace]++
	}
	return out
}

func TestMarkingNamespacesFiltersPods(t *testing.T) {
	m := demoModel(t, 160, 48)
	total := len(m.visiblePods())
	a, b := namespacesWithPods(t, m)
	m = mark(t, m, a)
	if got := podNamespaces(m); len(got) != 1 || got[a] == 0 {
		t.Fatalf("one mark shows only that namespace: %v", got)
	}
	m = mark(t, m, b)
	if got := podNamespaces(m); len(got) != 2 || got[a] == 0 || got[b] == 0 {
		t.Fatalf("two marks show both namespaces: %v", got)
	}
	if p := m.selectedPod(); p == nil || (p.Namespace != a && p.Namespace != b) {
		t.Fatal("the selection stays inside the marked namespaces")
	}
	m = mark(t, m, a)
	if got := podNamespaces(m); len(got) != 1 || got[b] == 0 {
		t.Fatalf("marking again removes the namespace: %v", got)
	}
	m = mark(t, m, b)
	if len(m.visiblePods()) != total {
		t.Fatal("removing the last mark shows every pod")
	}
	if m.focus != FocusNamespaces || m.nav.Depth() != 0 {
		t.Fatal("marking keeps the focus and pushes no route")
	}
}

func TestMarkingShowsSelectionCompactly(t *testing.T) {
	m := demoModel(t, 160, 48)
	a, b := namespacesWithPods(t, m)
	m = mark(t, m, a)
	plain := m.Render().Plain()
	if !strings.Contains(plain, "ns:"+a+" ") || !strings.Contains(plain, "● "+a) || !strings.Contains(plain, "Namespaces · 1") {
		t.Fatalf("one mark is shown in the filter header, the row and the title:\n%s", plain)
	}
	m = mark(t, m, b)
	plain = m.Render().Plain()
	want := []string{a, b}
	slices.Sort(want)
	if !strings.Contains(plain, "ns:"+want[0]+" +1") || !strings.Contains(plain, "● "+b) || !strings.Contains(plain, "Namespaces · 2") {
		t.Fatalf("two marks stay compact:\n%s", plain)
	}
}

func TestEscClearsMarks(t *testing.T) {
	m := demoModel(t, 160, 48)
	total := len(m.visiblePods())
	a, b := namespacesWithPods(t, m)
	m = press(t, mark(t, m, a, b), "esc")
	if len(m.visiblePods()) != total || strings.Contains(m.Render().Plain(), "● "+a) || strings.Contains(m.Render().Plain(), "Namespaces · ") {
		t.Fatal("esc clears every mark at the root")
	}
}

func TestEscClearsMarksAcrossScreens(t *testing.T) {
	m := demoModel(t, 160, 48)
	total := len(m.visiblePods())
	a, b := namespacesWithPods(t, m)
	m = mark(t, m, a, b)
	m.activateDestination(DestDeployments)
	if got := podNamespaces(m); len(got) != 2 {
		t.Fatalf("the marks follow to Workloads: %v", got)
	}
	for _, d := range m.visibleDeployments() {
		if d.Namespace != a && d.Namespace != b {
			t.Fatalf("deployment outside the marks: %s", d.Namespace)
		}
	}
	depth := m.nav.Depth()
	m = press(t, m, "esc")
	if m.nav.Depth() != depth || len(m.visiblePods()) != total {
		t.Fatal("the first esc clears the marks without leaving the screen")
	}
	m = press(t, m, "esc")
	if m.nav.Depth() != depth-1 || len(m.visiblePods()) != total {
		t.Fatal("going back after clearing never restores the old marks")
	}
}

func TestEnterOpensSingleNamespaceAndBackRestoresMarks(t *testing.T) {
	m := demoModel(t, 160, 48)
	a, b := namespacesWithPods(t, m)
	m = mark(t, m, a, b)
	m = press(t, focusNamespace(t, m, b), "enter")
	if got := podNamespaces(m); len(got) != 1 || got[b] == 0 || navCrumbs(m) != "Dashboard > "+b {
		t.Fatalf("enter keeps opening one namespace scope: %v %s", got, navCrumbs(m))
	}
	m = press(t, m, "esc")
	if got := podNamespaces(m); len(got) != 2 || m.nav.Depth() != 0 {
		t.Fatalf("back returns to the marked set: %v", got)
	}
}

func TestMarkingInsideScopeBecomesSelection(t *testing.T) {
	m := demoModel(t, 160, 48)
	a, b := namespacesWithPods(t, m)
	m = press(t, focusNamespace(t, m, a), "enter")
	m = mark(t, m, b)
	if got := podNamespaces(m); len(got) != 2 || got[a] == 0 || got[b] == 0 {
		t.Fatalf("a mark inside a scope adds to it: %v", got)
	}
	if navCrumbs(m) != "Dashboard" || m.scopeNamespace() != "" {
		t.Fatalf("the single scope route gives way to the selection: %s", navCrumbs(m))
	}
	if m.focus != FocusNamespaces || m.snap.Namespaces[m.nsCursor].Name != b {
		t.Fatal("the cursor stays on the namespace just marked")
	}
}

func TestSpaceOnlyMarksInNamespacesPanel(t *testing.T) {
	m := demoModel(t, 160, 48)
	total := len(m.visiblePods())
	m = press(t, m, "space")
	if m.focus != FocusPods || len(m.visiblePods()) != total {
		t.Fatal("space on the pods panel marks nothing")
	}
	m = press(t, m, "/", "a", "space", "b")
	if m.filter.Value() != "a b" {
		t.Fatalf("space stays a character while filtering, got %q", m.filter.Value())
	}
}

func TestMarkKeyIsFreeAndDocumented(t *testing.T) {
	m := demoModel(t, 160, 48)
	matches := 0
	for _, b := range m.keys.Bindings() {
		if key.Matches(keyPress("space"), b) {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("space belongs to exactly one binding, got %d", matches)
	}
	if !slices.ContainsFunc(ui.FullHelp(), func(h ui.KeyHelp) bool { return h.Key == "space" }) {
		t.Fatal("the full help documents the mark key")
	}
	s, _ := screenFor(ScreenDashboard)
	if slices.ContainsFunc(s.Help(m), func(h ui.KeyHelp) bool { return h.Key == "space" }) {
		t.Fatal("the footer offers space only on the namespaces panel")
	}
	m.setFocus(FocusNamespaces)
	if !slices.ContainsFunc(s.Help(m), func(h ui.KeyHelp) bool { return h.Key == "space" }) {
		t.Fatal("the footer offers space on the namespaces panel")
	}
}

func TestMarksReadNoCatalogAndStartNoSubscription(t *testing.T) {
	b := newFakeBrowser(nil, nil)
	m := nodeModel(t, b, 160, 48)
	first, second := namespacesWithPods(t, m)
	m = mark(t, m, first, second)
	next, cmd := m.Update(tea.FocusMsg{})
	pump(t, next.(Model), cmd)
	if b.catalogs.Load() != 0 || b.subs.Load() != 0 {
		t.Fatalf("marks are a client filter: catalogs %d subs %d", b.catalogs.Load(), b.subs.Load())
	}
}

func TestSecurityFollowsMarkedSet(t *testing.T) {
	m := demoModel(t, 160, 48)
	a, b := namespacesWithPods(t, m)
	m = press(t, mark(t, m, a), "S")
	rows := strings.Join(m.securityView.Rows, "\n")
	if !strings.Contains(rows, "POSTURE | Namespace: "+a) {
		t.Fatalf("one mark keeps the namespace posture:\n%s", rows)
	}
	pod := m.activeName()
	m = mark(t, m, b)
	if m.activeName() != pod {
		t.Fatal("the fixture keeps the selected pod so only the set changes")
	}
	rows = strings.Join(m.securityView.Rows, "\n")
	want := []string{a, b}
	slices.Sort(want)
	if !strings.Contains(rows, "POSTURE | Namespaces: "+want[0]+", "+want[1]) {
		t.Fatalf("the cache key follows the set and the posture names it:\n%s", rows)
	}
	merged := m.securitySession.Dashboard.Namespaces[a].Distribution.Count + m.securitySession.Dashboard.Namespaces[b].Distribution.Count
	if !strings.Contains(m.securityView.Coverage, "/") || merged == 0 {
		t.Fatalf("the coverage line is about the set: %q", m.securityView.Coverage)
	}
	if strings.Contains(rows, "Trend: ▁") {
		t.Fatal("no trend is invented for a set without history")
	}
	if !strings.Contains(rows, "Trend: only per namespace") {
		t.Fatalf("the trend states its limit:\n%s", rows)
	}
}

func TestEventsScopeWithMarks(t *testing.T) {
	m := demoModel(t, 160, 48)
	a, b := namespacesWithPods(t, m)
	m = mark(t, m, a, b)
	m.setFocus(FocusDetails)
	m = press(t, m, "e", "E")
	p := m.selectedPod()
	if p == nil || m.events.Scope != data.ScopeNamespace || m.events.Target != p.Namespace {
		t.Fatalf("namespace events follow the selected pod inside the set: %+v", m.events)
	}
}

func TestCompactMarkFlow(t *testing.T) {
	m := demoModel(t, 80, 24)
	a, b := namespacesWithPods(t, m)
	m = press(t, m, "1")
	if !m.layout.ShowLeft {
		t.Fatal("the left column opens in the compact layout")
	}
	m = mark(t, m, a, b)
	plain := m.Render().Plain()
	if !strings.Contains(plain, "Namespaces · 2") || !strings.Contains(plain, "● "+a) {
		t.Fatalf("the compact panel shows the marks:\n%s", plain)
	}
	c := m.Render()
	golden(t, "ns_marks_panel_80x24.txt", c.Plain())
	golden(t, "ns_marks_panel_80x24.ansi", c.String())
	m.setFocus(FocusPods)
	want := []string{a, b}
	slices.Sort(want)
	plain = m.Render().Plain()
	if !strings.Contains(plain, "ns:"+want[0]+" +1") {
		t.Fatalf("the compact pods header shows the set:\n%s", plain)
	}
	c = m.Render()
	golden(t, "ns_marks_pods_80x24.txt", c.Plain())
	golden(t, "ns_marks_pods_80x24.ansi", c.String())
}

func TestMarkGolden(t *testing.T) {
	m := demoModel(t, 160, 48)
	a, b := namespacesWithPods(t, m)
	c := mark(t, m, a, b).Render()
	golden(t, "ns_marks_160x48.txt", c.Plain())
	golden(t, "ns_marks_160x48.ansi", c.String())
}

func TestPodDetailKeepsMarks(t *testing.T) {
	m := demoModel(t, 160, 48)
	a, b := namespacesWithPods(t, m)
	m = mark(t, m, a, b)
	m.setFocus(FocusPods)
	m = press(t, m, "enter")
	if m.nav.Active().Kind != RoutePod {
		t.Fatal("enter opens the pod")
	}
	m = press(t, m, "esc")
	if got := podNamespaces(m); len(got) != 2 || m.nav.Depth() != 0 {
		t.Fatalf("esc on the pod goes back and keeps the marks: %v", got)
	}
}

func TestNamespaceScopeIsNotShownAsMark(t *testing.T) {
	m := demoModel(t, 160, 48)
	a, _ := namespacesWithPods(t, m)
	plain := press(t, focusNamespace(t, m, a), "enter").Render().Plain()
	if strings.Contains(plain, "● "+a) || strings.Contains(plain, "Namespaces · ") {
		t.Fatalf("enter keeps the single scope look:\n%s", plain)
	}
}

func TestMarkedNamespaceThatVanishesIsClearable(t *testing.T) {
	m := demoModel(t, 160, 48)
	total := len(m.visiblePods())
	a, _ := namespacesWithPods(t, m)
	m = mark(t, m, a)
	m.nsFilter = m.nsFilter.toggle(a).toggle("gone")
	m.syncSelection()
	if len(m.visiblePods()) != 0 || !strings.Contains(m.Render().Plain(), "ns:gone") {
		t.Fatal("a mark without pods stays visible instead of silently showing everything")
	}
	if m = press(t, m, "esc"); len(m.visiblePods()) != total {
		t.Fatal("esc recovers from a vanished namespace")
	}
}

func TestNSSetIsOrderedAndComparable(t *testing.T) {
	a := nsSet("").toggle("b").toggle("a")
	b := nsSet("").toggle("a").toggle("b")
	if a != b || strings.Join(a.names(), ",") != "a,b" || a.label() != "a, b" {
		t.Fatalf("the set has one canonical form: %q %q", a, b)
	}
	if _, ok := a.single(); ok {
		t.Fatal("two names are not a single namespace")
	}
	if name, ok := a.toggle("a").single(); !ok || name != "b" {
		t.Fatal("removing one leaves the other")
	}
	if a.toggle("a").toggle("b") != "" {
		t.Fatal("removing all empties the set")
	}
	if !nsSet("").matcher()("x") || nsSet("a").matcher()("x") || !a.matcher()("b") || a.matcher()("c") {
		t.Fatal("the matcher follows the set")
	}
}

func TestSecurityWithoutSelectedPodKeepsSetAggregate(t *testing.T) {
	m := demoModel(t, 160, 48)
	a, b := namespacesWithPods(t, m)
	m = press(t, mark(t, m, a, b), "S")
	m.filter.SetValue("zzzz-no-pod-match-zzzz")
	m.syncSelection()
	if m.selectedPod() != nil {
		t.Fatal("the fixture selects no pod")
	}
	rows := strings.Join(m.securityView.Rows, "\n")
	for _, wrong := range []string{"namespace not observed", "No pod observations in this scope."} {
		if strings.Contains(rows, wrong) {
			t.Fatalf("observed namespaces never read %q:\n%s", wrong, rows)
		}
	}
	want := []string{a, b}
	slices.Sort(want)
	if !strings.Contains(rows, "POSTURE | Namespaces: "+want[0]+", "+want[1]) {
		t.Fatalf("the aggregate of the set stays:\n%s", rows)
	}
	if !strings.Contains(rows, "No pod selected in the marked namespaces.") {
		t.Fatalf("the missing selection is stated:\n%s", rows)
	}
	if !strings.Contains(m.securityView.Scope, "Namespaces: "+want[0]+", "+want[1]) {
		t.Fatalf("the scope names the set: %q", m.securityView.Scope)
	}
	c := m.Render()
	golden(t, "ns_marks_security_no_pod_160x48.txt", c.Plain())
	golden(t, "ns_marks_security_no_pod_160x48.ansi", c.String())
}

func TestMarkAfterHistoryOverflowLeavesScope(t *testing.T) {
	m := demoModel(t, 160, 48)
	total := len(m.visiblePods())
	a, b := namespacesWithPods(t, m)
	for i := range maxFrames + 2 {
		ns := a
		if i%2 == 1 {
			ns = b
		}
		m = press(t, focusNamespace(t, m, ns), "enter")
	}
	m = mark(t, m, a)
	if m.nav.Active().Kind != RouteScreen || m.nav.Active().Screen != ScreenDashboard || m.scopeNamespace() != "" {
		t.Fatalf("the mark leaves the namespace route: %+v scope %q", m.nav.Active(), m.scopeNamespace())
	}
	if !m.onScreen(ScreenDashboard) || m.nav.Active().Dest != DestDashboard {
		t.Fatalf("the screen stays the Dashboard: %+v", m.nav.Active())
	}
	plain := m.Render().Plain()
	if !strings.Contains(plain, "● "+a) || !strings.Contains(plain, "● "+b) || !strings.Contains(plain, "Namespaces · 2") {
		t.Fatalf("the marks are visible:\n%s", plain)
	}
	if got := podNamespaces(m); len(got) != 2 {
		t.Fatalf("the pods follow the set: %v", got)
	}
	m = press(t, m, "esc")
	if len(m.visiblePods()) != total || strings.Contains(m.Render().Plain(), "Namespaces · ") {
		t.Fatal("esc clears the marks after the overflow")
	}
}

func TestMarkOverPodFrameLeavesScope(t *testing.T) {
	m := demoModel(t, 160, 48)
	a, b := namespacesWithPods(t, m)
	m.setFocus(FocusPods)
	m = press(t, m, "enter")
	if m.nav.Active().Kind != RoutePod {
		t.Fatal("the fixture opens a pod")
	}
	m.navigate(Route{Kind: RouteNamespace, Screen: ScreenDashboard, Namespace: a})
	m = mark(t, m, b)
	if m.nav.Active().Kind != RouteScreen || m.nav.Active().Screen != ScreenDashboard || m.scopeNamespace() != "" {
		t.Fatalf("a pod below the scope never becomes the active route: %+v", m.nav.Active())
	}
	if got := podNamespaces(m); len(got) != 2 {
		t.Fatalf("the set is applied: %v", got)
	}
}

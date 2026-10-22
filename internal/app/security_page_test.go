package app

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func onSecurity(t *testing.T, m Model) Model {
	t.Helper()
	m = press(t, m, "5")
	if !m.onScreen(ScreenSecurity) {
		t.Fatal("5 must activate Security")
	}
	return m
}

func TestSecurityPageActivation(t *testing.T) {
	for _, size := range []struct{ w, h, summary, list int }{{160, 48, 6, 52}, {130, 40, 5, 42}} {
		m := onSecurity(t, demoModel(t, size.w, size.h))
		l := m.layout
		if l.SecSummary.H != size.summary || l.SecTabs.H != 2 || l.SecList.W != size.list || l.SecEvidence.X != l.SecList.X+l.SecList.W+1 || l.SecNotes.Y+l.SecNotes.H != size.h-2 {
			t.Fatalf("%dx%d security layout %+v %+v %+v %+v", size.w, size.h, l.SecSummary, l.SecList, l.SecEvidence, l.SecNotes)
		}
		if navCrumbs(m) != "Security" || m.nav.Depth() != 1 || m.menuActive() != 3 || m.nav.Active().Dest != DestSecurity {
			t.Fatalf("security route, crumbs %q", navCrumbs(m))
		}
		plain := m.Render().Plain()
		for _, want := range []string{"[5 Security]", "Postura de segurança", "Todos: Segurança", "Sem sistema: Segurança", "[Workloads]", "[Rede e exposição]", "[Audit]", "Nota não garante segurança", "Evidência e remediação", "Sugestão YAML", "PSS v1.34: verificação local do spec; não é admissão", "[DEMO] dados sintéticos"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("%dx%d security page missing %q", size.w, size.h, want)
			}
		}
	}
	m := onSecurity(t, demoModel(t, 160, 48))
	if again := press(t, m, "5"); again.nav != m.nav {
		t.Fatal("activating the same screen does not push")
	}
	if back := press(t, m, "esc"); !back.onScreen(ScreenDashboard) || back.nav.Depth() != 0 {
		t.Fatal("esc returns to the dashboard")
	}
	if menu := press(t, demoModel(t, 160, 48), "m", "right", "right", "right", "enter"); !menu.onScreen(ScreenSecurity) {
		t.Fatal("the menu activates Security")
	}
	x := ui.MenuSpans(m.menuItems())[3].X + m.layout.Menu.X + 1
	if click, _ := demoModel(t, 160, 48).Update(tea.MouseClickMsg{X: x, Y: 1, Button: tea.MouseLeft}); !click.(Model).onScreen(ScreenSecurity) {
		t.Fatal("clicking the menu item activates Security")
	}
}

func TestSecurityPageSharesDashboardScore(t *testing.T) {
	m := onSecurity(t, demoModel(t, 160, 48))
	cluster := m.securitySession.Dashboard.Cluster
	if m.sec.all.Score != cluster.Score || m.sec.all.Hygiene != cluster.Hygiene {
		t.Fatalf("page score %+v differs from dashboard %+v", m.sec.all.Score, cluster.Score)
	}
	want := fmt.Sprintf("Todos: Segurança %d %s | Higiene %d %s", cluster.Score.Value, cluster.Score.Grade, cluster.Hygiene.Value, cluster.Hygiene.Grade)
	if !strings.Contains(m.Render().Plain(), want) {
		t.Fatalf("summary must print the canonical score %q", want)
	}
	ns := navNamespace(t, demoModel(t, 160, 48))
	ns = onSecurity(t, ns)
	scope := ns.securityScope()
	if scope == "" || ns.sec.all.Score != ns.securitySession.Dashboard.Namespaces[scope].Score {
		t.Fatalf("namespace scope %q uses the dashboard namespace aggregate", scope)
	}
	for _, w := range ns.sec.workloads {
		if w.Key.Namespace != scope {
			t.Fatalf("namespace scope lists only its workloads: %+v", w.Key)
		}
	}
	if got := press(t, ns, "H"); got.sec.NoSystem {
		t.Fatal("cohort toggle only applies to the cluster scope")
	}
}

func TestSecurityPageCohortAndSelection(t *testing.T) {
	m := onSecurity(t, demoModel(t, 160, 48))
	all := len(m.sec.workloads)
	h := press(t, m, "H")
	if !h.sec.NoSystem || len(h.sec.workloads) >= all {
		t.Fatalf("H hides system namespaces from the list: %d -> %d", all, len(h.sec.workloads))
	}
	for _, w := range h.sec.workloads {
		if data.IsSystemNamespace(w.Key.Namespace) {
			t.Fatalf("system workload listed in the cohort: %+v", w.Key)
		}
	}
	if h.sec.all.Score != m.sec.all.Score || h.sec.noSystem.Score != m.sec.noSystem.Score {
		t.Fatal("cohort does not change the summary denominators")
	}
	if !strings.Contains(h.Render().Plain(), "| Sem sistema ") {
		t.Fatal("list title names the active cohort")
	}
	first := m.sec.Selected
	d := press(t, m, "down")
	if d.sec.Cursor != 1 || d.sec.Selected == first || d.sec.rulesFor != d.sec.Selected {
		t.Fatal("down selects the next workload and loads its rules")
	}
	if e := press(t, d, "tab"); e.sec.Focus != ui.SecurityFocusEvidence || e.sec.Cursor != 1 {
		t.Fatal("tab focuses the evidence panel")
	}
	if e := press(t, d, "enter", "down"); e.sec.Focus != ui.SecurityFocusEvidence || e.sec.Cursor != 1 || e.sec.Scroll != 1 {
		t.Fatal("enter focuses evidence and arrows scroll it")
	}
	moved := press(t, d, "end")
	if moved.sec.Cursor != len(m.sec.workloads)-1 {
		t.Fatal("end selects the last workload")
	}
	key := moved.sec.Selected
	moved.clock = anim.Fixed(240, goldenPhase, 30)
	next, _ := moved.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	adv := next.(Model)
	if adv.security.Revision == moved.security.Revision {
		t.Fatal("demo advances the security revision")
	}
	if adv.sec.Selected != key || adv.sec.workloads[adv.sec.Cursor].Key != key {
		t.Fatal("selection follows the workload identity across revisions")
	}
}

func TestSecurityPageRulesFollowRevision(t *testing.T) {
	m := onSecurity(t, demoModel(t, 160, 48))
	target := data.PodKey("bilibili-raffle", "bilibili-raffle-5d8f9c7b4-x2kqp")
	i := slices.IndexFunc(m.sec.workloads, func(w data.WorkloadPosture) bool { return slices.Contains(w.Pods, target) })
	if i < 0 {
		t.Fatal("demo workload missing")
	}
	m = press(t, m, "home")
	for range i {
		m = press(t, m, "down")
	}
	key := m.sec.Selected
	hasRule := func(m Model, rule string) bool {
		return slices.ContainsFunc(m.sec.rules, func(r data.WorkloadRule) bool { return r.Rule == rule })
	}
	if hasRule(m, "hostPID") {
		t.Fatal("hostPID appears only after the demo revision")
	}
	m.clock = anim.Fixed(240, goldenPhase, 30)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	adv := next.(Model)
	if adv.sec.Selected != key || !hasRule(adv, "hostPID") || !strings.Contains(adv.Render().Plain(), "hostPID") {
		t.Fatal("a new revision with the same selection refreshes rules and evidence")
	}
	renewed := adv
	renewed.security = renewed.security.Clone()
	p := renewed.security.Pods[target]
	delete(renewed.security.Pods, target)
	p.Target.Name, p.Target.UID, p.Findings = "bilibili-raffle-5d8f9c7b4-zz9zz", "renewed", nil
	renewed.security.Pods[data.PodKey(p.Target.Namespace, p.Target.Name)] = p
	renewed.security.Revision++
	renewed.securitySession.Update(renewed.security)
	renewed.refreshSecurityPage()
	if renewed.sec.Selected != key || hasRule(renewed, "hostPID") || slices.Contains(renewed.sec.workloads[renewed.sec.Cursor].Pods, target) {
		t.Fatal("a renewed pod under the same owner UID drops the removed finding")
	}
}

func TestSecurityPageTabs(t *testing.T) {
	m := onSecurity(t, demoModel(t, 160, 48))
	r := press(t, m, "]")
	if r.sec.Tab != ui.SecTabRBAC || !strings.Contains(r.Render().Plain(), "Concessões RBAC observadas; não são permissões efetivas.") || !strings.Contains(r.Render().Plain(), "nenhuma leitura foi feita") {
		t.Fatal("] opens RBAC with the waiting state and no reads")
	}
	if a := press(t, m, "["); a.sec.Tab != ui.SecTabAudit || !strings.Contains(a.Render().Plain(), "nenhum registro foi carregado") {
		t.Fatal("[ wraps to Audit")
	}
	if a := press(t, m, "[", "[", "["); a.sec.Tab != ui.SecTabPSS || !strings.Contains(a.Render().Plain(), "não é admissão") {
		t.Fatal("PSS tab never claims admission")
	}
	if a := press(t, m, "]", "down", "tab"); a.sec.Cursor != m.sec.Cursor || a.sec.Focus != ui.SecurityFocusList {
		t.Fatal("pending tabs do not move the hidden workload selection")
	}
	l := m.layout.SecTabs
	for _, c := range []struct {
		label string
		tab   ui.SecurityPageTab
		row   int
	}{{"[RBAC]", ui.SecTabRBAC, 0}, {"[Achados aceitos]", ui.SecTabAccepted, 1}} {
		line := strings.Split(m.Render().Plain(), "\n")[l.Y+c.row]
		x := strings.Index(line, c.label)
		if x < 0 {
			t.Fatalf("tab %s not rendered on row %d", c.label, c.row)
		}
		x = len([]rune(line[:x]))
		next, _ := m.Update(tea.MouseClickMsg{X: x + 1, Y: l.Y + c.row, Button: tea.MouseLeft})
		if next.(Model).sec.Tab != c.tab {
			t.Fatalf("clicking %s selects it", c.label)
		}
	}
	row := m.layout.SecList.Y + 2 + 2
	next, _ := m.Update(tea.MouseClickMsg{X: m.layout.SecList.X + 3, Y: row, Button: tea.MouseLeft})
	if next.(Model).sec.Cursor != 2 {
		t.Fatal("clicking a list row selects that workload")
	}
}

func TestSecurityPageKeysAreHonest(t *testing.T) {
	m := onSecurity(t, demoModel(t, 160, 48))
	for _, k := range []string{"l", "t", "d", "e", "S", "C", "P", "f", "w", "g", "G", "1", "/", "E", "W", "i", "B"} {
		got := press(t, m, k)
		if got.sec.securityFrame != m.sec.securityFrame || got.tab != m.tab || got.filtering || got.nav != m.nav || got.wrap != m.wrap || got.follow != m.follow || got.securityNamespace != m.securityNamespace {
			t.Fatalf("%s has no effect on Security and must not change hidden state", k)
		}
	}
	help := press(t, m, "?")
	plain := help.Render().Plain()
	if !help.helpOpen || !strings.Contains(plain, "previous / next Security tab") || !strings.Contains(plain, "list cohort") {
		t.Fatal("? opens the Security help")
	}
	for _, h := range append(ui.SecurityHelp(), ui.FullHelpSecurity()...) {
		for _, k := range strings.Fields(h.Key) {
			if k == "W" || k == "i" || k == "B" || k == "/" || k == "S" || k == "7" {
				t.Fatalf("security help announces %q before its handler exists", k)
			}
		}
	}
}

func TestSecurityPageGoldens(t *testing.T) {
	for _, size := range [][2]int{{160, 48}, {130, 40}} {
		name := fmt.Sprintf("security_%dx%d", size[0], size[1])
		c := onSecurity(t, demoModel(t, size[0], size[1])).Render()
		golden(t, name+".txt", c.Plain())
		golden(t, name+".ansi", c.String())
		golden(t, fmt.Sprintf("security_rbac_%dx%d.txt", size[0], size[1]), press(t, onSecurity(t, demoModel(t, size[0], size[1])), "]").Render().Plain())
	}
}

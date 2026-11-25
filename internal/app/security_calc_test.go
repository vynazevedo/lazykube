package app

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/goleak"

	"github.com/vynazevedo/lazykube/internal/data"
)

func pendingSecurity(t *testing.T) Model {
	t.Helper()
	next, cmd := demoModel(t, 160, 48).Update(keyPress("5"))
	m := next.(Model)
	if !m.secCalc.running || cmd == nil {
		t.Fatal("opening Security schedules the recompute")
	}
	return m
}

func calcMsg(m Model) securityCalcMsg {
	msg := m.secCalc.job.run()
	msg.session, msg.seq = m.session.Generation, m.secCalc.seq
	return msg
}

func deliver(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

func markNamespaces(t *testing.T, m Model, names ...string) Model {
	t.Helper()
	var marks nsSet
	for _, n := range names {
		marks = marks.toggle(n)
	}
	m.setMarks(marks)
	m.syncSelection()
	return deliver(t, m, tea.FocusMsg{})
}

func TestSecurityRecomputeRunsOutsideUpdate(t *testing.T) {
	m := pendingSecurity(t)
	if m.sec.have || len(m.sec.result.workloads) != 0 {
		t.Fatal("Update must not compute the page")
	}
	plain := m.Render().Plain()
	if !strings.Contains(plain, "Calculando a postura; ainda sem conclusão.") || strings.Contains(plain, "Sem amostra avaliável") || strings.Contains(plain, "Todos: Segurança") {
		t.Fatal("a pending recompute is not an empty or scored page")
	}
	for range 5 {
		seq := m.secCalc.seq
		m = deliver(t, m, dataMsg{})
		if m.secCalc.seq != seq {
			t.Fatal("at most one recompute in flight")
		}
	}
	if m = deliver(t, m, calcMsg(m)); !m.sec.have || m.secCalc.running || len(m.sec.result.workloads) == 0 {
		t.Fatal("the result is applied once and the recompute settles")
	}
	if m = deliver(t, m, dataMsg{}); m.secCalc.running {
		t.Fatal("an unchanged revision does not recompute")
	}
}

func TestSecurityRecomputeDiscardsStaleResults(t *testing.T) {
	m := pendingSecurity(t)
	job, old := m.secCalc.job, calcMsg(m)

	other := old
	other.session++
	if got := deliver(t, m, other); got.sec.have || !got.secCalc.running {
		t.Fatal("a result of another session is ignored")
	}
	past := old
	past.seq--
	if got := deliver(t, m, past); got.sec.have || !got.secCalc.running {
		t.Fatal("a result of another request is ignored")
	}

	scoped := markNamespaces(t, m, "kube-system")
	if job.ctx.Err() == nil {
		t.Fatal("changing the scope cancels the running recompute")
	}
	scoped = deliver(t, scoped, old)
	if scoped.sec.have || len(scoped.sec.result.workloads) != 0 {
		t.Fatal("a cluster result never fills a namespace scope")
	}
	if !scoped.secCalc.running || scoped.secCalc.job.key.Scope != "kube-system" {
		t.Fatal("the discarded result is followed by the wanted scope")
	}
	scoped = settleSecurity(scoped)
	for _, w := range scoped.sec.result.workloads {
		if w.Key.Namespace != "kube-system" {
			t.Fatalf("workload outside the scope: %+v", w.Key)
		}
	}

	wide := markNamespaces(t, scoped)
	if wide.sec.have || len(wide.sec.result.workloads) != 0 {
		t.Fatal("the previous scope is cleared before the new result arrives")
	}
}

func TestSecurityRecomputeFollowsRevisions(t *testing.T) {
	m := pendingSecurity(t)
	first := calcMsg(m)
	m.security = m.security.Clone()
	m.security.Revision++
	m.securitySession.Update(m.security)
	m = deliver(t, m, first)
	if !m.sec.have || m.sec.result.key.Revision == m.security.Revision || !m.secCalc.running {
		t.Fatal("a same-scope result is shown while the next revision runs")
	}
	if !strings.Contains(m.Render().Plain(), "recalculando") {
		t.Fatal("an older revision is labelled")
	}
	m = settleSecurity(m)
	if m.sec.result.key.Revision != m.security.Revision || strings.Contains(m.Render().Plain(), "recalculando") {
		t.Fatal("the latest revision replaces it")
	}
	rules := m
	rules.security = rules.security.Clone()
	rules.security.RuleVersion = "next"
	rules.security.Revision++
	rules.securitySession.Update(rules.security)
	if rules = deliver(t, rules, tea.FocusMsg{}); rules.sec.have {
		t.Fatal("a new rule version drops scores of the previous one")
	}
}

func TestSecurityRecomputeEndsWithScreenAndSession(t *testing.T) {
	baseline := goleak.IgnoreCurrent()
	m := pendingSecurity(t)
	job, late := m.secCalc.job, calcMsg(m)
	away := deliver(t, m, keyPress("esc"))
	if job.ctx.Err() == nil || !job.run().canceled {
		t.Fatal("leaving Security cancels the recompute")
	}
	if away = deliver(t, away, late); away.sec.have || away.secCalc.running {
		t.Fatal("a late result does not fill a closed page")
	}
	loaded := onSecurity(t, demoModel(t, 160, 48))
	if left := press(t, loaded, "2"); left.sec.have || left.sec.result.workloads != nil || left.sec.rules != nil {
		t.Fatal("leaving Security releases the derived page")
	}

	r := NewRoot(RootOptions{Options: Options{Source: data.NewDemo(), NoAnim: true}})
	live, _ := r.model()
	live.setLayout(160, 48)
	live = deliver(t, live, keyPress("5"))
	running, result := live.secCalc.job, calcMsg(live)
	r.Shutdown()
	if running.ctx.Err() == nil || !running.run().canceled {
		t.Fatal("Root.Shutdown cancels the recompute")
	}
	if live = deliver(t, live, result); live.sec.have {
		t.Fatal("a result after Shutdown is discarded")
	}
	goleak.VerifyNone(t, baseline)
}

func TestSecurityPageNamespaceSet(t *testing.T) {
	m := onSecurity(t, demoModel(t, 160, 48))
	d := m.securitySession.Dashboard
	names := []string{"kube-system", "bilibili-raffle"}
	slices.Sort(names)
	set := settleSecurity(markNamespaces(t, m, names...))
	if want := d.MergeNamespaces(names); set.sec.result.all.Score != want.Score || set.sec.result.all.Hygiene != want.Hygiene || set.sec.result.all.High != want.High {
		t.Fatalf("a namespace set uses the dashboard merge: %+v", set.sec.result.all.Score)
	}
	seen := map[string]bool{}
	for _, w := range set.sec.result.workloads {
		seen[w.Key.Namespace] = true
	}
	if len(seen) != 2 || !seen[names[0]] || !seen[names[1]] {
		t.Fatalf("only marked namespaces are listed: %v", seen)
	}
	plain := set.Render().Plain()
	if !strings.Contains(plain, "Namespaces (2) "+strings.Join(names, ", ")) || strings.Contains(plain, "Sem sistema") || !strings.Contains(plain, "sem histórico para a seleção") {
		t.Fatal("the summary names the set and has no cohort or invented history")
	}
	if got := press(t, set, "H"); got.sec.NoSystem {
		t.Fatal("the cohort only applies to the cluster scope")
	}
	one := settleSecurity(markNamespaces(t, m, "kube-system"))
	if one.sec.result.all.Score != d.Namespaces["kube-system"].Score {
		t.Fatal("one namespace keeps its canonical score")
	}
	gone := settleSecurity(markNamespaces(t, m, "kube-system", "missing"))
	if c := gone.sec.result.all.Score.Coverage; c.State != data.CoveragePartial {
		t.Fatalf("an unobserved namespace keeps the set partial: %+v", c)
	}
}

package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
)

func TestCompactOverviewKeepsSelectionVisible(t *testing.T) {
	m := openLeaf(t, demoModel(t, 80, 24), DestWorkloadsOver)
	plain := m.Render().Plain()
	if !strings.Contains(plain, "Pods") || strings.Contains(plain, "ReplicaSets") {
		t.Fatal("the compact overview opens on its first cards")
	}
	if !strings.Contains(plain, "cards 1-3 de 7") {
		t.Fatal("the compact overview states which cards are on screen")
	}
	m = press(t, m, "end")
	plain = m.Render().Plain()
	if !strings.Contains(plain, "ReplicaSets") || !strings.Contains(plain, "cards 5-7 de 7") {
		t.Fatalf("end brings the last card on screen:\n%s", plain)
	}
	c := m.Render()
	golden(t, "l2_overview_compact_end_80x24.txt", c.Plain())
	golden(t, "l2_overview_compact_end_80x24.ansi", c.String())
	m = press(t, m, "enter")
	if m.nav.Active().Dest != DestReplicaSets {
		t.Fatalf("enter opens the card on screen, got %s", m.nav.Active().Dest)
	}
}

func TestCompactOverviewHitTestFollowsViewport(t *testing.T) {
	m := openLeaf(t, demoModel(t, 80, 24), DestWorkloadsOver)
	tbl := m.layout.Table
	blank := tbl.Y + 1 + 3*5
	if blank >= tbl.Y+tbl.H {
		t.Fatalf("the fixture needs an empty band, table height %d", tbl.H)
	}
	next, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: tbl.X + 2, Y: blank})
	if got := next.(Model); got.nav.Active().Dest != DestWorkloadsOver {
		t.Fatalf("a click on the empty band opens nothing, got %s", got.nav.Active().Dest)
	}
	m = press(t, m, "end")
	next, _ = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: tbl.X + 2, Y: tbl.Y + 1})
	if got := next.(Model); got.nav.Active().Dest != DestJobs {
		t.Fatalf("a click opens the card drawn at that position, got %s", got.nav.Active().Dest)
	}
}

func TestCompactOverviewWheelMovesSelection(t *testing.T) {
	m := openLeaf(t, demoModel(t, 80, 24), DestWorkloadsOver)
	tbl := m.layout.Table
	for range 6 {
		next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: tbl.X + 2, Y: tbl.Y + 2})
		m = next.(Model)
	}
	if !strings.Contains(m.Render().Plain(), "ReplicaSets") {
		t.Fatal("the wheel reaches the cards below the fold")
	}
}

func namespacedJobs(names ...[2]string) data.ResourcePage {
	page := data.ResourcePage{Total: len(names), Coverage: data.Coverage{State: data.CoverageComplete}}
	for _, n := range names {
		r := workloadRow(n[1], data.WorkloadSummary{Kind: data.WorkloadJob, State: data.WorkloadCompleted})
		r.Ref.Namespace = n[0]
		page.Items = append(page.Items, r)
	}
	return page
}

func TestCompactTableDistinguishesNamespaces(t *testing.T) {
	b := newScriptBrowser(nil)
	b.page = namespacedJobs([2]string{"apps", "backup"}, [2]string{"ops", "backup"})
	m := openLeaf(t, nodeModel(t, b, 80, 24), DestJobs)
	plain := m.Render().Plain()
	for _, want := range []string{"apps/backup", "ops/backup"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("the compact table misses %q:\n%s", want, plain)
		}
	}
}

func TestCompactTableKeepsSelectedIdentityWhenTruncated(t *testing.T) {
	long := "a-workload-with-a-very-long-name-that-never-fits-the-compact-column"
	b := newScriptBrowser(nil)
	b.page = namespacedJobs([2]string{"apps", long + "-1"}, [2]string{"ops", long + "-2"})
	m := openLeaf(t, nodeModel(t, b, 80, 24), DestJobs)
	if !strings.Contains(m.Render().Plain(), "selecionado apps/a-workload") {
		t.Fatal("the footer keeps the namespace of the selected object")
	}
	m = press(t, m, "down")
	if !strings.Contains(m.Render().Plain(), "selecionado ops/a-workload") {
		t.Fatal("the footer follows the selection")
	}
}

func TestCompactTableInNamespaceScopeNeedsNoPrefix(t *testing.T) {
	b := newScriptBrowser(nil)
	b.Demo = data.NewDemoScoped("demo", "apps")
	b.page = namespacedJobs([2]string{"apps", "backup"})
	m := openLeaf(t, nodeModel(t, b, 80, 24), DestJobs)
	plain := m.Render().Plain()
	if strings.Contains(plain, "apps/backup") || !strings.Contains(plain, "backup") {
		t.Fatalf("a single namespace scope does not repeat the namespace:\n%s", plain)
	}
}

package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
)

type searchHandle struct {
	*fakeHandle
	started chan string
	release chan struct{}
}

func (h searchHandle) Search(query string, _ int) data.ResourcePage {
	h.started <- query
	<-h.release
	page := h.page
	page.Items, page.Filtered = nil, true
	for _, row := range h.page.Items {
		if row.Matches(query) {
			page.Items = append(page.Items, row)
		}
	}
	page.Matched = len(page.Items)
	return page
}

type searchBrowser struct {
	fakeBrowser
	started chan string
	release chan struct{}
}

func (b searchBrowser) Subscribe(ctx context.Context, key data.ResourceSubscriptionKey) (data.ResourceSubscription, error) {
	h, err := b.fakeBrowser.Subscribe(ctx, key)
	return searchHandle{h.(*fakeHandle), b.started, b.release}, err
}

func TestFilterSearchesOutsideUpdateAndDropsStaleResults(t *testing.T) {
	page := netPage(netRow("apps", "web", data.NetworkFacts{Service: &data.ServiceFacts{}}), netRow("apps", "db", data.NetworkFacts{Service: &data.ServiceFacts{}}))
	b := searchBrowser{newNetBrowser(nil, map[schema.GroupVersionResource]data.ResourcePage{serviceGVR: page}), make(chan string, 16), make(chan struct{})}
	m := openLeaf(t, nodeModel(t, b, 160, 48), DestServices)
	m.after = instantTimer
	next, _ := m.Update(keyPress("/"))
	m = next.(Model)
	var running tea.Cmd
	for i, k := range []string{"w", "e", "b"} {
		next, cmd := m.Update(keyPress(k))
		m = next.(Model)
		if i == 0 {
			running = cmd
		}
		if !m.res.searching || !m.searchPending() {
			t.Fatal("the search is pending")
		}
		if plain := m.Render().Plain(); !strings.Contains(plain, "filtrando…") || strings.Contains(plain, " de 2  ") {
			t.Fatal("a pending search claims no count")
		}
	}
	if len(b.started) != 0 {
		t.Fatal("Update never searches")
	}
	if before := m.nav; func() Route {
		n, _ := m.Update(keyPress("enter"))
		n2, _ := n.(Model).Update(keyPress("enter"))
		return n2.(Model).nav.Active()
	}() != before.Active() {
		t.Fatal("enter does not open a row of another query")
	}
	results := make(chan tea.Msg, 64)
	run := func(cmd tea.Cmd) { launch(cmd, results) }
	run(running)
	if q := <-b.started; q != "w" {
		t.Fatalf("one search at a time, first %q", q)
	}
	close(b.release)
	search := func() tableSearchMsg {
		for {
			if s, ok := (<-results).(tableSearchMsg); ok {
				return s
			}
		}
	}
	stale := search()
	next, cmd := m.Update(stale)
	m = next.(Model)
	if !m.searchPending() || len(m.tableState().Page.Items) != 2 {
		t.Fatal("the result of an older query is dropped")
	}
	run(cmd)
	if q := <-b.started; q != "web" {
		t.Fatalf("the current query follows: %q", q)
	}
	next, _ = m.Update(search())
	m = next.(Model)
	if st := m.tableState(); m.searchPending() || len(st.Page.Items) != 1 || st.Page.Items[0].Ref.Name != "web" {
		t.Fatalf("%+v", m.tableState().Page.Items)
	}
	next, _ = m.Update(stale)
	if st := next.(Model).tableState(); len(st.Page.Items) != 1 {
		t.Fatal("a late duplicate changes nothing")
	}
	next, _ = m.Update(keyPress("esc"))
	if st := next.(Model).tableState(); len(st.Page.Items) != 2 || next.(Model).searchPending() {
		t.Fatal("clearing the filter restores the list at once")
	}
}

func launch(cmd tea.Cmd, out chan<- tea.Msg) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				launch(c, out)
			}
			return
		}
		out <- msg
	}()
}

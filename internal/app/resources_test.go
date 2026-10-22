package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
)

type fakeHandle struct {
	page      data.ResourcePage
	changes   chan struct{}
	cancels   *atomic.Int32
	refreshes *atomic.Int32
}

func (h *fakeHandle) Page(string, int) data.ResourcePage { return h.page }
func (h *fakeHandle) Changes() <-chan struct{}           { return h.changes }
func (h *fakeHandle) Refresh()                           { h.refreshes.Add(1) }
func (h *fakeHandle) Cancel()                            { h.cancels.Add(1) }

type fakeBrowser struct {
	*data.Demo
	snap      data.ResourceCatalogSnapshot
	pages     map[schema.GroupVersionResource]data.ResourcePage
	catalogs  *atomic.Int32
	subs      *atomic.Int32
	cancels   *atomic.Int32
	refreshes *atomic.Int32
	keys      *[]data.ResourceSubscriptionKey
}

func newFakeBrowser(avail map[string]data.ResourceAvailability, pages map[schema.GroupVersionResource]data.ResourcePage) fakeBrowser {
	snap := data.ResourceCatalogSnapshot{Session: "s1", Generation: 4, Coverage: data.Coverage{State: data.CoverageComplete}}
	for _, w := range workloadLeaves {
		a, ok := avail[w.gvr.Resource]
		if !ok {
			a = data.ResourceServed
		}
		if a == data.ResourceAbsent {
			continue
		}
		snap.Resources = append(snap.Resources, data.ResourceDescriptor{GVR: w.gvr, Kind: string(w.kind), Namespaced: true, List: true, Watch: true, Availability: a})
	}
	return fakeBrowser{Demo: data.NewDemo(), snap: snap, pages: pages, catalogs: &atomic.Int32{}, subs: &atomic.Int32{}, cancels: &atomic.Int32{}, refreshes: &atomic.Int32{}, keys: &[]data.ResourceSubscriptionKey{}}
}

func (b fakeBrowser) Catalog(context.Context, bool) data.ResourceCatalogSnapshot {
	b.catalogs.Add(1)
	return b.snap
}

func (b fakeBrowser) Subscribe(_ context.Context, key data.ResourceSubscriptionKey) (data.ResourceSubscription, error) {
	b.subs.Add(1)
	*b.keys = append(*b.keys, key)
	return &fakeHandle{page: b.pages[key.GVR], changes: make(chan struct{}, 1), cancels: b.cancels, refreshes: b.refreshes}, nil
}

func pump(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		out := make(chan tea.Msg, 1)
		go func() { out <- c() }()
		var msg tea.Msg
		select {
		case msg = <-out:
		case <-time.After(100 * time.Millisecond):
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if msg == nil {
			continue
		}
		next, more := m.Update(msg)
		m = next.(Model)
		queue = append(queue, more)
	}
	return m
}

func openLeaf(t *testing.T, m Model, id DestinationID) Model {
	t.Helper()
	m.activateDestination(id)
	next, cmd := m.Update(tea.FocusMsg{})
	return pump(t, next.(Model), cmd)
}

func workloadRow(name string, w data.WorkloadSummary) data.ResourceRow {
	return data.ResourceRow{Ref: data.ResourceMetadataRef{Namespace: "apps", Name: name, UID: "uid-" + name, CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, Workload: w}
}

func TestWorkloadTableShowsTypedRowsAndUnknownCounts(t *testing.T) {
	sts := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
	page := data.ResourcePage{Total: 2, Revision: 3, ObservedAt: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Coverage: data.Coverage{State: data.CoverageComplete}, Items: []data.ResourceRow{
		workloadRow("db", data.WorkloadSummary{Kind: data.WorkloadStatefulSet, State: data.WorkloadReady, Ready: data.ResourceCount{Value: 3, Known: true}, Desired: data.ResourceCount{Value: 3, Known: true}}),
		workloadRow("cache", data.WorkloadSummary{Kind: data.WorkloadStatefulSet, State: data.WorkloadUnknown, Desired: data.ResourceCount{Value: 2, Known: true}}),
	}}
	page.Workloads.Add(page.Items[0].Workload, 1)
	page.Workloads.Add(page.Items[1].Workload, 1)
	jobs := schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
	b := newFakeBrowser(nil, map[schema.GroupVersionResource]data.ResourcePage{sts: page, jobs: {Coverage: data.Coverage{State: data.CoverageComplete}}})
	m := nodeModel(t, b, 160, 48)
	if b.catalogs.Load() != 0 {
		t.Fatal("the catalog is not read before a resource screen is opened")
	}
	m = openLeaf(t, m, DestStatefulSets)
	if b.catalogs.Load() != 1 || b.subs.Load() != 1 {
		t.Fatalf("opening the leaf reads the catalog once and subscribes once: %d %d", b.catalogs.Load(), b.subs.Load())
	}
	key := (*b.keys)[0]
	if key.Session != "s1" || key.Generation != 4 || key.GVR != sts || key.ProjectionVersion != data.ResourceProjectionVersion || key.Namespace != "" {
		t.Fatalf("subscription key = %+v", key)
	}
	plain := m.Render().Plain()
	for _, want := range []string{"StatefulSets · focus", "READY", "STATE", "AGE", "db", "3/3", "ready", "?/2", "unknown", "cobertura completa no escopo", "2 itens", "2d"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("table misses %q", want)
		}
	}
	if strings.Contains(plain, "0/2") {
		t.Fatal("an unknown counter never becomes zero")
	}
	if menu := m.menuActive(); menu != 1 {
		t.Fatalf("the bar highlights Workloads, got %d", menu)
	}
	m = press(t, m, "r")
	if b.refreshes.Load() != 1 {
		t.Fatal("r asks the subscription for an explicit refresh")
	}
	m = openLeaf(t, m, DestJobs)
	if b.cancels.Load() != 1 || b.subs.Load() != 2 {
		t.Fatalf("switching leaves cancels the old subscription: cancels %d subs %d", b.cancels.Load(), b.subs.Load())
	}
	if !strings.Contains(m.Render().Plain(), "nenhum Jobs neste escopo") {
		t.Fatal("a complete empty page is an empty inventory")
	}
	m = press(t, m, "2")
	next, _ := m.Update(tea.FocusMsg{})
	if b.cancels.Load() != 2 || len(next.(Model).res.subs) != 0 {
		t.Fatal("leaving the workload tables cancels every subscription")
	}
}

func TestWorkloadTableStatesStayDistinct(t *testing.T) {
	cases := []struct {
		name  string
		src   func() data.Source
		page  data.ResourcePage
		avail data.ResourceAvailability
		want  string
	}{
		{"no browser", func() data.Source { return plainSource{data.NewDemo()} }, data.ResourcePage{}, "", "esta fonte não oferece o catálogo de recursos"},
		{"denied discovery", nil, data.ResourcePage{}, data.ResourceDenied, "sem permissão para descobrir StatefulSets"},
		{"not served", nil, data.ResourcePage{}, data.ResourceAbsent, "StatefulSets não é servido por este cluster"},
		{"partial discovery", nil, data.ResourcePage{}, data.ResourcePartial, "descoberta parcial"},
		{"denied list", nil, data.ResourcePage{Coverage: data.Coverage{State: data.CoverageDenied, Reason: "list forbidden"}}, "", "sem permissão para listar StatefulSets"},
		{"partial page", nil, data.ResourcePage{Coverage: data.Coverage{State: data.CoveragePartial, Reason: "watch encerrado"}}, "", "sem itens observados; partial: watch encerrado"},
		{"list only", nil, data.ResourcePage{Coverage: data.Coverage{State: data.CoverageListOnly}}, "", "lista pontual, sem acompanhamento"},
	}
	sts := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var src data.Source
			var b fakeBrowser
			if tc.src != nil {
				src = tc.src()
			} else {
				avail := map[string]data.ResourceAvailability{}
				if tc.avail != "" {
					avail["statefulsets"] = tc.avail
				}
				b = newFakeBrowser(avail, map[schema.GroupVersionResource]data.ResourcePage{sts: tc.page})
				src = b
			}
			m := openLeaf(t, nodeModel(t, src, 160, 48), DestStatefulSets)
			plain := m.Render().Plain()
			if !strings.Contains(plain, tc.want) {
				t.Fatalf("%s shows %q", tc.name, tc.want)
			}
			if strings.Contains(plain, "nenhum StatefulSets") {
				t.Fatalf("%s never claims an empty inventory", tc.name)
			}
			if tc.avail != "" && b.subs.Load() != 0 {
				t.Fatalf("%s does not subscribe to a resource that is not served", tc.name)
			}
		})
	}
}

func TestStaleSubscriptionIsCancelled(t *testing.T) {
	b := newFakeBrowser(nil, nil)
	m := openLeaf(t, nodeModel(t, b, 160, 48), DestStatefulSets)
	sts := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
	cancels := &atomic.Int32{}
	late := &fakeHandle{changes: make(chan struct{}), cancels: cancels, refreshes: &atomic.Int32{}}
	next, _ := m.Update(resourceSubMsg{seq: 999, gvr: sts, handle: late})
	if cancels.Load() != 1 || next.(Model).res.subs[sts].handle == late {
		t.Fatal("a late subscription of an older request is cancelled and ignored")
	}
	if next, _ := m.Update(resourceCatalogMsg{session: m.session.Generation + 1}); next.(Model).res.catalog != m.res.catalog {
		t.Fatal("a catalog of another session is ignored")
	}
}

func TestOverviewSubscribesEveryKindAndOpensLists(t *testing.T) {
	b := newFakeBrowser(nil, nil)
	m := openLeaf(t, nodeModel(t, b, 160, 48), DestWorkloadsOver)
	if b.subs.Load() != int32(len(workloadLeaves)) {
		t.Fatalf("the overview subscribes to every workload kind, got %d", b.subs.Load())
	}
	plain := m.Render().Plain()
	for _, w := range workloadLeaves {
		if !strings.Contains(plain, w.label) {
			t.Fatalf("overview misses %s", w.label)
		}
	}
	if !strings.Contains(plain, "contagens por objeto, não por réplica") {
		t.Fatal("the overview states its denominator")
	}
	m = press(t, m, "right", "right", "enter")
	next, cmd := m.Update(tea.FocusMsg{})
	m = pump(t, next.(Model), cmd)
	if m.nav.Active().Dest != DestStatefulSets || b.cancels.Load() != int32(len(workloadLeaves))-1 || b.subs.Load() != int32(len(workloadLeaves)) || len(m.res.subs) != 1 {
		t.Fatalf("enter opens the card list, keeps the identical subscription and releases the others: dest %s cancels %d subs %d", m.nav.Active().Dest, b.cancels.Load(), b.subs.Load())
	}
}

func TestDemoWorkloadGoldens(t *testing.T) {
	for _, tc := range []struct {
		name string
		dest DestinationID
		w, h int
	}{
		{"l2_overview_160x48", DestWorkloadsOver, 160, 48},
		{"l2_statefulsets_160x48", DestStatefulSets, 160, 48},
		{"l2_cronjobs_160x48", DestCronJobs, 160, 48},
		{"l2_jobs_compact_80x24", DestJobs, 80, 24},
		{"l2_overview_compact_80x24", DestWorkloadsOver, 80, 24},
	} {
		m := openLeaf(t, demoModel(t, tc.w, tc.h), tc.dest)
		c := m.Render()
		golden(t, tc.name+".txt", c.Plain())
		golden(t, tc.name+".ansi", c.String())
	}
}

func TestTreeExpansionNeverReadsTheCatalog(t *testing.T) {
	b := newFakeBrowser(nil, nil)
	m := press(t, nodeModel(t, b, 160, 48), "b")
	m = press(t, treeTo(t, m, DestWorkloads), "right", "down", "down", "down", "up", "left", "right")
	m = press(t, treeTo(t, m, DestConfiguration), "right", "down", "enter")
	next, cmd := m.Update(tea.FocusMsg{})
	pump(t, next.(Model), cmd)
	if b.catalogs.Load() != 0 || b.subs.Load() != 0 {
		t.Fatalf("expanding and browsing the tree read the catalog %d times and subscribed %d times", b.catalogs.Load(), b.subs.Load())
	}
}

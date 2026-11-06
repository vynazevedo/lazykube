package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
)

var jobsGVR = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}

type pagedHandle struct {
	mu      sync.Mutex
	rows    []data.ResourceRow
	rev     uint64
	bumps   int
	expire  bool
	changes chan struct{}
	cancels atomic.Int32
}

func (h *pagedHandle) Page(cursor string, limit int) data.ResourcePage {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := data.ResourcePage{Revision: h.rev, Total: len(h.rows), Coverage: data.Coverage{State: data.CoverageComplete}}
	offset := 0
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		rev, _ := strconv.ParseUint(parts[0], 10, 64)
		n, _ := strconv.Atoi(parts[1])
		if h.bumps > 0 {
			h.bumps--
			h.rev++
		}
		if h.expire || rev != h.rev {
			out.Coverage = data.Coverage{State: data.CoverageStale, Reason: "page cursor expired; restart from first page"}
			return out
		}
		offset = n
	}
	end := min(offset+min(limit, 500), len(h.rows))
	out.Items = append(out.Items, h.rows[offset:end]...)
	if end < len(h.rows) {
		out.Continue = fmt.Sprintf("%d:%d", h.rev, end)
	}
	return out
}

func (h *pagedHandle) Changes() <-chan struct{} { return h.changes }
func (h *pagedHandle) Refresh()                 {}
func (h *pagedHandle) Cancel()                  { h.cancels.Add(1) }

func (h *pagedHandle) set(f func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f()
}

type pagedBrowser struct {
	*data.Demo
	snap   data.ResourceCatalogSnapshot
	handle *pagedHandle
}

func (b pagedBrowser) Catalog(context.Context, bool) data.ResourceCatalogSnapshot { return b.snap }

func (b pagedBrowser) Subscribe(_ context.Context, key data.ResourceSubscriptionKey) (data.ResourceSubscription, error) {
	if key.GVR == jobsGVR {
		return b.handle, nil
	}
	return &fakeHandle{changes: make(chan struct{}, 1), cancels: &atomic.Int32{}, refreshes: &atomic.Int32{}}, nil
}

func jobRows(n int) []data.ResourceRow {
	rows := make([]data.ResourceRow, n)
	for i := range rows {
		rows[i] = workloadRow(fmt.Sprintf("job-%04d", i+1), data.WorkloadSummary{Kind: data.WorkloadJob, State: data.WorkloadCompleted})
	}
	return rows
}

func pagedModel(t *testing.T, n int) (Model, *pagedHandle) {
	t.Helper()
	h := &pagedHandle{rows: jobRows(n), rev: 1, changes: make(chan struct{}, 1)}
	b := pagedBrowser{Demo: data.NewDemo(), snap: newFakeBrowser(nil, nil).snap, handle: h}
	return openLeaf(t, nodeModel(t, b, 160, 48), DestJobs), h
}

func selectedJob(m Model) string {
	for _, line := range strings.Split(m.Render().Plain(), "\n") {
		if _, rest, ok := strings.Cut(line, "› job-"); ok {
			return "job-" + rest[:4]
		}
	}
	return ""
}

func TestPagingReachesEveryObject(t *testing.T) {
	m, _ := pagedModel(t, 501)
	plain := m.Render().Plain()
	if !strings.Contains(plain, "1-500 de 501") {
		t.Fatal("the first page states the loaded range")
	}
	m = press(t, m, "end")
	if got := selectedJob(m); got != "job-0501" {
		t.Fatalf("end reaches the last object, got %q", got)
	}
	plain = m.Render().Plain()
	if !strings.Contains(plain, "job-0501") || !strings.Contains(plain, "501-501 de 501") {
		t.Fatal("the last page shows its object and its range")
	}
	m = press(t, m, "up")
	if got := selectedJob(m); got != "job-0500" {
		t.Fatalf("up crosses back to the previous page, got %q", got)
	}
	m = press(t, m, "down")
	if got := selectedJob(m); got != "job-0501" {
		t.Fatalf("down crosses forward, got %q", got)
	}
	m = press(t, m, "home")
	if got := selectedJob(m); got != "job-0001" {
		t.Fatalf("home returns to the first object, got %q", got)
	}
	for range 30 {
		m = press(t, m, "pgdown")
	}
	if got := selectedJob(m); got != "job-0501" {
		t.Fatalf("pgdown walks across the page boundary, got %q", got)
	}
}

func TestPagingClickUsesLoadedPage(t *testing.T) {
	m, _ := pagedModel(t, 600)
	m = press(t, m, "end", "home")
	m = press(t, m, "end")
	y := m.layout.Table.Y + 1 + 2
	next, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: m.layout.Table.X + 4, Y: y})
	m = next.(Model)
	got := selectedJob(m)
	if got == "" || got < "job-0501" {
		t.Fatalf("a click selects a row of the page on screen, got %q", got)
	}
}

func TestPagingSurvivesExpiredCursor(t *testing.T) {
	m, h := pagedModel(t, 700)
	h.set(func() { h.bumps = 1 })
	m = press(t, m, "end")
	if got := selectedJob(m); got != "job-0700" {
		t.Fatalf("one expired cursor restarts the walk and still arrives, got %q", got)
	}
	h.set(func() { h.expire = true })
	m = press(t, m, "home", "end")
	if got := selectedJob(m); got != "job-0001" {
		t.Fatalf("a cursor that keeps expiring falls back to the first page, got %q", got)
	}
	if !strings.Contains(m.Render().Plain(), "posição reiniciada") {
		t.Fatal("the fallback tells the position was restarted")
	}
	h.set(func() { h.expire = false })
	m = press(t, m, "end")
	if got := selectedJob(m); got != "job-0700" {
		t.Fatalf("navigation resumes after expiry, got %q", got)
	}
	if strings.Contains(m.Render().Plain(), "posição reiniciada") {
		t.Fatal("the restart notice leaves once a walk succeeds")
	}
}

func TestPagingChangeKeepsPositionAndClamps(t *testing.T) {
	h := &pagedHandle{rows: jobRows(700), rev: 1, changes: make(chan struct{}, 1)}
	b := pagedBrowser{Demo: data.NewDemo(), snap: newFakeBrowser(nil, nil).snap, handle: h}
	m, reply := pendingSubscription(t, nodeModel(t, b, 160, 48), DestJobs)
	next, wait := m.Update(reply)
	m = press(t, next.(Model), "end", "up")
	change := func() {
		t.Helper()
		h.changes <- struct{}{}
		msgs := drainMsgs(t, wait)
		if len(msgs) != 1 {
			t.Fatalf("expected one change, got %d", len(msgs))
		}
		after, more := m.Update(msgs[0])
		m, wait = after.(Model), more
	}
	h.set(func() { h.rev++ })
	change()
	if got := selectedJob(m); got != "job-0699" {
		t.Fatalf("a change keeps the loaded page, got %q", got)
	}
	h.set(func() { h.rows, h.rev = jobRows(3), h.rev+1 })
	change()
	if got := selectedJob(m); got != "job-0003" {
		t.Fatalf("a shrinking list clamps the selection, got %q", got)
	}
}

func TestReopeningLeafReturnsToFirstPage(t *testing.T) {
	m, h := pagedModel(t, 700)
	m = press(t, m, "end")
	m = openLeaf(t, m, DestWorkloadsOver)
	if h.cancels.Load() != 0 {
		t.Fatal("the overview keeps the subscription of the same key")
	}
	m = openLeaf(t, m, DestJobs)
	if got := selectedJob(m); got != "job-0001" {
		t.Fatalf("reopening the list starts at its first object, got %q", got)
	}
	if !strings.Contains(m.Render().Plain(), "1-500 de 700") {
		t.Fatal("the range follows the loaded page")
	}
}

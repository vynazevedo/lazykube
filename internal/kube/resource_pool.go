package kube

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
)

const (
	resourcePageSize          = 500
	resourceRowLimit          = 10000
	resourcePoolRows          = 30000
	resourceSubscriptionLimit = 12
	resourceWatchLimit        = 6
)

var errResourceScope = errors.New("resource scope or identity unavailable")

type resourcePool struct {
	mu        sync.Mutex
	ctx       context.Context
	client    dynamic.Interface
	source    *Source
	entries   map[data.ResourceSubscriptionKey]*resourceEntry
	lists     chan struct{}
	watches   int
	rows      int
	closed    bool
	wg        sync.WaitGroup
	now       func() time.Time
	retry     func(int) time.Duration
	rowLimit  int
	poolLimit int
}

type resourceEntry struct {
	pool       *resourcePool
	key        data.ResourceSubscriptionKey
	descriptor data.ResourceDescriptor
	ctx        context.Context
	cancel     context.CancelFunc
	handles    map[*resourceHandle]struct{}
	rows       map[string]data.ResourceRow
	revision   uint64
	observedAt time.Time
	coverage   data.Coverage
	truncated  bool
	borrowed   bool
	refresh    chan struct{}
}

type resourceHandle struct {
	entry   *resourceEntry
	changes chan struct{}
	once    sync.Once
	stop    func() bool
}

func newResourcePool(ctx context.Context, client dynamic.Interface, source *Source) *resourcePool {
	return &resourcePool{ctx: ctx, client: client, source: source, entries: make(map[data.ResourceSubscriptionKey]*resourceEntry), lists: make(chan struct{}, 2), now: source.opts.Now, rowLimit: resourceRowLimit, poolLimit: resourcePoolRows, retry: func(n int) time.Duration {
		return min(30*time.Second, time.Second*time.Duration(1<<min(n, 5))+time.Duration(rand.IntN(250))*time.Millisecond)
	}}
}

func (p *resourcePool) subscribe(ctx context.Context, key data.ResourceSubscriptionKey, d data.ResourceDescriptor) (data.ResourceSubscription, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if key.Session != p.source.resourceSession() || key.Generation != p.source.generation || key.ProjectionVersion != data.ResourceProjectionVersion || key.GVR != d.GVR || d.Availability != data.ResourceServed || strings.Contains(key.GVR.Resource, "/") {
		return nil, errResourceScope
	}
	if d.Namespaced {
		if p.source.opts.Namespace != "" && key.Namespace != p.source.opts.Namespace {
			return nil, errResourceScope
		}
	} else if key.Namespace != "" {
		return nil, errResourceScope
	}
	if key.GVR.Group == "" && key.GVR.Resource == "secrets" {
		return nil, errors.New("Secret metadata requires a separate opt-in source")
	}
	if len(key.LabelSelector) > 1024 || len(key.FieldSelector) > 1024 {
		return nil, errResourceScope
	}
	if _, err := labels.Parse(key.LabelSelector); err != nil {
		return nil, errResourceScope
	}
	fs, err := fields.ParseSelector(key.FieldSelector)
	if err != nil {
		return nil, errResourceScope
	}
	for _, r := range fs.Requirements() {
		if r.Field != "metadata.name" && r.Field != "metadata.namespace" {
			return nil, errResourceScope
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("resource pool closed")
	}
	e := p.entries[key]
	if e == nil {
		if len(p.entries) >= resourceSubscriptionLimit {
			return nil, errors.New("resource subscription budget exhausted")
		}
		entryCtx, cancel := context.WithCancel(p.ctx)
		e = &resourceEntry{pool: p, key: key, descriptor: d, ctx: entryCtx, cancel: cancel, handles: make(map[*resourceHandle]struct{}), rows: make(map[string]data.ResourceRow), coverage: data.Coverage{State: data.CoverageCollecting}, refresh: make(chan struct{}, 1), borrowed: p.source.borrowedResource(key.GVR)}
		p.entries[key] = e
		p.wg.Add(1)
		go func() { defer p.wg.Done(); e.run() }()
	}
	h := &resourceHandle{entry: e, changes: make(chan struct{}, 1)}
	e.handles[h] = struct{}{}
	h.stop = context.AfterFunc(ctx, h.Cancel)
	return h, nil
}

func (p *resourcePool) close() {
	p.mu.Lock()
	p.closed = true
	for _, e := range p.entries {
		e.cancel()
	}
	p.mu.Unlock()
	p.wg.Wait()
	p.mu.Lock()
	for _, e := range p.entries {
		e.rows = nil
		e.coverage = data.Coverage{State: data.CoverageUnavailable, Reason: "session closed"}
		e.changedLocked()
	}
	p.entries = make(map[data.ResourceSubscriptionKey]*resourceEntry)
	p.rows = 0
	p.mu.Unlock()
}

func (h *resourceHandle) Changes() <-chan struct{} { return h.changes }
func (h *resourceHandle) Cancel() {
	h.once.Do(func() {
		e := h.entry
		p := e.pool
		p.mu.Lock()
		defer p.mu.Unlock()
		if h.stop != nil {
			h.stop()
		}
		delete(e.handles, h)
		if len(e.handles) == 0 {
			e.cancel()
			p.rows -= len(e.rows)
			e.rows = nil
			delete(p.entries, e.key)
		}
	})
}

func (h *resourceHandle) Refresh() {
	select {
	case h.entry.refresh <- struct{}{}:
	default:
	}
}

func (h *resourceHandle) Page(cursor string, limit int) data.ResourcePage {
	e := h.entry
	p := e.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := e.handles[h]; !ok {
		return data.ResourcePage{Coverage: data.Coverage{State: data.CoverageUnavailable, Reason: "subscription canceled"}}
	}
	out := data.ResourcePage{Revision: e.revision, ObservedAt: e.observedAt, Coverage: e.coverage, Total: len(e.rows), Truncated: e.truncated, Borrowed: e.borrowed}
	offset := 0
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		valid := len(parts) == 2
		if valid {
			rev, err := strconv.ParseUint(parts[0], 10, 64)
			n, numErr := strconv.Atoi(parts[1])
			valid = err == nil && numErr == nil && rev == e.revision && n >= 0 && n <= len(e.rows)
			offset = n
		}
		if !valid {
			out.Coverage = data.Coverage{State: data.CoverageStale, Reason: "page cursor expired; restart from first page"}
			return out
		}
	}
	limit = min(max(limit, 1), resourcePageSize)
	keys := make([]string, 0, len(e.rows))
	for k := range e.rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	end := min(offset+limit, len(keys))
	out.Items = make([]data.ResourceRow, 0, end-offset)
	for _, k := range keys[offset:end] {
		out.Items = append(out.Items, e.rows[k])
	}
	if end < len(keys) {
		out.Continue = fmt.Sprintf("%d:%d", e.revision, end)
	}
	return out
}

func (e *resourceEntry) changedLocked() {
	e.revision++
	for h := range e.handles {
		select {
		case h.changes <- struct{}{}:
		default:
		}
	}
}

func (e *resourceEntry) state(state data.CoverageState, reason string) {
	p := e.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	e.coverage = data.Coverage{State: state, Reason: reason}
	e.changedLocked()
}

func (e *resourceEntry) clear() {
	p := e.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.ctx.Err() != nil {
		return
	}
	p.rows -= len(e.rows)
	e.rows = make(map[string]data.ResourceRow)
	e.truncated = false
	e.changedLocked()
}

func (e *resourceEntry) put(row data.ResourceRow, deleted bool) bool {
	p := e.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.ctx.Err() != nil {
		return false
	}
	if e.key.Namespace != "" && row.Ref.Namespace != e.key.Namespace {
		e.coverage = data.Coverage{State: data.CoveragePartial, Reason: "response outside requested scope"}
		e.truncated = true
		e.changedLocked()
		return false
	}
	key := row.Ref.Namespace + "/" + row.Ref.Name
	old, exists := e.rows[key]
	if deleted {
		if exists && old.Ref.UID == row.Ref.UID {
			delete(e.rows, key)
			p.rows--
			e.changedLocked()
		}
		return true
	}
	if !exists && (len(e.rows) >= p.rowLimit || p.rows >= p.poolLimit) {
		e.truncated = true
		e.coverage = data.Coverage{State: data.CoveragePartial, Reason: "resource store budget exhausted"}
		e.changedLocked()
		return false
	}
	if exists && old == row {
		return true
	}
	if !exists {
		p.rows++
	}
	e.rows[key] = row
	e.observedAt = p.now()
	e.changedLocked()
	return true
}

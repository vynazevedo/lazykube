package app

import (
	"context"
	"maps"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
)

const (
	resourcePageLimit    = 500
	resourceSeekAttempts = 3
	catalogTimeout       = 15 * time.Second
)

type workloadLeaf struct {
	dest  DestinationID
	gvr   schema.GroupVersionResource
	kind  data.WorkloadKind
	label string
	net   data.NetworkKind
	scope leafScope
	field string
}

type leafScope struct {
	cluster   bool
	fixed     bool
	namespace string
}

var workloadLeaves = []workloadLeaf{
	{dest: DestPods, gvr: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, kind: data.WorkloadPod, label: "Pods"},
	{dest: DestDeployments, gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, kind: data.WorkloadDeployment, label: "Deployments"},
	{dest: DestStatefulSets, gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, kind: data.WorkloadStatefulSet, label: "StatefulSets"},
	{dest: DestDaemonSets, gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, kind: data.WorkloadDaemonSet, label: "DaemonSets"},
	{dest: DestJobs, gvr: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, kind: data.WorkloadJob, label: "Jobs"},
	{dest: DestCronJobs, gvr: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, kind: data.WorkloadCronJob, label: "CronJobs"},
	{dest: DestReplicaSets, gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, kind: data.WorkloadReplicaSet, label: "ReplicaSets"},
}

func workloadLeafFor(id DestinationID) (workloadLeaf, bool) {
	for _, leaves := range [][]workloadLeaf{workloadLeaves, networkLeaves} {
		for _, w := range leaves {
			if w.dest == id {
				return w, true
			}
		}
	}
	return workloadLeaf{}, false
}

type resourceSub struct {
	key       data.ResourceSubscriptionKey
	seq       uint64
	served    bool
	handle    data.ResourceSubscription
	cancel    context.CancelFunc
	done      <-chan struct{}
	page      data.ResourcePage
	offset    int
	restarted bool
	err       string
	loading   bool
}

type resourceState struct {
	catalog        *data.ResourceCatalogSnapshot
	catalogLoading bool
	catalogSeq     uint64
	catalogCancel  context.CancelFunc
	subs           map[schema.GroupVersionResource]*resourceSub
	seq            uint64
	cursor         int
	card           int
	opened         data.ResourceRow
}

type resourceCatalogMsg struct {
	session uint64
	seq     uint64
	snap    data.ResourceCatalogSnapshot
}

type resourceTag struct {
	session uint64
	seq     uint64
	key     data.ResourceSubscriptionKey
}

type resourceSubMsg struct {
	tag    resourceTag
	handle data.ResourceSubscription
	err    error
}

type resourceChangeMsg struct {
	tag resourceTag
}

func (m Model) browser() (data.ResourceBrowser, bool) {
	b, ok := m.src.(data.ResourceBrowser)
	return b, ok
}

func (m Model) wantedResources() []workloadLeaf {
	switch {
	case m.onScreen(ScreenResources):
		if w, ok := workloadLeafFor(m.nav.Active().Dest); ok {
			return []workloadLeaf{w}
		}
	case m.onScreen(ScreenOverview):
		return workloadLeaves
	case m.onScreen(ScreenResourceDetail):
		if w, ok := workloadLeafFor(m.nav.Active().Dest); ok && w.net != "" {
			return []workloadLeaf{w}
		}
	}
	return nil
}

func (m Model) resourceNamespace() string {
	if ns := m.snap.Cluster.Namespace; ns != "" {
		return ns
	}
	return m.scopeNamespace()
}

func (m Model) resourceKey(w workloadLeaf) data.ResourceSubscriptionKey {
	c := m.res.catalog
	key := data.ResourceSubscriptionKey{Session: c.Session, Generation: c.Generation, GVR: w.gvr, Namespace: m.resourceNamespace(), ProjectionVersion: data.ResourceProjectionVersion}
	if w.net != "" || w.scope.fixed {
		key.ProjectionVersion = data.ResourceNetworkProjection
	}
	if w.scope.fixed {
		key.Namespace = w.scope.namespace
	}
	if w.scope.cluster {
		key.Namespace = ""
	}
	if w.field != "" {
		key.FieldSelector = "metadata.name=" + w.field
	}
	return key
}

func (s *resourceSub) release() {
	if s.handle != nil {
		s.handle.Cancel()
	}
	if s.cancel != nil {
		s.cancel()
	}
}

func (m *Model) releaseResources() {
	for _, subs := range []map[schema.GroupVersionResource]*resourceSub{m.res.subs, m.acc.subs} {
		for _, s := range subs {
			s.release()
		}
	}
	m.res.subs = nil
	m.acc = m.acc.closed()
	if m.res.catalogLoading {
		m.cancelCatalog()
	}
}

func (m *Model) cancelCatalog() {
	if m.res.catalogCancel != nil {
		m.res.catalogCancel()
	}
	m.res.catalogCancel, m.res.catalogLoading = nil, false
	m.res.catalogSeq++
}

func (m *Model) loadCatalog(b data.ResourceBrowser, refresh bool) tea.Cmd {
	m.cancelCatalog()
	ctx, cancel := context.WithTimeout(m.life, catalogTimeout)
	m.res.catalogLoading, m.res.catalogCancel = true, cancel
	session, seq := m.session.Generation, m.res.catalogSeq
	return func() tea.Msg {
		return resourceCatalogMsg{session: session, seq: seq, snap: b.Catalog(ctx, refresh)}
	}
}

func (m *Model) syncResources() tea.Cmd {
	b, ok := m.browser()
	want := m.wantedResources()
	graph := m.followAccess()
	if !ok || len(want)+len(graph) == 0 {
		if len(m.res.subs)+len(m.acc.subs) > 0 || m.res.catalogLoading {
			m.releaseResources()
		}
		return nil
	}
	if m.res.catalog == nil {
		if m.res.catalogLoading {
			return nil
		}
		return m.loadCatalog(b, false)
	}
	var lists, graphs []tea.Cmd
	var changed bool
	m.res.subs, lists, _ = m.syncSubs(b, m.res.subs, want)
	m.acc.subs, graphs, changed = m.syncSubs(b, m.acc.subs, graph)
	if changed {
		m.acc.dirty = true
	}
	m.seekTable(m.res.cursor)
	return tea.Batch(append(append(lists, graphs...), m.computeAccess())...)
}

func (m *Model) syncSubs(b data.ResourceBrowser, current map[schema.GroupVersionResource]*resourceSub, want []workloadLeaf) (map[schema.GroupVersionResource]*resourceSub, []tea.Cmd, bool) {
	subs := maps.Clone(current)
	if subs == nil {
		subs = map[schema.GroupVersionResource]*resourceSub{}
	}
	var cmds []tea.Cmd
	changed := false
	for gvr, s := range subs {
		served := m.res.catalog.Lookup(gvr).Availability == data.ResourceServed
		keep := s.served == served && slices.ContainsFunc(want, func(w workloadLeaf) bool { return w.gvr == gvr && m.resourceKey(w) == s.key })
		if !keep {
			s.release()
			delete(subs, gvr)
			changed = true
		}
	}
	for _, w := range want {
		if _, ok := subs[w.gvr]; ok {
			continue
		}
		changed = true
		m.res.seq++
		key := m.resourceKey(w)
		tag := resourceTag{session: m.session.Generation, seq: m.res.seq, key: key}
		served := m.res.catalog.Lookup(w.gvr).Availability == data.ResourceServed
		ctx, cancel := context.WithCancel(m.life)
		subs[w.gvr] = &resourceSub{key: key, seq: tag.seq, served: served, cancel: cancel, done: ctx.Done(), loading: served}
		if !served {
			continue
		}
		cmds = append(cmds, func() tea.Msg {
			h, err := b.Subscribe(ctx, key)
			return resourceSubMsg{tag: tag, handle: h, err: err}
		})
	}
	return subs, cmds, changed
}

func waitResource(h data.ResourceSubscription, done <-chan struct{}, tag resourceTag) tea.Cmd {
	ch := h.Changes()
	return func() tea.Msg {
		select {
		case <-done:
			return nil
		case _, ok := <-ch:
			if !ok {
				return nil
			}
			return resourceChangeMsg{tag: tag}
		}
	}
}

func (m *Model) currentSub(tag resourceTag) *resourceSub {
	return matchSub(m.res.subs, tag, m.session.Generation)
}

func matchSub(subs map[schema.GroupVersionResource]*resourceSub, tag resourceTag, session uint64) *resourceSub {
	s, ok := subs[tag.key.GVR]
	if !ok || tag.session != session || s.seq != tag.seq || s.key != tag.key {
		return nil
	}
	return s
}

func walkPages(h data.ResourceSubscription, abs int) (data.ResourcePage, int, bool) {
	page, offset := h.Page("", resourcePageLimit), 0
	for abs >= offset+len(page.Items) && page.Continue != "" {
		next := h.Page(page.Continue, resourcePageLimit)
		if len(next.Items) == 0 {
			return page, offset, false
		}
		offset += len(page.Items)
		page = next
	}
	return page, offset, true
}

func seekPage(h data.ResourceSubscription, abs int) (data.ResourcePage, int, bool) {
	for range resourceSeekAttempts {
		if page, offset, ok := walkPages(h, abs); ok {
			return page, offset, false
		}
	}
	return h.Page("", resourcePageLimit), 0, true
}

func (m Model) tableLeaf() (workloadLeaf, bool) {
	if !m.onScreen(ScreenResources) && !m.onScreen(ScreenResourceDetail) {
		return workloadLeaf{}, false
	}
	return workloadLeafFor(m.nav.Active().Dest)
}

func (m *Model) readTable(h data.ResourceSubscription, abs int) (data.ResourcePage, int, bool) {
	q := m.filter.Value()
	if q == "" {
		return seekPage(h, abs)
	}
	if s, ok := h.(data.ResourceSearcher); ok {
		return s.Search(q, resourcePageLimit), 0, false
	}
	page := h.Page("", resourcePageLimit)
	items := page.Items
	page.Items, page.Filtered = nil, true
	for _, row := range items {
		if row.Matches(q) {
			page.Items = append(page.Items, row)
		}
	}
	page.Matched = len(page.Items)
	page.Truncated = page.Truncated || page.Continue != ""
	page.Continue = ""
	return page, 0, false
}

func (m *Model) reloadTable() {
	m.res.cursor = 0
	w, ok := m.tableLeaf()
	if !ok {
		return
	}
	s, ok := m.res.subs[w.gvr]
	if !ok || s.handle == nil {
		return
	}
	next := *s
	next.page, next.offset, next.restarted = m.readTable(s.handle, 0)
	m.storeSub(w.gvr, next)
}

func (m *Model) storeSub(gvr schema.GroupVersionResource, s resourceSub) {
	m.res.subs = maps.Clone(m.res.subs)
	m.res.subs[gvr] = &s
}

func clampToPage(abs int, s resourceSub) int {
	return max(0, min(abs, s.offset+len(s.page.Items)-1))
}

func (m *Model) seekTable(abs int) {
	w, ok := m.tableLeaf()
	if !ok {
		return
	}
	s, ok := m.res.subs[w.gvr]
	if !ok || s.handle == nil {
		return
	}
	abs = max(0, abs)
	inWindow := abs >= s.offset && abs < s.offset+len(s.page.Items)
	if inWindow || (abs == s.offset && len(s.page.Items) == 0) {
		m.res.cursor = abs
		return
	}
	if m.filter.Value() != "" {
		m.res.cursor = clampToPage(abs, *s)
		return
	}
	next := *s
	next.page, next.offset, next.restarted = seekPage(s.handle, abs)
	m.storeSub(w.gvr, next)
	m.res.cursor = clampToPage(abs, next)
	if next.restarted {
		m.res.cursor = 0
	}
}

func (m Model) handleResourceMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case accessMsg:
		m.storeAccess(msg)
		return m, nil, true
	case resourceCatalogMsg:
		if msg.session != m.session.Generation || msg.seq != m.res.catalogSeq {
			return m, nil, true
		}
		if m.res.catalogCancel != nil {
			m.res.catalogCancel()
		}
		snap := msg.snap
		m.res.catalog, m.res.catalogLoading, m.res.catalogCancel = &snap, false, nil
		m.tree.gateway = snap.Lookup(httpRouteGVR).Availability == data.ResourceServed
		m.res.subs, m.acc.subs = dropFailed(m.res.subs), dropFailed(m.acc.subs)
		m.acc.dirty = true
		return m, nil, true
	case resourceSubMsg:
		if next, cmd, ok := m.openedAccess(msg); ok {
			return next, cmd, true
		}
		s := m.currentSub(msg.tag)
		if s == nil {
			if msg.handle != nil {
				msg.handle.Cancel()
			}
			return m, nil, true
		}
		next := *s
		next.loading = false
		gvr := msg.tag.key.GVR
		if msg.err != nil || msg.handle == nil {
			next.err = "assinatura indisponível"
			m.storeSub(gvr, next)
			return m, nil, true
		}
		next.handle = msg.handle
		next.page, next.offset, _ = m.readTable(msg.handle, 0)
		m.storeSub(gvr, next)
		return m, waitResource(msg.handle, next.done, msg.tag), true
	case resourceChangeMsg:
		if s := matchSub(m.acc.subs, msg.tag, m.session.Generation); s != nil && s.handle != nil {
			m.acc.dirty = true
			return m, waitResource(s.handle, s.done, msg.tag), true
		}
		s := m.currentSub(msg.tag)
		if s == nil || s.handle == nil {
			return m, nil, true
		}
		gvr := msg.tag.key.GVR
		w, active := m.tableLeaf()
		active = active && w.gvr == gvr
		abs := 0
		if active {
			abs = m.res.cursor
		}
		next := *s
		next.page, next.offset, next.restarted = m.readTable(s.handle, abs)
		m.storeSub(gvr, next)
		if active {
			m.res.cursor = clampToPage(abs, next)
			if next.restarted {
				m.res.cursor = 0
			}
		}
		return m, waitResource(s.handle, s.done, msg.tag), true
	}
	return m, nil, false
}

func (m *Model) refreshResources() tea.Cmd {
	b, ok := m.browser()
	want := m.wantedResources()
	if !ok || len(want)+len(m.acc.subs) == 0 {
		return nil
	}
	for _, s := range m.acc.subs {
		if s.handle != nil {
			s.handle.Refresh()
		}
	}
	for _, w := range want {
		if s, ok := m.res.subs[w.gvr]; ok && s.handle != nil {
			s.handle.Refresh()
		}
	}
	if m.res.catalogLoading {
		return nil
	}
	return m.loadCatalog(b, true)
}

func dropFailed(in map[schema.GroupVersionResource]*resourceSub) map[schema.GroupVersionResource]*resourceSub {
	subs := maps.Clone(in)
	for gvr, s := range subs {
		if s.err != "" {
			s.release()
			delete(subs, gvr)
		}
	}
	return subs
}

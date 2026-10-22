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
	resourcePageLimit = 500
	catalogTimeout    = 15 * time.Second
)

type workloadLeaf struct {
	dest  DestinationID
	gvr   schema.GroupVersionResource
	kind  data.WorkloadKind
	label string
}

var workloadLeaves = []workloadLeaf{
	{DestPods, schema.GroupVersionResource{Version: "v1", Resource: "pods"}, data.WorkloadPod, "Pods"},
	{DestDeployments, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, data.WorkloadDeployment, "Deployments"},
	{DestStatefulSets, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, data.WorkloadStatefulSet, "StatefulSets"},
	{DestDaemonSets, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, data.WorkloadDaemonSet, "DaemonSets"},
	{DestJobs, schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, data.WorkloadJob, "Jobs"},
	{DestCronJobs, schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, data.WorkloadCronJob, "CronJobs"},
	{DestReplicaSets, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, data.WorkloadReplicaSet, "ReplicaSets"},
}

func workloadLeafFor(id DestinationID) (workloadLeaf, bool) {
	for _, w := range workloadLeaves {
		if w.dest == id {
			return w, true
		}
	}
	return workloadLeaf{}, false
}

type resourceSub struct {
	key     data.ResourceSubscriptionKey
	seq     uint64
	handle  data.ResourceSubscription
	cancel  context.CancelFunc
	page    data.ResourcePage
	err     string
	loading bool
}

type resourceState struct {
	catalog        *data.ResourceCatalogSnapshot
	catalogLoading bool
	subs           map[schema.GroupVersionResource]*resourceSub
	seq            uint64
	cursor         int
	card           int
}

type resourceCatalogMsg struct {
	session uint64
	snap    data.ResourceCatalogSnapshot
}

type resourceSubMsg struct {
	seq    uint64
	gvr    schema.GroupVersionResource
	handle data.ResourceSubscription
	err    error
}

type resourceChangeMsg struct {
	seq uint64
	gvr schema.GroupVersionResource
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
	return data.ResourceSubscriptionKey{Session: c.Session, Generation: c.Generation, GVR: w.gvr, Namespace: m.resourceNamespace(), ProjectionVersion: data.ResourceProjectionVersion}
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
	for _, s := range m.res.subs {
		s.release()
	}
	m.res.subs = nil
}

func (m *Model) syncResources() tea.Cmd {
	b, ok := m.browser()
	want := m.wantedResources()
	if !ok || len(want) == 0 {
		if len(m.res.subs) > 0 {
			m.releaseResources()
		}
		return nil
	}
	if m.res.catalog == nil {
		if m.res.catalogLoading {
			return nil
		}
		m.res.catalogLoading = true
		session := m.session.Generation
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
			defer cancel()
			return resourceCatalogMsg{session: session, snap: b.Catalog(ctx, false)}
		}
	}
	subs := maps.Clone(m.res.subs)
	if subs == nil {
		subs = map[schema.GroupVersionResource]*resourceSub{}
	}
	var cmds []tea.Cmd
	for gvr, s := range subs {
		keep := slices.ContainsFunc(want, func(w workloadLeaf) bool { return w.gvr == gvr && m.resourceKey(w) == s.key })
		if !keep {
			s.release()
			delete(subs, gvr)
		}
	}
	for _, w := range want {
		if _, ok := subs[w.gvr]; ok {
			continue
		}
		m.res.seq++
		key, seq, gvr := m.resourceKey(w), m.res.seq, w.gvr
		ctx, cancel := context.WithCancel(context.Background())
		subs[gvr] = &resourceSub{key: key, seq: seq, cancel: cancel, loading: true}
		if m.res.catalog.Lookup(gvr).Availability != data.ResourceServed {
			continue
		}
		cmds = append(cmds, func() tea.Msg {
			h, err := b.Subscribe(ctx, key)
			return resourceSubMsg{seq: seq, gvr: gvr, handle: h, err: err}
		})
	}
	m.res.subs = subs
	return tea.Batch(cmds...)
}

func waitResource(h data.ResourceSubscription, seq uint64, gvr schema.GroupVersionResource) tea.Cmd {
	ch := h.Changes()
	return func() tea.Msg {
		if _, ok := <-ch; !ok {
			return nil
		}
		return resourceChangeMsg{seq: seq, gvr: gvr}
	}
}

func (m *Model) currentSub(seq uint64, gvr schema.GroupVersionResource) *resourceSub {
	s, ok := m.res.subs[gvr]
	if !ok || s.seq != seq {
		return nil
	}
	return s
}

func (m Model) handleResourceMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case resourceCatalogMsg:
		if msg.session != m.session.Generation {
			return m, nil, true
		}
		snap := msg.snap
		m.res.catalog, m.res.catalogLoading = &snap, false
		return m, nil, true
	case resourceSubMsg:
		s := m.currentSub(msg.seq, msg.gvr)
		if s == nil {
			if msg.handle != nil {
				msg.handle.Cancel()
			}
			return m, nil, true
		}
		next := *s
		next.loading = false
		if msg.err != nil || msg.handle == nil {
			next.err = "assinatura indisponível"
			m.res.subs = maps.Clone(m.res.subs)
			m.res.subs[msg.gvr] = &next
			return m, nil, true
		}
		next.handle, next.page = msg.handle, msg.handle.Page("", resourcePageLimit)
		m.res.subs = maps.Clone(m.res.subs)
		m.res.subs[msg.gvr] = &next
		return m, waitResource(msg.handle, msg.seq, msg.gvr), true
	case resourceChangeMsg:
		s := m.currentSub(msg.seq, msg.gvr)
		if s == nil || s.handle == nil {
			return m, nil, true
		}
		next := *s
		next.page = s.handle.Page("", resourcePageLimit)
		m.res.subs = maps.Clone(m.res.subs)
		m.res.subs[msg.gvr] = &next
		m.res.cursor = max(0, min(m.res.cursor, len(next.page.Items)-1))
		return m, waitResource(s.handle, msg.seq, msg.gvr), true
	}
	return m, nil, false
}

func (m Model) refreshResource() {
	for _, w := range m.wantedResources() {
		if s, ok := m.res.subs[w.gvr]; ok && s.handle != nil {
			s.handle.Refresh()
		}
	}
}

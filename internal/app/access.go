package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/netpath"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const (
	graphRowLimit = 5000
	graphAttempts = 3
)

type accessTarget struct {
	active    bool
	kind      netpath.TargetKind
	gvr       schema.GroupVersionResource
	namespace string
	name      string
	uid       string
}

type accessView struct {
	report  netpath.Report
	row     *data.ResourceRow
	loading bool
	gone    bool
}

type accessState struct {
	target    accessTarget
	subs      map[schema.GroupVersionResource]*resourceSub
	seq       uint64
	computing bool
	dirty     bool
	view      *accessView
	selected  int
	scroll    int
}

type accessMsg struct {
	session uint64
	seq     uint64
	view    accessView
}

type graphSource struct {
	handle  data.ResourceSubscription
	state   netpath.SetState
	loading bool
}

type accessRequest struct {
	target  accessTarget
	sources map[schema.GroupVersionResource]graphSource
}

func (a accessState) closed() accessState {
	return accessState{seq: a.seq + 1}
}

func (m Model) accessTarget() accessTarget {
	r := m.nav.Active()
	if r.Kind == RoutePod && m.tab == ui.TabDescribe && r.Ref.Name != "" {
		return accessTarget{active: true, kind: netpath.TargetPod, gvr: podGVR, namespace: r.Ref.Namespace, name: r.Ref.Name}
	}
	w, ref, ok := m.activeResource()
	if !ok {
		return accessTarget{}
	}
	t := accessTarget{active: true, gvr: w.gvr, namespace: ref.Namespace, name: ref.Name, uid: ref.UID}
	switch w.net {
	case data.NetworkService:
		t.kind = netpath.TargetService
	case data.NetworkIngress:
		t.kind = netpath.TargetIngress
	case "":
		t.kind = netpath.TargetWorkload
	default:
		return accessTarget{}
	}
	return t
}

func graphLeaves(t accessTarget) []workloadLeaf {
	in := func(gvr schema.GroupVersionResource) workloadLeaf {
		return workloadLeaf{gvr: gvr, scope: leafScope{fixed: true, namespace: t.namespace}}
	}
	wide := func(gvr schema.GroupVersionResource) workloadLeaf {
		return workloadLeaf{gvr: gvr, scope: leafScope{fixed: true, cluster: true}}
	}
	out := []workloadLeaf{in(serviceGVR), in(endpointSliceGVR), in(ingressGVR), in(podGVR), wide(ingressClassGVR)}
	if t.kind != netpath.TargetIngress {
		out = append(out, wide(nodeGVR))
	}
	if t.kind == netpath.TargetPod || t.kind == netpath.TargetWorkload {
		out = append(out, in(netPolicyGVR))
	}
	if t.kind == netpath.TargetWorkload {
		own := in(t.gvr)
		own.field = t.name
		out = append(out, own)
	}
	return out
}

func (m *Model) followAccess() []workloadLeaf {
	t := m.accessTarget()
	if !t.active {
		if m.acc.target.active || len(m.acc.subs) > 0 {
			for _, s := range m.acc.subs {
				s.release()
			}
			m.acc = m.acc.closed()
		}
		return nil
	}
	if t != m.acc.target {
		subs, seq := m.acc.subs, m.acc.seq+1
		m.acc = accessState{target: t, subs: subs, seq: seq, dirty: true}
	}
	return graphLeaves(t)
}

func (m Model) openedAccess(msg resourceSubMsg) (Model, tea.Cmd, bool) {
	s := matchSub(m.acc.subs, msg.tag, m.session.Generation)
	if s == nil {
		return m, nil, false
	}
	next := *s
	next.loading = false
	m.acc.dirty = true
	if msg.err != nil || msg.handle == nil {
		next.err = "assinatura indisponível"
	} else {
		next.handle = msg.handle
	}
	subs := make(map[schema.GroupVersionResource]*resourceSub, len(m.acc.subs))
	for gvr, old := range m.acc.subs {
		subs[gvr] = old
	}
	subs[msg.tag.key.GVR] = &next
	m.acc.subs = subs
	if next.handle == nil {
		return m, nil, true
	}
	return m, waitResource(next.handle, next.done, msg.tag), true
}

func (m *Model) computeAccess() tea.Cmd {
	a := &m.acc
	if !a.target.active || !a.dirty || a.computing || m.res.catalog == nil {
		return nil
	}
	a.dirty, a.computing = false, true
	a.seq++
	req := accessRequest{target: a.target, sources: make(map[schema.GroupVersionResource]graphSource, len(a.subs))}
	for gvr, s := range a.subs {
		req.sources[gvr] = m.graphSource(gvr, s)
	}
	session, seq := m.session.Generation, a.seq
	return func() tea.Msg { return accessMsg{session: session, seq: seq, view: req.run()} }
}

func (m Model) graphSource(gvr schema.GroupVersionResource, s *resourceSub) graphSource {
	if s.handle != nil {
		return graphSource{handle: s.handle}
	}
	if s.loading {
		return graphSource{loading: true}
	}
	switch m.res.catalog.Lookup(gvr).Availability {
	case data.ResourceAbsent:
		return graphSource{state: netpath.SetAbsent}
	case data.ResourceDenied:
		return graphSource{state: netpath.SetDenied}
	}
	return graphSource{}
}

func (m *Model) storeAccess(msg accessMsg) {
	if msg.session != m.session.Generation || msg.seq != m.acc.seq || !m.acc.target.active {
		return
	}
	m.acc.computing = false
	view := msg.view
	m.acc.view = &view
	m.acc.selected = max(0, min(m.acc.selected, len(view.report.Addresses)-1))
	m.clampAccess()
}

func (s graphSource) read() (netpath.Set, data.Coverage, bool) {
	if s.handle == nil {
		return netpath.Set{State: s.state}, data.Coverage{}, s.loading
	}
	for range graphAttempts {
		page := s.handle.Page("", resourcePageLimit)
		coverage, truncated := page.Coverage, page.Truncated
		rows := slices.Clone(page.Items)
		moved := false
		for page.Continue != "" && len(rows) < graphRowLimit {
			page = s.handle.Page(page.Continue, resourcePageLimit)
			if len(page.Items) == 0 {
				moved = true
				break
			}
			rows = append(rows, page.Items...)
		}
		if moved {
			continue
		}
		set := netpath.Set{Rows: rows}
		switch coverage.State {
		case data.CoverageComplete, data.CoverageListOnly:
			if !truncated && page.Continue == "" {
				set.State = netpath.SetKnown
			}
		case data.CoverageDenied:
			set.State = netpath.SetDenied
		}
		return set, coverage, coverage.State == data.CoverageCollecting
	}
	return netpath.Set{}, data.Coverage{}, false
}

func (r accessRequest) run() accessView {
	var view accessView
	var listOnly []string
	set := func(gvr schema.GroupVersionResource) netpath.Set {
		src, ok := r.sources[gvr]
		if !ok {
			return netpath.Set{}
		}
		out, coverage, loading := src.read()
		view.loading = view.loading || loading
		if coverage.State == data.CoverageListOnly {
			listOnly = append(listOnly, gvr.Resource)
		}
		return out
	}
	t := r.target
	target := netpath.Target{Kind: t.kind, Namespace: t.namespace, Name: t.name}
	g := netpath.Graph{Services: set(serviceGVR), Slices: set(endpointSliceGVR), Ingresses: set(ingressGVR), Classes: set(ingressClassGVR), Policies: set(netPolicyGVR), Pods: set(podGVR), Nodes: set(nodeGVR)}
	var own netpath.Set
	switch t.kind {
	case netpath.TargetWorkload:
		own = set(t.gvr)
	case netpath.TargetService:
		own = g.Services
	case netpath.TargetIngress:
		own = g.Ingresses
	default:
		own = g.Pods
	}
	for _, row := range own.Rows {
		if row.Ref.Namespace == t.namespace && row.Ref.Name == t.name && (t.uid == "" || row.Ref.UID == t.uid) {
			view.row = &row
		}
	}
	view.gone = view.row == nil && own.State == netpath.SetKnown
	if t.kind == netpath.TargetWorkload && view.row != nil && view.row.Network != nil && view.row.Network.Template != nil && !view.row.Network.Truncated {
		target.Labels, target.LabelsKnown = view.row.Network.Template.Labels, true
	}
	if view.gone {
		return view
	}
	view.report = netpath.Access(g, target)
	for _, name := range listOnly {
		view.report.Notes = append(view.report.Notes, name+": leitura pontual, sem acompanhamento; r recarrega")
	}
	return view
}

func (m Model) accessView() *ui.AccessView {
	if !m.acc.target.active {
		return nil
	}
	out := &ui.AccessView{Loading: true, Selected: m.acc.selected}
	if _, ok := m.browser(); !ok {
		out.Loading, out.Err = false, "esta fonte não oferece o catálogo de recursos"
		return out
	}
	if v := m.acc.view; v != nil {
		out.Loading, out.Report, out.Gone = v.loading, &v.report, v.gone
	}
	return out
}

func (m Model) accessAddresses() []netpath.Address {
	if m.acc.view == nil || !m.acc.target.active {
		return nil
	}
	return m.acc.view.report.Addresses
}

func (m *Model) nextURL() {
	list := m.accessAddresses()
	if len(list) == 0 {
		return
	}
	m.acc.selected = (m.acc.selected + 1) % len(list)
	if m.inDetail() {
		rows := ui.DescribeRows(m.diagnosis(), m.describe)
		m.scrollTo(ui.ScrollToSelection(rows, m.scroll, ui.ScrollRows(ui.TabDescribe, m.layout.Details.H)))
		return
	}
	m.acc.scroll = ui.ScrollToSelection(m.resourceDetailState().Rows(), m.acc.scroll, ui.ResourceDetailRows(m.layout.Table.H))
	m.clampAccess()
}

func (m *Model) clampAccess() {
	if m.nav.Active().Kind != RouteResource {
		m.clampScroll()
		return
	}
	m.acc.scroll = max(0, min(m.acc.scroll, len(m.resourceDetailState().Rows())-ui.ResourceDetailRows(m.layout.Table.H)))
}

func (m Model) yankAddress() (yank, string, bool) {
	list := m.accessAddresses()
	if len(list) == 0 || m.acc.selected >= len(list) {
		return yank{}, "", false
	}
	a := list[m.acc.selected]
	if a.URL.IsZero() {
		return yank{}, "sem URL copiável: " + a.Refused, true
	}
	label := a.URL.String()
	if !a.URL.Linkable() {
		label += " (só dentro do cluster)"
	}
	return yank{label: label, text: a.URL.String()}, "", true
}

func (m Model) diagnosis() ui.Diagnosis {
	return ui.Diagnosis{Containers: m.dg.containers, LogContainer: m.dg.logView.Container, Access: m.podAccess()}
}

func (m Model) podAccess() *ui.AccessView {
	if !m.inDetail() {
		return nil
	}
	return m.accessView()
}

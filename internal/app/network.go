package app

import (
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
)

var (
	podGVR           = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	nodeGVR          = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	serviceGVR       = schema.GroupVersionResource{Version: "v1", Resource: "services"}
	endpointSliceGVR = schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}
	ingressGVR       = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	ingressClassGVR  = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingressclasses"}
	netPolicyGVR     = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}
	httpRouteGVR     = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}
)

var networkLeaves = []workloadLeaf{
	{dest: DestServices, gvr: serviceGVR, net: data.NetworkService, label: "Services"},
	{dest: DestEndpointSlices, gvr: endpointSliceGVR, net: data.NetworkSlice, label: "EndpointSlices"},
	{dest: DestIngresses, gvr: ingressGVR, net: data.NetworkIngress, label: "Ingresses"},
	{dest: DestIngressClasses, gvr: ingressClassGVR, net: data.NetworkIngressClass, label: "IngressClasses", scope: leafScope{cluster: true}},
	{dest: DestNetPolicies, gvr: netPolicyGVR, net: data.NetworkPolicy, label: "NetworkPolicies"},
	{dest: DestHTTPRoutes, gvr: httpRouteGVR, net: data.NetworkHTTPRoute, label: "HTTPRoutes"},
}

func (m Model) onTables() bool {
	return m.onScreen(ScreenResources) || m.onScreen(ScreenOverview) || m.onScreen(ScreenResourceDetail)
}

func (m Model) activeResource() (workloadLeaf, ResourceRef, bool) {
	r := m.nav.Active()
	if r.Kind != RouteResource {
		return workloadLeaf{}, ResourceRef{}, false
	}
	w, ok := workloadLeafFor(r.Dest)
	return w, r.Ref, ok
}

func (m *Model) openResource() {
	w, ok := m.tableLeaf()
	st := m.tableState()
	if !ok || m.searchPending() || st.Cursor < 0 || st.Cursor >= len(st.Page.Items) {
		return
	}
	row := st.Page.Items[st.Cursor]
	m.openResourceRef(w, row)
}

func (m *Model) openResourceRef(w workloadLeaf, row data.ResourceRow) {
	ref := ResourceRef{Group: w.gvr.Group, Version: w.gvr.Version, Resource: w.gvr.Resource, Namespace: row.Ref.Namespace, Name: row.Ref.Name, UID: row.Ref.UID}
	m.endEditing()
	m.res.opened = row
	m.navigate(Route{Kind: RouteResource, Screen: ScreenResourceDetail, Dest: w.dest, Ref: ref})
}

func (m Model) openedRow(w workloadLeaf, ref ResourceRef) (data.ResourceRow, bool) {
	same := func(r data.ResourceRow) bool {
		return r.Ref.Namespace == ref.Namespace && r.Ref.Name == ref.Name && (ref.UID == "" || r.Ref.UID == ref.UID)
	}
	if s, ok := m.res.subs[w.gvr]; ok {
		for _, r := range s.page.Items {
			if same(r) {
				return r, true
			}
		}
	}
	if v := m.acc.view; v != nil && v.row != nil && same(*v.row) {
		return *v.row, true
	}
	return m.res.opened, same(m.res.opened)
}

package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/goleak"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/ui"
)

var graphGVRs = []schema.GroupVersionResource{serviceGVR, endpointSliceGVR, ingressGVR, ingressClassGVR, netPolicyGVR, nodeGVR}

func newNetBrowser(avail map[string]data.ResourceAvailability, pages map[schema.GroupVersionResource]data.ResourcePage) fakeBrowser {
	b := newFakeBrowser(avail, pages)
	for _, gvr := range append(slices.Clone(graphGVRs), httpRouteGVR) {
		a, ok := avail[gvr.Resource]
		if !ok {
			a = data.ResourceServed
		}
		if a == data.ResourceAbsent {
			continue
		}
		b.snap.Resources = append(b.snap.Resources, data.ResourceDescriptor{GVR: gvr, Namespaced: gvr != nodeGVR && gvr != ingressClassGVR, List: true, Watch: true, Availability: a})
	}
	return b
}

func netRow(ns, name string, f data.NetworkFacts) data.ResourceRow {
	return data.ResourceRow{Ref: data.ResourceMetadataRef{Namespace: ns, Name: name, UID: "uid-" + name, ResourceVersion: "1", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, Network: &f}
}

func netPage(rows ...data.ResourceRow) data.ResourcePage {
	return data.ResourcePage{Items: rows, Total: len(rows), Revision: 1, ObservedAt: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Coverage: data.Coverage{State: data.CoverageComplete}}
}

func pressDrive(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	m.after = instantTimer
	for _, k := range keys {
		next, cmd := m.Update(keyPress(k))
		m = drive(t, next.(Model), cmd)
	}
	t.Cleanup(m.releaseResources)
	return m
}

func openPodAccess(t *testing.T, m Model) Model {
	t.Helper()
	return pressDrive(t, m, "enter", "d")
}

func hostPages(host string) map[schema.GroupVersionResource]data.ResourcePage {
	labels := data.Labels{{Key: "app", Value: "web"}}
	return map[schema.GroupVersionResource]data.ResourcePage{
		serviceGVR:       netPage(netRow("apps", "web", data.NetworkFacts{Service: &data.ServiceFacts{Type: "ClusterIP", ClusterIPs: []string{"10.1.0.1"}, Selector: labels, Ports: []data.ServicePort{{Name: "http", Port: 80, Protocol: "TCP"}}}})),
		endpointSliceGVR: netPage(netRow("apps", "web-x", data.NetworkFacts{Slice: &data.SliceFacts{Service: "web", AddressType: "IPv4", Ports: []data.SlicePort{{Name: "http", Port: 80}}, Total: 1, Ready: 1}})),
		ingressGVR:       netPage(netRow("apps", "web", data.NetworkFacts{Ingress: &data.IngressFacts{Class: "nginx", Rules: []data.IngressRule{{Host: host, Paths: []data.IngressPath{{Path: "/", Backend: data.IngressBackend{Service: "web", PortName: "http"}}}}}, Addresses: []data.LoadBalancerAddress{{IP: "203.0.113.7"}}}})),
		ingressClassGVR:  netPage(netRow("", "nginx", data.NetworkFacts{Class: &data.IngressClassFacts{Controller: "x"}})),
		netPolicyGVR:     netPage(),
		podGVR:           netPage(netRow("apps", "web-1", data.NetworkFacts{Pod: &data.PodFacts{Labels: labels, Phase: "Running", Ready: data.TriTrue}})),
		nodeGVR:          netPage(),
	}
}

func copyURL(t *testing.T, m Model) (Model, []tea.Msg) {
	t.Helper()
	m = pressDrive(t, m, "u")
	next, cmd := m.Update(keyPress("enter"))
	return next.(Model), drain(cmd)
}

func TestNetworkGoldens(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"160x48", 160, 48}, {"130x40", 130, 40}, {"80x24", 80, 24}} {
		for _, tc := range []struct {
			name string
			dest DestinationID
		}{{"net_services", DestServices}, {"net_ingresses", DestIngresses}, {"net_endpointslices", DestEndpointSlices}, {"net_ingressclasses", DestIngressClasses}, {"net_policies", DestNetPolicies}} {
			m := openLeaf(t, demoModel(t, size.w, size.h), tc.dest)
			c := m.Render()
			golden(t, tc.name+"_"+size.name+".txt", c.Plain())
			golden(t, tc.name+"_"+size.name+".ansi", c.String())
		}
		for _, tc := range []struct {
			name string
			open func(m Model) Model
		}{
			{"net_ingress_broken", func(m Model) Model { return pressDrive(t, openLeaf(t, m, DestIngresses), "end", "enter") }},
			{"net_service_detail", func(m Model) Model {
				return pressDrive(t, openLeaf(t, m, DestServices), "down", "down", "enter")
			}},
			{"net_pod_access", func(m Model) Model { return openPodAccess(t, m) }},
			{"net_url_picker", func(m Model) Model { return pressDrive(t, openPodAccess(t, m), "u") }},
			{"net_filter", func(m Model) Model { return pressDrive(t, openLeaf(t, m, DestServices), "/", "o", "d", "o", "o") }},
		} {
			c := tc.open(demoModel(t, size.w, size.h)).Render()
			golden(t, tc.name+"_"+size.name+".txt", c.Plain())
			golden(t, tc.name+"_"+size.name+".ansi", c.String())
		}
	}
}

func TestNetworkTablesUseTheNetworkProjectionAndRelease(t *testing.T) {
	b := newNetBrowser(nil, nil)
	m := nodeModel(t, b, 160, 48)
	for i, w := range networkLeaves {
		m = openLeaf(t, m, w.dest)
		key := (*b.keys)[i]
		if key.GVR != w.gvr || key.ProjectionVersion != data.ResourceNetworkProjection || key.Namespace != "" {
			t.Fatalf("key = %+v", key)
		}
		if int(b.cancels.Load()) != i {
			t.Fatalf("the previous list is released: %d", b.cancels.Load())
		}
	}
	m = press(t, m, "2")
	next, _ := m.Update(tea.FocusMsg{})
	if int(b.cancels.Load()) != len(networkLeaves) || len(next.(Model).res.subs) != 0 {
		t.Fatal("leaving Network releases every subscription")
	}
}

func TestNetworkTableStatesStayDistinct(t *testing.T) {
	for _, tc := range []struct {
		name  string
		avail data.ResourceAvailability
		page  data.ResourcePage
		want  string
	}{
		{"denied discovery", data.ResourceDenied, data.ResourcePage{}, "sem permissão para descobrir Services"},
		{"absent", data.ResourceAbsent, data.ResourcePage{}, "Services não é servido por este cluster"},
		{"partial", data.ResourcePartial, data.ResourcePage{}, "descoberta parcial"},
		{"denied list", "", data.ResourcePage{Coverage: data.Coverage{State: data.CoverageDenied}}, "sem permissão para listar Services"},
		{"empty", "", netPage(), "nenhum Services neste escopo"},
		{"unknown", "", data.ResourcePage{Coverage: data.Coverage{State: data.CoverageStale, Reason: "x"}}, "sem itens observados"},
	} {
		avail := map[string]data.ResourceAvailability{}
		if tc.avail != "" {
			avail["services"] = tc.avail
		}
		m := openLeaf(t, nodeModel(t, newNetBrowser(avail, map[schema.GroupVersionResource]data.ResourcePage{serviceGVR: tc.page}), 160, 48), DestServices)
		if plain := m.Render().Plain(); !strings.Contains(plain, tc.want) {
			t.Errorf("%s misses %q", tc.name, tc.want)
		}
	}
}

func TestUnreadableRowsShowUnknownInsteadOfEmpty(t *testing.T) {
	page := netPage(netRow("apps", "odd", data.NetworkFacts{Truncated: true}))
	m := openLeaf(t, nodeModel(t, newNetBrowser(nil, map[schema.GroupVersionResource]data.ResourcePage{serviceGVR: page}), 160, 48), DestServices)
	if !strings.Contains(m.Render().Plain(), "odd") || !strings.Contains(m.Render().Plain(), " ?  ") {
		t.Fatal("unreadable facts are shown as unknown")
	}
	m = pressDrive(t, m, "enter")
	if !strings.Contains(m.Render().Plain(), "só parte dos dados foi lida") {
		t.Fatal("the detail says the object is unreadable")
	}
}

func TestHTTPRoutesOnlyAppearWhenServed(t *testing.T) {
	has := func(m Model) bool {
		return slices.ContainsFunc(m.tree.withExpanded(DestNetwork, true).rows(), func(r treeRow) bool { return r.dest.ID == DestHTTPRoutes })
	}
	m := nodeModel(t, newNetBrowser(map[string]data.ResourceAvailability{"httproutes": data.ResourceAbsent}, nil), 160, 48)
	if has(m) || has(openLeaf(t, m, DestServices)) {
		t.Fatal("HTTPRoutes stays out of the tree while discovery does not serve it")
	}
	b := newNetBrowser(map[string]data.ResourceAvailability{"httproutes": data.ResourceAbsent}, nil)
	m = openLeaf(t, nodeModel(t, b, 160, 48), DestHTTPRoutes)
	if b.subs.Load() != 0 || !strings.Contains(m.Render().Plain(), "não é servido") {
		t.Fatal("an absent HTTPRoute API is never read")
	}
	if m = openLeaf(t, nodeModel(t, newNetBrowser(nil, nil), 160, 48), DestServices); !has(m) {
		t.Fatal("HTTPRoutes appears when served")
	}
}

func TestTableFilterNarrowsAndEscClears(t *testing.T) {
	page := netPage(netRow("apps", "web", data.NetworkFacts{Service: &data.ServiceFacts{Type: "ClusterIP"}}), netRow("apps", "db", data.NetworkFacts{Service: &data.ServiceFacts{Type: "NodePort"}}))
	m := openLeaf(t, nodeModel(t, newNetBrowser(nil, map[schema.GroupVersionResource]data.ResourcePage{serviceGVR: page}), 160, 48), DestServices)
	m = pressDrive(t, m, "/", "n", "o", "d", "e", "q")
	if m.filter.Value() != "nodeq" || !m.filtering {
		t.Fatalf("typing is text: %q", m.filter.Value())
	}
	if !strings.Contains(m.Render().Plain(), "nenhum item lido corresponde ao filtro") {
		t.Fatal("no match is not an empty inventory")
	}
	m = pressDrive(t, m, "backspace")
	if st := m.tableState(); len(st.Page.Items) != 1 || st.Page.Items[0].Ref.Name != "db" || st.Page.Matched != 1 || st.Page.Total != 2 {
		t.Fatalf("%+v", m.tableState().Page)
	}
	m = pressDrive(t, m, "enter", "enter")
	if m.nav.Active().Kind != RouteResource || m.nav.Active().Ref.Name != "db" {
		t.Fatalf("%+v", m.nav.Active())
	}
	m = pressDrive(t, m, "esc")
	if !m.onScreen(ScreenResources) || m.filter.Value() != "node" || len(m.tableState().Page.Items) != 1 {
		t.Fatal("back keeps the filtered list")
	}
	m = pressDrive(t, m, "esc")
	if !m.onScreen(ScreenResources) || m.filter.Value() != "" || len(m.tableState().Page.Items) != 2 {
		t.Fatal("esc clears the filter before leaving")
	}
	m = pressDrive(t, m, "/", "w", "esc")
	if m.filter.Value() != "" || m.filtering {
		t.Fatal("esc while typing clears the filter")
	}
}

func TestTableFilterNeverLeaksToOtherScreens(t *testing.T) {
	m := openLeaf(t, demoModel(t, 160, 48), DestServices)
	m = pressDrive(t, m, "/", "o", "d", "o", "o", "enter")
	m = openLeaf(t, m, DestIngresses)
	if m.filter.Value() != "" || len(m.tableState().Page.Items) != 5 {
		t.Fatal("each list starts without the filter of another")
	}
	m = pressDrive(t, m, "/", "e", "r", "p", "enter", "2")
	if m.filter.Value() != "" {
		t.Fatal("a table filter never filters pods")
	}
	m = pressDrive(t, press(t, demoModel(t, 160, 48), "/", "l", "g", "o", "enter"), "3")
	m = openLeaf(t, m, DestServices)
	if len(m.tableState().Page.Items) != 8 {
		t.Fatal("a pod filter never filters services")
	}
}

func TestPodAccessIsLazyScopedAndReleased(t *testing.T) {
	b := newNetBrowser(nil, nil)
	m := nodeModel(t, b, 160, 48)
	m = pressDrive(t, m, "enter")
	if b.subs.Load() != 0 || b.catalogs.Load() != 0 {
		t.Fatal("opening a pod reads no network")
	}
	m = pressDrive(t, m, "d")
	ns := m.nav.Active().Ref.Namespace
	if int(b.subs.Load()) != len(graphGVRs)+1 {
		t.Fatalf("subs = %d", b.subs.Load())
	}
	for _, key := range *b.keys {
		cluster := key.GVR == nodeGVR || key.GVR == ingressClassGVR
		if key.ProjectionVersion != data.ResourceNetworkProjection || cluster != (key.Namespace == "") || !cluster && key.Namespace != ns {
			t.Fatalf("key = %+v", key)
		}
		if key.GVR.Resource == "secrets" {
			t.Fatal("access never reads secrets")
		}
	}
	m = pressDrive(t, m, "e")
	if b.cancels.Load() != b.subs.Load() || len(m.acc.subs) != 0 || m.acc.target.active {
		t.Fatal("leaving Describe releases every subscription")
	}
	m = pressDrive(t, m, "d")
	m = pressDrive(t, m, "esc")
	if b.cancels.Load() != b.subs.Load() || len(m.acc.subs) != 0 {
		t.Fatal("leaving the pod releases every subscription")
	}
}

func TestAccessNeverExceedsTheWatchBudget(t *testing.T) {
	const watchBudget, entryBudget = 6, 12
	borrowed := map[schema.GroupVersionResource]bool{podGVR: true, nodeGVR: true, {Group: "apps", Version: "v1", Resource: "deployments"}: true}
	check := func(name string, m Model, b fakeBrowser) {
		live := map[data.ResourceSubscriptionKey]bool{}
		for _, subs := range []map[schema.GroupVersionResource]*resourceSub{m.res.subs, m.acc.subs} {
			for _, s := range subs {
				live[s.key] = true
			}
		}
		watches := 0
		for key := range live {
			if !borrowed[key.GVR] {
				watches++
			}
		}
		if watches > watchBudget || len(live) > entryBudget {
			t.Errorf("%s keeps %d watches and %d entries", name, watches, len(live))
		}
	}
	sts := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
	for _, tc := range []struct {
		dest DestinationID
		gvr  schema.GroupVersionResource
		row  data.ResourceRow
	}{
		{DestServices, serviceGVR, netRow("apps", "web", data.NetworkFacts{Service: &data.ServiceFacts{}})},
		{DestIngresses, ingressGVR, netRow("apps", "web", data.NetworkFacts{Ingress: &data.IngressFacts{}})},
		{DestStatefulSets, sts, netRow("apps", "web", data.NetworkFacts{Template: &data.TemplateFacts{}})},
	} {
		b := newNetBrowser(nil, map[schema.GroupVersionResource]data.ResourcePage{tc.gvr: netPage(tc.row)})
		m := pressDrive(t, openLeaf(t, nodeModel(t, b, 160, 48), tc.dest), "enter")
		if m.nav.Active().Kind != RouteResource || len(m.acc.subs) == 0 {
			t.Fatalf("%s did not open", tc.dest)
		}
		check(string(tc.dest), m, b)
	}
	b := newNetBrowser(nil, nil)
	check("pod", openPodAccess(t, nodeModel(t, b, 160, 48)), b)
}

func TestDetailsWithoutAccessSubscribeToNothingElse(t *testing.T) {
	for _, tc := range []struct {
		dest DestinationID
		gvr  schema.GroupVersionResource
		row  data.ResourceRow
		want string
	}{
		{DestIngressClasses, ingressClassGVR, netRow("", "nginx", data.NetworkFacts{Class: &data.IngressClassFacts{Controller: "ctl"}}), "ctl"},
		{DestNetPolicies, netPolicyGVR, netRow("apps", "deny", data.NetworkFacts{Policy: &data.NetworkPolicyFacts{}}), "depende do CNI"},
		{DestEndpointSlices, endpointSliceGVR, netRow("apps", "web-x", data.NetworkFacts{Slice: &data.SliceFacts{Service: "web", Total: 40, Ready: 3, Endpoints: []data.SliceEndpoint{{Addresses: []string{"10.0.0.1"}, TargetKind: "Pod", TargetName: "web-1"}}}}), "mais 39 não mostrados"},
		{DestHTTPRoutes, httpRouteGVR, netRow("apps", "r", data.NetworkFacts{Route: &data.HTTPRouteFacts{Hostnames: []string{"r.example.com"}}}), "não derivada"},
	} {
		b := newNetBrowser(nil, map[schema.GroupVersionResource]data.ResourcePage{tc.gvr: netPage(tc.row)})
		m := pressDrive(t, openLeaf(t, nodeModel(t, b, 160, 48), tc.dest), "enter")
		if b.subs.Load() != 1 || !strings.Contains(m.Render().Plain(), tc.want) {
			t.Errorf("%s subs=%d", tc.dest, b.subs.Load())
		}
		if strings.Contains(m.Render().String(), "\x1b]8") {
			t.Errorf("%s has no links", tc.dest)
		}
	}
}

func openIngress(t *testing.T, host string, w, h int) Model {
	t.Helper()
	b := newNetBrowser(nil, hostPages(host))
	return pressDrive(t, openLeaf(t, nodeModel(t, b, w, h), DestIngresses), "enter")
}

func TestLinkAndCopyShareTheValidatedURL(t *testing.T) {
	m := openIngress(t, "web.example.com", 160, 48)
	out := m.Render().String()
	if strings.Count(out, "\x1b]8;;http://web.example.com\x1b\\") != 1 || strings.Count(out, "\x1b]8;;\x1b\\") != 1 {
		t.Fatalf("one link expected, got %d", strings.Count(out, "\x1b]8;;"))
	}
	got, msgs := copyURL(t, m)
	if text, ok := clipboardOf(msgs); !ok || text != "http://web.example.com" {
		t.Fatalf("copied %q %v", text, ok)
	}
	if got.notice.text != "enviado ao clipboard: http://web.example.com" {
		t.Fatalf("notice %q", got.notice.text)
	}
}

func TestHostileHostsAreNeitherLinkedNorCopied(t *testing.T) {
	for _, host := range []string{
		"*.example.com", "", "localhost", "169.254.169.254", "127.1", "2130706433",
		"\u0430pple.com", "payp\u0430l.com", "trusted.com@evil.com", "trusted.com\\evil.com", "a.com\r\nevil", "a.com/evil",
		"a\x1b]8;;http://evil.com\x1b\\x", "a\x1b]52;c;ZXZpbA==\x07", "a\u009d8;;http://evil.com\u009c", "a\u202emoc.live", "a\u2066b.com", "a%2ecom",
	} {
		m := openIngress(t, host, 160, 48)
		out := m.Render().String()
		for _, banned := range []string{"\x1b]", "\x07", "\u009d", "\u009c", "\u202e", "\u2066", "\r"} {
			if strings.Contains(out, banned) {
				t.Errorf("%q leaks %q into the terminal", host, banned)
			}
		}
		opened := pressDrive(t, m, "u")
		for _, banned := range []string{"\x1b]", "\x07", "\u009d", "\u009c", "\u202e", "\u2066", "\r"} {
			if strings.Contains(opened.Render().String(), banned) {
				t.Errorf("%q leaks %q through the picker", host, banned)
			}
		}
		got, msgs := copyURL(t, m)
		if text, ok := clipboardOf(msgs); ok {
			t.Errorf("%q copied %q", host, text)
		}
		if !strings.HasPrefix(got.notice.text, "sem URL copiável: ") {
			t.Errorf("%q notice %q", host, got.notice.text)
		}
	}
}

func TestPunycodeIsShownInASCIIWithAWarning(t *testing.T) {
	m := openIngress(t, "www.xn--pple-43d.com", 160, 48)
	plain := m.Render().Plain()
	if !strings.Contains(plain, "http://www.xn--pple-43d.com") || !strings.Contains(plain, "host em punycode") {
		t.Fatal("punycode keeps the ASCII form and warns")
	}
}

func TestTruncatedURLIsNotClickable(t *testing.T) {
	host := "trusted.example.com." + strings.Repeat("a", 60) + ".evil.com"
	m := openIngress(t, host, 80, 24)
	if strings.Contains(m.Render().String(), "\x1b]8") {
		t.Fatal("a URL that does not fit never hides its real host behind a link")
	}
	m = openIngress(t, host, 160, 48)
	if !strings.Contains(m.Render().String(), "\x1b]8;;http://"+host) {
		t.Fatal("the same URL is clickable when it is fully visible")
	}
}

func TestClusterAddressesCopyButNeverLink(t *testing.T) {
	b := newNetBrowser(nil, hostPages("web.example.com"))
	b.pages[ingressGVR] = netPage()
	m := pressDrive(t, openLeaf(t, nodeModel(t, b, 160, 48), DestServices), "enter")
	if strings.Contains(m.Render().String(), "\x1b]8") || !strings.Contains(m.Render().Plain(), "endereço interno do cluster") {
		t.Fatal("a ClusterIP is labeled and never clickable")
	}
	if !strings.Contains(pressDrive(t, m, "u").Render().Plain(), "endereço interno do cluster") {
		t.Fatal("the picker states the reach")
	}
	_, msgs := copyURL(t, m)
	if text, _ := clipboardOf(msgs); text != "http://10.1.0.1" {
		t.Fatalf("%q", text)
	}
}

func TestDescribeWithoutAddressesStillCopiesTheManifest(t *testing.T) {
	m := openPodAccess(t, nodeModel(t, newNetBrowser(nil, nil), 160, 48))
	_, msgs := pressYank(t, m)
	if text, _ := clipboardOf(msgs); !strings.HasPrefix(text, "apiVersion:") {
		t.Fatalf("copied %q", text)
	}
}

func TestDeploymentOverlayOpensAccess(t *testing.T) {
	m := pressDrive(t, demoModel(t, 160, 48), "3")
	m = openLeaf(t, m, DestDeployments)
	m = pressDrive(t, m, "enter")
	if !m.dep.open {
		t.Fatal("overlay closed")
	}
	m = pressDrive(t, m, "u")
	if m.dep.open || m.nav.Active().Kind != RouteResource || !strings.Contains(m.Render().Plain(), "live.example.com") {
		t.Fatalf("%+v", m.nav.Active())
	}
	m = pressDrive(t, m, "esc")
	if !m.onScreen(ScreenWorkloads) || len(m.acc.subs) != 0 {
		t.Fatal("back releases the access graph")
	}
}

func TestStaleAccessResultsAreIgnored(t *testing.T) {
	m := openPodAccess(t, demoModel(t, 160, 48))
	want := m.acc.view
	for _, msg := range []accessMsg{
		{session: m.session.Generation + 1, seq: m.acc.seq},
		{session: m.session.Generation, seq: m.acc.seq - 1},
	} {
		next, _ := m.Update(msg)
		if next.(Model).acc.view != want {
			t.Fatal("a result of another session or request never replaces the current one")
		}
	}
	other := pressDrive(t, pressDrive(t, m, "esc", "down"), "enter", "d")
	if other.acc.target == m.acc.target {
		t.Fatal("the target follows the pod")
	}
	next, _ := other.Update(accessMsg{session: m.session.Generation, seq: m.acc.seq, view: *want})
	if next.(Model).acc.view != other.acc.view {
		t.Fatal("the result of the previous pod is ignored")
	}
	left := pressDrive(t, other, "esc")
	if next, _ := left.Update(accessMsg{session: left.session.Generation, seq: left.acc.seq, view: *want}); next.(Model).acc.view != nil {
		t.Fatal("a result after leaving is ignored")
	}
}

func TestAccessKeepsDeniedUnknownAbsentAndEmptyApart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		avail map[string]data.ResourceAvailability
		page  *data.ResourcePage
		want  string
		never string
	}{
		{"denied discovery", map[string]data.ResourceAvailability{"services": data.ResourceDenied}, nil, "sem permissão para ler Services", "nenhum Service"},
		{"absent", map[string]data.ResourceAvailability{"services": data.ResourceAbsent}, nil, "Services não é servido", "nenhum Service"},
		{"denied list", nil, &data.ResourcePage{Coverage: data.Coverage{State: data.CoverageDenied}}, "sem permissão para ler Services", "nenhum Service"},
		{"partial", nil, &data.ResourcePage{Coverage: data.Coverage{State: data.CoveragePartial}}, "leitura incompleta", "nenhum Service"},
		{"truncated", nil, &data.ResourcePage{Coverage: data.Coverage{State: data.CoverageComplete}, Truncated: true}, "leitura incompleta", "nenhum Service"},
		{"empty", nil, &data.ResourcePage{Coverage: data.Coverage{State: data.CoverageComplete}}, "nenhum Service da lista lida seleciona este alvo", "sem permissão"},
		{"list only", nil, &data.ResourcePage{Coverage: data.Coverage{State: data.CoverageListOnly}}, "leitura pontual", "sem permissão"},
	} {
		pages := hostPages("web.example.com")
		if tc.page != nil {
			pages[serviceGVR] = *tc.page
		}
		m := nodeModel(t, newNetBrowser(tc.avail, pages), 160, 48)
		pod := m.selectedPod()
		pods := pages[podGVR]
		pods.Items[0].Ref.Namespace, pods.Items[0].Ref.Name, pods.Items[0].Ref.UID = pod.Namespace, pod.Name, pod.UID
		m = openPodAccess(t, m)
		plain := m.Render().Plain()
		if !strings.Contains(plain, tc.want) || strings.Contains(plain, tc.never) {
			t.Errorf("%s: want %q never %q", tc.name, tc.want, tc.never)
		}
	}
}

func TestVanishedObjectIsReported(t *testing.T) {
	b := newNetBrowser(nil, hostPages("web.example.com"))
	m := pressDrive(t, openLeaf(t, nodeModel(t, b, 160, 48), DestIngresses), "enter")
	b.pages[ingressGVR] = netPage(netRow("apps", "web", data.NetworkFacts{Ingress: &data.IngressFacts{}}))
	replaced := b.pages[ingressGVR].Items[0]
	replaced.Ref.UID = "another"
	for _, s := range m.acc.subs {
		if s.key.GVR == ingressGVR {
			s.handle.(*fakeHandle).page = netPage(replaced)
		}
	}
	m.acc.dirty = true
	next, cmd := m.Update(tea.FocusMsg{})
	m = drive(t, next.(Model), cmd)
	if !strings.Contains(m.Render().Plain(), "o objeto não está mais na lista lida") || len(m.accessAddresses()) != 0 {
		t.Fatal("an object recreated with another UID is not the opened one")
	}
}

func TestSourceWithoutCatalogExplainsAccess(t *testing.T) {
	m := openPodAccess(t, nodeModel(t, plainSource{data.NewDemo()}, 160, 48))
	if !strings.Contains(m.Render().Plain(), "esta fonte não oferece o catálogo de recursos") {
		t.Fatal("no browser is not an empty access section")
	}
}

func TestShutdownEndsAccessWaits(t *testing.T) {
	baseline := goleak.IgnoreCurrent()
	r := NewRoot(RootOptions{Options: Options{Source: data.NewDemo(), NoAnim: true}})
	m, _ := r.model()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	m = next.(Model)
	for _, k := range []string{"enter", "d"} {
		next, cmd := m.Update(keyPress(k))
		m = drive(t, next.(Model), cmd)
	}
	if len(m.acc.subs) == 0 || m.acc.view == nil {
		t.Fatal("access not open")
	}
	var dones []<-chan struct{}
	for _, s := range m.acc.subs {
		dones = append(dones, s.done)
	}
	r.cur = m
	r.Shutdown()
	for _, d := range dones {
		awaitClosed(t, d, "access subscription")
	}
	goleak.VerifyNone(t, baseline)
}

type slowBrowser struct {
	fakeBrowser
	release chan struct{}
	reads   *atomic.Int32
}

type slowHandle struct {
	*fakeHandle
	b slowBrowser
}

func (h slowHandle) Page(cursor string, limit int) data.ResourcePage {
	h.b.reads.Add(1)
	<-h.b.release
	return h.fakeHandle.Page(cursor, limit)
}

func (b slowBrowser) Subscribe(ctx context.Context, key data.ResourceSubscriptionKey) (data.ResourceSubscription, error) {
	h, err := b.fakeBrowser.Subscribe(ctx, key)
	return slowHandle{h.(*fakeHandle), b}, err
}

func TestAccessReadsPagesOutsideUpdate(t *testing.T) {
	b := slowBrowser{newNetBrowser(nil, hostPages("web.example.com")), make(chan struct{}), &atomic.Int32{}}
	m := nodeModel(t, b, 160, 48)
	done := make(chan Model, 1)
	go func() {
		for _, k := range []string{"enter", "d"} {
			next, _ := m.Update(keyPress(k))
			m = next.(Model)
		}
		snap := b.snap
		next, _ := m.Update(resourceCatalogMsg{session: m.session.Generation, seq: m.res.catalogSeq, snap: snap})
		m = next.(Model)
		for _, s := range m.acc.subs {
			h, _ := b.Subscribe(context.Background(), s.key)
			next, _ = m.Update(resourceSubMsg{tag: resourceTag{session: m.session.Generation, seq: s.seq, key: s.key}, handle: h})
			m = next.(Model)
		}
		before := m.acc.seq
		next, _ = m.Update(accessMsg{session: m.session.Generation, seq: m.acc.seq})
		m = next.(Model)
		if m.acc.seq == before || !m.acc.computing {
			t.Error("the graph was not scheduled for reading")
		}
		_ = m.Render()
		done <- m
	}()
	select {
	case m = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Update or View waited for a page read")
	}
	if b.reads.Load() != 0 {
		t.Fatalf("Update read %d pages", b.reads.Load())
	}
	close(b.release)
	m.releaseResources()
}

func longURL() (string, string) {
	host := "trusted.example.com." + strings.Repeat("a", 60) + ".evil.com"
	return host, "http://" + host
}

func TestTruncatedURLIsShownWholeBeforeTheCopy(t *testing.T) {
	host, url := longURL()
	for _, size := range [][2]int{{80, 24}, {100, 30}, {130, 40}, {160, 48}} {
		m := openIngress(t, host, size[0], size[1])
		if _, msgs := pressYank(t, m); len(msgs) > 0 {
			if text, ok := clipboardOf(msgs); ok && text != "apps/web" {
				t.Fatalf("y copies the name, got %q", text)
			}
		}
		opened := pressDrive(t, m, "u")
		lines := ui.URLPickerLines(size[0], size[1], opened.urls.items[0].URL)
		plain := opened.Render().Plain()
		for _, line := range lines {
			if !strings.Contains(plain, line) {
				t.Fatalf("%v hides %q", size, line)
			}
		}
		_, msgs := copyURL(t, m)
		text, ok := clipboardOf(msgs)
		if !ok || text != url || strings.Join(lines, "") != text {
			t.Fatalf("%v copied %q, shown %q", size, text, strings.Join(lines, ""))
		}
	}
}

func TestURLPickerHighlightsTheWholeHost(t *testing.T) {
	host, _ := longURL()
	m := pressDrive(t, openIngress(t, host, 80, 24), "u")
	c := m.Render()
	var shown strings.Builder
	for y := range c.Height() {
		for x := range c.Width() {
			if cell := c.At(x, y); cell.Bold && cell.Fg == m.pal.A {
				shown.WriteRune(cell.Rune)
			}
		}
	}
	if !strings.Contains(shown.String(), host) {
		t.Fatalf("highlighted %q", shown.String())
	}
}

func TestURLThatCannotBeShownWholeIsRefused(t *testing.T) {
	b := newNetBrowser(nil, hostPages("web.example.com"))
	b.pages[ingressGVR].Items[0].Network.Ingress.Rules[0].Paths[0].Path = "/" + strings.Repeat("p", 1000)
	m := pressDrive(t, openLeaf(t, nodeModel(t, b, 80, 24), DestIngresses), "enter")
	got, msgs := copyURL(t, m)
	if text, ok := clipboardOf(msgs); ok || !got.urls.open || got.notice.text != "a URL inteira não cabe neste terminal" {
		t.Fatalf("copied %q notice %q", text, got.notice.text)
	}
	if !strings.Contains(got.Render().Plain(), "a URL inteira não cabe neste terminal; aumente a janela") {
		t.Fatal("the picker says why")
	}
	next, _ := got.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	after, cmd := next.(Model).Update(keyPress("enter"))
	if text, _ := clipboardOf(drain(cmd)); !strings.HasPrefix(text, "http://web.example.com/ppp") || after.(Model).urls.open {
		t.Fatal("a larger window shows and copies it")
	}
}

func TestYankInDescribeKeepsTheManifestInEveryAccessState(t *testing.T) {
	check := func(name string, m Model) {
		t.Helper()
		_, msgs := pressYank(t, m)
		if text, ok := clipboardOf(msgs); !ok || !strings.HasPrefix(text, "apiVersion:") {
			t.Fatalf("%s: y copied %q", name, text)
		}
	}
	m := demoModel(t, 80, 24)
	m.after = instantTimer
	for _, k := range []string{"enter", "d"} {
		next, _ := m.Update(keyPress(k))
		m = next.(Model)
	}
	check("before the report", m)
	m = openPodAccess(t, demoModel(t, 80, 24))
	if len(m.accessAddresses()) == 0 {
		t.Fatal("fixture needs addresses")
	}
	check("after the report", m)
	check("scrolled away", pressDrive(t, m, "end"))
	check("after the picker", pressDrive(t, m, "u", "esc"))
	check("without addresses", openPodAccess(t, nodeModel(t, newNetBrowser(nil, nil), 80, 24)))
}

func TestOffscreenAccessIsShownInThePickerBeforeTheCopy(t *testing.T) {
	m := pressDrive(t, openPodAccess(t, demoModel(t, 80, 24)), "end")
	if strings.Contains(m.Render().Plain(), "https://live.example.com") {
		t.Fatal("fixture needs the address off screen")
	}
	opened := pressDrive(t, m, "u")
	if !strings.Contains(opened.Render().Plain(), "https://live.example.com") {
		t.Fatal("the picker shows the URL")
	}
	_, msgs := copyURL(t, m)
	if text, _ := clipboardOf(msgs); text != "https://live.example.com" {
		t.Fatalf("copied %q", text)
	}
}

func TestURLPickerIsFrozenAndCapturesInput(t *testing.T) {
	m := pressDrive(t, openPodAccess(t, demoModel(t, 160, 48)), "u")
	frozen := slices.Clone(m.urls.items)
	changed := *m.acc.view
	changed.report.Addresses = nil
	next, _ := m.Update(accessMsg{session: m.session.Generation, seq: m.acc.seq, view: changed})
	m = next.(Model)
	if !slices.Equal(m.urls.items, frozen) {
		t.Fatal("an asynchronous report never changes the open picker")
	}
	for _, k := range []string{"y", "c", "n", "2", "m", "b", "?", "/", "e", "left", "tab"} {
		next, cmd := m.Update(keyPress(k))
		if _, ok := clipboardOf(drain(cmd)); ok {
			t.Fatalf("%s copied something", k)
		}
		if got := next.(Model); !got.urls.open || got.nav != m.nav || got.tab != m.tab || got.helpOpen || got.menu.open {
			t.Fatalf("%s leaked through the picker", k)
		}
	}
	m = pressDrive(t, m, "u", "u", "u", "u", "up")
	if m.urls.cursor != len(frozen)-2 {
		t.Fatalf("cursor = %d", m.urls.cursor)
	}
	want := frozen[m.urls.cursor].URL.String()
	if !strings.Contains(m.Render().Plain(), want) {
		t.Fatal("the chosen URL is shown")
	}
	after, cmd := m.Update(keyPress("enter"))
	if text, _ := clipboardOf(drain(cmd)); text != want || after.(Model).urls.open {
		t.Fatalf("copied %q want %q", text, want)
	}
	clicked, _ := m.Update(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	if clicked.(Model).urls.open {
		t.Fatal("a click closes the picker")
	}
	small, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	if small.(Model).urls.open {
		t.Fatal("a terminal that is too small closes the picker")
	}
}

func TestURLPickerNeedsAddresses(t *testing.T) {
	m := pressDrive(t, openPodAccess(t, nodeModel(t, newNetBrowser(nil, nil), 160, 48)), "u")
	if m.urls.open {
		t.Fatal("nothing to choose")
	}
	m = pressDrive(t, demoModel(t, 160, 48), "enter", "u")
	if m.urls.open {
		t.Fatal("u belongs to Describe")
	}
	m = pressDrive(t, openLeaf(t, demoModel(t, 160, 48), DestServices), "u")
	if m.urls.open {
		t.Fatal("u does nothing in a list")
	}
}

func partialLinks(c *draw.Canvas) (int, []string) {
	whole := 0
	var broken []string
	for y := range c.Height() {
		for x := 0; x < c.Width(); {
			u := c.At(x, y).Link
			if u.IsZero() {
				x++
				continue
			}
			var label strings.Builder
			for ; x < c.Width() && c.At(x, y).Link == u; x++ {
				label.WriteRune(c.At(x, y).Rune)
			}
			if label.String() == u.String() {
				whole++
			} else {
				broken = append(broken, label.String())
			}
		}
	}
	if opened := strings.Count(c.String(), "\x1b]8;;http"); opened != whole {
		broken = append(broken, fmt.Sprintf("%d links emitted for %d intact labels", opened, whole))
	}
	return whole, broken
}

func TestOverlaysAndResizeNeverLeaveAPartialLink(t *testing.T) {
	long, _ := longURL()
	seen := 0
	for _, host := range []string{"web.example.com", long} {
		base := openIngress(t, host, 160, 48)
		for _, keys := range [][]string{nil, {"u"}, {"?"}, {"m"}, {"b"}, {"b", "b"}} {
			for _, size := range [][2]int{{80, 24}, {100, 30}, {120, 36}, {130, 40}, {160, 48}, {200, 48}, {240, 48}, {300, 48}} {
				next, _ := base.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m := pressDrive(t, next.(Model), keys...)
				whole, broken := partialLinks(m.Render())
				if len(broken) > 0 {
					t.Fatalf("%s %v %v: %q", host, keys, size, broken)
				}
				seen += whole
				for w := size[0]; w >= 80; w -= 7 {
					next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: size[1]})
					m = next.(Model)
					if _, broken := partialLinks(m.Render()); len(broken) > 0 {
						t.Fatalf("%s %v resized to %d: %q", host, keys, w, broken)
					}
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("the fixture must also show intact links")
	}
}

func TestRootPickersNeverLeaveAPartialLink(t *testing.T) {
	long, _ := longURL()
	occluded := 0
	for _, kind := range []pickKind{pickContext, pickNamespace} {
		for _, w := range []int{100, 160, 200, 240, 300} {
			r := NewRoot(RootOptions{Options: Options{Source: newNetBrowser(nil, hostPages(long)), NoAnim: true}})
			t.Cleanup(r.Shutdown)
			base := openIngress(t, long, w, 48)
			for h := 24; h <= 48; h++ {
				next, _ := base.Update(tea.WindowSizeMsg{Width: w, Height: h})
				r.cur, r.w, r.h, r.pick = next, w, h, nil
				before, broken := partialLinks(r.Render())
				if len(broken) > 0 {
					t.Fatalf("%dx%d: %q", w, h, broken)
				}
				r, _ = r.openPicker(kind)
				if r.pick == nil {
					t.Fatal("picker closed")
				}
				after, broken := partialLinks(r.Render())
				if len(broken) > 0 {
					t.Fatalf("picker %v at %dx%d: %q", kind, w, h, broken)
				}
				if after < before {
					occluded++
				}
			}
		}
	}
	if occluded == 0 {
		t.Fatal("no size puts a picker over the link")
	}
}

func radarPodAccess(t *testing.T, host string, w, h int) (Model, fakeBrowser) {
	t.Helper()
	harness := newRadarHarness(t)
	m := harness.active(w, h)
	sig := m.selectedSignal()
	if sig == nil || sig.Entity.UID == "" {
		t.Fatal("the fixture needs a Radar signal with UID")
	}
	b := newNetBrowser(nil, hostPages(host))
	b.Demo = harness.demo
	pod := &b.pages[podGVR].Items[0]
	pod.Ref.Namespace, pod.Ref.Name, pod.Ref.UID = sig.Entity.Namespace, sig.Entity.Name, sig.Entity.UID
	for _, gvr := range []schema.GroupVersionResource{serviceGVR, endpointSliceGVR, ingressGVR} {
		b.pages[gvr].Items[0].Ref.Namespace = sig.Entity.Namespace
	}
	m.src = b
	m = pressDrive(t, m, "enter", "d")
	r := m.nav.Active()
	if r.Kind != RoutePod || r.Ref.UID != sig.Entity.UID || m.acc.target.uid != sig.Entity.UID || len(m.accessAddresses()) == 0 {
		t.Fatalf("the Radar pod is pinned by UID and has addresses: %+v %+v", r, m.acc.target)
	}
	if !strings.Contains(m.Render().Plain(), host) && h >= 40 {
		t.Fatal("the section shows the URL")
	}
	return m, b
}

func shownPod(m Model) (string, string) {
	r := m.nav.Active()
	return r.Pod, r.Ref.UID
}

func requireNoAccess(t *testing.T, m Model, when string) {
	t.Helper()
	if m.acc.target.active || m.acc.view != nil || len(m.acc.subs) != 0 || m.urls.open || len(m.accessAddresses()) != 0 {
		t.Fatalf("%s keeps access: target %+v subs %d picker %v", when, m.acc.target, len(m.acc.subs), m.urls.open)
	}
	if m.manifest != nil || len(m.logs) != 0 {
		t.Fatalf("%s keeps the manifest or logs", when)
	}
	plain, out := m.Render().Plain(), m.Render().String()
	if strings.Contains(plain, "http://") || strings.Contains(plain, "https://") || strings.Contains(out, "\x1b]8") || strings.Contains(plain, "Copiar URL") {
		t.Fatalf("%s still shows a URL", when)
	}
	opened := pressDrive(t, m, "u")
	if opened.urls.open {
		t.Fatalf("%s opens the picker", when)
	}
	_, msgs := pressYank(t, m)
	if text, ok := clipboardOf(msgs); ok && strings.HasPrefix(text, "http") {
		t.Fatalf("%s copied %q", when, text)
	}
}

func TestRadarPodAccessIsDroppedWithTheReplacedPod(t *testing.T) {
	for _, picker := range []bool{false, true} {
		for _, size := range [][2]int{{80, 24}, {130, 40}, {160, 48}} {
			m, b := radarPodAccess(t, "web.example.com", size[0], size[1])
			if picker {
				m = pressDrive(t, m, "u")
				if !m.urls.open {
					t.Fatal("picker closed")
				}
			}
			late := accessMsg{session: m.session.Generation, seq: m.acc.seq, view: *m.acc.view}
			key, _ := shownPod(m)
			m = replacePod(m, key, "replacement-uid")
			m.refresh()
			next, cmd := m.Update(tea.FocusMsg{})
			m = drive(t, next.(Model), cmd)
			requireNoAccess(t, m, "the replacement")
			if plain := m.Render().Plain(); !strings.Contains(plain, "pod original não existe mais") || strings.Contains(plain, "replacement-image") || strings.Contains(plain, "web.example.com") {
				t.Fatal("only the missing original is shown")
			}
			if b.cancels.Load() != b.subs.Load() {
				t.Fatalf("subscriptions %d cancels %d", b.subs.Load(), b.cancels.Load())
			}
			next, cmd = m.Update(late)
			requireNoAccess(t, drive(t, next.(Model), cmd), "a late report of the original")
		}
	}
}

func accessHarness(t *testing.T) (Model, fakeBrowser) {
	t.Helper()
	b := newNetBrowser(nil, hostPages("web.example.com"))
	m := nodeModel(t, b, 160, 48)
	pod := *m.selectedPod()
	row := &b.pages[podGVR].Items[0]
	row.Ref.Namespace, row.Ref.Name, row.Ref.UID = pod.Namespace, pod.Name, pod.UID
	for _, gvr := range []schema.GroupVersionResource{serviceGVR, endpointSliceGVR, ingressGVR} {
		b.pages[gvr].Items[0].Ref.Namespace = pod.Namespace
	}
	m.navigate(Route{Kind: RoutePod, Screen: ScreenPodDetail, Pod: pod.Key(), Ref: ResourceRef{Version: "v1", Resource: "pods", Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID}})
	m = pressDrive(t, m, "d")
	if len(m.accessAddresses()) == 0 || pod.UID == "" {
		t.Fatalf("fixture needs addresses and UID %q", pod.UID)
	}
	return m, b
}

func TestPinnedPodAccessIsDroppedByTheSnapshot(t *testing.T) {
	for _, picker := range []bool{false, true} {
		m, b := accessHarness(t)
		if picker {
			m = pressDrive(t, m, "u")
		}
		key, _ := shownPod(m)
		late := accessMsg{session: m.session.Generation, seq: m.acc.seq, view: *m.acc.view}
		m = replacePod(m, key, "replacement-uid")
		m.refresh()
		next, cmd := m.Update(tea.FocusMsg{})
		m = drive(t, next.(Model), cmd)
		requireNoAccess(t, m, "the snapshot replacement")
		if b.cancels.Load() != b.subs.Load() {
			t.Fatalf("subscriptions %d cancels %d", b.subs.Load(), b.cancels.Load())
		}
		next, cmd = m.Update(late)
		requireNoAccess(t, drive(t, next.(Model), cmd), "a late report of the original")
	}
}

func TestPinnedPodAccessIsDroppedWhenTheGraphSeesTheReplacementFirst(t *testing.T) {
	for _, state := range []data.CoverageState{data.CoverageComplete, data.CoveragePartial, data.CoverageListOnly} {
		m, b := accessHarness(t)
		m = pressDrive(t, m, "u")
		row := b.pages[podGVR].Items[0]
		row.Ref.UID = "replacement-uid"
		page := netPage(row)
		page.Coverage.State = state
		for gvr, s := range m.acc.subs {
			if gvr == podGVR {
				s.handle.(*fakeHandle).page = page
			}
		}
		m.acc.dirty = true
		next, cmd := m.Update(tea.FocusMsg{})
		m = drive(t, next.(Model), cmd)
		if len(m.accessAddresses()) != 0 || m.urls.open || !m.acc.view.gone {
			t.Fatalf("%s: the replacement's addresses were adopted", state)
		}
		if plain := m.Render().Plain(); strings.Contains(plain, "web.example.com") || !strings.Contains(plain, "o objeto não está mais na lista lida") {
			t.Fatalf("%s: the section must say the object is gone", state)
		}
	}
}

func TestPodOpenedByNameDropsAccessWhenItsUIDChanges(t *testing.T) {
	m, _ := accessHarness(t)
	key, uid := shownPod(m)
	m = pressDrive(t, m, "esc")
	m.selName = key
	m.openPod()
	m = pressDrive(t, m, "d", "u")
	before := m.acc.target
	if _, pinned := shownPod(m); pinned != "" || before.uid != uid || !m.urls.open {
		t.Fatalf("the route carries no UID and the observed one pins access: %+v picker %v", before, m.urls.open)
	}
	m = replacePod(m, key, "replacement-uid")
	m.refresh()
	next, _ := m.Update(tea.FocusMsg{})
	m = next.(Model)
	if m.acc.target == before || m.acc.view != nil || m.urls.open {
		t.Fatal("another UID starts another report from nothing and closes the picker")
	}
}

package app

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/goleak"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
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
	got, msgs := pressYank(t, m)
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
		got, msgs := pressYank(t, m)
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
	got, msgs := pressYank(t, m)
	if text, _ := clipboardOf(msgs); text != "http://10.1.0.1" || !strings.Contains(got.notice.text, "endereço interno do cluster") {
		t.Fatalf("%q %q", text, got.notice.text)
	}
}

func TestNextURLCyclesAndYankFollows(t *testing.T) {
	m := openPodAccess(t, demoModel(t, 160, 48))
	list := m.accessAddresses()
	if len(list) < 2 {
		t.Fatalf("addresses = %+v", list)
	}
	for i := range len(list) + 1 {
		a := m.accessAddresses()[m.acc.selected]
		if m.acc.selected != i%len(list) {
			t.Fatalf("selected %d at step %d", m.acc.selected, i)
		}
		_, msgs := pressYank(t, m)
		if text, _ := clipboardOf(msgs); text != a.URL.String() {
			t.Fatalf("copied %q want %q", text, a.URL.String())
		}
		m = pressDrive(t, m, "u")
	}
	m = pressDrive(t, m, "]")
	m.tab = ui.TabConfig
	m = pressDrive(t, m, "down")
	_, msgs := pressYank(t, m)
	if text, _ := clipboardOf(msgs); !strings.HasPrefix(text, "apiVersion:") {
		t.Fatalf("Config still copies the YAML, got %q", text)
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
		pods.Items[0].Ref.Namespace, pods.Items[0].Ref.Name = pod.Namespace, pod.Name
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

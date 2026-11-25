package app

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const netHost = "web.example.com"

var statefulGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}

type netLive struct {
	fakeBrowser
	exec *guard.Executor
	rows *actionRows
}

func (s netLive) Actions() *guard.Executor { return s.exec }

func (s netLive) Snapshot() data.Snapshot {
	return actionLive{Demo: s.Demo, exec: s.exec, stamp: s.rows}.Snapshot()
}

type netRig struct {
	*actionRig
	browser fakeBrowser
}

func newNetRig(t *testing.T, host string, w, h int) *netRig {
	t.Helper()
	r := newActionRig(t, w, h, guard.Environment{Valid: true}, true)
	r.rows.keep = true
	b := newNetBrowser(nil, hostPages(host))
	b.Demo = r.demo
	row := netRow("apps", "web", data.NetworkFacts{Template: &data.TemplateFacts{Labels: data.Labels{{Key: "app", Value: "web"}}}})
	row.Ref.UID, row.Ref.ResourceVersion = "uid-sts", "51"
	b.pages[statefulGVR] = netPage(row)
	r.m.src = netLive{b, r.exec, r.rows}
	r.m.clock = anim.Fixed(goldenT, goldenPhase, 30)
	r.m.reload()
	n := &netRig{r, b}
	n.pinPod()
	return n
}

func (r *netRig) pinPod() {
	pod := r.m.selectedPod()
	if pod == nil || pod.UID == "" {
		r.t.Fatal("the fixture needs a selected pod with UID")
	}
	row := &r.browser.pages[podGVR].Items[0]
	row.Ref.Namespace, row.Ref.Name, row.Ref.UID = pod.Namespace, pod.Name, pod.UID
	for _, gvr := range []schema.GroupVersionResource{serviceGVR, endpointSliceGVR, ingressGVR, statefulGVR} {
		r.browser.pages[gvr].Items[0].Ref.Namespace = pod.Namespace
	}
}

func (r *netRig) press(keys ...string) {
	r.t.Helper()
	r.m = pressDrive(r.t, r.m, keys...)
}

func (r *netRig) settle(msg tea.Msg) {
	r.t.Helper()
	next, cmd := r.m.Update(msg)
	r.m = drive(r.t, next.(Model), cmd)
}

func (r *netRig) leaf(id DestinationID) { r.m = openLeaf(r.t, r.m, id) }

func (r *netRig) podAccess() {
	r.t.Helper()
	pod := *r.m.selectedPod()
	r.m.navigate(Route{Kind: RoutePod, Screen: ScreenPodDetail, Pod: pod.Key(), Ref: ResourceRef{Version: "v1", Resource: "pods", Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID}})
	r.press("d")
	r.wantAddresses()
}

func (r *netRig) podByName() {
	r.t.Helper()
	r.m.openPod()
	r.press("d")
	r.wantAddresses()
}

func (r *netRig) statefulDetail() {
	r.t.Helper()
	r.leaf(DestStatefulSets)
	r.press("enter")
	r.wantAddresses()
}

func (r *netRig) wantAddresses() {
	r.t.Helper()
	if len(r.m.accessAddresses()) == 0 {
		r.t.Fatalf("the fixture needs addresses: %+v", r.m.acc.target)
	}
}

func (r *netRig) wide(action guard.Action) {
	r.t.Helper()
	r.press("o")
	i := slices.IndexFunc(r.m.act.items, func(s actionSpec) bool { return s.action == action })
	if i < 0 {
		r.t.Fatalf("action %v is not offered for %+v", action, r.m.act.target)
	}
	for range i {
		r.press("down")
	}
}

func (r *netRig) to(c actionControl) {
	r.t.Helper()
	for range 12 {
		if i := r.m.act.focus - len(r.m.actionFields()); i >= 0 && r.m.actionButtons()[i].control == c {
			return
		}
		r.press("tab")
	}
	r.t.Fatalf("control %d is not reachable in stage %d", c, r.m.act.stage)
}

func (r *netRig) each(check func(stage string)) {
	r.t.Helper()
	hold := func(stage string, want actionStage) {
		r.t.Helper()
		next, cmd := r.m.Update(keyPress("enter"))
		r.m = next.(Model)
		r.stage(want)
		check(stage)
		r.m = drive(r.t, r.m, cmd)
	}
	r.wide(guard.Delete)
	r.stage(actionChoosing)
	check("choosing")
	hold("observing", actionObserving)
	r.stage(actionEditing)
	check("form")
	r.to(controlPrepare)
	hold("preparing", actionPreparing)
	r.stage(actionPreview)
	check("preview")
	for _, c := range r.m.act.plan.Name {
		r.press(string(c))
	}
	r.to(controlConfirm)
	hold("sending", actionExecuting)
	r.stage(actionResult)
	check("result")
	r.to(controlVerify)
	hold("reconciling", actionReconciling)
	r.stage(actionResult)
	check("verified")
	r.press("esc")
	r.stage(actionClosed)
	r.back.locked(func() { r.back.checkErr = guard.RefusedOwner })
	r.press("o")
	r.stage(actionChoosing)
	if !r.exec.AuditBlocked() || !strings.Contains(r.dialog(), auditBlockText(r.exec.AuditRefusal())) {
		r.t.Fatal("the fixture needs the audit refusal")
	}
	check("refused by the audit")
}

func hostileTarget(m Model) Model {
	const evil = "\x1b]8;;http://evil.example.com\x1b\\x\x1b]8;;\x1b\\\u202e\u0007http://web.example.com"
	m.act.target.Name, m.act.target.Namespace, m.act.context, m.act.err = m.act.target.Name+evil, evil, evil, evil
	if m.act.plan.Name != "" {
		m.act.plan.Name, m.act.plan.UID = m.act.plan.Name+evil, evil
	}
	m.act.lines = append(slices.Clone(m.act.lines), ui.ActionLine{Text: evil})
	return m
}

func unsafeText(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r))
	})
}

func TestActionDialogNeverCarriesALink(t *testing.T) {
	long, _ := longURL()
	openers := map[string]func(*netRig){
		"list":            func(r *netRig) { r.leaf(DestStatefulSets) },
		"workload detail": (*netRig).statefulDetail,
		"pod access":      (*netRig).podAccess,
	}
	shown := 0
	for _, host := range []string{netHost, long} {
		for name, open := range openers {
			r := newNetRig(t, host, 160, 48)
			open(r)
			r.each(func(stage string) {
				for w := 80; w <= 300; w += 20 {
					next, _ := r.m.Update(tea.WindowSizeMsg{Width: w, Height: 24 + w%3*12})
					m := next.(Model)
					for what, m := range map[string]Model{"plain": m, "hostile": hostileTarget(m)} {
						c := m.Render()
						if whole, broken := partialLinks(c); whole != 0 || len(broken) > 0 || strings.Contains(c.String(), "\x1b]8") {
							t.Fatalf("%s %s %s %s at %d: %d links %q", host, name, stage, what, w, whole, broken)
						}
						if unsafeText(c.Plain()) {
							t.Fatalf("%s %s %s at %d: the target is not sanitized", name, stage, what, w)
						}
						if row := strings.Split(c.Plain(), "\n")[0]; !strings.Contains(row, "[RW") || !strings.Contains(row, "◎") {
							t.Fatalf("%s %s at %d: header %q", name, stage, w, row)
						}
					}
					closed := m
					closed.act = actionState{}
					whole, broken := partialLinks(closed.Render())
					if len(broken) > 0 {
						t.Fatalf("%s %s at %d after the dialog: %q", name, stage, w, broken)
					}
					shown += whole
				}
			})
		}
	}
	if shown == 0 {
		t.Fatal("the screens behind must carry links")
	}
}

func lateDone(r *netRig) actionLateMsg {
	return actionLateMsg{done: actionDone{session: 11, context: "anterior", action: guard.Delete, target: guard.Target{Version: "v1", Resource: "pods", Namespace: "apps", Name: "old", UID: "u"}, err: errors.New("x"), exec: r.exec}}
}

func TestLateResultOverThePickerHidesItsLinks(t *testing.T) {
	r := newNetRig(t, netHost, 160, 48)
	r.podAccess()
	r.press("u")
	r.settle(lateDone(r))
	r.stage(actionResult)
	if !r.m.urls.open {
		t.Fatal("the picker waits behind the result")
	}
	for w := 80; w <= 300; w += 10 {
		next, _ := r.m.Update(tea.WindowSizeMsg{Width: w, Height: 48})
		if whole, broken := partialLinks(next.(Model).Render()); whole != 0 || len(broken) > 0 {
			t.Fatalf("width %d: %d %q", w, whole, broken)
		}
	}
	cursor := r.m.urls.cursor
	next, cmd := r.m.Update(keyPress("u"))
	if _, ok := clipboardOf(drain(cmd)); ok || next.(Model).urls.cursor != cursor {
		t.Fatal("the dialog on top captures the picker keys")
	}
	r.press("esc")
	r.stage(actionClosed)
	if !r.m.urls.open {
		t.Fatal("esc closes only the dialog on top")
	}
}

func TestDialogCapturesEveryKey(t *testing.T) {
	r := newNetRig(t, netHost, 160, 48)
	r.podAccess()
	nav, tab := r.m.nav, r.m.tab
	r.each(func(stage string) {
		for _, k := range []string{"u", "y", "/", "o", "c", "n", "b", "m", "2", "7", "?", "q", "e", "A"} {
			next, cmd := r.m.Update(keyPress(k))
			m := next.(Model)
			if m.act.stage != r.m.act.stage || m.urls.open || m.filtering || m.dg.searching || m.helpOpen || m.menu.open || m.tree.visible != r.m.tree.visible || m.nav != nav || m.tab != tab || cmd != nil {
				t.Fatalf("%s: %q leaked through the dialog", stage, k)
			}
		}
		switch r.m.act.stage {
		case actionObserving, actionPreparing, actionExecuting, actionReconciling:
			return
		}
		next, _ := r.m.Update(keyPress("esc"))
		if m := next.(Model); m.act.stage != actionClosed || m.nav != nav || m.tab != tab {
			t.Fatalf("%s: esc closes the dialog and nothing else, stage %d", stage, m.act.stage)
		}
	})
}

func TestPickerCapturesTheActionKey(t *testing.T) {
	for name, open := range map[string]func(*netRig){"pod": (*netRig).podAccess, "workload": (*netRig).statefulDetail} {
		r := newNetRig(t, netHost, 160, 48)
		open(r)
		r.press("u", "o")
		if r.m.act.stage != actionClosed || !r.m.urls.open || r.back.calls != 0 || r.back.checks != 0 {
			t.Fatalf("%s: o leaked through the picker", name)
		}
		r.press("esc", "o")
		r.stage(actionChoosing)
	}
}

func TestNetworkScreensOfferNoAction(t *testing.T) {
	r := newNetRig(t, netHost, 160, 48)
	if r.m.selectedPod() == nil {
		t.Fatal("the fixture needs a global pod")
	}
	check := func(what string) {
		t.Helper()
		s := r.m.screen()
		if helpHas(s.Help(r.m), "o") || helpHas(s.FullHelp(r.m), "o") {
			t.Fatalf("%s: the help promises o", what)
		}
		nav := r.m.nav
		r.press("o")
		if r.m.act.stage != actionClosed || r.m.nav != nav || r.back.calls != 0 || r.back.checks != 0 {
			t.Fatalf("%s: o acts on %+v", what, r.m.act.target)
		}
	}
	for _, w := range networkLeaves {
		r.leaf(w.dest)
		check(w.label)
		if items := r.m.tableState().Page.Items; len(items) > 0 {
			r.press("enter")
			if r.m.nav.Active().Kind == RouteResource {
				check(w.label + " detail")
			}
		}
	}
	r.leaf(DestWorkloadsOver)
	check("overview")
}

func TestWorkloadDetailActsOnTheExactObject(t *testing.T) {
	r := newNetRig(t, netHost, 160, 48)
	r.leaf(DestStatefulSets)
	s := r.m.screen()
	if !helpHas(s.FullHelp(r.m), "o") {
		t.Fatal("the list announces the row actions")
	}
	r.press("enter")
	s = r.m.screen()
	if !helpHas(s.Help(r.m), "o") || !helpHas(s.FullHelp(r.m), "o") {
		t.Fatal("the detail announces the object actions")
	}
	r.press("o")
	want := guard.Target{Group: "apps", Version: "v1", Resource: "statefulsets", Namespace: r.browser.pages[statefulGVR].Items[0].Ref.Namespace, Name: "web", UID: "uid-sts", ResourceVersion: "51"}
	if r.m.act.stage != actionChoosing || r.m.act.target != want {
		t.Fatalf("stage %d target %+v", r.m.act.stage, r.m.act.target)
	}
	for name, change := range map[string]func(*accessView){
		"gone":          func(v *accessView) { v.gone = true },
		"unread":        func(v *accessView) { v.row = nil },
		"no version":    func(v *accessView) { v.row.Ref.ResourceVersion = "" },
		"recreated":     func(v *accessView) { v.row.Ref.UID = "uid-new" },
		"other name":    func(v *accessView) { v.row.Ref.Name = "db" },
		"other scope":   func(v *accessView) { v.row.Ref.Namespace = "other" },
		"nothing ready": nil,
	} {
		m := r.m
		if change == nil {
			m.acc.view = nil
		} else {
			view, row := *m.acc.view, *m.acc.view.row
			view.row = &row
			change(&view)
			m.acc.view = &view
		}
		if _, ok := m.actionTarget(); ok {
			t.Fatalf("%s is not an exact target", name)
		}
		next, _ := m.Update(tea.FocusMsg{})
		if got := next.(Model); got.act.stage != actionResult || got.act.lines[0].Text != textSelection {
			t.Fatalf("%s keeps the dialog in stage %d", name, got.act.stage)
		}
	}
}

func TestActionsWaitForTheFilterResult(t *testing.T) {
	r := newNetRig(t, netHost, 160, 48)
	other := r.browser.pages[statefulGVR].Items[0]
	other.Ref.Name, other.Ref.UID = "db", "uid-db"
	page := netPage(other, r.browser.pages[statefulGVR].Items[0])
	r.browser.pages[statefulGVR] = page
	b := searchBrowser{r.browser, make(chan string, 4), make(chan struct{})}
	r.m.src = searchLive{b, r.exec, r.rows}
	r.leaf(DestStatefulSets)
	next, _ := r.m.Update(keyPress("/"))
	next, cmd := next.(Model).Update(keyPress("w"))
	next, _ = next.(Model).Update(keyPress("enter"))
	m := next.(Model)
	if !m.searchPending() || m.filtering || len(m.tableState().Page.Items) != 2 {
		t.Fatal("the fixture needs a pending filter over the old page")
	}
	next, _ = m.Update(keyPress("o"))
	if got := next.(Model); got.act.stage != actionClosed || got.notice.text != textSearchPending {
		t.Fatalf("o acts on a row of another query: %+v", got.act.target)
	}
	close(b.release)
	m = drive(t, m, cmd)
	if m.searchPending() {
		t.Fatal("the filter never settled")
	}
	next, _ = m.Update(keyPress("o"))
	if got := next.(Model); got.act.stage != actionChoosing || got.act.target.UID != "uid-sts" {
		t.Fatalf("%+v", got.act.target)
	}
}

type searchLive struct {
	searchBrowser
	exec *guard.Executor
	rows *actionRows
}

func (s searchLive) Actions() *guard.Executor { return s.exec }

func (s searchLive) Snapshot() data.Snapshot {
	return actionLive{Demo: s.Demo, exec: s.exec, stamp: s.rows}.Snapshot()
}

func TestReplacedPodClosesTheDialogAndThePicker(t *testing.T) {
	routes := map[string]func(*netRig){
		"pinned":  (*netRig).podAccess,
		"by name": (*netRig).podByName,
	}
	replace := map[string]func(*netRig){
		"snapshot": func(r *netRig) {
			key, _ := shownPod(r.m)
			r.m = replacePod(r.m, key, "replacement-uid")
			r.m.refresh()
			r.settle(tea.FocusMsg{})
		},
		"graph": func(r *netRig) {
			row := r.browser.pages[podGVR].Items[0]
			row.Ref.UID = "replacement-uid"
			r.m.acc.subs[podGVR].handle.(*fakeHandle).page = netPage(row)
			r.m.acc.dirty = true
			r.settle(tea.FocusMsg{})
		},
	}
	for route, open := range routes {
		for how, change := range replace {
			for _, surface := range []string{"picker", "dialog", "preview"} {
				r := newNetRig(t, netHost, 160, 48)
				open(r)
				switch surface {
				case "picker":
					r.press("u")
					if !r.m.urls.open {
						t.Fatal("picker closed")
					}
				case "dialog":
					r.press("o")
					r.stage(actionChoosing)
				case "preview":
					r.wide(guard.Delete)
					r.press("enter")
					r.to(controlPrepare)
					r.press("enter")
					r.stage(actionPreview)
				}
				change(r)
				what := route + " " + how + " " + surface
				if r.m.urls.open || len(r.m.accessAddresses()) != 0 && r.m.acc.target.uid != "replacement-uid" {
					t.Fatalf("%s keeps the access of the original", what)
				}
				if surface != "picker" {
					r.stage(actionResult)
					if !strings.Contains(r.dialog(), textSelection) {
						t.Fatal(what, r.dialog())
					}
					r.press("esc")
				}
				r.back.locked(func() {
					if len(r.back.real) != 0 {
						t.Fatalf("%s sent a request", what)
					}
				})
				if route == "pinned" || how == "graph" {
					r.press("o")
					r.stage(actionClosed)
				}
			}
		}
	}
}

func TestRadarRouteClosesTheDialogWithTheReplacedPod(t *testing.T) {
	for _, graph := range []bool{false, true} {
		a := radarRig(t, 160, 48)
		sig := a.m.selectedSignal()
		b := newNetBrowser(nil, hostPages(netHost))
		b.Demo = a.demo
		row := &b.pages[podGVR].Items[0]
		row.Ref.Namespace, row.Ref.Name, row.Ref.UID = sig.Entity.Namespace, sig.Entity.Name, sig.Entity.UID
		for _, gvr := range []schema.GroupVersionResource{serviceGVR, endpointSliceGVR, ingressGVR} {
			b.pages[gvr].Items[0].Ref.Namespace = sig.Entity.Namespace
		}
		a.m.src = netLive{b, a.exec, a.rows}
		r := &netRig{a, b}
		r.press("enter", "d")
		r.wantAddresses()
		r.press("o")
		if r.m.act.target.UID != sig.Entity.UID || r.m.nav.Active().Ref.UID != sig.Entity.UID {
			t.Fatalf("%+v", r.m.act.target)
		}
		if graph {
			fresh := b.pages[podGVR].Items[0]
			fresh.Ref.UID = "replacement-uid"
			r.m.acc.subs[podGVR].handle.(*fakeHandle).page = netPage(fresh)
			r.m.acc.dirty = true
			r.settle(tea.FocusMsg{})
		} else {
			r.rows.uid = "-new"
			r.settle(dataMsg{session: 12})
		}
		r.stage(actionResult)
		if !strings.Contains(r.dialog(), textSelection) || len(r.m.accessAddresses()) != 0 || r.m.urls.open {
			t.Fatal(r.dialog())
		}
	}
}

func TestHeaderAndLinksShareTheNetworkScreens(t *testing.T) {
	const long = "arn:aws:eks:sa-east-1:123456789012:cluster/prod-eu-1"
	for name, open := range map[string]func(*netRig){
		"services": func(r *netRig) { r.leaf(DestServices) },
		"ingress":  func(r *netRig) { r.leaf(DestIngresses); r.press("enter") },
		"pod":      (*netRig).podAccess,
		"picker":   func(r *netRig) { r.podAccess(); r.press("u") },
		"workload": (*netRig).statefulDetail,
	} {
		r := newNetRig(t, netHost, 160, 48)
		open(r)
		for w := 80; w <= 300; w += 5 {
			next, _ := r.m.Update(tea.WindowSizeMsg{Width: w, Height: 24 + w%3*12})
			m := next.(Model)
			m.snap.Cluster.Context, m.snap.Cluster.Namespace = long, "observability-producao-principal"
			c := m.Render()
			row := strings.Split(c.Plain(), "\n")[0]
			if !strings.Contains(row, "-1 ") || !strings.Contains(row, "◎") || !strings.Contains(row, "ns ") && !strings.Contains(row, "namespace ") || !strings.Contains(row, "[RW") {
				t.Fatalf("%s at %d: %q", name, w, row)
			}
			if _, broken := partialLinks(c); len(broken) > 0 {
				t.Fatalf("%s at %d: %q", name, w, broken)
			}
			for x := range c.Width() {
				if !c.At(x, 0).Link.IsZero() {
					t.Fatalf("%s at %d: the header carries a link", name, w)
				}
			}
		}
	}
}

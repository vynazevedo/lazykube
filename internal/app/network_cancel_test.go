package app

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"go.uber.org/goleak"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/netpath"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func TestAccessStopsWhenTheSessionOrTheTargetGoes(t *testing.T) {
	large := func() map[schema.GroupVersionResource]data.ResourcePage {
		pages := hostPages("app.example.com")
		for gvr, count := range map[schema.GroupVersionResource]int{serviceGVR: 5000, ingressGVR: 5000, podGVR: 1000, endpointSliceGVR: 1000} {
			base := pages[gvr].Items[0]
			page := netPage()
			for i := range count {
				row, facts := base, *base.Network
				row.Ref.Name, row.Ref.UID, row.Network = fmt.Sprintf("web-%05d", i), fmt.Sprintf("web-%05d", i), &facts
				if facts.Slice != nil {
					s := *facts.Slice
					s.Service, facts.Slice = row.Ref.Name, &s
				}
				if facts.Ingress != nil {
					s := *facts.Ingress
					rule := s.Rules[0]
					rule.Paths = []data.IngressPath{{Path: "/", Backend: data.IngressBackend{Service: row.Ref.Name, PortName: "http"}}}
					s.Rules, facts.Ingress = []data.IngressRule{rule}, &s
				}
				page.Items = append(page.Items, row)
			}
			page.Total = len(page.Items)
			pages[gvr] = page
		}
		return pages
	}
	for name, stop := range map[string]func(*Model, context.CancelFunc){
		"session":  func(_ *Model, cancel context.CancelFunc) { cancel() },
		"release":  func(m *Model, _ context.CancelFunc) { m.releaseResources() },
		"leaving":  func(m *Model, _ context.CancelFunc) { m.followAccess() },
		"shutdown": func(m *Model, _ context.CancelFunc) { m.acc = m.acc.closed() },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := New(Options{Context: ctx, Source: data.NewDemo(), NoAnim: true})
			pages := large()
			m.res.catalog = &data.ResourceCatalogSnapshot{}
			m.acc = accessState{target: accessTarget{active: true, kind: netpath.TargetPod, namespace: "apps", name: "web-00000", uid: "web-00000"}, dirty: true, subs: map[schema.GroupVersionResource]*resourceSub{}}
			counter := &atomic.Int32{}
			for gvr, page := range pages {
				m.acc.subs[gvr] = &resourceSub{handle: &fakeHandle{page: page, cancels: counter, refreshes: counter}}
			}
			reached, release := make(chan struct{}), make(chan struct{})
			m.acc.subs[nodeGVR] = &resourceSub{handle: barrierHandle{&fakeHandle{page: pages[nodeGVR], cancels: counter, refreshes: counter}, reached, release}}
			cmd := m.computeAccess()
			baseline := goleak.IgnoreCurrent()
			done := make(chan accessMsg, 1)
			go func() { done <- cmd().(accessMsg) }()
			<-reached
			stop(&m, cancel)
			close(release)
			goleak.VerifyNone(t, baseline)
			msg := <-done
			if !msg.canceled || len(msg.view.report.Addresses) != 0 {
				t.Fatalf("the canceled request still derived %d addresses", len(msg.view.report.Addresses))
			}
		})
	}
}

type barrierHandle struct {
	*fakeHandle
	reached, release chan struct{}
}

func (h barrierHandle) Page(cursor string, limit int) data.ResourcePage {
	page := h.fakeHandle.Page(cursor, limit)
	close(h.reached)
	<-h.release
	return page
}

func TestTruncatedReportSaysTheRestWasNotComputed(t *testing.T) {
	pages := hostPages("app.example.com")
	ing := pages[ingressGVR].Items[0].Network.Ingress
	for i := range 100 {
		ing.Rules = append(ing.Rules, data.IngressRule{Host: fmt.Sprintf("h%d.example.com", i), Paths: ing.Rules[0].Paths})
	}
	m := pressDrive(t, openLeaf(t, nodeModel(t, newNetBrowser(nil, pages), 160, 48), DestIngresses), "enter")
	rows := m.resourceDetailState().Rows()
	if !slices.ContainsFunc(rows, func(r ui.DescribeRow) bool {
		return r.Value == "lista limitada a 64 endereços e 32 rotas; o restante não foi calculado"
	}) {
		t.Fatal("the section states the limit")
	}
}

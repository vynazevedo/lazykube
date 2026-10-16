package kube

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/TNK-Studio/lazykube/internal/data"
)

const scopedEventsLimit = 200

const (
	reasonEventsDeny     = "sem permissão: events"
	reasonClusterDeny    = "sem permissão: events em todo o cluster"
	reasonNoNamespace    = "nenhum namespace selecionado"
	reasonEventsLoading  = "sincronizando eventos do cluster"
	reasonEventsDown     = "eventos do cluster indisponíveis"
	reasonOutOfNamespace = "fora do escopo --namespace "
)

type wideEvents struct {
	state   data.FeedState
	reason  string
	factory informers.SharedInformerFactory
	inf     cache.SharedIndexInformer
}

func eventsOf(objs []any) []*corev1.Event {
	out := make([]*corev1.Event, 0, len(objs))
	for _, o := range objs {
		if e, ok := o.(*corev1.Event); ok {
			out = append(out, e)
		}
	}
	return out
}

func (s *Source) ScopedEvents(q data.EventQuery) data.EventFeed {
	feed := data.EventFeed{Scope: q.Scope}
	if s.eventInf == nil {
		feed.State, feed.Reason = data.FeedForbidden, reasonEventsDeny
		return feed
	}
	now := s.opts.Now()
	switch q.Scope {
	case data.ScopePod:
		feed.Target = q.Pod
		p := s.pod(q.Pod)
		if p == nil {
			return feed
		}
		objs, err := s.eventInf.GetIndexer().ByIndex(involvedIndex, podKey(p.Namespace, p.Name))
		if err != nil {
			feed.State, feed.Reason = data.FeedUnavailable, clip(err.Error())
			return feed
		}
		var own []*corev1.Event
		for _, e := range eventsOf(objs) {
			if e.InvolvedObject.UID == "" || e.InvolvedObject.UID == p.UID {
				own = append(own, e)
			}
		}
		feed.Items = toEvents(own, now, eventsLimit)
	case data.ScopeNamespace:
		feed.Target = q.Namespace
		switch {
		case q.Namespace == "":
			feed.State, feed.Reason = data.FeedUnavailable, reasonNoNamespace
			return feed
		case s.opts.Namespace != "" && q.Namespace != s.opts.Namespace:
			feed.State, feed.Reason = data.FeedUnavailable, reasonOutOfNamespace+s.opts.Namespace
			return feed
		}
		objs, err := s.eventInf.GetIndexer().ByIndex(cache.NamespaceIndex, q.Namespace)
		if err != nil {
			feed.State, feed.Reason = data.FeedUnavailable, clip(err.Error())
			return feed
		}
		feed.Items = toEvents(eventsOf(objs), now, scopedEventsLimit)
	case data.ScopeCluster:
		inf, state, reason := s.clusterEventsInformer()
		if inf == nil {
			feed.State, feed.Reason = state, reason
			return feed
		}
		feed.Items = toEvents(eventsOf(inf.GetStore().List()), now, scopedEventsLimit)
	}
	return feed
}

func (s *Source) clusterEventsInformer() (cache.SharedIndexInformer, data.FeedState, string) {
	if s.opts.Namespace == "" {
		return s.eventInf, data.FeedReady, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wide == nil {
		if s.ctx.Err() != nil {
			return nil, data.FeedUnavailable, reasonEventsDown
		}
		s.wide = &wideEvents{state: data.FeedLoading, reason: reasonEventsLoading}
		s.wg.Add(1)
		go s.startWideEvents()
	}
	if s.wide.state != data.FeedReady {
		return nil, s.wide.state, s.wide.reason
	}
	return s.wide.inf, data.FeedReady, ""
}

func (s *Source) setWide(state data.FeedState, reason string) {
	s.mu.Lock()
	s.wide.state, s.wide.reason = state, reason
	s.mu.Unlock()
	s.notify()
}

func (s *Source) startWideEvents() {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(s.ctx, requestTimeout)
	_, err := s.client.CoreV1().Events("").List(ctx, metav1.ListOptions{Limit: 1})
	cancel()
	switch {
	case s.ctx.Err() != nil:
		return
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		s.setWide(data.FeedForbidden, reasonClusterDeny)
		return
	case err != nil:
		s.setWide(data.FeedUnavailable, reasonEventsDown+": "+clip(err.Error()))
		return
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	factory := informers.NewSharedInformerFactoryWithOptions(s.client, 0, informers.WithTransform(stripManagedFields))
	inf := factory.Core().V1().Events().Informer()
	if _, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { s.markDirty() },
		UpdateFunc: func(any, any) { s.markDirty() },
		DeleteFunc: func(any) { s.markDirty() },
	}); err != nil {
		s.mu.Unlock()
		s.setWide(data.FeedUnavailable, reasonEventsDown)
		return
	}
	s.wide.factory, s.wide.inf = factory, inf
	factory.Start(s.ctx.Done())
	s.mu.Unlock()
	if !cache.WaitForCacheSync(s.ctx.Done(), inf.HasSynced) {
		return
	}
	s.setWide(data.FeedReady, "")
}

func (s *Source) shutdownWideEvents() {
	s.mu.Lock()
	w := s.wide
	s.mu.Unlock()
	if w != nil && w.factory != nil {
		w.factory.Shutdown()
	}
}

var _ data.Diagnostics = (*Source)(nil)

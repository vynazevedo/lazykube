package kube

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/TNK-Studio/lazykube/internal/data"
)

const scopedEventsLimit = 200

type wideTimingConfig struct {
	sync     time.Duration
	backoff  time.Duration
	cooldown time.Duration
	retries  int
}

var wideTiming = wideTimingConfig{sync: defaultTimeout, backoff: 2 * time.Second, cooldown: 30 * time.Second, retries: 3}

const (
	reasonEventsDeny        = "sem permissão: events"
	reasonClusterDeny       = "sem permissão: events em todo o cluster"
	reasonNoNamespace       = "nenhum namespace selecionado"
	reasonEventsLoading     = "sincronizando eventos do cluster"
	reasonEventsDown        = "eventos do cluster indisponíveis"
	reasonOutOfNamespace    = "fora do escopo --namespace "
	reasonEventsSyncTimeout = "sincronização de eventos do cluster expirou"
	reasonClusterLost       = "permissão de events no cluster perdida"
)

type wideEvents struct {
	running bool
	synced  bool
	state   data.FeedState
	reason  string
	retryAt time.Time
	cancel  context.CancelFunc
	inf     cache.SharedIndexInformer
}

type wideFailure struct {
	state  data.FeedState
	reason string
}

func (f wideFailure) Error() string {
	return f.reason
}

func denied(err error) bool {
	return apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err)
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
	w := s.wide
	if w == nil {
		w = &wideEvents{state: data.FeedUnavailable, reason: reasonEventsDown}
		s.wide = w
	}
	if !w.running && !w.synced && s.ctx.Err() == nil && !s.opts.Now().Before(w.retryAt) {
		w.running, w.state, w.reason = true, data.FeedLoading, reasonEventsLoading
		s.wg.Add(1)
		go s.runWideEvents(w)
	}
	if w.state != data.FeedReady || w.inf == nil {
		return nil, w.state, w.reason
	}
	return w.inf, data.FeedReady, ""
}

func (s *Source) setWideState(w *wideEvents, state data.FeedState, reason string) {
	s.mu.Lock()
	changed := w.state != state || w.reason != reason
	w.state, w.reason = state, reason
	s.mu.Unlock()
	if changed {
		s.notify()
	}
}

func (s *Source) runWideEvents(w *wideEvents) {
	defer s.wg.Done()
	for attempt := 1; ; attempt++ {
		err := s.wideAttempt(w)
		if err == nil || s.ctx.Err() != nil {
			return
		}
		f, ok := err.(wideFailure)
		if !ok {
			f = wideFailure{state: data.FeedUnavailable, reason: reasonEventsDown + ": " + clip(err.Error())}
		}
		if f.state == data.FeedForbidden || attempt >= wideTiming.retries {
			s.mu.Lock()
			w.running, w.state, w.reason = false, f.state, f.reason
			w.retryAt = s.opts.Now().Add(wideTiming.cooldown)
			s.mu.Unlock()
			s.notify()
			return
		}
		s.setWideState(w, data.FeedUnavailable, f.reason)
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(wideTiming.backoff << (attempt - 1)):
		}
		s.setWideState(w, data.FeedLoading, reasonEventsLoading)
	}
}

func classify(err error) wideFailure {
	if denied(err) {
		return wideFailure{state: data.FeedForbidden, reason: reasonClusterDeny}
	}
	return wideFailure{state: data.FeedUnavailable, reason: reasonEventsDown + ": " + clip(err.Error())}
}

func (s *Source) wideObserved(w *wideEvents, err error) {
	if s.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	if !w.synced {
		s.mu.Unlock()
		return
	}
	state, reason := data.FeedReady, ""
	switch {
	case err == nil:
	case denied(err):
		state, reason = data.FeedForbidden, reasonClusterLost
	default:
		state, reason = data.FeedUnavailable, reasonEventsDown+": "+clip(err.Error())
	}
	s.mu.Unlock()
	s.setWideState(w, state, reason)
}

func (s *Source) newWideInformer(w *wideEvents, errs chan<- error) (cache.SharedIndexInformer, error) {
	events := s.client.CoreV1().Events("")
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			obj, err := events.List(ctx, o)
			s.wideObserved(w, err)
			return obj, err
		},
		WatchFuncWithContext: func(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
			wt, err := events.Watch(ctx, o)
			s.wideObserved(w, err)
			return wt, err
		},
	}
	inf := cache.NewSharedIndexInformerWithOptions(cache.ToListWatcherWithWatchListSemantics(lw, s.client), &corev1.Event{},
		cache.SharedIndexInformerOptions{Indexers: cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}})
	if err := inf.SetTransform(stripManagedFields); err != nil {
		return nil, err
	}
	if err := inf.SetWatchErrorHandlerWithContext(func(_ context.Context, _ *cache.Reflector, err error) {
		s.wideObserved(w, err)
		select {
		case errs <- err:
		default:
		}
	}); err != nil {
		return nil, err
	}
	if _, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { s.markDirty() },
		UpdateFunc: func(any, any) { s.markDirty() },
		DeleteFunc: func(any) { s.markDirty() },
	}); err != nil {
		return nil, err
	}
	return inf, nil
}

func (s *Source) wideAttempt(w *wideEvents) error {
	ctx, cancel := context.WithTimeout(s.ctx, requestTimeout)
	_, err := s.client.CoreV1().Events("").List(ctx, metav1.ListOptions{Limit: 1})
	cancel()
	if err != nil {
		return classify(err)
	}
	errs := make(chan error, 1)
	inf, err := s.newWideInformer(w, errs)
	if err != nil {
		return err
	}
	runCtx, stop := context.WithCancel(s.ctx)
	s.mu.Lock()
	w.inf, w.cancel = inf, stop
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		inf.RunWithContext(runCtx)
	}()
	abort := func(f error) error {
		stop()
		s.mu.Lock()
		w.inf, w.cancel = nil, nil
		s.mu.Unlock()
		return f
	}
	deadline := time.NewTimer(wideTiming.sync)
	defer deadline.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return abort(s.ctx.Err())
		case err := <-errs:
			return abort(classify(err))
		case <-deadline.C:
			return abort(wideFailure{state: data.FeedUnavailable, reason: reasonEventsSyncTimeout})
		case <-poll.C:
			if !inf.HasSynced() {
				continue
			}
			s.mu.Lock()
			w.synced = true
			s.mu.Unlock()
			s.setWideState(w, data.FeedReady, "")
			return nil
		}
	}
}

func (s *Source) shutdownWideEvents() {
	s.mu.Lock()
	var cancel context.CancelFunc
	if s.wide != nil {
		cancel = s.wide.cancel
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

var _ data.Diagnostics = (*Source)(nil)

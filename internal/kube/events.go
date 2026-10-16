package kube

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
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
	reasonEventsLost        = "permissão de events perdida"
	reasonEventsResync      = "ressincronizando eventos"
	initialEventsEnd        = "k8s.io/initial-events-end"
)

type wideEvents struct {
	running bool
	synced  bool
	state   data.FeedState
	reason  string
	retryAt time.Time
	cancel  context.CancelFunc
	inf     *eventsInformer
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
		if feed.State, feed.Reason = eventsStatus(s.eventInf); feed.State != data.FeedReady {
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
		if feed.State, feed.Reason = eventsStatus(s.eventInf); feed.State != data.FeedReady {
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
		if feed.State, feed.Reason = eventsStatus(inf); feed.State != data.FeedReady {
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

type eventsStamp struct{ uid, version string }

type eventsInformer struct {
	cache.SharedIndexInformer
	mu       sync.Mutex
	expected map[string]eventsStamp
	listed   bool
	watching bool
	degraded bool
	epoch    uint64
	state    data.FeedState
	reason   string
	lost     string
	dirty    func()
	failed   func(error)
}

func eventsStatus(inf cache.SharedIndexInformer) (data.FeedState, string) {
	tracked, ok := inf.(*eventsInformer)
	if !ok {
		return data.FeedReady, ""
	}
	return tracked.status()
}

func (s *Source) newEventsInformer() cache.SharedIndexInformer {
	return s.factory.InformerFor(&corev1.Event{}, func(client kubernetes.Interface, resync time.Duration) cache.SharedIndexInformer {
		lost := reasonEventsLost
		if s.opts.Namespace == "" {
			lost = reasonClusterLost
		}
		return s.buildEventsInformer(client, s.opts.Namespace, resync, lost, nil)
	})
}

func (s *Source) buildEventsInformer(client kubernetes.Interface, namespace string, resync time.Duration, lost string, failed func(error)) *eventsInformer {
	t := &eventsInformer{lost: lost, dirty: s.markDirty, failed: failed, degraded: true, state: data.FeedLoading, reason: reasonEventsLoading}
	events := client.CoreV1().Events(namespace)
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			obj, err := events.List(ctx, o)
			if err != nil {
				t.failList(err)
				return nil, err
			}
			t.listedPage(obj, o.Continue == "")
			return obj, nil
		},
		WatchFuncWithContext: func(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
			wt, err := events.Watch(ctx, o)
			if err != nil {
				t.failWatch(err)
				return nil, err
			}
			return t.observe(ctx, wt, o.SendInitialEvents != nil && *o.SendInitialEvents), nil
		},
	}
	t.SharedIndexInformer = cache.NewSharedIndexInformerWithOptions(cache.ToListWatcherWithWatchListSemantics(lw, client), &corev1.Event{},
		cache.SharedIndexInformerOptions{ResyncPeriod: resync, Indexers: cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}})
	if err := t.SetWatchErrorHandlerWithContext(func(_ context.Context, _ *cache.Reflector, err error) {
		if !routine(err) {
			t.failWatch(err)
		}
	}); err != nil {
		t.failList(err)
	}
	return t
}

func routine(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || apierrors.IsResourceExpired(err) || apierrors.IsGone(err)
}

func (t *eventsInformer) failList(err error) {
	t.fail(err, true)
}

func (t *eventsInformer) failWatch(err error) {
	t.fail(err, false)
}

func (t *eventsInformer) fail(err error, list bool) {
	if errors.Is(err, context.Canceled) {
		return
	}
	t.mu.Lock()
	if list {
		t.listed = false
	}
	t.watching = false
	t.degradeLocked(err)
	t.mu.Unlock()
	t.dirty()
	if t.failed != nil {
		t.failed(err)
	}
}

func (t *eventsInformer) listedPage(list runtime.Object, first bool) {
	items, err := meta.ExtractList(list)
	if err != nil {
		t.failList(err)
		return
	}
	metadata, err := meta.ListAccessor(list)
	if err != nil {
		t.failList(err)
		return
	}
	t.mu.Lock()
	if first || t.expected == nil {
		t.resync()
		t.expected = make(map[string]eventsStamp, len(items))
		t.watching = false
	}
	for _, obj := range items {
		t.record(watch.Added, obj)
	}
	t.listed = metadata.GetContinue() == ""
	t.mu.Unlock()
	t.dirty()
}

func (t *eventsInformer) record(kind watch.EventType, obj runtime.Object) {
	m, err := meta.Accessor(obj)
	if err != nil {
		t.listed = false
		return
	}
	key := podKey(m.GetNamespace(), m.GetName())
	if kind == watch.Deleted {
		delete(t.expected, key)
		return
	}
	t.expected[key] = eventsStamp{string(m.GetUID()), m.GetResourceVersion()}
}

type eventsWatch struct {
	upstream watch.Interface
	out      chan watch.Event
	done     chan struct{}
	once     sync.Once
}

func (w *eventsWatch) Stop() { w.once.Do(func() { close(w.done); w.upstream.Stop() }) }

func (w *eventsWatch) ResultChan() <-chan watch.Event { return w.out }

func (t *eventsInformer) observe(ctx context.Context, upstream watch.Interface, initial bool) watch.Interface {
	w := &eventsWatch{upstream: upstream, out: make(chan watch.Event), done: make(chan struct{})}
	t.mu.Lock()
	t.epoch++
	epoch := t.epoch
	if initial {
		t.resync()
		t.expected = make(map[string]eventsStamp)
		t.listed = false
	}
	t.watching = true
	t.mu.Unlock()
	t.dirty()
	go func() {
		defer close(w.out)
		defer w.Stop()
		defer t.unwatch(epoch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.done:
				return
			case event, ok := <-upstream.ResultChan():
				if !ok {
					return
				}
				if err := t.apply(epoch, initial, event); err != nil {
					t.failWatch(err)
				}
				select {
				case <-ctx.Done():
					return
				case <-w.done:
					return
				case w.out <- event:
				}
				t.dirty()
			}
		}
	}()
	return w
}

func (t *eventsInformer) unwatch(epoch uint64) {
	t.mu.Lock()
	if t.epoch == epoch {
		t.watching = false
	}
	t.mu.Unlock()
	t.dirty()
}

func (t *eventsInformer) apply(epoch uint64, initial bool, event watch.Event) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.epoch != epoch || t.expected == nil {
		return nil
	}
	switch event.Type {
	case watch.Added, watch.Modified, watch.Deleted:
		t.record(event.Type, event.Object)
	case watch.Bookmark:
		m, err := meta.Accessor(event.Object)
		if err == nil && initial && m.GetAnnotations()[initialEventsEnd] == "true" {
			t.listed = true
		}
	case watch.Error:
		t.watching = false
		err := apierrors.FromObject(event.Object)
		if !routine(err) {
			return err
		}
		t.resync()
	}
	return nil
}

func (t *eventsInformer) degradeLocked(err error) {
	t.degraded = true
	t.state, t.reason = data.FeedUnavailable, reasonEventsDown+": "+clip(err.Error())
	if denied(err) {
		t.state, t.reason = data.FeedForbidden, t.lost
	}
}

func (t *eventsInformer) resync() {
	if t.degraded {
		return
	}
	t.degraded, t.state, t.reason = true, data.FeedLoading, reasonEventsResync
}

func (t *eventsInformer) status() (data.FeedState, string) {
	objects := t.GetStore().List()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.degraded && t.consumed(objects) {
		t.degraded = false
	}
	if !t.degraded {
		return data.FeedReady, ""
	}
	return t.state, t.reason
}

func (t *eventsInformer) consumed(objects []any) bool {
	if !t.listed || !t.watching || len(objects) != len(t.expected) {
		return false
	}
	for _, obj := range objects {
		m, err := meta.Accessor(obj)
		if err != nil {
			return false
		}
		stamp, ok := t.expected[podKey(m.GetNamespace(), m.GetName())]
		if !ok || stamp != (eventsStamp{string(m.GetUID()), m.GetResourceVersion()}) {
			return false
		}
	}
	return true
}

func (s *Source) wideAttempt(w *wideEvents) error {
	ctx, cancel := context.WithTimeout(s.ctx, requestTimeout)
	_, err := s.client.CoreV1().Events("").List(ctx, metav1.ListOptions{Limit: 1})
	cancel()
	if err != nil {
		return classify(err)
	}
	errs := make(chan error, 1)
	inf := s.buildEventsInformer(s.client, "", 0, reasonClusterLost, func(err error) {
		select {
		case errs <- err:
		default:
		}
	})
	if err := inf.SetTransform(stripManagedFields); err != nil {
		return err
	}
	if _, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { s.markDirty() },
		UpdateFunc: func(any, any) { s.markDirty() },
		DeleteFunc: func(any) { s.markDirty() },
	}); err != nil {
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
			if state, _ := inf.status(); state != data.FeedReady {
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

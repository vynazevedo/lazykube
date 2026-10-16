package kube

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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

var securityGeneration atomic.Uint64

func (s *Source) rebuildSecurity(pods []*corev1.Pod, namespaces []data.Namespace, now time.Time) {
	s.recoverSecurityCoverage(pods)
	s.mu.RLock()
	previous := s.security
	objects := s.securityObjects
	coverage := s.podCoverage
	s.mu.RUnlock()
	changed := len(previous.Pods) != len(pods) || previous.Coverage.ObservedAt.IsZero()
	for _, p := range previous.Pods {
		expected := p.LocalCoverage
		if coverage.State != data.CoverageComplete {
			expected = coverage
		}
		if p.Coverage.State != expected.State || p.Coverage.Reason != expected.Reason {
			changed = true
		}
	}
	if len(previous.Pods) == 0 && previous.Coverage.State != coverage.State {
		changed = true
	}
	for _, p := range pods {
		if securityPodChanged(previous.Pods, objects, p) {
			changed = true
			break
		}
	}
	names := make([]string, len(namespaces))
	for i, ns := range namespaces {
		names[i] = ns.Name
		if _, ok := previous.Namespaces[ns.Name]; !ok {
			changed = true
		}
	}
	expectedNamespaces := make(map[string]bool)
	for _, name := range names {
		expectedNamespaces[name] = true
	}
	for _, p := range pods {
		expectedNamespaces[p.Namespace] = true
	}
	if len(expectedNamespaces) != len(previous.Namespaces) {
		changed = true
	}
	if !changed {
		return
	}
	out := make(map[string]data.PodPosture, len(pods))
	nextObjects := make(map[string]*corev1.Pod, len(pods))
	for _, p := range pods {
		key := podKey(p.Namespace, p.Name)
		old := previous.Pods[key]
		nextObjects[key] = p
		if securityPodChanged(previous.Pods, objects, p) {
			old = evaluatePod(p, s.opts.Context, s.generation, now)
			old.LocalCoverage = old.Coverage
		}
		old.Coverage = old.LocalCoverage
		if coverage.State != data.CoverageComplete {
			old.Coverage.State, old.Coverage.Reason = coverage.State, coverage.Reason
		}
		out[key] = old
	}
	coverage.ObservedAt = now
	next := data.SummarizeSecurity(out, names, coverage)
	s.mu.Lock()
	next.Revision = s.security.Revision + 1
	s.security = next
	s.securityObjects = nextObjects
	s.mu.Unlock()
}

func (s *Source) Security() data.SecuritySnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.security.Clone()
}

func (s *Source) watchSecurityError(deployments bool) cache.WatchErrorHandler {
	return func(_ *cache.Reflector, err error) {
		inf := s.podInf
		if deployments {
			inf = s.deployInf
		}
		if tracked, ok := inf.(*securityInformer); ok {
			tracked.fail(err)
			return
		}
		s.recordSecurityFailure(deployments, err)
	}
}

func (s *Source) recordSecurityFailure(deployments bool, err error) {
	state, reason := data.CoverageStale, "list/watch interrupted; cached observations may be outdated"
	if apierrors.IsForbidden(err) {
		state, reason = data.CoverageDenied, "list/watch permission denied; cached observations may be outdated"
	}
	s.mu.Lock()
	if deployments {
		s.deploymentCoverage.State, s.deploymentCoverage.Reason = state, reason
	} else {
		s.podCoverage.State, s.podCoverage.Reason = state, reason
	}
	s.mu.Unlock()
	s.markDirty()
}

func securityPodChanged(previous map[string]data.PodPosture, objects map[string]*corev1.Pod, p *corev1.Pod) bool {
	key := podKey(p.Namespace, p.Name)
	old, exists := previous[key]
	return !exists || old.Target.UID != string(p.UID) || old.Target.ResourceVersion != p.ResourceVersion || ((p.UID == "" || p.ResourceVersion == "") && objects[key] != p)
}

func (s *Source) SecurityRevision() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.security.Revision
}

type securityStamp struct{ uid, version string }
type securityInformer struct {
	cache.SharedIndexInformer
	mu               sync.Mutex
	expected         map[string]securityStamp
	listed, watching bool
	epoch            uint64
	dirty            func()
	reportFailure    func(error)
}

func (s *Source) newSecurityInformer(deployments bool) cache.SharedIndexInformer {
	var obj runtime.Object = &corev1.Pod{}
	if deployments {
		obj = &appsv1.Deployment{}
	}
	return s.factory.InformerFor(obj, func(client kubernetes.Interface, resync time.Duration) cache.SharedIndexInformer {
		tracked := &securityInformer{dirty: s.markDirty, reportFailure: func(err error) { s.recordSecurityFailure(deployments, err) }}
		lw := &cache.ListWatch{
			ListWithContextFunc: func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
				var list runtime.Object
				var err error
				if deployments {
					list, err = client.AppsV1().Deployments(s.opts.Namespace).List(ctx, opts)
				} else {
					list, err = client.CoreV1().Pods(s.opts.Namespace).List(ctx, opts)
				}
				if err != nil {
					if ctx.Err() == nil {
						tracked.fail(err)
					} else {
						tracked.invalidate()
					}
					return nil, err
				}
				tracked.listedPage(list, opts.Continue == "")
				return list, nil
			},
			WatchFuncWithContext: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
				var stream watch.Interface
				var err error
				if deployments {
					stream, err = client.AppsV1().Deployments(s.opts.Namespace).Watch(ctx, opts)
				} else {
					stream, err = client.CoreV1().Pods(s.opts.Namespace).Watch(ctx, opts)
				}
				if err != nil {
					if ctx.Err() == nil {
						tracked.fail(err)
					} else {
						tracked.invalidate()
					}
					return nil, err
				}
				return tracked.observe(ctx, stream, opts.SendInitialEvents != nil && *opts.SendInitialEvents), nil
			},
		}
		tracked.SharedIndexInformer = cache.NewSharedIndexInformer(cache.ToListWatcherWithWatchListSemantics(lw, client), obj, resync, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
		return tracked
	})
}

func (t *securityInformer) fail(err error) {
	t.mu.Lock()
	t.failLocked(err)
	t.mu.Unlock()
	t.dirty()
}

func (t *securityInformer) failLocked(err error) {
	t.watching = false
	if t.reportFailure != nil {
		t.reportFailure(err)
	}
}

func (t *securityInformer) invalidate() {
	t.mu.Lock()
	t.watching = false
	t.mu.Unlock()
	t.dirty()
}

func (t *securityInformer) listedPage(list runtime.Object, first bool) {
	items, err := meta.ExtractList(list)
	if err != nil {
		t.fail(err)
		return
	}
	metadata, err := meta.ListAccessor(list)
	if err != nil {
		t.fail(err)
		return
	}
	t.mu.Lock()
	if first {
		t.expected = make(map[string]securityStamp)
		t.listed = false
		t.watching = false
	}
	for _, obj := range items {
		t.record(watch.Added, obj)
	}
	t.listed = metadata.GetContinue() == ""
	t.mu.Unlock()
	t.dirty()
}

func (t *securityInformer) record(kind watch.EventType, obj runtime.Object) {
	metadata, err := meta.Accessor(obj)
	if err != nil {
		t.listed = false
		return
	}
	key := podKey(metadata.GetNamespace(), metadata.GetName())
	if kind == watch.Deleted {
		delete(t.expected, key)
		return
	}
	t.expected[key] = securityStamp{string(metadata.GetUID()), metadata.GetResourceVersion()}
}

type securityWatch struct {
	upstream watch.Interface
	out      chan watch.Event
	done     chan struct{}
	once     sync.Once
}

func (w *securityWatch) Stop()                          { w.once.Do(func() { close(w.done); w.upstream.Stop() }) }
func (w *securityWatch) ResultChan() <-chan watch.Event { return w.out }

func (t *securityInformer) observe(ctx context.Context, upstream watch.Interface, initial bool) watch.Interface {
	w := &securityWatch{upstream: upstream, out: make(chan watch.Event), done: make(chan struct{})}
	t.mu.Lock()
	t.epoch++
	epoch := t.epoch
	if initial {
		t.expected = make(map[string]securityStamp)
		t.listed = false
	}
	t.watching = true
	t.mu.Unlock()
	t.dirty()
	go func() {
		defer close(w.out)
		defer w.Stop()
		defer func() {
			t.mu.Lock()
			if t.epoch == epoch {
				t.watching = false
			}
			t.mu.Unlock()
			t.dirty()
		}()
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
				t.mu.Lock()
				if t.epoch == epoch {
					switch event.Type {
					case watch.Added, watch.Modified, watch.Deleted:
						t.record(event.Type, event.Object)
					case watch.Error:
						t.failLocked(apierrors.FromObject(event.Object))
					case watch.Bookmark:
						m, err := meta.Accessor(event.Object)
						if err == nil && initial && m.GetAnnotations()["k8s.io/initial-events-end"] == "true" {
							t.listed = true
						}
					}
				}
				t.mu.Unlock()
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

func (t *securityInformer) consumed(objects []any) bool {
	if !t.listed || !t.watching || len(objects) != len(t.expected) {
		return false
	}
	for _, obj := range objects {
		m, err := meta.Accessor(obj)
		if err != nil {
			return false
		}
		stamp, ok := t.expected[podKey(m.GetNamespace(), m.GetName())]
		if !ok || stamp != (securityStamp{string(m.GetUID()), m.GetResourceVersion()}) {
			return false
		}
	}
	return true
}

func (s *Source) recoverSecurityCoverage(pods []*corev1.Pod) {
	if tracked, ok := s.podInf.(*securityInformer); ok {
		objects := make([]any, len(pods))
		for i, p := range pods {
			objects[i] = p
		}
		tracked.mu.Lock()
		if tracked.consumed(objects) {
			s.mu.Lock()
			s.podCoverage.State = data.CoverageComplete
			s.podCoverage.Reason = ""
			s.mu.Unlock()
		}
		tracked.mu.Unlock()
	}
	if tracked, ok := s.deployInf.(*securityInformer); ok {
		objects := tracked.GetStore().List()
		tracked.mu.Lock()
		if tracked.consumed(objects) {
			s.mu.Lock()
			s.deploymentCoverage.State = data.CoverageComplete
			s.deploymentCoverage.Reason = ""
			s.mu.Unlock()
		}
		tracked.mu.Unlock()
	}
}

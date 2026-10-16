package kube

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/vynazevedo/lazykube/internal/data"
)

const (
	reasonNodesStale     = "watch de nodes interrompido; cache pode estar desatualizado"
	reasonNodesLostGrant = "sem permissão: nodes; cache pode estar desatualizado"
)

func (s *Source) newNodeInformer(factory informers.SharedInformerFactory) cache.SharedIndexInformer {
	return factory.InformerFor(&corev1.Node{}, func(client kubernetes.Interface, resync time.Duration) cache.SharedIndexInformer {
		tracked := &securityInformer{dirty: s.markDirty, reportFailure: s.recordNodeFailure}
		nodes := client.CoreV1().Nodes()
		lw := &cache.ListWatch{
			ListWithContextFunc: func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
				list, err := nodes.List(ctx, opts)
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
				stream, err := nodes.Watch(ctx, opts)
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
		tracked.SharedIndexInformer = cache.NewSharedIndexInformer(cache.ToListWatcherWithWatchListSemantics(lw, client), &corev1.Node{}, resync, cache.Indexers{})
		return tracked
	})
}

func (s *Source) watchNodeError(_ *cache.Reflector, err error) {
	if routine(err) {
		return
	}
	if tracked, ok := s.nodeInf.(*securityInformer); ok {
		tracked.fail(err)
		return
	}
	s.recordNodeFailure(err)
}

func (s *Source) recordNodeFailure(err error) {
	if routine(err) {
		return
	}
	cov := data.Coverage{State: data.CoverageStale, Reason: reasonNodesStale}
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		cov = data.Coverage{State: data.CoverageDenied, Reason: reasonNodesLostGrant}
	}
	s.mu.Lock()
	s.nodeHealth = cov
	s.mu.Unlock()
	s.markDirty()
}

func (s *Source) recoverNodeCoverage() {
	tracked, ok := s.nodeInf.(*securityInformer)
	if !ok {
		return
	}
	objects := tracked.GetStore().List()
	tracked.mu.Lock()
	defer tracked.mu.Unlock()
	if tracked.consumed(objects) {
		s.mu.Lock()
		s.nodeHealth = data.Coverage{}
		s.mu.Unlock()
	}
}

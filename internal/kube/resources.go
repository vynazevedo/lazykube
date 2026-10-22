package kube

import (
	"context"
	"errors"
	"fmt"

	"github.com/vynazevedo/lazykube/internal/data"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

func (s *Source) resourceSession() string { return fmt.Sprintf("resources-%d", s.generation) }

func (s *Source) initResources(client dynamic.Interface, dc discovery.DiscoveryInterface) {
	s.catalog = &resourceCatalog{client: dc, now: s.opts.Now, snapshot: data.ResourceCatalogSnapshot{Session: s.resourceSession(), Generation: s.generation}}
	s.resources = newResourcePool(s.ctx, client, s)
}

func (s *Source) Catalog(ctx context.Context, refresh bool) data.ResourceCatalogSnapshot {
	if s.catalog == nil {
		return data.ResourceCatalogSnapshot{Coverage: data.Coverage{State: data.CoverageUnavailable, Reason: "resource discovery unavailable"}}
	}
	bound, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	defer cancel()
	if s.ctx.Err() != nil {
		cancel()
	}
	return s.catalog.get(bound, refresh)
}

func (s *Source) Subscribe(ctx context.Context, key data.ResourceSubscriptionKey) (data.ResourceSubscription, error) {
	if s.resources == nil || s.catalog == nil {
		return nil, errors.New("resource browser unavailable")
	}
	s.catalog.mu.Lock()
	descriptor := s.catalog.snapshot.Lookup(key.GVR)
	s.catalog.mu.Unlock()
	return s.resources.subscribe(ctx, key, descriptor)
}

var _ data.ResourceBrowser = (*Source)(nil)

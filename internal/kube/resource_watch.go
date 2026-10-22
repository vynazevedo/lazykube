package kube

import (
	"context"
	"strings"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

type resourceResult uint8

const (
	resourceStop resourceResult = iota
	resourceRetry
)

func (e *resourceEntry) run() {
	if e.borrowed {
		e.runBorrowed()
		return
	}
	failures := 0
	for e.ctx.Err() == nil {
		result := e.load()
		if result == resourceStop {
			select {
			case <-e.ctx.Done():
				return
			case <-e.refresh:
				failures = 0
			}
		} else {
			timer := time.NewTimer(e.pool.retry(failures))
			failures++
			select {
			case <-e.ctx.Done():
				timer.Stop()
				return
			case <-e.refresh:
				timer.Stop()
				failures = 0
			case <-timer.C:
			}
		}
	}
}

func (e *resourceEntry) api() dynamic.ResourceInterface {
	r := e.pool.client.Resource(e.key.GVR)
	if e.descriptor.Namespaced {
		return r.Namespace(e.key.Namespace)
	}
	return r
}

func (e *resourceEntry) load() resourceResult {
	if e.pool.client == nil {
		e.state(data.CoverageUnavailable, "resource client unavailable")
		return resourceStop
	}
	if !e.descriptor.List && !e.descriptor.Watch {
		e.state(data.CoverageUnsupported, "neither list nor watch served")
		return resourceStop
	}
	complete := false
	rv := ""
	if e.descriptor.List {
		var result resourceResult
		var denied bool
		rv, complete, denied, result = e.list()
		if !complete && !denied {
			return result
		}
		if denied && !e.descriptor.Watch {
			return resourceStop
		}
	} else {
		e.clear()
		e.state(data.CoveragePartial, "watch-only; initial inventory unknown")
	}
	if !e.descriptor.Watch {
		e.state(data.CoverageListOnly, "watch unsupported; refresh for a new snapshot")
		return resourceStop
	}
	p := e.pool
	p.mu.Lock()
	if p.watches >= resourceWatchLimit {
		p.mu.Unlock()
		if complete {
			e.state(data.CoverageListOnly, "watch budget exhausted; refresh manually")
		} else {
			e.state(data.CoveragePartial, "watch budget exhausted; inventory unknown")
		}
		return resourceStop
	}
	p.watches++
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.watches--; p.mu.Unlock() }()
	return e.watch(rv, complete)
}

func (e *resourceEntry) list() (string, bool, bool, resourceResult) {
	e.clear()
	e.state(data.CoverageCollecting, "loading resource pages")
	cursor, rv := "", ""
	seen := make(map[string]struct{})
	for e.ctx.Err() == nil {
		ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
		select {
		case e.pool.lists <- struct{}{}:
		case <-ctx.Done():
			cancel()
			e.state(data.CoverageStale, "list canceled or timed out")
			return "", false, false, resourceRetry
		}
		page, err := e.api().List(ctx, metav1.ListOptions{Limit: resourcePageSize, Continue: cursor, LabelSelector: e.key.LabelSelector, FieldSelector: e.key.FieldSelector})
		<-e.pool.lists
		cancel()
		if err != nil {
			if strings.Contains(err.Error(), errResourceResponseLimit.Error()) {
				e.state(data.CoveragePartial, "response budget exceeded")
				return "", false, false, resourceStop
			}
			if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
				e.state(data.CoverageDenied, "list denied")
				return "", false, true, resourceStop
			}
			if apierrors.IsNotFound(err) {
				e.state(data.CoverageUnsupported, "resource no longer served; refresh discovery")
				return "", false, false, resourceStop
			}
			e.state(data.CoverageStale, "list unavailable or cursor expired")
			return "", false, false, resourceRetry
		}
		if len(page.Items) > resourcePageSize {
			e.state(data.CoveragePartial, "server ignored page size limit")
			return "", false, false, resourceStop
		}
		if rv == "" {
			rv = page.GetResourceVersion()
		} else if page.GetResourceVersion() != rv {
			e.state(data.CoverageStale, "list resource version changed between pages")
			return "", false, false, resourceRetry
		}
		for i := range page.Items {
			row, ok := projectResource(&page.Items[i])
			if !ok {
				e.state(data.CoveragePartial, "object exceeds metadata limits")
				return "", false, false, resourceStop
			}
			if !e.put(row, false) {
				return "", false, false, resourceStop
			}
		}
		cursor = page.GetContinue()
		if cursor == "" {
			e.pool.mu.Lock()
			e.observedAt = e.pool.now()
			e.pool.mu.Unlock()
			return rv, true, false, resourceStop
		}
		if _, ok := seen[cursor]; ok || len(cursor) > 4096 || len(seen) >= resourceRowLimit/resourcePageSize+1 {
			e.state(data.CoveragePartial, "invalid or excessive list cursor")
			return "", false, false, resourceStop
		}
		seen[cursor] = struct{}{}
	}
	return "", false, false, resourceStop
}

func (e *resourceEntry) watch(rv string, complete bool) resourceResult {
	if complete && rv == "" {
		e.state(data.CoverageListOnly, "list has no resource version; watch continuity unknown")
		return resourceStop
	}
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	timer := time.AfterFunc(10*time.Second, cancel)
	stream, err := e.api().Watch(ctx, metav1.ListOptions{ResourceVersion: rv, AllowWatchBookmarks: true, LabelSelector: e.key.LabelSelector, FieldSelector: e.key.FieldSelector})
	timer.Stop()
	if err != nil {
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
			if complete {
				e.state(data.CoverageListOnly, "watch denied; point-in-time list only")
			} else {
				e.state(data.CoverageDenied, "list and watch unavailable")
			}
			return resourceStop
		}
		e.state(data.CoverageStale, "watch connection unavailable")
		return resourceRetry
	}
	defer stream.Stop()
	if complete {
		e.state(data.CoverageComplete, "")
	} else {
		e.state(data.CoveragePartial, "watch-only; initial inventory unknown")
	}
	for {
		select {
		case <-e.ctx.Done():
			return resourceStop
		case <-e.refresh:
			return resourceRetry
		case event, ok := <-stream.ResultChan():
			if !ok {
				e.state(data.CoverageStale, "watch closed; relist required")
				return resourceRetry
			}
			if event.Type == watch.Error {
				err := apierrors.FromObject(event.Object)
				if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
					e.state(data.CoverageDenied, "watch access revoked")
					return resourceStop
				}
				e.state(data.CoverageStale, "watch cursor lost; relist required")
				return resourceRetry
			}
			if event.Type == watch.Bookmark {
				continue
			}
			if event.Type != watch.Added && event.Type != watch.Modified && event.Type != watch.Deleted {
				e.state(data.CoverageStale, "unknown watch event")
				return resourceRetry
			}
			object, err := meta.Accessor(event.Object)
			if err != nil {
				e.state(data.CoveragePartial, "watch object unavailable")
				return resourceStop
			}
			row, valid := projectResource(object)
			if !valid {
				e.state(data.CoveragePartial, "object exceeds metadata limits")
				return resourceStop
			}
			if !e.put(row, event.Type == watch.Deleted) {
				return resourceStop
			}
		}
	}
}

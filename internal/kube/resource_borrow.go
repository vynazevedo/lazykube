package kube

import (
	"maps"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

func (s *Source) borrowedResource(gvr schema.GroupVersionResource) bool {
	switch gvr {
	case schema.GroupVersionResource{Version: "v1", Resource: "pods"}, schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, schema.GroupVersionResource{Version: "v1", Resource: "events"}, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}:
		return true
	}
	return false
}

func (e *resourceEntry) borrowedInformer() cache.SharedIndexInformer {
	s := e.pool.source
	switch e.key.GVR.Resource {
	case "pods":
		return s.podInf
	case "deployments":
		return s.deployInf
	case "nodes":
		return s.nodeInf
	case "namespaces":
		return s.nsInf
	case "events":
		return s.eventInf
	}
	return nil
}

func (e *resourceEntry) borrowedCoverage(inf cache.SharedIndexInformer) data.Coverage {
	s := e.pool.source
	s.mu.RLock()
	defer s.mu.RUnlock()
	if inf == nil {
		return data.Coverage{State: data.CoverageUnavailable, Reason: "existing informer unavailable; no additional read"}
	}
	switch e.key.GVR.Resource {
	case "pods":
		return s.podCoverage
	case "deployments":
		return s.deploymentCoverage
	case "nodes":
		return s.nodeHealth
	}
	if !inf.HasSynced() {
		return data.Coverage{State: data.CoverageCollecting}
	}
	return data.Coverage{State: data.CoveragePartial, Reason: "existing cache; continuous watch health not available"}
}

func (e *resourceEntry) runBorrowed() {
	inf := e.borrowedInformer()
	if inf == nil {
		cov := e.borrowedCoverage(nil)
		e.state(cov.State, cov.Reason)
		return
	}
	dirty := make(chan struct{}, 1)
	notify := func() {
		select {
		case dirty <- struct{}{}:
		default:
		}
	}
	handler, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: func(any) { notify() }, UpdateFunc: func(any, any) { notify() }, DeleteFunc: func(any) { notify() }})
	if err != nil {
		e.state(data.CoverageUnavailable, "cache subscription unavailable")
		return
	}
	defer func() { _ = inf.RemoveEventHandler(handler) }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	notify()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-e.refresh:
			e.projectBorrowed(inf)
		case <-dirty:
			e.projectBorrowed(inf)
		case <-ticker.C:
			cov := e.borrowedCoverage(inf)
			p := e.pool
			p.mu.Lock()
			if !e.truncated && e.coverage != cov {
				e.coverage = cov
				e.changedLocked()
			}
			p.mu.Unlock()
		}
	}
}

func (e *resourceEntry) projectBorrowed(inf cache.SharedIndexInformer) {
	labelSelector, _ := labels.Parse(e.key.LabelSelector)
	fieldSelector, _ := fields.ParseSelector(e.key.FieldSelector)
	next := make(map[string]data.ResourceRow)
	truncated := false
	for _, obj := range inf.GetStore().List() {
		m, err := meta.Accessor(obj)
		if err != nil {
			truncated = true
			continue
		}
		if e.key.Namespace != "" && m.GetNamespace() != e.key.Namespace {
			continue
		}
		if !labelSelector.Matches(labels.Set(m.GetLabels())) || !fieldSelector.Matches(fields.Set{"metadata.name": m.GetName(), "metadata.namespace": m.GetNamespace()}) {
			continue
		}
		if len(next) >= e.pool.rowLimit {
			truncated = true
			break
		}
		row, ok := projectResource(m)
		if !ok {
			truncated = true
			continue
		}
		next[row.Ref.Namespace+"/"+row.Ref.Name] = row
	}
	cov := e.borrowedCoverage(inf)
	p := e.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.ctx.Err() != nil {
		return
	}
	if p.rows-len(e.rows)+len(next) > p.poolLimit {
		e.coverage = data.Coverage{State: data.CoveragePartial, Reason: "resource pool budget exhausted"}
		e.truncated = true
		e.changedLocked()
		return
	}
	if truncated {
		cov = data.Coverage{State: data.CoveragePartial, Reason: "existing cache projection limited"}
	}
	if maps.Equal(e.rows, next) && e.coverage == cov && e.truncated == truncated {
		return
	}
	p.rows += len(next) - len(e.rows)
	e.rows = next
	e.coverage = cov
	e.truncated = truncated
	e.observedAt = p.now()
	e.changedLocked()
}

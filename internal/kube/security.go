package kube

import (
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

	"github.com/TNK-Studio/lazykube/internal/data"
)

var securityGeneration atomic.Uint64

func (s *Source) rebuildSecurity(pods []*corev1.Pod, namespaces []data.Namespace, now time.Time) {
	s.mu.RLock()
	previous := s.security
	objects := s.securityObjects
	coverage := s.podCoverage
	s.mu.RUnlock()
	if coverage.State == data.CoverageCollecting && s.podInf.HasSynced() {
		coverage.State = data.CoverageComplete
	}
	changed := len(previous.Pods) != len(pods) || previous.Coverage.ObservedAt.IsZero()
	if len(previous.Pods) > 0 {
		for _, p := range previous.Pods {
			if coverage.State != data.CoverageComplete && (p.Coverage.State != coverage.State || p.Coverage.Reason != coverage.Reason) {
				changed = true
			}
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
		}
		if coverage.State != data.CoverageComplete {
			old.Coverage.State, old.Coverage.Reason = coverage.State, coverage.Reason
		}
		out[key] = old
	}
	coverage.ObservedAt = now
	next := data.SummarizeSecurity(out, names, coverage)
	s.mu.Lock()
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
		state, reason := data.CoverageStale, "watch interrupted; cached observations may be outdated"
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
}

func securityPodChanged(previous map[string]data.PodPosture, objects map[string]*corev1.Pod, p *corev1.Pod) bool {
	key := podKey(p.Namespace, p.Name)
	old, exists := previous[key]
	return !exists || old.Target.UID != string(p.UID) || old.Target.ResourceVersion != p.ResourceVersion || ((p.UID == "" || p.ResourceVersion == "") && objects[key] != p)
}

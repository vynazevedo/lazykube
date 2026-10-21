package kube

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ insight.ObservationSource = (*Source)(nil)

func (s *Source) Observations(after insight.Cursor) insight.ObservationBatch {
	s.mu.RLock()
	current := s.observations
	s.mu.RUnlock()
	return current.Observations(after)
}

func (s *Source) publishObservations(pods []*corev1.Pod) {
	s.rebuildMu.Lock()
	defer s.rebuildMu.Unlock()
	next := s.prepareObservations(pods)
	s.mu.Lock()
	s.observations = next
	s.mu.Unlock()
}

func (s *Source) prepareObservations(pods []*corev1.Pod) *insight.Inventory {
	s.mu.RLock()
	nextInventory := *s.observations
	podCoverage := s.podCoverage
	s.mu.RUnlock()
	source := insight.SourceIdentity{SessionID: strconv.FormatUint(s.generation, 10), Context: s.opts.Context, Generation: s.generation}
	pods = slices.Clone(pods)
	slices.SortFunc(pods, func(a, b *corev1.Pod) int {
		if n := cmp.Compare(a.Namespace, b.Namespace); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		return cmp.Compare(a.UID, b.UID)
	})
	limit := min(len(pods), insight.MaxInventoryPods)
	projected := make([]insight.PodObservation, 0, limit)
	next := make(map[string]insight.PodObservation, limit)
	budget := newObservationBudget()
	nextBudgets := make(map[string]observationBudgetUse, limit)
	omissions := insight.ObservationOmissions{}
	for _, p := range pods[:limit] {
		key := podKey(p.Namespace, p.Name)
		cached, ok := s.observationPods[key]
		use := s.observationBudgets[key]
		before := budget
		if !ok || cached.Entity.UID != string(p.UID) || cached.Entity.ResourceVersion != p.ResourceVersion || p.UID == "" || p.ResourceVersion == "" || cached.Entity.Source != source || use.Before != before {
			cached = observePodBounded(p, source, &budget)
		} else {
			budget = use.After
		}
		next[key] = cached
		nextBudgets[key] = observationBudgetUse{Before: before, After: budget}
		omissions.Containers += cached.Omissions.Containers
		omissions.Conditions += cached.Omissions.Conditions
		omissions.Owners += cached.Omissions.Owners
		omissions.TextBytes += cached.Omissions.TextBytes
		projected = append(projected, cached)
	}
	s.observationPods = next
	s.observationBudgets = nextBudgets
	coverage := observationCoverage(podCoverage)
	coverage.Total = len(pods)
	if omissions != (insight.ObservationOmissions{}) {
		if coverage.State == insight.CoverageComplete {
			coverage.State = insight.CoveragePartial
		}
		detail := fmt.Sprintf("projection omitted: %d containers, %d conditions, %d owners, %d text bytes", omissions.Containers, omissions.Conditions, omissions.Owners, omissions.TextBytes)
		if coverage.Reason != "" {
			coverage.Reason += "; "
		}
		coverage.Reason += detail
	}
	nextInventory.Publish(source, s.opts.Namespace, false, projected, coverage)
	return &nextInventory
}

func observationCoverage(c data.Coverage) insight.Coverage {
	state := insight.CoverageUnavailable
	switch c.State {
	case data.CoverageCollecting:
		state = insight.CoverageCollecting
	case data.CoverageComplete:
		state = insight.CoverageComplete
	case data.CoveragePartial:
		state = insight.CoveragePartial
	case data.CoverageDenied:
		state = insight.CoverageDenied
	case data.CoverageStale:
		state = insight.CoverageStale
	case data.CoverageDisabled:
		state = insight.CoverageDisabled
	}
	return insight.Coverage{State: state, Reason: clip(c.Reason)}
}

func observationReason(reason string) insight.Reason {
	switch reason {
	case "":
		return insight.ReasonUnknown
	case "CrashLoopBackOff":
		return insight.ReasonCrashLoopBackOff
	case "OOMKilled":
		return insight.ReasonOOMKilled
	case "ImagePullBackOff":
		return insight.ReasonImagePullBackOff
	case "ErrImagePull":
		return insight.ReasonErrImagePull
	case "ContainerCreating":
		return insight.ReasonContainerCreating
	case "PodInitializing":
		return insight.ReasonPodInitializing
	case "Evicted":
		return insight.ReasonEvicted
	case "NodeLost":
		return insight.ReasonNodeLost
	case "Completed":
		return insight.ReasonCompleted
	default:
		return insight.ReasonOther
	}
}

func observePod(p *corev1.Pod, source insight.SourceIdentity) insight.PodObservation {
	budget := newObservationBudget()
	return observePodBounded(p, source, &budget)
}

func observePodBounded(p *corev1.Pod, source insight.SourceIdentity, budget *observationBudget) insight.PodObservation {
	observer := observationProjector{budget: budget}
	out := insight.PodObservation{
		Entity:    insight.EntityRef{EntityKey: insight.EntityKey{Source: source, Version: "v1", Kind: "Pod", Namespace: p.Namespace, Name: p.Name, UID: string(p.UID)}, ResourceVersion: p.ResourceVersion},
		CreatedAt: p.CreationTimestamp.Time, Reason: observationReason(p.Status.Reason), ReasonText: observer.text(p.Status.Reason), Message: observer.text(p.Status.Message), NodeName: p.Spec.NodeName,
	}
	switch p.Status.Phase {
	case corev1.PodPending:
		out.Phase = insight.PodPending
	case corev1.PodRunning:
		out.Phase = insight.PodRunning
	case corev1.PodSucceeded:
		out.Phase = insight.PodSucceeded
	case corev1.PodFailed:
		out.Phase = insight.PodFailed
	}
	if p.DeletionTimestamp != nil {
		out.DeletionAt = p.DeletionTimestamp.Time
	}
	if p.Status.StartTime != nil {
		out.StartedAt = p.Status.StartTime.Time
	}
	owners := slices.Clone(p.OwnerReferences)
	slices.SortFunc(owners, func(a, b metav1.OwnerReference) int {
		if n := cmp.Compare(a.UID, b.UID); n != 0 {
			return n
		}
		return cmp.Compare(a.Name, b.Name)
	})
	observer.omissions.Owners = max(0, len(owners)-maxObservationOwners)
	for _, owner := range owners[:min(len(owners), maxObservationOwners)] {
		group, version, split := strings.Cut(owner.APIVersion, "/")
		if !split {
			version = group
			group = ""
		}
		out.Owners = append(out.Owners, insight.OwnerRef{Entity: insight.EntityRef{EntityKey: insight.EntityKey{Source: source, Group: group, Version: version, Kind: owner.Kind, Namespace: p.Namespace, Name: owner.Name, UID: string(owner.UID)}}, Controller: owner.Controller != nil && *owner.Controller})
	}
	conditions := slices.Clone(p.Status.Conditions)
	slices.SortFunc(conditions, func(a, b corev1.PodCondition) int { return cmp.Compare(a.Type, b.Type) })
	observer.omissions.Conditions = max(0, len(conditions)-maxObservationConditions)
	for _, c := range conditions[:min(len(conditions), maxObservationConditions)] {
		kind := insight.ConditionOther
		switch c.Type {
		case corev1.PodReady:
			kind = insight.ConditionReady
		case corev1.PodScheduled:
			kind = insight.ConditionScheduled
		case corev1.PodInitialized:
			kind = insight.ConditionInitialized
		case corev1.ContainersReady:
			kind = insight.ConditionContainersReady
		}
		status := insight.TruthUnknown
		switch c.Status {
		case corev1.ConditionTrue:
			status = insight.TruthTrue
		case corev1.ConditionFalse:
			status = insight.TruthFalse
		}
		out.Conditions = append(out.Conditions, insight.Condition{Kind: kind, Name: observer.text(string(c.Type)), Status: status, ReasonText: observer.text(c.Reason), Message: observer.text(c.Message), LastProbeAt: c.LastProbeTime.Time, LastTransitionAt: c.LastTransitionTime.Time})
	}
	out.Containers = observer.containers(p)
	out.Omissions = observer.omissions
	return out
}

func (observer *observationProjector) container(name, image string, kind insight.ContainerKind, cs *corev1.ContainerStatus) insight.ContainerObservation {
	out := insight.ContainerObservation{Name: name, Image: observer.text(image), Kind: kind}
	if cs == nil {
		return out
	}
	out.StatusObserved = true
	out.Ready = observationTruth(cs.Ready)
	if cs.Started != nil {
		out.Started = observationTruth(*cs.Started)
	}
	out.RestartCount = cs.RestartCount
	out.LastTermination = observer.termination(cs.LastTerminationState.Terminated)
	switch {
	case cs.State.Waiting != nil:
		out.Phase = insight.ContainerWaiting
		out.Reason = observationReason(cs.State.Waiting.Reason)
		out.ReasonText = observer.text(cs.State.Waiting.Reason)
		out.Message = observer.text(cs.State.Waiting.Message)
	case cs.State.Running != nil:
		out.Phase = insight.ContainerRunning
		out.StartedAt = cs.State.Running.StartedAt.Time
	case cs.State.Terminated != nil:
		out.Phase = insight.ContainerTerminated
		out.CurrentTermination = observer.termination(cs.State.Terminated)
		out.Reason = out.CurrentTermination.Reason
		out.ReasonText = out.CurrentTermination.ReasonText
		out.Message = out.CurrentTermination.Message
	}
	return out
}

func observationTruth(value bool) insight.Truth {
	if value {
		return insight.TruthTrue
	}
	return insight.TruthFalse
}

func (observer *observationProjector) termination(t *corev1.ContainerStateTerminated) *insight.Termination {
	if t == nil {
		return nil
	}
	return &insight.Termination{Reason: observationReason(t.Reason), ReasonText: observer.text(t.Reason), Message: observer.text(t.Message), ExitCode: t.ExitCode, Signal: t.Signal, StartedAt: t.StartedAt.Time, FinishedAt: t.FinishedAt.Time}
}

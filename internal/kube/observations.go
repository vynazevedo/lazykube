package kube

import (
	"strconv"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
	corev1 "k8s.io/api/core/v1"
)

var _ insight.ObservationSource = (*Source)(nil)

func (s *Source) Observations(after insight.Cursor) insight.ObservationBatch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.observations.Observations(after)
}

func (s *Source) publishObservations(pods []*corev1.Pod) {
	source := insight.SourceIdentity{SessionID: strconv.FormatUint(s.generation, 10), Context: s.opts.Context, Generation: s.generation}
	limit := min(len(pods), insight.MaxInventoryPods)
	projected := make([]insight.PodObservation, 0, limit)
	next := make(map[string]insight.PodObservation, limit)
	for _, p := range pods[:limit] {
		key := podKey(p.Namespace, p.Name)
		cached, ok := s.observationPods[key]
		if !ok || cached.Entity.UID != string(p.UID) || cached.Entity.ResourceVersion != p.ResourceVersion || p.UID == "" || p.ResourceVersion == "" || cached.Entity.Source != source {
			cached = observePod(p, source)
		}
		next[key] = cached
		projected = append(projected, cached)
	}
	s.observationPods = next
	coverage := observationCoverage(s.podCoverage)
	coverage.Total = len(pods)
	s.observations.Publish(source, s.opts.Namespace, false, projected, coverage)
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
	out := insight.PodObservation{
		Entity:    insight.EntityRef{EntityKey: insight.EntityKey{Source: source, Version: "v1", Kind: "Pod", Namespace: p.Namespace, Name: p.Name, UID: string(p.UID)}, ResourceVersion: p.ResourceVersion},
		CreatedAt: p.CreationTimestamp.Time, Reason: observationReason(p.Status.Reason), ReasonText: clip(p.Status.Reason), Message: clip(p.Status.Message), NodeName: p.Spec.NodeName,
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
	for _, owner := range p.OwnerReferences {
		group, version, split := strings.Cut(owner.APIVersion, "/")
		if !split {
			version = group
			group = ""
		}
		out.Owners = append(out.Owners, insight.OwnerRef{Entity: insight.EntityRef{EntityKey: insight.EntityKey{Source: source, Group: group, Version: version, Kind: owner.Kind, Namespace: p.Namespace, Name: owner.Name, UID: string(owner.UID)}}, Controller: owner.Controller != nil && *owner.Controller})
	}
	for _, c := range p.Status.Conditions {
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
		out.Conditions = append(out.Conditions, insight.Condition{Kind: kind, Name: clip(string(c.Type)), Status: status, ReasonText: clip(c.Reason), Message: clip(c.Message), LastProbeAt: c.LastProbeTime.Time, LastTransitionAt: c.LastTransitionTime.Time})
	}
	inits := statusIndex(p.Status.InitContainerStatuses)
	for _, c := range p.Spec.InitContainers {
		kind := insight.ContainerInit
		if restartable(&c) {
			kind = insight.ContainerSidecar
		}
		out.Containers = append(out.Containers, observeContainer(c.Name, c.Image, kind, inits[c.Name]))
	}
	regular := statusIndex(p.Status.ContainerStatuses)
	for _, c := range p.Spec.Containers {
		out.Containers = append(out.Containers, observeContainer(c.Name, c.Image, insight.ContainerRegular, regular[c.Name]))
	}
	ephemeral := statusIndex(p.Status.EphemeralContainerStatuses)
	for _, c := range p.Spec.EphemeralContainers {
		out.Containers = append(out.Containers, observeContainer(c.Name, c.Image, insight.ContainerEphemeral, ephemeral[c.Name]))
	}
	return out
}

func observeContainer(name, image string, kind insight.ContainerKind, cs *corev1.ContainerStatus) insight.ContainerObservation {
	out := insight.ContainerObservation{Name: name, Image: clip(image), Kind: kind}
	if cs == nil {
		return out
	}
	out.StatusObserved = true
	out.Ready = observationTruth(cs.Ready)
	if cs.Started != nil {
		out.Started = observationTruth(*cs.Started)
	}
	out.RestartCount = cs.RestartCount
	out.LastTermination = observeTermination(cs.LastTerminationState.Terminated)
	switch {
	case cs.State.Waiting != nil:
		out.Phase = insight.ContainerWaiting
		out.Reason = observationReason(cs.State.Waiting.Reason)
		out.ReasonText = clip(cs.State.Waiting.Reason)
		out.Message = clip(cs.State.Waiting.Message)
	case cs.State.Running != nil:
		out.Phase = insight.ContainerRunning
		out.StartedAt = cs.State.Running.StartedAt.Time
	case cs.State.Terminated != nil:
		out.Phase = insight.ContainerTerminated
		out.CurrentTermination = observeTermination(cs.State.Terminated)
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

func observeTermination(t *corev1.ContainerStateTerminated) *insight.Termination {
	if t == nil {
		return nil
	}
	return &insight.Termination{Reason: observationReason(t.Reason), ReasonText: clip(t.Reason), Message: clip(t.Message), ExitCode: t.ExitCode, Signal: t.Signal, StartedAt: t.StartedAt.Time, FinishedAt: t.FinishedAt.Time}
}

package kube

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
	corev1 "k8s.io/api/core/v1"
)

const (
	maxObservationContainers = 100000
	maxObservationConditions = 16
	maxObservationOwners     = 8
	maxObservationText       = 512
	maxObservationEvidence   = 4 << 20
)

type observationBudget struct {
	Containers int
	TextBytes  int
}

type observationBudgetUse struct{ Before, After observationBudget }

type observationProjector struct {
	budget    *observationBudget
	omissions insight.ObservationOmissions
}

func newObservationBudget() observationBudget {
	return observationBudget{Containers: maxObservationContainers, TextBytes: maxObservationEvidence}
}

func (o *observationProjector) text(value string) string {
	value = strings.Join(strings.Fields(data.Sanitize(value)), " ")
	limit := min(len(value), maxObservationText, o.budget.TextBytes)
	for limit > 0 && limit < len(value) && !utf8.RuneStart(value[limit]) {
		limit--
	}
	o.omissions.TextBytes += len(value) - limit
	o.budget.TextBytes -= limit
	return strings.Clone(value[:limit])
}

type observationContainerRef struct {
	name   string
	image  string
	kind   insight.ContainerKind
	status *corev1.ContainerStatus
	order  int
}

func (o *observationProjector) containers(p *corev1.Pod) []insight.ContainerObservation {
	total := len(p.Spec.InitContainers) + len(p.Spec.Containers) + len(p.Spec.EphemeralContainers)
	limit := min(total, o.budget.Containers)
	o.omissions.Containers = total - limit
	o.budget.Containers -= limit
	if limit == 0 {
		return nil
	}
	refs := make([]observationContainerRef, 0, total)
	inits := statusIndex(p.Status.InitContainerStatuses)
	for _, c := range p.Spec.InitContainers {
		kind, order := insight.ContainerInit, 0
		if restartable(&c) {
			kind, order = insight.ContainerSidecar, 1
		}
		refs = append(refs, observationContainerRef{name: c.Name, image: c.Image, kind: kind, status: inits[c.Name], order: order})
	}
	regular := statusIndex(p.Status.ContainerStatuses)
	for _, c := range p.Spec.Containers {
		refs = append(refs, observationContainerRef{name: c.Name, image: c.Image, kind: insight.ContainerRegular, status: regular[c.Name], order: 2})
	}
	ephemeral := statusIndex(p.Status.EphemeralContainerStatuses)
	for _, c := range p.Spec.EphemeralContainers {
		refs = append(refs, observationContainerRef{name: c.Name, image: c.Image, kind: insight.ContainerEphemeral, status: ephemeral[c.Name], order: 3})
	}
	slices.SortFunc(refs, func(a, b observationContainerRef) int {
		if n := cmp.Compare(a.order, b.order); n != 0 {
			return n
		}
		return cmp.Compare(a.name, b.name)
	})
	out := make([]insight.ContainerObservation, limit)
	for i, ref := range refs[:limit] {
		out[i] = o.container(ref.name, ref.image, ref.kind, ref.status)
	}
	return out
}

package kube

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

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

type observationEscape uint8

const (
	observationPlain observationEscape = iota
	observationEscapeStart
	observationCSI
	observationString
	observationStringEnd
)

func (o *observationProjector) text(value string) string {
	limit := min(maxObservationText, max(0, o.budget.TextBytes))
	var out strings.Builder
	out.Grow(min(len(value), limit))
	state := observationPlain
	total := 0
	space := false
	clipped := false
	emit := func(r rune) {
		n := utf8.RuneLen(r)
		total += n
		if !clipped && out.Len()+n <= limit {
			out.WriteRune(r)
		} else {
			clipped = true
		}
	}
	for _, r := range value {
		switch state {
		case observationEscapeStart:
			switch r {
			case '[':
				state = observationCSI
			case ']', 'P', '_', '^', 'X':
				state = observationString
			default:
				state = observationPlain
			}
			continue
		case observationCSI:
			if r >= 0x40 && r <= 0x7e {
				state = observationPlain
			}
			continue
		case observationString:
			switch r {
			case 7:
				state = observationPlain
			case 0x1b:
				state = observationStringEnd
			}
			continue
		case observationStringEnd:
			state = observationPlain
			continue
		}
		if r == 0x1b {
			state = observationEscapeStart
			continue
		}
		if r != '\t' && (r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0) {
			continue
		}
		if unicode.IsSpace(r) {
			space = total > 0
			continue
		}
		if space {
			emit(' ')
			space = false
		}
		emit(r)
	}
	o.omissions.TextBytes += total - out.Len()
	o.budget.TextBytes -= out.Len()
	return out.String()
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

package insight

import (
	"cmp"
	"slices"
)

func (e *Engine) selectPods(pods []PodObservation) {
	ordered := make([]*PodObservation, len(pods))
	e.ruleTotals = make(map[Rule]int, 6)
	for i := range pods {
		ordered[i] = &pods[i]
		e.ruleTotals[RulePending]++
		e.ruleTotals[RuleEvicted]++
		for _, rule := range []Rule{RuleRestarts, RuleCrashLoop, RuleOOM, RuleImagePull} {
			e.ruleTotals[rule] += len(pods[i].Containers)
		}
	}
	slices.SortFunc(ordered, func(a, b *PodObservation) int {
		if n := cmp.Compare(a.Entity.Namespace, b.Entity.Namespace); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Entity.Name, b.Entity.Name); n != 0 {
			return n
		}
		return cmp.Compare(a.Entity.UID, b.Entity.UID)
	})
	selected := make([]PodObservation, 0, min(len(pods), MaxOperationalSignals/2))
	budget := MaxOperationalSignals
	for _, p := range ordered {
		if budget < 2 {
			break
		}
		budget -= 2
		count := min(len(p.Containers), budget/4)
		indices := make([]int, len(p.Containers))
		for n := range indices {
			indices[n] = n
		}
		slices.SortFunc(indices, func(a, b int) int {
			if n := cmp.Compare(p.Containers[a].Kind, p.Containers[b].Kind); n != 0 {
				return n
			}
			return cmp.Compare(p.Containers[a].Name, p.Containers[b].Name)
		})
		copyPod := *p
		copyPod.Owners = nil
		copyPod.Conditions = nil
		copyPod.Message = ""
		copyPod.ReasonText = ""
		copyPod.Containers = make([]ContainerObservation, count)
		for n, index := range indices[:count] {
			copyPod.Containers[n] = p.Containers[index]
		}
		copyPod.Omissions.Containers += len(p.Containers) - count
		selected = append(selected, clonePod(copyPod))
		budget -= 4 * count
	}
	entities := make(map[EntityKey]*EntityKey, len(selected))
	for _, p := range selected {
		key := p.Entity.EntityKey
		ref := e.entities[key]
		if ref == nil {
			ref = &key
		}
		entities[key] = ref
	}
	e.entities = entities
	e.pods = make([]PodObservation, len(selected))
	copy(e.pods, selected)
}

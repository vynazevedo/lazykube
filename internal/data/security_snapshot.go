package data

import "slices"

func SummarizeSecurity(pods map[string]PodPosture, namespaces []string, coverage Coverage) SecuritySnapshot {
	s := SecuritySnapshot{Pods: pods, Namespaces: make(map[string]NamespacePosture), Coverage: coverage}
	for _, ns := range namespaces {
		c := coverage
		c.Evaluated, c.Total = 0, 0
		s.Namespaces[ns] = NamespacePosture{Name: ns, Coverage: c}
	}
	s.Coverage.Total, s.Coverage.Evaluated = len(pods), 0
	for _, p := range pods {
		ns, ok := s.Namespaces[p.Target.Namespace]
		if !ok {
			c := coverage
			c.Evaluated, c.Total = 0, 0
			ns = NamespacePosture{Name: p.Target.Namespace, Coverage: c}
		}
		ns.Coverage.Total++
		ns.Coverage.Evaluated += p.Coverage.Evaluated
		s.Coverage.Evaluated += p.Coverage.Evaluated
		if p.Coverage.State == CoveragePartial && coverage.State == CoverageComplete {
			ns.Coverage.State, s.Coverage.State = CoveragePartial, CoveragePartial
		}
		for _, f := range p.Findings {
			switch f.Result {
			case ResultFinding:
				ns.Findings++
			case ResultUnknown:
				ns.Unknown++
			}
		}
		s.Namespaces[p.Target.Namespace] = ns
	}
	return s
}

func (s SecuritySnapshot) Clone() SecuritySnapshot {
	out := SecuritySnapshot{Coverage: s.Coverage, Pods: make(map[string]PodPosture, len(s.Pods)), Namespaces: make(map[string]NamespacePosture, len(s.Namespaces))}
	for k, p := range s.Pods {
		p.Findings = slices.Clone(p.Findings)
		out.Pods[k] = p
	}
	for k, n := range s.Namespaces {
		out.Namespaces[k] = n
	}
	return out
}

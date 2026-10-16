package data

import (
	"cmp"
	"math"
	"slices"
)

const SecurityHistoryLimit = 40
const SecurityScoreFormula = "100 - (25*high + 8*medium + 2*info)/evaluated pods; floor 0, rounded."

type PostureScore struct {
	Value     int
	Grade     string
	Available bool
	Partial   bool
	Coverage  Coverage
}

func ScorePosture(c Coverage, high, medium, info int) PostureScore {
	s := PostureScore{Coverage: c, Partial: c.State == CoveragePartial || c.Evaluated < c.Total}
	if c.Evaluated <= 0 || (c.State != CoverageComplete && c.State != CoveragePartial) {
		return s
	}
	penalty := (25*high + 8*medium + 2*info)
	s.Value = max(0, int(math.Round(100-float64(penalty)/float64(c.Evaluated))))
	s.Available = true
	switch {
	case s.Value >= 90:
		s.Grade = "A"
	case s.Value >= 80:
		s.Grade = "B"
	case s.Value >= 70:
		s.Grade = "C"
	case s.Value >= 60:
		s.Grade = "D"
	default:
		s.Grade = "F"
	}
	return s
}

type RuleCount struct {
	Rule  string
	Count int
}
type SecurityAggregate struct {
	Name                        string
	Score                       PostureScore
	High, Medium, Info, Unknown int
	Rules                       []RuleCount
}
type SecurityDashboard struct {
	Cluster    SecurityAggregate
	Namespaces map[string]SecurityAggregate
	Pods       map[string]PostureScore
	Ranking    []SecurityAggregate
}

func AggregateSecurity(s SecuritySnapshot) SecurityDashboard {
	d := SecurityDashboard{Namespaces: make(map[string]SecurityAggregate), Pods: make(map[string]PostureScore)}
	counts := make(map[string]map[string]int)
	for name, ns := range s.Namespaces {
		d.Namespaces[name] = SecurityAggregate{Name: name, Score: PostureScore{Coverage: ns.Coverage}}
	}
	for key, p := range s.Pods {
		a := d.Namespaces[p.Target.Namespace]
		a.Name = p.Target.Namespace
		var high, medium, info int
		for _, f := range p.Findings {
			if f.Result == ResultUnknown {
				a.Unknown++
				d.Cluster.Unknown++
				continue
			}
			if f.Result != ResultFinding {
				continue
			}
			switch f.Severity {
			case SeverityHigh:
				high++
			case SeverityMedium:
				medium++
			case SeverityInfo:
				info++
			}
			for _, scope := range []string{"", p.Target.Namespace} {
				if counts[scope] == nil {
					counts[scope] = make(map[string]int)
				}
				counts[scope][f.Rule]++
			}
		}
		podCoverage := p.Coverage
		if s.Coverage.State != CoverageComplete && s.Coverage.State != CoveragePartial {
			podCoverage.State, podCoverage.Reason = s.Coverage.State, s.Coverage.Reason
		}
		d.Pods[key] = ScorePosture(podCoverage, high, medium, info)
		a.High += high
		a.Medium += medium
		a.Info += info
		d.Cluster.High += high
		d.Cluster.Medium += medium
		d.Cluster.Info += info
		d.Namespaces[a.Name] = a
	}
	finish := func(a SecurityAggregate, c Coverage, rules map[string]int) SecurityAggregate {
		a.Score = ScorePosture(c, a.High, a.Medium, a.Info)
		for rule, count := range rules {
			a.Rules = append(a.Rules, RuleCount{rule, count})
		}
		slices.SortFunc(a.Rules, func(a, b RuleCount) int {
			if n := cmp.Compare(b.Count, a.Count); n != 0 {
				return n
			}
			return cmp.Compare(a.Rule, b.Rule)
		})
		return a
	}
	d.Cluster.Name = "Cluster"
	d.Cluster = finish(d.Cluster, s.Coverage, counts[""])
	for name, a := range d.Namespaces {
		coverage := a.Score.Coverage
		if s.Coverage.State != CoverageComplete && s.Coverage.State != CoveragePartial {
			coverage.State, coverage.Reason = s.Coverage.State, s.Coverage.Reason
		}
		a = finish(a, coverage, counts[name])
		d.Namespaces[name] = a
		d.Ranking = append(d.Ranking, a)
	}
	slices.SortFunc(d.Ranking, func(a, b SecurityAggregate) int {
		if a.Score.Available != b.Score.Available {
			if a.Score.Available {
				return -1
			}
			return 1
		}
		if n := cmp.Compare(a.Score.Value, b.Score.Value); n != 0 {
			return n
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return d
}

type SecurityIdentity struct {
	Context              string
	Generation           uint64
	UID, Rule, Container string
}
type SecuritySession struct {
	Revision  uint64
	Dashboard SecurityDashboard
	Alerts    []Finding
	History   map[string][]float64
	baseline  bool
	loaded    bool
	previous  map[SecurityIdentity]bool
	pending   map[SecurityIdentity]Finding
}

func (s *SecuritySession) Update(snapshot SecuritySnapshot) bool {
	if s.loaded && s.Revision == snapshot.Revision {
		return false
	}
	s.Dashboard = AggregateSecurity(snapshot)
	current := make(map[SecurityIdentity]bool)
	if s.pending == nil {
		s.pending = make(map[SecurityIdentity]Finding)
	}
	for _, p := range snapshot.Pods {
		for _, f := range p.Findings {
			if f.Result != ResultFinding || f.Severity != SeverityHigh || p.Target.UID == "" {
				continue
			}
			id := SecurityIdentity{p.Target.Context, p.Target.Generation, p.Target.UID, f.Rule, f.Container}
			current[id] = true
			if s.baseline && !s.previous[id] && (p.Coverage.State == CoverageComplete || p.Coverage.State == CoveragePartial) && (snapshot.Coverage.State == CoverageComplete || snapshot.Coverage.State == CoveragePartial) {
				f.Target = p.Target
				s.pending[id] = f
			}
		}
	}
	if snapshot.Coverage.State == CoverageComplete || snapshot.Coverage.State == CoveragePartial {
		if snapshot.Coverage.State == CoveragePartial || snapshot.Coverage.Evaluated < snapshot.Coverage.Total {
			for id := range s.previous {
				current[id] = true
			}
		}
		s.previous = current
		s.baseline = true
	}
	s.Alerts = nil
	for _, f := range s.pending {
		s.Alerts = append(s.Alerts, f)
	}
	slices.SortFunc(s.Alerts, func(a, b Finding) int {
		for _, pair := range [][2]string{{a.Target.Namespace, b.Target.Namespace}, {a.Target.Name, b.Target.Name}, {a.Rule, b.Rule}, {a.Container, b.Container}, {a.Target.UID, b.Target.UID}} {
			if n := cmp.Compare(pair[0], pair[1]); n != 0 {
				return n
			}
		}
		return 0
	})
	if s.History == nil {
		s.History = make(map[string][]float64)
	}
	sample := func(scope string, score PostureScore) {
		if score.Available {
			h := append(s.History[scope], float64(score.Value))
			if len(h) > SecurityHistoryLimit {
				h = slices.Clone(h[len(h)-SecurityHistoryLimit:])
			}
			s.History[scope] = h
		}
	}
	sample("", s.Dashboard.Cluster.Score)
	for name, a := range s.Dashboard.Namespaces {
		sample(name, a.Score)
	}
	for name := range s.History {
		if name != "" {
			if _, ok := s.Dashboard.Namespaces[name]; !ok {
				delete(s.History, name)
			}
		}
	}
	s.Revision, s.loaded = snapshot.Revision, true
	return true
}

func (s *SecuritySession) Acknowledge() { clear(s.pending); s.Alerts = nil }

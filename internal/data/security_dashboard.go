package data

import (
	"cmp"
	"math"
	"slices"
)

const SecurityHistoryLimit = 40
const SecurityIdentityRetention = 40
const SecurityUncertainIdentityLimit = 4096
const SecurityScoreFormula = "v2 per pod/dimension: max(0, 100 - 40H - 6M - I); each rule once, worst severity."

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
	penalty := (40*high + 6*medium + info)
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
	case s.Value >= 50:
		s.Grade = "E"
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
	Hygiene                     PostureScore
	Distribution                ScoreDistribution
	HygieneDistribution         ScoreDistribution
	Name                        string
	Score                       PostureScore
	High, Medium, Info, Unknown int
	Rules                       []RuleCount
}
type SecurityDashboard struct {
	Cluster    SecurityAggregate
	Namespaces map[string]SecurityAggregate
	Pods       map[string]PostureScore
	PodHygiene map[string]PostureScore
	Ranking    []SecurityAggregate
}

type SecurityIdentity struct {
	Context              string
	Generation           uint64
	UID, Rule, Container string
}
type SecuritySession struct {
	Revision    uint64
	Dashboard   SecurityDashboard
	Alerts      []Finding
	History     map[string][]float64
	baseline    bool
	loaded      bool
	previous    map[SecurityIdentity]uint64
	observation uint64
	pending     map[SecurityIdentity]Finding
	ruleVersion string
}

func (s *SecuritySession) Update(snapshot SecuritySnapshot) bool {
	if s.loaded && s.Revision == snapshot.Revision && (snapshot.RuleVersion == "" || snapshot.RuleVersion == s.ruleVersion) {
		return false
	}
	if snapshot.RuleVersion != "" && s.ruleVersion != "" && s.ruleVersion != snapshot.RuleVersion {
		s.baseline = false
		s.previous = nil
		s.pending = nil
		s.History = nil
	}
	if snapshot.RuleVersion != "" {
		s.ruleVersion = snapshot.RuleVersion
	}
	s.Dashboard = AggregateSecurity(snapshot)
	s.observation++
	current := make(map[SecurityIdentity]uint64)
	usable := snapshot.Coverage.State == CoverageComplete || snapshot.Coverage.State == CoveragePartial
	if s.pending == nil {
		s.pending = make(map[SecurityIdentity]Finding)
	}
	for _, p := range snapshot.Pods {
		for _, f := range p.Findings {
			if f.Result != ResultFinding || f.Severity != SeverityHigh || p.Target.UID == "" {
				continue
			}
			id := SecurityIdentity{p.Target.Context, p.Target.Generation, p.Target.UID, f.Rule, f.Container}
			if usable && (p.Coverage.State == CoverageComplete || p.Coverage.State == CoveragePartial) {
				current[id] = s.observation
			}
			if s.baseline && s.previous[id] == 0 && (p.Coverage.State == CoverageComplete || p.Coverage.State == CoveragePartial) && (snapshot.Coverage.State == CoverageComplete || snapshot.Coverage.State == CoveragePartial) {
				f.Target = p.Target
				s.pending[id] = f
			}
		}
	}
	s.reconcileIdentities(snapshot, current, usable)
	if usable {
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

type securityPodIdentity struct {
	Context    string
	Generation uint64
	UID        string
}

func (s *SecuritySession) reconcileIdentities(snapshot SecuritySnapshot, current map[SecurityIdentity]uint64, usable bool) {
	complete := make(map[securityPodIdentity]bool, len(snapshot.Pods))
	for _, p := range snapshot.Pods {
		complete[securityPodIdentity{p.Target.Context, p.Target.Generation, p.Target.UID}] = usable && p.Coverage.State == CoverageComplete && p.Coverage.Evaluated == p.Coverage.Total
	}
	listed := usable && (snapshot.ListCoverage.State == CoverageComplete || (snapshot.ListCoverage.State == "" && snapshot.Coverage.State == CoverageComplete)) && len(snapshot.Pods) == snapshot.Coverage.Total
	uncertain := make([]SecurityIdentity, 0)
	for id, seen := range s.previous {
		if current[id] != 0 || s.observation-seen >= SecurityIdentityRetention {
			continue
		}
		observed, exists := complete[securityPodIdentity{id.Context, id.Generation, id.UID}]
		if observed || (!exists && listed) {
			continue
		}
		uncertain = append(uncertain, id)
	}
	if len(uncertain) > SecurityUncertainIdentityLimit {
		slices.SortFunc(uncertain, func(a, b SecurityIdentity) int {
			if n := cmp.Compare(s.previous[b], s.previous[a]); n != 0 {
				return n
			}
			if n := cmp.Compare(a.Context, b.Context); n != 0 {
				return n
			}
			if n := cmp.Compare(a.Generation, b.Generation); n != 0 {
				return n
			}
			if n := cmp.Compare(a.UID, b.UID); n != 0 {
				return n
			}
			if n := cmp.Compare(a.Rule, b.Rule); n != 0 {
				return n
			}
			return cmp.Compare(a.Container, b.Container)
		})
		uncertain = uncertain[:SecurityUncertainIdentityLimit]
	}
	for _, id := range uncertain {
		current[id] = s.previous[id]
	}
	s.previous = current
}

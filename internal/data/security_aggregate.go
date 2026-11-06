package data

import (
	"cmp"
	"math"
	"slices"
	"strings"
)

type ScoreDistribution struct {
	Grades                    [6]int
	Count                     int
	Mean, Median, WorstDecile int
	bins                      [101]int
}

func (d *ScoreDistribution) add(s PostureScore) {
	if !s.Available {
		return
	}
	d.Count++
	d.bins[s.Value]++
	switch s.Grade {
	case "A":
		d.Grades[0]++
	case "B":
		d.Grades[1]++
	case "C":
		d.Grades[2]++
	case "D":
		d.Grades[3]++
	case "E":
		d.Grades[4]++
	default:
		d.Grades[5]++
	}
}
func (d *ScoreDistribution) finish() {
	if d.Count == 0 {
		return
	}
	total, seen, sumLow := 0, 0, 0
	lowCount := (d.Count + 9) / 10
	left, right := (d.Count-1)/2, d.Count/2
	medianSum := 0
	for score, n := range d.bins {
		total += score * n
		sumLow += score * max(0, min(n, lowCount-seen))
		if seen <= left && seen+n > left {
			medianSum += score
		}
		if seen <= right && seen+n > right {
			medianSum += score
		}
		seen += n
	}
	d.Mean = int(math.Round(float64(total) / float64(d.Count)))
	d.Median = int(math.Round(float64(medianSum) / 2))
	d.WorstDecile = int(math.Round(float64(sumLow) / float64(lowCount)))
}
func distributionScore(c Coverage, d ScoreDistribution) PostureScore {
	c.Evaluated = d.Count
	s := ScorePosture(c, 0, 0, 0)
	if s.Available {
		s.Value = d.Mean
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
	}
	return s
}
func rankRules(rules map[string]int) []RuleCount {
	var out []RuleCount
	for rule, count := range rules {
		out = append(out, RuleCount{rule, count})
	}
	slices.SortFunc(out, func(a, b RuleCount) int {
		if n := cmp.Compare(b.Count, a.Count); n != 0 {
			return n
		}
		return cmp.Compare(a.Rule, b.Rule)
	})
	return out
}

func mergeCoverage(d SecurityDashboard, names []string) Coverage {
	merged := Coverage{State: CoverageComplete}
	observed := false
	for _, name := range names {
		a, ok := d.Namespaces[name]
		if !ok {
			if merged.State == CoverageComplete {
				merged.State, merged.Reason = CoveragePartial, "namespace not observed"
			}
			continue
		}
		observed = true
		c := a.Score.Coverage
		merged.Total += c.Total
		if c.State != CoverageComplete && merged.State == CoverageComplete {
			merged.State, merged.Reason = CoveragePartial, c.Reason
		}
	}
	if !observed {
		return Coverage{State: CoverageUnavailable, Reason: "namespace not observed"}
	}
	return merged
}

func (d SecurityDashboard) MergeNamespaces(names []string) SecurityAggregate {
	merged := SecurityAggregate{Name: strings.Join(names, ", ")}
	rules := make(map[string]int)
	for _, name := range names {
		a := d.Namespaces[name]
		merged.High += a.High
		merged.Medium += a.Medium
		merged.Info += a.Info
		merged.Unknown += a.Unknown
		for _, rule := range a.Rules {
			rules[rule.Rule] += rule.Count
		}
	}
	for key, score := range d.Pods {
		namespace, _, _ := strings.Cut(key, "/")
		if slices.Contains(names, namespace) {
			merged.Distribution.add(score)
			merged.HygieneDistribution.add(d.PodHygiene[key])
		}
	}
	merged.Distribution.finish()
	merged.HygieneDistribution.finish()
	coverage := mergeCoverage(d, names)
	merged.Score = distributionScore(coverage, merged.Distribution)
	merged.Hygiene = distributionScore(coverage, merged.HygieneDistribution)
	merged.Rules = rankRules(rules)
	return merged
}

func AggregateSecurity(s SecuritySnapshot) SecurityDashboard {
	d := SecurityDashboard{Namespaces: make(map[string]SecurityAggregate), Pods: make(map[string]PostureScore, len(s.Pods)), PodHygiene: make(map[string]PostureScore, len(s.Pods))}
	counts := make(map[string]map[string]int)
	for name, ns := range s.Namespaces {
		d.Namespaces[name] = SecurityAggregate{Name: name, Score: PostureScore{Coverage: ns.Coverage}}
	}
	for key, p := range s.Pods {
		a := d.Namespaces[p.Target.Namespace]
		a.Name = p.Target.Namespace
		if s.Coverage.State != CoverageComplete && s.Coverage.State != CoveragePartial {
			p.Coverage.State, p.Coverage.Reason = s.Coverage.State, s.Coverage.Reason
		}
		scores := scorePod(p, func(rule string, severity Severity) {
			switch severity {
			case SeverityHigh:
				a.High++
				d.Cluster.High++
			case SeverityMedium:
				a.Medium++
				d.Cluster.Medium++
			case SeverityInfo:
				a.Info++
				d.Cluster.Info++
			}
			for _, scope := range []string{"", p.Target.Namespace} {
				if counts[scope] == nil {
					counts[scope] = make(map[string]int)
				}
				counts[scope][rule]++
			}
		})
		for _, f := range p.Findings {
			if f.Result == ResultUnknown {
				a.Unknown++
				d.Cluster.Unknown++
			}
		}
		d.Pods[key], d.PodHygiene[key] = scores.Security, scores.Hygiene
		a.Distribution.add(scores.Security)
		a.HygieneDistribution.add(scores.Hygiene)
		d.Cluster.Distribution.add(scores.Security)
		d.Cluster.HygieneDistribution.add(scores.Hygiene)
		d.Namespaces[a.Name] = a
	}
	finish := func(a SecurityAggregate, c Coverage, rules map[string]int) SecurityAggregate {
		a.Distribution.finish()
		a.HygieneDistribution.finish()
		a.Score = distributionScore(c, a.Distribution)
		a.Hygiene = distributionScore(c, a.HygieneDistribution)
		a.Rules = rankRules(rules)
		return a
	}
	d.Cluster.Name = "Cluster"
	d.Cluster = finish(d.Cluster, s.Coverage, counts[""])
	for name, a := range d.Namespaces {
		c := a.Score.Coverage
		if s.Coverage.State != CoverageComplete && s.Coverage.State != CoveragePartial {
			c.State, c.Reason = s.Coverage.State, s.Coverage.Reason
		}
		a = finish(a, c, counts[name])
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

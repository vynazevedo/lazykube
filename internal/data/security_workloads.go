package data

import (
	"cmp"
	"slices"
	"strings"
)

type OwnerResolution string

const (
	OwnerDirect     OwnerResolution = "direct controller"
	OwnerExternal   OwnerResolution = "unrecognized controller"
	OwnerUnresolved OwnerResolution = "owner unresolved"
	OwnerStandalone OwnerResolution = "standalone"
)

var SystemNamespaces = []string{"kube-system", "kube-public", "kube-node-lease"}

func IsSystemNamespace(ns string) bool {
	return slices.Contains(SystemNamespaces, ns)
}

type WorkloadKey struct {
	Context    string
	Generation uint64
	Namespace  string
	Group      string
	Kind       string
	UID        string
	Pod        string
}

type WorkloadPosture struct {
	Key        WorkloadKey
	Name       string
	APIVersion string
	Resolution OwnerResolution
	Pods       []string
	Aggregate  SecurityAggregate
}

func (w WorkloadPosture) Label() string {
	switch w.Resolution {
	case OwnerStandalone, OwnerUnresolved:
		return "Pod/" + w.Name
	}
	return w.Key.Kind + "/" + w.Name
}

type WorkloadRule struct {
	Rule      string
	Version   string
	Dimension SecurityDimension
	State     AssessmentState
	Severity  Severity
	Pods      int
	Evidence  Finding
}

var knownControllers = map[[3]string]bool{
	{"apps", "v1", "ReplicaSet"}:        true,
	{"apps", "v1", "StatefulSet"}:       true,
	{"apps", "v1", "DaemonSet"}:         true,
	{"batch", "v1", "Job"}:              true,
	{"", "v1", "ReplicationController"}: true,
}

func parseGroupVersion(apiVersion string) (group, version string, ok bool) {
	if apiVersion == "" || strings.Count(apiVersion, "/") > 1 {
		return "", "", false
	}
	group, version, found := strings.Cut(apiVersion, "/")
	if !found {
		group, version = "", apiVersion
	}
	if version == "" || (found && group == "") {
		return "", "", false
	}
	return group, version, true
}

type workloadIdentity struct {
	key        WorkloadKey
	name       string
	apiVersion string
	resolution OwnerResolution
}

func workloadOf(key string, p PodPosture) workloadIdentity {
	id := workloadIdentity{key: WorkloadKey{Context: p.Target.Context, Generation: p.Target.Generation, Namespace: p.Target.Namespace}, name: p.Target.Name, resolution: OwnerStandalone}
	o := p.Owner
	if o == nil {
		id.key.Pod = key
		return id
	}
	group, version, ok := parseGroupVersion(o.APIVersion)
	if !ok || o.UID == "" || o.Kind == "" || o.Name == "" {
		id.key.Pod = key
		id.resolution = OwnerUnresolved
		return id
	}
	id.key.Group, id.key.Kind, id.key.UID = group, o.Kind, o.UID
	id.name, id.apiVersion, id.resolution = o.Name, o.APIVersion, OwnerDirect
	if !knownControllers[[3]string{group, version, o.Kind}] {
		id.resolution = OwnerExternal
	}
	return id
}

func SubsetSecurity(s SecuritySnapshot, keep func(PodPosture) bool) SecuritySnapshot {
	pods := make(map[string]PodPosture)
	for key, p := range s.Pods {
		if keep(p) {
			pods[key] = p
		}
	}
	return summarizeSubset(s, pods)
}

func summarizeSubset(s SecuritySnapshot, pods map[string]PodPosture) SecuritySnapshot {
	coverage := s.ListCoverage
	if coverage.State == "" {
		coverage = s.Coverage
	}
	out := SummarizeSecurity(pods, nil, coverage)
	out.Revision = s.Revision
	return out
}

func AggregateWorkloads(s SecuritySnapshot, keep func(PodPosture) bool) []WorkloadPosture {
	groups := make(map[WorkloadKey]*WorkloadPosture)
	for key, p := range s.Pods {
		if keep != nil && !keep(p) {
			continue
		}
		id := workloadOf(key, p)
		g, ok := groups[id.key]
		if !ok {
			g = &WorkloadPosture{Key: id.key, Name: id.name, APIVersion: id.apiVersion, Resolution: id.resolution}
			groups[id.key] = g
		}
		g.Pods = append(g.Pods, key)
	}
	sorted := make([]*WorkloadPosture, 0, len(groups))
	for _, g := range groups {
		slices.Sort(g.Pods)
		g.Aggregate = aggregateGroup(s, g.Pods)
		g.Aggregate.Name = g.Label()
		sorted = append(sorted, g)
	}
	slices.SortFunc(sorted, func(a, b *WorkloadPosture) int {
		as, bs := a.Aggregate.Score, b.Aggregate.Score
		if as.Available != bs.Available {
			if as.Available {
				return -1
			}
			return 1
		}
		if n := cmp.Compare(as.Value, bs.Value); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Key.Namespace, b.Key.Namespace); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Aggregate.Name, b.Aggregate.Name); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Key.UID, b.Key.UID); n != 0 {
			return n
		}
		return cmp.Compare(a.Key.Pod, b.Key.Pod)
	})
	out := make([]WorkloadPosture, len(sorted))
	for i, g := range sorted {
		out[i] = *g
	}
	return out
}

func WorkloadRules(s SecuritySnapshot, w WorkloadPosture) []WorkloadRule {
	rules := make(map[string]*WorkloadRule)
	for _, key := range w.Pods {
		p, ok := s.Pods[key]
		if !ok {
			continue
		}
		for _, a := range AssessPod(p) {
			if a.State != AssessmentUnsafe && a.State != AssessmentUnspecified && a.State != AssessmentUnknown {
				continue
			}
			r, ok := rules[a.Rule]
			if !ok {
				r = &WorkloadRule{Rule: a.Rule, Version: a.Version, Dimension: a.Dimension}
				rules[a.Rule] = r
			}
			r.Pods++
			worse := assessmentRank(a.State) > assessmentRank(r.State) || (a.State == r.State && severityRank(a.Severity) > severityRank(r.Severity))
			if worse || r.Evidence.Rule == "" {
				r.State, r.Severity = a.State, a.Severity
				if a.Version != "" {
					r.Version = a.Version
				}
				for _, f := range a.Evidence {
					if findingAssessment(f) == a.State {
						r.Evidence = f
						break
					}
				}
				if r.Evidence.Rule == "" && len(a.Evidence) > 0 {
					r.Evidence = a.Evidence[0]
				}
			}
		}
	}
	out := make([]WorkloadRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b WorkloadRule) int {
		if n := cmp.Compare(severityRank(b.Severity), severityRank(a.Severity)); n != 0 {
			return n
		}
		if n := cmp.Compare(assessmentRank(b.State), assessmentRank(a.State)); n != 0 {
			return n
		}
		return cmp.Compare(a.Rule, b.Rule)
	})
	return out
}

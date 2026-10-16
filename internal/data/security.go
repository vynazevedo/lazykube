package data

import "time"

type CoverageState string

const (
	CoverageCollecting  CoverageState = "collecting"
	CoverageComplete    CoverageState = "complete in scope"
	CoveragePartial     CoverageState = "partial"
	CoverageDenied      CoverageState = "no permission"
	CoverageUnavailable CoverageState = "unavailable"
	CoverageStale       CoverageState = "stale"
	CoverageUnsupported CoverageState = "unsupported"
	CoverageDisabled    CoverageState = "disabled"
)

type RuleResult string

const (
	ResultFinding       RuleResult = "finding"
	ResultNotFound      RuleResult = "not found"
	ResultUnknown       RuleResult = "unknown"
	ResultNotApplicable RuleResult = "not applicable"
)

type Severity string

const (
	SeverityHigh   Severity = "high"
	SeverityMedium Severity = "medium"
	SeverityInfo   Severity = "info"
)

type Confidence string

const (
	ConfidenceObserved Confidence = "configuration observed"
	ConfidenceUnknown  Confidence = "runtime unknown"
)

type ResourceRef struct {
	Context         string
	Generation      uint64
	Group           string
	Version         string
	Resource        string
	Namespace       string
	Name            string
	UID             string
	ResourceVersion string
}

type Finding struct {
	Rule           string
	RuleVersion    string
	Origin         string
	Severity       Severity
	Confidence     Confidence
	Result         RuleResult
	Target         ResourceRef
	Container      string
	Field          string
	Evidence       string
	Explanation    string
	Recommendation string
	ObservedAt     time.Time
}

type Coverage struct {
	State      CoverageState
	Reason     string
	Evaluated  int
	Total      int
	ObservedAt time.Time
}

type PodPosture struct {
	Target   ResourceRef
	Coverage Coverage
	Findings []Finding
}

type NamespacePosture struct {
	Name     string
	Coverage Coverage
	Findings int
	Unknown  int
}

type SecuritySnapshot struct {
	Pods       map[string]PodPosture
	Namespaces map[string]NamespacePosture
	Coverage   Coverage
}

type SecuritySource interface {
	Security() SecuritySnapshot
}

package data

import (
	"cmp"
	"slices"
)

const SecurityScoreVersion = "2"
const SecurityRuleVersion = "2"

type SecurityDimension string

const (
	DimensionSecurity SecurityDimension = "security"
	DimensionHygiene  SecurityDimension = "hygiene"
)

type AssessmentState string

const (
	AssessmentUnsafe        AssessmentState = "explicitly unsafe"
	AssessmentUnspecified   AssessmentState = "unspecified"
	AssessmentConformant    AssessmentState = "conformant with local rule"
	AssessmentUnknown       AssessmentState = "unknown"
	AssessmentNotApplicable AssessmentState = "not applicable"
)

type RuleAssessment struct {
	Rule      string
	Version   string
	Dimension SecurityDimension
	State     AssessmentState
	Severity  Severity
	Evidence  []Finding
}
type PodScores struct{ Security, Hygiene PostureScore }

func RuleDimension(rule string) SecurityDimension {
	switch rule {
	case "imageDigest", "imageLatest", "imageReference", "limits", "limit-cpu", "limit-memory":
		return DimensionHygiene
	default:
		return DimensionSecurity
	}
}

func severityRank(s Severity) int {
	switch s {
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}
func assessmentRank(s AssessmentState) int {
	switch s {
	case AssessmentUnsafe:
		return 5
	case AssessmentUnspecified:
		return 4
	case AssessmentUnknown:
		return 3
	case AssessmentConformant:
		return 2
	default:
		return 1
	}
}
func findingAssessment(f Finding) AssessmentState {
	if f.Assessment != "" {
		return f.Assessment
	}
	switch f.Result {
	case ResultFinding:
		return AssessmentUnsafe
	case ResultNotFound:
		return AssessmentConformant
	case ResultNotApplicable:
		return AssessmentNotApplicable
	default:
		return AssessmentUnknown
	}
}

func AssessPod(p PodPosture) []RuleAssessment {
	if p.Assessments != nil {
		return p.Assessments
	}
	rules := make(map[string]RuleAssessment)
	for _, f := range p.Findings {
		a, ok := rules[f.Rule]
		state := findingAssessment(f)
		severity := Severity("")
		if state == AssessmentUnsafe || state == AssessmentUnspecified {
			severity = f.Severity
		}
		if !ok {
			a = RuleAssessment{Rule: f.Rule, Version: f.RuleVersion, Dimension: RuleDimension(f.Rule), State: state, Severity: severity}
		}
		if severityRank(severity) > severityRank(a.Severity) {
			a.Severity = severity
		}
		if assessmentRank(state) > assessmentRank(a.State) {
			a.State = state
		}
		a.Evidence = append(a.Evidence, f)
		rules[f.Rule] = a
	}
	result := make([]RuleAssessment, 0, len(rules))
	for _, a := range rules {
		result = append(result, a)
	}
	slices.SortFunc(result, func(a, b RuleAssessment) int { return cmp.Compare(a.Rule, b.Rule) })
	return result
}

func ScoreRuleAssessments(c Coverage, assessments []RuleAssessment) PodScores {
	return scorePod(PodPosture{Coverage: c, Assessments: assessments}, nil)
}

func ScorePodPosture(p PodPosture) PodScores { return scorePod(p, nil) }

func scorePod(p PodPosture, visit func(string, Severity)) PodScores {
	worst := make(map[string]Severity)
	add := func(rule string, severity Severity) {
		if old, ok := worst[rule]; !ok || severityRank(severity) > severityRank(old) {
			worst[rule] = severity
		}
	}
	if p.Assessments != nil {
		for _, a := range p.Assessments {
			if a.State == AssessmentUnsafe || a.State == AssessmentUnspecified {
				add(a.Rule, a.Severity)
			}
		}
	} else {
		for _, f := range p.Findings {
			if f.Result == ResultFinding {
				add(f.Rule, f.Severity)
			}
		}
	}
	var security, hygiene [3]int
	for rule, severity := range worst {
		counts := &security
		if RuleDimension(rule) == DimensionHygiene {
			counts = &hygiene
		}
		switch severity {
		case SeverityHigh:
			counts[0]++
		case SeverityMedium:
			counts[1]++
		case SeverityInfo:
			counts[2]++
		}
		if visit != nil {
			visit(rule, severity)
		}
	}
	return PodScores{ScorePosture(p.Coverage, security[0], security[1], security[2]), ScorePosture(p.Coverage, hygiene[0], hygiene[1], hygiene[2])}
}

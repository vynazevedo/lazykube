package ui

import (
	"fmt"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
)

func securityScoreLabel(s data.PostureScore) string {
	if !s.Available {
		reason := s.Coverage.Reason
		if reason == "" {
			reason = string(s.Coverage.State)
		}
		if s.Coverage.Evaluated == 0 {
			reason += "; no evaluated pods"
		}
		return "not rated | " + reason
	}
	partial := ""
	if s.Partial {
		partial = " partial"
	}
	return fmt.Sprintf("%d/100 (%s)%s | %d/%d %s", s.Value, s.Grade, partial, s.Coverage.Evaluated, s.Coverage.Total, securityPlural(s.Coverage.Total, "pod"))
}

func securityBar(value, maximum, width int) string {
	filled := draw.Filled(float64(value), float64(maximum), width)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func DashboardSecurityView(detail SecurityView, session data.SecuritySession, scope, pod string, width int) SecurityView {
	a := session.Dashboard.Cluster
	scopeLabel := "Cluster"
	if scope != "" {
		scopeLabel = "Namespace: " + scope
		var ok bool
		a, ok = session.Dashboard.Namespaces[scope]
		if !ok {
			a.Score.Coverage = data.Coverage{State: data.CoverageUnavailable, Reason: "namespace not observed"}
		}
	}
	detail.Coverage = fmt.Sprintf("Dashboard coverage: %s | pods %d/%d", a.Score.Coverage.State, a.Score.Coverage.Evaluated, a.Score.Coverage.Total)
	rows := make([]string, 0, 64+len(detail.Rows))
	add := func(s string) { rows = append(rows, draw.Wrap(s, max(1, width))...) }
	namespace, _, _ := strings.Cut(pod, "/")
	if namespace == "" {
		namespace = scope
	}
	selected := session.Dashboard.Pods[pod]
	ns := session.Dashboard.Namespaces[namespace]
	cluster := session.Dashboard.Cluster
	add("Security /100: Pod " + securityCompactScore(selected) + " | Namespace " + securityCompactScore(ns.Score) + " | Cluster " + securityCompactScore(cluster.Score))
	add("Hygiene /100: Pod " + securityCompactScore(session.Dashboard.PodHygiene[pod]) + " | Namespace " + securityCompactScore(ns.Hygiene) + " | Cluster " + securityCompactScore(cluster.Hygiene))
	add(fmt.Sprintf("Namespace mean (%d pods) | Cluster mean (%d pods)", ns.Score.Coverage.Evaluated, cluster.Score.Coverage.Evaluated))
	add("Selected pod security: " + securityScoreLabel(selected))
	if detail.Synthetic {
		add("Synthetic data (demo)")
	}
	rows = append(rows, detail.Rows...)
	add("POSTURE | " + scopeLabel + " | " + securityScoreLabel(a.Score))
	if a.Score.Available {
		add("Score " + securityBar(a.Score.Value, 100, max(1, min(36, width-6))))
	}
	add("SEC column: Security; p = partial. Hygiene scored separately.")
	for _, dimension := range []struct {
		name         string
		score        data.PostureScore
		distribution data.ScoreDistribution
	}{{"Security", a.Score, a.Distribution}, {"Hygiene", a.Hygiene, a.HygieneDistribution}} {
		if !dimension.score.Available {
			add(dimension.name + ": " + securityScoreLabel(dimension.score))
			continue
		}
		add(fmt.Sprintf("%s mean %d | median %d | worst decile %d", dimension.name, dimension.distribution.Mean, dimension.distribution.Median, dimension.distribution.WorstDecile))
		for i, grade := range "ABCDEF" {
			n := dimension.distribution.Grades[i]
			add(fmt.Sprintf("%c %4d %s", grade, n, securityBar(n, max(1, dimension.distribution.Count), min(24, max(1, width-8)))))
		}
	}
	add("Worst decile = mean of lowest ceil(N/10) pod scores.")
	maximum := max(1, a.High, a.Medium, a.Info)
	for _, severity := range []struct {
		name  string
		count int
	}{{"high", a.High}, {"medium", a.Medium}, {"info", a.Info}} {
		add(fmt.Sprintf("%-6s %4d %s", severity.name, severity.count, securityBar(severity.count, maximum, max(1, min(24, width-12)))))
	}
	add(fmt.Sprintf("Unknown checks: %d | confirmed findings only in score", a.Unknown))
	history := session.History[scope]
	if len(history) > width-9 && width > 9 {
		history = history[len(history)-(width-9):]
	}
	if !a.Score.Available {
		add("Trend: paused until coverage is evaluable")
	} else {
		add("Trend: " + draw.Sparkline(history, 100) + " (revision samples)")
	}
	add("ALERTS | new high since baseline / last ack | A: ack all")
	count := 0
	for _, f := range session.Alerts {
		if scope != "" && f.Target.Namespace != scope {
			continue
		}
		count++
		if count <= 5 {
			container := f.Container
			if container == "" {
				container = "pod"
			}
			add("! " + f.Target.Namespace + "/" + f.Target.Name + " | " + f.Rule + " | " + container)
		}
	}
	if count == 0 {
		add("No unacknowledged high alerts.")
	}
	if count > 5 {
		add(fmt.Sprintf("%d more %s below pod details.", count-5, securityPlural(count-5, "alert")))
	}
	add("TOP RULES | affected pods, both dimensions")
	for i, rule := range a.Rules {
		if i == 5 {
			break
		}
		add(fmt.Sprintf("%-22s %4d %s", draw.Truncate(rule.Rule, 22), rule.Count, securityBar(rule.Count, max(1, a.Rules[0].Count), max(1, min(18, width-28)))))
	}
	if len(a.Rules) == 0 {
		add("No confirmed findings in observed checks.")
	}
	add("NAMESPACES | lowest scores first")
	shown := 0
	for _, ns := range session.Dashboard.Ranking {
		if scope != "" && ns.Name != scope {
			continue
		}
		if shown == 5 {
			break
		}
		shown++
		if ns.Score.Available {
			label := fmt.Sprintf("%d", ns.Score.Value)
			if ns.Score.Partial {
				label += " partial"
			}
			add(fmt.Sprintf("%-18s %s %s | %d %s", draw.Truncate(ns.Name, 18), label, securityBar(ns.Score.Value, 100, 8), ns.High+ns.Medium+ns.Info, securityPlural(ns.High+ns.Medium+ns.Info, "finding")))
		} else {
			add(ns.Name + " | " + securityScoreLabel(ns.Score))
		}
	}
	if scope == "" && len(session.Dashboard.Ranking) > 5 {
		add("More namespaces: use the namespace filter.")
	}
	add("Score: " + data.SecurityScoreFormula)
	add("Grades: A >=90, B >=80, C >=70, D >=60, E >=50, F <50. Not a safety rating.")

	if count > 5 {
		add("MORE ALERTS | pending since baseline / last ack")
		seen := 0
		for _, f := range session.Alerts {
			if scope != "" && f.Target.Namespace != scope {
				continue
			}
			seen++
			if seen <= 5 {
				continue
			}
			if len(rows) >= maxSecurityRows {
				rows = append(rows, "More alerts omitted; narrow the namespace filter.")
				break
			}
			container := f.Container
			if container == "" {
				container = "pod"
			}
			add("! " + f.Target.Namespace + "/" + f.Target.Name + " | " + f.Rule + " | " + container)
		}
	}
	if len(rows) > maxSecurityRows {
		rows = rows[:maxSecurityRows]
		rows[len(rows)-1] = "More results omitted; select a pod for details."
	}
	detail.Rows = rows
	return detail
}

func securityPlural(count int, singular string) string {
	if count == 1 {
		return singular
	}
	return singular + "s"
}

func securityCompactScore(s data.PostureScore) string {
	if !s.Available {
		return "-"
	}
	label := fmt.Sprintf("%d %s", s.Value, s.Grade)
	if s.Partial {
		label += " partial"
	}
	return label
}

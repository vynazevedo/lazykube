package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

const SecurityDisclaimer = "No findings does not mean safe. Local checks only; not PSS compliance."
const securityHeaderRows = 4
const maxSecurityRows = 4096

type SecurityView struct {
	Synthetic bool
	Scope     string
	Coverage  string
	Rows      []string
}

func BuildSecurityView(s data.SecuritySnapshot, key, namespace string, namespaceView bool, width int) SecurityView {
	v := SecurityView{Scope: "S: namespace | Pod: " + key}
	coverage := data.Coverage{State: data.CoverageUnavailable, Reason: "no selected pod"}
	var pods []data.PodPosture
	if namespaceView {
		v.Scope = "S: pod | Namespace: " + namespace
		if n, ok := s.Namespaces[namespace]; ok {
			coverage = n.Coverage
		} else {
			coverage = data.Coverage{State: data.CoverageUnavailable, Reason: "namespace not observed"}
		}
		for _, p := range s.Pods {
			if p.Target.Namespace == namespace {
				pods = append(pods, p)
			}
		}
		sort.Slice(pods, func(i, j int) bool { return pods[i].Target.Name < pods[j].Target.Name })
	} else if p, ok := s.Pods[key]; ok {
		coverage, pods = p.Coverage, []data.PodPosture{p}
	} else if s.Coverage.State != "" {
		coverage = s.Coverage
	}
	v.Coverage = fmt.Sprintf("Coverage: %s | pods evaluated %d/%d", coverage.State, coverage.Evaluated, coverage.Total)
	truncated := false
	appendRow := func(text string) {
		rows := draw.Wrap(text, max(1, width))
		remaining := maxSecurityRows - len(v.Rows)
		if len(rows) > remaining {
			rows, truncated = rows[:remaining], true
		}
		v.Rows = append(v.Rows, rows...)
	}
	if coverage.Reason != "" {
		appendRow(coverage.Reason)
	}
	for _, p := range pods {
		if truncated {
			break
		}
		appendRow("Pod " + p.Target.Namespace + "/" + p.Target.Name + " | " + string(p.Coverage.State))
		if p.Coverage.Reason != "" {
			appendRow(p.Coverage.Reason)
		}
		appendRow("Observed " + p.Coverage.ObservedAt.UTC().Format("2006-01-02 15:04:05Z") + " | UID " + p.Target.UID + " | RV " + p.Target.ResourceVersion)
		if len(p.Findings) == 0 {
			appendRow("No findings in evaluated local checks.")
		}
		for _, f := range p.Findings {
			if truncated {
				break
			}
			container := f.Container
			if container == "" {
				container = "pod"
			}
			appendRow(fmt.Sprintf("[%s] %s | %s | container: %s", f.Severity, f.Rule, f.Result, container))
			appendRow("Field: " + f.Field)
			appendRow("Evidence: " + f.Evidence)
			appendRow(f.Explanation + ". " + f.Recommendation)
			appendRow("Source: " + f.Origin + " v" + f.RuleVersion + " | " + string(f.Confidence))
		}
	}
	if len(pods) == 0 {
		appendRow("No pod observations in this scope.")
	}
	if truncated {
		v.Coverage = "Display truncated | " + v.Coverage
		v.Rows[len(v.Rows)-1] = "More results omitted; select a pod for details."
	}
	return v
}

func SecurityTab(r draw.Region, view SecurityView, scroll int) {
	c := insetBox(r)
	c.Text(0, 0, draw.Truncate(view.Scope, c.W), draw.Fg(theme.Text))
	c.Text(0, 1, draw.Truncate(view.Coverage, c.W), draw.Fg(theme.Muted))
	warning := strings.SplitN(SecurityDisclaimer, ". ", 2)
	for i, line := range warning {
		c.Text(0, 2+i, draw.Truncate(line, c.W), draw.Fg(theme.Warn))
	}
	for i := 0; i < c.H-securityHeaderRows && scroll+i < len(view.Rows); i++ {
		row := draw.Truncate(view.Rows[scroll+i], c.W)
		style := draw.Fg(theme.TextSoft)
		if strings.HasPrefix(row, "! ") || strings.HasPrefix(row, "high ") {
			style = draw.Fg(theme.Err).WithBold()
		}
		if strings.HasPrefix(row, "POSTURE") || strings.HasPrefix(row, "ALERTS") {
			style = draw.Fg(theme.Warn).WithBold()
		}
		c.Text(0, securityHeaderRows+i, row, style)
		if strings.HasPrefix(row, "Score ") {
			for x, ch := range []rune(row) {
				if x >= 6 && ch == '█' {
					c.Put(x, securityHeaderRows+i, ch, draw.Fg(theme.Mix(theme.Warn, theme.OK, float64(x-6)/float64(max(1, draw.Width(row)-7)))))
				}
			}
		}
	}
}

package app

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
)

func securityFixture(pods, findings int) data.SecuritySnapshot {
	out := make(map[string]data.PodPosture, pods)
	for i := range pods {
		ns, name := "ns"+strconv.Itoa(i%50), "p"+strconv.Itoa(i)
		target := data.ResourceRef{Context: "ctx", Generation: 1, Version: "v1", Resource: "pods", Namespace: ns, Name: name, UID: "uid-" + name}
		p := data.PodPosture{
			Owner:    &data.OwnerRef{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs" + strconv.Itoa(i/4), UID: "rs-" + strconv.Itoa(i/4)},
			Target:   target,
			Coverage: data.Coverage{State: data.CoverageComplete, Evaluated: 1, Total: 1},
		}
		for f := range findings {
			severity := data.SeverityMedium
			if f == 0 {
				severity = data.SeverityHigh
			}
			p.Findings = append(p.Findings, data.Finding{Rule: "rule" + strconv.Itoa(f), RuleVersion: data.SecurityRuleVersion, Severity: severity, Result: data.ResultFinding, Container: "c", Target: target, Field: "spec.containers[0].securityContext"})
		}
		out[data.PodKey(ns, name)] = p
	}
	s := data.SummarizeSecurity(out, nil, data.Coverage{State: data.CoverageComplete})
	s.Revision = 1
	return s
}

func BenchmarkSecurityUpdateThread(b *testing.B) {
	for _, findings := range []int{1, 12} {
		source := &largeSecuritySource{Source: data.NewDemo(), snapshot: securityFixture(10000, findings)}
		for _, screen := range []ScreenID{ScreenDashboard, ScreenSecurity} {
			b.Run(fmt.Sprintf("findings=%d/screen=%d", findings, screen), func(b *testing.B) {
				m := New(Options{Source: source, NoAnim: true})
				m.setLayout(160, 48)
				m.activateScreen(screen)
				m.refresh()
				b.ReportAllocs()
				for b.Loop() {
					source.snapshot.Revision++
					next, _ := m.Update(dataMsg{})
					m = next.(Model)
					b.StopTimer()
					m = settleSecurity(m)
					b.StartTimer()
				}
			})
		}
		b.Run(fmt.Sprintf("findings=%d/apply", findings), func(b *testing.B) {
			m := New(Options{Source: source, NoAnim: true})
			m.setLayout(160, 48)
			m.activateScreen(ScreenSecurity)
			next, _ := m.Update(dataMsg{})
			m = next.(Model)
			msg := m.secCalc.job.run()
			msg.session, msg.seq = m.session.Generation, m.secCalc.seq
			b.ReportAllocs()
			for b.Loop() {
				if applied, ok := m.updateSecurityCalc(msg); !ok || !applied.sec.have {
					b.Fatal("result not applied")
				}
			}
		})
		b.Run(fmt.Sprintf("findings=%d/worker", findings), func(b *testing.B) {
			m := New(Options{Source: source, NoAnim: true})
			job := securityJob{ctx: b.Context(), key: m.wantedSecurityKey(), snapshot: m.security, dash: m.securitySession.Dashboard}
			b.ReportAllocs()
			for b.Loop() {
				if msg := job.run(); msg.canceled {
					b.Fatal("worker canceled")
				}
			}
		})
		b.Run(fmt.Sprintf("findings=%d/clone+session", findings), func(b *testing.B) {
			b.ReportAllocs()
			var session data.SecuritySession
			for b.Loop() {
				source.snapshot.Revision++
				session.Update(source.Security())
			}
		})
	}
}

package app

import (
	"fmt"
	"github.com/TNK-Studio/lazykube/internal/data"
	"testing"
)

type revisionSecuritySource struct {
	*data.Demo
	revision uint64
	copies   int
}

func (s *revisionSecuritySource) SecurityRevision() uint64 { return s.revision }
func (s *revisionSecuritySource) Security() data.SecuritySnapshot {
	s.copies++
	snapshot := s.Demo.Security()
	snapshot.Revision = s.revision
	return snapshot
}

func TestUnchangedDataMessagesDoNotCopySecurity(t *testing.T) {
	s := &revisionSecuritySource{Demo: data.NewDemo(), revision: 1}
	m := New(Options{Source: s, NoAnim: true})
	m.setLayout(160, 48)
	m.refresh()
	rows := m.securityView.Rows
	for range 10 {
		next, _ := m.Update(dataMsg{})
		m = next.(Model)
	}
	if s.copies != 1 {
		t.Fatalf("unchanged posture copied %d times", s.copies)
	}
	if &rows[0] != &m.securityView.Rows[0] {
		t.Fatal("unchanged presentation rebuilt")
	}
	s.revision++
	next, _ := m.Update(dataMsg{})
	m = next.(Model)
	if s.copies != 2 || &rows[0] == &m.securityView.Rows[0] {
		t.Fatal("new revision not consumed")
	}
}

type largeSecuritySource struct {
	data.Source
	snapshot data.SecuritySnapshot
	copies   int
}

func (s *largeSecuritySource) SecurityRevision() uint64        { return s.snapshot.Revision }
func (s *largeSecuritySource) Security() data.SecuritySnapshot { s.copies++; return s.snapshot.Clone() }

func BenchmarkSecurityConsumptionUnchanged(b *testing.B) {
	for _, count := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			snapshot := data.SecuritySnapshot{Revision: 1, Coverage: data.Coverage{State: data.CoverageComplete}, Pods: make(map[string]data.PodPosture, count)}
			for i := range count {
				snapshot.Pods[fmt.Sprint(i)] = data.PodPosture{Findings: make([]data.Finding, 12)}
			}
			source := &largeSecuritySource{Source: data.NewDemo(), snapshot: snapshot}
			m := Model{src: source}
			m.loadSecurity()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				m.loadSecurity()
			}
			if source.copies != 1 {
				b.Fatalf("unchanged snapshots copied %d times", source.copies)
			}
		})
	}
}

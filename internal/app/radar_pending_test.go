package app

import (
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
)

type gatedObservations struct {
	fakeLive
	revision atomic.Uint64
	inFlight atomic.Int32
	peak     atomic.Int32
	reads    atomic.Int32
	entered  chan struct{}
	release  chan struct{}
}

func newGatedObservations() *gatedObservations {
	s := &gatedObservations{fakeLive: fakeLive{Demo: data.NewDemo(), changes: make(chan struct{})}, entered: make(chan struct{}), release: make(chan struct{})}
	s.revision.Store(1)
	return s
}

func (s *gatedObservations) Observations(insight.Cursor) insight.ObservationBatch {
	s.peak.Store(max(s.peak.Load(), s.inFlight.Add(1)))
	defer s.inFlight.Add(-1)
	s.reads.Add(1)
	rev := s.revision.Load()
	s.entered <- struct{}{}
	<-s.release
	return insight.ObservationBatch{Cursor: insight.Cursor{Revisions: insight.Revisions{Inventory: rev}}, FullInventory: true, Coverage: insight.SourceCoverage{Inventory: insight.Coverage{State: insight.CoverageComplete}}}
}

func (s *gatedObservations) start(cmd tea.Cmd) <-chan tea.Msg {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-s.entered
	return done
}

func (s *gatedObservations) finish(done <-chan tea.Msg) tea.Msg {
	s.release <- struct{}{}
	return <-done
}

func TestRadarCoalescesUpdatesDuringRead(t *testing.T) {
	src := newGatedObservations()
	m := New(Options{Source: src, NoAnim: true})
	done := src.start(m.radarInit())
	for rev := uint64(2); rev <= 5; rev++ {
		src.revision.Store(rev)
		m = deliver(t, m, dataMsg{session: m.session.Generation})
	}
	if !m.radar.pending || src.reads.Load() != 1 {
		t.Fatal("notifications during a read become one pending step")
	}
	next, retry := m.Update(src.finish(done))
	m = next.(Model)
	if retry == nil || m.radar.pending {
		t.Fatal("the pending step starts when the running one ends")
	}
	next, after := m.Update(src.finish(src.start(retry)))
	m = next.(Model)
	if got := m.radar.feed.cursor.Revisions.Inventory; got != 5 || after != nil || m.radar.pending || src.reads.Load() != 2 || src.peak.Load() != 1 {
		t.Fatalf("four notifications cost one extra read of the latest revision: cursor %d, reads %d, peak %d", got, src.reads.Load(), src.peak.Load())
	}
}

func TestRadarTickDuringReadIsNotLost(t *testing.T) {
	src := newGatedObservations()
	m := New(Options{Source: src, NoAnim: true})
	done := src.start(m.radarInit())
	m = deliver(t, m, radarTickMsg{session: m.session.Generation})
	if _, retry := m.Update(src.finish(done)); retry == nil {
		t.Fatal("a tick during a read is reprocessed")
	}
}

func TestRadarAppliesResultsInOrder(t *testing.T) {
	m := newRadarHarness(t).active(160, 48)
	newer := insight.SignalSnapshot{Coverage: insight.Coverage{State: insight.CoverageComplete, Observed: 9, Total: 9}}
	older := insight.SignalSnapshot{Coverage: insight.Coverage{State: insight.CoverageComplete, Observed: 1, Total: 1}}
	base := m.radar.applied
	m = deliver(t, m, radarMsg{session: m.session.Generation, seq: base + 2, snap: newer})
	m = deliver(t, m, radarMsg{session: m.session.Generation, seq: base + 1, snap: older})
	if m.radar.snap.Coverage.Observed != 9 {
		t.Fatal("an older result never replaces a newer one")
	}
}

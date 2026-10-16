package kube

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
	"k8s.io/client-go/kubernetes/fake"
)

func observationHeap() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func BenchmarkObservationPublication10000(b *testing.B) {
	b.Run("lean", func(b *testing.B) { observationBenchmark(b, false, "") })
	b.Run("evidence", func(b *testing.B) { observationBenchmark(b, false, strings.Repeat("x", 1024)) })
}

func BenchmarkObservationChangedRead10000(b *testing.B) {
	b.Run("lean", func(b *testing.B) { observationBenchmark(b, true, "") })
	b.Run("evidence", func(b *testing.B) { observationBenchmark(b, true, strings.Repeat("x", 1024)) })
}

func observationBenchmark(b *testing.B, read bool, message string) {
	pods := observationBudgetPods(10000, 32, message)
	s := newSource(fake.NewClientset(), nil, Options{Now: func() time.Time { return fixedNow }})
	defer s.cancel()
	s.podCoverage = data.Coverage{State: data.CoverageComplete}
	base := observationHeap()
	var snapshot insight.ObservationBatch
	b.ReportAllocs()
	b.ResetTimer()
	if read {
		b.StopTimer()
	}
	for i := range b.N {
		rv := strconv.Itoa(i + 2)
		for _, p := range pods {
			p.ResourceVersion = rv
		}
		s.publishObservations(pods)
		if read {
			b.StartTimer()
			snapshot = s.Observations(snapshot.Cursor)
			b.StopTimer()
			if snapshot.Unchanged {
				b.Fatal("benchmark needs changed batch")
			}
		}
	}
	if !read {
		b.StopTimer()
	}
	retained := observationHeap()
	if retained > base {
		b.ReportMetric(float64(retained-base), "retained-bytes")
	}
	runtime.KeepAlive(snapshot)
	runtime.KeepAlive(s)
	runtime.KeepAlive(pods)
}

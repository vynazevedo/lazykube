package kube

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestReviewNoAllocationAfterTextBudget(t *testing.T) {
	input := strings.Repeat("text\x1b[31m  á\t", 10000)
	if allocs := testing.AllocsPerRun(5, func() {
		budget := observationBudget{}
		p := observationProjector{budget: &budget}
		if p.text(input) != "" {
			t.Fatal("budget ignored")
		}
	}); allocs != 0 {
		t.Fatalf("exhausted budget allocated %.0f times", allocs)
	}
}

func TestReviewProjectionOutsideSourceLock(t *testing.T) {
	var calls atomic.Int32
	var armed atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	now := func() time.Time {
		if armed.Load() && calls.Add(1) == 2 {
			close(entered)
			<-release
		}
		return fixedNow
	}
	s := newSource(fake.NewClientset(), nil, Options{Now: now})
	defer s.cancel()
	s.podInf = cache.NewSharedIndexInformer(&cache.ListWatch{}, &corev1.Pod{}, 0, cache.Indexers{})
	s.podCoverage = data.Coverage{State: data.CoverageComplete}
	if err := s.podInf.GetStore().Add(hardenedPod()); err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	done := make(chan struct{})
	go func() { s.rebuild(); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("projection not reached")
	}
	read := make(chan struct{})
	go func() { s.Observations(insight.Cursor{}); close(read) }()
	blocked := false
	select {
	case <-read:
	case <-time.After(100 * time.Millisecond):
		blocked = true
	}
	close(release)
	<-done
	<-read
	if blocked {
		t.Fatal("projection holds reader mutex")
	}
}

func TestReviewBoundedSanitizerMatchesExisting(t *testing.T) {
	for _, input := range []string{"a\tb\nc", "  á界 hello\u00a0world ", "x\x1b[31mred\x1b[0m\x1b]hidden\a!", "x\x1bPprivate\x1b\\ y", string([]byte{0xff, 'x'})} {
		expected := strings.Join(strings.Fields(data.Sanitize(input)), " ")
		for _, limit := range []int{0, 1, 2, 5, 512} {
			budget := observationBudget{TextBytes: limit}
			p := observationProjector{budget: &budget}
			result := p.text(input)
			n := min(len(expected), limit)
			for n > 0 && n < len(expected) && !utf8.RuneStart(expected[n]) {
				n--
			}
			if result != expected[:n] || p.omissions.TextBytes != len(expected)-n {
				t.Fatalf("input %q limit %d got %q omitted %d expected %q", input, limit, result, p.omissions.TextBytes, expected[:n])
			}
		}
	}
}

func BenchmarkReviewSanitizer(b *testing.B) {
	for _, tc := range []struct {
		name         string
		size, budget int
	}{{"short", 64, 512}, {"long", 1 << 20, 512}, {"exhausted", 1 << 20, 0}} {
		b.Run(tc.name, func(b *testing.B) {
			input := strings.Repeat("x", tc.size)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				budget := observationBudget{TextBytes: tc.budget}
				p := observationProjector{budget: &budget}
				_ = p.text(input)
			}
		})
	}
}

func BenchmarkReviewRebuildReaders(b *testing.B) {
	pods := observationBudgetPods(1000, 10, strings.Repeat("x", 1024))
	source := newSource(fake.NewClientset(), nil, Options{Now: func() time.Time { return fixedNow }})
	defer source.cancel()
	source.podInf = cache.NewSharedIndexInformer(&cache.ListWatch{}, &corev1.Pod{}, 0, cache.Indexers{})
	source.podCoverage = data.Coverage{State: data.CoverageComplete}
	for _, pod := range pods {
		if err := source.podInf.GetStore().Add(pod); err != nil {
			b.Fatal(err)
		}
	}
	source.rebuild()
	var maxRead atomic.Int64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		cursor := source.Observations(insight.Cursor{}).Cursor
		for {
			select {
			case <-stop:
				return
			default:
			}
			start := time.Now()
			next := source.Observations(cursor)
			elapsed := time.Since(start).Nanoseconds()
			maxRead.Store(max(maxRead.Load(), elapsed))
			cursor = next.Cursor
			time.Sleep(100 * time.Microsecond)
		}
	}()
	b.ReportAllocs()
	b.ResetTimer()
	for n := range b.N {
		b.StopTimer()
		for _, pod := range pods {
			next := pod.DeepCopy()
			next.ResourceVersion = fmt.Sprint(n + 2)
			if err := source.podInf.GetStore().Update(next); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		source.rebuild()
	}
	b.StopTimer()
	close(stop)
	<-done
	b.ReportMetric(float64(maxRead.Load()), "max-reader-ns")
}

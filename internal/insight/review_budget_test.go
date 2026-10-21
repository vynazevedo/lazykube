package insight

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

func reviewLargeInventory(pods, containers int) []PodObservation {
	out := make([]PodObservation, pods)
	for n := range out {
		p := observationFixture(fmt.Sprint(n), "1")
		p.Entity.Name = fmt.Sprintf("pod-%05d", n)
		p.Phase = PodRunning
		p.Containers = make([]ContainerObservation, containers)
		for c := range p.Containers {
			p.Containers[c] = ContainerObservation{Name: fmt.Sprint(c), StatusObserved: true, Phase: ContainerRunning, Ready: TruthTrue}
		}
		out[n] = p
	}
	return out
}

func TestReviewMachinesBoundedDuringGeneration(t *testing.T) {
	clock := &observationClock{now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	engine, _ := NewEngine(clock, DefaultOperationalConfig())
	pods := reviewLargeInventory(1000, 10)
	batch := ObservationBatch{Cursor: Cursor{Revisions: Revisions{Inventory: 1}}, FullInventory: true, Pods: pods, Coverage: SourceCoverage{Inventory: Coverage{State: CoverageComplete}}}
	out := engine.Step(batch)
	if len(engine.machines) > MaxOperationalSignals {
		t.Fatalf("materialized %d machines", len(engine.machines))
	}
	if out.Coverage.State != CoveragePartial || out.OmittedSignals != 42000-len(out.Signals) {
		t.Fatal("generation omissions hidden")
	}
	retained := 0
	for _, pod := range engine.pods {
		retained += len(pod.Containers)
	}
	if retained > MaxOperationalSignals/4 {
		t.Fatalf("retained excess containers: %d", retained)
	}
	if out = engine.Advance(); out.Coverage.State != CoveragePartial {
		t.Fatal("timer hid omissions")
	}
}

func reviewHeap() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

func BenchmarkReviewEngine10000(b *testing.B) {
	for _, operation := range []string{"Step", "Advance", "Snapshot"} {
		b.Run(operation, func(b *testing.B) {
			clock := &observationClock{now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
			pods := reviewLargeInventory(10000, 10)
			base := reviewHeap()
			inventory := NewInventory(clock)
			inventory.Publish(SourceIdentity{SessionID: "bench"}, "", false, pods, Coverage{State: CoverageComplete})
			batch := inventory.Observations(Cursor{})
			engine, _ := NewEngine(clock, DefaultOperationalConfig())
			var result SignalSnapshot
			if operation != "Step" {
				engine.Step(batch)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for n := range b.N {
				switch operation {
				case "Step":
					batch.Cursor.Revisions.Inventory = uint64(n + 1)
					result = engine.Step(batch)
				case "Advance":
					result = engine.Advance()
				case "Snapshot":
					result = engine.Snapshot()
				}
			}
			b.StopTimer()
			if heap := reviewHeap(); heap > base {
				b.ReportMetric(float64(heap-base), "retained-bytes")
			}
			b.ReportMetric(float64(len(engine.machines)), "machines")
			runtime.KeepAlive(inventory)
			runtime.KeepAlive(batch)
			runtime.KeepAlive(result)
			runtime.KeepAlive(engine)
			runtime.KeepAlive(pods)
		})
	}
}

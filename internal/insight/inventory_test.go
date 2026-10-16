package insight

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

type observationClock struct{ now time.Time }

func (c *observationClock) Now() time.Time { return c.now }

func observationFixture(uid, rv string) PodObservation {
	return PodObservation{Entity: EntityRef{EntityKey: EntityKey{Version: "v1", Kind: "Pod", Namespace: "apps", Name: "api", UID: uid}, ResourceVersion: rv}, Containers: []ContainerObservation{{Name: "app", Kind: ContainerRegular, LastTermination: &Termination{Reason: ReasonOOMKilled}}}}
}

func TestInventoryReplayAndIdentity(t *testing.T) {
	clock := &observationClock{now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	inventory := NewInventory(clock)
	source := SourceIdentity{SessionID: "test", Context: "fake", Generation: 1}
	coverage := Coverage{State: CoverageComplete}
	pod := observationFixture("uid1", "rv-one")
	if !inventory.Publish(source, "apps", false, []PodObservation{pod}, coverage) {
		t.Fatal("missing initial publication")
	}
	first := inventory.Observations(Cursor{})
	clock.now = clock.now.Add(time.Minute)
	if inventory.Publish(source, "apps", false, []PodObservation{pod}, coverage) {
		t.Fatal("replay advanced revision")
	}
	if !reflect.DeepEqual(first, inventory.Observations(Cursor{})) {
		t.Fatal("replay changed timestamps or content")
	}
	unchanged := inventory.Observations(first.Cursor)
	if !unchanged.Unchanged || unchanged.FullInventory || len(unchanged.Pods) != 0 {
		t.Fatal("cursor must suppress copies")
	}
	pod.Entity.ResourceVersion = "opaque-older-looking-rv"
	inventory.Publish(source, "apps", false, []PodObservation{pod}, coverage)
	updated := inventory.Observations(Cursor{})
	if updated.Pods[0].FirstObservedAt != first.Pods[0].FirstObservedAt || updated.Pods[0].ReceivedAt != clock.now {
		t.Fatal("revision update lost observed times")
	}
	pod.Entity.UID = "uid2"
	inventory.Publish(source, "apps", false, []PodObservation{pod}, coverage)
	recreated := inventory.Observations(Cursor{})
	if recreated.Pods[0].FirstObservedAt != clock.now || recreated.Pods[0].Entity.EntityKey == first.Pods[0].Entity.EntityKey {
		t.Fatal("new UID retained old identity or baseline")
	}
	source.Generation++
	clock.now = clock.now.Add(time.Minute)
	inventory.Publish(source, "apps", false, []PodObservation{pod}, coverage)
	reset := inventory.Observations(recreated.Cursor)
	if reset.Unchanged || reset.Cursor.Revisions.Inventory != 1 || reset.Pods[0].FirstObservedAt != clock.now {
		t.Fatal("source change did not reset")
	}
	inventory.Publish(source, "apps", false, nil, coverage)
	removed := inventory.Observations(reset.Cursor)
	if !removed.FullInventory || len(removed.Pods) != 0 || removed.Coverage.Inventory.State != CoverageComplete {
		t.Fatal("authoritative empty snapshot missing")
	}
}

func TestInventoryOwnershipAndCoverage(t *testing.T) {
	clock := &observationClock{now: time.Unix(1000, 0)}
	inventory := NewInventory(clock)
	source := SourceIdentity{SessionID: "a"}
	pod := observationFixture("uid", "rv")
	pod.Conditions = []Condition{{Kind: ConditionReady, Status: TruthTrue}}
	pod.Owners = []OwnerRef{{Entity: EntityRef{EntityKey: EntityKey{UID: "owner"}}}}
	inventory.Publish(source, "apps", true, []PodObservation{pod}, Coverage{State: CoverageComplete})
	pod.Containers[0].LastTermination.Reason = ReasonOther
	first := inventory.Observations(Cursor{})
	first.Pods[0].Containers[0].LastTermination.Reason = ReasonCompleted
	first.Pods[0].Conditions[0].Status = TruthFalse
	first.Pods[0].Owners[0].Entity.UID = "changed"
	stored := inventory.Observations(Cursor{})
	if stored.Pods[0].Containers[0].LastTermination.Reason != ReasonOOMKilled || stored.Pods[0].Conditions[0].Status != TruthTrue || stored.Pods[0].Owners[0].Entity.UID != "owner" {
		t.Fatal("mutable snapshot aliases inventory")
	}
	for _, state := range []CoverageState{CoverageStale, CoverageDenied, CoverageCollecting, CoverageUnavailable, CoverageComplete} {
		inventory.Publish(source, "apps", true, []PodObservation{observationFixture("uid", "rv")}, Coverage{State: state})
		got := inventory.Observations(Cursor{})
		if got.Coverage.Inventory.State != state || got.Coverage.Network.State != CoverageDisabled || got.Coverage.Events.State != CoverageUnavailable || !got.Synthetic {
			t.Fatal("coverage not preserved")
		}
	}
	missing := observationFixture("", "rv")
	inventory.Publish(source, "apps", false, []PodObservation{missing}, Coverage{State: CoverageComplete})
	got := inventory.Observations(Cursor{})
	if got.Coverage.Inventory.State != CoveragePartial || got.Coverage.Inventory.Observed != 0 || got.Coverage.Inventory.Total != 1 {
		t.Fatal("missing UID must not become trusted observation")
	}
}

func TestInventoryOrderingCapsAndClockRegression(t *testing.T) {
	clock := &observationClock{now: time.Unix(1000, 0)}
	source := SourceIdentity{SessionID: "a"}
	pods := make([]PodObservation, MaxInventoryPods+1)
	for i := range pods {
		pods[i] = observationFixture(fmt.Sprint(i), "1")
		pods[i].Entity.Name = fmt.Sprintf("pod-%05d", i)
	}
	inventory := NewInventory(clock)
	inventory.Publish(source, "", false, pods, Coverage{State: CoverageComplete})
	first := inventory.Observations(Cursor{})
	rand.New(rand.NewSource(42)).Shuffle(len(pods), func(i, j int) { pods[i], pods[j] = pods[j], pods[i] })
	if inventory.Publish(source, "", false, pods, Coverage{State: CoverageComplete}) {
		t.Fatal("ordering changed revision")
	}
	if first.Coverage.Inventory.State != CoveragePartial || len(first.Pods) != MaxInventoryPods || first.Coverage.Inventory.Total != MaxInventoryPods+1 {
		t.Fatal("cap not visible")
	}
	clock.now = clock.now.Add(-time.Minute)
	inventory.Publish(source, "", false, pods, Coverage{State: CoverageStale})
	if inventory.Observations(Cursor{}).ObservedAt.Before(first.ObservedAt) {
		t.Fatal("local clock moved backwards")
	}
}

func BenchmarkInventoryReplay10000(b *testing.B) {
	clock := &observationClock{now: time.Unix(1000, 0)}
	inventory := NewInventory(clock)
	pods := make([]PodObservation, 10000)
	for i := range pods {
		pods[i] = observationFixture(fmt.Sprint(i), "1")
		pods[i].Entity.Name = fmt.Sprint(i)
	}
	source := SourceIdentity{SessionID: "bench"}
	inventory.Publish(source, "", false, pods, Coverage{State: CoverageComplete})
	cursor := inventory.Observations(Cursor{}).Cursor
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		inventory.Observations(cursor)
	}
}

func TestInventoryCursorGapsAndNamespaces(t *testing.T) {
	clock := &observationClock{now: time.Unix(1000, 0)}
	inventory := NewInventory(clock)
	source := SourceIdentity{SessionID: "test"}
	pods := []PodObservation{observationFixture("one", "1"), observationFixture("two", "1")}
	pods[1].Entity.Namespace = "other"
	inventory.Publish(source, "", false, pods, Coverage{State: CoverageComplete})
	first := inventory.Observations(Cursor{})
	if !first.Reset || len(first.Pods) != 2 {
		t.Fatal("initial baseline or namespaces missing")
	}
	for _, rv := range []string{"2", "3"} {
		pods[0].Entity.ResourceVersion = rv
		inventory.Publish(source, "", false, pods, Coverage{State: CoverageComplete})
	}
	latest := inventory.Observations(first.Cursor)
	if latest.Reset || latest.SkippedInventoryRevisions != 1 || !latest.FullInventory {
		t.Fatal("missed inventory revisions hidden")
	}
	if latest.Pods[0].Entity.EntityKey == latest.Pods[1].Entity.EntityKey {
		t.Fatal("same name merged across namespaces")
	}
	latest.Pods[0].Containers[0].LastTermination.Reason = ReasonOther
	if !reflect.DeepEqual(inventory.Observations(first.Cursor), inventory.Observations(first.Cursor)) {
		t.Fatal("same cursor produced different results")
	}
}

func TestInventoryPrelimitedCoverage(t *testing.T) {
	inventory := NewInventory(&observationClock{now: time.Unix(1000, 0)})
	inventory.Publish(SourceIdentity{SessionID: "test"}, "", false, []PodObservation{observationFixture("uid", "rv")}, Coverage{State: CoverageDenied, Reason: "permission revoked", Total: 10001})
	got := inventory.Observations(Cursor{}).Coverage.Inventory
	if got.State != CoverageDenied || got.Total != 10001 || got.Observed != 1 || got.Reason != "permission revoked; inventory limited to 10000 pods" {
		t.Fatalf("coverage lost: %+v", got)
	}
}

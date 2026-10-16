package insight

import (
	"fmt"
	"runtime"
	"testing"
	"time"
	"weak"
)

func inventoryRetentionFixture(inventory *Inventory, invalid bool) weak.Pointer[Termination] {
	count := MaxInventoryPods + 1
	if invalid {
		count = 2
	}
	pods := make([]PodObservation, count)
	for i := range pods {
		pods[i] = observationFixture(fmt.Sprint(i), "rv")
		pods[i].Entity.Name = fmt.Sprintf("pod-%05d", i)
	}
	tail := &Termination{ExitCode: 123}
	pods[count-1].Containers[0].LastTermination = tail
	if invalid {
		pods[count-1].Entity.UID = ""
	}
	pointer := weak.Make(tail)
	inventory.Publish(SourceIdentity{SessionID: "tail"}, "", false, pods, Coverage{State: CoverageComplete})
	return pointer
}

func TestInventoryDiscardedTailIsCollectable(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			inventory := NewInventory(&observationClock{now: time.Unix(1000, 0)})
			pointer := inventoryRetentionFixture(inventory, invalid)
			runtime.GC()
			if pointer.Value() != nil {
				t.Error("discarded pod still retains its nested termination")
			}
			if cap(inventory.batch.Pods) != len(inventory.batch.Pods) {
				t.Errorf("retained backing array capacity=%d len=%d", cap(inventory.batch.Pods), len(inventory.batch.Pods))
			}
			runtime.KeepAlive(inventory)
		})
	}
}

func TestInventoryBudgetChangeWithoutResourceVersion(t *testing.T) {
	inventory := NewInventory(&observationClock{now: time.Unix(1000, 0)})
	pod := observationFixture("uid", "same")
	source := SourceIdentity{SessionID: "test"}
	inventory.Publish(source, "", false, []PodObservation{pod}, Coverage{State: CoverageComplete})
	previous := inventory.Observations(Cursor{})
	pod.Containers = nil
	pod.Omissions.Containers = 1
	inventory.Publish(source, "", false, []PodObservation{pod}, Coverage{State: CoveragePartial})
	next := inventory.Observations(previous.Cursor)
	if next.Unchanged || len(next.Pods[0].Containers) != 0 || next.Omissions.Containers != 1 {
		t.Fatal("same RV retained evidence outside the new projection budget")
	}
}

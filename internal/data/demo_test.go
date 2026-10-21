package data

import (
	"reflect"
	"testing"
)

const (
	selectedName = "bilibili-live-7f6b8d9c5-mn4tz"
	selected     = "bilibili-raffle/" + selectedName
)

func TestDemoSnapshotMatchesSpec(t *testing.T) {
	s := NewDemo().Snapshot()
	if len(s.Pods) != 13 || s.Cluster.Pods != 13 {
		t.Fatalf("expected 13 pods, got %d", len(s.Pods))
	}
	if s.Pods[1].Name != selectedName || s.Pods[1].Key() != selected || s.Pods[1].Restarts != 2 {
		t.Fatalf("pod 1 mismatch: %+v", s.Pods[1])
	}
	if s.Pods[7].Status != StatusPending || s.Pods[5].Status != StatusCompleted {
		t.Fatal("statuses mismatch")
	}
	if len(s.Namespaces) != 8 || s.Namespaces[1] != (Namespace{"kube-system", 5}) {
		t.Fatalf("namespaces mismatch: %+v", s.Namespaces)
	}
	if len(s.Deployments) != 8 {
		t.Fatalf("deployments mismatch: %d", len(s.Deployments))
	}
	if s.Pods[1].Controller != "ReplicaSet/bilibili-live-7f6b8d9c5" {
		t.Fatalf("controller got %q", s.Pods[1].Controller)
	}
}

func TestDemoMetricsDeterministic(t *testing.T) {
	d := NewDemo()
	a := d.Metrics(40, selected, 36)
	b := d.Metrics(40, selected, 36)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("metrics must be deterministic")
	}
	if len(a.CPU) != 60 || len(a.Mem) != 34 || len(a.RX) != 34 || len(a.TX) != 34 {
		t.Fatal("series windows mismatch")
	}
	for _, v := range a.CPU {
		if v < 0.5 || v > 19.5 {
			t.Fatalf("cpu out of clamp: %f", v)
		}
	}
	if len(a.Heat) != 8 || len(a.Heat[0]) != 36 {
		t.Fatalf("heat grid got %dx%d", len(a.Heat), len(a.Heat[0]))
	}
	want := []string{"bilibili-raffle", "bilibili-live", "lgo-7c5d97584", "psql-6bc7744f9", "odoo-7575bf6d8", "coredns-6799fbcd5", "metrics-server", "traefik-f4564c4f4"}
	if !reflect.DeepEqual(a.HeatLabels, want) {
		t.Fatalf("heat labels got %v", a.HeatLabels)
	}
	if a.PodCPU["xcx/qiandao-1605400800-k8f2m"] != 0 {
		t.Fatal("completed pod must have zero cpu")
	}
}

func TestDemoHeatScrollsLeft(t *testing.T) {
	d := NewDemo()
	a := d.Metrics(40, selected, 36)
	b := d.Metrics(42, selected, 36)
	for row := range a.Heat {
		for col := 0; col < 35; col++ {
			if a.Heat[row][col+1] != b.Heat[row][col] {
				t.Fatalf("row %d col %d did not shift", row, col)
			}
		}
	}
}

func TestDemoLogs(t *testing.T) {
	d := NewDemo()
	lines := d.Logs(selected, 0, 5)
	if len(lines) != 5 {
		t.Fatalf("got %d lines", len(lines))
	}
	first := d.Logs(selected, 0, 100)
	if first[0].Time != "20:14:00" || first[0].Message != "ws connected room=21452505 shard=3" {
		t.Fatalf("first line got %+v", first[0])
	}
	if first[1].Time != "20:14:03" || first[1].Level != LevelDebug {
		t.Fatalf("second line got %+v", first[1])
	}
	later := d.Logs(selected, 8, 100)
	if len(later) != len(first)+1 {
		t.Fatal("logs must advance one line every 8 ticks")
	}
}

func TestDemoManifestAndDescribe(t *testing.T) {
	d := NewDemo()
	m := d.Manifest(selected)
	if m[6] != "    app: bilibili-live" {
		t.Fatalf("label got %q", m[6])
	}
	f := d.Describe(selected)
	if f[2].Value != "demo-node-01/192.168.0.12" {
		t.Fatalf("node got %q", f[2].Value)
	}
	if d.Manifest("missing") != nil || d.Describe("missing") != nil {
		t.Fatal("unknown pod must return nil")
	}
}

func TestShortName(t *testing.T) {
	if ShortName("metrics-server-67c658944b-lm2x") != "metrics-server" || ShortName("solo") != "solo" {
		t.Fatal("short name broken")
	}
}

func TestDemoEventsHaveUniqueIdentity(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range NewDemo().ScopedEvents(EventQuery{Scope: ScopeCluster}).Items {
		if e.UID == "" || seen[e.UID] {
			t.Fatalf("demo event %s %s needs a unique UID, got %q", e.Object, e.Reason, e.UID)
		}
		seen[e.UID] = true
		if full, ok := NewDemo().EventMessage(e.UID); !ok || full.Text != e.Message || full.Truncated {
			t.Fatalf("demo serves the full message of %s", e.UID)
		}
	}
	if _, ok := NewDemo().EventMessage(""); ok {
		t.Fatal("an empty UID is not an event")
	}
}

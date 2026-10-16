package ui

import (
	"strings"
	"testing"

	"github.com/TNK-Studio/lazykube/internal/data"
)

func TestIndexFoldAndMatchRows(t *testing.T) {
	if IndexFold("Heartbeat OK", "heart") != 0 || IndexFold("abc", "") != -1 || IndexFold("abc", "x") != -1 {
		t.Fatal("case-insensitive index broken")
	}
	rows := []LogRow{{Text: "ERROR boom"}, {Text: "ok"}, {Text: "error again"}}
	if got := MatchRows(rows, "Error"); len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Fatalf("matches = %v", got)
	}
	if MatchRows(rows, "") != nil {
		t.Fatal("empty query has no matches")
	}
}

func TestDescribeRowsShowEvidence(t *testing.T) {
	d := Diagnosis{LogContainer: "app", Containers: []data.Container{
		{Name: "migrate", Kind: data.ContainerInit, Phase: data.PhaseTerminated, Current: &data.Termination{Reason: "Completed"}},
		{Name: "app", Kind: data.ContainerApp, Phase: data.PhaseWaiting, Reason: "CrashLoopBackOff", Message: "back-off", Restarts: 4,
			Last: &data.Termination{Reason: "OOMKilled", ExitCode: 137, Finished: "2026-10-16 10:00:00", Age: "3m"}},
		{Name: "side", Kind: data.ContainerApp, Phase: data.PhaseRunning, Started: true},
	}}
	rows := DescribeRows(d, []data.Field{{Key: "Name", Value: "p"}})
	var text []string
	for _, r := range rows {
		text = append(text, r.Label+" "+r.Value)
	}
	joined := strings.Join(text, "\n")
	for _, want := range []string{"ready 0/2", "init migrate", "CrashLoopBackOff · restarts 4", "OOMKilled · exit 137 · 3m ago (2026-10-16 10:00:00)", "P logs anteriores", "Name p"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
	health := map[string]Health{}
	for _, r := range rows {
		if r.Kind == RowContainer {
			health[r.Label] = r.Health
			if r.Selected != (r.Label == "app app") {
				t.Fatalf("selection marker on %q", r.Label)
			}
		}
	}
	if health["init migrate"] != HealthOK || health["app app"] != HealthFailed || health["app side"] != HealthWarn {
		t.Fatalf("health = %v", health)
	}
	if got := DescribeRows(Diagnosis{}, nil); len(got) != 0 {
		t.Fatal("no containers and no fields render nothing")
	}
}

func TestPodStateLabel(t *testing.T) {
	cases := []struct {
		pod  data.Pod
		want string
		ok   bool
	}{
		{data.Pod{Status: data.StatusRunning, Reason: "Running", Ready: 1, Containers: 1}, "", false},
		{data.Pod{Status: data.StatusRunning, Reason: "Running", Ready: 0, Containers: 2}, "ready 0/2", true},
		{data.Pod{Status: data.StatusFailed, Reason: "OOMKilled"}, "OOMKilled", true},
		{data.Pod{Status: data.StatusPending}, "Pending", true},
	}
	for _, c := range cases {
		if got, _, ok := PodStateLabel(c.pod); got != c.want || ok != c.ok {
			t.Errorf("%+v: got %q %v", c.pod, got, ok)
		}
	}
}

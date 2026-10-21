package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/theme"
)

type keyedSource struct {
	pods      []data.Pod
	requested []string
}

func (s *keyedSource) Snapshot() data.Snapshot {
	return data.Snapshot{Pods: s.pods, Namespaces: []data.Namespace{{Name: "apps", Pods: 1}, {Name: "staging", Pods: 1}}}
}

func (s *keyedSource) Metrics(_ int, pod string, _ int) data.Metrics {
	s.requested = append(s.requested, pod)
	return data.Metrics{PodCPU: map[string]int{"apps/web-0": 3, "staging/web-0": 9}}
}

func (s *keyedSource) Logs(string, int, int) []data.LogLine { return nil }
func (s *keyedSource) Events(string) []data.Event           { return nil }
func (s *keyedSource) Manifest(string) []string             { return nil }

func (s *keyedSource) Describe(pod string) []data.Field {
	return []data.Field{{Key: "Key", Value: pod}}
}

func TestSelectionUsesNamespaceQualifiedKey(t *testing.T) {
	src := &keyedSource{pods: []data.Pod{
		{Name: "web-0", Namespace: "apps"},
		{Name: "web-0", Namespace: "staging"},
	}}
	m := New(Options{Source: src, Palette: theme.DefaultPalette(), NoAnim: true, Now: func() time.Time { return time.Unix(0, 0) }})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	m = next.(Model)
	if m.selName != "apps/web-0" {
		t.Fatalf("initial selection = %q", m.selName)
	}
	m = press(t, m, "j")
	if m.selName != "staging/web-0" {
		t.Fatalf("second pod with the same name must be selectable, got %q", m.selName)
	}
	if p := m.selectedPod(); p == nil || p.Namespace != "staging" {
		t.Fatalf("selected pod = %+v", p)
	}
	if got := src.requested[len(src.requested)-1]; got != "staging/web-0" {
		t.Fatalf("source received %q", got)
	}
	if len(m.describe) != 1 || m.describe[0].Value != "staging/web-0" {
		t.Fatalf("describe used the wrong key: %+v", m.describe)
	}
	if ks := m.selectionKs(m.visiblePods()); len(ks) != 1 || ks[1] == 0 {
		t.Fatalf("only the staging row may be highlighted: %v", ks)
	}
}

func TestWaitChangeReturnsAfterClose(t *testing.T) {
	ch := make(chan struct{})
	cmd := waitChange(ch, 0)
	close(ch)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if msg != nil {
			t.Fatalf("closed channel must end the wait, got %T", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitChange still blocked after the channel closed")
	}
}

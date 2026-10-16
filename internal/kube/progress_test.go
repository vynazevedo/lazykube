package kube

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/version"

	"github.com/TNK-Studio/lazykube/internal/data"
)

func TestStartReportsProgress(t *testing.T) {
	var mu sync.Mutex
	var got []data.Progress
	startWith(t, nil, func(s *Source) {
		s.manualPoll = true
		s.opts.Progress = func(p data.Progress) {
			mu.Lock()
			got = append(got, p)
			mu.Unlock()
		}
	}, testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	mu.Lock()
	defer mu.Unlock()
	want := []struct {
		stage data.Stage
		done  bool
	}{
		{data.StageConnect, true},
		{data.StagePermissions, false},
		{data.StagePermissions, true},
		{data.StageSync, false},
		{data.StageSync, true},
	}
	if len(got) != len(want) {
		t.Fatalf("progress = %+v", got)
	}
	for i, w := range want {
		if got[i].Stage != w.stage || got[i].Done != w.done || got[i].Context != "test" {
			t.Fatalf("step %d = %+v, want %+v", i, got[i], w)
		}
	}
	if got[2].Detail != "acesso completo" {
		t.Fatalf("permissions detail = %q", got[2].Detail)
	}
	if got[3].Detail != "pods, deployments (optional), events, namespaces, nodes" || got[4].Detail != "1 pods, 0 deployments" {
		t.Fatalf("sync details = %q / %q", got[3].Detail, got[4].Detail)
	}
}

func TestCapsSummaryListsMissing(t *testing.T) {
	c := caps{events: true, deployments: true}
	if got := c.summary(true, true); got != "sem acesso: nodes, namespaces, metrics" {
		t.Fatalf("summary = %q", got)
	}
	if got := c.summary(false, false); got != "sem acesso: nodes" {
		t.Fatalf("namespaced summary = %q", got)
	}
}

type blockingVersion struct{ release chan struct{} }

func (b blockingVersion) ServerVersion() (*version.Info, error) {
	<-b.release
	return &version.Info{GitVersion: "v1"}, nil
}

func TestServerVersionHonorsCancel(t *testing.T) {
	b := blockingVersion{release: make(chan struct{})}
	defer close(b.release)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := serverVersion(ctx, b)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serverVersion ignored cancellation")
	}
}

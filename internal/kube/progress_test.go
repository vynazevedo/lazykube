package kube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	restfake "k8s.io/client-go/rest/fake"

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
	if got[3].Detail != "pods, deployments, events, namespaces, nodes" || got[4].Detail != "1 pods, 0 deployments" {
		t.Fatalf("sync details = %q / %q", got[3].Detail, got[4].Detail)
	}
}

func TestCapsSummaryListsMissing(t *testing.T) {
	c := caps{events: true}
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

type restVersion struct {
	rest.Interface
}

func (r restVersion) RESTClient() rest.Interface { return r.Interface }

func (r restVersion) ServerVersion() (*version.Info, error) {
	return nil, errors.New("context-free path must not be used")
}

func TestServerVersionPropagatesContext(t *testing.T) {
	started, aborted := make(chan struct{}), make(chan struct{})
	client := &restfake.RESTClient{
		NegotiatedSerializer: scheme.Codecs.WithoutConversion(),
		Client: restfake.CreateHTTPClient(func(req *http.Request) (*http.Response, error) {
			close(started)
			<-req.Context().Done()
			close(aborted)
			return nil, req.Context().Err()
		}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := serverVersion(ctx, restVersion{client})
		done <- err
	}()
	<-started
	cancel()
	select {
	case <-aborted:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery request kept running after cancel")
	}
	if err := <-done; err == nil {
		t.Fatal("cancelled discovery must fail")
	}

	ok := &restfake.RESTClient{
		NegotiatedSerializer: scheme.Codecs.WithoutConversion(),
		Client: restfake.CreateHTTPClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"gitVersion":"v1.31.2"}`))}, nil
		}),
	}
	info, err := serverVersion(context.Background(), restVersion{ok})
	if err != nil || info.GitVersion != "v1.31.2" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
}

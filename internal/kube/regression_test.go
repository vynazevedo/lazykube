package kube

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	restfake "k8s.io/client-go/rest/fake"
	k8stesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/TNK-Studio/lazykube/internal/data"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type kubelet struct {
	mu    sync.Mutex
	calls map[string]int
	reply func(node string, call int) (int, string)
}

func (k *kubelet) count(node string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.calls[node]
}

func (k *kubelet) handle(req *http.Request) (*http.Response, error) {
	parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	node := ""
	if len(parts) > 3 {
		node = parts[3]
	}
	k.mu.Lock()
	k.calls[node]++
	call := k.calls[node]
	k.mu.Unlock()
	code, body := k.reply(node, call)
	if code == 0 {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func networkSource(t *testing.T, clock *testClock, k *kubelet, objs ...runtime.Object) *Source {
	t.Helper()
	k.calls = map[string]int{}
	return startWith(t, nil, func(s *Source) {
		s.manualPoll = true
		s.opts.Now = clock.Now
		s.rest = &restfake.RESTClient{
			NegotiatedSerializer: scheme.Codecs.WithoutConversion(),
			Client:               restfake.CreateHTTPClient(k.handle),
		}
	}, objs...)
}

func summary(ns, name, uid string, rx, tx uint64) string {
	return fmt.Sprintf(`{"pods":[{"podRef":{"name":%q,"namespace":%q,"uid":%q},"network":{"rxBytes":%d,"txBytes":%d}}]}`, name, ns, uid, rx, tx)
}

func status(code int, reason, kind, name string) string {
	return fmt.Sprintf(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":%q,"code":%d,"details":{"kind":%q,"name":%q}}`, reason, code, kind, name)
}

func TestSameNameInDifferentNamespacesStaysDistinct(t *testing.T) {
	mc := metricsfake.NewSimpleClientset()
	mc.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		item := func(ns, cpu string) metricsv1beta1.PodMetrics {
			return metricsv1beta1.PodMetrics{
				ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web-0"},
				Timestamp:  metav1.NewTime(fixedNow),
				Containers: []metricsv1beta1.ContainerMetrics{{Name: "app", Usage: corev1.ResourceList{
					corev1.ResourceCPU:    resourceMust(cpu),
					corev1.ResourceMemory: resourceMust("10Mi"),
				}}},
			}
		}
		return true, &metricsv1beta1.PodMetricsList{Items: []metricsv1beta1.PodMetrics{item("apps", "5m"), item("staging", "40m")}}, nil
	})
	staging := testPod("staging", "web-0", "node-a", corev1.PodRunning)
	staging.UID = "uid-staging-web-0"
	s := startWith(t, mc, func(s *Source) { s.manualPoll = true },
		testNode("node-a"), testPod("apps", "web-0", "node-a", corev1.PodRunning), staging)

	snap := s.Snapshot()
	if len(snap.Pods) != 2 || snap.Pods[0].Key() != "apps/web-0" || snap.Pods[1].Key() != "staging/web-0" {
		t.Fatalf("both pods must survive rebuild: %+v", snap.Pods)
	}
	s.pollMetrics(context.Background())
	a := s.Metrics(0, "apps/web-0", 10)
	b := s.Metrics(0, "staging/web-0", 10)
	if a.CPUNow != 5 || b.CPUNow != 40 || a.Samples != 1 || b.Samples != 1 {
		t.Fatalf("samples mixed between namespaces: apps=%+v staging=%+v", a, b)
	}
	if a.PodCPU["apps/web-0"] != 5 || a.PodCPU["staging/web-0"] != 40 {
		t.Fatalf("pod cpu keyed by namespace/name: %v", a.PodCPU)
	}
	if len(a.HeatLabels) != 2 || a.HeatLabels[0] != "web-0" {
		t.Fatalf("heat labels keep short names: %v", a.HeatLabels)
	}
	if got := s.Describe("staging/web-0"); got[1].Value != "staging" {
		t.Fatalf("describe resolved the wrong pod: %+v", got)
	}
	if !strings.Contains(strings.Join(s.Manifest("staging/web-0"), "\n"), "namespace: staging") {
		t.Fatal("manifest resolved the wrong pod")
	}
	if s.Describe("web-0") != nil {
		t.Fatal("bare names must not resolve")
	}
}

func TestPollNetworkHasDeadline(t *testing.T) {
	clock := &testClock{t: fixedNow}
	k := &kubelet{reply: func(string, int) (int, string) { return 0, "" }}
	s := networkSource(t, clock, k, testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	s.netTimeout = 50 * time.Millisecond
	done := make(chan struct{})
	go func() {
		s.pollNetwork(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pollNetwork blocked on an unresponsive kubelet")
	}
	if got := s.Metrics(0, "apps/web-1", 10).NetReason; got != reasonKubeletDown {
		t.Fatalf("timeout must surface as kubelet down, got %q", got)
	}
}

func TestNetworkFailuresArePerNodeWithBackoff(t *testing.T) {
	clock := &testClock{t: fixedNow}
	var denyA atomic.Bool
	denyA.Store(true)
	k := &kubelet{reply: func(node string, _ int) (int, string) {
		if node == "node-a" && denyA.Load() {
			return http.StatusForbidden, status(http.StatusForbidden, "Forbidden", "nodes", node)
		}
		return http.StatusOK, `{"pods":[]}`
	}}
	s := networkSource(t, clock, k, testNode("node-a"), testNode("node-b"),
		testPod("apps", "a", "node-a", corev1.PodRunning), testPod("apps", "b", "node-b", corev1.PodRunning))

	s.Metrics(0, "apps/a", 10)
	s.pollNetwork(context.Background())
	if got := s.Metrics(0, "apps/a", 10).NetReason; got != reasonProxyDeny {
		t.Fatalf("node-a reason = %q", got)
	}
	s.Metrics(0, "apps/b", 10)
	s.pollNetwork(context.Background())
	if got := s.Metrics(0, "apps/b", 10).NetReason; got != reasonNoNetwork {
		t.Fatalf("denial on node-a must not affect node-b, got %q", got)
	}

	s.Metrics(0, "apps/a", 10)
	s.pollNetwork(context.Background())
	if k.count("node-a") != 1 {
		t.Fatalf("retry before backoff elapsed: %d calls", k.count("node-a"))
	}
	clock.Add(netBackoffBase + time.Second)
	denyA.Store(false)
	s.pollNetwork(context.Background())
	if k.count("node-a") != 2 {
		t.Fatalf("expected re-evaluation after backoff, got %d calls", k.count("node-a"))
	}
	if got := s.Metrics(0, "apps/a", 10).NetReason; got != reasonNoNetwork {
		t.Fatalf("recovered node must clear the denial, got %q", got)
	}
}

func TestNetworkBackoffGrowsAndCaps(t *testing.T) {
	if netBackoff(netBackoffBase, 1) != netBackoffBase || netBackoff(netBackoffBase, 2) != 2*netBackoffBase {
		t.Fatal("backoff must double per failure")
	}
	if netBackoff(netBackoffBase, 50) != netBackoffMax {
		t.Fatal("backoff must be capped")
	}
}

func TestRemovedNodeIsNotTreatedAsMissingEndpoint(t *testing.T) {
	notFound := func(string, int) (int, string) { return http.StatusNotFound, "404 page not found" }
	clock := &testClock{t: fixedNow}
	k := &kubelet{reply: notFound}
	s := networkSource(t, clock, k, testPod("apps", "web-1", "node-gone", corev1.PodRunning))
	s.Metrics(0, "apps/web-1", 10)
	s.pollNetwork(context.Background())
	s.mu.RLock()
	_, tracked := s.usage.net["node-gone"]
	s.mu.RUnlock()
	if tracked {
		t.Fatal("a removed node must not be marked as lacking stats/summary")
	}
	s.pollNetwork(context.Background())
	if k.count("node-gone") != 2 {
		t.Fatal("a removed node must not be backed off")
	}

	k2 := &kubelet{reply: notFound}
	s2 := networkSource(t, clock, k2, testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	s2.Metrics(0, "apps/web-1", 10)
	s2.pollNetwork(context.Background())
	if got := s2.Metrics(0, "apps/web-1", 10).NetReason; got != reasonNoStats {
		t.Fatalf("missing endpoint reason = %q", got)
	}
}

func TestNodeGoneFromStatusDetails(t *testing.T) {
	s := startWith(t, nil, func(s *Source) { s.manualPoll = true }, testNode("node-a"))
	gone := apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, "node-a")
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.nodeGoneLocked("node-a", gone) {
		t.Fatal("NotFound with nodes details must mean the node is gone")
	}
	if s.nodeGoneLocked("node-a", apierrors.NewNotFound(schema.GroupResource{}, "")) {
		t.Fatal("generic 404 on a known node is a missing endpoint")
	}
}

func TestStaleNetworkIsReportedWithHistory(t *testing.T) {
	clock := &testClock{t: fixedNow}
	var fail atomic.Bool
	var rx atomic.Uint64
	k := &kubelet{reply: func(string, int) (int, string) {
		if fail.Load() {
			return http.StatusServiceUnavailable, status(http.StatusServiceUnavailable, "ServiceUnavailable", "", "")
		}
		return http.StatusOK, summary("apps", "web-1", "uid-web-1", rx.Add(10240), 2048)
	}}
	s := networkSource(t, clock, k, testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	s.Metrics(0, "apps/web-1", 10)
	s.pollNetwork(context.Background())
	clock.Add(metricsEvery)
	s.pollNetwork(context.Background())
	m := s.Metrics(0, "apps/web-1", 10)
	if len(m.RX) != 1 || m.NetStale || m.NetReason != "" {
		t.Fatalf("fresh sample expected: %+v", m)
	}

	fail.Store(true)
	clock.Add(metricsEvery)
	s.pollNetwork(context.Background())
	m = s.Metrics(0, "apps/web-1", 10)
	if len(m.RX) != 1 || !m.NetStale || m.NetReason != reasonKubeletDown {
		t.Fatalf("kubelet failure must mark history as stale: stale=%v reason=%q rx=%v", m.NetStale, m.NetReason, m.RX)
	}

	fail.Store(false)
	clock.Add(time.Minute)
	s.mu.Lock()
	*s.usage.netState("node-a") = netState{okAt: clock.Now()}
	s.mu.Unlock()
	m = s.Metrics(0, "apps/web-1", 10)
	if !m.NetStale || m.NetReason != reasonNetStale {
		t.Fatalf("old samples must be flagged stale: stale=%v reason=%q", m.NetStale, m.NetReason)
	}
}

func TestRecreatedPodStartsFreshSeries(t *testing.T) {
	clock := &testClock{t: fixedNow}
	var uid atomic.Value
	uid.Store("uid-web-1")
	var rx atomic.Uint64
	k := &kubelet{reply: func(string, int) (int, string) {
		return http.StatusOK, summary("apps", "web-1", uid.Load().(string), rx.Add(10240), 4096)
	}}
	var mu sync.Mutex
	at := fixedNow
	mc := metricsfake.NewSimpleClientset()
	mc.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		return true, podMetricsAt("30m", at), nil
	})
	s := startWith(t, mc, func(s *Source) {
		s.manualPoll = true
		s.opts.Now = clock.Now
		s.rest = &restfake.RESTClient{NegotiatedSerializer: scheme.Codecs.WithoutConversion(), Client: restfake.CreateHTTPClient(k.handle)}
	}, testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	k.calls = map[string]int{}
	ctx := context.Background()
	s.Metrics(0, "apps/web-1", 10)
	s.pollMetrics(ctx)
	s.pollNetwork(ctx)
	clock.Add(metricsEvery)
	s.pollNetwork(ctx)
	if m := s.Metrics(0, "apps/web-1", 10); m.Samples != 1 || len(m.RX) != 1 {
		t.Fatalf("expected history before recreation: %+v", m)
	}

	pods := s.client.CoreV1().Pods("apps")
	if err := pods.Delete(ctx, "web-1", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	fresh := testPod("apps", "web-1", "node-a", corev1.PodRunning)
	fresh.UID = types.UID("uid-web-1-b")
	fresh.CreationTimestamp = metav1.NewTime(fixedNow.Add(time.Minute))
	if _, err := pods.Create(ctx, fresh, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { p := s.pod("apps/web-1"); return p != nil && p.UID == fresh.UID })

	if m := s.Metrics(0, "apps/web-1", 10); m.Samples != 0 || len(m.RX) != 0 || m.PodCPU["apps/web-1"] != 0 {
		t.Fatalf("recreated pod inherited history: %+v", m)
	}
	s.pollMetrics(ctx)
	if m := s.Metrics(0, "apps/web-1", 10); m.Samples != 0 {
		t.Fatal("samples older than the new pod must be ignored")
	}
	s.pollNetwork(ctx)
	if m := s.Metrics(0, "apps/web-1", 10); len(m.RX) != 0 {
		t.Fatal("stats for the previous incarnation must be ignored")
	}

	uid.Store("uid-web-1-b")
	mu.Lock()
	at = fixedNow.Add(2 * time.Minute)
	mu.Unlock()
	s.pollMetrics(ctx)
	clock.Add(metricsEvery)
	s.pollNetwork(ctx)
	clock.Add(metricsEvery)
	s.pollNetwork(ctx)
	m := s.Metrics(0, "apps/web-1", 10)
	if m.Samples != 1 || len(m.RX) != 1 {
		t.Fatalf("new incarnation must build its own series: %+v", m)
	}
	if m.RXNow != 2 {
		t.Fatalf("network baseline must restart with the new pod, got %.2f kB/s", m.RXNow)
	}
}

func logSource(t *testing.T) (*Source, *logStream) {
	t.Helper()
	s := startWith(t, nil, func(s *Source) { s.manualPoll = true })
	return s, &logStream{}
}

func stamp(sec int, nanos int) string {
	return fixedNow.Add(time.Duration(sec)*time.Second + time.Duration(nanos)).Format(time.RFC3339Nano)
}

func messages(st *logStream) []string {
	out := make([]string, len(st.lines))
	for i, l := range st.lines {
		out[i] = l.Message
	}
	return out
}

func TestLogsKeepDistinctLinesWithSameTimestamp(t *testing.T) {
	s, st := logSource(t)
	ts := stamp(0, 5)
	in := ts + " first\n" + ts + " second\n" + ts + " second\n"
	if err := s.consumeLogs(context.Background(), st, strings.NewReader(in), time.Time{}, 0); err != nil {
		t.Fatal(err)
	}
	if got := messages(st); len(got) != 3 {
		t.Fatalf("lines with equal timestamps were dropped: %v", got)
	}
	if st.atLast != 3 {
		t.Fatalf("atLast = %d", st.atLast)
	}
}

func TestLogsDedupeOnlyReconnectOverlap(t *testing.T) {
	s, st := logSource(t)
	first := stamp(1, 0) + " a\n" + stamp(1, 500) + " b\n" + stamp(1, 500) + " c\n"
	ctx := context.Background()
	if err := s.consumeLogs(ctx, st, strings.NewReader(first), time.Time{}, 0); err != nil {
		t.Fatal(err)
	}
	since, skip := st.lastTS, st.atLast
	replay := stamp(1, 0) + " a\n" + stamp(1, 500) + " b\n" + stamp(1, 500) + " c\n" +
		stamp(1, 500) + " d\n" + stamp(2, 0) + " e\n" + stamp(2, 0) + " e\n"
	if err := s.consumeLogs(ctx, st, strings.NewReader(replay), since, skip); err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b", "c", "d", "e", "e"}
	if got := messages(st); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestLogsSurviveOversizedLine(t *testing.T) {
	s, st := logSource(t)
	huge := stamp(0, 0) + " " + strings.Repeat("x", 3<<20) + "\n" + stamp(1, 0) + " after\n"
	if err := s.consumeLogs(context.Background(), st, strings.NewReader(huge), time.Time{}, 0); err != nil {
		t.Fatalf("oversized line must not abort the stream: %v", err)
	}
	got := messages(st)
	if len(got) != 2 || got[1] != "after" {
		t.Fatalf("stream must continue after the long line: %d lines", len(got))
	}
	if !strings.HasSuffix(got[0], truncatedMark) || len(got[0]) > maxLogMessage+len(truncatedMark) {
		t.Fatalf("long line must be truncated and marked, len=%d", len(got[0]))
	}
	if st.lines[0].Time != "12:00:00" {
		t.Fatalf("timestamp of the truncated line lost: %q", st.lines[0].Time)
	}
}

func TestReadLogLineBoundaries(t *testing.T) {
	br := bufioReader("abc\r\n" + strings.Repeat("y", 100) + "\nlast")
	line, cut, err := readLogLine(br, 10)
	if string(line) != "abc" || cut || err != nil {
		t.Fatalf("line=%q cut=%v err=%v", line, cut, err)
	}
	line, cut, err = readLogLine(br, 10)
	if string(line) != strings.Repeat("y", 10) || !cut || err != nil {
		t.Fatalf("line=%q cut=%v err=%v", line, cut, err)
	}
	line, cut, err = readLogLine(br, 10)
	if string(line) != "last" || cut || err != io.EOF {
		t.Fatalf("line=%q cut=%v err=%v", line, cut, err)
	}
}

func TestLogsEnforceByteBudget(t *testing.T) {
	s, st := logSource(t)
	var b strings.Builder
	body := strings.Repeat("z", maxLogMessage)
	for i := 0; i < 2*logTail; i++ {
		fmt.Fprintf(&b, "%s %s\n", stamp(i, 0), body)
	}
	if err := s.consumeLogs(context.Background(), st, strings.NewReader(b.String()), time.Time{}, 0); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, l := range st.lines {
		total += len(l.Message)
	}
	if total > logBudget || st.bytes != total {
		t.Fatalf("byte budget exceeded: total=%d tracked=%d budget=%d", total, st.bytes, logBudget)
	}
	if len(st.lines) == 0 || !strings.HasPrefix(st.lines[len(st.lines)-1].Time, "12:16") {
		t.Fatalf("newest lines must be kept, last=%+v", st.lines[len(st.lines)-1].Time)
	}
}

func TestClampMessageKeepsRunes(t *testing.T) {
	msg := strings.Repeat("é", maxLogMessage)
	got := clampMessage(msg, false)
	if !strings.HasSuffix(got, truncatedMark) || len(got) > maxLogMessage+len(truncatedMark) {
		t.Fatalf("unexpected clamp len=%d", len(got))
	}
	if body := strings.TrimSuffix(got, truncatedMark); strings.ToValidUTF8(body, "?") != body {
		t.Fatal("clamp split a rune")
	}
	if clampMessage("ok", false) != "ok" {
		t.Fatal("short messages must be untouched")
	}
}

func TestCloseClosesChanges(t *testing.T) {
	s := startTest(t, nil, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	ch := s.Changes()
	s.Close()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				s.Metrics(0, "missing", 10)
				s.notify()
				return
			}
		case <-deadline:
			t.Fatal("changes channel not closed after Close")
		}
	}
}

var _ data.Live = (*Source)(nil)

func resourceMust(v string) resource.Quantity {
	return resource.MustParse(v)
}

func bufioReader(s string) *bufio.Reader {
	return bufio.NewReaderSize(strings.NewReader(s), 16)
}

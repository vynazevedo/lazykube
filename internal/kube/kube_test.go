package kube

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/TNK-Studio/lazykube/internal/data"
)

var fixedNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func testPod(ns, name, node string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         ns,
			Name:              name,
			UID:               types.UID("uid-" + name),
			CreationTimestamp: metav1.NewTime(fixedNow.Add(-2 * time.Hour)),
			OwnerReferences:   []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "rs-" + name, Controller: ptr(true)}},
		},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{
				Name:  "app",
				Image: "registry.local/app:1",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("128Mi")},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: phase, PodIP: "10.0.0.1", QOSClass: corev1.PodQOSBurstable},
	}
}

func testNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
			Addresses:  []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.168.0.10"}},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			NodeInfo:   corev1.NodeSystemInfo{Architecture: "arm64"},
		},
	}
}

func startTest(t *testing.T, mc *metricsfake.Clientset, objs ...runtime.Object) *Source {
	t.Helper()
	return startWith(t, mc, nil, objs...)
}

func startWith(t *testing.T, mc *metricsfake.Clientset, setup func(*Source), objs ...runtime.Object) *Source {
	t.Helper()
	client := fake.NewClientset(objs...)
	var metrics *metricsfake.Clientset
	if mc != nil {
		metrics = mc
	}
	s := newSource(client, nil, Options{Context: "test", Now: func() time.Time { return fixedNow }, Location: time.UTC, SyncTimeout: 5 * time.Second})
	if metrics != nil {
		s.metrics = metrics
	}
	s.rest = nil
	if setup != nil {
		setup(s)
	}
	if err := s.start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestSnapshotFromInformers(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "apps", CreationTimestamp: metav1.NewTime(fixedNow.Add(-72 * time.Hour))}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "web"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr(int32(2))},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
	s := startTest(t, nil, ns, dep, testNode("node-a"),
		testPod("apps", "web-1", "node-a", corev1.PodRunning),
		testPod("apps", "web-2", "node-a", corev1.PodPending))

	snap := s.Snapshot()
	if snap.Cluster.Context != "test" || snap.Cluster.Pods != 2 || snap.Cluster.Nodes != 1 || snap.Cluster.Namespaces != 1 {
		t.Fatalf("unexpected cluster: %+v", snap.Cluster)
	}
	if snap.Cluster.Uptime != "3d" {
		t.Fatalf("uptime = %q", snap.Cluster.Uptime)
	}
	if len(snap.Pods) != 2 || snap.Pods[0].Name != "web-1" || snap.Pods[0].Status != data.StatusRunning {
		t.Fatalf("unexpected pods: %+v", snap.Pods)
	}
	p := snap.Pods[0]
	if p.CPURequest != 10 || p.CPULimit != 50 || p.MemRequest != 64 || p.MemLimit != 128 || p.Age != "120m" || p.Controller != "ReplicaSet/rs-web-1" {
		t.Fatalf("unexpected pod conversion: %+v", p)
	}
	if snap.Pods[1].Status != data.StatusPending {
		t.Fatalf("pending pod status = %v", snap.Pods[1].Status)
	}
	if len(snap.Deployments) != 1 || snap.Deployments[0] != (data.Deployment{Namespace: "apps", Name: "web", Ready: 1, Desired: 2}) {
		t.Fatalf("unexpected deployments: %+v", snap.Deployments)
	}
	if snap.Namespaces[0] != (data.Namespace{Name: "apps", Pods: 2}) {
		t.Fatalf("unexpected namespaces: %+v", snap.Namespaces)
	}
	n := snap.Node
	if n.Name != "node-a" || n.Address != "192.168.0.10" || !n.Ready || n.PodCapacity != 110 || n.Pods != 2 || n.Arch != "arm64" || n.MemTotal != "4.0Gi" {
		t.Fatalf("unexpected node: %+v", n)
	}
}

func TestChangesFireOnPodUpdates(t *testing.T) {
	s := startTest(t, nil, testPod("apps", "web-1", "", corev1.PodRunning))
	drain := func() {
		for {
			select {
			case <-s.Changes():
			default:
				return
			}
		}
	}
	drain()
	_, err := s.client.CoreV1().Pods("apps").Create(context.Background(), testPod("apps", "web-2", "", corev1.PodRunning), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Changes():
	case <-time.After(5 * time.Second):
		t.Fatal("no change notification")
	}
	waitFor(t, func() bool { return len(s.Snapshot().Pods) == 2 })
}

func TestMetricsFromMetricsServer(t *testing.T) {
	mc := metricsfake.NewSimpleClientset()
	mc.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &metricsv1beta1.PodMetricsList{Items: []metricsv1beta1.PodMetrics{{
			ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "web-1"},
			Containers: []metricsv1beta1.ContainerMetrics{{Name: "app", Usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("12m"),
				corev1.ResourceMemory: resource.MustParse("90Mi"),
			}}},
		}}}, nil
	})
	mc.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &metricsv1beta1.NodeMetricsList{Items: []metricsv1beta1.NodeMetrics{{
			ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			Usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		}}}, nil
	})
	s := startTest(t, mc, testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	waitFor(t, func() bool { return s.Metrics(0, "apps/web-1", 10).Available })

	m := s.Metrics(0, "apps/web-1", 10)
	if m.CPUNow != 12 || m.MemNow != 90 || m.PodCPU["apps/web-1"] != 12 || m.NodeCPU != 25 || m.MemMax != 128 {
		t.Fatalf("unexpected metrics: %+v", m)
	}
	if len(m.HeatLabels) != 1 || len(m.Heat[0]) != 10 || m.Heat[0][9] != 1 {
		t.Fatalf("unexpected heat: %v %v", m.HeatLabels, m.Heat)
	}
	if len(m.RX) != 0 {
		t.Fatal("network must be n/a without kubelet stats")
	}
	if n := s.Snapshot().Node; n.MemUsed != "1.0Gi" || n.MemFraction != 0.25 {
		t.Fatalf("unexpected node memory: %+v", n)
	}
}

func TestMetricsUnavailableWithoutMetricsServer(t *testing.T) {
	s := startTest(t, nil, testPod("apps", "web-1", "", corev1.PodRunning))
	s.pollMetrics(context.Background())
	m := s.Metrics(0, "apps/web-1", 10)
	if m.Available || len(m.CPU) != 0 || len(m.Heat) != 0 || m.Reason != reasonNoClient {
		t.Fatalf("expected stale metrics, got %+v", m)
	}
}

func podMetricsAt(cpu string, at time.Time) *metricsv1beta1.PodMetricsList {
	return &metricsv1beta1.PodMetricsList{Items: []metricsv1beta1.PodMetrics{{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "web-1"},
		Timestamp:  metav1.NewTime(at),
		Containers: []metricsv1beta1.ContainerMetrics{{Name: "app", Usage: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpu),
			corev1.ResourceMemory: resource.MustParse("90Mi"),
		}}},
	}}}
}

func TestMetricsSkipsRepeatedTimestamps(t *testing.T) {
	var mu sync.Mutex
	list := podMetricsAt("10m", fixedNow)
	mc := metricsfake.NewSimpleClientset()
	mc.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		return true, list.DeepCopy(), nil
	})
	s := startTest(t, mc, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	ctx := context.Background()
	s.pollMetrics(ctx)
	s.pollMetrics(ctx)
	if m := s.Metrics(0, "apps/web-1", 10); m.Samples != 1 || m.Window != cpuWindow {
		t.Fatalf("repeated timestamp must not add samples: %+v", m)
	}
	mu.Lock()
	list = podMetricsAt("20m", fixedNow.Add(15*time.Second))
	mu.Unlock()
	s.pollMetrics(ctx)
	m := s.Metrics(0, "apps/web-1", 10)
	if m.Samples != 2 || m.CPU[0] != 10 || m.CPU[1] != 20 {
		t.Fatalf("new timestamp must add a sample: %+v", m)
	}
}

func TestMetricsReasons(t *testing.T) {
	gr := schema.GroupResource{Group: "metrics.k8s.io", Resource: "pods"}
	cases := []struct {
		err  error
		want string
	}{
		{apierrors.NewForbidden(gr, "", errors.New("denied")), reasonMetricsDeny},
		{apierrors.NewNotFound(gr, ""), reasonNotInstalled},
		{apierrors.NewServiceUnavailable("down"), reasonDown},
	}
	for _, c := range cases {
		mc := metricsfake.NewSimpleClientset()
		mc.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, c.err
		})
		s := startTest(t, mc, testPod("apps", "web-1", "node-a", corev1.PodRunning))
		s.pollMetrics(context.Background())
		if m := s.Metrics(0, "apps/web-1", 10); m.Available || m.Reason != c.want {
			t.Errorf("%v: got reason %q want %q", c.err, m.Reason, c.want)
		}
	}
}

func TestNetworkReasons(t *testing.T) {
	s := startWith(t, nil, func(s *Source) { s.manualPoll = true }, testNode("node-a"), testPod("apps", "web-1", "node-a", corev1.PodRunning))
	if got := s.Metrics(0, "apps/web-1", 10).NetReason; got != reasonCollecting {
		t.Fatalf("initial reason = %q", got)
	}
	s.mu.Lock()
	s.usage.netState("node-a").okAt = fixedNow
	s.mu.Unlock()
	if got := s.Metrics(0, "apps/web-1", 10).NetReason; got != reasonNoNetwork {
		t.Fatalf("polled node without pod stats: %q", got)
	}
	s.mu.Lock()
	*s.usage.netState("node-a") = netState{reason: reasonProxyDeny, denied: true}
	s.mu.Unlock()
	if got := s.Metrics(0, "apps/web-1", 10).NetReason; got != reasonProxyDeny {
		t.Fatalf("denied reason = %q", got)
	}
	s.caps.nodes = false
	if got := s.Metrics(0, "apps/web-1", 10).NetReason; got != reasonNodesDeny {
		t.Fatalf("no nodes reason = %q", got)
	}
}

func TestLogsStreamAndDescribe(t *testing.T) {
	s := startTest(t, nil, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	waitFor(t, func() bool { return len(s.Logs("apps/web-1", 0, 10)) > 0 })
	logs := s.Logs("apps/web-1", 0, 10)
	if logs[0].Message == "" {
		t.Fatalf("unexpected logs: %+v", logs)
	}
	if got := s.Describe("apps/web-1"); got[0].Value != "web-1" || got[2].Value != "node-a" {
		t.Fatalf("unexpected describe: %+v", got)
	}
	manifest := strings.Join(s.Manifest("apps/web-1"), "\n")
	if !strings.Contains(manifest, "kind: Pod") || !strings.Contains(manifest, "name: web-1") {
		t.Fatalf("unexpected manifest:\n%s", manifest)
	}
	if s.Logs("missing", 0, 10) != nil || s.Manifest("missing") != nil {
		t.Fatal("unknown pods must return nil")
	}
}

func TestPodStatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*corev1.Pod)
		want   data.PodStatus
	}{
		{"running", func(*corev1.Pod) {}, data.StatusRunning},
		{"succeeded", func(p *corev1.Pod) { p.Status.Phase = corev1.PodSucceeded }, data.StatusCompleted},
		{"crashloop", func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}}
		}, data.StatusFailed},
		{"creating", func(p *corev1.Pod) {
			p.Status.Phase = corev1.PodPending
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}}}
		}, data.StatusContainerCreating},
		{"pending", func(p *corev1.Pod) { p.Status.Phase = corev1.PodPending }, data.StatusPending},
	}
	for _, c := range cases {
		p := testPod("ns", "p", "", corev1.PodRunning)
		c.mutate(p)
		if got := podStatus(p); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestParseLogLine(t *testing.T) {
	line, ts := parseLogLine("2026-10-16T03:04:05.123456789Z \x1b[31mERROR\x1b[0m boom\tnow", time.UTC)
	if ts.IsZero() || line.Time != "03:04:05" || line.Level != data.LevelError || line.Message != "ERROR boom    now" {
		t.Fatalf("unexpected line: %+v", line)
	}
	line, ts = parseLogLine("no timestamp WARN here", time.UTC)
	if !ts.IsZero() || line.Level != data.LevelWarn || line.Time != "--:--:--" {
		t.Fatalf("unexpected line: %+v", line)
	}
}

func TestSanitizeStripsControlSequences(t *testing.T) {
	in := "a\x1b]0;title\x07b\x1b[2Jc\rd\x00e"
	if got := sanitize(in); strings.ContainsAny(got, "\x1b\x07\r\x00") {
		t.Fatalf("control characters leaked: %q", got)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{512: "512B", 1536: "1.5Ki", 1503238553: "1.4Gi"}
	for in, want := range cases {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%d) = %q want %q", in, got, want)
		}
	}
}

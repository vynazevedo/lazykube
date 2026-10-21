package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/vynazevedo/lazykube/internal/app"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/theme"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

type bugbashMetricsFixture struct {
	source    *Source
	pods      *metricsv1beta1.PodMetricsList
	nodeUsage corev1.ResourceList
	queryErr  error
}

func bugbashMetrics(t *testing.T) *bugbashMetricsFixture {
	t.Helper()
	f := &bugbashMetricsFixture{pods: podMetricsAt("25m", fixedNow)}
	client := metricsfake.NewSimpleClientset()
	client.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) { return true, f.pods, f.queryErr })
	client.PrependReactor("list", "nodes", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, &metricsv1beta1.NodeMetricsList{Items: []metricsv1beta1.NodeMetrics{{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}, Usage: f.nodeUsage}}}, nil
	})
	f.source = newSource(fake.NewClientset(), client, Options{Now: func() time.Time { return fixedNow }})
	t.Cleanup(f.source.cancel)
	p := testPod("apps", "web-1", "node-a", corev1.PodRunning)
	p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}
	f.source.byKey["apps/web-1"] = p
	f.source.nodes["node-a"] = testNode("node-a")
	f.source.nodeList = []string{"node-a"}
	f.source.caps.nodes = true
	return f
}
func (f *bugbashMetricsFixture) poll() data.Metrics {
	f.source.pollMetrics(context.Background())
	return f.source.Metrics(0, "apps/web-1", 0)
}

type bugbashProjectedSource struct {
	*data.Demo
	snapshot data.Snapshot
	metrics  data.Metrics
}

func (s bugbashProjectedSource) Snapshot() data.Snapshot               { return s.snapshot }
func (s bugbashProjectedSource) Metrics(int, string, int) data.Metrics { return s.metrics }
func (f *bugbashMetricsFixture) render(width int, screen string) string {
	m := f.source.Metrics(0, "apps/web-1", 0)
	mem, known := f.source.usage.nodeMem["node-a"]
	snap := data.Snapshot{Synthetic: true, Cluster: data.Cluster{Context: "demo", Version: "demo", Nodes: 1, Pods: 1}, Pods: []data.Pod{toPod(f.source.byKey["apps/web-1"], fixedNow)}, Node: toNode(f.source.nodes["node-a"], 1, mem, known)}
	snap.Node.Coverage = data.Coverage{State: data.CoverageComplete}
	source := bugbashProjectedSource{Demo: data.NewDemo(), snapshot: snap, metrics: m}
	model := app.New(app.Options{Source: source, Palette: theme.DefaultPalette(), NoAnim: true, Now: func() time.Time { return fixedNow }})
	next, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: 48})
	model = next.(app.Model)
	if screen == "detail" {
		next, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		model = next.(app.Model)
	} else if screen != "2" {
		next, _ = model.Update(tea.KeyPressMsg{Code: []rune(screen)[0], Text: screen})
		model = next.(app.Model)
	}
	return model.Render().Plain()
}
func TestBugbashMemorySurvivesMissingCPU(t *testing.T) {
	f := bugbashMetrics(t)
	for i, mem := range []int{100, 110} {
		f.pods = podMetricsAt("0", fixedNow.Add(time.Duration(i)*time.Second))
		usage := f.pods.Items[0].Containers[0].Usage
		delete(usage, corev1.ResourceCPU)
		usage[corev1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%dMi", mem))
		m := f.poll()
		if m.CPUKnown || len(m.CPU) != 0 || m.MemNow != mem || len(m.Mem) != i+1 {
			t.Fatalf("CPU absence discarded memory: %+v", m)
		}
		for _, screen := range []string{"2", "detail"} {
			for _, width := range []int{130, 160} {
				out := f.render(width, screen)
				if !strings.Contains(out, fmt.Sprintf("%dMi", mem)) {
					t.Fatal("observed memory absent from Top", out)
				}
			}
		}
	}
	f.pods.Items[0].Containers[0].Usage[corev1.ResourceCPU] = resource.MustParse("0")
	m := f.poll()
	if !m.CPUKnown || len(m.CPU) != 1 || len(m.Mem) != 2 {
		t.Fatal("independent timestamps not preserved", m)
	}
}
func TestBugbashPodCPUListsFollowKnowledge(t *testing.T) {
	for _, state := range []string{"initial", "zero", "error", "omitted", "recovered", "missing-cpu"} {
		t.Run(state, func(t *testing.T) {
			f := bugbashMetrics(t)
			known := state == "zero" || state == "recovered"
			if state != "initial" {
				f.poll()
				switch state {
				case "error":
					f.queryErr = errors.New("synthetic failure")
				case "omitted":
					f.pods = &metricsv1beta1.PodMetricsList{}
				case "missing-cpu":
					delete(f.pods.Items[0].Containers[0].Usage, corev1.ResourceCPU)
				case "recovered":
					f.queryErr = errors.New("synthetic failure")
					f.poll()
					f.queryErr = nil
					f.pods = podMetricsAt("0", fixedNow.Add(time.Second))
				case "zero":
					f.pods = podMetricsAt("0", fixedNow.Add(time.Second))
				}
				f.poll()
			}
			metrics := f.source.Metrics(0, "apps/web-1", 0)
			_, present := metrics.PodCPU["apps/web-1"]
			if present != known {
				t.Error("pod CPU knowledge mismatch", state, metrics.PodCPU)
			}
			for _, screen := range []string{"2", "3", "4"} {
				for _, width := range []int{130, 160} {
					out := f.render(width, screen)
					row := ""
					for _, line := range strings.Split(out, "\n") {
						if strings.Contains(line, "web-1") && strings.Contains(line, "apps") {
							row = line
							break
						}
					}
					if row == "" {
						t.Fatal("pod list row missing", out)
					}
					if known {
						if !strings.Contains(row, "░░░░░░░") {
							t.Fatal("observed zero missing", row)
						}
					} else if !strings.Contains(row, "n/a") || strings.ContainsAny(row, "█░") {
						t.Fatal("unknown CPU rendered as a value", row)
					}
				}
			}
		})
	}
}
func TestBugbashNodeMetricsRequireCPUKey(t *testing.T) {
	for _, state := range []string{"nil", "memory", "zero"} {
		t.Run(state, func(t *testing.T) {
			f := bugbashMetrics(t)
			if state != "nil" {
				f.nodeUsage = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("100Mi")}
			}
			if state == "zero" {
				f.nodeUsage[corev1.ResourceCPU] = resource.MustParse("0")
			}
			m := f.poll()
			known := state == "zero"
			if m.NodeCPUKnown != known || m.NodeCPU != 0 {
				t.Fatal("missing key became known zero", m)
			}
			if state == "memory" && f.source.usage.nodeMem["node-a"] != 100<<20 {
				t.Fatal("memory discarded")
			}
			for _, screen := range []string{"2", "4"} {
				out := f.render(160, screen)
				if known {
					if !strings.Contains(out, "cpu 0%") && !strings.Contains(out, "CPU: 0%") {
						t.Fatal(out)
					}
				} else if !strings.Contains(out, "cpu indisp") && !strings.Contains(out, "cpu n/a") && !strings.Contains(out, "CPU: indisponível") {
					t.Fatal(out)
				}
			}
		})
	}
}

func TestBugbashResourceSamplesAdvanceIndependently(t *testing.T) {
	f := bugbashMetrics(t)
	f.poll()
	f.pods = podMetricsAt("0", fixedNow.Add(time.Second))
	delete(f.pods.Items[0].Containers[0].Usage, corev1.ResourceCPU)
	m := f.poll()
	if m.CPUKnown || !m.MemKnown || len(m.CPU) != 1 || len(m.Mem) != 2 {
		t.Fatal("CPU gap changed history or memory validity", m)
	}
	f.pods.Items[0].Containers[0].Usage = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("0")}
	m = f.poll()
	if !m.CPUKnown || m.MemKnown || len(m.CPU) != 2 || len(m.Mem) != 2 {
		t.Fatal("memory gap changed history or CPU validity", m)
	}
	for _, screen := range []string{"2", "detail"} {
		out := f.render(160, screen)
		if !strings.Contains(out, "Memory n/a") && !strings.Contains(out, "Mem n/a") {
			t.Fatal("unknown memory shown as current", out)
		}
	}
	f.pods = podMetricsAt("0", fixedNow.Add(2*time.Second))
	f.pods.Items[0].Containers[0].Usage[corev1.ResourceMemory] = resource.MustParse("0")
	m = f.poll()
	if !m.MemKnown || m.MemNow != 0 || len(m.Mem) != 3 {
		t.Fatal("observed memory zero lost", m)
	}
}

func TestBugbashPartialContainerResourceKeepsOtherResource(t *testing.T) {
	f := bugbashMetrics(t)
	f.pods.Items[0].Containers = append(f.pods.Items[0].Containers, metricsv1beta1.ContainerMetrics{Name: "sidecar", Usage: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("10Mi")}})
	m := f.poll()
	if m.CPUKnown || len(m.CPU) != 0 || !m.MemKnown || m.MemNow != 100 {
		t.Fatal("partial CPU invalidated complete memory", m)
	}
	f.pods.Items[0].Containers[1].Usage[corev1.ResourceCPU] = resource.MustParse("0")
	m = f.poll()
	if !m.CPUKnown || m.CPUNow != 25 || len(m.CPU) != 1 || len(m.Mem) != 1 {
		t.Fatal("same timestamp completion duplicated samples", m)
	}
}

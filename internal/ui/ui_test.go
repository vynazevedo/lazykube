package ui

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TNK-Studio/lazykube/internal/anim"
	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/draw"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

var update = flag.Bool("update", false, "update golden files")

const selectedPod = "bilibili-raffle/bilibili-live-7f6b8d9c5-mn4tz"

func fixedCtx() Ctx {
	return Ctx{Pal: theme.DefaultPalette(), Clock: anim.Fixed(40, 0.25, 30)}
}

func check(t *testing.T, name string, c *draw.Canvas) {
	t.Helper()
	for suffix, got := range map[string]string{".txt": c.Plain(), ".ansi": c.String()} {
		path := filepath.Join("testdata", name+suffix+".golden")
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing golden %s, run go test -update: %v", path, err)
		}
		if !bytes.Equal(want, []byte(got)) {
			t.Fatalf("%s differs from golden", path)
		}
	}
}

func canvas(w, h int, draw func(r draw.Region)) *draw.Canvas {
	c := newCanvas(w, h)
	draw(c.Region())
	return c
}

func newCanvas(w, h int) *draw.Canvas {
	return draw.NewCanvas(w, h, theme.BgApp)
}

func demoDetails(tab Tab) DetailsState {
	d := data.NewDemo()
	snap := d.Snapshot()
	pod := snap.Pods[1]
	return DetailsState{
		Tab:      tab,
		HoverTab: -1,
		Pod:      &pod,
		Metrics:  d.Metrics(40, selectedPod, 36),
		Logs:     d.Logs(selectedPod, 40, 500),
		Events:   d.ScopedEvents(data.EventQuery{Scope: data.ScopePod, Pod: selectedPod}),
		Manifest: d.Manifest(selectedPod),
		Describe: d.Describe(selectedPod),
		Diag:     Diagnosis{Containers: d.Containers(selectedPod), LogContainer: "app"},
		LogInfo:  LogInfo{Container: "app", Containers: 2},
		Follow:   true,
		MemBar:   float64(d.Metrics(40, selectedPod, 36).MemNow) / float64(pod.MemLimit),
	}
}

func TestPanelGoldens(t *testing.T) {
	ctx := fixedCtx()
	d := data.NewDemo()
	snap := d.Snapshot()
	m := d.Metrics(40, selectedPod, 36)

	check(t, "header", canvas(158, 1, func(r draw.Region) {
		Header(r, HeaderState{Cluster: snap.Cluster, MetricsOK: true}, ctx)
	}))
	check(t, "header_stale", canvas(158, 1, func(r draw.Region) {
		Header(r, HeaderState{Cluster: snap.Cluster, MetricsOK: false}, ctx)
	}))
	check(t, "node", canvas(37, 16, func(r draw.Region) {
		NodePanel(r, NodeState{Node: snap.Node, CPU: m.NodeCPU, CPUFrac: float64(m.NodeCPU) / 100, MemFrac: snap.Node.MemFraction}, ctx)
	}))
	check(t, "namespaces", canvas(37, 12, func(r draw.Region) {
		NamespacesPanel(r, NamespacesState{Items: snap.Namespaces, Highlight: "bilibili-raffle"}, ctx)
	}))
	check(t, "deployments", canvas(37, 16, func(r draw.Region) {
		DeploymentsPanel(r, DeploymentsState{Items: snap.Deployments}, ctx)
	}))
	check(t, "pods", canvas(54, 19, func(r draw.Region) {
		PodsPanel(r, PodsState{
			Pods: snap.Pods, Selected: 1, Hover: -1, CPU: m.PodCPU, Focused: true,
			SelectionK: map[int]float64{1: 0.22},
		}, ctx)
	}))
	check(t, "heatmap", canvas(54, 25, func(r draw.Region) {
		HeatmapPanel(r, HeatmapState{Labels: m.HeatLabels, Heat: m.Heat, Available: true}, ctx)
	}))
	for _, tab := range Tabs() {
		check(t, "details_"+strings.ToLower(tab.String()), canvas(65, 44, func(r draw.Region) {
			DetailsPanel(r, demoDetails(tab), ctx)
		}))
	}
	check(t, "footer", canvas(160, 2, func(r draw.Region) {
		Footer(r.Sub(0, 0, 160, 1), r.Sub(0, 1, 160, 1), FooterState{FPS: 30})
	}))
	check(t, "help", canvas(160, 48, func(r draw.Region) { HelpOverlay(r, ctx) }))
	check(t, "too_small", canvas(100, 30, func(r draw.Region) { TooSmall(r, 100, 30) }))
}

func TestFocusedBorderIsGradient(t *testing.T) {
	ctx := fixedCtx()
	c := newCanvas(20, 6)
	Panel(c.Region(), "Pods", true, ctx)
	colors := map[theme.RGB]bool{}
	for x := 0; x < 20; x++ {
		colors[c.At(x, 0).Fg] = true
	}
	if len(colors) < 5 {
		t.Fatalf("focused border must vary along the perimeter, got %d colors", len(colors))
	}
	u := newCanvas(20, 6)
	Panel(u.Region(), "Pods", false, ctx)
	if u.At(5, 5).Fg != theme.Border || u.At(0, 3).Fg != theme.Border {
		t.Fatal("unfocused border uses the border token")
	}
	if c.At(3, 0).Fg != ctx.Pal.A || u.At(3, 0).Fg != ctx.Pal.C {
		t.Fatal("title color must follow focus")
	}
}

func TestPanelPaintsEveryInnerCell(t *testing.T) {
	c := newCanvas(30, 10)
	inner := Panel(c.Region(), "x", false, fixedCtx())
	for y := 0; y < inner.H; y++ {
		for x := 0; x < inner.W; x++ {
			if inner.Cell(x, y).Bg != theme.BgPanel {
				t.Fatalf("cell %d,%d leaks background", x, y)
			}
		}
	}
}

func TestPaletteChangesAccents(t *testing.T) {
	ctx := fixedCtx()
	ocean := ctx
	ocean.Pal = theme.NextPalette(ctx.Pal)
	a := newCanvas(54, 25)
	b := newCanvas(54, 25)
	d := data.NewDemo()
	m := d.Metrics(40, selectedPod, 36)
	HeatmapPanel(a.Region(), HeatmapState{Labels: m.HeatLabels, Heat: m.Heat, Available: true}, ctx)
	HeatmapPanel(b.Region(), HeatmapState{Labels: m.HeatLabels, Heat: m.Heat, Available: true}, ocean)
	if a.String() == b.String() {
		t.Fatal("palette must change the heatmap colors")
	}
}

func TestSpriteShape(t *testing.T) {
	px := Sprite(theme.DefaultPalette(), 40, func(int) theme.RGB { return theme.BgPanel })
	if len(px) != SpriteH || len(px[0]) != SpriteW {
		t.Fatal("sprite size mismatch")
	}
	if px[0][0] != spriteOutline || px[22][3] != spriteFeet || px[22][0] != theme.BgPanel {
		t.Fatal("outline or feet mismatch")
	}
	if px[4][3] != theme.OK {
		t.Fatal("status LED must be ok")
	}
	if px[4][10] != spriteBay || px[5][10] != spriteVent || px[5][11] != spriteOutline {
		t.Fatal("bays and vents mismatch")
	}
	blinks := map[theme.RGB]bool{}
	for tick := 0; tick < 40; tick++ {
		blinks[Sprite(theme.DefaultPalette(), tick, func(int) theme.RGB { return theme.BgPanel })[4][5]] = true
	}
	if len(blinks) != 2 {
		t.Fatal("activity LED must blink between two colors")
	}
}

func TestTabAt(t *testing.T) {
	for _, s := range TabSpans() {
		if got, ok := TabAt(s.X); !ok || got != s.Tab {
			t.Fatalf("tab %s not hit", s.Tab)
		}
	}
	if _, ok := TabAt(-1); ok {
		t.Fatal("negative x must miss")
	}
	if TabTop.Next(1) != TabDescribe || TabLog.Next(-1) != TabEvents {
		t.Fatal("tab cycling broken")
	}
}

func TestNetworkCardFlagsStaleData(t *testing.T) {
	m := data.NewDemo().Metrics(40, selectedPod, 36)
	fresh := canvas(60, 5, func(r draw.Region) { networkCard(r, m) }).Plain()
	if !strings.Contains(fresh, "kB/s") || strings.Contains(fresh, "obsoleto") {
		t.Fatalf("fresh network card:\n%s", fresh)
	}
	m.NetStale, m.NetReason = true, "kubelet indisponível"
	stale := canvas(60, 5, func(r draw.Region) { networkCard(r, m) }).Plain()
	if !strings.Contains(stale, "obsoleto: kubelet indisponível") || strings.Contains(stale, "kB/s") {
		t.Fatalf("stale network card:\n%s", stale)
	}
}

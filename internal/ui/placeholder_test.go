package ui

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
)

func topText(st DetailsState) string {
	return canvas(80, 24, func(r draw.Region) { TopTab(r, st, fixedCtx()) }).Plain()
}

func TestTopTabShowsCollectingProgress(t *testing.T) {
	st := demoDetails(TabTop)
	st.Metrics = data.Metrics{Available: true, CPU: []float64{12}, Samples: 1, Window: 60, NetReason: "coletando amostras"}
	out := topText(st)
	for _, want := range []string{"coletando amostras 1/60", "coletando 1/60", "Network coletando amostras"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "streamline") {
		t.Fatal("full-window hint must be hidden while collecting")
	}
}

func TestTopTabHidesProgressWhenWindowIsFull(t *testing.T) {
	out := topText(demoDetails(TabTop))
	if strings.Contains(out, "coletando") || !strings.Contains(out, "streamline") {
		t.Fatalf("demo must render the full chart:\n%s", out)
	}
}

func TestTopTabShowsUnavailableReason(t *testing.T) {
	st := demoDetails(TabTop)
	st.Metrics = data.Metrics{Reason: "sem permissão: pods.metrics.k8s.io", NetReason: "sem permissão: nodes/proxy"}
	out := topText(st)
	for _, want := range []string{"sem permissão: pods.metrics.k8s.io", "Network sem permissão: nodes/proxy"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHeatmapPlaceholders(t *testing.T) {
	render := func(st HeatmapState) string {
		return canvas(54, 25, func(r draw.Region) { HeatmapPanel(r, st, fixedCtx()) }).Plain()
	}
	if out := render(HeatmapState{Available: true}); !strings.Contains(out, "coletando amostras") {
		t.Fatalf("empty heatmap must show collecting:\n%s", out)
	}
	if out := render(HeatmapState{Reason: "metrics-server não instalado"}); !strings.Contains(out, "metrics-server não instalado") {
		t.Fatalf("unavailable heatmap must show the reason:\n%s", out)
	}
	if out := render(HeatmapState{}); !strings.Contains(out, metricsDown) {
		t.Fatalf("unavailable heatmap without reason must use the default:\n%s", out)
	}
}

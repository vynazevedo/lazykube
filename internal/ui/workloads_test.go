package ui

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

func TestDeploymentOverlayMissingShowsCoverage(t *testing.T) {
	ctx := Ctx{Pal: theme.DefaultPalette(), Clock: anim.Fixed(0, 0, 30)}
	for _, c := range []data.Coverage{
		{State: data.CoverageComplete},
		{State: data.CoverageCollecting, Reason: "initial sync"},
		{State: data.CoverageStale, Reason: "watch interrupted\x1b[31m"},
		{State: data.CoverageDenied, Reason: "list/watch permission denied"},
	} {
		canvas := draw.NewCanvas(160, 48, theme.BgApp)
		DeploymentOverlay(canvas.Region(), DeploymentOverlayState{Key: "apps/web", Coverage: c}, ctx)
		plain := canvas.Plain()
		if !strings.Contains(plain, "deployment não observado neste escopo:") || !strings.Contains(plain, "apps/web") {
			t.Fatalf("%s: missing deployment message", c.State)
		}
		want := draw.Sanitize(coverageText(c))
		if !strings.Contains(plain, "Cobertura      "+want) {
			t.Fatalf("%s: missing deployment must still show the coverage %q", c.State, want)
		}
		if strings.Contains(plain, "\x1b") {
			t.Fatalf("%s: coverage must be sanitized", c.State)
		}
	}
}

package ui

import (
	"fmt"
	"math"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

const (
	SpriteW = 20
	SpriteH = 24
)

var (
	spriteOutline = theme.MustHex("#3A3D55")
	spriteFeet    = theme.MustHex("#2A2C3D")
	spriteGap     = theme.MustHex("#0D0E15")
	spriteBezel   = theme.MustHex("#2B2E42")
	spriteShadow  = theme.MustHex("#1A1C2A")
	spriteBay     = theme.MustHex("#4A4E6A")
	spriteVent    = theme.MustHex("#22243A")
)

type NodeState struct {
	Node     data.Node
	CPU      int
	CPUKnown bool
	CPUFrac  float64
	MemFrac  float64
}

func Sprite(pal theme.Palette, t int, transparent func(y int) theme.RGB) draw.Pixels {
	px := draw.NewPixels(SpriteW, SpriteH, theme.BgPanel)
	frame := theme.Mix(pal.B, theme.BgApp, 0.35)
	tf := float64(t)
	for y := 0; y < SpriteH; y++ {
		for x := 0; x < SpriteW; x++ {
			var c theme.RGB
			switch {
			case y >= 22:
				if x == 3 || x == 4 || x == 15 || x == 16 {
					c = spriteFeet
				} else {
					c = transparent(y)
				}
			case y == 0 || y == 21 || x == 0 || x == 19:
				c = spriteOutline
			case y == 1 || y == 20 || x == 1 || x == 18:
				c = frame
			default:
				c = spriteInterior(pal, x, y, tf)
			}
			px[y][x] = c
		}
	}
	return px
}

func spriteInterior(pal theme.Palette, x, y int, t float64) theme.RGB {
	r := (y - 2) % 6
	u := float64((y - 2) / 6)
	switch r {
	case 0:
		return spriteGap
	case 1, 4:
		return spriteBezel
	case 5:
		return spriteShadow
	}
	c := spriteOutline
	if x >= 8 && x <= 16 {
		switch {
		case r == 2:
			c = spriteBay
		case x%2 == 0:
			c = spriteVent
		}
	}
	dim := func(base theme.RGB, k float64) theme.RGB { return theme.Mix(base, theme.BgApp, k) }
	switch {
	case x == 3 && r == 2:
		c = theme.OK
	case x == 3 && r == 3:
		c = pal.A
		if theme.Noise(t*3+u*7) <= 0.15 {
			c = dim(pal.A, 0.7)
		}
	case x == 5 && r == 2:
		c = pal.C
		if theme.Noise(t*7+u*13+1) <= 0.45 {
			c = dim(pal.C, 0.75)
		}
	case x == 5 && r == 3:
		c = pal.C
		if theme.Noise(t*5+u*11+3) <= 0.55 {
			c = dim(pal.C, 0.75)
		}
	}
	return c
}

func GlowFactor(phase float64) float64 {
	return 0.16 + 0.06*math.Sin(2*math.Pi*phase/2.4)
}

func NodePanel(r draw.Region, st NodeState, ctx Ctx) {
	inner := Panel(r, "Node", false, ctx)
	c := inner.Inset(1, 1, 0, 1)
	if c.Empty() {
		return
	}
	glow := theme.Mix(theme.BgPanel, ctx.Pal.C, GlowFactor(ctx.Clock.Phase))
	px := Sprite(ctx.Pal, ctx.Clock.FX(), func(y int) theme.RGB {
		if y >= 22 {
			return glow
		}
		return theme.BgPanel
	})
	spriteRows := SpriteH / 2
	c.HalfBlock(0, max(0, c.H-spriteRows), px)

	gap := 2
	if c.W-SpriteW-gap < 13 {
		gap = 1
	}
	info := c.Sub(SpriteW+gap, 0, c.W-SpriteW-gap, c.H)
	w := info.W
	n := st.Node
	ready, readyColor := "NotReady", theme.Err
	switch {
	case n.Coverage.State != "" && n.Coverage.State != data.CoverageComplete:
		ready, readyColor = "Unknown", theme.Muted
	case n.Ready:
		ready, readyColor = "Ready", theme.OK
	}
	lines := []draw.Line{
		draw.Fit(w, draw.Line{bold(n.Name, theme.Text)}),
		{colored("●", ctx.Pulse(readyColor)), colored(" "+ready, readyColor)},
		nodeCPULine(w, st),
		nil,
		nodeMemLine(w, n),
		nil,
		draw.Fit(w,
			draw.Line{muted("pods "), text(fmt.Sprintf("%d / %d", n.Pods, n.PodCapacity))},
			draw.Line{muted("pods "), text(fmt.Sprintf("%d/%d", n.Pods, n.PodCapacity))},
		),
		draw.Fit(w, draw.Line{muted("arch "), text(n.Arch)}),
	}
	for y, l := range lines {
		info.Line(0, y, l)
	}
	if st.CPUKnown {
		Bar(info, 0, 3, w, st.CPUFrac, ctx.Pal.C, ctx.Pal.B)
	}
	if nodeMemKnown(n) {
		Bar(info, 0, 5, w, st.MemFrac, ctx.Pal.B, ctx.Pal.A)
	}
}

func nodeMemKnown(n data.Node) bool {
	if n.Coverage.State != "" && n.Coverage.State != data.CoverageComplete {
		return false
	}
	return n.MemUsed != "" && n.MemUsed != "n/a"
}

func nodeMemLine(w int, n data.Node) draw.Line {
	used := n.MemUsed
	if !nodeMemKnown(n) {
		used = "n/a"
	}
	return draw.Fit(w,
		draw.Line{muted("mem "), text(used + " / " + n.MemTotal)},
		draw.Line{muted("mem "), text(used + "/" + n.MemTotal)},
		draw.Line{muted("mem "), text(trimUnit(used) + "/" + n.MemTotal)},
	)
}

func nodeCPULine(w int, st NodeState) draw.Line {
	if !st.CPUKnown {
		return draw.Fit(w,
			draw.Line{muted("cpu "), faint("indisponível")},
			draw.Line{muted("cpu "), faint("indisp.")},
			draw.Line{muted("cpu "), faint("n/a")},
		)
	}
	return draw.Fit(w, draw.Line{muted("cpu "), text(fmt.Sprintf("%d%%", st.CPU))})
}

func trimUnit(s string) string {
	for _, suffix := range []string{"Gi", "Mi", "Ki"} {
		if len(s) > len(suffix) && s[len(s)-len(suffix):] == suffix {
			return s[:len(s)-len(suffix)]
		}
	}
	return s
}

package ui

import (
	"fmt"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

type DeploymentOverlayState struct {
	Key        string
	Deployment *data.Deployment
	Coverage   data.Coverage
}

func coverageText(c data.Coverage) string {
	state := string(c.State)
	if state == "" {
		state = string(data.CoverageComplete)
	}
	if c.Reason != "" {
		return state + " · " + c.Reason
	}
	return state
}

type Rect struct {
	X, Y, W, H int
}

func DeploymentOverlayRect(screenW, screenH int) Rect {
	w := max(0, min(screenW-4, 72))
	h := max(0, min(screenH-2, 11))
	return Rect{X: (screenW - w) / 2, Y: (screenH - h) / 2, W: w, H: h}
}

func DeploymentOverlay(screen draw.Region, st DeploymentOverlayState, ctx Ctx) {
	rect := DeploymentOverlayRect(screen.W, screen.H)
	box := screen.Sub(rect.X, rect.Y, rect.W, rect.H)
	inner := Panel(box, "Deployment "+draw.Sanitize(st.Key), true, ctx).Inset(2, 1, 2, 1)
	if inner.Empty() {
		return
	}
	line := func(y int, l draw.Line) {
		if y < inner.H {
			inner.Line(0, y, l.Truncate(inner.W))
		}
	}
	d := st.Deployment
	if d == nil {
		line(1, draw.Line{colored("deployment não observado neste escopo:", theme.Warn)})
		line(2, draw.Line{text(draw.Sanitize(st.Key))})
		line(3, draw.Line{muted("Cobertura      "), text(draw.Sanitize(coverageText(st.Coverage)))})
		line(4, draw.Line{muted("nenhum outro deployment foi selecionado")})
		line(inner.H-1, draw.Line{faint("enter / esc fechar")})
		return
	}
	ready := theme.OK
	if d.Ready < d.Desired {
		ready = theme.Warn
	}
	line(0, draw.Line{muted("Namespace      "), text(draw.Sanitize(d.Namespace))})
	line(1, draw.Line{muted("Nome           "), text(draw.Sanitize(d.Name))})
	line(2, draw.Line{muted("Ready/Desired  "), bold(fmt.Sprintf("%d/%d", d.Ready, d.Desired), ready)})
	line(3, draw.Line{muted("Cobertura      "), text(draw.Sanitize(coverageText(st.Coverage)))})
	line(5, draw.Line{faint("Sem relação inferida com pods; owners dependem de contrato futuro.")})
	line(inner.H-1, draw.Line{faint("enter / esc fechar")})
}

func OverlayHelp() []KeyHelp {
	return []KeyHelp{
		{"enter esc", "close"},
		{"q", "quit"},
	}
}

package app

import "math"

const (
	MinWidth       = 120
	MinHeight      = 36
	ShellMinWidth  = 80
	ShellMinHeight = 24
	dockMinContent = 100
	compactScope   = 4
	wideThreshold  = 140
	nodeHeight     = 16
	nsHeight       = 11
	podsHeight     = 19
	minLeftWidth   = 32
	minCenterWidth = 46
	minRightWidth  = 50
	bodyTop        = 3
)

type Rect struct {
	X, Y, W, H int
}

func (r Rect) Contains(x, y int) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H
}

func (r Rect) Empty() bool {
	return r.W <= 0 || r.H <= 0
}

type LeftMode int

const (
	LeftAuto LeftMode = iota
	LeftShown
	LeftHidden
)

type Layout struct {
	W, H        int
	ContentW    int
	TooSmall    bool
	Compact     bool
	Tree        Rect
	TreeOverlay bool
	ShowLeft    bool
	Header      Rect
	Menu        Rect
	Crumbs      Rect
	Body        Rect
	Node        Rect
	Namespaces  Rect
	Deployments Rect
	Pods        Rect
	Heatmap     Rect
	Details     Rect
	Scope       Rect
	Events      Rect
	Message     Rect
	DividerY    int
	FooterY     int
}

func LeftVisible(w int, mode LeftMode) bool {
	switch mode {
	case LeftShown:
		return true
	case LeftHidden:
		return false
	default:
		return w >= wideThreshold
	}
}

func shellTooSmall(w, h int) bool {
	return w < ShellMinWidth || h < ShellMinHeight
}

func compactSize(w, h int) bool {
	return w < MinWidth || h < MinHeight
}

func compactLayout(w, h int) (Layout, bool) {
	l := Layout{W: w, H: h}
	if shellTooSmall(w, h) {
		l.TooSmall = true
		return l, true
	}
	if !compactSize(w, h) {
		return l, false
	}
	l.shell(w, h)
	l.Compact = true
	return l, true
}

func Compute(w, h int, mode LeftMode) Layout {
	if l, done := compactLayout(w, h); done {
		if l.Compact {
			l.ShowLeft = mode == LeftShown
			l.Pods, l.Details, l.Heatmap = l.Body, l.Body, l.Body
			if l.ShowLeft {
				l.Namespaces, l.Deployments = l.Body, l.Body
			}
		}
		return l
	}
	l := Layout{W: w, H: h}
	l.ShowLeft = LeftVisible(w, mode)
	l.shell(w, h)
	top, m, u := l.Body.Y, l.Body.H, l.Body.W

	var left, center, right int
	if l.ShowLeft {
		left = max(minLeftWidth, round(float64(u)*0.234))
		center = max(minCenterWidth, round(float64(u)*0.341))
		right = u - left - center - 2
		if right < minRightWidth {
			deficit := minRightWidth - right
			take := min(deficit, center-minCenterWidth)
			center -= take
			deficit -= take
			left -= min(deficit, left-minLeftWidth)
			right = u - left - center - 2
		}
	} else {
		center = round(float64(u-1) * 0.45)
		right = u - 1 - center
	}

	x := 1
	if l.ShowLeft {
		l.Node = Rect{X: x, Y: top, W: left, H: nodeHeight}
		l.Namespaces = Rect{X: x, Y: top + nodeHeight, W: left, H: nsHeight}
		l.Deployments = Rect{X: x, Y: top + nodeHeight + nsHeight, W: left, H: m - nodeHeight - nsHeight}
		x += left + 1
	}
	l.Pods = Rect{X: x, Y: top, W: center, H: podsHeight}
	l.Heatmap = Rect{X: x, Y: top + podsHeight, W: center, H: m - podsHeight}
	x += center + 1
	l.Details = Rect{X: x, Y: top, W: right, H: m}
	return l
}

func ComputeWorkloads(w, h int) Layout {
	if l, done := compactLayout(w, h); done {
		if l.Compact {
			l.Pods, l.Deployments, l.Details = l.Body, l.Body, l.Body
		}
		return l
	}
	l := Layout{W: w, H: h}
	l.shell(w, h)
	top, m, u := l.Body.Y, l.Body.H, l.Body.W
	preview := max(minRightWidth, round(float64(u)*0.2+31.4))
	list := u - 1 - preview
	pods := round(float64(m) * 0.63)
	l.Pods = Rect{X: l.Body.X, Y: top, W: list, H: pods}
	l.Deployments = Rect{X: l.Body.X, Y: top + pods, W: list, H: m - pods}
	l.Details = Rect{X: l.Body.X + list + 1, Y: top, W: preview, H: m}
	return l
}

func ComputeNodes(w, h int) Layout {
	if l, done := compactLayout(w, h); done {
		if l.Compact {
			l.Node, l.Pods = l.Body, l.Body
		}
		return l
	}
	l := Layout{W: w, H: h}
	l.shell(w, h)
	summary := round(float64(l.Body.W)*0.4 - 9.2)
	l.Node = Rect{X: l.Body.X, Y: l.Body.Y, W: summary, H: l.Body.H}
	l.Pods = Rect{X: l.Body.X + summary + 1, Y: l.Body.Y, W: l.Body.W - summary - 1, H: l.Body.H}
	return l
}

func ComputeEvents(w, h int) Layout {
	if l, done := compactLayout(w, h); done {
		if l.Compact {
			b := l.Body
			l.Scope = Rect{X: b.X, Y: b.Y, W: b.W, H: compactScope}
			l.Events = Rect{X: b.X, Y: b.Y + compactScope, W: b.W, H: b.H - compactScope}
			l.Message = l.Events
		}
		return l
	}
	l := Layout{W: w, H: h}
	l.shell(w, h)
	b := l.Body
	extra := max(0, h-40) / 4
	scope, message := 6+extra, 11+extra
	l.Scope = Rect{X: b.X, Y: b.Y, W: b.W, H: scope}
	l.Events = Rect{X: b.X, Y: b.Y + scope, W: b.W, H: b.H - scope - message}
	l.Message = Rect{X: b.X, Y: b.Y + b.H - message, W: b.W, H: message}
	return l
}

func ComputeDetail(w, h int) Layout {
	if l, done := compactLayout(w, h); done {
		l.Details = l.Body
		return l
	}
	l := Layout{W: w, H: h}
	l.shell(w, h)
	l.Details = l.Body
	return l
}

func (l *Layout) shell(w, h int) {
	l.ContentW = w
	l.shellBars(w, h)
	l.Body = Rect{X: 1, Y: bodyTop, W: w - 2, H: h - bodyTop - 2}
}

func (l *Layout) shellBars(w, h int) {
	l.Header = Rect{X: 1, Y: 0, W: w - 2, H: 1}
	l.Menu = Rect{X: 1, Y: 1, W: w - 2, H: 1}
	l.Crumbs = Rect{X: 1, Y: 2, W: w - 2, H: 1}
	l.DividerY = h - 2
	l.FooterY = h - 1
}

const (
	treeWide     = 26
	treeNarrow   = 24
	treeWideFrom = 150
)

func TreeWidth(w int) int {
	if w >= treeWideFrom {
		return treeWide
	}
	return treeNarrow
}

func withTree(w, h int, compute func(w, h int) Layout) Layout {
	tw := TreeWidth(w)
	inner := w - tw - 1
	if inner >= dockMinContent {
		l := compute(inner, h)
		if !l.TooSmall {
			l.dock(w, h, tw)
			return l
		}
	}
	l := compute(w, h)
	if !l.TooSmall {
		l.Tree = Rect{X: l.Body.X, Y: l.Body.Y, W: min(tw, l.Body.W), H: l.Body.H}
		l.TreeOverlay = true
	}
	return l
}

func (l *Layout) keepOnly(keep *Rect) {
	for _, r := range []*Rect{&l.Node, &l.Namespaces, &l.Deployments, &l.Pods, &l.Heatmap, &l.Details, &l.Scope, &l.Events, &l.Message} {
		if r != keep && r != &l.Scope {
			*r = Rect{}
		}
	}
}

func (l *Layout) dock(w, h, tw int) {
	dx := tw + 1
	for _, r := range []*Rect{&l.Body, &l.Node, &l.Namespaces, &l.Deployments, &l.Pods, &l.Heatmap, &l.Details, &l.Scope, &l.Events, &l.Message} {
		if !r.Empty() {
			r.X += dx
		}
	}
	l.W, l.ContentW = w, l.W
	l.shellBars(w, h)
	l.Tree = Rect{X: 1, Y: bodyTop, W: tw, H: h - bodyTop - 2}
}

func round(v float64) int {
	return int(math.Round(v))
}

type Area int

const (
	AreaNone Area = iota
	AreaNode
	AreaNamespaces
	AreaDeployments
	AreaPods
	AreaHeatmap
	AreaDetails
	AreaMenu
	AreaCrumbs
	AreaTree
)

func (l Layout) Hit(x, y int) Area {
	if l.TooSmall {
		return AreaNone
	}
	if !l.Tree.Empty() && l.Tree.Contains(x, y) {
		return AreaTree
	}
	for _, c := range []struct {
		r Rect
		a Area
	}{
		{l.Node, AreaNode},
		{l.Namespaces, AreaNamespaces},
		{l.Deployments, AreaDeployments},
		{l.Pods, AreaPods},
		{l.Heatmap, AreaHeatmap},
		{l.Details, AreaDetails},
		{l.Menu, AreaMenu},
		{l.Crumbs, AreaCrumbs},
	} {
		if !c.r.Empty() && c.r.Contains(x, y) {
			return c.a
		}
	}
	return AreaNone
}

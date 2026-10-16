package app

import "testing"

func TestComputeReference(t *testing.T) {
	l := Compute(160, 48, LeftAuto)
	if l.TooSmall || !l.ShowLeft {
		t.Fatal("160x48 must show every column")
	}
	if l.Menu != (Rect{1, 1, 158, 1}) || l.Crumbs != (Rect{1, 2, 158, 1}) || l.Body != (Rect{1, 3, 158, 43}) {
		t.Fatalf("shell rows mismatch: %+v %+v %+v", l.Menu, l.Crumbs, l.Body)
	}
	if l.Node != (Rect{1, 3, 37, 16}) || l.Namespaces != (Rect{1, 19, 37, 11}) || l.Deployments != (Rect{1, 30, 37, 16}) {
		t.Fatalf("left column mismatch: %+v %+v %+v", l.Node, l.Namespaces, l.Deployments)
	}
	if l.Pods != (Rect{39, 3, 54, 19}) || l.Heatmap != (Rect{39, 22, 54, 24}) {
		t.Fatalf("center column mismatch: %+v %+v", l.Pods, l.Heatmap)
	}
	if l.Details != (Rect{94, 3, 65, 43}) {
		t.Fatalf("details mismatch: %+v", l.Details)
	}
	if l.DividerY != 46 || l.FooterY != 47 {
		t.Fatal("footer rows mismatch")
	}
	if l.Details.X+l.Details.W != 159 {
		t.Fatal("right margin must be one column")
	}
}

func TestComputeSizes(t *testing.T) {
	tests := []struct {
		w, h     int
		mode     LeftMode
		small    bool
		showLeft bool
	}{
		{119, 48, LeftAuto, true, false},
		{160, 35, LeftAuto, true, false},
		{120, 36, LeftAuto, false, false},
		{139, 40, LeftAuto, false, false},
		{140, 40, LeftAuto, false, true},
		{130, 40, LeftShown, false, true},
		{200, 60, LeftHidden, false, false},
		{300, 80, LeftAuto, false, true},
	}
	for _, tt := range tests {
		l := Compute(tt.w, tt.h, tt.mode)
		if l.TooSmall != tt.small || l.ShowLeft != tt.showLeft {
			t.Fatalf("%dx%d mode %d: small=%v left=%v", tt.w, tt.h, tt.mode, l.TooSmall, l.ShowLeft)
		}
		if l.TooSmall {
			continue
		}
		used := l.Pods.W + l.Details.W + 1
		if l.ShowLeft {
			used += l.Node.W + 1
		}
		if used != tt.w-2 {
			t.Fatalf("%dx%d: columns use %d of %d", tt.w, tt.h, used, tt.w-2)
		}
		roomForMinimums := !l.ShowLeft || tt.w-2 >= minLeftWidth+minCenterWidth+minRightWidth+2
		if roomForMinimums && (l.Pods.W < minCenterWidth || l.Details.W < minRightWidth) {
			t.Fatalf("%dx%d: minimum widths violated: %d %d", tt.w, tt.h, l.Pods.W, l.Details.W)
		}
		if l.ShowLeft && l.Node.W < minLeftWidth {
			t.Fatalf("%dx%d: left too narrow", tt.w, tt.h)
		}
		if l.Details.H != tt.h-5 || l.Details.Y != 3 || l.Heatmap.Y+l.Heatmap.H != tt.h-2 || l.Deployments.H > 0 && l.Deployments.H < 4 {
			t.Fatalf("%dx%d: heights mismatch", tt.w, tt.h)
		}
	}
}

func TestHit(t *testing.T) {
	l := Compute(160, 48, LeftAuto)
	cases := map[[2]int]Area{
		{5, 5}:   AreaNode,
		{5, 20}:  AreaNamespaces,
		{5, 40}:  AreaDeployments,
		{50, 10}: AreaPods,
		{50, 30}: AreaHeatmap,
		{120, 3}: AreaDetails,
		{10, 1}:  AreaMenu,
		{10, 2}:  AreaCrumbs,
		{0, 0}:   AreaNone,
		{38, 10}: AreaNone,
	}
	for p, want := range cases {
		if got := l.Hit(p[0], p[1]); got != want {
			t.Fatalf("hit %v got %d want %d", p, got, want)
		}
	}
	if Compute(100, 30, LeftAuto).Hit(5, 5) != AreaNone {
		t.Fatal("too small layout has no areas")
	}
}

func TestComputeDetailUsesWholeBody(t *testing.T) {
	for _, size := range [][2]int{{160, 48}, {130, 40}} {
		l := ComputeDetail(size[0], size[1])
		if l.TooSmall || l.Details != l.Body || l.Body != (Rect{1, 3, size[0] - 2, size[1] - 5}) {
			t.Fatalf("%v: detail layout %+v", size, l)
		}
		if !l.Pods.Empty() || !l.Node.Empty() || !l.Heatmap.Empty() || l.ShowLeft {
			t.Fatalf("%v: detail layout must not keep dashboard panels", size)
		}
		if l.Hit(10, 1) != AreaMenu || l.Hit(10, 2) != AreaCrumbs || l.Hit(10, 10) != AreaDetails {
			t.Fatalf("%v: detail hit areas", size)
		}
	}
	if !ComputeDetail(100, 30).TooSmall {
		t.Fatal("detail keeps the global minimum")
	}
}

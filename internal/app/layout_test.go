package app

import "testing"

func TestComputeReference(t *testing.T) {
	l := Compute(160, 48, LeftAuto)
	if l.TooSmall || !l.ShowLeft {
		t.Fatal("160x48 must show every column")
	}
	if l.Node != (Rect{1, 2, 37, 16}) || l.Namespaces != (Rect{1, 18, 37, 12}) || l.Deployments != (Rect{1, 30, 37, 16}) {
		t.Fatalf("left column mismatch: %+v %+v %+v", l.Node, l.Namespaces, l.Deployments)
	}
	if l.Pods != (Rect{39, 2, 54, 19}) || l.Heatmap != (Rect{39, 21, 54, 25}) {
		t.Fatalf("center column mismatch: %+v %+v", l.Pods, l.Heatmap)
	}
	if l.Details != (Rect{94, 2, 65, 44}) {
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
		if l.Details.H != tt.h-4 || l.Heatmap.Y+l.Heatmap.H != tt.h-2 {
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

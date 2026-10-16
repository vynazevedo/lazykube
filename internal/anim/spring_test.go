package anim

import (
	"math"
	"testing"
)

func TestSpringConvergesAndRests(t *testing.T) {
	s := NewSpring(30, 0)
	s.SetTarget(1)
	if s.AtRest() {
		t.Fatal("a new target must wake the spring")
	}
	for i := 0; i < 300 && !s.AtRest(); i++ {
		s.Step()
	}
	if !s.AtRest() || s.Pos != 1 || s.Vel != 0 {
		t.Fatalf("spring must settle on the target, pos=%f vel=%f", s.Pos, s.Vel)
	}
}

func TestSpringMovesTowardsTarget(t *testing.T) {
	s := NewSpring(30, 0)
	s.SetTarget(0.6)
	s.Step()
	if s.Pos <= 0 || s.Pos >= 0.6 {
		t.Fatalf("first step must move partially, got %f", s.Pos)
	}
	s.Snap()
	if s.Pos != 0.6 {
		t.Fatal("snap jumps to the target")
	}
}

func TestFade(t *testing.T) {
	f := Fade{From: 0, To: 0.22, Start: 1, Duration: 0.18}
	if f.At(0.5) != 0 || f.At(2) != 0.22 {
		t.Fatal("fade endpoints")
	}
	if got := f.At(1.09); math.Abs(got-0.11) > 1e-9 {
		t.Fatalf("fade midpoint got %f", got)
	}
	if f.Done(1.1) || !f.Done(1.18) {
		t.Fatal("fade completion")
	}
	if (Fade{To: 0.3}).At(0) != 0.3 {
		t.Fatal("zero duration jumps to the end")
	}
}

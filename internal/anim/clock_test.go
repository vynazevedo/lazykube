package anim

import (
	"testing"
	"time"
)

func TestClockAdvance(t *testing.T) {
	start := time.Unix(1000, 0)
	c := NewClock(start, 30)
	c.Advance(start.Add(1500 * time.Millisecond))
	if c.T != 10 || c.Phase != 1.5 || c.Step() != 5 {
		t.Fatalf("got T=%d phase=%f step=%d", c.T, c.Phase, c.Step())
	}
	if c.Spinner() != '⠋' {
		t.Fatalf("spinner frame 0 expected, got %q", c.Spinner())
	}
}

func TestClockPauseResume(t *testing.T) {
	start := time.Unix(1000, 0)
	c := NewClock(start, 30)
	c.Advance(start.Add(time.Second))
	c.Pause(start.Add(time.Second))
	c.Advance(start.Add(5 * time.Second))
	if c.Phase != 1 {
		t.Fatalf("paused clock must not advance, phase=%f", c.Phase)
	}
	c.Resume(start.Add(5 * time.Second))
	c.Advance(start.Add(6 * time.Second))
	if c.Phase != 2 {
		t.Fatalf("resume must skip paused time, phase=%f", c.Phase)
	}
}

func TestFixedClock(t *testing.T) {
	c := Fixed(40, 0.5, 30)
	c.Advance(time.Now())
	if c.T != 40 || c.Phase != 0.5 || !c.CursorVisible() {
		t.Fatal("fixed clock must not move")
	}
	if Fixed(13, 0.45, 30).Spinner() != '⠸' || Fixed(0, 1.2, 30).CursorVisible() {
		t.Fatal("spinner or cursor mismatch")
	}
	if Fixed(99, 0, 30).Spinner() != '⠋' {
		t.Fatal("frozen effects keep the first spinner frame regardless of data ticks")
	}
}

package anim

import (
	"math"

	"github.com/charmbracelet/harmonica"
)

const (
	springFrequency = 6.0
	springDamping   = 0.5
	restEpsilon     = 0.001
)

type Spring struct {
	s      harmonica.Spring
	Pos    float64
	Vel    float64
	Target float64
}

func NewSpring(fps int, value float64) Spring {
	if fps <= 0 {
		fps = 30
	}
	return Spring{
		s:      harmonica.NewSpring(harmonica.FPS(fps), springFrequency, springDamping),
		Pos:    value,
		Target: value,
	}
}

func (s *Spring) SetTarget(v float64) {
	s.Target = v
}

func (s *Spring) Step() {
	s.Pos, s.Vel = s.s.Update(s.Pos, s.Vel, s.Target)
	if s.AtRest() {
		s.Snap()
	}
}

func (s *Spring) Snap() {
	s.Pos, s.Vel = s.Target, 0
}

func (s Spring) AtRest() bool {
	return math.Abs(s.Vel) < restEpsilon && math.Abs(s.Pos-s.Target) < restEpsilon
}

type Fade struct {
	From     float64
	To       float64
	Start    float64
	Duration float64
}

func (f Fade) At(phase float64) float64 {
	if f.Duration <= 0 || phase >= f.Start+f.Duration {
		return f.To
	}
	if phase <= f.Start {
		return f.From
	}
	k := (phase - f.Start) / f.Duration
	return f.From + (f.To-f.From)*k
}

func (f Fade) Done(phase float64) bool {
	return f.Duration <= 0 || phase >= f.Start+f.Duration
}

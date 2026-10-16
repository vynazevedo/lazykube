package anim

import "time"

const TickInterval = 150 * time.Millisecond

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

type Clock struct {
	start    time.Time
	last     time.Time
	paused   time.Duration
	frozenAt time.Time
	frozen   bool
	Phase    float64
	T        int
	FPS      float64
}

func NewClock(now time.Time, targetFPS int) Clock {
	return Clock{start: now, last: now, FPS: float64(targetFPS)}
}

func Fixed(t int, phase float64, fps float64) Clock {
	return Clock{T: t, Phase: phase, FPS: fps, frozen: true}
}

func (c *Clock) Advance(now time.Time) {
	if c.frozen {
		return
	}
	if dt := now.Sub(c.last).Seconds(); dt > 0 {
		inst := 1 / dt
		if c.FPS <= 0 {
			c.FPS = inst
		} else {
			c.FPS = c.FPS*0.9 + inst*0.1
		}
	}
	c.last = now
	elapsed := now.Sub(c.start) - c.paused
	c.Phase = elapsed.Seconds()
	c.T = int(elapsed / TickInterval)
}

func (c *Clock) Pause(now time.Time) {
	if c.frozen {
		return
	}
	c.frozen = true
	c.frozenAt = now
}

func (c *Clock) Resume(now time.Time) {
	if !c.frozen || c.frozenAt.IsZero() {
		return
	}
	c.paused += now.Sub(c.frozenAt)
	c.frozenAt = time.Time{}
	c.frozen = false
	c.last = now
}

func (c Clock) Paused() bool {
	return c.frozen
}

func (c Clock) Step() int {
	return c.T / 2
}

func (c Clock) FX() int {
	return int(c.Phase / TickInterval.Seconds())
}

func (c Clock) Spinner() rune {
	n := len(spinnerFrames)
	return spinnerFrames[((c.FX()%n)+n)%n]
}

func (c Clock) CursorVisible() bool {
	return int(c.Phase)%2 == 0
}

package theme

import "math"

func Frac(x float64) float64 {
	return x - math.Floor(x)
}

func Cycle(p Palette, pos float64) RGB {
	stops := [4]RGB{p.A, p.B, p.C, p.A}
	pos = Frac(pos) * 3
	i := int(pos)
	if i >= 3 {
		i = 2
	}
	return Mix(stops[i], stops[i+1], pos-float64(i))
}

func TextGradient(p Palette, n int, phase float64) []RGB {
	out := make([]RGB, n)
	for i := range out {
		out[i] = Cycle(p, float64(i)/float64(n)+phase/6)
	}
	return out
}

func BorderGradient(p Palette, j, perimeter int, phase float64) RGB {
	if perimeter <= 0 {
		return p.A
	}
	return Cycle(p, float64(j)/float64(perimeter)*1.5+phase/6)
}

func Linear(from, to RGB, i, n int) RGB {
	if n <= 1 {
		return from
	}
	return Mix(from, to, float64(i)/float64(n-1))
}

func Heat(p Palette, k float64) RGB {
	k = Clamp(k, 0, 1)
	if k < 0.5 {
		return Mix(HeatLow, p.B, k*2)
	}
	return Mix(p.B, p.A, (k-0.5)*2)
}

func Pulse(base RGB, phase float64) RGB {
	k := 0.65 * (0.5 - 0.5*math.Cos(2*math.Pi*phase/1.6))
	return Mix(base, BgPanel, k)
}

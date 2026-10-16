package draw

import "math"

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

func SparkRune(v, maxV float64) rune {
	if maxV <= 0 {
		return sparkRunes[0]
	}
	i := int(math.Round(v / maxV * 7))
	if i < 0 {
		i = 0
	}
	if i > 7 {
		i = 7
	}
	return sparkRunes[i]
}

func Sparkline(values []float64, maxV float64) string {
	out := make([]rune, len(values))
	for i, v := range values {
		out[i] = SparkRune(v, maxV)
	}
	return string(out)
}

func Filled(value, maxV float64, cells int) int {
	if maxV <= 0 || cells <= 0 {
		return 0
	}
	n := int(math.Round(value / maxV * float64(cells)))
	if n < 0 {
		return 0
	}
	if n > cells {
		return cells
	}
	return n
}

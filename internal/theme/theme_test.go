package theme

import (
	"math"
	"testing"
)

func TestParseHex(t *testing.T) {
	c, err := ParseHex("#FF5FAF")
	if err != nil {
		t.Fatal(err)
	}
	if c != (RGB{R: 0xFF, G: 0x5F, B: 0xAF}) {
		t.Fatalf("got %+v", c)
	}
	if c.Hex() != "#FF5FAF" {
		t.Fatalf("hex round trip: %s", c.Hex())
	}
	for _, bad := range []string{"", "#12345", "#GGGGGG", "1234567"} {
		if _, err := ParseHex(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestMix(t *testing.T) {
	a := RGB{0, 0, 0}
	b := RGB{200, 100, 50}
	if Mix(a, b, 0) != a || Mix(a, b, 1) != b {
		t.Fatal("endpoints must be preserved")
	}
	if got := Mix(a, b, 0.5); got != (RGB{100, 50, 25}) {
		t.Fatalf("midpoint got %+v", got)
	}
	if Mix(a, b, -3) != a || Mix(a, b, 9) != b {
		t.Fatal("k must be clamped")
	}
}

func TestNoiseRangeAndDeterminism(t *testing.T) {
	for i := 0; i < 1000; i++ {
		v := Noise(float64(i) * 0.37)
		if v < 0 || v >= 1 {
			t.Fatalf("noise out of range: %f", v)
		}
		if v != Noise(float64(i)*0.37) {
			t.Fatal("noise must be deterministic")
		}
	}
}

func TestCycleStops(t *testing.T) {
	p := DefaultPalette()
	if Cycle(p, 0) != p.A {
		t.Fatal("pos 0 must be A")
	}
	if Cycle(p, 1.0/3) != p.B {
		t.Fatalf("pos 1/3 must be B, got %s", Cycle(p, 1.0/3).Hex())
	}
	if Cycle(p, 2.0/3) != p.C {
		t.Fatalf("pos 2/3 must be C, got %s", Cycle(p, 2.0/3).Hex())
	}
	if Cycle(p, 1) != p.A {
		t.Fatal("pos 1 wraps to A")
	}
}

func TestHeatScale(t *testing.T) {
	p := DefaultPalette()
	if Heat(p, 0) != HeatLow || Heat(p, 0.5) != p.B || Heat(p, 1) != p.A {
		t.Fatal("heat stops must be low, B, A")
	}
}

func TestPulseRange(t *testing.T) {
	if Pulse(OK, 0) != OK {
		t.Fatal("pulse starts at full color")
	}
	mid := Pulse(OK, 0.8)
	if mid != Mix(OK, BgPanel, 0.65) {
		t.Fatalf("pulse midpoint got %s", mid.Hex())
	}
}

func TestPalettes(t *testing.T) {
	p := DefaultPalette()
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		seen[p.Name] = true
		p = NextPalette(p)
	}
	if len(seen) != 3 || p.Name != "charm" {
		t.Fatalf("palette cycle broken: %v %s", seen, p.Name)
	}
	if _, err := PaletteByName("OCEAN"); err != nil {
		t.Fatal(err)
	}
	if _, err := PaletteByName("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestFrac(t *testing.T) {
	if math.Abs(Frac(2.25)-0.25) > 1e-9 || math.Abs(Frac(-0.25)-0.75) > 1e-9 {
		t.Fatal("frac broken")
	}
}

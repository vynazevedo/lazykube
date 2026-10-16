package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type RGB struct {
	R, G, B uint8
}

func (c RGB) RGBA() (r, g, b, a uint32) {
	return uint32(c.R) * 0x101, uint32(c.G) * 0x101, uint32(c.B) * 0x101, 0xffff
}

func (c RGB) Hex() string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

func ParseHex(s string) (RGB, error) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) != 6 {
		return RGB{}, fmt.Errorf("invalid hex color %q", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return RGB{}, fmt.Errorf("invalid hex color %q: %w", s, err)
	}
	return RGB{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v)}, nil
}

func MustHex(s string) RGB {
	c, err := ParseHex(s)
	if err != nil {
		panic(err)
	}
	return c
}

var (
	BgApp       = MustHex("#0B0C12")
	BgPanel     = MustHex("#10111A")
	BgInset     = MustHex("#0D0E15")
	BgCard      = MustHex("#141522")
	BgChip      = MustHex("#161827")
	BgChip2     = MustHex("#121420")
	Border      = MustHex("#2A2C3D")
	BorderSoft  = MustHex("#1E2030")
	BorderFaint = MustHex("#1A1C2A")
	Text        = MustHex("#E6E6F0")
	TextSoft    = MustHex("#C9CBDD")
	TextMid     = MustHex("#B9BBD0")
	Muted       = MustHex("#8A8CA3")
	Faint       = MustHex("#5C5F7A")
	DimGlyph    = MustHex("#6B6E88")
	OK          = MustHex("#5FE3A1")
	Warn        = MustHex("#FFC857")
	Err         = MustHex("#FF6B6B")
	White       = MustHex("#FFFFFF")
	Hover       = MustHex("#1A1C2A")
	HeatLow     = MustHex("#161827")
)

type Palette struct {
	Name string
	A    RGB
	B    RGB
	C    RGB
}

var palettes = []Palette{
	{Name: "charm", A: MustHex("#FF5FAF"), B: MustHex("#9D7CFF"), C: MustHex("#5FD7FF")},
	{Name: "ocean", A: MustHex("#4FC3F7"), B: MustHex("#5C7CFA"), C: MustHex("#63E6BE")},
	{Name: "matrix", A: MustHex("#B6FF5C"), B: MustHex("#3DDC84"), C: MustHex("#9CF5D0")},
}

func Palettes() []Palette {
	out := make([]Palette, len(palettes))
	copy(out, palettes)
	return out
}

func PaletteByName(name string) (Palette, error) {
	for _, p := range palettes {
		if strings.EqualFold(p.Name, name) {
			return p, nil
		}
	}
	return Palette{}, fmt.Errorf("unknown palette %q", name)
}

func NextPalette(current Palette) Palette {
	for i, p := range palettes {
		if p.Name == current.Name {
			return palettes[(i+1)%len(palettes)]
		}
	}
	return palettes[0]
}

func DefaultPalette() Palette {
	return palettes[0]
}

func Mix(a, b RGB, k float64) RGB {
	k = Clamp(k, 0, 1)
	lerp := func(x, y uint8) uint8 {
		return uint8(math.Round(float64(x) + (float64(y)-float64(x))*k))
	}
	return RGB{R: lerp(a.R, b.R), G: lerp(a.G, b.G), B: lerp(a.B, b.B)}
}

func Noise(i float64) float64 {
	x := math.Sin(i*12.9898+78.233) * 43758.5453
	return x - math.Floor(x)
}

func Clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

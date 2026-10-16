package draw

import (
	"strings"
	"testing"

	"github.com/TNK-Studio/lazykube/internal/theme"
)

var (
	red  = theme.RGB{R: 255}
	blue = theme.RGB{B: 255}
)

func TestCanvasGroupsRuns(t *testing.T) {
	c := NewCanvas(4, 1, theme.BgApp)
	c.Region().Text(0, 0, "abcd", Fg(red))
	out := c.String()
	if n := strings.Count(out, "\x1b[0;38;2"); n != 1 {
		t.Fatalf("expected one SGR for a uniform run, got %d: %q", n, out)
	}
	c.Region().Put(2, 0, 'X', Fg(blue))
	if n := strings.Count(c.String(), "\x1b[0;38;2"); n != 3 {
		t.Fatalf("expected three runs, got %d", n)
	}
	if c.Plain() != "abXd" {
		t.Fatalf("plain got %q", c.Plain())
	}
}

func TestRegionClipping(t *testing.T) {
	c := NewCanvas(5, 3, theme.BgApp)
	r := c.Region().Sub(1, 1, 3, 1)
	r.Text(0, 0, "hello", Fg(red))
	r.Put(-1, 0, 'Z', Fg(red))
	r.Put(0, 5, 'Z', Fg(red))
	want := "     \n hel \n     "
	if c.Plain() != want {
		t.Fatalf("got %q want %q", c.Plain(), want)
	}
	if sub := c.Region().Sub(4, 2, 10, 10); sub.W != 1 || sub.H != 1 {
		t.Fatalf("sub must clip to parent, got %dx%d", sub.W, sub.H)
	}
}

func TestTruncateAndPad(t *testing.T) {
	if Truncate("bilibili-raffle", 10) != "bilibili-…" {
		t.Fatalf("got %q", Truncate("bilibili-raffle", 10))
	}
	if Truncate("abc", 3) != "abc" || Truncate("abc", 0) != "" || Truncate("abc", 1) != "…" {
		t.Fatal("edge cases broken")
	}
	if PadLeft("7", 3) != "  7" || PadRight("ab", 4) != "ab  " {
		t.Fatal("padding broken")
	}
}

func TestLineFitAndTruncate(t *testing.T) {
	long := Line{S("mem ", Fg(red)), S("1.4Gi / 3.8Gi", Fg(blue))}
	short := Line{S("mem ", Fg(red)), S("1.4/3.8Gi", Fg(blue))}
	if got := Fit(13, long, short); got.Width() != 13 {
		t.Fatalf("fit should pick the short variant, got width %d", got.Width())
	}
	if got := Fit(5, long, short); got.Width() != 5 {
		t.Fatalf("fit should truncate the last variant, got width %d", got.Width())
	}
	if got := long.Truncate(6); got.Width() != 6 {
		t.Fatalf("truncate width got %d", got.Width())
	}
}

func TestBrailleBits(t *testing.T) {
	b := NewBraille(1, 1)
	for dy := 0; dy < 4; dy++ {
		for dx := 0; dx < 2; dx++ {
			b.Set(dx, dy, red)
		}
	}
	if b.Rune(0, 0) != '⣿' {
		t.Fatalf("full cell got %q", b.Rune(0, 0))
	}
	b2 := NewBraille(1, 1)
	b2.Set(0, 3, red)
	b2.Set(1, 0, red)
	if b2.Rune(0, 0) != rune(0x2800+0x40+0x08) {
		t.Fatalf("bit layout wrong: %U", b2.Rune(0, 0))
	}
	b2.Set(5, 5, red)
	if b2.Has(1, 1) {
		t.Fatal("out of range dot must be ignored")
	}
}

func TestBrailleLineIsContinuous(t *testing.T) {
	b := NewBraille(10, 2)
	b.Line(0, 7, 19, 0, func(int) theme.RGB { return red })
	for col := 0; col < 10; col++ {
		if !b.Has(col, 0) && !b.Has(col, 1) {
			t.Fatalf("column %d has no dots", col)
		}
	}
}

func TestSparkAndFilled(t *testing.T) {
	if Sparkline([]float64{0, 6, 12}, 12) != "▁▅█" {
		t.Fatalf("got %q", Sparkline([]float64{0, 6, 12}, 12))
	}
	if SparkRune(99, 10) != '█' || SparkRune(-1, 10) != '▁' || SparkRune(5, 0) != '▁' {
		t.Fatal("spark clamp broken")
	}
	if Filled(10, 20, 7) != 4 || Filled(30, 20, 7) != 7 || Filled(-2, 20, 7) != 0 {
		t.Fatal("filled broken")
	}
}

func TestHalfBlock(t *testing.T) {
	c := NewCanvas(2, 1, theme.BgApp)
	px := NewPixels(2, 2, red)
	px[1][1] = blue
	c.Region().HalfBlock(0, 0, px)
	if c.At(0, 0).Rune != ' ' || c.At(0, 0).Bg != red {
		t.Fatal("equal pixels must collapse to a background cell")
	}
	if cl := c.At(1, 0); cl.Rune != '▀' || cl.Fg != red || cl.Bg != blue {
		t.Fatalf("half block wrong: %+v", cl)
	}
}

func TestWideAndCombiningGraphemes(t *testing.T) {
	if Width("日本") != 4 || Width("é") != 1 || Width("abc") != 3 {
		t.Fatalf("widths: %d %d", Width("日本"), Width("é"))
	}
	c := NewCanvas(6, 1, theme.BgApp)
	n := c.Region().Text(0, 0, "日éx", Fg(red))
	if n != 4 {
		t.Fatalf("advance got %d", n)
	}
	if c.Plain() != "日éx  " {
		t.Fatalf("plain got %q", c.Plain())
	}
	if !c.At(1, 0).Cont {
		t.Fatal("wide rune must reserve a continuation cell")
	}
	c.Region().Put(1, 0, 'Z', Fg(red))
	if c.At(0, 0).Rune != ' ' || c.At(1, 0).Cont {
		t.Fatal("overwriting a continuation must clear the wide head")
	}
	edge := NewCanvas(3, 1, theme.BgApp)
	edge.Region().Text(2, 0, "日", Fg(red))
	if edge.Plain() != "   " {
		t.Fatalf("wide rune at the edge must not overflow, got %q", edge.Plain())
	}
}

func TestTruncateByWidth(t *testing.T) {
	if got := Truncate("日本語テキスト", 5); got != "日本…" || Width(got) != 5 {
		t.Fatalf("got %q width %d", got, Width(got))
	}
	if got := PadLeft("日", 4); got != "  日" {
		t.Fatalf("pad got %q", got)
	}
	l := Line{S("日本語", Fg(red)), S("abc", Fg(blue))}
	if w := l.Truncate(4).Width(); w > 4 {
		t.Fatalf("line truncate width %d", w)
	}
	if got := Wrap("abcdef", 4); len(got) != 2 || got[1] != "ef" {
		t.Fatalf("wrap got %q", got)
	}
	if got := Wrap("join failed: already ended", 16); len(got) != 2 || got[0] != "join failed:" || got[1] != "already ended" {
		t.Fatalf("word wrap got %q", got)
	}
}

func TestSanitizeControls(t *testing.T) {
	if got := Sanitize("a\x1b[31mb\tc\x07"); got != "a?[31mb c?" {
		t.Fatalf("got %q", got)
	}
	c := NewCanvas(8, 1, theme.BgApp)
	c.Region().Text(0, 0, "\x1b]0;x\x07", Fg(red))
	if strings.ContainsAny(c.String(), "\x07") || strings.Contains(c.Plain(), "\x1b") {
		t.Fatal("control characters must never reach the terminal")
	}
}

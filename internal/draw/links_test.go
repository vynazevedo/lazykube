package draw

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/weburl"
)

var (
	sgr  = regexp.MustCompile("\x1b\\[[0-9;]*m")
	osc8 = regexp.MustCompile("\x1b\\]8;;([^\x1b]*)\x1b\\\\")
)

func emittedLinks(t *testing.T, out string) map[string]string {
	t.Helper()
	links := map[string]string{}
	for n, line := range strings.Split(sgr.ReplaceAllString(out, ""), "\n") {
		marks := osc8.FindAllStringSubmatchIndex(line, -1)
		for i, m := range marks {
			target := line[m[2]:m[3]]
			if target == "" {
				continue
			}
			if i+1 >= len(marks) {
				t.Fatalf("link to %s is never closed", target)
			}
			links[fmt.Sprintf("%d:%d", n, i)] = target + "|" + line[m[1]:marks[i+1][0]]
		}
	}
	return links
}

func requireWholeLinks(t *testing.T, c *Canvas) int {
	t.Helper()
	links := emittedLinks(t, c.String())
	for at, link := range links {
		target, label, _ := strings.Cut(link, "|")
		if target != label {
			t.Fatalf("%s: label %q opens %q", at, label, target)
		}
	}
	c.SealLinks()
	for y := range c.Height() {
		for x := 0; x < c.Width(); {
			u := c.At(x, y).Link
			if u.IsZero() {
				x++
				continue
			}
			var label strings.Builder
			for ; x < c.Width() && c.At(x, y).Link == u; x++ {
				label.WriteRune(c.At(x, y).Rune)
			}
			if label.String() != u.String() {
				t.Fatalf("row %d keeps %q on a link to %q", y, label.String(), u.String())
			}
		}
	}
	if again := emittedLinks(t, c.String()); len(again) != len(links) {
		t.Fatalf("sealing changed the output from %d to %d links", len(links), len(again))
	}
	return len(links)
}

func longLink(t *testing.T) weburl.URL {
	t.Helper()
	return mustURL(t, "http", "trusted.example.com."+strings.Repeat("a", 60)+".evil.com")
}

func linkedCanvas(u weburl.URL, w, x int) *Canvas {
	c := NewCanvas(w, 3, theme.BgApp)
	c.Region().Text(x, 1, u.String(), Fg(theme.Text).WithLink(u))
	return c
}

func TestAnyOccludedCellRemovesTheWholeLink(t *testing.T) {
	u := longLink(t)
	n := len(u.String())
	const left = 10
	for _, width := range []int{n + 2*left, 160, 200, 240, 300} {
		if requireWholeLinks(t, linkedCanvas(u, width, left)) != 1 {
			t.Fatal("an untouched link is emitted")
		}
		covers := map[string]func(r Region, x int){
			"letter":    func(r Region, x int) { r.Put(x, 1, 'Z', Fg(theme.Text)) },
			"same rune": func(r Region, x int) { r.Put(x, 1, rune(u.String()[x-left]), Fg(theme.Text)) },
			"fill":      func(r Region, x int) { r.Sub(x, 0, 1, 3).Fill(theme.BgPanel) },
			"line":      func(r Region, x int) { r.HLine(x, 1, 1, '─', Fg(theme.Text)) },
			"combining": func(r Region, x int) { r.Text(x, 1, "é", Fg(theme.Text)) },
			"wide":      func(r Region, x int) { r.Text(x, 1, "漢", Fg(theme.Text)) },
			"wide tail": func(r Region, x int) { r.Text(x-1, 1, "漢", Fg(theme.Text)) },
			"other":     func(r Region, x int) { r.Put(x, 1, 'h', Fg(theme.Text).WithLink(mustURL(t, "https", "b.example.com"))) },
		}
		for name, cover := range covers {
			for at := range n {
				c := linkedCanvas(u, width, left)
				cover(c.Region(), left+at)
				if got := requireWholeLinks(t, c); got != 0 {
					t.Fatalf("width %d %s over cell %d keeps %d links", width, name, at, got)
				}
			}
		}
	}
}

func TestNeighboursDoNotRemoveTheLink(t *testing.T) {
	u := longLink(t)
	n := len(u.String())
	for name, touch := range map[string]func(r Region){
		"before":      func(r Region) { r.Put(9, 1, 'Z', Fg(theme.Text)) },
		"after":       func(r Region) { r.Put(10+n, 1, 'Z', Fg(theme.Text)) },
		"wide before": func(r Region) { r.Text(8, 1, "漢", Fg(theme.Text)) },
		"wide after":  func(r Region) { r.Text(10+n, 1, "漢", Fg(theme.Text)) },
		"row above":   func(r Region) { r.Sub(0, 0, 200, 1).Fill(theme.BgPanel) },
		"background":  func(r Region) { r.Sub(0, 1, 200, 1).FillBg(theme.BgPanel) },
	} {
		c := linkedCanvas(u, 200, 10)
		touch(c.Region())
		if requireWholeLinks(t, c) != 1 {
			t.Fatalf("%s removed an intact link", name)
		}
	}
}

func TestClippedLinksAreRemoved(t *testing.T) {
	u := longLink(t)
	n := len(u.String())
	for _, tc := range []struct {
		name string
		w, x int
	}{{"right edge", n + 3, 4}, {"one cell short", n - 1, 0}, {"left edge", n + 10, -1}, {"mostly out", 30, 10}, {"outside", 20, 40}} {
		if got := requireWholeLinks(t, linkedCanvas(u, tc.w, tc.x)); got != 0 {
			t.Fatalf("%s keeps %d links", tc.name, got)
		}
	}
	c := NewCanvas(200, 1, theme.BgApp)
	c.Region().Sub(5, 0, n-1, 1).Text(0, 0, u.String(), Fg(theme.Text).WithLink(u))
	if requireWholeLinks(t, c) != 0 {
		t.Fatal("a region narrower than the URL keeps the link")
	}
}

func TestInvisibleOrRepeatedLabelsAreNotLinks(t *testing.T) {
	u := longLink(t)
	c := linkedCanvas(u, 200, 10)
	c.Fade(theme.BgApp, 1)
	if requireWholeLinks(t, c) != 0 {
		t.Fatal("a fully faded URL is invisible and not clickable")
	}
	c = NewCanvas(300, 1, theme.BgApp)
	n := c.Region().Text(0, 0, u.String(), Fg(theme.Text).WithLink(u))
	c.Region().Text(n, 0, u.String(), Fg(theme.Text).WithLink(u))
	if requireWholeLinks(t, c) != 0 {
		t.Fatal("glued labels are ambiguous")
	}
	c = NewCanvas(200, 1, theme.BgApp)
	c.Region().Text(0, 0, "http://trusted.example.com", Fg(theme.Text).WithLink(u))
	if requireWholeLinks(t, c) != 0 {
		t.Fatal("a label that is not the destination is never a link")
	}
	c = NewCanvas(200, 1, theme.BgApp)
	c.Region().Text(0, 0, strings.Replace(u.String(), "e", "e\u0301", 1), Fg(theme.Text).WithLink(u))
	if requireWholeLinks(t, c) != 0 {
		t.Fatal("a combining mark changes the visible label")
	}
}

func TestRandomCompositionNeverLeavesAPartialLink(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	urls := []weburl.URL{longLink(t), mustURL(t, "https", "a.example.com"), mustURL(t, "http", "b.example.com")}
	kept, dropped := 0, 0
	for range 4000 {
		w, h := 20+rng.IntN(200), 1+rng.IntN(6)
		c := NewCanvas(w, h, theme.BgApp)
		r := c.Region()
		placed := 1 + rng.IntN(5)
		for range placed {
			u := urls[rng.IntN(len(urls))]
			r.Text(rng.IntN(w)-5, rng.IntN(h), u.String(), Fg(theme.Text).WithLink(u))
		}
		for range rng.IntN(6) {
			x, y := rng.IntN(w), rng.IntN(h)
			switch rng.IntN(5) {
			case 0:
				r.Sub(x, y, rng.IntN(40), rng.IntN(4)).Fill(theme.BgPanel)
			case 1:
				r.Text(x-1, y, "漢字", Fg(theme.Text))
			case 2:
				r.Put(x, y, '│', Fg(theme.Text))
			case 3:
				r.Sub(x, y, rng.IntN(40), 2).FillBg(theme.BgPanel)
			case 4:
				r.Text(x, y, "http://trusted.example.com", Fg(theme.Text))
			}
		}
		got := requireWholeLinks(t, c)
		kept += got
		dropped += placed - got
	}
	if kept == 0 || dropped <= 0 {
		t.Fatalf("the generator must exercise both outcomes: kept %d dropped %d", kept, dropped)
	}
}

package draw

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/weburl"
)

func mustURL(t *testing.T, scheme, host string) weburl.URL {
	t.Helper()
	port := 443
	if scheme == "http" {
		port = 80
	}
	u, err := weburl.New(scheme, host, port, "/")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestLinkIsOpenedAndClosedAroundItsCells(t *testing.T) {
	u := mustURL(t, "https", "a.example.com")
	c := NewCanvas(30, 2, theme.BgApp)
	r := c.Region()
	r.Text(0, 0, "x ", Fg(theme.Text))
	r.Text(2, 0, "link", Fg(theme.Text).WithLink(u))
	r.Text(6, 0, " y", Fg(theme.Text))
	out := c.String()
	if strings.Count(out, "\x1b]8;;https://a.example.com\x1b\\") != 1 || strings.Count(out, "\x1b]8;;\x1b\\") != 1 {
		t.Fatalf("one open and one close expected: %q", out)
	}
	open, end := strings.Index(out, "\x1b]8;;h"), strings.LastIndex(out, "\x1b]8;;\x1b\\")
	if !strings.Contains(out[open:end], "link") || strings.Contains(out[open:end], "y") {
		t.Fatal("only the styled cells belong to the link")
	}
	if strings.Contains(c.Plain(), "\x1b") {
		t.Fatal("plain output has no escapes")
	}
}

func TestLinkClosesAtTheEndOfTheRowAndBetweenLinks(t *testing.T) {
	a, b := mustURL(t, "https", "a.example.com"), mustURL(t, "http", "b.example.com")
	c := NewCanvas(4, 2, theme.BgApp)
	c.Region().Text(0, 0, "ab", Fg(theme.Text).WithLink(a))
	c.Region().Text(2, 0, "cd", Fg(theme.Text).WithLink(b))
	rows := strings.Split(c.String(), "\n")
	if strings.Count(rows[0], "\x1b]8;;http") != 2 || !strings.HasSuffix(rows[0], "\x1b]8;;\x1b\\\x1b[0m") {
		t.Fatalf("row = %q", rows[0])
	}
	if strings.Contains(rows[1], "\x1b]8") {
		t.Fatal("a link never leaks into the next row")
	}
}

func TestOverwritingCellsDropsTheLink(t *testing.T) {
	c := NewCanvas(8, 1, theme.BgApp)
	c.Region().Text(0, 0, "link", Fg(theme.Text).WithLink(mustURL(t, "https", "a.example.com")))
	c.Region().Text(0, 0, "text", Fg(theme.Text))
	if strings.Contains(c.String(), "\x1b]8") {
		t.Fatal("overwritten cells keep no link")
	}
	c.Region().Text(0, 0, "link", Fg(theme.Text).WithLink(mustURL(t, "https", "a.example.com")))
	c.Region().Fill(theme.BgApp)
	if strings.Contains(c.String(), "\x1b]8") {
		t.Fatal("filled cells keep no link")
	}
}

func TestClusterTextNeverInjectsLinks(t *testing.T) {
	c := NewCanvas(80, 1, theme.BgApp)
	c.Region().Text(0, 0, "\x1b]8;;http://evil\x1b\\x\x1b]8;;\x1b\\ \x1b]52;c;eA==\x07 \u009d8;;http://evil\u009c", Fg(theme.Text))
	out := c.String()
	if strings.Contains(out, "\x1b]") || strings.Contains(out, "\u009d") || strings.Contains(out, "\x07") {
		t.Fatalf("escapes must not survive: %q", out)
	}
}

func TestClusterAddressIsNeverALink(t *testing.T) {
	u := mustURL(t, "http", "web.apps.svc")
	c := NewCanvas(20, 1, theme.BgApp)
	c.Region().Text(0, 0, "web", Fg(theme.Text).WithLink(u))
	c.Region().Text(4, 0, "zero", Fg(theme.Text).WithLink(weburl.URL{}))
	if strings.Contains(c.String(), "\x1b]8") {
		t.Fatal("cluster-only addresses are not clickable")
	}
}

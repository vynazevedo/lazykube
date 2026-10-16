package app

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/TNK-Studio/lazykube/internal/anim"
	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

var update = flag.Bool("update", false, "update golden files")

const (
	goldenT     = 40
	goldenPhase = 0.25
)

func demoModel(t testing.TB, w, h int) Model {
	t.Helper()
	m := New(Options{
		Source:     data.NewDemo(),
		Palette:    theme.DefaultPalette(),
		InitialPod: 1,
		Now:        func() time.Time { return time.Unix(0, 0) },
	})
	m.clock = anim.Fixed(goldenT, goldenPhase, 30)
	m.animated = false
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

func golden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s, run go test -update: %v", path, err)
	}
	if !bytes.Equal(want, []byte(got)) {
		t.Fatalf("%s differs from golden; run go test ./internal/app -update and review the diff", name)
	}
}

func TestScreenGolden(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{
		{"screen_160x48", 160, 48},
		{"screen_130x40", 130, 40},
		{"screen_too_small", 100, 30},
	} {
		t.Run(size.name, func(t *testing.T) {
			c := demoModel(t, size.w, size.h).Render()
			golden(t, size.name+".txt", c.Plain())
			golden(t, size.name+".ansi", c.String())
		})
	}
}

func TestEveryTabRenders(t *testing.T) {
	m := demoModel(t, 160, 48)
	for _, k := range []string{"l", "c", "t", "d", "e"} {
		next, _ := m.Update(keyPress(k))
		got := next.(Model).Render().Plain()
		golden(t, "tab_"+k+".txt", got)
	}
}

func TestCanvasCoversScreen(t *testing.T) {
	c := demoModel(t, 160, 48).Render()
	lines := strings.Split(c.Plain(), "\n")
	if len(lines) != 48 {
		t.Fatalf("expected 48 lines, got %d", len(lines))
	}
	for i, l := range lines {
		if n := len([]rune(l)); n != 160 {
			t.Fatalf("line %d has %d cells", i, n)
		}
	}
}

func BenchmarkView(b *testing.B) {
	m := demoModel(b, 160, 48)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

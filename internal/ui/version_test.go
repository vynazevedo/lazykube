package ui

import (
	"runtime/debug"
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/anim"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

const revision = "6f9b0fd0123456789abcdef0123456789abcdef0"

func buildInfo(module string, settings ...string) *debug.BuildInfo {
	info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/vynazevedo/lazykube", Version: module}}
	for i := 0; i+1 < len(settings); i += 2 {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
	}
	return info
}

func TestVersionPrecedence(t *testing.T) {
	clean := []string{"vcs.revision", revision, "vcs.modified", "false"}
	dirty := []string{"vcs.revision", revision, "vcs.modified", "true"}
	for name, tc := range map[string]struct {
		linked string
		info   *debug.BuildInfo
		want   string
	}{
		"release ldflags win over the module":   {"v0.9.0", buildInfo("v0.8.0", clean...), "v0.9.0"},
		"release ldflags win over a dirty tree": {"v0.9.0", buildInfo("(devel)", dirty...), "v0.9.0"},
		"snapshot ldflags":                      {"0.9.0-SNAPSHOT-6f9b0fd", nil, "0.9.0-SNAPSHOT-6f9b0fd"},
		"ldflags without build info":            {"v0.9.0", nil, "v0.9.0"},
		"hostile ldflags are sanitized":         {"v0.9.0\x1b]52;c;x\x07\n\u202e", nil, "v0.9.052cx"},
		"ldflags with nothing usable":           {"\x1b\n ", buildInfo("v0.9.0"), "v0.9.0"},
		"long ldflags are limited":              {strings.Repeat("9", 500), nil, strings.Repeat("9", versionLimit)},
		"go install of a tag":                   {"", buildInfo("v0.9.0"), "v0.9.0"},
		"go install of a prerelease":            {"", buildInfo("v0.9.0-rc.1"), "v0.9.0-rc.1"},
		"go build on a clean tag":               {"", buildInfo("v0.9.0", clean...), "v0.9.0"},
		"go build on a modified tag":            {"", buildInfo("v0.9.0+dirty", dirty...), "dev-6f9b0fd-dirty"},
		"modified tag without the mark":         {"", buildInfo("v0.9.0", dirty...), "dev-6f9b0fd-dirty"},
		"dirty mark without vcs":                {"", buildInfo("v0.9.0+dirty"), "dev-dirty"},
		"pseudo version without a tag":          {"", buildInfo("v0.0.0-20261125170624-5bb8a1886f66"), "dev-5bb8a18"},
		"pseudo version after a tag":            {"", buildInfo("v0.9.1-0.20261125170624-5bb8a1886f66"), "dev-5bb8a18"},
		"pseudo version after a prerelease":     {"", buildInfo("v0.9.0-rc.1.0.20261125170624-5bb8a1886f66"), "dev-5bb8a18"},
		"pseudo version with vcs":               {"", buildInfo("v0.0.0-20261125170624-5bb8a1886f66", clean...), "dev-6f9b0fd"},
		"pseudo version of a dirty tree":        {"", buildInfo("v0.0.0-20261125170624-5bb8a1886f66+dirty", dirty...), "dev-6f9b0fd-dirty"},
		"devel without vcs":                     {"", buildInfo("(devel)"), "dev"},
		"devel with vcs":                        {"", buildInfo("(devel)", clean...), "dev-6f9b0fd"},
		"devel with a dirty tree":               {"", buildInfo("(devel)", dirty...), "dev-6f9b0fd-dirty"},
		"empty module":                          {"", buildInfo(""), "dev"},
		"no build info":                         {"", nil, "dev"},
		"unknown module version":                {"", buildInfo("latest"), "dev"},
		"incompatible build metadata":           {"", buildInfo("v2.0.0+incompatible"), "dev"},
		"hostile module version":                {"", buildInfo("v0.9.0-\x1b[31m"), "dev"},
		"long module version":                   {"", buildInfo("v0.9.0-" + strings.Repeat("a", 200)), "dev"},
		"hostile revision":                      {"", buildInfo("(devel)", "vcs.revision", "\x1b[31m6f9b0fd"), "dev"},
		"short revision":                        {"", buildInfo("(devel)", "vcs.revision", "6f9b"), "dev"},
	} {
		if got := resolveVersion(tc.linked, tc.info); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestHeaderVersionIsBounded(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })
	st := HeaderState{ReadOnly: true, Protected: true, Synthetic: true, MetricsOK: true, Cluster: data.Cluster{Context: "producao", Namespace: "apps"}}
	for _, v := range []string{"dev", "v0.9.0", "dev-6f9b0fd-dirty", strings.Repeat("9", versionLimit)} {
		Version = v
		for _, w := range []int{80, 99, 100, 120, 130, 160} {
			c := draw.NewCanvas(w, 1, theme.BgApp)
			Header(c.Region(), st, Ctx{Pal: theme.DefaultPalette(), Clock: anim.Fixed(0, 0, 0)})
			plain := c.Plain()
			if draw.Width(headerVersion(w)) > versionHeaderMax+4 {
				t.Fatalf("%q takes %d columns", v, draw.Width(headerVersion(w)))
			}
			if shown := strings.Contains(plain, v); len(v) <= versionHeaderMax && (shown && w < headerVersionFrom || !shown && w >= 130) {
				t.Fatalf("%q at %d columns: shown %v", v, w, shown)
			}
			if !strings.Contains(plain, "producao") || !strings.Contains(plain, "apps") {
				t.Fatalf("%q at %d columns hides the target: %q", v, w, plain)
			}
			if !strings.Contains(plain, "live") || !strings.Contains(plain, "[PROTECTED]") {
				t.Fatalf("%q at %d columns hides the state: %q", v, w, plain)
			}
			if g := HeaderLayout(st, w); g.End > g.Right {
				t.Fatalf("%q at %d columns overlaps the pill: %+v", v, w, g)
			}
		}
	}
}

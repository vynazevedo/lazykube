package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/app"
	"github.com/vynazevedo/lazykube/internal/ui"
)

var update = flag.Bool("update", false, "update golden files")

var errParse = errors.New("parse failed")

func parseFlags(args []string, out io.Writer) (flags, error) {
	var got flags
	called := false
	code := execute(context.Background(), args, out, out, func(f flags) int {
		got, called = f, true
		return 0
	})
	if code != 0 || !called {
		return got, errParse
	}
	return got, nil
}

func defaults() flags {
	return flags{palette: "charm", fps: app.DefaultFPS}
}

func TestFlagTable(t *testing.T) {
	with := func(change func(*flags)) flags {
		f := defaults()
		change(&f)
		return f
	}
	for _, tc := range []struct {
		name string
		args []string
		want flags
	}{
		{"defaults", nil, defaults()},
		{"read-write", []string{"--read-write"}, with(func(f *flags) { f.readWrite = true })},
		{"read-write false", []string{"--read-write=false"}, defaults()},
		{"protected", []string{"--protected-contexts", "prod,staging"}, with(func(f *flags) { f.protectedContexts = "prod,staging" })},
		{"read-only", []string{"--read-only-contexts=qa"}, with(func(f *flags) { f.readOnlyContexts = "qa" })},
		{"audit", []string{"--audit-file", "/tmp/a.jsonl"}, with(func(f *flags) { f.auditPath = "/tmp/a.jsonl" })},
		{"demo", []string{"--demo"}, with(func(f *flags) { f.demo = true })},
		{"context", []string{"--context", "dev"}, with(func(f *flags) { f.context = "dev" })},
		{"kubeconfig", []string{"--kubeconfig=/k"}, with(func(f *flags) { f.kubeconfig = "/k" })},
		{"namespace", []string{"--namespace", "apps"}, with(func(f *flags) { f.namespace = "apps" })},
		{"namespace shorthand", []string{"-n", "apps"}, with(func(f *flags) { f.namespace = "apps" })},
		{"namespace shorthand equals", []string{"-n=apps"}, with(func(f *flags) { f.namespace = "apps" })},
		{"palette", []string{"--palette", "ocean"}, with(func(f *flags) { f.palette = "ocean" })},
		{"no-anim", []string{"--no-anim"}, with(func(f *flags) { f.noAnim = true })},
		{"fps", []string{"--fps", "60"}, with(func(f *flags) { f.fps = 60 })},
		{"single dash long flags", []string{"-demo", "-fps=12", "-context", "dev"}, with(func(f *flags) { f.demo, f.fps, f.context = true, 12, "dev" })},
		{"last namespace wins", []string{"-n", "a", "--namespace", "b"}, with(func(f *flags) { f.namespace = "b" })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFlags(tc.args, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestInvalidInputExitsWithUsageCode(t *testing.T) {
	for _, tc := range []struct {
		args []string
		msg  string
	}{
		{[]string{"--fps", "0"}, "--fps must be between 1 and 60"},
		{[]string{"--fps", "61"}, "--fps must be between 1 and 60"},
		{[]string{"--fps", "x"}, "invalid argument"},
		{[]string{"--palette", "nope"}, "nope"},
		{[]string{"--nope"}, "unknown flag: --nope"},
		{[]string{"-x"}, "unknown shorthand flag"},
		{[]string{"--read-only=false"}, "unknown flag: --read-only"},
		{[]string{"extra"}, "unknown command \"extra\""},
		{[]string{"--demo", "false"}, "unknown command \"false\""},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			started := false
			code := execute(context.Background(), tc.args, &stdout, &stderr, func(flags) int {
				started = true
				return 0
			})
			if code != exitUsage || started {
				t.Fatalf("code=%d started=%v", code, started)
			}
			if !strings.Contains(stderr.String(), tc.msg) {
				t.Fatalf("stderr %q does not mention %q", stderr.String(), tc.msg)
			}
		})
	}
}

func TestHelpAndVersionDoNotStart(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"-help"}, {"help"}, {"--version"}, {"-v"}} {
		var stdout, stderr bytes.Buffer
		started := false
		code := execute(context.Background(), args, &stdout, &stderr, func(flags) int {
			started = true
			return 0
		})
		if code != 0 || started || stdout.Len() == 0 {
			t.Fatalf("%v: code=%d started=%v stdout=%q stderr=%q", args, code, started, stdout.String(), stderr.String())
		}
	}
}

func TestVersionCarriesBuildCommit(t *testing.T) {
	prevVersion, prevCommit := ui.Version, commit
	t.Cleanup(func() { ui.Version, commit = prevVersion, prevCommit })
	ui.Version, commit = "v1.2.3", "0123456789abcdef"
	var stdout bytes.Buffer
	if code := execute(context.Background(), []string{"--version"}, &stdout, io.Discard, func(flags) int { return 1 }); code != 0 {
		t.Fatal(code)
	}
	if got := stdout.String(); got != "lazykube version v1.2.3 (0123456)\n" {
		t.Fatalf("version output %q", got)
	}
}

func TestStartExitCodePropagates(t *testing.T) {
	if code := execute(context.Background(), []string{"--demo"}, io.Discard, io.Discard, func(flags) int { return app.ExitCancelled }); code != app.ExitCancelled {
		t.Fatal(code)
	}
}

func TestCompletionsAndManPage(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		var stdout bytes.Buffer
		code := execute(context.Background(), []string{"completion", shell}, &stdout, io.Discard, func(flags) int { return 1 })
		if code != 0 || !strings.Contains(stdout.String(), "lazykube") {
			t.Fatalf("%s completion: code=%d len=%d", shell, code, stdout.Len())
		}
	}
	var stdout bytes.Buffer
	code := execute(context.Background(), []string{"__complete", "--pal"}, &stdout, io.Discard, func(flags) int { return 1 })
	if code != 0 || !strings.Contains(stdout.String(), "--palette") {
		t.Fatalf("flag completion: code=%d out=%q", code, stdout.String())
	}
}

func TestHelpGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var stdout bytes.Buffer
	if code := execute(context.Background(), []string{"--help"}, &stdout, io.Discard, func(flags) int { return 1 }); code != 0 {
		t.Fatal(code)
	}
	got := stdout.String()
	if strings.Contains(got, "\x1b[") {
		t.Fatal("NO_COLOR help must be plain text")
	}
	path := filepath.Join("testdata", "help.golden")
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
	if string(want) != got {
		t.Fatalf("help differs from golden; run go test ./cmd/lazykube -update and review the diff\n%s", got)
	}
}

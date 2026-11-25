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
	"github.com/vynazevedo/lazykube/internal/theme"
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
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Fatalf("help differs from golden; run go test ./cmd/lazykube -update and review the diff\n%s", got)
	}
}

func TestNormalizeArgsKeepsFlagValues(t *testing.T) {
	cmd := newCommand(&flags{})
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"-demo", "-fps=12", "-context", "dev"}, []string{"--demo", "--fps=12", "--context", "dev"}},
		{[]string{"-fps", "12"}, []string{"--fps", "12"}},
		{[]string{"--context", "-demo"}, []string{"--context", "-demo"}},
		{[]string{"-context", "-demo"}, []string{"--context", "-demo"}},
		{[]string{"--context", "-fps=12"}, []string{"--context", "-fps=12"}},
		{[]string{"--context=-demo", "-demo"}, []string{"--context=-demo", "--demo"}},
		{[]string{"--kubeconfig", "-demo"}, []string{"--kubeconfig", "-demo"}},
		{[]string{"-kubeconfig", "-no-anim"}, []string{"--kubeconfig", "-no-anim"}},
		{[]string{"--namespace", "-demo"}, []string{"--namespace", "-demo"}},
		{[]string{"-n", "-demo"}, []string{"-n", "-demo"}},
		{[]string{"-n", "-fps=12", "-demo"}, []string{"-n", "-fps=12", "--demo"}},
		{[]string{"-n=-demo", "-demo"}, []string{"--namespace=-demo", "--demo"}},
		{[]string{"--palette", "-ocean"}, []string{"--palette", "-ocean"}},
		{[]string{"-palette", "-demo"}, []string{"--palette", "-demo"}},
		{[]string{"--audit-file", "-demo"}, []string{"--audit-file", "-demo"}},
		{[]string{"-audit-file", "-x.jsonl"}, []string{"--audit-file", "-x.jsonl"}},
		{[]string{"--protected-contexts", "-demo"}, []string{"--protected-contexts", "-demo"}},
		{[]string{"-protected-contexts", "-fps=12"}, []string{"--protected-contexts", "-fps=12"}},
		{[]string{"--read-only-contexts", "-demo"}, []string{"--read-only-contexts", "-demo"}},
		{[]string{"-read-only-contexts", "-context"}, []string{"--read-only-contexts", "-context"}},
		{[]string{"--fps", "-demo"}, []string{"--fps", "-demo"}},
		{[]string{"-fps", "-12"}, []string{"--fps", "-12"}},
		{[]string{"--demo", "-no-anim"}, []string{"--demo", "--no-anim"}},
		{[]string{"-read-write", "-demo"}, []string{"--read-write", "--demo"}},
		{[]string{"-demo", "--", "-fps=12", "-context"}, []string{"--demo", "--", "-fps=12", "-context"}},
		{[]string{"--context", "--", "-demo"}, []string{"--context", "--", "--demo"}},
		{[]string{"-x", "-demo"}, []string{"-x", "--demo"}},
		{[]string{"-nope", "-demo"}, []string{"-nope", "--demo"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			if got := normalizeArgs(cmd, tc.args); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestFlagValuesStartingWithDash(t *testing.T) {
	with := func(change func(*flags)) flags {
		f := defaults()
		change(&f)
		return f
	}
	for _, tc := range []struct {
		args []string
		want flags
	}{
		{[]string{"--context", "-demo"}, with(func(f *flags) { f.context = "-demo" })},
		{[]string{"-context", "-demo"}, with(func(f *flags) { f.context = "-demo" })},
		{[]string{"--context", "-fps=12"}, with(func(f *flags) { f.context = "-fps=12" })},
		{[]string{"-kubeconfig", "-demo"}, with(func(f *flags) { f.kubeconfig = "-demo" })},
		{[]string{"-n", "-demo", "-demo"}, with(func(f *flags) { f.namespace, f.demo = "-demo", true })},
		{[]string{"-namespace", "-fps=12"}, with(func(f *flags) { f.namespace = "-fps=12" })},
		{[]string{"-audit-file", "-demo"}, with(func(f *flags) { f.auditPath = "-demo" })},
		{[]string{"-protected-contexts", "-demo"}, with(func(f *flags) { f.protectedContexts = "-demo" })},
		{[]string{"-read-only-contexts", "-demo"}, with(func(f *flags) { f.readOnlyContexts = "-demo" })},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
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

var namespaceAliasCases = [][]string{
	{"--n", "default"},
	{"--n=default"},
	{"-n", "default"},
	{"-n=default"},
	{"--namespace", "x", "--n", "y"},
	{"--n", "y", "--namespace", "x"},
	{"-n", "a", "--n=b"},
	{"--context", "--n"},
	{"--context=dev", "--n", "-demo"},
	{"-palette", "ocean", "--n", "apps", "-demo"},
	{"--n="},
	{"-n="},
	{"--namespace", "apps", "--n="},
	{"--namespace", "apps", "-n="},
	{"--n=", "--namespace", "apps"},
}

func legacyParse(args []string) (flags, error) {
	f := defaults()
	fs := flag.NewFlagSet("lazykube", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&f.readWrite, "read-write", false, "")
	fs.StringVar(&f.protectedContexts, "protected-contexts", "", "")
	fs.StringVar(&f.readOnlyContexts, "read-only-contexts", "", "")
	fs.StringVar(&f.auditPath, "audit-file", "", "")
	fs.BoolVar(&f.demo, "demo", false, "")
	fs.StringVar(&f.context, "context", "", "")
	fs.StringVar(&f.kubeconfig, "kubeconfig", "", "")
	fs.StringVar(&f.namespace, "namespace", "", "")
	fs.StringVar(&f.namespace, "n", "", "")
	fs.StringVar(&f.palette, "palette", "charm", "")
	fs.BoolVar(&f.noAnim, "no-anim", false, "")
	fs.IntVar(&f.fps, "fps", app.DefaultFPS, "")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if fs.NArg() > 0 {
		return f, errParse
	}
	if f.fps < 1 || f.fps > app.MaxFPS {
		return f, errParse
	}
	if _, err := theme.PaletteByName(f.palette); err != nil {
		return f, err
	}
	return f, nil
}

func TestNamespaceLongAliasN(t *testing.T) {
	with := func(change func(*flags)) flags {
		f := defaults()
		change(&f)
		return f
	}
	for i, want := range []flags{
		with(func(f *flags) { f.namespace = "default" }),
		with(func(f *flags) { f.namespace = "default" }),
		with(func(f *flags) { f.namespace = "default" }),
		with(func(f *flags) { f.namespace = "default" }),
		with(func(f *flags) { f.namespace = "y" }),
		with(func(f *flags) { f.namespace = "x" }),
		with(func(f *flags) { f.namespace = "b" }),
		with(func(f *flags) { f.context = "--n" }),
		with(func(f *flags) { f.context, f.namespace, f.demo = "dev", "-demo", false }),
		with(func(f *flags) { f.palette, f.namespace, f.demo = "ocean", "apps", true }),
		defaults(),
		defaults(),
		defaults(),
		defaults(),
		with(func(f *flags) { f.namespace = "apps" }),
	} {
		args := namespaceAliasCases[i]
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			got, err := parseFlags(args, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v want %+v", got, want)
			}
		})
	}
}

func TestNamespaceAliasMatchesLegacyFlag(t *testing.T) {
	for _, args := range namespaceAliasCases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			want, wantErr := legacyParse(args)
			got, err := parseFlags(args, &bytes.Buffer{})
			if (err != nil) != (wantErr != nil) {
				t.Fatalf("err=%v legacy err=%v", err, wantErr)
			}
			if err == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v legacy %+v", got, want)
			}
		})
	}
}

func TestNamespaceAliasStaysOutOfHelpAndCompletions(t *testing.T) {
	var help bytes.Buffer
	if code := execute(context.Background(), []string{"--help"}, &help, io.Discard, func(flags) int { return 1 }); code != 0 {
		t.Fatal(code)
	}
	if strings.Contains(help.String(), "--n ") || strings.Contains(help.String(), "--n=") {
		t.Fatalf("help lists --n as a long flag:\n%s", help.String())
	}
	var comp bytes.Buffer
	if code := execute(context.Background(), []string{"__complete", "--"}, &comp, io.Discard, func(flags) int { return 1 }); code != 0 {
		t.Fatal(code)
	}
	for _, line := range strings.Split(comp.String(), "\n") {
		if name, _, _ := strings.Cut(line, "\t"); name == "--n" {
			t.Fatalf("completions offer --n:\n%s", comp.String())
		}
	}
	if !strings.Contains(comp.String(), "--namespace") {
		t.Fatalf("completions lost --namespace:\n%s", comp.String())
	}
}

func TestDoubleDashShorthandOnlyForValueFlags(t *testing.T) {
	cmd := newCommand(&flags{})
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"--n", "a"}, []string{"--namespace", "a"}},
		{[]string{"-n", "a"}, []string{"-n", "a"}},
		{[]string{"-na"}, []string{"-na"}},
		{[]string{"-n="}, []string{"--namespace="}},
		{[]string{"--n=a"}, []string{"--namespace=a"}},
		{[]string{"--n="}, []string{"--namespace="}},
		{[]string{"--h"}, []string{"--h"}},
		{[]string{"--v"}, []string{"--v"}},
		{[]string{"--x", "-demo"}, []string{"--x", "--demo"}},
		{[]string{"-demo", "--", "--n", "a"}, []string{"--demo", "--", "--n", "a"}},
	} {
		if got := normalizeArgs(cmd, tc.args); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: got %q want %q", tc.args, got, tc.want)
		}
	}
}

func TestShorthandNKeepsAttachedValue(t *testing.T) {
	for args, want := range map[string]string{"-n x": "x", "-nx": "x", "-n=x": "x", "--n=x": "x", "-n=": "", "--n=": ""} {
		got, err := parseFlags(strings.Fields(args), &bytes.Buffer{})
		if err != nil || got.namespace != want {
			t.Fatalf("%q: namespace=%q err=%v want %q", args, got.namespace, err, want)
		}
	}
}

func TestNamespaceAliasMatchesLegacyGrid(t *testing.T) {
	values := []string{"default", "", "--n", "--n=x", "-n", "-n=", "--n=", "--", "-", "-demo", "-fps=12", "help", "man"}
	tails := [][]string{nil, {"--n=last"}, {"--n="}, {"-n="}, {"-n", "last", "--namespace", "last2"}, {"--namespace", "apps", "--n="}, {"-demo=false"}, {"--fps=0"}, {"--palette=nope"}}
	count := 0
	for _, prefix := range []string{"--n", "-n", "--namespace", "-namespace", "--context", "-context", "--audit-file", "--kubeconfig"} {
		for _, value := range values {
			for _, tail := range tails {
				args := append([]string{prefix, value}, tail...)
				want, wantErr := legacyParse(args)
				got, err := parseFlags(args, &bytes.Buffer{})
				if (err != nil) != (wantErr != nil) || (err == nil && !reflect.DeepEqual(got, want)) {
					t.Errorf("%q: got %+v err=%v legacy %+v err=%v", args, got, err, want, wantErr)
				}
				count++
			}
		}
	}
	for _, args := range [][]string{{"--n="}, {"-n="}, {"--n=="}, {"--n"}, {"--n", ""}, {"--namespace="}, {"--namespace", "apps", "--n="}, {"--namespace", "apps", "-n="}, {"--", "--n", "x"}} {
		want, wantErr := legacyParse(args)
		got, err := parseFlags(args, &bytes.Buffer{})
		if (err != nil) != (wantErr != nil) || (err == nil && !reflect.DeepEqual(got, want)) {
			t.Errorf("%q: got %+v err=%v legacy %+v err=%v", args, got, err, want, wantErr)
		}
		count++
	}
	if count < 900 {
		t.Fatalf("grid shrank to %d cases", count)
	}
}

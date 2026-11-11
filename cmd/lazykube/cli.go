package main

import (
	"context"
	"fmt"
	"image/color"
	"io"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/vynazevedo/lazykube/internal/app"
	"github.com/vynazevedo/lazykube/internal/theme"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const exitUsage = 2

var commit = ""

const examples = `lazykube --demo
lazykube --context staging -n apps
lazykube --read-write --protected-contexts prod
lazykube completion zsh > "${fpath[1]}/_lazykube"
lazykube man > lazykube.1`

func newCommand(f *flags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "lazykube",
		Short:   "Kubernetes cluster dashboard in the terminal",
		Long:    "lazykube shows pods, workloads, nodes, events and security posture of a Kubernetes cluster in the terminal.\nIt is read-only by default; --read-write enables actions for this process, subject to context policy.",
		Example: examples,
		Args:    cobra.NoArgs,
		Version: ui.Version,
	}
	fs := cmd.Flags()
	fs.SortFlags = false
	fs.BoolVar(&f.readWrite, "read-write", false, "enable actions for this process, subject to context policy")
	fs.StringVar(&f.protectedContexts, "protected-contexts", "", "context names treated as protected, comma-separated")
	fs.StringVar(&f.readOnlyContexts, "read-only-contexts", "", "context names forced to read-only, comma-separated")
	fs.StringVar(&f.auditPath, "audit-file", "", "private local audit JSONL path")
	fs.BoolVar(&f.demo, "demo", false, "run with deterministic synthetic data")
	fs.StringVar(&f.context, "context", "", "kubeconfig context to use")
	fs.StringVar(&f.kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
	fs.StringVarP(&f.namespace, "namespace", "n", "", "limit the view to one namespace (default: all)")
	fs.StringVar(&f.palette, "palette", "charm", "accent palette: charm, ocean or matrix")
	fs.BoolVar(&f.noAnim, "no-anim", false, "disable animations")
	fs.IntVar(&f.fps, "fps", app.DefaultFPS, "animation frames per second (max 60)")
	cmd.AddCommand(manCommand())
	cmd.InitDefaultHelpFlag()
	cmd.InitDefaultVersionFlag()
	return cmd
}

func validate(f flags) error {
	if f.fps < 1 || f.fps > app.MaxFPS {
		return fmt.Errorf("--fps must be between 1 and %d", app.MaxFPS)
	}
	if _, err := theme.PaletteByName(f.palette); err != nil {
		return err
	}
	return nil
}

func normalizeArgs(cmd *cobra.Command, args []string) []string {
	fs := cmd.Flags()
	out := make([]string, 0, len(args))
	value := false
	for i, a := range args {
		switch {
		case value:
			value = false
		case a == "--":
			return append(out, args[i:]...)
		case strings.HasPrefix(a, "--"):
			name, _, inline := strings.Cut(a[2:], "=")
			value = !inline && takesValue(fs.Lookup(name))
		case len(a) > 1 && a[0] == '-':
			name, _, inline := strings.Cut(a[1:], "=")
			if f := fs.Lookup(name); len(name) > 1 && f != nil {
				a = "-" + a
				value = !inline && takesValue(f)
			} else {
				value = len(a) == 2 && takesValue(fs.ShorthandLookup(a[1:2]))
			}
		}
		out = append(out, a)
	}
	return out
}

func takesValue(f *pflag.Flag) bool {
	return f != nil && f.NoOptDefVal == ""
}

func helpColors(c lipgloss.LightDarkFunc) fang.ColorScheme {
	cs := fang.DefaultColorScheme(c)
	pal := theme.DefaultPalette()
	dark := func(light color.Color, dark theme.RGB) color.Color { return c(light, dark) }
	cs.Title = dark(cs.Title, pal.A)
	cs.Program = dark(cs.Program, pal.C)
	cs.Command = dark(cs.Command, pal.B)
	cs.Flag = dark(cs.Flag, pal.B)
	cs.Base = dark(cs.Base, theme.TextSoft)
	cs.Description = dark(cs.Description, theme.TextSoft)
	cs.Argument = dark(cs.Argument, theme.TextSoft)
	cs.Comment = dark(cs.Comment, theme.Muted)
	cs.DimmedArgument = dark(cs.DimmedArgument, theme.Muted)
	cs.FlagDefault = dark(cs.FlagDefault, theme.Faint)
	cs.Codeblock = dark(cs.Codeblock, theme.BgCard)
	cs.QuotedString = dark(cs.QuotedString, theme.Warn)
	cs.ErrorHeader = [2]color.Color{theme.BgApp, theme.Err}
	return cs
}

func printError(w io.Writer, styles fang.Styles, err error) {
	text := styles.ErrorText.UnsetTransform().UnsetWidth()
	_, _ = fmt.Fprintln(w, styles.ErrorHeader.String())
	_, _ = fmt.Fprintln(w, text.Render("lazykube: "+err.Error()))
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, text.Render("Try lazykube --help for usage."))
	_, _ = fmt.Fprintln(w)
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer, start func(flags) int) int {
	var f flags
	code := 0
	cmd := newCommand(&f)
	cmd.RunE = func(*cobra.Command, []string) error {
		if err := validate(f); err != nil {
			return err
		}
		code = start(f)
		return nil
	}
	cmd.SetArgs(normalizeArgs(cmd, args))
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if err := fang.Execute(ctx, cmd, fang.WithVersion(ui.Version), fang.WithCommit(commit), fang.WithColorSchemeFunc(helpColors), fang.WithErrorHandler(printError), fang.WithoutManpage()); err != nil {
		return exitUsage
	}
	return code
}

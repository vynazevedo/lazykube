package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/TNK-Studio/lazykube/internal/app"
	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

var errRealModeUnavailable = errors.New("real cluster mode is not available yet in this build; run with --demo")

type flags struct {
	demo       bool
	context    string
	kubeconfig string
	palette    string
	noAnim     bool
	fps        int
}

func parseFlags(args []string, out io.Writer) (flags, error) {
	var f flags
	fs := flag.NewFlagSet("lazykube", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&f.demo, "demo", false, "run with deterministic synthetic data")
	fs.StringVar(&f.context, "context", "", "kubeconfig context to use")
	fs.StringVar(&f.kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
	fs.StringVar(&f.palette, "palette", "charm", "accent palette: charm, ocean or matrix")
	fs.BoolVar(&f.noAnim, "no-anim", false, "disable animations")
	fs.IntVar(&f.fps, "fps", app.DefaultFPS, "animation frames per second (max 60)")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if f.fps < 1 || f.fps > app.MaxFPS {
		return f, fmt.Errorf("--fps must be between 1 and %d", app.MaxFPS)
	}
	if _, err := theme.PaletteByName(f.palette); err != nil {
		return f, err
	}
	return f, nil
}

func buildOptions(f flags) (app.Options, error) {
	if !f.demo {
		return app.Options{}, errRealModeUnavailable
	}
	pal, err := theme.PaletteByName(f.palette)
	if err != nil {
		return app.Options{}, err
	}
	return app.Options{
		Source:     data.NewDemo(),
		Palette:    pal,
		FPS:        f.fps,
		NoAnim:     f.noAnim,
		InitialPod: 1,
	}, nil
}

func run(args []string, stderr io.Writer) int {
	f, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	opts, err := buildOptions(f)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "lazykube:", err)
		return 2
	}
	if _, err := tea.NewProgram(app.New(opts)).Run(); err != nil {
		_, _ = fmt.Fprintln(stderr, "lazykube:", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

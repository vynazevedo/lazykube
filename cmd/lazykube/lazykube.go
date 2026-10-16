package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/TNK-Studio/lazykube/internal/app"
	"github.com/TNK-Studio/lazykube/internal/data"
	"github.com/TNK-Studio/lazykube/internal/kube"
	"github.com/TNK-Studio/lazykube/internal/theme"
)

type flags struct {
	demo       bool
	context    string
	kubeconfig string
	namespace  string
	palette    string
	noAnim     bool
	fps        int
}

type connector func(ctx context.Context, opts kube.ConnectOptions) (data.Source, func(), error)

func parseFlags(args []string, out io.Writer) (flags, error) {
	var f flags
	fs := flag.NewFlagSet("lazykube", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&f.demo, "demo", false, "run with deterministic synthetic data")
	fs.StringVar(&f.context, "context", "", "kubeconfig context to use")
	fs.StringVar(&f.kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
	fs.StringVar(&f.namespace, "namespace", "", "limit the view to one namespace (default: all)")
	fs.StringVar(&f.namespace, "n", "", "shorthand for --namespace")
	fs.StringVar(&f.palette, "palette", "charm", "accent palette: charm, ocean or matrix")
	fs.BoolVar(&f.noAnim, "no-anim", false, "disable animations")
	fs.IntVar(&f.fps, "fps", app.DefaultFPS, "animation frames per second (max 60)")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if fs.NArg() > 0 {
		return f, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if f.fps < 1 || f.fps > app.MaxFPS {
		return f, fmt.Errorf("--fps must be between 1 and %d", app.MaxFPS)
	}
	if _, err := theme.PaletteByName(f.palette); err != nil {
		return f, err
	}
	return f, nil
}

func connectCluster(ctx context.Context, opts kube.ConnectOptions) (data.Source, func(), error) {
	kube.SilenceClientLogs()
	src, err := kube.Connect(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	return src, src.Close, nil
}

func baseOptions(f flags) (app.Options, error) {
	pal, err := theme.PaletteByName(f.palette)
	if err != nil {
		return app.Options{}, err
	}
	return app.Options{Palette: pal, FPS: f.fps, NoAnim: f.noAnim}, nil
}

func clusterConnector(f flags, connect connector) app.Connector {
	return func(ctx context.Context, report func(data.Progress)) (data.Source, func(), error) {
		return connect(ctx, kube.ConnectOptions{
			Kubeconfig: f.kubeconfig,
			Context:    f.context,
			Namespace:  f.namespace,
			Progress:   report,
		})
	}
}

func newModel(f flags, connect connector) (tea.Model, func(), error) {
	opts, err := baseOptions(f)
	if err != nil {
		return nil, nil, err
	}
	if f.demo {
		opts.Source, opts.InitialPod = data.NewDemo(), 1
		return app.New(opts), func() {}, nil
	}
	l := app.NewLoader(app.LoaderOptions{Options: opts, Context: f.context, Connect: clusterConnector(f, connect)})
	return l, l.Shutdown, nil
}

func finish(final tea.Model, stderr io.Writer) int {
	if l, ok := final.(app.Loader); ok {
		if stage, msg, failed := l.Failure(); failed {
			_, _ = fmt.Fprintf(stderr, "lazykube: %s: %s\n", stage, msg)
		}
	}
	return app.ExitCode(final)
}

func run(args []string, stderr io.Writer, connect connector, progOpts ...tea.ProgramOption) int {
	f, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	model, shutdown, err := newModel(f, connect)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "lazykube:", err)
		return 1
	}
	defer shutdown()
	final, err := tea.NewProgram(model, progOpts...).Run()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "lazykube:", err)
		return 1
	}
	return finish(final, stderr)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr, connectCluster))
}

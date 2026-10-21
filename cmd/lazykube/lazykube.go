package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/app"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/kube"
	"github.com/vynazevedo/lazykube/internal/theme"
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

var quietStderr = silenceStderr

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

func clusterConnector(kubeconfig string, t data.Target, connect connector) app.Connector {
	return func(ctx context.Context, report func(data.Progress)) (data.Source, func(), error) {
		return connect(ctx, kube.ConnectOptions{
			Kubeconfig: kubeconfig,
			Context:    t.Context,
			Namespace:  t.Namespace,
			Progress:   report,
		})
	}
}

func demoConnector(t data.Target) app.Connector {
	return func(ctx context.Context, report func(data.Progress)) (data.Source, func(), error) {
		if _, ok := data.DemoContexts().Find(t.Context); !ok {
			return nil, nil, data.NewPublicError(fmt.Sprintf("contexto sintético %q desconhecido", t.Context))
		}
		for _, s := range data.Stages() {
			report(data.Progress{Stage: s, Done: true, Context: t.Context})
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		return data.NewDemoScoped(t.Context, t.Namespace), func() {}, nil
	}
}

func newModel(f flags, connect connector) (tea.Model, func(), error) {
	opts, err := baseOptions(f)
	if err != nil {
		return nil, nil, err
	}
	if f.demo {
		opts.Source, opts.InitialPod = data.NewDemo(), 1
		r := app.NewRoot(app.RootOptions{
			Options: opts,
			Connect: demoConnector,
			Catalog: func() (data.ContextCatalog, error) { return data.DemoContexts(), nil },
		})
		return r, r.Shutdown, nil
	}
	kubeconfig := f.kubeconfig
	r := app.NewRoot(app.RootOptions{
		Options: opts,
		Initial: data.Target{Context: f.context, Namespace: f.namespace},
		Connect: func(t data.Target) app.Connector { return clusterConnector(kubeconfig, t, connect) },
		Catalog: func() (data.ContextCatalog, error) { return kube.Contexts(kubeconfig) },
	})
	return r, r.Shutdown, nil
}

func finish(final tea.Model, stderr io.Writer) int {
	if stage, msg, failed := app.Failure(final); failed {
		_, _ = fmt.Fprintf(stderr, "lazykube: %s: %s\n", stage, msg)
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
	final, err := func() (tea.Model, error) {
		restore := quietStderr()
		defer restore()
		defer shutdown()
		return tea.NewProgram(model, progOpts...).Run()
	}()
	if errors.Is(err, tea.ErrInterrupted) {
		return app.ExitCancelled
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "lazykube:", err)
		return 1
	}
	return finish(final, stderr)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr, connectCluster))
}

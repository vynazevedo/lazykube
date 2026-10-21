package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/app"
	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/kube"
	"github.com/vynazevedo/lazykube/internal/theme"
)

type flags struct {
	readWrite         bool
	protectedContexts string
	readOnlyContexts  string
	auditPath         string
	demo              bool
	context           string
	kubeconfig        string
	namespace         string
	palette           string
	noAnim            bool
	fps               int
}

var (
	quietStderr  = silenceStderr
	buildModel   = newModel
	errIsolation = errors.New("não foi possível isolar o stderr dos plugins de autenticação")
)

type connector func(ctx context.Context, opts kube.ConnectOptions) (data.Source, func(), error)

func parseFlags(args []string, out io.Writer) (flags, error) {
	var f flags
	fs := flag.NewFlagSet("lazykube", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&f.readWrite, "read-write", false, "enable actions for this process, subject to context policy")
	fs.StringVar(&f.protectedContexts, "protected-contexts", "", "comma-separated protected context names")
	fs.StringVar(&f.readOnlyContexts, "read-only-contexts", "", "comma-separated read-only context names")
	fs.StringVar(&f.auditPath, "audit-file", "", "private local audit JSONL path")
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

func listed(csv, name string) bool {
	for _, item := range strings.Split(csv, ",") {
		if strings.TrimSpace(item) == name {
			return true
		}
	}
	return false
}

func environmentFor(f flags) func(string) (guard.Environment, error) {
	return func(name string) (guard.Environment, error) {
		env := guard.DefaultEnvironment(name)
		env.Protected = listed(f.protectedContexts, name)
		env.ReadOnly = listed(f.readOnlyContexts, name)
		return env, nil
	}
}

func clusterConnector(f flags, t data.Target, connect connector) app.Connector {
	return func(ctx context.Context, report func(data.Progress)) (data.Source, func(), error) {
		return connect(ctx, kube.ConnectOptions{
			AllowWrites:        f.readWrite,
			AuditPath:          f.auditPath,
			ResolveEnvironment: environmentFor(f),
			Kubeconfig:         f.kubeconfig,
			Context:            t.Context,
			Namespace:          t.Namespace,
			Progress:           report,
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
	r := app.NewRoot(app.RootOptions{
		Options: opts,
		Initial: data.Target{Context: f.context, Namespace: f.namespace},
		Connect: func(t data.Target) app.Connector { return clusterConnector(f, t, connect) },
		Catalog: func() (data.ContextCatalog, error) { return kube.Contexts(f.kubeconfig) },
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
	model, shutdown, err := buildModel(f, connect)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "lazykube:", err)
		return 1
	}
	box := &crashBox{}
	final, err := func() (tea.Model, error) {
		restore, err := quietStderr()
		if err != nil {
			shutdown()
			return nil, err
		}
		defer restore()
		defer shutdown()
		p := tea.NewProgram(guarded{inner: model, box: box}, progOpts...)
		box.mu.Lock()
		box.quit = p.Quit
		box.mu.Unlock()
		return p.Run()
	}()
	if box.write(stderr) {
		return app.ExitFailure
	}
	final = unwrapGuarded(final)
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

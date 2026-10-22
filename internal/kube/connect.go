package kube

import (
	"context"
	"fmt"
	"github.com/vynazevedo/lazykube/internal/guard"
	"io"
	"k8s.io/client-go/dynamic"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/vynazevedo/lazykube/internal/data"
)

const (
	clientQPS   = 20
	clientBurst = 40
	userAgent   = "lazykube"
)

type ConnectOptions struct {
	AllowWrites        bool
	ResolveEnvironment func(string) (guard.Environment, error)
	AuditPath          string
	Kubeconfig         string
	Context            string
	Namespace          string
	Progress           func(data.Progress)
}

func SilenceClientLogs() {
	klog.SetLogger(logr.Discard())
	klog.SetOutput(io.Discard)
	klog.LogToStderr(false)
}

func ResolveContext(opts ConnectOptions) (string, error) {
	name, _, err := resolveContext(opts)
	return name, err
}

func resolveContext(opts ConnectOptions) (string, int, error) {
	raw, err := loadingRules(opts.Kubeconfig).Load()
	if err != nil {
		return "", 0, connectError(ErrorKubeconfig, opts.Context, err)
	}
	name := opts.Context
	if name == "" {
		name = raw.CurrentContext
	}
	if name == "" {
		return "", 0, connectError(ErrorNoCurrentContext, "", nil)
	}
	if _, ok := raw.Contexts[name]; !ok {
		return "", 0, connectError(ErrorContextMissing, name, nil)
	}
	return name, len(raw.Contexts), nil
}

func report(fn func(data.Progress), p data.Progress) {
	if fn != nil {
		fn(p)
	}
}

func Connect(ctx context.Context, opts ConnectOptions) (*Source, error) {
	report(opts.Progress, data.Progress{Stage: data.StageKubeconfig, Context: opts.Context})
	name, contexts, err := resolveContext(opts)
	if err != nil {
		return nil, err
	}
	report(opts.Progress, data.Progress{Stage: data.StageKubeconfig, Done: true, Context: name, Detail: fmt.Sprintf("%d contextos", contexts)})
	report(opts.Progress, data.Progress{Stage: data.StageConnect, Context: name})
	if err := ctx.Err(); err != nil {
		return nil, publicError(err, name, false)
	}
	cfg, err := restConfig(opts, name)
	if err != nil {
		return nil, connectError(ErrorClientConfig, name, err)
	}
	exec := cfg.ExecProvider != nil
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, connectError(ErrorClientConfig, name, err)
	}
	mc, err := metricsclient.NewForConfig(cfg)
	if err != nil {
		return nil, connectError(ErrorClientConfig, name, err)
	}
	timed := rest.CopyConfig(cfg)
	timed.Timeout = defaultTimeout
	dc, err := discovery.NewDiscoveryClientForConfig(timed)
	if err != nil {
		return nil, connectError(ErrorClientConfig, name, err)
	}
	s := newSource(client, mc, Options{Context: name, Namespace: opts.Namespace, Progress: opts.Progress, DisableNetwork: true})
	s.version = dc
	environment := guard.DefaultEnvironment(name)
	if opts.ResolveEnvironment != nil {
		var resolveErr error
		environment, resolveErr = opts.ResolveEnvironment(name)
		if resolveErr != nil {
			environment.Valid = false
		}
	}
	s.guardSession = guard.Session{Context: name, Namespace: opts.Namespace, Identity: fmt.Sprintf("session-%d", s.generation), Generation: s.generation, Environment: environment}
	s.guardPolicy = guard.NewPolicy(opts.AllowWrites)
	auditPath := opts.AuditPath
	if auditPath == "" {
		auditPath, _ = guard.DefaultAuditPath()
	}
	actionClient, clientErr := dynamic.NewForConfig(cfg)
	if clientErr != nil {
		s.Close()
		return nil, connectError(ErrorGuard, name, clientErr)
	}
	s.actions, err = NewActionExecutor(s.ctx, s.guardSession, opts.AllowWrites, client, actionClient, guard.NewFileAudit(auditPath), guard.ClockFunc(time.Now), nil)
	if err != nil {
		s.Close()
		return nil, connectError(ErrorGuard, name, err)
	}
	resourceClient, err := newResourceClient(cfg)
	if err != nil {
		s.Close()
		return nil, err
	}
	catalogClient, err := newResourceDiscovery(cfg)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.initResources(resourceClient, catalogClient)
	if err := s.start(ctx); err != nil {
		s.Close()
		return nil, publicError(err, name, exec)
	}
	return s, nil
}

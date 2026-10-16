package kube

import (
	"context"
	"fmt"
	"io"

	"github.com/go-logr/logr"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/TNK-Studio/lazykube/internal/data"
)

const (
	clientQPS   = 20
	clientBurst = 40
	userAgent   = "lazykube"
)

type ConnectOptions struct {
	Kubeconfig string
	Context    string
	Namespace  string
	Progress   func(data.Progress)
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
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = opts.Kubeconfig
	raw, err := rules.Load()
	if err != nil {
		return "", 0, fmt.Errorf("cannot load kubeconfig: %w", err)
	}
	name := opts.Context
	if name == "" {
		name = raw.CurrentContext
	}
	if name == "" {
		return "", 0, fmt.Errorf("no current context in kubeconfig; pass --context")
	}
	if _, ok := raw.Contexts[name]; !ok {
		return "", 0, fmt.Errorf("context %q not found in kubeconfig", name)
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
		return nil, err
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = opts.Kubeconfig
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: name})
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("invalid client config for context %q: %w", name, err)
	}
	cfg.QPS, cfg.Burst, cfg.UserAgent = clientQPS, clientBurst, userAgent
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	mc, err := metricsclient.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	timed := rest.CopyConfig(cfg)
	timed.Timeout = defaultTimeout
	dc, err := discovery.NewDiscoveryClientForConfig(timed)
	if err != nil {
		return nil, err
	}
	s := newSource(client, mc, Options{Context: name, Namespace: opts.Namespace, Progress: opts.Progress})
	s.version = dc
	if err := s.start(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("context %q: %w", name, err)
	}
	return s, nil
}

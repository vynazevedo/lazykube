package kube

import (
	"errors"
	"sort"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/vynazevedo/lazykube/internal/data"
)

const (
	problemCluster = "cluster ausente no kubeconfig"
	problemUser    = "usuário ausente no kubeconfig"
)

var errKubeconfig = errors.New("kubeconfig ilegível ou inválido")

func loadingRules(kubeconfig string) *clientcmd.ClientConfigLoadingRules {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = kubeconfig
	rules.MigrationRules = nil
	return rules
}

func Contexts(kubeconfig string) (data.ContextCatalog, error) {
	raw, err := loadingRules(kubeconfig).Load()
	if err != nil {
		return data.ContextCatalog{}, errKubeconfig
	}
	out := data.ContextCatalog{Current: raw.CurrentContext}
	for name, c := range raw.Contexts {
		if c == nil {
			continue
		}
		k := data.KubeContext{Name: name, Cluster: c.Cluster, User: c.AuthInfo, Namespace: c.Namespace, Current: name == raw.CurrentContext}
		if _, ok := raw.Clusters[c.Cluster]; !ok {
			k.Problem = problemCluster
		} else if _, ok := raw.AuthInfos[c.AuthInfo]; !ok && c.AuthInfo != "" {
			k.Problem = problemUser
		}
		out.Contexts = append(out.Contexts, k)
	}
	sort.Slice(out.Contexts, func(i, j int) bool { return out.Contexts[i].Name < out.Contexts[j].Name })
	return out, nil
}

func restConfig(opts ConnectOptions, name string) (*rest.Config, error) {
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules(opts.Kubeconfig), &clientcmd.ConfigOverrides{CurrentContext: name})
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, connectError(ErrorClientConfig, name, err)
	}
	cfg.QPS, cfg.Burst, cfg.UserAgent = clientQPS, clientBurst, userAgent
	return cfg, nil
}

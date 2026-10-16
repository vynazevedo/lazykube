package kubecli

import (
	"flag"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/vynazevedo/lazykube/pkg/kubecli/clusterinfo"
	"github.com/vynazevedo/lazykube/pkg/kubecli/config"
	"github.com/vynazevedo/lazykube/pkg/log"
	"io"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	_ "k8s.io/client-go/plugin/pkg/client/auth/exec"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/klog/v2"
	"k8s.io/kubectl/pkg/cmd/util"
)

var Cli *KubeCLI

func init() {
	Cli = NewKubeCLI()
	disableKlog()
}

type KubeCLI struct {
	factory   util.Factory
	namespace *string
	context   *string
}

type Cmd struct {
	cmd     *cobra.Command
	args    []string
	streams genericiooptions.IOStreams
}

func NewCmd(cmd *cobra.Command, args []string, streams genericiooptions.IOStreams) *Cmd {
	return &Cmd{
		cmd:     cmd,
		args:    args,
		streams: streams,
	}
}

func (c *Cmd) Run() {
	util.BehaviorOnFatal(func(s string, i int) {
		_, _ = fmt.Fprint(c.streams.ErrOut, s)
	})
	c.cmd.Run(c.cmd, c.args)
}

func (c *Cmd) SetFlag(name, value string) *Cmd {
	if err := c.cmd.Flags().Set(name, value); err != nil {
		log.Logger.Panicln(err)
	}
	return c
}

func NewKubeCLI() *KubeCLI {
	namespace := config.ContextNamespace()
	context := config.CurrentContext()
	kubeConfigFlags := genericclioptions.NewConfigFlags(true).WithDeprecatedPasswordFlag()
	kubeConfigFlags.Namespace = &namespace
	kubeConfigFlags.Context = &context

	matchVersionKubeConfigFlags := util.NewMatchVersionFlags(kubeConfigFlags)

	k := &KubeCLI{
		factory:   util.NewFactory(matchVersionKubeConfigFlags),
		namespace: &namespace,
		context:   &context,
	}
	return k
}

func (cli *KubeCLI) SetNamespace(namespace string) {
	kubeConfigFlags := genericclioptions.NewConfigFlags(true).WithDeprecatedPasswordFlag()
	kubeConfigFlags.Namespace = &namespace
	kubeConfigFlags.Context = cli.context

	matchVersionKubeConfigFlags := util.NewMatchVersionFlags(kubeConfigFlags)
	cli.factory = util.NewFactory(matchVersionKubeConfigFlags)
	cli.namespace = &namespace
}

func (cli *KubeCLI) SetCurrentContext(context string) {
	config.SetCurrentContext(context)
	kubeConfigFlags := genericclioptions.NewConfigFlags(true).WithDeprecatedPasswordFlag()
	kubeConfigFlags.Namespace = cli.namespace
	kubeConfigFlags.Context = &context

	matchVersionKubeConfigFlags := util.NewMatchVersionFlags(kubeConfigFlags)
	cli.factory = util.NewFactory(matchVersionKubeConfigFlags)
	cli.context = &context
}

func (cli *KubeCLI) WithNamespace(namespace string) *KubeCLI {
	kubeConfigFlags := genericclioptions.NewConfigFlags(true).WithDeprecatedPasswordFlag()
	kubeConfigFlags.Namespace = &namespace
	kubeConfigFlags.Context = cli.context

	matchVersionKubeConfigFlags := util.NewMatchVersionFlags(kubeConfigFlags)

	k := &KubeCLI{
		factory:   util.NewFactory(matchVersionKubeConfigFlags),
		namespace: &namespace,
	}
	return k
}

func (cli *KubeCLI) Namespace() string {
	return *cli.namespace
}

func (cli *KubeCLI) CurrentContext() string {
	return config.CurrentContext()
}

func (cli *KubeCLI) ListContexts() []string {
	return config.ListContexts()
}

func (cli *KubeCLI) ClusterInfo() (string, error) {
	return clusterinfo.ClusterInfo(cli.factory)
}

func disableKlog() {
	flagSet := &flag.FlagSet{}
	klog.InitFlags(flagSet)
	_ = flagSet.Set("logtostderr", "false")
	klog.SetOutput(io.Discard)
	klog.SetLogger(logr.New(NewKLogSink(log.Logger)))
}

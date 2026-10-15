package kubecli

import (
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/kubectl/pkg/cmd/run"
)

const kubectlCommandName = "kubectl"

func (cli *KubeCLI) Run(streams genericiooptions.IOStreams, args ...string) *Cmd {
	parent := &cobra.Command{Use: kubectlCommandName}
	cmd := run.NewCmdRun(cli.factory, streams)
	parent.AddCommand(cmd)
	return NewCmd(cmd, args, streams)
}

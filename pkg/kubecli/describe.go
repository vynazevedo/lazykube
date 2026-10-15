package kubecli

import (
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/kubectl/pkg/cmd/describe"
)

func (cli *KubeCLI) Describe(streams genericiooptions.IOStreams, args ...string) *Cmd {
	cmd := describe.NewCmdDescribe("kubectl", cli.factory, streams)
	return NewCmd(cmd, args, streams)
}

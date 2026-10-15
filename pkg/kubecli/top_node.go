package kubecli

import (
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/kubectl/pkg/cmd/top"
)

func (cli *KubeCLI) TopNode(streams genericiooptions.IOStreams, o *top.TopNodeOptions, args ...string) *Cmd {
	cmd := top.NewCmdTopNode(cli.factory, o, streams)
	return NewCmd(cmd, args, streams)
}

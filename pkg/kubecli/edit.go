package kubecli

import (
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/kubectl/pkg/cmd/edit"
)

func (cli *KubeCLI) Edit(streams genericiooptions.IOStreams, args ...string) *Cmd {
	cmd := edit.NewCmdEdit(cli.factory, streams)
	return NewCmd(cmd, args, streams)
}

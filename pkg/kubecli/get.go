package kubecli

import (
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/kubectl/pkg/cmd/get"
)

func (cli *KubeCLI) Get(streams genericiooptions.IOStreams, args ...string) *Cmd {
	cmd := get.NewCmdGet("kubectl", cli.factory, streams)
	return NewCmd(cmd, args, streams)
}

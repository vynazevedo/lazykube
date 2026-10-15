package kubecli

import (
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/kubectl/pkg/cmd/logs"
)

func (cli *KubeCLI) Logs(streams genericiooptions.IOStreams, args ...string) *Cmd {
	cmd := logs.NewCmdLogs(cli.factory, streams)
	return NewCmd(cmd, args, streams)
}

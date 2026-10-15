package kubecli

import (
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/kubectl/pkg/cmd/rollout"
)

func (cli *KubeCLI) RolloutRestart(streams genericiooptions.IOStreams, args ...string) *Cmd {
	cmd := rollout.NewCmdRolloutRestart(cli.factory, streams)
	return NewCmd(cmd, args, streams)
}

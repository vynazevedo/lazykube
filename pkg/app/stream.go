package app

import (
	"bytes"
	"github.com/TNK-Studio/lazykube/pkg/log"
	"io"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"os"
	"strings"
)

func newStream() genericiooptions.IOStreams {
	return genericiooptions.IOStreams{
		In:     &bytes.Buffer{},
		Out:    &bytes.Buffer{},
		ErrOut: &bytes.Buffer{},
	}
}

func newStdStream() genericiooptions.IOStreams {
	return genericiooptions.IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}
}

func streamCopyTo(streams genericiooptions.IOStreams, writer io.Writer) {
	if _, err := io.Copy(writer, (streams.Out).(io.Reader)); err != nil {
		log.Logger.Warningf("streamCopyTo - streams.Out copy error %s", err)
	}
	if _, err := io.Copy(writer, (streams.ErrOut).(io.Reader)); err != nil {
		log.Logger.Warningf("streamCopyTo - streams.ErrOut copy error %s", err)
	}
}

func streamToString(streams genericiooptions.IOStreams) string {
	buf := new(strings.Builder)
	streamCopyTo(streams, buf)
	return buf.String()
}

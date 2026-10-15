package kubecli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
)

func newTestSinkLogger() (*logrus.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := logrus.New()
	logger.SetOutput(buf)
	logger.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true, DisableColors: true})
	return logger, buf
}

func TestKLogSinkWritesFieldsAndName(t *testing.T) {
	logger, buf := newTestSinkLogger()
	log := logr.New(NewKLogSink(logger)).WithName("client").WithValues("cluster", "kind")

	log.Info("connected", "attempt", 2)

	out := buf.String()
	for _, want := range []string{"connected", "cluster=kind", "attempt=2", "logger=client"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q does not contain %q", out, want)
		}
	}
}

func TestKLogSinkError(t *testing.T) {
	logger, buf := newTestSinkLogger()
	logr.New(NewKLogSink(logger)).Error(errors.New("boom"), "failed")

	out := buf.String()
	if !strings.Contains(out, "level=error") || !strings.Contains(out, "boom") {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestKLogSinkWithValuesDoesNotMutateParent(t *testing.T) {
	logger, _ := newTestSinkLogger()
	parent := NewKLogSink(logger)
	child := parent.WithValues("a", 1)

	if len(parent.values) != 0 {
		t.Fatalf("parent values mutated: %v", parent.values)
	}
	if got := child.(*KLogSink).values; len(got) != 2 {
		t.Fatalf("child values = %v", got)
	}
}

func TestKeyValuesToFieldsIgnoresInvalidPairs(t *testing.T) {
	fields := keyValuesToFields([]any{"ok", 1, 2, "skipped", "dangling"})
	if len(fields) != 1 || fields["ok"] != 1 {
		t.Fatalf("unexpected fields %v", fields)
	}
}

const syntheticSecretResponse = `{"kind":"Secret","apiVersion":"v1","metadata":{"name":"db"},"data":{"password":"c3VwZXItc2VjcmV0"}}`

func TestKLogSinkDropsVerboseResponseBodies(t *testing.T) {
	logger, buf := newTestSinkLogger()
	log := logr.New(NewKLogSink(logger))

	log.V(8).Info("Response Body", "body", syntheticSecretResponse)
	log.V(10).Info("curl -v -XGET -H \"Authorization: Bearer abc\" https://cluster/api/v1/secrets")

	if buf.Len() != 0 {
		t.Fatalf("verbose logs must be dropped, got %q", buf.String())
	}
}

func TestKLogSinkRedactsSensitiveFieldsAtDefaultLevel(t *testing.T) {
	logger, buf := newTestSinkLogger()
	log := logr.New(NewKLogSink(logger)).WithValues("Authorization", "Bearer abc")

	log.Info("request", "body", syntheticSecretResponse, "verb", "GET", "token", "abc")

	out := buf.String()
	for _, leaked := range []string{"c3VwZXItc2VjcmV0", "Bearer abc", "token=abc"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("output leaked %q: %q", leaked, out)
		}
	}
	if !strings.Contains(out, "verb=GET") || !strings.Contains(out, redactedValue) {
		t.Fatalf("expected safe fields and redaction marker, got %q", out)
	}
}

func TestKLogSinkRedactsBodyMessagesEvenWhenVerbose(t *testing.T) {
	logger, buf := newTestSinkLogger()
	log := logr.New(NewKLogSinkWithVerbosity(logger, 10))

	log.V(8).Info("Response Body: " + syntheticSecretResponse)
	log.V(4).Info("GET https://cluster/api 200 OK")

	out := buf.String()
	if strings.Contains(out, "c3VwZXItc2VjcmV0") {
		t.Fatalf("body leaked: %q", out)
	}
	if !strings.Contains(out, "200 OK") {
		t.Fatalf("enabled verbose line missing: %q", out)
	}
}

func TestKLogSinkEnabled(t *testing.T) {
	logger, _ := newTestSinkLogger()
	sink := NewKLogSink(logger)
	if !sink.Enabled(0) || sink.Enabled(1) {
		t.Fatal("default sink must enable only level 0")
	}
	if NewKLogSink(nil).Enabled(0) {
		t.Fatal("sink without logger must be disabled")
	}
}

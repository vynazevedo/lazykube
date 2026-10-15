package kubecli

import (
	"strings"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
)

const (
	DefaultKLogVerbosity = 0
	redactedValue        = "[REDACTED]"
)

var sensitiveKeyFragments = []string{
	"authorization",
	"token",
	"password",
	"secret",
	"credential",
	"cookie",
	"body",
	"data",
	"cert",
	"key",
}

var sensitiveMessagePrefixes = []string{
	"response body",
	"request body",
	"curl -",
}

type KLogSink struct {
	logger       *logrus.Logger
	name         string
	values       []any
	maxVerbosity int
}

var _ logr.LogSink = (*KLogSink)(nil)

func NewKLogSink(logger *logrus.Logger) *KLogSink {
	return NewKLogSinkWithVerbosity(logger, DefaultKLogVerbosity)
}

func NewKLogSinkWithVerbosity(logger *logrus.Logger, maxVerbosity int) *KLogSink {
	return &KLogSink{logger: logger, maxVerbosity: maxVerbosity}
}

func (s *KLogSink) Init(logr.RuntimeInfo) {}

func (s *KLogSink) Enabled(level int) bool {
	return s.logger != nil && level <= s.maxVerbosity
}

func (s *KLogSink) Info(level int, msg string, keysAndValues ...any) {
	if !s.Enabled(level) {
		return
	}
	s.entry(keysAndValues).Info(redactMessage(msg))
}

func (s *KLogSink) Error(err error, msg string, keysAndValues ...any) {
	if s.logger == nil {
		return
	}
	s.entry(keysAndValues).WithError(err).Error(redactMessage(msg))
}

func (s *KLogSink) WithValues(keysAndValues ...any) logr.LogSink {
	values := make([]any, 0, len(s.values)+len(keysAndValues))
	values = append(values, s.values...)
	values = append(values, keysAndValues...)
	return &KLogSink{logger: s.logger, name: s.name, values: values, maxVerbosity: s.maxVerbosity}
}

func (s *KLogSink) WithName(name string) logr.LogSink {
	fullName := name
	if s.name != "" {
		fullName = s.name + "/" + name
	}
	return &KLogSink{logger: s.logger, name: fullName, values: s.values, maxVerbosity: s.maxVerbosity}
}

func (s *KLogSink) entry(keysAndValues []any) *logrus.Entry {
	fields := keyValuesToFields(append(append([]any{}, s.values...), keysAndValues...))
	if s.name != "" {
		fields["logger"] = s.name
	}
	return s.logger.WithFields(fields)
}

func keyValuesToFields(keysAndValues []any) logrus.Fields {
	fields := make(logrus.Fields, len(keysAndValues)/2)
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		key, ok := keysAndValues[i].(string)
		if !ok {
			continue
		}
		if isSensitiveKey(key) {
			fields[key] = redactedValue
			continue
		}
		fields[key] = keysAndValues[i+1]
	}
	return fields
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	for _, fragment := range sensitiveKeyFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func redactMessage(msg string) string {
	normalized := strings.ToLower(strings.TrimSpace(msg))
	for _, prefix := range sensitiveMessagePrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return redactedValue
		}
	}
	return msg
}

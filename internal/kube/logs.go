package kube

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TNK-Studio/lazykube/internal/data"
)

const (
	logTail       = 500
	logRetry      = 3 * time.Second
	maxLogLine    = 64 * 1024
	maxLogMessage = 4 * 1024
	logBudget     = 2 << 20
	logStreamBuf  = 64 * 1024
	truncatedMark = " [linha truncada]"
)

type logStream struct {
	key    string
	uid    string
	cancel context.CancelFunc
	lines  []data.LogLine
	bytes  int
	lastTS time.Time
	atLast int
	err    string
}

func (s *Source) Logs(key string, _ int, n int) []data.LogLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.ensureStreamLocked(key)
	if st == nil || n <= 0 {
		return nil
	}
	if len(st.lines) == 0 && st.err != "" {
		return []data.LogLine{{Time: "--:--:--", Level: data.LevelWarn, Message: st.err}}
	}
	lines := st.lines
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return append([]data.LogLine(nil), lines...)
}

func (s *Source) ensureStreamLocked(key string) *logStream {
	p := s.byKey[key]
	if p == nil {
		s.stopStreamLocked()
		return nil
	}
	if st := s.stream; st != nil && st.key == key && st.uid == string(p.UID) {
		return st
	}
	s.stopStreamLocked()
	if s.client == nil || s.ctx.Err() != nil || len(p.Spec.Containers) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(s.ctx)
	st := &logStream{key: key, uid: string(p.UID), cancel: cancel}
	s.stream = st
	s.wg.Add(1)
	go s.follow(ctx, st, p.Namespace, p.Name, p.Spec.Containers[0].Name)
	return st
}

func (s *Source) stopStreamLocked() {
	if s.stream != nil {
		s.stream.cancel()
		s.stream = nil
	}
}

func (s *Source) follow(ctx context.Context, st *logStream, namespace, name, container string) {
	defer s.wg.Done()
	for {
		err := s.readLogs(ctx, st, namespace, name, container)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.mu.Lock()
			st.err = "logs unavailable: " + sanitize(err.Error())
			s.mu.Unlock()
			s.notify()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(logRetry):
		}
	}
}

func (s *Source) readLogs(ctx context.Context, st *logStream, namespace, name, container string) error {
	opts := &corev1.PodLogOptions{Container: container, Follow: true, Timestamps: true}
	s.mu.RLock()
	since, skipEqual := st.lastTS, st.atLast
	s.mu.RUnlock()
	if since.IsZero() {
		tail := int64(logTail)
		opts.TailLines = &tail
	} else {
		t := metav1.NewTime(since.Truncate(time.Second))
		opts.SinceTime = &t
	}
	rc, err := s.client.CoreV1().Pods(namespace).GetLogs(name, opts).Stream(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	return s.consumeLogs(ctx, st, rc, since, skipEqual)
}

func (s *Source) consumeLogs(ctx context.Context, st *logStream, r io.Reader, since time.Time, skipEqual int) error {
	br := bufio.NewReaderSize(r, logStreamBuf)
	overlap := !since.IsZero()
	for {
		raw, truncated, err := readLogLine(br, maxLogLine)
		if len(raw) > 0 || err == nil {
			line, ts := parseLogLine(string(raw), s.opts.Location)
			if overlap && !ts.IsZero() {
				switch {
				case ts.Before(since):
					continue
				case ts.Equal(since) && skipEqual > 0:
					skipEqual--
					continue
				default:
					overlap = false
				}
			}
			line.Message = clampMessage(line.Message, truncated)
			if !s.appendLog(ctx, st, line, ts) {
				return nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (s *Source) appendLog(ctx context.Context, st *logStream, line data.LogLine, ts time.Time) bool {
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return false
	}
	switch {
	case ts.IsZero():
	case ts.Equal(st.lastTS):
		st.atLast++
	case ts.After(st.lastTS):
		st.lastTS, st.atLast = ts, 1
	}
	st.err = ""
	st.lines = append(st.lines, line)
	st.bytes += len(line.Message)
	st.trim()
	s.mu.Unlock()
	s.notify()
	return true
}

func (st *logStream) trim() {
	drop := 0
	if len(st.lines) > 2*logTail {
		drop = len(st.lines) - logTail
	}
	size := st.bytes
	for i := 0; i < drop; i++ {
		size -= len(st.lines[i].Message)
	}
	for drop < len(st.lines)-1 && size > logBudget {
		size -= len(st.lines[drop].Message)
		drop++
	}
	if drop == 0 {
		return
	}
	st.lines = append(st.lines[:0:0], st.lines[drop:]...)
	st.bytes = size
}

func readLogLine(br *bufio.Reader, limit int) ([]byte, bool, error) {
	var out []byte
	dropped := false
	for {
		chunk, err := br.ReadSlice('\n')
		body := bytes.TrimSuffix(chunk, []byte{'\n'})
		room := limit - len(out)
		if len(body) > room {
			body, dropped = body[:room], true
		}
		out = append(out, body...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimSuffix(out, []byte{'\r'}), dropped, err
	}
}

func clampMessage(msg string, truncated bool) string {
	if len(msg) > maxLogMessage {
		cut := maxLogMessage
		for cut > 0 && !utf8.RuneStart(msg[cut]) {
			cut--
		}
		msg, truncated = msg[:cut], true
	}
	if truncated {
		msg += truncatedMark
	}
	return msg
}

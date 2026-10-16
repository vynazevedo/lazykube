package kube

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
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

type streamKey struct {
	pod       string
	uid       string
	container string
	previous  bool
	instance  int
}

type logStream struct {
	key    streamKey
	ctx    context.Context
	cancel context.CancelFunc
	lines  []data.LogLine
	bytes  int
	lastTS time.Time
	atLast int
	err    string
	done   bool
}

func (s *Source) Logs(key string, tick int, n int) []data.LogLine {
	v := s.PodLogs(data.LogRequest{Pod: key}, tick, n)
	if len(v.Lines) == 0 && v.State == data.FeedUnavailable && v.Container != "" {
		return []data.LogLine{{Time: "--:--:--", Level: data.LevelWarn, Message: v.Reason}}
	}
	return v.Lines
}

func (s *Source) PodLogs(req data.LogRequest, _ int, n int) data.LogView {
	s.mu.Lock()
	defer s.mu.Unlock()
	view := data.LogView{Previous: req.Previous}
	p := s.byKey[req.Pod]
	if p == nil {
		s.stopStreamLocked()
		view.State, view.Reason = data.FeedUnavailable, "pod não encontrado"
		return view
	}
	name := req.Container
	if name == "" || !containerSpec(p, name) {
		name = defaultContainerName(p)
	}
	view.Container = name
	if reason := s.unavailableLocked(p, name, req.Previous); reason != "" {
		s.stopStreamLocked()
		view.State, view.Reason = data.FeedUnavailable, reason
		return view
	}
	st := s.ensureStreamLocked(p, streamKey{
		pod:       req.Pod,
		uid:       string(p.UID),
		container: name,
		previous:  req.Previous,
		instance:  instanceOf(p, name, req.Previous),
	})
	if st == nil {
		view.State, view.Reason = data.FeedUnavailable, "logs indisponíveis"
		return view
	}
	lines := st.lines
	if n <= 0 {
		lines = nil
	} else if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	view.Lines = append([]data.LogLine(nil), lines...)
	view.Reason = st.err
	switch {
	case len(view.Lines) == 0 && st.err != "":
		view.State = data.FeedUnavailable
	case len(view.Lines) == 0 && !st.done:
		view.State = data.FeedLoading
	}
	return view
}

func (s *Source) unavailableLocked(p *corev1.Pod, name string, previous bool) string {
	switch {
	case name == "":
		return "pod sem containers"
	case s.client == nil:
		return "logs indisponíveis"
	case previous && lastTermination(p, name) == nil:
		return "sem instância anterior do container " + name
	}
	if previous {
		return ""
	}
	for _, c := range toContainers(p, s.opts.Now(), s.opts.Location) {
		if c.Name == name && c.Phase == data.PhaseWaiting && c.Restarts == 0 && c.Last == nil {
			return "container " + name + " aguardando: " + reasonText(c.Reason)
		}
	}
	return ""
}

func reasonText(reason string) string {
	if reason == "" {
		return "sem status"
	}
	return reason
}

func instanceOf(p *corev1.Pod, name string, previous bool) int {
	if !previous {
		return 0
	}
	for _, list := range [][]corev1.ContainerStatus{p.Status.InitContainerStatuses, p.Status.ContainerStatuses, p.Status.EphemeralContainerStatuses} {
		for _, cs := range list {
			if cs.Name == name {
				return int(cs.RestartCount)
			}
		}
	}
	return 0
}

func (s *Source) ensureStreamLocked(p *corev1.Pod, key streamKey) *logStream {
	if st := s.stream; st != nil && st.key == key {
		return st
	}
	s.stopStreamLocked()
	if s.ctx.Err() != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(s.ctx)
	st := &logStream{key: key, ctx: ctx, cancel: cancel}
	s.stream = st
	s.wg.Add(1)
	if key.previous {
		go s.readPrevious(ctx, st, p.Namespace, p.Name, key.container)
	} else {
		go s.follow(ctx, st, p.Namespace, p.Name, key.container)
	}
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
			st.err = "logs unavailable: " + clip(err.Error())
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

func (s *Source) readPrevious(ctx context.Context, st *logStream, namespace, name, container string) {
	defer s.wg.Done()
	tail := int64(logTail)
	opts := &corev1.PodLogOptions{Container: container, Previous: true, Timestamps: true, TailLines: &tail}
	err := func() error {
		rc, err := s.client.CoreV1().Pods(namespace).GetLogs(name, opts).Stream(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		return s.consumeLogs(ctx, st, rc, time.Time{}, 0)
	}()
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	st.done = true
	if err != nil {
		st.err = "previous indisponível: " + clip(err.Error())
	}
	s.mu.Unlock()
	s.notify()
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
	s.mu.Lock()
	st.done = true
	s.mu.Unlock()
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
		return msg + truncatedMark
	}
	return strings.Clone(msg)
}

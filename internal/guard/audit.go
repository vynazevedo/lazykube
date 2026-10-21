package guard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode"
)

type AuditEvent struct {
	SchemaVersion int       `json:"schema_version"`
	Timestamp     time.Time `json:"timestamp"`
	OperationID   string    `json:"operation_id"`
	Phase         string    `json:"phase"`
	Action        Action    `json:"action"`
	Context       string    `json:"context"`
	Identity      string    `json:"identity"`
	Namespace     string    `json:"namespace"`
	Group         string    `json:"group"`
	Resource      string    `json:"resource"`
	Name          string    `json:"name"`
	UID           string    `json:"uid"`
	ReadOnly      bool      `json:"read_only"`
	Protected     bool      `json:"protected"`
	Outcome       Reason    `json:"outcome"`
}
type FileAudit struct {
	mu     sync.Mutex
	path   string
	limit  int64
	failed bool
	clock  Clock
}

func NewFileAudit(path string) *FileAudit {
	return &FileAudit{path: path, limit: 10 << 20, clock: ClockFunc(time.Now)}
}
func DefaultAuditPath() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", AuditFailed
		}
		state = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(state) {
		return "", AuditFailed
	}
	return filepath.Join(state, "lazykube", "audit.jsonl"), nil
}
func auditText(value string) string {
	out := make([]rune, 0, min(256, len(value)))
	for _, r := range value {
		if len(out) == 256 {
			break
		}
		if !unicode.IsControl(r) && !unicode.In(r, unicode.Cf) {
			out = append(out, r)
		}
	}
	return string(out)
}
func (a *FileAudit) Record(event AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed {
		return AuditFailed
	}
	if err := a.record(event); err != nil {
		a.failed = true
		return AuditFailed
	}
	return nil
}
func (a *FileAudit) record(event AuditEvent) (recordErr error) {
	if !filepath.IsAbs(a.path) {
		return AuditFailed
	}
	event.OperationID = auditText(event.OperationID)
	event.Phase = auditText(event.Phase)
	event.Outcome = Reason(auditText(string(event.Outcome)))
	event.Context = auditText(event.Context)
	event.Identity = auditText(event.Identity)
	event.Namespace = auditText(event.Namespace)
	event.Group = auditText(event.Group)
	event.Resource = auditText(event.Resource)
	event.Name = auditText(event.Name)
	event.UID = auditText(event.UID)
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	dir, name := filepath.Dir(a.path), filepath.Base(a.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !private(info, true) {
		return AuditFailed
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { recordErr = errors.Join(recordErr, root.Close()) }()
	current, err := root.Stat(".")
	if err != nil || !os.SameFile(info, current) {
		return AuditFailed
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { recordErr = errors.Join(recordErr, directory.Close()) }()
	if err = lockAudit(directory); err != nil {
		return err
	}
	if err = a.expire(root, name); err != nil {
		return err
	}
	if info, err = root.Lstat(name); err == nil {
		if !private(info, false) {
			return AuditFailed
		}
		if info.Size()+int64(len(raw)) > a.limit {
			if err = a.rotate(root, name); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY|auditNoFollow, 0600)
	if err != nil {
		return err
	}
	defer func() { recordErr = errors.Join(recordErr, file.Close()) }()
	info, err = file.Stat()
	if err != nil || !private(info, false) {
		return AuditFailed
	}
	if n, writeErr := file.Write(raw); writeErr != nil || n != len(raw) {
		return AuditFailed
	}
	if err = file.Sync(); err != nil {
		return err
	}
	return directory.Sync()
}
func (a *FileAudit) rotate(root *os.Root, name string) error {
	for i := 9; i >= 1; i-- {
		old := name
		if i > 1 {
			old = fmt.Sprintf("%s.%d", name, i-1)
		}
		next := fmt.Sprintf("%s.%d", name, i)
		for _, entry := range []string{old, next} {
			info, err := root.Lstat(entry)
			if err == nil && !private(info, false) {
				return AuditFailed
			}
			if err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := root.Rename(old, next); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (a *FileAudit) expire(root *os.Root, name string) error {
	cutoff := a.clock.Now().Add(-30 * 24 * time.Hour)
	for i := 0; i < 10; i++ {
		entry := name
		if i > 0 {
			entry = fmt.Sprintf("%s.%d", name, i)
		}
		info, err := root.Lstat(entry)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !private(info, false) {
			return AuditFailed
		}
		oldest := info.ModTime()
		file, openErr := root.OpenFile(entry, os.O_RDONLY|auditNoFollow, 0)
		if openErr != nil {
			return openErr
		}
		var first AuditEvent
		decodeErr := json.NewDecoder(io.LimitReader(file, 16384)).Decode(&first)
		closeErr := file.Close()
		if closeErr != nil {
			return closeErr
		}
		if decodeErr == nil && !first.Timestamp.IsZero() && first.Timestamp.Before(oldest) {
			oldest = first.Timestamp
		}
		if oldest.Before(cutoff) {
			if err = root.Remove(entry); err != nil {
				return err
			}
		}
	}
	return nil
}

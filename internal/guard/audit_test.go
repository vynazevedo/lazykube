package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditPrivateRotationAndRetention(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	path := filepath.Join(dir, "audit.jsonl")
	a := NewFileAudit(path)
	a.limit = 1
	now := time.Now()
	a.clock = ClockFunc(func() time.Time { return now })
	for range 10 {
		if err := a.Record(AuditEvent{Context: "dev\x1b\n", Timestamp: now}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 10 {
		t.Fatal(len(entries), err)
	}
	for _, entry := range entries {
		info, statErr := entry.Info()
		if statErr != nil || info.Mode().Perm() != 0600 {
			t.Fatal(info, statErr)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var e AuditEvent
	if err = json.Unmarshal(raw, &e); err != nil || e.Context != "dev" {
		t.Fatal(e, err)
	}
	old := now.Add(-31 * 24 * time.Hour)
	oldRecord, marshalErr := json.Marshal(AuditEvent{Timestamp: old})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err = os.WriteFile(path+".5", append(oldRecord, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path+".5", old, old); err != nil {
		t.Fatal(err)
	}
	a.limit = 10 << 20
	if err = a.Record(AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path + ".5"); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "command") || strings.Contains(string(raw), "payload") {
		t.Fatal(string(raw))
	}
}
func TestAuditRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"mode", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "audit")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "audit.jsonl")
			var err error
			switch kind {
			case "mode":
				err = os.WriteFile(path, []byte("private"), 0644)
			case "symlink":
				err = os.Symlink("missing", path)
			case "directory":
				err = os.Mkdir(path, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			a := NewFileAudit(path)
			if err = a.Record(AuditEvent{}); err == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
}

func TestAuditRetainsOldEventUntilEntireSegmentExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "audit.jsonl")
	a := NewFileAudit(path)
	now := time.Now()
	a.clock = ClockFunc(func() time.Time { return now })
	if err := a.Record(AuditEvent{OperationID: "old", Timestamp: now.Add(-31 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := a.Record(AuditEvent{OperationID: "new", Timestamp: now}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "old") {
		t.Fatal(string(raw), err)
	}
}

func TestAuditRejectsConcurrentDirectoryOwner(t *testing.T) {
	dir := t.TempDir()
	held, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if err = lockAudit(held); err != nil {
		t.Fatal(err)
	}
	a := NewFileAudit(filepath.Join(dir, "audit.jsonl"))
	if err = a.Record(AuditEvent{}); err == nil {
		t.Fatal("concurrent writer accepted")
	}
}

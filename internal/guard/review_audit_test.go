package guard

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReviewRetentionKeepsRecentRecordsInOldSegment(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "audit", "audit.jsonl")
	a := NewFileAudit(path)
	a.clock = ClockFunc(func() time.Time { return now })
	if err := a.Record(AuditEvent{Timestamp: now.Add(-31 * 24 * time.Hour), OperationID: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Record(AuditEvent{Timestamp: now, OperationID: "recent"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || !containsAuditID(raw, "old") {
		t.Fatal("append removed existing segment before its newest record expired")
	}
	old := now.Add(-31 * 24 * time.Hour)
	if err = os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err = a.Record(AuditEvent{Timestamp: now, OperationID: "next"}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil || !containsAuditID(raw, "recent") {
		t.Fatal("recent record removed", err)
	}
}

func containsAuditID(raw []byte, id string) bool {
	return bytes.Contains(raw, []byte(`"operation_id":"`+id+`"`))
}

func TestReviewFullAuditKeepsUnexpiredSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "audit.jsonl")
	a := NewFileAudit(path)
	a.limit = 1
	for range 10 {
		if err := a.Record(AuditEvent{Timestamp: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(path + ".9")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Record(AuditEvent{Timestamp: time.Now()}); !errors.Is(err, AuditFailed) {
		t.Fatal("full retention must block rather than overwrite", err)
	}
	after, err := os.ReadFile(path + ".9")
	if err != nil || string(after) != string(before) {
		t.Fatal("retained segment changed", err)
	}
}

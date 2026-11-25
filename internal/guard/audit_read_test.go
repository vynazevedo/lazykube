//go:build linux || darwin

package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeAudit(t *testing.T, path string, limit int64, n int) {
	t.Helper()
	a := NewFileAudit(path)
	if limit > 0 {
		a.limit = limit
	}
	now := time.Now().UTC()
	a.clock = ClockFunc(func() time.Time { return now })
	for i := range n {
		e := AuditEvent{SchemaVersion: 1, Timestamp: now.Add(time.Duration(i) * time.Second), OperationID: fmt.Sprintf("op-%03d", i), Phase: "result", Action: Scale, Context: "dev", Identity: "user", Namespace: "apps", Resource: "deployments", Name: "api", Outcome: Succeeded}
		if err := a.Record(e); err != nil {
			t.Fatal(err)
		}
	}
}

func readAll(t *testing.T, path string, limit int) ([]AuditEvent, int) {
	t.Helper()
	var out []AuditEvent
	cursor, pages := AuditCursor{}, 0
	for {
		page, err := ReadAudit(t.Context(), path, cursor, limit)
		if err != nil || page.Rotated {
			t.Fatal(err, page.Rotated)
		}
		pages++
		out = append(out, page.Events...)
		if !page.More {
			return out, pages
		}
		cursor = page.Next
	}
}

func TestReadAuditPagesNewestFirstAcrossSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "audit.jsonl")
	writeAudit(t, path, 2000, 40)
	if _, err := os.Stat(path + ".3"); err != nil {
		t.Fatal("fixture must rotate into several segments")
	}
	events, pages := readAll(t, path, 7)
	if len(events) != 40 || pages < 6 {
		t.Fatalf("read %d events in %d pages", len(events), pages)
	}
	for i, e := range events {
		if want := fmt.Sprintf("op-%03d", 39-i); e.OperationID != want {
			t.Fatalf("event %d = %s, want %s newest first without gaps or duplicates", i, e.OperationID, want)
		}
	}
}

func TestReadAuditPendingInvalidAndMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	path := filepath.Join(dir, "audit.jsonl")
	if page, err := ReadAudit(t.Context(), path, AuditCursor{}, 0); err != nil || !page.Missing || len(page.Events) != 0 {
		t.Fatalf("missing directory is an empty audit, not a failure: %+v %v", page, err)
	}
	writeAudit(t, path, 0, 3)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"schema_version":1,"timestamp":"2026-10-22T10:00:00Z","payload":"secret"}` + "\n")
	_, _ = f.WriteString(`{"schema_version":2,"timestamp":"2026-10-22T10:00:00Z"}` + "\n")
	_, _ = f.WriteString(`{"schema_version":1,"timestamp":"2026-10-22T10:00:00Z","operation_id":"half`)
	_ = f.Close()
	page, err := ReadAudit(t.Context(), path, AuditCursor{}, 0)
	if err != nil || !page.Pending || page.Invalid != 2 || len(page.Events) != 3 || page.More {
		t.Fatalf("partial tail waits, unknown fields and schemas are skipped: %+v %v", page, err)
	}
}

func TestReadAuditRejectsLinksAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	path := filepath.Join(dir, "audit.jsonl")
	writeAudit(t, path, 0, 1)
	other := filepath.Join(t.TempDir(), "other.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".bak"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAudit(t.Context(), path, AuditCursor{}, 0); err == nil {
		t.Fatal("symlinked segment must not be read")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".bak", path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAudit(t.Context(), path, AuditCursor{}, 0); err == nil {
		t.Fatal("group readable segment must not be read")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAudit(t.Context(), path, AuditCursor{}, 0); err == nil {
		t.Fatal("non private directory must not be read")
	}
	if _, err := ReadAudit(t.Context(), "relative/audit.jsonl", AuditCursor{}, 0); err == nil {
		t.Fatal("relative path refused")
	}
}

func TestReadAuditDetectsRotationAndDoesNotBlockWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "audit.jsonl")
	writeAudit(t, path, 0, 5)
	page, err := ReadAudit(t.Context(), path, AuditCursor{}, 2)
	if err != nil || !page.More || len(page.Events) != 2 {
		t.Fatal(page, err)
	}
	a := NewFileAudit(path)
	if err := a.Record(AuditEvent{SchemaVersion: 1, Timestamp: time.Now().UTC(), OperationID: "during-read"}); err != nil {
		t.Fatal("reader holds no lock that blocks the writer:", err)
	}
	next, err := ReadAudit(t.Context(), path, page.Next, 2)
	if err != nil || next.Rotated || next.Events[0].OperationID != "op-002" {
		t.Fatalf("appending keeps the cursor valid: %+v %v", next, err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	writeAudit(t, path, 0, 1)
	if rot, err := ReadAudit(t.Context(), path, page.Next, 2); err != nil || !rot.Rotated {
		t.Fatalf("rotation under the cursor is reported, not merged: %+v %v", rot, err)
	}
}

func TestReadAuditBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "audit.jsonl")
	writeAudit(t, path, 0, 300)
	page, err := ReadAudit(t.Context(), path, AuditCursor{}, 0)
	if err != nil || len(page.Events) != AuditPageSize || !page.More {
		t.Fatalf("page capped at %d: %d %v", AuditPageSize, len(page.Events), err)
	}
	events, _ := readAll(t, path, 0)
	if len(events) != 300 {
		t.Fatalf("all records reachable by paging: %d", len(events))
	}
}

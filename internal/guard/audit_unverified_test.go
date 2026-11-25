//go:build !linux

package guard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func unverifiedTree(t *testing.T, base string) string {
	t.Helper()
	var out strings.Builder
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		content := ""
		if info.Mode().IsRegular() {
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			content = string(raw)
		}
		_, printErr := fmt.Fprintln(&out, path, info.Mode(), info.ModTime().UnixNano(), content)
		return printErr
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func refusedEverywhere(t *testing.T, path string, want AuditRefusal) {
	t.Helper()
	for _, create := range []bool{false, true} {
		if _, err := openAudit(path, create); !errors.Is(err, want) {
			t.Fatalf("open create=%v: %v, want %q", create, err, want)
		}
	}
	page, err := ReadAudit(t.Context(), path, AuditCursor{}, 0)
	if !errors.Is(err, want) || !errors.Is(err, ErrAuditUnreadable) || err.Error() != want.Error() {
		t.Fatalf("reader: %v, want %q", err, want)
	}
	if page.Missing || page.More || page.Pending || page.Rotated || len(page.Events) != 0 {
		t.Fatalf("a refusal is not an empty audit: %+v", page)
	}
	a := NewFileAudit(path)
	for range 2 {
		if err := a.Record(AuditEvent{SchemaVersion: 1, Timestamp: time.Now().UTC(), OperationID: "op"}); !errors.Is(err, AuditFailed) {
			t.Fatalf("writer: %v", err)
		}
	}
	if !a.failed {
		t.Fatal("the writer stays blocked")
	}
}

func TestUnverifiedPlatformRefusesAndCreatesNothing(t *testing.T) {
	base := t.TempDir()
	before := unverifiedTree(t, base)
	for _, path := range []string{
		filepath.Join(base, "audit.jsonl"),
		filepath.Join(base, "audit", "audit.jsonl"),
		filepath.Join(base, "a", "b", "audit", "audit.jsonl"),
	} {
		refusedEverywhere(t, path, RefusedPlatform)
	}
	if after := unverifiedTree(t, base); after != before {
		t.Fatalf("a refused audit creates nothing:\n%s", after)
	}
}

func TestUnverifiedPlatformLeavesExistingAuditUntouched(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "audit")
	path := filepath.Join(dir, "audit.jsonl")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-31 * 24 * time.Hour)
	for _, name := range []string{path, path + ".1", path + ".5"} {
		line := fmt.Sprintf("{\"schema_version\":1,\"timestamp\":%q,\"operation_id\":\"old\"}\n", old.UTC().Format(time.RFC3339))
		if err := os.WriteFile(name, []byte(line), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(name, old, old); err != nil {
			t.Fatal(err)
		}
	}
	before := unverifiedTree(t, base)
	refusedEverywhere(t, path, RefusedPlatform)
	rotating := NewFileAudit(path)
	rotating.limit = 1
	if err := rotating.Record(AuditEvent{Timestamp: time.Now()}); !errors.Is(err, AuditFailed) {
		t.Fatalf("rotation: %v", err)
	}
	if after := unverifiedTree(t, base); after != before {
		t.Fatalf("no append, rotation or expiry:\n%s\n%s", before, after)
	}
}

func TestUnverifiedPlatformRefusesUnsafeFiles(t *testing.T) {
	for kind, plant := range map[string]func(path string) error{
		"mode":      func(path string) error { return os.WriteFile(path, []byte("private"), 0644) },
		"directory": func(path string) error { return os.Mkdir(path, 0700) },
	} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, "audit.jsonl")
			if err := plant(path); err != nil {
				t.Fatal(err)
			}
			before := unverifiedTree(t, base)
			refusedEverywhere(t, path, RefusedPlatform)
			if after := unverifiedTree(t, base); after != before {
				t.Fatalf("%s changed:\n%s", kind, after)
			}
		})
	}
}

func TestUnverifiedPlatformRefusesUncleanPath(t *testing.T) {
	for _, path := range []string{"audit.jsonl", filepath.Join(t.TempDir(), "x") + string(filepath.Separator) + ".." + string(filepath.Separator) + "audit.jsonl"} {
		refusedEverywhere(t, path, RefusedForm)
	}
	if _, err := os.Stat("audit.jsonl"); !os.IsNotExist(err) {
		t.Fatalf("a relative path creates nothing: %v", err)
	}
}

func TestUnverifiedPlatformTrustsNothing(t *testing.T) {
	dir := t.TempDir()
	held, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	info, err := held.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := lockAudit(held); !errors.Is(err, AuditFailed) {
		t.Fatalf("lock: %v", err)
	}
	if private(info, true) || private(info, false) || trustedFilesystem(held) {
		t.Fatal("privacy is never assumed")
	}
}

func TestUnverifiedPlatformBlocksActions(t *testing.T) {
	base := t.TempDir()
	before := unverifiedTree(t, base)
	f := &guardFake{allowed: true}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e, err := NewExecutor(context.Background(), guardSession(), NewPolicy(true), f, f, NewFileAudit(filepath.Join(base, "audit", "audit.jsonl")), ClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	for range 2 {
		p, err := e.Prepare(t.Context(), guardRequest())
		if err == nil {
			_, err = e.Execute(t.Context(), p.ID, Confirmation{Approved: true, Resource: "pod"})
		}
		if !errors.Is(err, AuditFailed) {
			t.Fatalf("no action without a verifiable audit: %v", err)
		}
	}
	for _, dry := range f.sends {
		if !dry {
			t.Fatal("a real write left without an audit")
		}
	}
	if after := unverifiedTree(t, base); after != before {
		t.Fatalf("blocked actions create nothing:\n%s", after)
	}
}

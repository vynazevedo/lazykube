//go:build linux || darwin

package guard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func refused(t *testing.T, path, why string) {
	t.Helper()
	done := make(chan struct{})
	var page AuditPage
	var err error
	go func() {
		defer close(done)
		page, err = ReadAudit(context.Background(), path, AuditCursor{}, 0)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s blocked the reader", why)
	}
	if err == nil || len(page.Events) != 0 {
		t.Fatalf("%s must fail closed: %d events, %v", why, len(page.Events), err)
	}
	if strings.Contains(err.Error(), path) {
		t.Fatalf("%s leaks the path: %v", why, err)
	}
}

func TestReadAuditRefusesLinkedDirectoryAndRotation(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	writeAudit(t, filepath.Join(real, "audit.jsonl"), 0, 2)
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	refused(t, filepath.Join(link, "audit.jsonl"), "a symlinked directory")

	path := filepath.Join(real, "audit.jsonl")
	other := filepath.Join(base, "other.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, seg := range []string{path + ".1", path + ".5"} {
		if err := os.Symlink(other, seg); err != nil {
			t.Fatal(err)
		}
		refused(t, path, "a symlinked rotation")
		if target, err := os.Readlink(seg); err != nil || target != other {
			t.Fatal("the reader never repairs the rotation")
		}
		if err := os.Remove(seg); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(other, path+".1"); err != nil {
		t.Fatal(err)
	}
	refused(t, path, "a hard linked rotation")
	if err := os.Remove(path + ".1"); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path+".1", 0600); err != nil {
		t.Fatal(err)
	}
	refused(t, path, "a fifo rotation")
}

func TestReadAuditRefusesLinkedAncestors(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	writeAudit(t, filepath.Join(real, "deep", "audit", "audit.jsonl"), 0, 1)
	if page, err := ReadAudit(t.Context(), filepath.Join(real, "deep", "audit", "audit.jsonl"), AuditCursor{}, 0); err != nil || len(page.Events) != 1 {
		t.Fatalf("a path without links is read: %+v %v", page, err)
	}
	if err := os.Symlink(real, filepath.Join(base, "alias")); err != nil {
		t.Fatal(err)
	}
	refused(t, filepath.Join(base, "alias", "deep", "audit", "audit.jsonl"), "a symlinked ancestor")
	if err := os.Symlink(filepath.Join(real, "deep"), filepath.Join(real, "link")); err != nil {
		t.Fatal(err)
	}
	refused(t, filepath.Join(real, "link", "audit", "audit.jsonl"), "a symlinked parent")
	refused(t, real+"/../real/deep/audit/audit.jsonl", "an unclean path")
	if page, err := ReadAudit(t.Context(), filepath.Join(base, "none", "audit", "audit.jsonl"), AuditCursor{}, 0); err != nil || !page.Missing {
		t.Fatalf("a missing ancestor is missing, not a failure: %+v %v", page, err)
	}
}

func TestReadAuditSurvivesAncestorSwap(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live, parked, foreign := filepath.Join(base, "live"), filepath.Join(base, "parked"), filepath.Join(base, "foreign")
	path := filepath.Join(live, "audit", "audit.jsonl")
	writeAudit(t, path, 0, 1)
	writeAudit(t, filepath.Join(foreign, "audit", "audit.jsonl"), 0, 3)
	stop, swapped := make(chan struct{}), make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				swapped <- nil
				return
			default:
			}
			for _, op := range []func() error{
				func() error { return os.Rename(live, parked) },
				func() error { return os.Symlink(foreign, live) },
				func() error { return os.Remove(live) },
				func() error { return os.Rename(parked, live) },
			} {
				if err := op(); err != nil {
					swapped <- err
					return
				}
			}
		}
	}()
	read, refusals := 0, 0
	for range 4000 {
		page, err := ReadAudit(t.Context(), path, AuditCursor{}, 0)
		switch {
		case err != nil || page.Missing:
			refusals++
		case len(page.Events) == 1:
			read++
		case len(page.Events) > 1:
			t.Errorf("followed the swapped ancestor: %d events", len(page.Events))
		}
	}
	close(stop)
	if err := <-swapped; err != nil {
		t.Fatal(err)
	}
	if refusals == 0 {
		t.Fatal("the swap never overlapped a read")
	}
	t.Logf("read %d, refused %d", read, refusals)
}

func TestReadAuditOpenNeverFollowsOrBlocks(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	if err := os.WriteFile(filepath.Join(dir, "target"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(dir, "swapped")); err != nil {
		t.Fatal(err)
	}
	if f, err := root.OpenFile("swapped", os.O_RDONLY|auditReadFlags, 0); err == nil {
		info, statErr := f.Stat()
		_ = f.Close()
		linked, lerr := root.Lstat("swapped")
		if statErr != nil || lerr != nil || os.SameFile(linked, info) {
			t.Fatal("a swapped link must not match its validated entry")
		}
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	go func() {
		defer close(opened)
		if f, err := root.OpenFile("fifo", os.O_RDONLY|auditReadFlags, 0); err == nil {
			_ = f.Close()
		}
	}()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("a fifo swapped in after validation blocks the open")
	}
}

func TestReadAuditStopsWhenCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "audit.jsonl")
	writeAudit(t, path, 0, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if page, err := ReadAudit(ctx, path, AuditCursor{}, 0); err == nil || len(page.Events) != 0 {
		t.Fatal("a canceled read returns nothing")
	}
}

func TestReadAuditDropsUnknownFieldsAndControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "audit.jsonl")
	writeAudit(t, path, 0, 1)
	e := AuditEvent{SchemaVersion: 1, Timestamp: time.Now().UTC(), OperationID: "op\x1b]52;c;x\x07", Context: "dev\u202e", Name: "a\nb"}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	lines := string(raw) + "\n" + `{"schema_version":1,"timestamp":"2026-11-25T00:00:00Z","payload":"segredo"}` + "\n" + strings.Repeat("x", auditLineLimit+1) + "\n"
	if _, err := f.WriteString(lines); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	page, err := ReadAudit(t.Context(), path, AuditCursor{}, 0)
	if err != nil || len(page.Events) != 2 || page.Invalid != 2 {
		t.Fatalf("unknown fields and long lines are counted, not shown: %+v %v", page, err)
	}
	for _, s := range []string{page.Events[0].OperationID, page.Events[0].Context, page.Events[0].Name} {
		if strings.ContainsAny(s, "\x1b\x07\n\u202e") {
			t.Fatalf("controls survive: %q", s)
		}
	}
}

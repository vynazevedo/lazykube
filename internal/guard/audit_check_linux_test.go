package guard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileAuditCheckExplainsTheRule(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(string) string
		want  AuditRefusal
	}{
		"group writable parent": {func(base string) string {
			if err := os.Chmod(base, 0o770); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(base, "audit", "audit.jsonl")
		}, RefusedOwner},
		"readable directory": {func(base string) string {
			dir := filepath.Join(base, "audit")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "audit.jsonl")
		}, RefusedPrivate},
		"readable file": {func(base string) string {
			dir := filepath.Join(base, "audit")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "audit.jsonl")
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			return path
		}, RefusedPrivate},
		"user link": {func(base string) string {
			real := filepath.Join(base, "real")
			if err := os.Mkdir(real, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, filepath.Join(base, "link")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(base, "link", "audit.jsonl")
		}, RefusedLink},
		"relative": {func(string) string { return "audit.jsonl" }, RefusedForm},
	} {
		t.Run(name, func(t *testing.T) {
			for _, create := range []bool{false, true} {
				base := filepath.Join(t.TempDir(), "base")
				if err := os.Mkdir(base, 0o700); err != nil {
					t.Fatal(err)
				}
				a := NewFileAudit(tc.setup(base))
				for range 2 {
					if err := a.Check(create); !errors.Is(err, tc.want) || !errors.Is(err, AuditFailed) || err.Error() != string(tc.want) {
						t.Fatal(create, err)
					}
					if err := a.Record(AuditEvent{SchemaVersion: 1, Timestamp: time.Now()}); !errors.Is(err, tc.want) {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestFileAuditCheckCreatesOnlyOnGesture(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "audit", "audit.jsonl")
	a := NewFileAudit(path)
	if err := a.Check(false); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(base); err != nil || len(entries) != 0 {
		t.Fatal("opening the menu creates nothing", entries, err)
	}
	if err := a.Check(true); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal(info, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a check writes no record", err)
	}
	if err := a.Record(AuditEvent{SchemaVersion: 1, Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := a.Check(false); err != nil {
		t.Fatal(err)
	}
}

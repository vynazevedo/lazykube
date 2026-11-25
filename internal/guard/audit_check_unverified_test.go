//go:build !linux

package guard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnverifiedPlatformCheckExplainsAndCreatesNothing(t *testing.T) {
	base := t.TempDir()
	for _, create := range []bool{false, true} {
		a := NewFileAudit(filepath.Join(base, "audit", "audit.jsonl"))
		if err := a.Check(create); !errors.Is(err, RefusedPlatform) {
			t.Fatal(err)
		}
	}
	if entries, err := os.ReadDir(base); err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func renderMan(t *testing.T) (string, int) {
	t.Helper()
	var stdout bytes.Buffer
	code := execute(context.Background(), []string{"man"}, &stdout, io.Discard, func(flags) int { return 1 })
	return stdout.String(), code
}

const manChildEnv = "LAZYKUBE_MAN_CHILD"

func TestManChild(t *testing.T) {
	if os.Getenv(manChildEnv) == "" {
		t.Skip("runs only as a subprocess")
	}
	page, code := renderMan(t)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if err := os.WriteFile(os.Getenv(manChildEnv), []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestManPageIsReproducibleAcrossTimeZones(t *testing.T) {
	dir := t.TempDir()
	var pages []string
	for i, zone := range []string{"Etc/GMT+12", "Etc/GMT-14"} {
		if _, err := time.LoadLocation(zone); err != nil {
			t.Skipf("tzdata unavailable: %v", err)
		}
		out := filepath.Join(dir, fmt.Sprintf("page%d", i))
		cmd := exec.Command(os.Args[0], "-test.run=^TestManChild$", "-test.count=1")
		cmd.Env = append(os.Environ(), manChildEnv+"="+out, "TZ="+zone, sourceDateEpoch+"=1794355200")
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", zone, err, msg)
		}
		page, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, string(page))
	}
	if pages[0] != pages[1] {
		t.Fatal("man page differs between time zones")
	}
	if !strings.HasPrefix(pages[0], `.TH LAZYKUBE 1 "2026-11-11" "lazykube"`) {
		t.Fatalf("man page heading lacks the SOURCE_DATE_EPOCH date: %q", strings.SplitN(pages[0], "\n", 2)[0])
	}
}

func TestManPageRejectsInvalidSourceDateEpoch(t *testing.T) {
	for _, raw := range []string{"yesterday", "-1", "1.5"} {
		t.Setenv(sourceDateEpoch, raw)
		page, code := renderMan(t)
		if code != exitUsage || page != "" {
			t.Fatalf("%q: code=%d len=%d", raw, code, len(page))
		}
	}
}

func TestManDateFallsBackToNowInUTC(t *testing.T) {
	loc := time.FixedZone("ahead", 14*3600)
	now := time.Date(2026, 11, 12, 5, 0, 0, 0, loc)
	got, err := manDate(func(string) string { return "" }, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if got.Location() != time.UTC || got.Format(time.DateOnly) != "2026-11-11" {
		t.Fatalf("got %v", got)
	}
}

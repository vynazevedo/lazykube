package guard

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func privateBase(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return base
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	must(t, err)
	return len(entries)
}

func TestAuditReaderAndWriterShareOneDecision(t *testing.T) {
	for name, tc := range map[string]struct {
		prepare func(t *testing.T, base string) string
		want    AuditRefusal
	}{
		"plain": {func(_ *testing.T, base string) string { return filepath.Join(base, "audit") }, ""},
		"nested creation": {func(_ *testing.T, base string) string {
			return filepath.Join(base, "a", "b", "audit")
		}, ""},
		"own link ancestor": {func(t *testing.T, base string) string {
			must(t, os.MkdirAll(filepath.Join(base, "real", "audit"), 0700))
			must(t, os.Symlink("real", filepath.Join(base, "alias")))
			return filepath.Join(base, "alias", "audit")
		}, RefusedLink},
		"own link directory": {func(t *testing.T, base string) string {
			must(t, os.MkdirAll(filepath.Join(base, "real"), 0700))
			must(t, os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "audit")))
			return filepath.Join(base, "audit")
		}, RefusedLink},
		"own dangling link": {func(t *testing.T, base string) string {
			must(t, os.Symlink("missing", filepath.Join(base, "alias")))
			return filepath.Join(base, "alias", "audit")
		}, RefusedLink},
		"group writable ancestor, gid equal to uid or not": {func(t *testing.T, base string) string {
			must(t, os.Mkdir(filepath.Join(base, "shared"), 0700))
			must(t, os.Chmod(filepath.Join(base, "shared"), 0775))
			return filepath.Join(base, "shared", "audit")
		}, RefusedOwner},
		"world writable ancestor": {func(t *testing.T, base string) string {
			must(t, os.Mkdir(filepath.Join(base, "open"), 0700))
			must(t, os.Chmod(filepath.Join(base, "open"), 0777))
			return filepath.Join(base, "open", "audit")
		}, RefusedOwner},
		"sticky ancestor": {func(t *testing.T, base string) string {
			must(t, os.Mkdir(filepath.Join(base, "sticky"), 0700))
			must(t, os.Chmod(filepath.Join(base, "sticky"), 0777|os.ModeSticky))
			return filepath.Join(base, "sticky", "audit")
		}, ""},
		"directory not private": {func(t *testing.T, base string) string {
			must(t, os.Mkdir(filepath.Join(base, "audit"), 0700))
			must(t, os.Chmod(filepath.Join(base, "audit"), 0750))
			return filepath.Join(base, "audit")
		}, RefusedPrivate},
		"file as ancestor": {func(t *testing.T, base string) string {
			must(t, os.WriteFile(filepath.Join(base, "file"), nil, 0600))
			return filepath.Join(base, "file", "audit")
		}, RefusedPrivate},
		"unclean":  {func(_ *testing.T, base string) string { return base + "/x/../audit" }, RefusedForm},
		"relative": {func(*testing.T, string) string { return "audit" }, RefusedForm},
		"proc":     {func(*testing.T, string) string { return "/proc/self/audit" }, RefusedFilesystem},
	} {
		t.Run(name, func(t *testing.T) {
			base := privateBase(t)
			dir := tc.prepare(t, base)
			path := dir + "/audit.jsonl"
			before := openFDs(t)
			tree := snapshotTree(t, base)
			_, readErr := ReadAudit(t.Context(), path, AuditCursor{}, 0)
			var refusal AuditRefusal
			errors.As(readErr, &refusal)
			if refusal != tc.want || (tc.want == "" && readErr != nil) {
				t.Fatalf("reader: %v, want %q", readErr, tc.want)
			}
			a := NewFileAudit(path)
			writeErr := a.Record(AuditEvent{SchemaVersion: 1, Timestamp: time.Now().UTC(), OperationID: "op"})
			_, direct := openAudit(path, true)
			refusal = ""
			if tc.want != "" {
				errors.As(direct, &refusal)
			}
			if refusal != tc.want || (tc.want == "") != (writeErr == nil) || (tc.want != "" && !errors.Is(writeErr, AuditFailed)) {
				t.Fatalf("writer: %v / %v, want %q", writeErr, direct, tc.want)
			}
			if tc.want != "" {
				if got := snapshotTree(t, base); got != tree {
					t.Fatalf("a refused path is left untouched:\n%s\n%s", tree, got)
				}
				if after := openFDs(t); after != before {
					t.Fatalf("descriptors leak: %d -> %d", before, after)
				}
				return
			}
			page, err := ReadAudit(t.Context(), path, AuditCursor{}, 0)
			if err != nil || len(page.Events) != 1 {
				t.Fatalf("what the writer records the reader shows: %+v %v", page, err)
			}
		})
	}
}

func snapshotTree(t *testing.T, base string) string {
	t.Helper()
	var out strings.Builder
	must(t, filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		_, werr := fmt.Fprintln(&out, path, info.Mode(), info.Size())
		return werr
	}))
	return out.String()
}

func TestAuditRefusesNamedACLDespiteMatchingIDs(t *testing.T) {
	base := privateBase(t)
	shared := filepath.Join(base, "shared")
	must(t, os.Mkdir(shared, 0755))
	var acl bytes.Buffer
	must(t, binary.Write(&acl, binary.LittleEndian, uint32(2)))
	for _, e := range []struct {
		Tag, Perm uint16
		ID        uint32
	}{{1, 7, ^uint32(0)}, {2, 7, 54321}, {4, 5, ^uint32(0)}, {16, 7, ^uint32(0)}, {32, 5, ^uint32(0)}} {
		must(t, binary.Write(&acl, binary.LittleEndian, e))
	}
	if err := unix.Setxattr(shared, "system.posix_acl_access", acl.Bytes(), 0); err != nil {
		t.Skipf("filesystem without POSIX ACL: %v", err)
	}
	if _, err := openAudit(filepath.Join(shared, "audit", "audit.jsonl"), true); !errors.Is(err, RefusedOwner) {
		t.Fatalf("a named user with write is reflected in the mask and refused: %v", err)
	}
}

func TestAuditCreationIgnoresDefaultACL(t *testing.T) {
	base := privateBase(t)
	var acl bytes.Buffer
	must(t, binary.Write(&acl, binary.LittleEndian, uint32(2)))
	for _, e := range []struct {
		Tag, Perm uint16
		ID        uint32
	}{{1, 7, ^uint32(0)}, {2, 7, 54321}, {4, 7, ^uint32(0)}, {16, 7, ^uint32(0)}, {32, 5, ^uint32(0)}} {
		must(t, binary.Write(&acl, binary.LittleEndian, e))
	}
	if err := unix.Setxattr(base, "system.posix_acl_default", acl.Bytes(), 0); err != nil {
		t.Skipf("filesystem without POSIX ACL: %v", err)
	}
	path := filepath.Join(base, "audit", "audit.jsonl")
	must(t, NewFileAudit(path).Record(AuditEvent{SchemaVersion: 1, Timestamp: time.Now().UTC()}))
	for p, want := range map[string]os.FileMode{filepath.Dir(path): 0700, path: 0600} {
		info, err := os.Stat(p)
		must(t, err)
		if info.Mode().Perm() != want {
			t.Fatalf("%s created as %o", p, info.Mode().Perm())
		}
	}
}

func injected(owners map[uint64]uint32) *auditWalk {
	return &auditWalk{euid: uint32(os.Geteuid()), mkdir: unix.Mkdirat, trusted: func(int) bool { return true }, node: func(st unix.Stat_t) auditNode {
		n := nodeOf(st)
		if uid, ok := owners[st.Ino]; ok {
			n.uid = uid
		}
		return n
	}}
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	var st unix.Stat_t
	must(t, unix.Lstat(path, &st))
	return st.Ino
}

func TestAuditWalkFollowsOnlyTrustedLinks(t *testing.T) {
	base := privateBase(t)
	sys, own, real := filepath.Join(base, "sys"), filepath.Join(base, "own"), filepath.Join(base, "real")
	for _, d := range []string{sys, own, filepath.Join(real, "audit"), filepath.Join(sys, "sub")} {
		must(t, os.MkdirAll(d, 0755))
	}
	link := func(dir, name, target string) string {
		path := filepath.Join(dir, name)
		must(t, os.Symlink(target, path))
		return path
	}
	links := map[string]string{
		"relative": link(sys, "relative", "../real"),
		"absolute": link(sys, "absolute", real),
		"parent":   link(sys, "parent", "sub/../../real/../real"),
		"chain":    link(sys, "chain", "absolute"),
		"cycle":    link(sys, "cycle", "cycle"),
		"long":     link(sys, "long", strings.Repeat("./", 1000)+"../real"),
	}
	inOwn := link(own, "root", real)
	toOwn := link(sys, "toOwn", filepath.Join(own, "root"))
	mine := link(sys, "mine", real)
	owners := map[uint64]uint32{inode(t, sys): 0}
	for _, p := range links {
		owners[inode(t, p)] = 0
	}
	owners[inode(t, inOwn)], owners[inode(t, toOwn)] = 0, 0
	want := inode(t, filepath.Join(real, "audit"))

	walk := func(path string) (uint64, error) {
		before := openFDs(t)
		f, err := injected(owners).open(path)
		var ino uint64
		if err == nil {
			var st unix.Stat_t
			must(t, unix.Fstat(int(f.Fd()), &st))
			ino = st.Ino
			must(t, f.Close())
		}
		if after := openFDs(t); after != before {
			t.Fatalf("%s leaks descriptors: %d -> %d", path, before, after)
		}
		return ino, err
	}
	for _, name := range []string{"relative", "absolute", "parent", "chain"} {
		if got, err := walk(filepath.Join(links[name], "audit")); err != nil || got != want {
			t.Fatalf("%s root link in a private root directory: %v", name, err)
		}
	}
	for name, path := range map[string]string{"cycle": links["cycle"], "too many components": links["long"], "root link in own directory": inOwn, "chain through an untrusted link": toOwn, "own link in root directory": mine} {
		if _, err := walk(filepath.Join(path, "audit")); !errors.Is(err, RefusedLink) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, mode := range []os.FileMode{0775, 0777 | os.ModeSticky, 0757} {
		must(t, os.Chmod(sys, mode))
		if _, err := walk(filepath.Join(links["absolute"], "audit")); err == nil {
			t.Fatalf("root link in a %v directory", mode)
		}
	}
	must(t, os.Chmod(sys, 0755))
	must(t, os.Chmod(real, 0775))
	if _, err := walk(filepath.Join(links["absolute"], "audit")); !errors.Is(err, RefusedOwner) {
		t.Fatalf("every component of the target is verified: %v", err)
	}
	must(t, os.Chmod(real, 0755))
	owners[inode(t, real)] = 4242
	if _, err := walk(filepath.Join(links["absolute"], "audit")); !errors.Is(err, RefusedOwner) {
		t.Fatalf("a target owned by another user: %v", err)
	}
	delete(owners, inode(t, real))
	w := injected(owners)
	w.trusted = func(int) bool { return false }
	if _, err := w.open(filepath.Join(real, "audit")); !errors.Is(err, RefusedFilesystem) {
		t.Fatalf("an unverifiable filesystem fails closed: %v", err)
	}
}

func TestAuditWalkFollowsNativeSystemLink(t *testing.T) {
	for _, path := range []string{"/bin", "/lib", "/sbin", "/var/run", "/var/lock"} {
		var st unix.Stat_t
		if unix.Lstat(path, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFLNK || st.Uid != 0 {
			continue
		}
		target, err := os.Stat(path)
		must(t, err)
		f, err := walkAudit(path, false)
		if err != nil {
			continue
		}
		info, err := f.Stat()
		must(t, errors.Join(err, f.Close()))
		if !os.SameFile(info, target) {
			t.Fatalf("%s resolves to another directory", path)
		}
		return
	}
	t.Skip("no root link with a verifiable target on this system")
}

func TestAuditFilesystemAllowlist(t *testing.T) {
	for magic, want := range map[uint32]bool{
		unix.EXT4_SUPER_MAGIC: true, unix.BTRFS_SUPER_MAGIC: true, unix.XFS_SUPER_MAGIC: true, unix.TMPFS_MAGIC: true, unix.OVERLAYFS_SUPER_MAGIC: true,
		unix.NFS_SUPER_MAGIC: false, unix.FUSE_SUPER_MAGIC: false, unix.V9FS_MAGIC: false, unix.SMB2_SUPER_MAGIC: false, unix.CIFS_SUPER_MAGIC: false, unix.MSDOS_SUPER_MAGIC: false, unix.PROC_SUPER_MAGIC: false, 0: false,
	} {
		if trustedMagic(magic) != want {
			t.Errorf("magic %x: want %v", magic, want)
		}
	}
}

func TestAuditWriterSurvivesAncestorSwap(t *testing.T) {
	base := privateBase(t)
	live, spare, foreign := filepath.Join(base, "live"), filepath.Join(base, "spare"), filepath.Join(base, "foreign")
	must(t, os.MkdirAll(filepath.Join(live, "audit"), 0700))
	must(t, os.MkdirAll(filepath.Join(foreign, "audit"), 0700))
	must(t, os.Symlink(foreign, spare))
	untouched := snapshotTree(t, foreign)
	if err := unix.Renameat2(unix.AT_FDCWD, live, unix.AT_FDCWD, spare, unix.RENAME_EXCHANGE); err != nil {
		t.Skipf("no atomic exchange: %v", err)
	}
	must(t, unix.Renameat2(unix.AT_FDCWD, live, unix.AT_FDCWD, spare, unix.RENAME_EXCHANGE))
	stop, done := make(chan struct{}), make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			if err := unix.Renameat2(unix.AT_FDCWD, live, unix.AT_FDCWD, spare, unix.RENAME_EXCHANGE); err != nil {
				done <- err
				return
			}
		}
	}()
	written, refused := 0, 0
	path := filepath.Join(live, "audit", "audit.jsonl")
	deadline := time.Now().Add(10 * time.Second)
	for n := 0; n < 400 || ((written == 0 || refused == 0) && time.Now().Before(deadline)); n++ {
		if NewFileAudit(path).Record(AuditEvent{SchemaVersion: 1, Timestamp: time.Now().UTC()}) == nil {
			written++
		} else {
			refused++
		}
	}
	close(stop)
	must(t, <-done)
	if got := snapshotTree(t, foreign); got != untouched {
		t.Fatalf("the writer followed the swapped ancestor:\n%s", got)
	}
	t.Logf("written %d, refused %d", written, refused)
}

func TestAuditCreationRace(t *testing.T) {
	base := privateBase(t)
	foreign := filepath.Join(base, "foreign")
	must(t, os.Mkdir(foreign, 0700))
	stop, done := make(chan struct{}), make(chan struct{})
	name := filepath.Join(base, "audit")
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if os.Symlink(foreign, name) == nil {
				_ = os.Remove(name)
			}
			_ = os.RemoveAll(name)
		}
	}()
	for range 500 {
		_ = NewFileAudit(filepath.Join(name, "audit.jsonl")).Record(AuditEvent{SchemaVersion: 1, Timestamp: time.Now().UTC()})
	}
	close(stop)
	<-done
	if entries, err := os.ReadDir(foreign); err != nil || len(entries) != 0 {
		t.Fatalf("a link raced into the created name was followed: %v %v", entries, err)
	}
}

func TestAuditWriterRefusesLinkedFiles(t *testing.T) {
	for name, plant := range map[string]func(other, path string) error{
		"symlink":  func(other, path string) error { return os.Symlink(other, path) },
		"hardlink": func(other, path string) error { return os.Link(other, path) },
		"fifo":     func(_, path string) error { return unix.Mkfifo(path, 0600) },
	} {
		t.Run(name, func(t *testing.T) {
			base := privateBase(t)
			path, other := filepath.Join(base, "audit", "audit.jsonl"), filepath.Join(base, "other")
			must(t, os.Mkdir(filepath.Dir(path), 0700))
			must(t, os.WriteFile(other, []byte("intacto"), 0600))
			must(t, plant(other, path))
			result := make(chan error, 1)
			go func() {
				result <- NewFileAudit(path).Record(AuditEvent{SchemaVersion: 1, Timestamp: time.Now().UTC()})
			}()
			select {
			case err := <-result:
				if !errors.Is(err, AuditFailed) {
					t.Fatalf("%s accepted: %v", name, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s blocks the writer", name)
			}
			if raw, err := os.ReadFile(other); err != nil || string(raw) != "intacto" {
				t.Fatalf("%s changed the target: %q %v", name, raw, err)
			}
		})
	}
}

func TestAuditRefusalBlocksActions(t *testing.T) {
	base := privateBase(t)
	must(t, os.Mkdir(filepath.Join(base, "real"), 0700))
	must(t, os.Symlink("real", filepath.Join(base, "alias")))
	f := &guardFake{allowed: true}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e, err := NewExecutor(context.Background(), guardSession(), NewPolicy(true), f, f, NewFileAudit(filepath.Join(base, "alias", "audit", "audit.jsonl")), ClockFunc(func() time.Time { return now }))
	must(t, err)
	t.Cleanup(e.Close)
	for range 2 {
		p, err := e.Prepare(t.Context(), guardRequest())
		if err == nil {
			_, err = e.Execute(t.Context(), p.ID, Confirmation{Approved: true, Resource: "pod"})
		}
		if !errors.Is(err, AuditFailed) {
			t.Fatalf("an untrusted audit path blocks the action: %v", err)
		}
	}
	for _, dry := range f.sends {
		if !dry {
			t.Fatal("a real write left without a durable audit")
		}
	}
}

func TestAuditWalkLimitsLinks(t *testing.T) {
	base := privateBase(t)
	must(t, os.Mkdir(filepath.Join(base, "sys"), 0755))
	owners := map[uint64]uint32{inode(t, filepath.Join(base, "sys")): 0}
	target := base
	for i := range auditLinkLimit + 1 {
		path := filepath.Join(base, "sys", fmt.Sprint(i))
		must(t, os.Symlink(target, path))
		owners[inode(t, path)], target = 0, fmt.Sprint(i)
	}
	f, err := injected(owners).open(filepath.Join(base, "sys", fmt.Sprint(auditLinkLimit-1)))
	if err != nil {
		t.Fatalf("%d links are followed: %v", auditLinkLimit, err)
	}
	must(t, f.Close())
	if _, err := injected(owners).open(filepath.Join(base, "sys", fmt.Sprint(auditLinkLimit))); !errors.Is(err, RefusedLink) {
		t.Fatalf("one more is refused: %v", err)
	}
}

func TestAuditWalkVerifiesWhatItCreated(t *testing.T) {
	base := privateBase(t)
	foreign := filepath.Join(base, "foreign")
	must(t, os.Mkdir(foreign, 0700))
	must(t, os.Mkdir(filepath.Join(base, "sys"), 0755))
	for name, plant := range map[string]func(path string) error{
		"trusted link":   func(path string) error { return os.Symlink(foreign, path) },
		"open directory": func(path string) error { return errors.Join(os.Mkdir(path, 0700), os.Chmod(path, 0755)) },
		"file":           func(path string) error { return os.WriteFile(path, nil, 0600) },
	} {
		path := filepath.Join(base, "sys", strings.ReplaceAll(name, " ", "-"))
		owners := map[uint64]uint32{inode(t, filepath.Join(base, "sys")): 0}
		w := injected(owners)
		w.create = true
		w.mkdir = func(int, string, uint32) error {
			must(t, plant(path))
			if name == "trusted link" {
				owners[inode(t, path)] = 0
			}
			return unix.EEXIST
		}
		if _, err := w.open(path); !errors.Is(err, RefusedOwner) {
			t.Fatalf("%s raced into the created name: %v", name, err)
		}
	}
}

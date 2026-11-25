package guard

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	auditWalkFlags  = unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC
	auditLinkLimit  = 40
	auditPartLimit  = 256
	auditBytesLimit = 16 << 10
	zfsMagic        = 0x2fc12fc1
	bcachefsMagic   = 0xca451a4e
)

type auditLevel struct {
	fd   int
	node auditNode
}

type auditWalk struct {
	euid                uint32
	create              bool
	trusted             func(fd int) bool
	node                func(unix.Stat_t) auditNode
	mkdir               func(fd int, name string, mode uint32) error
	stack               []auditLevel
	links, parts, bytes int
}

func walkAudit(dir string, create bool) (*os.File, error) {
	return (&auditWalk{euid: uint32(os.Geteuid()), create: create, trusted: trustedFD, node: nodeOf, mkdir: unix.Mkdirat}).open(dir)
}

func nodeOf(st unix.Stat_t) auditNode {
	return auditNode{uid: st.Uid, mode: st.Mode}
}

func (w *auditWalk) open(dir string) (file *os.File, err error) {
	defer func() {
		if closeErr := w.unwind(0); closeErr != nil {
			err = errors.Join(err, closeErr)
			if file != nil {
				err, file = errors.Join(err, file.Close()), nil
			}
		}
	}()
	fd, err := unix.Open("/", auditWalkFlags|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	if err = w.push(fd); err != nil {
		return nil, err
	}
	queue := strings.Split(dir, "/")
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if w.parts++; w.parts > auditPartLimit {
			return nil, RefusedLink
		}
		switch name {
		case "", ".":
			continue
		case "..":
			if err = w.unwind(max(1, len(w.stack)-1)); err != nil {
				return nil, err
			}
			continue
		}
		expanded, err := w.step(name)
		if err != nil {
			return nil, err
		}
		queue = append(expanded, queue...)
	}
	last := len(w.stack) - 1
	file = os.NewFile(uintptr(w.stack[last].fd), dir)
	w.stack = w.stack[:last]
	return file, nil
}

func (w *auditWalk) unwind(depth int) error {
	var errs []error
	for len(w.stack) > depth {
		last := len(w.stack) - 1
		errs = append(errs, unix.Close(w.stack[last].fd))
		w.stack = w.stack[:last]
	}
	return errors.Join(errs...)
}

func (w *auditWalk) push(fd int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return errors.Join(err, unix.Close(fd))
	}
	node := w.node(st)
	refusal := AuditRefusal("")
	switch {
	case st.Mode&unix.S_IFMT != unix.S_IFDIR:
		refusal = RefusedPrivate
	case !w.trusted(fd):
		refusal = RefusedFilesystem
	case !trustedDir(node, w.euid):
		refusal = RefusedOwner
	}
	if refusal != "" {
		return errors.Join(refusal, unix.Close(fd))
	}
	w.stack = append(w.stack, auditLevel{fd: fd, node: node})
	return nil
}

func (w *auditWalk) step(name string) ([]string, error) {
	parent := w.stack[len(w.stack)-1]
	fd, err := unix.Openat(parent.fd, name, auditWalkFlags, 0)
	created := false
	if errors.Is(err, unix.ENOENT) && w.create {
		if err = w.mkdir(parent.fd, name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, err
		}
		created = true
		fd, err = unix.Openat(parent.fd, name, auditWalkFlags, 0)
	}
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return nil, errors.Join(err, unix.Close(fd))
	}
	node := w.node(st)
	if created && (st.Mode&unix.S_IFMT != unix.S_IFDIR || !createdDir(node, w.euid)) {
		return nil, errors.Join(RefusedOwner, unix.Close(fd))
	}
	if st.Mode&unix.S_IFMT != unix.S_IFLNK {
		return nil, w.push(fd)
	}
	target, err := w.follow(parent.node, node, fd, st.Size)
	if err = errors.Join(err, unix.Close(fd)); err != nil {
		return nil, err
	}
	if strings.HasPrefix(target, "/") {
		if err = w.unwind(1); err != nil {
			return nil, err
		}
	}
	return strings.Split(target, "/"), nil
}

func (w *auditWalk) follow(parent, link auditNode, fd int, size int64) (string, error) {
	if w.links++; w.links > auditLinkLimit || !trustedLink(parent, link) {
		return "", RefusedLink
	}
	buf := make([]byte, unix.PathMax)
	n, err := unix.Readlinkat(fd, "", buf)
	if err != nil {
		return "", err
	}
	if w.bytes += n; n == 0 || n == len(buf) || int64(n) != size || w.bytes > auditBytesLimit {
		return "", RefusedLink
	}
	return string(buf[:n]), nil
}

func trustedFilesystem(file *os.File) bool {
	return trustedFD(int(file.Fd()))
}

func trustedFD(fd int) bool {
	var st unix.Statfs_t
	return unix.Fstatfs(fd, &st) == nil && trustedMagic(uint32(st.Type))
}

func trustedMagic(magic uint32) bool {
	switch magic {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC, unix.F2FS_SUPER_MAGIC,
		unix.TMPFS_MAGIC, unix.RAMFS_MAGIC, unix.OVERLAYFS_SUPER_MAGIC, unix.SQUASHFS_MAGIC,
		unix.EROFS_SUPER_MAGIC_V1, zfsMagic, bcachefsMagic:
		return true
	}
	return false
}

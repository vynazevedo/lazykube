//go:build linux || darwin

package guard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func openAuditDir(dir string) (*os.File, error) {
	flags := auditWalkFlags | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Open("/", flags, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.Trim(filepath.Clean(dir), "/"), "/") {
		if part == "" {
			continue
		}
		next, err := unix.Openat(fd, part, flags, 0)
		if closeErr := unix.Close(fd); closeErr != nil && err == nil {
			err = errors.Join(closeErr, unix.Close(next))
		}
		if err != nil {
			return nil, err
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), dir), nil
}

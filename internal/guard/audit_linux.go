package guard

import (
	"os"
	"syscall"
)

const auditNoFollow = syscall.O_NOFOLLOW

func private(info os.FileInfo, dir bool) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if dir {
		return info.IsDir() && info.Mode().Perm() == 0700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && stat.Nlink == 1
}

func lockAudit(directory *os.File) error {
	return syscall.Flock(int(directory.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

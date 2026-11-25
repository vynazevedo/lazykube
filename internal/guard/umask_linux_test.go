package guard

import "syscall"

func init() {
	syscall.Umask(0o022)
}

//go:build unix

package main

import (
	"os"
	"runtime/debug"

	"golang.org/x/sys/unix"
)

func silenceStderr() func() {
	saved, err := unix.Dup(2)
	if err != nil {
		return func() {}
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		_ = unix.Close(saved)
		return func() {}
	}
	defer func() { _ = null.Close() }()
	if err := redirect(int(null.Fd()), 2); err != nil {
		_ = unix.Close(saved)
		return func() {}
	}
	crash := os.NewFile(uintptr(saved), "stderr")
	_ = debug.SetCrashOutput(crash, debug.CrashOptions{})
	return func() {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		_ = redirect(saved, 2)
		_ = crash.Close()
	}
}

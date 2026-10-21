//go:build windows

package main

import (
	"os"
	"runtime/debug"

	"golang.org/x/sys/windows"
)

func silenceStderr() (func(), error) {
	original, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err != nil {
		return nil, errIsolation
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, errIsolation
	}
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(null.Fd())); err != nil {
		_ = null.Close()
		return nil, errIsolation
	}
	saved := os.Stderr
	os.Stderr = null
	_ = debug.SetCrashOutput(saved, debug.CrashOptions{})
	return func() {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		os.Stderr = saved
		_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, original)
		_ = null.Close()
	}, nil
}

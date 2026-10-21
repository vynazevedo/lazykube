//go:build windows

package main

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsIsolationSwapsHandleAndStderr(t *testing.T) {
	original, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	restore, err := silenceStderr()
	if err != nil {
		t.Fatal(err)
	}
	current, _ := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	swapped := os.Stderr != saved && current != original
	child := exec.Command("cmd", "/c", "echo SENTINELA-WINDOWS 1>&2")
	child.Stderr = os.Stderr
	_ = child.Run()
	restore()
	if !swapped {
		t.Fatal("the process stderr handle and os.Stderr point to NUL while isolated")
	}
	if back, _ := windows.GetStdHandle(windows.STD_ERROR_HANDLE); back != original || os.Stderr != saved {
		t.Fatal("both are restored at the end")
	}
}

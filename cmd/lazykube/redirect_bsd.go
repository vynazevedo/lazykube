//go:build unix && !linux

package main

import "golang.org/x/sys/unix"

func redirect(from, to int) error {
	return unix.Dup2(from, to)
}

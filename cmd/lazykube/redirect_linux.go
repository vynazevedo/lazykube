//go:build linux

package main

import "golang.org/x/sys/unix"

func redirect(from, to int) error {
	return unix.Dup3(from, to, 0)
}

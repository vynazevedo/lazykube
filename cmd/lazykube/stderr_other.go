//go:build !unix

package main

func silenceStderr() func() {
	return func() {}
}

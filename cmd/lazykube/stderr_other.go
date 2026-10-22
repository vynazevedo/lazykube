//go:build !unix && !windows

package main

func silenceStderr() (func(), error) {
	return nil, errIsolation
}

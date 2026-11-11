package main

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if err := os.Setenv("__FANG_TEST_WIDTH", "100"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

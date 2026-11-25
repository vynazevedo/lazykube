package main

import (
	"os"
	"testing"

	"github.com/vynazevedo/lazykube/internal/testversion"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func TestMain(m *testing.M) {
	if err := os.Setenv("__FANG_TEST_WIDTH", "100"); err != nil {
		panic(err)
	}
	ui.Version, commit = testversion.Golden, ""
	os.Exit(m.Run())
}

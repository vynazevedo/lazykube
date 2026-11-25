package ui

import (
	"os"
	"testing"

	"github.com/vynazevedo/lazykube/internal/testversion"
)

func TestMain(m *testing.M) {
	Version = testversion.Golden
	os.Exit(m.Run())
}

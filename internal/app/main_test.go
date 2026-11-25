package app

import (
	"testing"

	"go.uber.org/goleak"

	"github.com/vynazevedo/lazykube/internal/testversion"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func TestMain(m *testing.M) {
	ui.Version = testversion.Golden
	goleak.VerifyTestMain(m)
}

package netpath

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDerivationHasNoWayToReachTheNetwork(t *testing.T) {
	allowed := map[string]bool{"cmp": true, "context": true, "errors": true, "fmt": true, "net/netip": true, "net/url": true, "slices": true, "strconv": true, "strings": true, "github.com/vynazevedo/lazykube/internal/data": true, "github.com/vynazevedo/lazykube/internal/weburl": true}
	for _, dir := range []string{".", filepath.Join("..", "weburl")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatal(files, err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, spec := range parsed.Imports {
				if path, _ := strconv.Unquote(spec.Path.Value); !allowed[path] {
					t.Errorf("%s imports %s", file, path)
				}
			}
		}
	}
}

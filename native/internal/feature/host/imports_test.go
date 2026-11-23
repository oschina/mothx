package host

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Business feature packages must depend on the shared host contract, never on
// the composition shell. Keeping this static prevents a feature from reaching
// back into desktop for session/run state as the module grows.
func TestFeaturePackagesDoNotImportDesktopShell(t *testing.T) {
	featureRoot := ".."
	err := filepath.WalkDir(featureRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			if strings.Contains(spec.Path.Value, "/internal/desktop") {
				t.Errorf("%s imports the composition shell: %s", path, spec.Path.Value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

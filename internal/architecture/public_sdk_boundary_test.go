package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestPublicSDKMustNotImportInternal enforces the public SDK boundary stated
// in AGENTS.md: the public agent package is consumed by external modules and
// must never import this module's internal packages. Implementation wiring
// belongs in bootstrap/, which external modules blank-import. The examples
// demonstrate correct public SDK usage and follow the same rule.
func TestPublicSDKMustNotImportInternal(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture guard")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	var violations []string
	for _, dir := range []string{"agent", "example"} {
		dirViolations, err := publicSDKInternalImports(root, dir)
		if err != nil {
			t.Fatal(err)
		}
		violations = append(violations, dirViolations...)
	}
	if len(violations) > 0 {
		t.Fatalf("public SDK boundary violations (move wiring to bootstrap/):\n- %s", strings.Join(violations, "\n- "))
	}
}

func publicSDKInternalImports(root, dir string) ([]string, error) {
	var violations []string
	fset := token.NewFileSet()
	err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		fileAST, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imp := range fileAST.Imports {
			importPath, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				continue
			}
			// Any import of this module's internal packages breaks the
			// boundary; external consumers cannot follow such imports.
			if idx := strings.Index(importPath, "/internal/"); idx >= 0 && strings.HasPrefix(importPath, "github.com/oschina/mothx/") {
				violations = append(violations, filepath.ToSlash(rel)+" imports "+importPath)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return violations, nil
}

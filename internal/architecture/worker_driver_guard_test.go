package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWorkerDriverIsolationGuard enforces the Router worker adapter boundary:
// the workerdriver package resolves protocol behavior only. It must not read
// settings, sessions, the database, or Runtime state, or a flavor would become a
// second policy/config owner.
func TestWorkerDriverIsolationGuard(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate guard")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	driverDir := filepath.Join(root, "internal", "agentruntime", "workerdriver")
	entries, err := os.ReadDir(driverDir)
	if err != nil {
		t.Fatalf("read workerdriver: %v", err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(driverDir, entry.Name())
		parsed, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range parsed.Imports {
			imported := strings.Trim(imp.Path.Value, `"`)
			switch imported {
			case "github.com/oschina/mothx/internal/config",
				"github.com/oschina/mothx/internal/session",
				"github.com/oschina/mothx/internal/dao",
				"github.com/oschina/mothx/internal/agentruntime":
				t.Errorf("%s: workerdriver must stay a leaf capability package, but imports %s", entry.Name(), imported)
			}
		}
	}
}

// TestWorkerDriverBusinessCodeHasNoDriverBranches keeps driver identity out of
// business code. The runtime selects an adapter through Lookup; anything else
// that compares a driver string would reintroduce per-agent branching.
func TestWorkerDriverBusinessCodeHasNoDriverBranches(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate guard")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	driverDir := filepath.Join(root, "internal", "agentruntime", "workerdriver")
	fset := token.NewFileSet()
	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasPrefix(path, driverDir+string(filepath.Separator)) {
			return nil
		}
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			binary, ok := node.(*ast.BinaryExpr)
			if !ok || binary.Op != token.EQL && binary.Op != token.NEQ {
				return true
			}
			if mentionsDriverConstant(binary.X) || mentionsDriverConstant(binary.Y) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s: business code branches on a driver identity; resolve capabilities through workerdriver.Lookup instead", rel)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func mentionsDriverConstant(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return strings.HasPrefix(selector.Sel.Name, "WorkerDriver")
}

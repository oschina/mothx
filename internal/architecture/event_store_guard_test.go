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

// TestEventStoreOwnershipGuard keeps the JSONL physical store private to the
// Session/Runtime layers. Adapters may only consume Runtime projections; a
// direct EventStore call would recreate the duplicate replay/write paths this
// migration is specifically removing.
func TestEventStoreOwnershipGuard(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate event store ownership guard")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" || info.Name() == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/session/") || strings.HasPrefix(rel, "internal/agentruntime/") {
			return nil
		}
		fset := token.NewFileSet()
		fileAST, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		imports := map[string]string{}
		for _, imp := range fileAST.Imports {
			name := filepath.Base(strings.Trim(imp.Path.Value, `"`))
			if imp.Name != nil {
				name = imp.Name.Name
			}
			imports[name] = strings.Trim(imp.Path.Value, `"`)
		}
		ast.Inspect(fileAST, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || imports[ident.Name] != "github.com/oschina/mothx/internal/session" {
				return true
			}
			switch selector.Sel.Name {
			case "NewEventStore", "RemoveEventStore", "ForgetEventStoreCaches", "ReadCommittedEventBatches", "AppendPreparedEventBatch", "CommitEventIntent", "ReconcileEventIntents", "RebuildEventProjection", "InspectEventStore", "EventStoreWriteStats":
				violations = append(violations, rel+": direct session."+selector.Sel.Name+"; use SessionRuntime EventStore projection APIs")
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("EventStore access belongs to internal/session and internal/agentruntime:\n- %s", strings.Join(violations, "\n- "))
	}
}

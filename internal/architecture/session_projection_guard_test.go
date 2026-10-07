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

// TestSessionExecutionProjectionHasOneAuthority enforces the AGENTS.md rule that
// Session execution snapshots have a single owner: only internal/agentruntime
// may construct an agentruntime.SessionExecutionSnapshot or write its
// Busy/CanSubmit/CanCancelLocal projection fields. Adapters must consume
// InspectSessionExecution, InspectLocalSessionExecution, or
// UnknownSessionExecution and render the result.
func TestSessionExecutionProjectionHasOneAuthority(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture guard")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	var violations []string
	for _, base := range []string{"internal", "cmd"} {
		walkErr := filepath.Walk(filepath.Join(root, base), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "internal/agentruntime/") {
				return nil
			}
			fileViolations, parseErr := sessionProjectionViolations(path, rel)
			if parseErr != nil {
				return parseErr
			}
			violations = append(violations, fileViolations...)
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("session execution projection must be owned by internal/agentruntime:\n- %s", strings.Join(violations, "\n- "))
	}
}

func sessionProjectionViolations(path, rel string) ([]string, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var violations []string
	mentionsSnapshot := false
	ast.Inspect(parsed, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			if isSessionExecutionSnapshotType(node.Type) {
				violations = append(violations, rel+" constructs agentruntime.SessionExecutionSnapshot")
			}
		case *ast.SelectorExpr:
			if node.Sel != nil && node.Sel.Name == "SessionExecutionSnapshot" {
				mentionsSnapshot = true
			}
		case *ast.Ident:
			if node.Name == "SessionExecutionSnapshot" {
				mentionsSnapshot = true
			}
		}
		return true
	})
	// Only files that already deal with the snapshot type are checked for field
	// writes, so an unrelated struct's Busy field never trips the guard.
	if mentionsSnapshot {
		ast.Inspect(parsed, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				selector, ok := lhs.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				switch selector.Sel.Name {
				case "Busy", "CanSubmit", "CanCancelLocal":
					violations = append(violations, rel+" writes snapshot."+selector.Sel.Name+" outside internal/agentruntime")
				}
			}
			return true
		})
	}
	return violations, nil
}

func isSessionExecutionSnapshotType(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name == "SessionExecutionSnapshot"
	case *ast.SelectorExpr:
		return typed.Sel != nil && typed.Sel.Name == "SessionExecutionSnapshot"
	default:
		return false
	}
}

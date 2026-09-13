package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// foreignKeyEnforcementPattern matches every SQLite spelling that turns foreign
// key enforcement ON: the mattn DSN _pragma forms foreign_keys(1)/(ON)/(TRUE)
// and PRAGMA foreign_keys = 1/ON/TRUE, case-insensitively. Values that disable
// enforcement (0, OFF, FALSE) intentionally do not match. internal/db/db.go is
// the only file allowed to contain it; the canonical session database policy
// keeps enforcement OFF.
var foreignKeyEnforcementPattern = regexp.MustCompile(`(?i)foreign_keys\s*(?:\(\s*(?:1|on|true)\s*\)|=\s*(?:1|on|true))`)

// TestProductionArchitectureGuard prevents adapters from silently reintroducing
// complete Agent construction or canonical Run persistence. The allowlist is
// intentionally small and documents migration ownership rather than package
// convenience.
func TestProductionArchitectureGuard(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture guard")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	violations, err := productionArchitectureViolations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("production architecture violations:\n- %s", strings.Join(violations, "\n- "))
	}
}

func productionArchitectureViolations(root string) ([]string, error) {
	var violations []string
	fset := token.NewFileSet()
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
		// SQLite foreign key enforcement is owned solely by internal/db, which
		// keeps it OFF for the canonical session database and exposes an opt-in
		// for private, rebuildable derived stores. No other production file may
		// turn it ON in any spelling (foreign_keys(1)/(ON)/(TRUE) DSN pragmas or
		// PRAGMA foreign_keys = ON/1/TRUE), or it would silently re-activate the
		// dormant REFERENCES clauses on canonical session data that project
		// policy keeps in the repository layer instead.
		if filepath.ToSlash(rel) != "internal/db/db.go" {
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if foreignKeyEnforcementPattern.Match(content) {
				violations = append(violations, fmt.Sprintf("%s: SQLite foreign key enforcement is owned by internal/db; do not enable foreign_keys here", rel))
			}
		}
		skipConstructionChecks := strings.HasPrefix(filepath.ToSlash(rel), "internal/agentruntime/")
		fileAST, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		imports := make(map[string]string)
		for _, imp := range fileAST.Imports {
			pathValue := strings.Trim(imp.Path.Value, `"`)
			if pathValue == "github.com/startvibecoding/mothx/internal/commondb" {
				violations = append(violations, fmt.Sprintf("%s: internal/commondb is removed; use internal/db plus internal/dao", rel))
			}
			if pathValue == "database/sql" && !isSchemaOrDatabaseOwner(rel) {
				violations = append(violations, fmt.Sprintf("%s: database/sql is restricted to internal/db, internal/dao, and schema migrations", rel))
			}
			if pathValue == "github.com/uptrace/bun" && !isBunOwner(rel) {
				violations = append(violations, fmt.Sprintf("%s: uptrace/bun is restricted to internal/db and internal/dao; use DAO methods from business code", rel))
			}
			name := ""
			if imp.Name != nil {
				name = imp.Name.Name
			} else {
				name = filepath.Base(pathValue)
			}
			imports[name] = pathValue
		}
		// Database execution is owned by internal/db and internal/dao. Keep
		// migration/schema SQL explicit, but reject direct SQL handles in every
		// other production package. The receiver-name check avoids confusing
		// HTTP's URL.Query with database queries.
		if !isSchemaOrDatabaseOwner(rel) {
			ast.Inspect(fileAST, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !isDirectSQLMethod(selector.Sel.Name) || !isDatabaseReceiver(selector.X) {
					return true
				}
				violations = append(violations, fmt.Sprintf("%s: direct database %s; move SQL into internal/dao", rel, selector.Sel.Name))
				return true
			})
		}
		// The durable decision ledger has exactly one schema: the
		// decision_<status> event name and the {"decision": record} envelope. The
		// name is owned by internal/session (the run-event schema owner) and the
		// envelope by internal/agentruntime. Every other production file must go
		// through the agentruntime helpers (BuildDecisionEvent, DecodeDecisionEvent,
		// LoadDecisionRecords) instead of re-deriving them, so a future move to a
		// dedicated store stays local to that package.
		relSlash := filepath.ToSlash(rel)
		ast.Inspect(fileAST, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.BasicLit:
				if n.Kind == token.STRING && n.Value == `"decision_"` && !strings.HasPrefix(relSlash, "internal/session/") {
					violations = append(violations, fmt.Sprintf("%s: decision event names belong to internal/session; use agentruntime.BuildDecisionEvent", rel))
				}
			case *ast.KeyValueExpr:
				key, ok := n.Key.(*ast.BasicLit)
				if ok && key.Kind == token.STRING && key.Value == `"decision"` && !strings.HasPrefix(relSlash, "internal/agentruntime/") {
					violations = append(violations, fmt.Sprintf("%s: the decision envelope belongs to internal/agentruntime; use agentruntime.DecisionEventFields", rel))
				}
			}
			return true
		})
		if skipConstructionChecks {
			return nil
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
			if isBunQueryBuilderMethod(selector.Sel.Name) && !isSchemaOrDatabaseOwner(rel) {
				violations = append(violations, fmt.Sprintf("%s: direct Bun query builder %s; move SQL into internal/dao", rel, selector.Sel.Name))
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			pkgPath := imports[ident.Name]
			switch {
			case pkgPath == "github.com/startvibecoding/mothx/internal/agent" && (selector.Sel.Name == "New" || selector.Sel.Name == "NewWithLoopConfig"):
				violations = append(violations, fmt.Sprintf("%s: direct %s.%s; use SessionRuntime.BuildAgent/BuildTransientAgent", rel, ident.Name, selector.Sel.Name))
			case pkgPath == "github.com/startvibecoding/mothx/internal/session" && isCanonicalRunPersistence(selector.Sel.Name):
				violations = append(violations, fmt.Sprintf("%s: direct session.%s; use ExecutionRuntime/RunStore", rel, selector.Sel.Name))
			case pkgPath == "github.com/startvibecoding/mothx/internal/session" && isCanonicalRunQuery(selector.Sel.Name):
				violations = append(violations, fmt.Sprintf("%s: direct session.%s; use agentruntime durable query boundary", rel, selector.Sel.Name))
			case pkgPath == "github.com/startvibecoding/mothx/internal/session" && isLegacyRuntimeLeaseAPI(selector.Sel.Name) && !legacyRuntimeLeaseBridgeFiles[filepath.ToSlash(rel)]:
				violations = append(violations, fmt.Sprintf("%s: new use of legacy session.%s; use an explicit admission/execution/recovery/mutation lease API", rel, selector.Sel.Name))
			case isLegacyAttachmentDeliveryAPI(selector.Sel.Name):
				violations = append(violations, fmt.Sprintf("%s: new use of legacy attachment delivery API %s; use DeliveryCoordinator/DeliveryOperation", rel, selector.Sel.Name))
			}
			return true
		})
		ast.Inspect(fileAST, func(node ast.Node) bool {
			fn, ok := node.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			runStores := collectRunStoreIdentifiers(fn, imports)
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !isCanonicalRunStoreMethod(selector.Sel.Name) {
					return true
				}
				if !isRunStoreExpression(selector.X, imports, runStores) {
					return true
				}
				violations = append(violations, fmt.Sprintf("%s: direct agentruntime.RunStore.%s; use ExecutionRuntime durable lifecycle methods", rel, selector.Sel.Name))
				return true
			})
			return false
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(violations)
	return violations, nil
}

func isDirectSQLMethod(name string) bool {
	switch name {
	case "Query", "QueryRow", "QueryContext", "QueryRowContext", "Exec", "ExecContext":
		return true
	default:
		return false
	}
}

func isBunQueryBuilderMethod(name string) bool {
	switch name {
	case "NewSelect", "NewInsert", "NewUpdate", "NewDelete", "NewRaw":
		return true
	default:
		return false
	}
}

func isDatabaseReceiver(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	switch ident.Name {
	case "db", "tx", "database", "sqlDB", "rootDB", "sessionDB", "first", "second", "reopened":
		return true
	default:
		return false
	}
}

func isSchemaOrDatabaseOwner(rel string) bool {
	rel = filepath.ToSlash(rel)
	return rel == "internal/session/schema.go" ||
		rel == "internal/session/migrations.go" ||
		strings.HasPrefix(rel, "internal/db/") ||
		strings.HasPrefix(rel, "internal/dao/")
}

func isBunOwner(rel string) bool {
	rel = filepath.ToSlash(rel)
	return strings.HasPrefix(rel, "internal/db/") || strings.HasPrefix(rel, "internal/dao/")
}

func isCanonicalRunPersistence(name string) bool {
	switch name {
	case "SaveSessionRun", "CreateSessionRun", "UpdateSessionRunStatus", "SaveSessionRunEvent":
		return true
	default:
		return false
	}
}

func isCanonicalRunQuery(name string) bool {
	switch name {
	case "GetSessionRun", "GetSessionRunContext", "GetActiveSessionRun", "GetActiveSessionRunContext":
		return true
	default:
		return false
	}
}

// Legacy runtime lease helpers have no production allowlist. New production
// code must use the explicit admission/execution/recovery/mutation lease APIs.
var legacyRuntimeLeaseBridgeFiles = map[string]bool{}

func isLegacyRuntimeLeaseAPI(name string) bool {
	switch name {
	case "TryLockRuntime", "LockRuntime", "TryLockRuntimes":
		return true
	default:
		return false
	}
}

// These legacy delivery methods are forbidden in production. Adapter code
// must use the schema 34 delivery intent/operation coordinator instead.
func isLegacyAttachmentDeliveryAPI(name string) bool {
	switch name {
	case "ProjectDeliveries", "BeginDelivery", "FinishDelivery":
		return true
	default:
		return false
	}
}

func isCanonicalRunStoreMethod(name string) bool {
	switch name {
	case "Create", "Update", "Finish":
		return true
	default:
		return false
	}
}

func collectRunStoreIdentifiers(fn *ast.FuncDecl, imports map[string]string) map[string]struct{} {
	result := make(map[string]struct{})
	collectRunStoreFields(fn.Type.Params, imports, result)

	changed := true
	for changed {
		changed = false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.AssignStmt:
				for i, rhs := range value.Rhs {
					if i >= len(value.Lhs) || !isRunStoreExpression(rhs, imports, result) {
						continue
					}
					ident, ok := value.Lhs[i].(*ast.Ident)
					if !ok {
						continue
					}
					if _, exists := result[ident.Name]; !exists {
						result[ident.Name] = struct{}{}
						changed = true
					}
				}
			case *ast.ValueSpec:
				if isRunStoreType(value.Type, imports) {
					for _, name := range value.Names {
						if _, exists := result[name.Name]; !exists {
							result[name.Name] = struct{}{}
							changed = true
						}
					}
				}
				for i, rhs := range value.Values {
					if i >= len(value.Names) || !isRunStoreExpression(rhs, imports, result) {
						continue
					}
					if _, exists := result[value.Names[i].Name]; !exists {
						result[value.Names[i].Name] = struct{}{}
						changed = true
					}
				}
			}
			return true
		})
	}
	return result
}

func collectRunStoreFields(fields *ast.FieldList, imports map[string]string, result map[string]struct{}) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		if !isRunStoreType(field.Type, imports) {
			continue
		}
		for _, name := range field.Names {
			result[name.Name] = struct{}{}
		}
	}
}

func isRunStoreExpression(expr ast.Expr, imports map[string]string, identifiers map[string]struct{}) bool {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return isRunStoreExpression(value.X, imports, identifiers)
	case *ast.UnaryExpr:
		return value.Op == token.AND && isRunStoreExpression(value.X, imports, identifiers)
	case *ast.CompositeLit:
		return isRunStoreType(value.Type, imports)
	case *ast.Ident:
		_, ok := identifiers[value.Name]
		return ok
	default:
		return false
	}
}

func isRunStoreType(expr ast.Expr, imports map[string]string) bool {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return isRunStoreType(value.X, imports)
	case *ast.StarExpr:
		return isRunStoreType(value.X, imports)
	case *ast.SelectorExpr:
		ident, ok := value.X.(*ast.Ident)
		return ok && imports[ident.Name] == "github.com/startvibecoding/mothx/internal/agentruntime" && value.Sel.Name == "RunStore"
	default:
		return false
	}
}

func TestProductionArchitectureGuardDetectsCanonicalRunBypasses(t *testing.T) {
	tests := []struct {
		name string
		path string
		src  string
		want string
	}{
		{
			name: "session create",
			path: "internal/serve/adapter.go",
			src: `package serve
import sessiondb "github.com/startvibecoding/mothx/internal/session"
func persist() { _ = sessiondb.CreateSessionRun("", sessiondb.SessionRun{}) }
`,
			want: "direct session.CreateSessionRun",
		},
		{
			name: "session run query",
			path: "internal/serve/adapter.go",
			src: `package serve
import sessiondb "github.com/startvibecoding/mothx/internal/session"
func inspect() { _, _ = sessiondb.GetSessionRun("", "run") }
`,
			want: "direct session.GetSessionRun",
		},
		{
			name: "composite run store",
			path: "internal/serve/adapter.go",
			src: `package serve
import runtimepkg "github.com/startvibecoding/mothx/internal/agentruntime"
func persist() { _ = (runtimepkg.RunStore{}).Finish("run", runtimepkg.RunStateFailed, "") }
`,
			want: "direct agentruntime.RunStore.Finish",
		},
		{
			name: "local run store",
			path: "internal/serve/adapter.go",
			src: `package serve
import runtimepkg "github.com/startvibecoding/mothx/internal/agentruntime"
func persist() { store := runtimepkg.RunStore{}; _ = store.Update("run", runtimepkg.RunStateRunning, "") }
`,
			want: "direct agentruntime.RunStore.Update",
		},
		{
			name: "legacy runtime lease",
			path: "internal/serve/new_adapter.go",
			src: `package serve
import sessiondb "github.com/startvibecoding/mothx/internal/session"
func reserve() { _, _ = sessiondb.TryLockRuntime("", "session") }
`,
			want: "new use of legacy session.TryLockRuntime",
		},
		{
			name: "legacy attachment delivery",
			path: "internal/serve/new_adapter.go",
			src: `package serve
func project(service interface{ BeginDelivery() }) { service.BeginDelivery() }
`,
			want: "new use of legacy attachment delivery API BeginDelivery",
		},
		{
			name: "direct SQL handle",
			path: "internal/serve/new_adapter.go",
			src: `package serve
func persist(db interface{ ExecContext(...any) }) { db.ExecContext("UPDATE sessions SET cwd = ''") }
`,
			want: "direct database ExecContext",
		},
		{
			name: "foreign key enforcement DSN pragma",
			path: "internal/serve/adapter.go",
			src: `package serve
func open() { _ = "file:db?_pragma=foreign_keys(ON)" }
`,
			want: "SQLite foreign key enforcement is owned by internal/db",
		},
		{
			name: "foreign key enforcement equality pragma",
			path: "internal/serve/adapter.go",
			src: `package serve
func open() { _ = "PRAGMA foreign_keys = 1" }
`,
			want: "SQLite foreign key enforcement is owned by internal/db",
		},
		{
			name: "foreign key enforcement lowercase pragma",
			path: "internal/serve/adapter.go",
			src: `package serve
func open() { _ = "pragma foreign_keys = true" }
`,
			want: "SQLite foreign key enforcement is owned by internal/db",
		},
		{
			name: "foreign key disable pragma is allowed",
			path: "internal/serve/adapter.go",
			src: `package serve
func open() { _ = "PRAGMA foreign_keys = OFF" }
`,
		},
		{
			name: "bare foreign_keys pragma without enable value is allowed",
			path: "internal/serve/adapter.go",
			src: `package serve
func open() { _ = "PRAGMA foreign_keys" }
`,
		},
		{
			name: "runtime store wiring is allowed",
			path: "internal/serve/adapter.go",
			src: `package serve
import runtimepkg "github.com/startvibecoding/mothx/internal/agentruntime"
func wire(execution *runtimepkg.ExecutionRuntime) { execution.SetRunStore(runtimepkg.RunStore{}) }
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(tt.path))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.src), 0o600); err != nil {
				t.Fatal(err)
			}
			violations, err := productionArchitectureViolations(root)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(violations, "\n")
			if tt.want == "" {
				if joined != "" {
					t.Fatalf("unexpected violations:\n%s", joined)
				}
				return
			}
			if !strings.Contains(joined, tt.want) {
				t.Fatalf("violations %q do not contain %q", joined, tt.want)
			}
		})
	}
}

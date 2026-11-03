package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Content-store routing is a single-owner decision. These guards keep the
// session content store from drifting back to the three-judge state the
// split-brain remediation removed: an in-memory flag, a physical log probe, and
// a persisted column each deciding independently where a write lands.
//
// The authoritative rule is:
//
//   - the persisted `sessions.content_store` column decides, read only through
//     Manager.resolveContentStoreLocked;
//   - opening/loading a session never flips the store or retires legacy payload
//     rows (both are fenced writes owned by migration/attach);
//   - no write path branches on Manager.eventStoreMode to pick a store.
//
// See docs/proposal/session-content-store-consistency-remediation-proposal.md.

const sessionPackageDir = "internal/session"

// contentStoreResolverFunc is the only function allowed to consult the
// persisted column for write routing.
const contentStoreResolverFunc = "resolveContentStoreLocked"

// TestContentStoreResolverIsTheOnlyWriteRouter keeps the persisted-column read
// behind the one resolver. A second caller would recreate independent routing.
func TestContentStoreResolverIsTheOnlyWriteRouter(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	walkProductionGoFiles(t, root, func(rel string, fileAST *ast.File) {
		for _, decl := range fileAST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch selector.Sel.Name {
				case "persistedContentStoreLocked":
					if rel != sessionPackageDir+"/session.go" || fn.Name.Name != contentStoreResolverFunc {
						violations = append(violations, rel+": "+fn.Name.Name+" calls persistedContentStoreLocked; the persisted content store must be resolved only by "+contentStoreResolverFunc)
					}
				case "ContentStore":
					// The DAO accessor is the only way to read the column. It may
					// be consumed by the resolver and by the two read-projections
					// that answer "which transcript should I replay", never by an
					// adapter or another write router.
					if !strings.HasPrefix(rel, sessionPackageDir+"/") {
						violations = append(violations, rel+": "+fn.Name.Name+" reads sessions.content_store directly; use the Runtime/Session projection")
						return true
					}
					if fn.Name.Name != "sessionUsesEventStore" && fn.Name.Name != "entryContentStoreFlippedTx" {
						violations = append(violations, rel+": "+fn.Name.Name+" reads sessions.content_store outside the resolver and its two read-projections")
					}
				}
				return true
			})
		}
	})
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("content-store reads bypass the single resolver:\n- %s", strings.Join(violations, "\n- "))
	}
}

// TestContentStoreReadProjectionsAreAllowlisted closes the second bypass: the
// package-level sessionUsesEventStore helper reads the persisted column for
// read-only projections and DB-level durable run gates. Every production caller
// must be named here, so a new caller cannot quietly become a second writer.
func TestContentStoreReadProjectionsAreAllowlisted(t *testing.T) {
	root := repoRoot(t)
	allowed := map[string]string{
		"persistedContentStoreLocked":             "the single write router",
		"sessionContentStoreFlipped":              "migration/attach gate; the persisted column is the authority",
		"adoptLegacyDeltaLocked":                  "delta adoption gate; refuses to adopt into a session that is not flipped",
		"convergeEventStoreLocked":                "convergence gate; decides whether an open must observe the canonical log",
		"createExecutionIntentAndSessionRunEvent": "durable run admission gate, already inside the run-store transaction",
		"createSessionRunAndEvent":                "durable run start gate, already inside the run-store transaction",
		"LatestSessionTitle":                      "read-only list projection",
		"ListSessionMessagesWithSeq":              "read-only transcript projection",
		"ListSessionMessagesAfter":                "read-only transcript projection",
		"ListSessionMessagesLatest":               "read-only transcript projection",
		"ListSessionMessagesBefore":               "read-only transcript projection",
	}
	var violations []string
	walkProductionGoFiles(t, root, func(rel string, fileAST *ast.File) {
		if !strings.HasPrefix(rel, sessionPackageDir+"/") {
			return
		}
		for _, decl := range fileAST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := ""
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					name = fun.Name
				case *ast.SelectorExpr:
					name = fun.Sel.Name
				}
				if name != "sessionUsesEventStore" && name != "sessionContentStoreFlipped" {
					return true
				}
				if _, ok := allowed[fn.Name.Name]; !ok {
					violations = append(violations, rel+": "+fn.Name.Name+" reads the persisted content store; use "+contentStoreResolverFunc)
				}
				return true
			})
		}
	})
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("content-store read projections are not allowlisted:\n- %s", strings.Join(violations, "\n- "))
	}
}

// TestSessionOpenNeverMutatesContentStore proves an open is observational. The
// functions reachable from load/reload/convergence must not reach a store flip,
// a legacy-payload retire, an export, or an EventStore append. Those are fenced
// writes whose only owners are the migration/attach path and the append path.
func TestSessionOpenNeverMutatesContentStore(t *testing.T) {
	root := repoRoot(t)
	graph, funcs := sessionCallGraph(t, root)
	roots := []string{"loadLocked", "reloadLocked", "convergeEventStoreLocked", "convergeFlippedSession", "reportLegacyPayloadStateLocked"}
	denied := map[string]string{
		"retireLegacyPayload":        "legacy payload retirement requires a content lease and belongs to the attach/migration path",
		"retireLegacyPayloadRows":    "legacy payload retirement requires a content lease and belongs to the attach/migration path",
		"adoptLegacyDeltaLocked":     "delta adoption is a fenced write owned by AdoptLegacyContent",
		"exportLegacyTranscript":     "export is the irreversible migration owner, never an open side effect",
		"flipSessionContentStore":    "a store flip belongs to a committed migration batch",
		"SetContentStoreFlipped":     "a store flip belongs to a committed migration batch",
		"FlipContentStoreOnLease":    "a store flip belongs to a committed migration batch",
		"DeleteLegacyEntries":        "unconditional legacy deletion is retired; use DeleteCoveredLegacyEntries under a lease",
		"DeleteCoveredLegacyEntries": "legacy deletion is a fenced retire, never an open side effect",
		"AppendPreparedEventBatch":   "open must not append to the EventStore",
		"CommitEventIntent":          "open must not commit EventStore intents",
		"RemoveEventStore":           "open must never reset the canonical log",
	}

	seen := map[string]bool{}
	flagged := map[string]bool{}
	var violations []string
	flag := func(name, reason string) {
		if flagged[name] {
			return
		}
		flagged[name] = true
		violations = append(violations, name+": "+reason)
	}
	var visit func(fn string)
	visit = func(fn string) {
		if seen[fn] {
			return
		}
		seen[fn] = true
		if reason, bad := denied[fn]; bad {
			flag(fn, reason)
		}
		for _, callee := range graph[fn] {
			// DAO methods live outside this package, so they never appear in the
			// local call graph. Check the deny list before the membership test or
			// every cross-package mutation would be silently skipped.
			if reason, bad := denied[callee]; bad {
				flag(callee, reason)
			}
			if _, known := funcs[callee]; known {
				visit(callee)
			}
		}
	}
	for _, rootFn := range roots {
		if !funcs[rootFn] {
			violations = append(violations, "missing guard root "+rootFn+"; update this guard when renaming the open path")
		}
		visit(rootFn)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("session open reaches content-store mutations:\n- %s", strings.Join(violations, "\n- "))
	}
}

// TestEventStoreModeDoesNotRouteWrites pins the in-memory-mode readers. Once a
// handle has adopted the EventStore it never moves back, so reading
// Manager.eventStoreMode is safe only for the resolver's hot path and for
// functions whose entire job is an EventStore-only operation. Any other reader
// is a potential "in-memory flag decides the write target" regression.
func TestEventStoreModeDoesNotRouteWrites(t *testing.T) {
	root := repoRoot(t)
	// Each entry names the one reason the read is safe. Adding a reader requires
	// updating this list with the same justification.
	allowed := map[string]string{
		"resolveContentStoreLocked":     "returns the resolved store; once true the session never moves back",
		"exportLegacyTranscript":        "idempotent no-op on an already migrated handle",
		"MigrateToEventStoreOutcome":    "attach-path post-convergence cleanup, guarded by the fenced retire and its coverage probe",
		"AppendEventStoreEntries":       "explicit EventStore append entry point; the caller already selected that store",
		"appendEventStoreEntriesLocked": "sanity check that a legacy handle cannot jump stores; the append below is unconditionally the EventStore",
		"ensureDisplayProjection":       "EventStore-only display projection with a maintenance backfill fallback; never routes transcript content",
		"inspectCommittedEventEntries":  "defaults a flipped-but-empty session to the EventStore instead of falling back to a stale legacy copy",
		"projectDisplayEntries":         "read-only display projection; selects a replay source, never a write target",
	}
	var violations []string
	walkProductionGoFiles(t, root, func(rel string, fileAST *ast.File) {
		if !strings.HasPrefix(rel, sessionPackageDir+"/") {
			return
		}
		for _, decl := range fileAST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			assigned := map[token.Pos]bool{}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				assign, ok := node.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, lhs := range assign.Lhs {
					if selector, ok := lhs.(*ast.SelectorExpr); ok {
						assigned[selector.Sel.Pos()] = true
					}
				}
				return true
			})
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := selector.X.(*ast.Ident)
				if !ok || ident.Name != "m" || selector.Sel.Name != "eventStoreMode" {
					return true
				}
				if assigned[selector.Sel.Pos()] {
					return true
				}
				if _, ok := allowed[fn.Name.Name]; !ok {
					violations = append(violations, rel+": "+fn.Name.Name+" branches on Manager.eventStoreMode; writes must use "+contentStoreResolverFunc)
				}
				return true
			})
		}
	})
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("in-memory content mode routes a write:\n- %s", strings.Join(violations, "\n- "))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture guard")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func walkProductionGoFiles(t *testing.T, root string, visit func(rel string, fileAST *ast.File)) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "dist", "bin":
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
		fileAST, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		visit(rel, fileAST)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// sessionCallGraph maps each function in internal/session to the identifiers it
// invokes. Selector calls are recorded by method/function name, which is enough
// to follow same-package delegation without a type checker.
func sessionCallGraph(t *testing.T, root string) (map[string][]string, map[string]bool) {
	t.Helper()
	graph := map[string][]string{}
	funcs := map[string]bool{}
	walkProductionGoFiles(t, root, func(rel string, fileAST *ast.File) {
		if !strings.HasPrefix(rel, sessionPackageDir+"/") {
			return
		}
		for _, decl := range fileAST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			funcs[name] = true
			var calls []string
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					calls = append(calls, fun.Name)
				case *ast.SelectorExpr:
					calls = append(calls, fun.Sel.Name)
				}
				return true
			})
			graph[name] = calls
		}
	})
	return graph, funcs
}

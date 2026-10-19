package architecture

import (
	"fmt"
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

// The serve background-run paths hold one pool drain slot from before a Run is
// durably admitted until the executor has finished every deferred step
// (internal/serve/openaiapi/session_mgr.go: ReserveBackground/LaunchBackground).
// Shutdown only waits on that slot, so the slot must be released strictly after
// the last durable write of the run. That reduces to one ordering invariant:
//
//	Every executor reached from LaunchBackground must register the hand-off
//	release as its FIRST defer, which makes it the outermost defer and therefore
//	the last thing to run.
//
// Defers run inside-out, so anything registered afterwards (closing the artifact
// collector, cancelling the run context, the fallback terminal write, FinalizeRun)
// is covered by the drain; anything registered before it is not. Breaking this
// still compiles and still passes functional tests until a real shutdown races a
// live run, at which point Shutdown returns while the executor is still writing
// into a session directory the same process is closing.
//
// The companion invariant — that the drain slot does not travel inside
// runtimeRelease on the path that reaches the executor — is enforced structurally
// rather than by AST matching: LaunchBackground receives the hand-off release as
// its first argument, and the executor takes it as a distinct parameter position,
// so a fold into runtimeRelease would have to drop the parameter. The pre-launch
// fold at the reservation sites (handler_run_submit.go, background_external.go)
// is deliberate and correct: it lets every admission early-return hand the slot
// back through the already-existing runtimeRelease call, and post-launch the
// chained copy is dead because the executor only ever calls the separately
// threaded hand-off release.
//
// An end-to-end harness cannot pin this. The approval-blocking fixture used while
// developing this guard emits a tool call, parks forever in RequestToolApproval,
// and re-emits the same call on each resolution, so it can never reach the
// executor's deferred tail; a fixture fast enough to terminalize deterministically
// would make the assertion vacuous. Hence the static guard.

// drainReleaseParams names the identifiers that carry the pool drain hand-off
// release. The submit and Responses executors spell it handOffRelease, the
// recovery monitor spells it drainRelease.
var drainReleaseParams = map[string]bool{
	"handOffRelease": true,
	"drainRelease":   true,
}

// drainLeaves names the terminal executors: the functions that actually run the
// deferred tail (artifact close, context cancel, durable terminal write,
// FinalizeRun) and therefore must hold the drain slot until every one of those
// steps is done. Everything else in the chain is a wrapper that only forwards.
var drainLeaves = map[string]bool{
	"executeBackgroundRun":                    true,
	"executeResponsesBackgroundRunWithConfig": true,
	"monitorRecoveredResponsesBackgroundRun":  true,
}

// drainParamPosition returns the index of the parameter carrying the pool drain
// release in fn's signature, derived from the declaration itself rather than a
// hand-written table. Deriving it means an added or reordered leading parameter
// cannot silently move the slot out of the guard's reach; the name still has to be
// one of drainReleaseParams, which is the single naming convention these paths use.
func drainParamPosition(fn *ast.FuncDecl) int {
	if fn == nil || fn.Type.Params == nil {
		return -1
	}
	i := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			i++
			continue
		}
		for _, name := range field.Names {
			if drainReleaseParams[name.Name] {
				return i
			}
			i++
		}
	}
	return -1
}

func isDrainLeaf(name string) bool { return drainLeaves[name] }

// TestServeExecutorsRegisterHandOffReleaseFirst walks every task handed to
// SessionPool.LaunchBackground and checks that the drain slot is forwarded to a
// known executor which registers it as its first defer.
//
// The package's non-test sources are walked rather than a hand-picked file list,
// so a background path added to a new file cannot escape the guard. Call sites are
// recognised by the `LaunchBackground` selector, which pins them to
// SessionPool.LaunchBackground (internal/serve/openaiapi/session_mgr.go); the
// method declaration contributes no selector node, so it is not mistaken for a
// call site.
func TestServeExecutorsRegisterHandOffReleaseFirst(t *testing.T) {
	root := architectureRepoRoot(t)
	dir := filepath.Join(root, "internal", "serve", "openaiapi")
	fset := token.NewFileSet()
	index := packageFuncDecls(t, fset, dir)
	files := packageSourceFiles(t, fset, dir)

	var violations []string
	launched := 0
	reached := map[string]bool{}
	for _, parsed := range files {
		path := parsed.Name.String()
		rel := repoRelative(root, path)
		for _, task := range launchBackgroundTasks(fset, index, parsed) {
			launched++
			for name := range collectReachable(fset, index, task.callee, map[string]bool{}) {
				reached[name] = true
			}
			violations = append(violations, checkExecutor(fset, index, task, rel, map[string]bool{})...)
		}
	}
	if launched == 0 {
		t.Fatal("no LaunchBackground call sites found; the guard would be vacuous")
	}
	// Every declaration that takes the drain release must be reachable from a call
	// site. This is the non-vacuity half: a renamed parameter, or a new executor
	// wired through a path the walk does not recognise, would otherwise shrink the
	// checked set silently while the test still passed.
	for name, fn := range index {
		if drainParamPosition(fn) < 0 || reached[name] {
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"%s declares a drain-release parameter but is not reachable from any LaunchBackground call site, so its defer ordering is unchecked", name))
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("executor drain-release ordering violated:\n- %s", strings.Join(violations, "\n- "))
	}
}

// collectReachable returns every executor name transitively delegated to from the
// named function, following the same wrapper chain checkExecutor walks. It exists
// only to build the reachability set for the non-vacuity assertion.
func collectReachable(fset *token.FileSet, index map[string]*ast.FuncDecl, name string, seen map[string]bool) map[string]bool {
	out := map[string]bool{}
	fn := index[name]
	if fn == nil || fn.Body == nil || seen[name] {
		return out
	}
	seen[name] = true
	if drainParamPosition(fn) >= 0 {
		out[name] = true
	}
	for _, next := range trackedCalls(fset, index, fn.Body) {
		for k := range collectReachable(fset, index, next.name, seen) {
			out[k] = true
		}
	}
	return out
}

// launchTask describes one unit of work handed to LaunchBackground: the executor
// the closure invokes and the argument position carrying the drain release.
type launchTask struct {
	callee string // named executor invoked by the closure ("" if none)
	argPos int    // positional index of the drain release inside that call
	line   int    // LaunchBackground call site, for messages
}

// checkExecutor verifies one unit of handed-off work. A wrapper forwards the slot
// onward without deferring it, so the walk recurses until it reaches a leaf that
// runs the deferred tail; at that leaf the first defer must be the drain release.
func checkExecutor(fset *token.FileSet, index map[string]*ast.FuncDecl, task launchTask, rel string, visited map[string]bool) []string {
	name := task.callee
	if name == "" {
		return []string{fmt.Sprintf(
			"%s:%d hands the pool drain slot to a closure that calls no tracked executor; either it delegates nowhere, or the executor's drain parameter was renamed away from handOffRelease/drainRelease",
			rel, task.line)}
	}
	if visited[name] {
		return nil // already verified on the path that first reached it
	}
	visited[name] = true

	fn := index[name]
	if fn == nil || fn.Body == nil {
		return []string{fmt.Sprintf("%s:%d hands work to %s which is not a non-test function in internal/serve/openaiapi", rel, task.line, name)}
	}
	loc := fmt.Sprintf("%s: %s", rel, name)
	wantPos := drainParamPosition(fn)
	if wantPos < 0 {
		return []string{fmt.Sprintf("%s:%d calls %s which takes no drain-release parameter, so the slot is dropped", rel, task.line, name)}
	}
	if task.argPos != wantPos {
		return []string{fmt.Sprintf(
			"%s:%d passes the drain release to %s at argument %d, but its signature declares it at %d",
			rel, task.line, name, task.argPos, wantPos)}
	}

	if !isDrainLeaf(name) {
		// Wrapper: forward through every tracked callee it invokes.
		targets := trackedCalls(fset, index, fn.Body)
		if len(targets) == 0 {
			return []string{fmt.Sprintf("%s forwards the drain release but delegates to no tracked executor, so the slot is never handed back", loc)}
		}
		var out []string
		for _, next := range targets {
			out = append(out, checkExecutor(fset, index, launchTask{callee: next.name, argPos: next.drainArg, line: next.line}, rel, visited)...)
		}
		return out
	}

	// Leaf: its first defer must be the drain release, making it the outermost.
	first, isFirstClosure := firstDeferCall(fn.Body)
	if isFirstClosure {
		return []string{fmt.Sprintf("%s defers an anonymous closure first, so the drain release cannot be proven outermost", loc)}
	}
	if first == "" {
		return []string{fmt.Sprintf("%s is a leaf executor with no defer statement, so the drain slot is never handed back", loc)}
	}
	if !drainReleaseParams[first] {
		return []string{fmt.Sprintf(
			"%s defers %s before the drain release; the hand-off release must be registered first so it runs last",
			loc, first)}
	}
	return nil
}

// calledTarget is one direct call in a body whose callee participates in the
// drain chain, with the argument position carrying the drain release.
type calledTarget struct {
	name     string
	line     int
	drainArg int
}

// trackedCalls returns the calls in body that take a drain-release parameter,
// recording which argument position carries it. Matching on the identifier name
// is what makes a simultaneous rename on both sides detectable: the guard compares
// the caller's argument against the callee's declared position, not against a
// string.
func trackedCalls(fset *token.FileSet, index map[string]*ast.FuncDecl, body *ast.BlockStmt) []calledTarget {
	if body == nil {
		return nil
	}
	var out []calledTarget
	seenName := map[string]bool{}
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calleeName(call)
		if name == "" || seenName[name] {
			return true
		}
		fn := index[name]
		if fn == nil {
			return true
		}
		want := drainParamPosition(fn)
		if want < 0 {
			return true
		}
		seenName[name] = true
		got := -1
		if want < len(call.Args) {
			if _, ok := call.Args[want].(*ast.Ident); ok {
				got = want
			}
		}
		out = append(out, calledTarget{name: name, line: fset.Position(call.Pos()).Line, drainArg: got})
		return true
	})
	return out
}

// launchBackgroundTasks collects, for every SessionPool.LaunchBackground call in
// the file, the executor named inside its closure and the argument position that
// carries the drain release.
func launchBackgroundTasks(fset *token.FileSet, index map[string]*ast.FuncDecl, file *ast.File) []launchTask {
	var out []launchTask
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "LaunchBackground" {
			return true
		}
		line := fset.Position(call.Pos()).Line
		for _, arg := range call.Args {
			lit, ok := arg.(*ast.FuncLit)
			if !ok {
				continue
			}
			targets := trackedCalls(fset, index, lit.Body)
			if len(targets) == 0 {
				// Distinguish "the closure calls nothing" from "the closure calls an
				// executor whose drain parameter has been renamed away from the
				// convention". The latter is far more likely and reads very differently.
				out = append(out, launchTask{callee: untrackedCallee(lit.Body), line: line})
				continue
			}
			for _, t := range targets {
				out = append(out, launchTask{callee: t.name, argPos: t.drainArg, line: line})
			}
		}
		return true
	})
	return out
}

// untrackedCallee names the first executor-looking call in body, used only to make
// the "no tracked executor" failure message point at the real culprit. Returning
// "" keeps the plain meaning of the diagnostic when the closure genuinely calls
// nothing.
func untrackedCallee(body *ast.BlockStmt) string {
	name := ""
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || name != "" {
			return true
		}
		called := calleeName(call)
		if called == "" || !looksLikeExecutor(called) {
			return true
		}
		name = called
		return true
	})
	return name
}

// looksLikeExecutor applies the naming convention of the background executors so a
// renamed drain parameter is reported as such instead of as a missing delegation.
func looksLikeExecutor(name string) bool {
	return strings.HasPrefix(name, "execute") || strings.HasPrefix(name, "monitor")
}

// calleeName renders the bare name of a called function or method.
func calleeName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	default:
		return ""
	}
}

// firstDeferCall reports the identifier deferred by the function's first defer
// statement. The second result is true when that defer wraps an anonymous
// closure, which has no single identifiable release.
func firstDeferCall(body *ast.BlockStmt) (string, bool) {
	if body == nil {
		return "", false
	}
	for _, stmt := range body.List {
		d, ok := stmt.(*ast.DeferStmt)
		if !ok {
			continue
		}
		switch fun := d.Call.Fun.(type) {
		case *ast.Ident:
			return fun.Name, false
		case *ast.SelectorExpr:
			return fun.Sel.Name, false
		default:
			return "", true
		}
	}
	return "", false
}

// packageSourceFiles parses every non-test Go source file in dir, sharing one
// fset so positions taken from different files stay comparable.
func packageSourceFiles(t *testing.T, fset *token.FileSet, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []*ast.File
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		out = append(out, parsed)
	}
	if len(out) == 0 {
		t.Fatalf("no non-test sources under %s; the guard would be vacuous", dir)
	}
	return out
}

// packageFuncDecls indexes every non-test top-level declaration in dir by bare
// name. Methods are included: every executor here is a method on *Server, so
// skipping receivers would hide all of them.
func packageFuncDecls(t *testing.T, fset *token.FileSet, dir string) map[string]*ast.FuncDecl {
	t.Helper()
	out := map[string]*ast.FuncDecl{}
	for _, parsed := range packageSourceFiles(t, fset, dir) {
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
			out[fn.Name.Name] = fn
		}
	}
	return out
}

// architectureRepoRoot returns the repository root from this guard's location.
func architectureRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture guard")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func repoRelative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

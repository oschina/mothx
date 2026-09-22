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
	"strconv"
	"strings"
	"testing"
)

// TestAdapterTestsUseCanonicalRunBoundaries freezes the L5 test-hygiene debt.
//
// Production code is already barred from the legacy session run/lease APIs and
// from agent.New (see architecture_guard_test.go), but the guard skips _test.go,
// so adapter tests still set up canonical run/lease state by hand. Those
// fixtures can drift from the real Runtime lifecycle: a test can pass against a
// hand-written row while production fails. New occurrences are rejected unless
// the file is listed in legacyTestAllowlist with a reason. Migrate a file to the
// canonical test boundaries and delete its entry:
//
//	session.SaveSessionRun / CreateSessionRun / UpdateSessionRunStatus -> agentruntime.RunStore{SessionDir}.Create/Update/Finish
//	session.SaveSessionRunEvent                                        -> agentruntime.SessionRunEventSink{}.Record
//	session.GetSessionRun*                                             -> agentruntime.GetDurableRun / GetActiveDurableRun
//	session.TryLockRuntime / LockRuntime                               -> agentruntime.AcquireExecutionAdmission
//	agent.New / agent.NewWithLoopConfig                                -> SessionRuntime.BuildAgent / BuildTransientAgent / NewAgentManager
//
// Owner packages (internal/agentruntime, internal/session, internal/dao,
// internal/db, internal/agent) and the guard itself are exempt: they test the
// APIs they own.
func TestAdapterTestsUseCanonicalRunBoundaries(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture guard")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	violations, err := legacyTestBoundaryViolations(root)
	if err != nil {
		t.Fatal(err)
	}
	sorted := make([]string, 0, len(violations))
	for violation := range violations {
		sorted = append(sorted, violation)
	}
	sort.Strings(sorted)
	if len(sorted) > 0 {
		t.Fatalf("adapter tests must use the canonical runtime boundaries (migrate, or add a documented legacyTestAllowlist entry):\n- %s\n\nallowlist (%d entries):\n\t%s",
			strings.Join(sorted, "\n- "), len(legacyTestAllowlist), strings.Join(sortedAllowlist(), "\n\t"))
	}
}

// legacyTestAllowlist documents adapter test files that still construct
// canonical run/lease state through the legacy session APIs or build a
// low-level agent directly. Each entry must state why the file cannot migrate
// yet; the list may only shrink as files move to the canonical boundaries.
var legacyTestAllowlist = map[string]string{
	// Lease-holding fixtures: no drop-in agentruntime equivalent exists yet,
	// because AcquireExecutionAdmission has different ownership semantics than
	// the raw session lease these tests lean on to simulate a busy session.
	"internal/acp/acp_decision_rehydrate_test.go": "holds a runtime lease to simulate a peer-owned session",
	"internal/acp/acp_mcp_test.go":                "holds a runtime lease to simulate a peer-owned session",
	"internal/cron/cron_test.go":                  "holds a runtime lease to keep the session busy",
	"internal/serve/channel_tools_http_test.go":   "holds a runtime lease to keep the session busy",
	"internal/serve/lifecycle_http_test.go":       "holds a runtime lease to keep the session busy",
	"internal/serve/session_lifecycle_test.go":    "holds a runtime lease to keep the session busy",
	// Run-row fixtures: seed a canonical run row to exercise one specific
	// adapter recovery/admission/delivery path before RunStore covers it.
	"internal/acp/acp_admission_test.go":                    "seeds a run row to drive admission ownership resolution",
	"internal/acp/acp_process_integration_test.go":          "process-boundary fixture seeds run rows and lease state",
	"internal/acp/manage_delivery_test.go":                  "seeds a run row so a delivery operation has a run owner",
	"internal/serve/channels/dispatcher_test.go":            "seeds and inspects run rows for background reconciliation",
	"internal/serve/delivery_recovery_test.go":              "seeds a run row so delivery recovery has a canonical owner",
	"internal/serve/openaiapi/delivery_persistence_test.go": "seeds a run row for delivery persistence",
	"internal/serve/openaiapi/handler_deliveries_test.go":   "seeds a run row so delivery retry has a run owner",
	"internal/serve/openaiapi/handler_run_submit_test.go":   "legacy submit fixture seeds run rows and a session lease",
	"internal/serve/openaiapi/responses_run_api_test.go":    "abandon/reconnect recovery fixture seeds run rows and leases directly",
	"internal/serve/process_integration_test.go":            "process-boundary fixture seeds run rows",
	"internal/tui/background_test.go":                       "seeds a run row to drive background reconciliation",
	// Low-level agent fixtures: drive a focused adapter event path.
	"internal/serve/channels/watchdog_test.go":                    "holds a lease and builds a low-level agent to simulate a stuck run",
	"internal/serve/openaiapi/approval_test.go":                   "builds a low-level agent and seeds a run row for approval replay",
	"internal/serve/openaiapi/background_run_coordinator_test.go": "builds low-level agents and seeds run rows for the background coordinator",
	"internal/serve/openaiapi/server_test.go":                     "builds low-level agents to drive focused server handlers",
	"internal/tui/cache_test.go":                                  "builds a low-level agent to drive the /compact command path",
	"internal/tui/decision_resolution_test.go":                    "builds a low-level agent to drive decision resolution",
}

func sortedAllowlist() []string {
	entries := make([]string, 0, len(legacyTestAllowlist))
	for file, reason := range legacyTestAllowlist {
		entries = append(entries, fmt.Sprintf("%q: %q,", file, reason))
	}
	sort.Strings(entries)
	return entries
}

var legacyTestExemptDirs = []string{
	"internal/agentruntime/",
	"internal/session/",
	"internal/dao/",
	"internal/db/",
	"internal/agent/",
	"internal/architecture/",
}

func legacyTestBoundaryViolations(root string) (map[string]string, error) {
	violations := make(map[string]string)
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
		if filepath.Ext(path) != ".go" || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if _, ok := legacyTestAllowlist[rel]; ok {
			return nil
		}
		for _, prefix := range legacyTestExemptDirs {
			if strings.HasPrefix(rel, prefix) {
				return nil
			}
		}
		fileAST, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", rel, parseErr)
		}
		imports := make(map[string]string)
		for _, imp := range fileAST.Imports {
			pathValue, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				continue
			}
			name := filepath.Base(pathValue)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			imports[name] = pathValue
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
			if !ok {
				return true
			}
			pkgPath := imports[ident.Name]
			switch {
			case pkgPath == "github.com/oschina/mothx/internal/session" && isCanonicalRunPersistence(selector.Sel.Name):
				addLegacyTestViolation(violations, rel, fmt.Sprintf("session.%s; use agentruntime.RunStore/SessionRunEventSink", selector.Sel.Name))
			case pkgPath == "github.com/oschina/mothx/internal/session" && isCanonicalRunQuery(selector.Sel.Name):
				addLegacyTestViolation(violations, rel, fmt.Sprintf("session.%s; use agentruntime.GetDurableRun", selector.Sel.Name))
			case pkgPath == "github.com/oschina/mothx/internal/session" && isLegacyRuntimeLeaseAPI(selector.Sel.Name):
				addLegacyTestViolation(violations, rel, fmt.Sprintf("session.%s; use agentruntime.AcquireExecutionAdmission", selector.Sel.Name))
			case pkgPath == "github.com/oschina/mothx/internal/agent" && (selector.Sel.Name == "New" || selector.Sel.Name == "NewWithLoopConfig"):
				addLegacyTestViolation(violations, rel, fmt.Sprintf("agent.%s; use SessionRuntime.BuildAgent/BuildTransientAgent/NewAgentManager", selector.Sel.Name))
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return violations, nil
}

func addLegacyTestViolation(violations map[string]string, rel, detail string) {
	if _, ok := violations[rel]; ok {
		return
	}
	violations[rel] = fmt.Sprintf("%s: %s", rel, detail)
}

package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The external ACP child transport has exactly one production owner: the shared
// Runtime. An adapter that imports it would be able to launch and drive another
// product's Agent outside Runtime policy, decisions, and lifecycle.
//
// Phase 2 of docs/proposal/acp-client-external-subagent-proposal.md will move
// raw subprocess launch into a Runtime-owned process-tree launcher. Until then
// `internal/acp/client` still imports os/exec for its supplied stdio transport;
// this guard already forbids it from depending on MothX persistence, providers,
// UI adapters, or the Agent Runtime itself.
const acpClientPackage = "github.com/oschina/mothx/internal/acp/client"

func TestACPClientHasOneRuntimeOwner(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	walkProductionGoFiles(t, root, func(rel string, fileAST *ast.File) {
		for _, imp := range fileAST.Imports {
			if strings.Trim(imp.Path.Value, `"`) != acpClientPackage {
				continue
			}
			if !strings.HasPrefix(rel, "internal/agentruntime/") {
				violations = append(violations, rel+": imports internal/acp/client; only the shared Runtime may own an external ACP child")
			}
		}
	})
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("external ACP client ownership leaked out of Runtime:\n- %s", strings.Join(violations, "\n- "))
	}
}

func TestACPClientRemainsTransportOnly(t *testing.T) {
	root := repoRoot(t)
	clientDir := filepath.Join(root, "internal", "acp", "client")
	forbidden := map[string]string{
		"github.com/oschina/mothx/internal/agent":        "Agent Core owns execution, not the transport",
		"github.com/oschina/mothx/internal/agentruntime": "the Runtime wires and consumes the transport",
		"github.com/oschina/mothx/internal/dao":          "the transport performs no SQL",
		"github.com/oschina/mothx/internal/db":           "the transport performs no SQL",
		"github.com/oschina/mothx/internal/provider":     "an ACP child is an Agent, not a model provider",
		"github.com/oschina/mothx/internal/serve":        "serve is an adapter",
		"github.com/oschina/mothx/internal/session":      "the transport owns no session record",
		"github.com/oschina/mothx/internal/tui":          "the TUI is an adapter",
		"os/exec":                                        "process launch belongs to the Runtime-owned process-tree launcher",
	}
	var violations []string
	inspected := 0
	err := filepath.WalkDir(clientDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileAST, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		inspected++
		rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		for _, imp := range fileAST.Imports {
			pathValue := strings.Trim(imp.Path.Value, `"`)
			if reason, bad := forbidden[pathValue]; bad {
				violations = append(violations, rel+": imports "+pathValue+" ("+reason+")")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if inspected == 0 {
		t.Fatalf("inspected no production files under %s; the transport-only guard is not running", clientDir)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("internal/acp/client is no longer transport-only:\n- %s", strings.Join(violations, "\n- "))
	}
}

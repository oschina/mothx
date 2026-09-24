package architecture

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestKnowledgeContextMigrationBridgeGuard pins the one remaining legacy
// knowledge-context migration bridge (KnowledgeCapsule / WithKnowledgeContext /
// KnowledgeBaseReference) to a named owner and a minimal allowlist.
//
// The bridge predates the standard Knowledge MCP query path. New callers must
// use the MCP server (`mothx knowledge-mcp serve`) instead of injecting a
// Runtime capsule. The only permitted production caller today is the ACP prompt
// path, which still accepts the additive `knowledgeBaseRefs` field for hosts
// that have not migrated.
//
// Removal condition: once Desktop ships the MCP configuration path and no
// supported client sends `knowledgeBaseRefs`, delete the bridge and this guard.
func TestKnowledgeContextMigrationBridgeGuard(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate knowledge bridge guard")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	// The Runtime owner of the bridge, plus the single allowlisted adapter.
	ownerDir := filepath.Join("internal", "agentruntime")
	allowed := map[string]struct{}{
		filepath.Join("internal", "acp", "acp.go"): {},
	}
	markers := []string{"WithKnowledgeContext(", "KnowledgeBaseReference", "KnowledgeCapsule"}

	internalDir := filepath.Join(root, "internal")
	err := filepath.WalkDir(internalDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if strings.HasPrefix(rel, ownerDir+string(filepath.Separator)) {
			return nil
		}
		if _, ok := allowed[rel]; ok {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, marker := range markers {
			if strings.Contains(string(src), marker) {
				t.Fatalf("%s uses the legacy knowledge-context bridge (%q); new callers must use the Knowledge MCP query path instead", rel, marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

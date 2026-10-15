package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/sandbox"
)

func writeProjectMCP(t *testing.T, workDir, serversJSON string) {
	t.Helper()
	dir := filepath.Join(workDir, config.ProjectDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"mcpServers":` + serversJSON + `}`
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testBuildSettings(t *testing.T) *config.Settings {
	t.Helper()
	// Isolate the global config directory so the developer's own mcp.json and
	// skills cannot leak into the build under test.
	t.Setenv("MOTHX_DIR", t.TempDir())
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	return settings
}

// TestBuilderBuildDefersConfiguredMCP pins the interactive deferral: a build
// with Deferred MCP must return immediately even when a configured server would
// hang, mark the runtime as deferred, and connect (tolerantly) only when the
// entry point asks. The overall timeout bounds a hanging server instead of
// freezing first paint.
func TestBuilderBuildDefersConfiguredMCP(t *testing.T) {
	workDir := t.TempDir()
	writeProjectMCP(t, workDir, `[{"name":"hang","command":"sleep","args":["30"]}]`)
	settings := testBuildSettings(t)

	start := time.Now()
	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{
		Source: SourceTUI, WorkDir: workDir, MCP: MCPConnectPolicy{Deferred: true, Optional: true, OverallTimeout: 600 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("deferred build: %v", err)
	}
	t.Cleanup(runtime.Close)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("deferred build blocked for %v; it must not connect MCP", elapsed)
	}
	if !runtime.MCPDeferred() {
		t.Fatal("runtime must report deferred MCP")
	}
	if len(runtime.MCPClients) != 0 {
		t.Fatalf("deferred build connected %d clients, want none", len(runtime.MCPClients))
	}

	start = time.Now()
	// No explicit timeout: the deferred build's overall bound must be inherited.
	if err := runtime.ConnectDeferredMCP(context.Background(), MCPPolicy{Optional: true}); err != nil {
		t.Fatalf("tolerant deferred connect: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("deferred connect exceeded its overall bound: %v", elapsed)
	}
	if runtime.MCPDeferred() {
		t.Fatal("deferred flag must clear after connecting")
	}
	failures := runtime.MCPConnectFailures()
	if len(failures) != 1 || !strings.Contains(failures[0], "hang") {
		t.Fatalf("MCPConnectFailures = %v, want one entry for the hanging server", failures)
	}
}

// TestBuilderBuildOptionalMCPToleratesFailure pins that an optional build
// surfaces a failed configured server through MCPConnectFailures instead of
// failing the whole runtime build.
func TestBuilderBuildOptionalMCPToleratesFailure(t *testing.T) {
	workDir := t.TempDir()
	writeProjectMCP(t, workDir, `[{"name":"bad","command":"/definitely/missing/mcp-server"}]`)
	settings := testBuildSettings(t)

	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{
		Source: SourceTUI, WorkDir: workDir, MCP: MCPConnectPolicy{Optional: true},
	})
	if err != nil {
		t.Fatalf("optional build must not fail on a bad server: %v", err)
	}
	t.Cleanup(runtime.Close)
	failures := runtime.MCPConnectFailures()
	if len(failures) != 1 || !strings.Contains(failures[0], "bad") {
		t.Fatalf("MCPConnectFailures = %v, want one entry for the bad server", failures)
	}
}

// TestBuilderBuildStrictMCPFails keeps the historical strict contract for
// non-interactive callers: an unreachable configured server fails the build so
// a print/serve command reports the error instead of silently lacking tools.
func TestBuilderBuildStrictMCPFails(t *testing.T) {
	workDir := t.TempDir()
	writeProjectMCP(t, workDir, `[{"name":"bad","command":"/definitely/missing/mcp-server"}]`)
	settings := testBuildSettings(t)

	if _, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{
		Source: SourceTUI, WorkDir: workDir,
	}); err == nil {
		t.Fatal("strict build must fail when a configured server is unreachable")
	}
}

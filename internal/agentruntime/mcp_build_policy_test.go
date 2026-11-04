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

// writeMCPFixtureServer writes a stdio MCP server that answers the handshake
// and exposes one tool. It is the same minimal protocol surface the MCP
// client needs, expressed as a shell script so the test has no external
// dependency.
func writeMCPFixtureServer(t *testing.T, workDir, name string) {
	t.Helper()
	path := filepath.Join(workDir, name)
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  method=$(printf '%s\n' "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
  case "$method" in
    initialize)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-11-25"}}\n' "$id"
      ;;
    tools/list)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"ping","description":"d","inputSchema":{"type":"object"}}]}}\n' "$id"
      ;;
    resources/list|prompts/list)
      printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
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

// TestReloadConfiguredMCPReplacesToolsWithoutRestart pins the hot-reload
// contract: a configuration change replaces the MCP tool surface on the
// shared registry in place, so the entry point does not need a process
// restart. The stale tool of a removed server must disappear.
func TestReloadConfiguredMCPReplacesToolsWithoutRestart(t *testing.T) {
	workDir := t.TempDir()
	settings := testBuildSettings(t)

	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{
		Source: SourceTUI, WorkDir: workDir, MCP: MCPConnectPolicy{Optional: true},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(runtime.Close)

	// A stdio server that speaks the MCP handshake over stdin/stdout.
	writeMCPFixtureServer(t, workDir, "alpha-server")
	writeProjectMCP(t, workDir, `[{"name":"alpha","command":"`+filepath.Join(workDir, "alpha-server")+`"}]`)
	if err := runtime.ReloadConfiguredMCP(context.Background(), MCPPolicy{Optional: true}); err != nil {
		t.Fatalf("reload configured MCP: %v", err)
	}
	if len(runtime.MCPClients) != 1 {
		t.Fatalf("MCP clients = %d, want one connected server", len(runtime.MCPClients))
	}
	names := runtime.Registry.OwnedToolNames("mcp:alpha")
	if len(names) == 0 {
		t.Fatal("the reloaded server must contribute its tools to the registry")
	}

	// Removing the server from the configuration must remove its tools.
	writeProjectMCP(t, workDir, `[]`)
	if err := runtime.ReloadConfiguredMCP(context.Background(), MCPPolicy{Optional: true}); err != nil {
		t.Fatalf("reload after removal: %v", err)
	}
	if got := runtime.Registry.OwnedToolNames("mcp:alpha"); len(got) != 0 {
		t.Fatalf("removed server still owns tools %v", got)
	}
	if len(runtime.MCPClients) != 0 {
		t.Fatalf("MCP clients = %d, want none after removal", len(runtime.MCPClients))
	}
}

// TestReloadConfiguredMCPKeepsWorkingConfigOnFailure pins that a failed
// reload does not tear down the working configuration: the previous clients
// stay connected so the session keeps its tools.
func TestReloadConfiguredMCPKeepsWorkingConfigOnFailure(t *testing.T) {
	workDir := t.TempDir()
	settings := testBuildSettings(t)

	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{
		Source: SourceTUI, WorkDir: workDir, MCP: MCPConnectPolicy{Optional: true},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(runtime.Close)

	writeMCPFixtureServer(t, workDir, "alpha-server")
	writeProjectMCP(t, workDir, `[{"name":"alpha","command":"`+filepath.Join(workDir, "alpha-server")+`"}]`)
	if err := runtime.ReloadConfiguredMCP(context.Background(), MCPPolicy{Optional: true}); err != nil {
		t.Fatalf("reload configured MCP: %v", err)
	}
	clients := runtime.MCPClients
	if len(clients) != 1 {
		t.Fatalf("MCP clients = %d, want one connected server", len(clients))
	}

	// A strict reload against an unreachable server must fail without
	// releasing the previously connected clients.
	writeProjectMCP(t, workDir, `[{"name":"bad","command":"/definitely/missing/mcp-server"}]`)
	if err := runtime.ReloadConfiguredMCP(context.Background(), MCPPolicy{}); err == nil {
		t.Fatal("a strict reload against an unreachable server must fail")
	}
	if len(runtime.MCPClients) != 1 {
		t.Fatalf("a failed reload released the working clients: %d remain", len(runtime.MCPClients))
	}
	if runtime.MCPClients[0] != clients[0] {
		t.Fatal("a failed reload replaced the working client")
	}
}

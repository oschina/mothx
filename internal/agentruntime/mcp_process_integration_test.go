package agentruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/mcp"
	"github.com/oschina/mothx/internal/tools"
)

func TestSessionRuntimeShutdownTerminatesMCPProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stdio process liveness assertion is Unix-specific")
	}
	fixtureDir := t.TempDir()
	pidPath := filepath.Join(fixtureDir, "pid")
	commandPath := filepath.Join(fixtureDir, "mcp-runtime-fixture")
	fixture := `#!/bin/sh
printf '%s' "$$" > "$MCP_RUNTIME_PID_FILE"
while IFS= read -r line; do
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  method=$(printf '%s\n' "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
  case "$method" in
    initialize)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-11-25"}}\n' "$id"
      ;;
    tools/list)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[]}}\n' "$id"
      ;;
    resources/list)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"resources":[]}}\n' "$id"
      ;;
    prompts/list)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"prompts":[]}}\n' "$id"
      ;;
  esac
done
`
	if err := os.WriteFile(commandPath, []byte(fixture), 0755); err != nil {
		t.Fatal(err)
	}

	registry := tools.NewRegistry(t.TempDir(), nil)
	runtimeState := &SessionRuntime{Source: SourceTUI, WorkDir: t.TempDir(), Registry: registry}
	if err := runtimeState.ConnectMCP(context.Background(), MCPPolicy{Servers: []mcp.ServerConfig{{
		Name: "runtime-process", Type: "stdio", Command: commandPath,
		Env: []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{{Name: "MCP_RUNTIME_PID_FILE", Value: pidPath}},
	}}}); err != nil {
		t.Fatal(err)
	}
	pid := waitMCPFixturePID(t, pidPath)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("MCP process %d was not alive before shutdown: %v", pid, err)
	}

	execution := &ExecutionRuntime{}
	if _, err := execution.Begin(context.Background(), "mcp-shutdown-run"); err != nil {
		t.Fatal(err)
	}
	runtimeState.SetExecution(execution)
	if err := runtimeState.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtimeState.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
	if execution.State() != RunStateCancelled {
		t.Fatalf("execution state = %q, want cancelled", execution.State())
	}
	waitProcessExit(t, pid)
	if len(runtimeState.MCPClients) != 0 {
		t.Fatalf("MCP clients remain after shutdown: %d", len(runtimeState.MCPClients))
	}
}

func waitMCPFixturePID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("MCP fixture did not write PID to %s", path)
	return 0
}

func waitProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("MCP process %d remained alive after Runtime shutdown", pid)
}

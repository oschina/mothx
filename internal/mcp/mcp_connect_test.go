package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/tools"
)

// mcpFixtureServer writes a minimal stdio MCP server script that sleeps for
// delay before answering initialize, then advertises a single tool named
// tool-<tool>. The sleep models a slow-starting server without a real network
// or external dependency.
func mcpFixtureServer(t *testing.T, dir, name, delay, tool string) ServerConfig {
	t.Helper()
	path := filepath.Join(dir, name)
	script := fmt.Sprintf(`#!/bin/sh
sleep %s
while IFS= read -r line; do
  id=$(printf '%%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  method=$(printf '%%s\n' "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
  case "$method" in
    initialize)
      printf '{"jsonrpc":"2.0","id":%%s,"result":{"protocolVersion":"2025-11-25"}}\n' "$id"
      ;;
    tools/list)
      printf '{"jsonrpc":"2.0","id":%%s,"result":{"tools":[{"name":"tool-%s","description":"d","inputSchema":{"type":"object"}}]}}\n' "$id" "%s"
      ;;
    resources/list|prompts/list)
      printf '{"jsonrpc":"2.0","id":%%s,"result":{}}\n' "$id"
      ;;
  esac
done
`, delay, tool, tool)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return ServerConfig{Name: name, Type: "stdio", Command: path}
}

func newMCPTestRegistry(t *testing.T) *tools.Registry {
	t.Helper()
	return tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
}

func hasToolContaining(registry *tools.Registry, substring string) bool {
	for _, tool := range registry.All() {
		if strings.Contains(tool.Name(), substring) {
			return true
		}
	}
	return false
}

// TestConnectServersWithOptionsToleratesFailure pins the strict/optional split:
// a strict group aborts on an unreachable server and leaves no tools registered,
// while an optional group keeps the healthy server, registers its tools, and
// reports the failed server for a warning surface.
func TestConnectServersWithOptionsToleratesFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script MCP fixture is Unix-specific")
	}
	dir := t.TempDir()
	good := mcpFixtureServer(t, dir, "good", "0", "echo")
	bad := ServerConfig{Name: "bad", Type: "stdio", Command: filepath.Join(dir, "does-not-exist")}

	strictRegistry := newMCPTestRegistry(t)
	clients, failures, err := ConnectServersWithOptions(context.Background(), []ServerConfig{good, bad}, strictRegistry, Callbacks{}, ConnectOptions{})
	if err == nil {
		CloseClients(clients)
		t.Fatal("strict connect must fail when a server is unreachable")
	}
	if len(failures) != 0 || clients != nil {
		t.Fatalf("strict failure returned clients=%d failures=%d, want none", len(clients), len(failures))
	}
	if hasToolContaining(strictRegistry, "_echo") {
		t.Fatal("strict failure must not leave a healthy server's tools registered")
	}

	optionalRegistry := newMCPTestRegistry(t)
	clients, failures, err = ConnectServersWithOptions(context.Background(), []ServerConfig{good, bad}, optionalRegistry, Callbacks{}, ConnectOptions{Optional: true})
	if err != nil {
		t.Fatalf("optional connect: %v", err)
	}
	defer CloseClients(clients)
	if len(clients) != 1 || clients[0].name != "good" {
		t.Fatalf("optional clients = %v, want only the healthy server", clientNames(clients))
	}
	if len(failures) != 1 || failures[0].Name != "bad" {
		t.Fatalf("optional failures = %v, want one for bad", failures)
	}
	if !strings.Contains(failures[0].Error(), "bad") {
		t.Fatalf("failure description = %q, want it to name the server", failures[0].Error())
	}
	if !hasToolContaining(optionalRegistry, "_echo") {
		t.Fatal("optional connect must register the healthy server's tools")
	}
}

// TestConnectServersWithOptionsConnectsConcurrently guards the fix for the
// startup freeze: two servers that each take ~1s must finish in roughly the
// slowest one rather than their sum.
func TestConnectServersWithOptionsConnectsConcurrently(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script MCP fixture is Unix-specific")
	}
	if testing.Short() {
		t.Skip("timing assertion skipped in -short mode")
	}
	dir := t.TempDir()
	a := mcpFixtureServer(t, dir, "slow-a", "1", "a")
	b := mcpFixtureServer(t, dir, "slow-b", "1", "b")

	registry := newMCPTestRegistry(t)
	start := time.Now()
	clients, failures, err := ConnectServersWithOptions(context.Background(), []ServerConfig{a, b}, registry, Callbacks{}, ConnectOptions{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer CloseClients(clients)
	if len(clients) != 2 || len(failures) != 0 {
		t.Fatalf("clients=%v failures=%v, want two healthy clients", clientNames(clients), failures)
	}
	if elapsed >= 1900*time.Millisecond {
		t.Fatalf("two ~1s servers took %v; connection must be concurrent", elapsed)
	}
}

// TestConnectServersWithOptionsOverallTimeoutKeepsClientsLive pins the fix for a
// regression where the whole-group timeout context also parented every client's
// lifetime, so its deferred cancel (and the timeout itself) killed an already
// connected server: its tools were registered but every call failed. A
// successful connect under an overall timeout must stay usable afterwards.
func TestConnectServersWithOptionsOverallTimeoutKeepsClientsLive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script MCP fixture is Unix-specific")
	}
	dir := t.TempDir()
	good := mcpFixtureServer(t, dir, "good", "0", "echo")

	registry := newMCPTestRegistry(t)
	clients, failures, err := ConnectServersWithOptions(context.Background(), []ServerConfig{good}, registry, Callbacks{}, ConnectOptions{OverallTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer CloseClients(clients)
	if len(clients) != 1 || len(failures) != 0 {
		t.Fatalf("clients=%v failures=%v, want one healthy client", clientNames(clients), failures)
	}

	// The server must outlive ConnectServersWithOptions returning and still
	// answer a real request; the group timeout must only bound the handshake.
	tools, err := clients[0].listTools(context.Background())
	if err != nil {
		t.Fatalf("the overall timeout tore down the connected client: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "tool-echo" {
		t.Fatalf("tools = %v, want tool-echo", tools)
	}
	if !hasToolContaining(registry, "_echo") {
		t.Fatal("healthy server tools must be registered")
	}
}

func clientNames(clients []*Client) []string {
	names := make([]string, 0, len(clients))
	for _, client := range clients {
		names = append(names, client.name)
	}
	return names
}

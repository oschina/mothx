package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/tools"
)

func TestResolveMCPCommandUsesConfiguredPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script executable setup is Unix-specific")
	}
	dir := t.TempDir()
	command := filepath.Join(dir, "mcp-test-command")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveMCPCommand("mcp-test-command", []string{"PATH=" + dir})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != command {
		t.Fatalf("resolved command = %q, want %q", resolved, command)
	}
}

func TestMergeMCPEnvironmentOverridesInheritedValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("environment key case behavior differs on Windows")
	}
	t.Setenv("MCP_TEST_INHERITED", "old")
	cfg := config.MCPServer{
		Env: []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{
			{Name: "MCP_TEST_INHERITED", Value: "new"},
			{Name: "MCP_TEST_ADDED", Value: "added"},
		},
	}
	env, err := mergeMCPEnvironment(cfg.Env)
	if err != nil {
		t.Fatal(err)
	}
	if got := mcpEnvValue(env, "MCP_TEST_INHERITED"); got != "new" {
		t.Fatalf("overridden env = %q, want new", got)
	}
	if got := mcpEnvValue(env, "MCP_TEST_ADDED"); got != "added" {
		t.Fatalf("added env = %q, want added", got)
	}
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "MCP_TEST_INHERITED=") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("overridden env appears %d times, want once", count)
	}
}

func TestMCPStdioCommandFromPATHReceivesConfiguredEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script MCP fixture is Unix-specific")
	}
	commandDir := t.TempDir()
	commandPath := filepath.Join(commandDir, "mcp-stdio-fixture")
	fixture := `#!/bin/sh
while IFS= read -r line; do
	id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
	method=$(printf '%s\n' "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
  case "$method" in
    initialize)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-11-25"}}\n' "$id"
      ;;
    tools/list)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"env_echo","description":"echo configured env","inputSchema":{"type":"object"}}]}}\n' "$id"
      ;;
    tools/call)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"env:%s"}]}}\n' "$id" "$MCP_FIXTURE_VALUE"
      ;;
    resources/list|prompts/list)
      printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      ;;
  esac
done
`
	if err := os.WriteFile(commandPath, []byte(fixture), 0755); err != nil {
		t.Fatal(err)
	}

	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.RegisterDefaults()
	clients, err := ConnectServers(context.Background(), []ServerConfig{
		{
			Name:    "path-fixture",
			Type:    "stdio",
			Command: filepath.Base(commandPath),
			Env: []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}{
				{Name: "PATH", Value: commandDir + string(os.PathListSeparator) + os.Getenv("PATH")},
				{Name: "MCP_FIXTURE_VALUE", Value: "from-config"},
			},
		},
	}, registry, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer CloseClients(clients)

	var envTool tools.Tool
	for _, tool := range registry.All() {
		if strings.Contains(tool.Name(), "_env_echo") {
			envTool = tool
			break
		}
	}
	if envTool == nil {
		t.Fatal("stdio command did not register env_echo tool")
	}
	result, err := envTool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != fmt.Sprintf("env:%s", "from-config") {
		t.Fatalf("stdio tool output = %q", result.Text)
	}
}

// TestMCPStdioImageToolResultCarriesImageContent drives a real stdio MCP
// handshake against a shell fixture that returns an image content block, then
// asserts the decoded image reaches tools.ToolResult.Contents. It needs no
// external MCP server and no display, so it stays safe for CI. The payload is
// generated at test time rather than checked in as a fixture.
func TestMCPStdioImageToolResultCarriesImageContent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script MCP fixture is Unix-specific")
	}
	payload := base64.StdEncoding.EncodeToString(mcpTestPNG)
	commandDir := t.TempDir()
	commandPath := filepath.Join(commandDir, "mcp-image-fixture")
	fixture := fmt.Sprintf(`#!/bin/sh
png=%s
while IFS= read -r line; do
  id=$(printf '%%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  method=$(printf '%%s\n' "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
  case "$method" in
    initialize)
      printf '{"jsonrpc":"2.0","id":%%s,"result":{"protocolVersion":"2025-11-25"}}\n' "$id"
      ;;
    tools/list)
      printf '{"jsonrpc":"2.0","id":%%s,"result":{"tools":[{"name":"screenshot","description":"return an image","inputSchema":{"type":"object"}}]}}\n' "$id"
      ;;
    tools/call)
      printf '{"jsonrpc":"2.0","id":%%s,"result":{"content":[{"type":"text","text":"captured"},{"type":"image","mimeType":"image/png","data":"%%s"}]}}\n' "$id" "$png"
      ;;
    resources/list|prompts/list)
      printf '{"jsonrpc":"2.0","id":%%s,"result":{}}\n' "$id"
      ;;
  esac
done
`, payload)
	if err := os.WriteFile(commandPath, []byte(fixture), 0755); err != nil {
		t.Fatal(err)
	}

	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.RegisterDefaults()
	clients, err := ConnectServers(context.Background(), []ServerConfig{
		{
			Name:    "image-fixture",
			Type:    "stdio",
			Command: filepath.Base(commandPath),
			Env: []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}{
				{Name: "PATH", Value: commandDir + string(os.PathListSeparator) + os.Getenv("PATH")},
			},
		},
	}, registry, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer CloseClients(clients)

	var imageTool tools.Tool
	for _, tool := range registry.All() {
		if strings.Contains(tool.Name(), "_screenshot") {
			imageTool = tool
			break
		}
	}
	if imageTool == nil {
		t.Fatal("stdio fixture did not register its screenshot tool")
	}

	result, err := imageTool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "captured") {
		t.Fatalf("tool text lost the server payload: %q", result.Text)
	}
	if len(result.Contents) != 2 {
		t.Fatalf("contents = %d blocks, want a text summary plus the image", len(result.Contents))
	}
	image := result.Contents[1]
	if image.Type != "image" || image.Image == nil {
		t.Fatalf("second block = %#v, want an image block", image)
	}
	if image.Image.MimeType != "image/png" {
		t.Fatalf("image mime = %q, want image/png", image.Image.MimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(image.Image.Data)
	if err != nil {
		t.Fatalf("projected payload is not valid base64: %v", err)
	}
	if string(decoded) != string(mcpTestPNG) {
		t.Fatal("image bytes changed while crossing the MCP client boundary")
	}
}

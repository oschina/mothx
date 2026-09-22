package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

func TestIsTemplateServer(t *testing.T) {
	cases := []struct {
		name string
		srv  config.MCPServer
		want bool
	}{
		{
			name: "real stdio",
			srv:  config.MCPServer{Name: "local", Type: "stdio", Command: "/usr/local/bin/mcp-server"},
		},
		{
			name: "empty name",
			srv:  config.MCPServer{Type: "stdio", Command: "/usr/local/bin/mcp-server"},
			want: true,
		},
		{
			name: "placeholder command",
			srv:  config.MCPServer{Name: "example", Type: "stdio", Command: "/absolute/path/to/mcp-server"},
			want: true,
		},
		{
			name: "placeholder url",
			srv:  config.MCPServer{Name: "example", Type: "http", URL: "https://mcp.example.com"},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTemplateServer(tc.srv); got != tc.want {
				t.Fatalf("isTemplateServer() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoadConfiguredServersSkipsDisabledEntries(t *testing.T) {
	configDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	disabled := false
	enabled := true
	if err := config.SaveMCPConfig(config.GlobalMCPPath(), &config.MCPConfig{MCPServers: []config.MCPServer{
		{Name: "disabled", Type: "stdio", Command: "disabled-command", Enabled: &disabled},
		{Name: "enabled", Type: "stdio", Command: "enabled-command", Enabled: &enabled},
	}}); err != nil {
		t.Fatal(err)
	}
	projectConfigPath := filepath.Join(projectDir, config.ProjectMCPPath())
	if err := os.MkdirAll(filepath.Dir(projectConfigPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectConfigPath, []byte(`{"mcpServers":[{"name":"legacy","type":"stdio","command":"legacy-command"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadConfiguredServers(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || servers[0].Name != "enabled" || servers[1].Name != "legacy" {
		t.Fatalf("configured servers = %#v, want enabled global and legacy project entries", servers)
	}
}

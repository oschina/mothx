package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/oschina/mothx/internal/config"
)

// LoadConfiguredServers loads usable MCP servers from global and project mcp.json.
// Missing config files are ignored. Obvious template placeholders are skipped so
// creating a starter config does not break normal startup.
func LoadConfiguredServers(cwd string) ([]ServerConfig, error) {
	paths := []string{
		config.GlobalMCPPath(),
		filepath.Join(cwd, config.ProjectMCPPath()),
	}
	var servers []ServerConfig
	for _, path := range paths {
		cfg, err := config.LoadMCPConfig(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("load MCP config %s: %w", path, err)
		}
		config.NormalizeMCPConfig(cfg)
		for _, srv := range cfg.MCPServers {
			if !config.MCPServerEnabled(srv) || isTemplateServer(srv) {
				continue
			}
			servers = append(servers, srv)
		}
	}
	return servers, nil
}

func isTemplateServer(srv config.MCPServer) bool {
	if strings.TrimSpace(srv.Name) == "" {
		return true
	}
	if strings.Contains(srv.Command, "/absolute/path/to/mcp-server") {
		return true
	}
	if strings.Contains(srv.URL, "example.com") || strings.Contains(srv.MessageURL, "example.com") {
		return true
	}
	for _, header := range srv.Headers {
		if strings.TrimSpace(header.Value) == "replace-me" || strings.Contains(header.Value, "Bearer replace-me") {
			return true
		}
	}
	for _, env := range srv.Env {
		if strings.TrimSpace(env.Value) == "replace-me" {
			return true
		}
	}
	return false
}

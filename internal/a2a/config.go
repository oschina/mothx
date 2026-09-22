// Package a2a implements the A2A (Agent-to-Agent) protocol server.
// It provides a JSON-RPC 2.0 endpoint for other agents to send tasks to VibeCoding.
// Supports standalone A2A mode.
package a2a

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/oschina/mothx/internal/config"
)

// Config holds A2A server configuration.
type Config struct {
	Enabled   bool          `json:"enabled"`
	Port      int           `json:"port"`
	Host      string        `json:"host"`
	AuthToken string        `json:"auth_token,omitempty"`
	WorkDir   string        `json:"work_dir,omitempty"`
	AgentCard *AgentCardCfg `json:"agent_card,omitempty"`
}

// AgentCardCfg holds customizable Agent Card fields.
type AgentCardCfg struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
}

// DefaultConfig returns default A2A configuration.
func DefaultConfig() *Config {
	return &Config{
		Enabled: false,
		Port:    8093,
		Host:    "127.0.0.1",
	}
}

// ConfigPath returns the path to the global a2a.json.
func ConfigPath() string {
	return filepath.Join(config.ConfigDir(), "a2a.json")
}

// ProjectConfigPath returns the path to the project-level .mothx/a2a.json.
func ProjectConfigPath() string {
	return config.ProjectPath("a2a.json")
}

// GetListenAddr returns the listen address.
func (c *Config) GetListenAddr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// GetWorkDir returns the resolved working directory.
func (c *Config) GetWorkDir() string {
	if c.WorkDir != "" && c.WorkDir != "." {
		return c.WorkDir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// SaveConfig writes the config to a JSON file.
func SaveConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal a2a config: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// InitA2AConfig creates the a2a.json template at the default location.
// Returns the file path. If force is false and the file already exists, returns an error.
func InitA2AConfig(force bool) (string, error) {
	path := ConfigPath()
	if !force {
		if _, err := os.Stat(path); err == nil {
			return path, fmt.Errorf("a2a.json already exists: %s", path)
		}
	}
	cfg := DefaultConfig()
	cfg.AuthToken = "change-me-to-a-random-secret"
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/home/user"
	}
	cfg.WorkDir = filepath.Join(home, "projects")
	cfg.AgentCard = &AgentCardCfg{
		Name:        "My A2A Agent",
		Description: "An AI coding agent accessible via A2A protocol",
	}

	if err := SaveConfig(path, cfg); err != nil {
		return "", err
	}
	return path, nil
}

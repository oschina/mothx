package openaiapi

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/oschina/mothx/internal/util"
)

// Config holds the OpenAI-compatible API configuration used by serve.
type Config struct {
	Listen               string               `json:"listen,omitempty"`
	Auth                 AuthConfig           `json:"auth"`
	DefaultMode          string               `json:"defaultMode,omitempty"`
	DefaultThinkingLevel string               `json:"defaultThinkingLevel,omitempty"`
	EnableSubAgents      bool                 `json:"enableSubAgents,omitempty"`
	EnableDelegate       bool                 `json:"enableDelegate,omitempty"`
	EnableWorkflows      bool                 `json:"enableWorkflows,omitempty"`
	EnableWebSearch      bool                 `json:"enableWebSearch,omitempty"`
	EnableBrowser        bool                 `json:"enableBrowser,omitempty"`
	EnableArtifact       bool                 `json:"enableArtifact,omitempty"`
	EnableA2AMaster      bool                 `json:"enableA2AMaster,omitempty"`
	Sandbox              SandboxConfig        `json:"sandbox"`
	AllowedWorkDirs      *[]string            `json:"allowedWorkDirs,omitempty"` // nil=no check, []=deny all overrides
	Session              SessionConfig        `json:"session"`
	DefaultWorkDir       string               `json:"defaultWorkDir,omitempty"`
	WorkingDir           string               `json:"workingDir,omitempty"` // legacy alias for defaultWorkDir
	CORS                 CORSConfig           `json:"cors"`
	Provider             string               `json:"provider,omitempty"`
	Model                string               `json:"model,omitempty"`
	ToolVisibility       ToolVisibilityConfig `json:"toolVisibility"`
	SystemPromptMode     string               `json:"systemPromptMode,omitempty"` // "append" (default), "ignore"
	RequestTimeoutSecs   int                  `json:"requestTimeoutSeconds,omitempty"`
	BackgroundRunMaxSecs int                  `json:"backgroundRunMaxSeconds,omitempty"` // hard cap for durable background polling (default 57600)
	MaxConcurrentReqs    int                  `json:"maxConcurrentRequests,omitempty"`
	LogLevel             string               `json:"logLevel,omitempty"`
}

// AuthConfig controls bearer token authentication.
type AuthConfig struct {
	Enabled bool     `json:"enabled"`
	Tokens  []string `json:"tokens,omitempty"`
}

// SandboxConfig controls sandbox behavior for API requests.
type SandboxConfig struct {
	Enabled bool   `json:"enabled"`
	Level   string `json:"level,omitempty"` // "none", "standard", "strict"; empty=auto from mode
}

// SessionConfig controls session pool behavior.
type SessionConfig struct {
	IdleTimeoutSeconds int `json:"idleTimeoutSeconds,omitempty"`
	MaxSessions        int `json:"maxSessions,omitempty"`
}

// CORSConfig controls cross-origin resource sharing.
type CORSConfig struct {
	Enabled      bool     `json:"enabled"`
	AllowOrigins []string `json:"allowOrigins,omitempty"`
}

// ToolVisibilityConfig controls how tool calls are exposed to the client.
type ToolVisibilityConfig struct {
	// Mode controls the transport for tool status:
	//   "content" (default) — tool output mixed into content stream
	//   "sse_event" — tool output via separate SSE events
	//   "none" — no tool output
	Mode string `json:"mode,omitempty"`

	// Detail controls the verbosity of tool output in content mode:
	//   "collapsed" (default) — one-line summary: 🔧 `read` main.go
	//                           edit always shows path + diff
	//   "expanded" — full output with code fences (Ctrl+O style)
	Detail string `json:"detail,omitempty"`
}

// DefaultConfig returns the default OpenAI-compatible API configuration.
func DefaultConfig() *Config {
	return &Config{
		Listen:               "127.0.0.1:7872",
		Auth:                 AuthConfig{Enabled: false},
		DefaultMode:          "yolo",
		DefaultThinkingLevel: "medium",
		EnableSubAgents:      false,
		EnableDelegate:       false,
		EnableWorkflows:      false,
		Sandbox:              SandboxConfig{Enabled: false},
		Session:              SessionConfig{IdleTimeoutSeconds: 1800},
		CORS:                 CORSConfig{Enabled: false, AllowOrigins: []string{"*"}},
		ToolVisibility:       ToolVisibilityConfig{Mode: "content", Detail: "collapsed"},
		SystemPromptMode:     "append",
		RequestTimeoutSecs:   1800,
		LogLevel:             "info",
	}
}

func cloneConfig(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	clone := *cfg
	clone.Auth.Tokens = append([]string(nil), cfg.Auth.Tokens...)
	clone.CORS.AllowOrigins = append([]string(nil), cfg.CORS.AllowOrigins...)
	if cfg.AllowedWorkDirs != nil {
		allowed := append([]string(nil), (*cfg.AllowedWorkDirs)...)
		clone.AllowedWorkDirs = &allowed
	}
	return &clone
}

// normalizeConfig fills in defaults for empty fields.
func normalizeConfig(cfg *Config) {
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:7872"
	}
	if cfg.DefaultMode == "" {
		cfg.DefaultMode = "yolo"
	}
	if cfg.ToolVisibility.Mode == "" {
		cfg.ToolVisibility.Mode = "content"
	}
	if cfg.ToolVisibility.Detail == "" {
		cfg.ToolVisibility.Detail = "collapsed"
	}
	if cfg.SystemPromptMode == "" {
		cfg.SystemPromptMode = "append"
	}
	if cfg.RequestTimeoutSecs <= 0 {
		cfg.RequestTimeoutSecs = 1800
	}
}

func validateListenSecurity(cfg *Config, unsafe bool) error {
	if cfg == nil || unsafe || isLoopbackListen(cfg.GetListenAddr()) {
		return nil
	}
	if !cfg.Auth.Enabled || len(cfg.Auth.Tokens) == 0 {
		return fmt.Errorf("public listen address %q requires at least one configured API token; use --unsafe to override", cfg.GetListenAddr())
	}
	return nil
}

func isLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// GetListenAddr returns the effective listen address.
func (c *Config) GetListenAddr() string {
	if c.Listen != "" {
		return c.Listen
	}
	return "127.0.0.1:7872"
}

// GetWorkDir returns the effective working directory.
func (c *Config) GetWorkDir() string {
	workDir := c.DefaultWorkDir
	if workDir == "" {
		workDir = c.WorkingDir
	}
	if workDir != "" {
		if strings.HasPrefix(workDir, "~") {
			home, _ := os.UserHomeDir()
			if home != "" {
				return filepath.Join(home, workDir[1:])
			}
		}
		return workDir
	}
	cwd, _ := os.Getwd()
	return cwd
}

// GetToolDetail returns the effective tool detail level.
func (c *Config) GetToolDetail() string {
	if c.ToolVisibility.Detail != "" {
		return c.ToolVisibility.Detail
	}
	return "collapsed"
}

// ApplyUnsafeAccess disables API auth and exposes loopback/default listens on all interfaces.
func (c *Config) ApplyUnsafeAccess() {
	if c == nil {
		return
	}
	c.Auth.Enabled = false
	c.Auth.Tokens = nil
	c.Listen = unsafeListenAddr(c.GetListenAddr())
}

func unsafeListenAddr(listen string) string {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		listen = "127.0.0.1:7872"
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		if strings.HasPrefix(listen, ":") {
			return "0.0.0.0" + listen
		}
		if !strings.Contains(listen, ":") {
			return net.JoinHostPort("0.0.0.0", listen)
		}
		return listen
	}
	if shouldUnsafeBindAll(host) {
		return net.JoinHostPort("0.0.0.0", port)
	}
	return listen
}

func shouldUnsafeBindAll(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateWorkDir checks if the given directory is allowed by the allowedWorkDirs whitelist.
// Returns nil if allowed, an error describing the violation otherwise.
func (c *Config) ValidateWorkDir(dir string) error {
	// nil AllowedWorkDirs = no restriction
	if c.AllowedWorkDirs == nil {
		return nil
	}
	allowed := *c.AllowedWorkDirs
	// empty list = deny all overrides
	if len(allowed) == 0 {
		return fmt.Errorf("x_working_dir overrides are disabled")
	}

	resolvedDir, err := util.ResolvePathWithExistingSymlinks(dir)
	if err != nil {
		return fmt.Errorf("resolve directory %q: %w", dir, err)
	}
	for _, a := range allowed {
		resolvedAllowed, err := util.ResolvePathWithExistingSymlinks(a)
		if err != nil {
			return fmt.Errorf("resolve allowedWorkDirs entry %q: %w", a, err)
		}
		if resolvedDir == resolvedAllowed {
			return nil
		}
		rel, err := filepath.Rel(resolvedAllowed, resolvedDir)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("directory %q is not in allowedWorkDirs", dir)
}

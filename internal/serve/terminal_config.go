package serve

import (
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/oschina/mothx/internal/terminal"
)

// TerminalConfig controls the serve-only interactive terminal backend.
type TerminalConfig struct {
	// nil = automatic: loopback is enabled; a non-loopback listener is enabled
	// when auth is configured with at least one token. false disables it.
	Enabled *bool `json:"enabled,omitempty"`

	Shell                string   `json:"shell,omitempty"`
	MaxSessions          int      `json:"maxSessions,omitempty"`
	MaxPerSession        int      `json:"maxPerSession,omitempty"`
	ScrollbackBytes      int      `json:"scrollbackBytes,omitempty"`
	MaxOutputBytesPerSec int      `json:"maxOutputBytesPerSec,omitempty"`
	AllowedOrigins       []string `json:"allowedOrigins,omitempty"`
}

func defaultTerminalConfig() TerminalConfig {
	return TerminalConfig{
		MaxSessions:          8,
		MaxPerSession:        2,
		ScrollbackBytes:      1 << 20,
		MaxOutputBytesPerSec: 1 << 20,
	}
}

func normalizeTerminalConfig(c *TerminalConfig) {
	if c == nil {
		return
	}
	if c.MaxSessions <= 0 {
		c.MaxSessions = 8
	}
	if c.MaxSessions > 32 {
		c.MaxSessions = 32
	}
	if c.MaxPerSession <= 0 {
		c.MaxPerSession = 2
	}
	if c.MaxPerSession > 4 {
		c.MaxPerSession = 4
	}
	if c.ScrollbackBytes <= 0 {
		c.ScrollbackBytes = 1 << 20
	}
	if c.ScrollbackBytes < 64<<10 {
		c.ScrollbackBytes = 64 << 10
	}
	if c.ScrollbackBytes > 8<<20 {
		c.ScrollbackBytes = 8 << 20
	}
	if c.MaxOutputBytesPerSec <= 0 {
		c.MaxOutputBytesPerSec = 1 << 20
	}
	if c.MaxOutputBytesPerSec < 64<<10 {
		c.MaxOutputBytesPerSec = 64 << 10
	}
	if c.MaxOutputBytesPerSec > 16<<20 {
		c.MaxOutputBytesPerSec = 16 << 20
	}
	origins := c.AllowedOrigins[:0]
	for _, origin := range c.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "" || !validOrigin(origin) {
			continue
		}
		origins = append(origins, origin)
	}
	c.AllowedOrigins = origins
}

func validOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme != "" && u.Host != ""
}

// effectiveTerminalShell applies the documented shell precedence: an explicit
// serve.json shell when it is an absolute executable or a resolvable basename,
// otherwise the shell from settings.json, and finally the platform default
// chosen by terminal.Config.withDefaults.
func effectiveTerminalShell(shell, fallback string) string {
	shell = strings.TrimSpace(shell)
	fallback = strings.TrimSpace(fallback)
	if shell == "" {
		return fallback
	}
	if filepath.IsAbs(shell) {
		if info, err := os.Stat(shell); err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0o111 != 0) {
			return shell
		}
		return fallback
	}
	if strings.ContainsAny(shell, `/\\`) {
		return fallback
	}
	if resolved, err := exec.LookPath(shell); err == nil {
		return resolved
	}
	return fallback
}

func (c TerminalConfig) managerConfig() terminal.Config {
	return terminal.Config{
		Shell:                c.Shell,
		MaxSessions:          c.MaxSessions,
		MaxPerSession:        c.MaxPerSession,
		ScrollbackBytes:      c.ScrollbackBytes,
		MaxOutputBytesPerSec: c.MaxOutputBytesPerSec,
	}
}

// managerConfigForConfig resolves the effective shell against settings.json
// without mutating the persisted serve config.
func managerConfigForConfig(cfg *Config, fallbackShell string) terminal.Config {
	if cfg == nil {
		return terminal.Config{Shell: effectiveTerminalShell("", fallbackShell)}
	}
	c := cfg.Terminal.managerConfig()
	c.Shell = effectiveTerminalShell(c.Shell, fallbackShell)
	return c
}

// EffectiveTerminalEnabled is intentionally evaluated per request (and for
// /api/status) instead of being frozen into Config at startup. Serve auth can
// be updated at runtime; the terminal gate must follow it immediately.
func EffectiveTerminalEnabled(cfg *Config) (bool, string) {
	if cfg == nil || !terminal.Supported() {
		return false, "unsupported"
	}
	if cfg.Terminal.Enabled != nil && !*cfg.Terminal.Enabled {
		return false, "disabled"
	}
	if isLoopbackListen(cfg.API.Listen) {
		return true, ""
	}
	if cfg.API.Auth.Enabled && len(cfg.API.Auth.Tokens) > 0 {
		if UsesPlaceholderAuthToken(cfg) {
			return true, "placeholder_token"
		}
		return true, ""
	}
	return false, "unsafe"
}

func isLoopbackListen(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

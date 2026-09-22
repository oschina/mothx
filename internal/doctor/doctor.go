// Package doctor owns the machine-readable installation and runtime checks.
// CLI and ACP deliberately render this same result instead of maintaining
// separate diagnostic implementations.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/mcp"
	"github.com/oschina/mothx/internal/platform"
	providerfactory "github.com/oschina/mothx/internal/provider/factory"
	"github.com/oschina/mothx/internal/serve"
	"github.com/oschina/mothx/internal/skills"
	appversion "github.com/oschina/mothx/internal/version"
)

const (
	StatusOK    = "ok"
	StatusWarn  = "warn"
	StatusError = "error"
	StatusSkip  = "skip"
)

type Check struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

type Response struct {
	OK      bool    `json:"ok"`
	Version string  `json:"version"`
	Summary string  `json:"summary"`
	Checks  []Check `json:"checks"`
}

// Run performs all doctor checks for cwd. An empty cwd means the process cwd.
// It does not print or mutate configuration, making it safe for ACP requests.
func Run(cwd, version string) Response {
	if strings.TrimSpace(version) == "" {
		version = appversion.Current()
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}

	checks := make([]Check, 0, 40)
	checks = append(checks, Check{ID: "cli", Status: StatusOK, Title: "mothx CLI", Detail: version})
	checks = append(checks, checkEnvironment(cwd)...)
	settings, settingsErr := config.LoadSettingsFor(cwd)
	checks = append(checks, checkSettingsFiles(cwd, settingsErr))
	checks = append(checks, checkConfigFiles(cwd, settingsErr)...)
	if settingsErr != nil {
		checks = append(checks, Check{ID: "provider.default", Status: StatusError, Title: "Default provider", Detail: "settings unavailable", Fix: "Fix settings.json syntax"})
	} else {
		checks = append(checks, checkProvider(settings)...)
		checks = append(checks, checkConfiguredProviders(settings)...)
		checks = append(checks, checkEnvironmentOverrides()...)
	}
	checks = append(checks, checkSandbox(settings)...)
	checks = append(checks, checkMCP(cwd)...)
	checks = append(checks, checkSessions(settings))
	checks = append(checks, checkSkills(cwd, settings)...)
	checks = append(checks, checkContext(cwd, settings)...)

	result := Response{OK: true, Version: version, Checks: checks}
	for _, check := range checks {
		if check.Status == StatusError {
			result.OK = false
			if result.Summary == "" {
				result.Summary = summaryFor(check)
			}
		}
	}
	if result.Summary == "" {
		for _, check := range checks {
			if check.Status == StatusWarn {
				result.Summary = summaryFor(check)
				break
			}
		}
	}
	if result.Summary == "" {
		result.Summary = "All checks passed"
	}
	return result
}

func checkEnvironment(cwd string) []Check {
	checks := []Check{
		{ID: "environment.os", Status: StatusOK, Title: "OS / Arch", Detail: runtime.GOOS + "/" + runtime.GOARCH},
		{ID: "environment.go", Status: StatusOK, Title: "Go version", Detail: runtime.Version()},
	}
	shell := platform.DefaultShell()
	if _, err := os.Stat(shell); err != nil {
		checks = append(checks, Check{ID: "environment.shell", Status: StatusWarn, Title: "Shell", Detail: shell + " (not found)"})
	} else {
		checks = append(checks, Check{ID: "environment.shell", Status: StatusOK, Title: "Shell", Detail: shell})
	}
	home := platform.HomeDir()
	if _, err := os.Stat(home); err != nil {
		checks = append(checks, Check{ID: "environment.home", Status: StatusError, Title: "Home directory", Detail: home + " (not accessible)"})
	} else {
		checks = append(checks, Check{ID: "environment.home", Status: StatusOK, Title: "Home directory", Detail: home})
	}
	return append(checks, checkCWD(cwd))
}

func checkCWD(cwd string) Check {
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		detail := "working directory is unavailable"
		if err != nil {
			detail = err.Error()
		}
		return Check{ID: "cwd", Status: StatusError, Title: "Working directory", Detail: detail, Fix: "Start mothx from an existing directory"}
	}
	return Check{ID: "cwd", Status: StatusOK, Title: "Working directory", Detail: cwd}
}

func checkSettingsFiles(cwd string, settingsErr error) Check {
	path := config.GlobalSettingsPath()
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return Check{ID: "config", Status: StatusError, Title: "settings", Detail: path + " is a directory", Fix: "Replace settings.json with a file"}
		}
		if settingsErr != nil {
			return Check{ID: "config", Status: StatusError, Title: "settings", Detail: path + ": " + settingsErr.Error(), Fix: "Fix settings.json syntax"}
		}
		return Check{ID: "config", Status: StatusOK, Title: "settings", Detail: path}
	} else if !os.IsNotExist(err) {
		return Check{ID: "config", Status: StatusError, Title: "settings", Detail: err.Error()}
	}
	projectPath := config.ProjectPathFor(cwd, "settings.json")
	if _, err := os.Stat(projectPath); err == nil && settingsErr == nil {
		return Check{ID: "config", Status: StatusOK, Title: "settings", Detail: projectPath}
	}
	if settingsErr != nil {
		return Check{ID: "config", Status: StatusError, Title: "settings", Detail: settingsErr.Error(), Fix: "Create or fix settings.json"}
	}
	return Check{ID: "config", Status: StatusSkip, Title: "settings", Detail: path + " (not found; defaults in use)"}
}

func checkConfigFiles(cwd string, settingsErr error) []Check {
	type configFile struct {
		id    string
		title string
		path  string
	}
	files := []configFile{
		{"config.project", "Project settings", config.ProjectPathFor(cwd, "settings.json")},
		{"serve.global", "Serve config (global)", serve.ConfigPath()},
		{"serve.project", "Serve config (project)", config.ProjectPathFor(cwd, "serve.json")},
		{"mcp.global", "MCP config (global)", config.GlobalMCPPath()},
		{"mcp.project", "MCP config (project)", config.ProjectPathFor(cwd, "mcp.json")},
	}
	checks := make([]Check, 0, len(files)+1)
	for _, file := range files {
		info, err := os.Stat(file.path)
		if os.IsNotExist(err) {
			checks = append(checks, Check{ID: file.id, Status: StatusSkip, Title: file.title, Detail: file.path + " (not found)"})
			continue
		}
		if err != nil {
			checks = append(checks, Check{ID: file.id, Status: StatusError, Title: file.title, Detail: err.Error()})
			continue
		}
		if info.IsDir() {
			checks = append(checks, Check{ID: file.id, Status: StatusError, Title: file.title, Detail: file.path + " is a directory"})
			continue
		}
		checks = append(checks, Check{ID: file.id, Status: StatusOK, Title: file.title, Detail: file.path})
	}
	status := StatusOK
	detail := "loaded successfully"
	if settingsErr != nil {
		status = StatusError
		detail = "failed to parse settings"
	}
	checks = append(checks, Check{ID: "config.parse", Status: status, Title: "Settings parse", Detail: detail})
	return checks
}

func checkProvider(settings *config.Settings) []Check {
	return ValidateProvider(settings, settings.DefaultProvider, settings.DefaultModel)
}

// ValidateProvider applies the same provider/model checks to an explicit ACP
// selection as the default-provider doctor check.
func ValidateProvider(settings *config.Settings, providerName, modelID string) []Check {
	if settings == nil {
		return []Check{{ID: "provider.default", Status: StatusError, Title: "Default provider", Detail: "settings unavailable", Fix: "Fix settings.json syntax"}}
	}
	name := strings.TrimSpace(providerName)
	model := strings.TrimSpace(modelID)
	if name == "" {
		return []Check{{ID: "provider.default", Status: StatusError, Title: "Default provider", Detail: "no default provider configured", Fix: "Set defaultProvider in settings.json"}}
	}
	if settings.GetProviderConfig(name) == nil && config.DefaultProviderConfig(name) == nil {
		return []Check{{ID: "provider.default", Status: StatusError, Title: "Default provider", Detail: name + ": unknown provider", Fix: "Add the provider to settings.json"}}
	}
	pc := config.ResolveProviderConfig(name, settings)
	if pc == nil || strings.TrimSpace(pc.BaseURL) == "" {
		return []Check{{ID: "provider.default", Status: StatusError, Title: "Default provider", Detail: name + ": missing base URL", Fix: "Set " + name + ".baseUrl"}}
	}
	apiKey := strings.TrimSpace(settings.ResolveKey(name))
	if apiKey == "" || strings.HasPrefix(apiKey, "${") || strings.HasPrefix(apiKey, "!") {
		return []Check{{ID: "provider.default", Status: StatusError, Title: "Default provider", Detail: name + ": missing API key", Fix: "Set " + name + ".apiKey or " + apiKeyEnv(name, pc)}}
	}

	_, _, err := providerfactory.CreateWithOptions(settings, name, model, providerfactory.Options{RequireModel: true})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "model") && strings.Contains(strings.ToLower(err.Error()), "available") {
			detail := name + ": no usable model"
			if model != "" {
				detail = name + "/" + model + ": model is not available"
			}
			return []Check{
				{ID: "provider.default", Status: StatusOK, Title: "Default provider", Detail: name},
				{ID: "model.default", Status: StatusError, Title: "Default model", Detail: detail, Fix: "Choose a model listed for this provider"},
			}
		}
		return []Check{{ID: "provider.default", Status: StatusError, Title: "Default provider", Detail: name + ": configuration is unusable", Fix: "Check the provider base URL and configuration"}}
	}

	checks := []Check{{ID: "provider.default", Status: StatusOK, Title: "Default provider", Detail: name}}
	if model != "" {
		checks = append(checks, Check{ID: "model.default", Status: StatusOK, Title: "Default model", Detail: model})
	}
	return checks
}

func checkConfiguredProviders(settings *config.Settings) []Check {
	if settings == nil {
		return nil
	}
	names := make([]string, 0, len(settings.Providers))
	for name := range settings.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	checks := make([]Check, 0, len(names)+1)
	configured := 0
	for _, name := range names {
		if strings.EqualFold(name, settings.DefaultProvider) {
			continue
		}
		pc := settings.Providers[name]
		if pc == nil {
			continue
		}
		key := strings.TrimSpace(settings.ResolveKey(name))
		if key == "" || strings.HasPrefix(key, "${") || strings.HasPrefix(key, "!") {
			continue
		}
		configured++
		checks = append(checks, Check{ID: "provider." + stableID(name), Status: StatusOK, Title: "Provider", Detail: name})
	}
	if configured == 0 && len(names) == 0 {
		checks = append(checks, Check{ID: "providers", Status: StatusWarn, Title: "Providers", Detail: "no providers configured"})
	}
	return checks
}

func checkEnvironmentOverrides() []Check {
	overrides := []struct {
		env  string
		name string
	}{
		{"VIBECODING_PROVIDER", "defaultProvider"},
		{"VIBECODING_MODEL", "defaultModel"},
		{"VIBECODING_MODE", "defaultMode"},
		{"VIBECODING_THINKING", "defaultThinkingLevel"},
	}
	checks := make([]Check, 0, len(overrides))
	for _, override := range overrides {
		if value := os.Getenv(override.env); value != "" {
			checks = append(checks, Check{ID: "environment." + stableID(override.env), Status: StatusWarn, Title: "Environment override", Detail: override.env + " overrides " + override.name})
		}
	}
	return checks
}

func checkSandbox(settings *config.Settings) []Check {
	if p, err := exec.LookPath("bwrap"); err == nil {
		checks := []Check{{ID: "sandbox", Status: StatusOK, Title: "Sandbox", Detail: p}}
		return appendSandboxConfig(checks, settings)
	}
	return appendSandboxConfig([]Check{{ID: "sandbox", Status: StatusWarn, Title: "Sandbox", Detail: "bwrap not found"}}, settings)
}

func appendSandboxConfig(checks []Check, settings *config.Settings) []Check {
	if settings == nil {
		return checks
	}
	return append(checks, Check{ID: "sandbox.config", Status: StatusOK, Title: "Sandbox config", Detail: fmt.Sprintf("enabled=%v, level=%s", settings.Sandbox.Enabled, valueOr(settings.Sandbox.Level, "none"))})
}

func checkMCP(cwd string) []Check {
	servers, err := mcp.LoadConfiguredServers(cwd)
	if err != nil {
		return []Check{{ID: "mcp", Status: StatusError, Title: "MCP", Detail: "MCP configuration could not be loaded", Fix: "Fix mcp.json syntax"}}
	}
	if len(servers) == 0 {
		return []Check{{ID: "mcp", Status: StatusSkip, Title: "MCP", Detail: "none configured"}}
	}
	checks := make([]Check, 0, len(servers))
	for _, server := range servers {
		checks = append(checks, Check{ID: "mcp." + stableID(server.Name), Status: StatusOK, Title: "MCP server", Detail: server.Name})
	}
	return checks
}

func checkSessions(settings *config.Settings) Check {
	if settings == nil {
		return Check{ID: "sessions", Status: StatusSkip, Title: "Sessions", Detail: "settings unavailable"}
	}
	path := settings.GetSessionDir()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return Check{ID: "sessions", Status: StatusSkip, Title: "Sessions", Detail: path + " (not created yet)"}
	}
	if err != nil {
		return Check{ID: "sessions", Status: StatusError, Title: "Sessions", Detail: err.Error()}
	}
	if !info.IsDir() {
		return Check{ID: "sessions", Status: StatusError, Title: "Sessions", Detail: path + " is not a directory"}
	}
	return Check{ID: "sessions", Status: StatusOK, Title: "Sessions", Detail: path}
}

func checkSkills(cwd string, settings *config.Settings) []Check {
	if settings == nil {
		return []Check{{ID: "skills", Status: StatusSkip, Title: "Skills", Detail: "settings unavailable"}}
	}
	path := settings.GetGlobalSkillsDir()
	if _, err := os.Stat(path); err == nil {
		return []Check{{ID: "skills", Status: StatusOK, Title: "Skills", Detail: path}}
	} else if !os.IsNotExist(err) {
		return []Check{{ID: "skills", Status: StatusError, Title: "Skills", Detail: err.Error()}}
	}
	for _, projectPath := range skills.ProjectSkillDirs(cwd) {
		if _, err := os.Stat(projectPath); err == nil {
			return []Check{{ID: "skills", Status: StatusOK, Title: "Skills", Detail: projectPath}}
		}
	}
	return []Check{{ID: "skills", Status: StatusSkip, Title: "Skills", Detail: path + " (not created)"}}
}

func checkContext(cwd string, settings *config.Settings) []Check {
	checks := make([]Check, 0, 2)
	if settings != nil {
		status := StatusSkip
		if settings.ContextFiles.Enabled {
			status = StatusOK
		}
		checks = append(checks, Check{ID: "context.files", Status: status, Title: "Context files", Detail: fmt.Sprintf("enabled=%v", settings.ContextFiles.Enabled)})
	}
	known := []string{"AGENTS.md", "CLAUDE.md", "CURSOR.md", ".cursorrules", "CONVENTIONS.md"}
	for _, name := range known {
		path := filepath.Join(cwd, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			checks = append(checks, Check{ID: "context.project", Status: StatusOK, Title: "Project context", Detail: name})
			break
		}
	}
	if len(checks) == 0 || checks[len(checks)-1].ID != "context.project" {
		checks = append(checks, Check{ID: "context.project", Status: StatusSkip, Title: "Project context", Detail: "none found"})
	}
	globalFiles := []string{"AGENTS.md", "CLAUDE.md"}
	for _, name := range globalFiles {
		if _, err := os.Stat(filepath.Join(config.ConfigDir(), name)); err == nil {
			checks = append(checks, Check{ID: "context.global", Status: StatusOK, Title: "Global context", Detail: name})
			return checks
		}
	}
	checks = append(checks, Check{ID: "context.global", Status: StatusSkip, Title: "Global context", Detail: "none found"})
	return checks
}

func apiKeyEnv(name string, pc *config.ProviderConfig) string {
	if pc != nil {
		if match := regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`).FindStringSubmatch(strings.TrimSpace(pc.APIKey)); len(match) == 2 {
			return match[1]
		}
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_") + "_API_KEY"
}

func summaryFor(check Check) string {
	if check.ID == "provider.default" && strings.Contains(check.Detail, "missing API key") {
		return "Default provider " + strings.TrimSuffix(strings.TrimSpace(strings.SplitN(check.Detail, ":", 2)[0]), ":") + " has no API key"
	}
	if check.Detail != "" {
		return check.Title + ": " + check.Detail
	}
	return check.Title + " check failed"
}

func stableID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

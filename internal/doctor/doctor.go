// Package doctor owns the machine-readable installation and runtime checks.
// CLI and ACP deliberately render this same result instead of maintaining
// separate diagnostic implementations.
package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
	"github.com/oschina/mothx/internal/config"
	database "github.com/oschina/mothx/internal/db"
	"github.com/oschina/mothx/internal/mcp"
	"github.com/oschina/mothx/internal/platform"
	providerfactory "github.com/oschina/mothx/internal/provider/factory"
	"github.com/oschina/mothx/internal/serve"
	"github.com/oschina/mothx/internal/session"
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
	// Repairs is present only when the caller asked for a session-content repair.
	// It records what each attempt changed (or why it deferred/failed) so the
	// machine-readable output stays a faithful account of the side effects.
	Repairs []SessionContentRepair `json:"repairs,omitempty"`
}

// Options configures one diagnosis run.
type Options struct {
	// CWD scopes project-level checks. An empty value means the process cwd.
	CWD string
	// Version overrides the reported CLI version (tests and ACP pass it in).
	Version string
	// RepairSessionContent adopts split session content before the checks run.
	// "all" repairs every split session; any other value is treated as a session
	// ID. An empty value performs no writes.
	RepairSessionContent string
}

// Run performs all doctor checks for cwd. An empty cwd means the process cwd.
// It does not print or mutate configuration, making it safe for ACP requests.
func Run(cwd, version string) Response {
	return RunWithOptions(Options{CWD: cwd, Version: version})
}

// RunWithOptions performs the checks and, when requested, repairs split session
// content first so the reported checks describe the post-repair state.
func RunWithOptions(opts Options) Response {
	cwd, version := opts.CWD, opts.Version
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
	var repairs []SessionContentRepair
	if settingsErr != nil {
		checks = append(checks, Check{ID: "provider.default", Status: StatusError, Title: "Default provider", Detail: "settings unavailable", Fix: "Fix settings.json syntax"})
	} else {
		checks = append(checks, checkProvider(settings)...)
		checks = append(checks, checkConfiguredProviders(settings)...)
		checks = append(checks, checkEnvironmentOverrides()...)
		checks = append(checks, checkACPAgents(settings)...)
		checks = append(checks, checkWorkerBridge(settings)...)
		if strings.TrimSpace(opts.RepairSessionContent) != "" {
			repairs = RepairSessionContent(context.Background(), settings, opts.RepairSessionContent)
		}
	}
	checks = append(checks, checkSandbox(settings)...)
	checks = append(checks, checkMCP(cwd)...)
	checks = append(checks, checkSessions(settings))
	checks = append(checks, checkSessionIntegrity(settings))
	checks = append(checks, checkSessionContentStore(settings))
	checks = append(checks, checkSessionTurnBoundaries(settings))
	checks = append(checks, checkSkills(cwd, settings)...)
	checks = append(checks, checkContext(cwd, settings)...)

	result := Response{OK: true, Version: version, Checks: checks, Repairs: repairs}
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
		{"MOTHX_SQLITE_QUICK_CHECK", "startup integrity checks"},
	}
	checks := make([]Check, 0, len(overrides))
	for _, override := range overrides {
		if value := os.Getenv(override.env); value != "" {
			checks = append(checks, Check{ID: "environment." + stableID(override.env), Status: StatusWarn, Title: "Environment override", Detail: override.env + " overrides " + override.name})
		}
	}
	return checks
}

// checkACPAgents validates configured external ACP worker profiles without
// launching them. It reports configuration shape and executable availability;
// it never starts a process, sends a prompt, or prints environment values.
func checkACPAgents(settings *config.Settings) []Check {
	check := Check{ID: "acp.profiles", Status: StatusSkip, Title: "ACP worker profiles", Detail: "no profiles configured"}
	if settings == nil || len(settings.ACPAgents) == 0 {
		return []Check{check}
	}
	if err := settings.ValidateACPAgentProfiles(); err != nil {
		return []Check{{ID: "acp.profiles", Status: StatusError, Title: "ACP worker profiles", Detail: err.Error(), Fix: "Fix acpAgents in settings.json (valid command/transport, environment names, concurrency, and permission policy)"}}
	}
	enabled := 0
	for _, profile := range settings.ACPAgents {
		if !profile.Enabled {
			continue
		}
		enabled++
		if profile.IsRemote() {
			// Validate already checked the URL shape and rejected embedded
			// credentials. Doctor never connects to or launches a remote worker.
			continue
		}
		// Resolve the way a real launch would: a bare command through PATH, a path
		// as given. exec.LookPath reports "not found" for a missing bare command,
		// so this is the same answer the launcher would get.
		resolved, err := exec.LookPath(profile.Command[0])
		if err != nil {
			return []Check{{ID: "acp.profiles", Status: StatusError, Title: "ACP worker profiles", Detail: fmt.Sprintf("profile %q command %q was not found", profile.ID, profile.Command[0]), Fix: "Install the worker, add it to PATH, or use an absolute path in acpAgents"}}
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return []Check{{ID: "acp.profiles", Status: StatusError, Title: "ACP worker profiles", Detail: fmt.Sprintf("profile %q executable %q is unavailable", profile.ID, resolved), Fix: "Update acpAgents command or install the worker"}}
		}
		if info.IsDir() {
			return []Check{{ID: "acp.profiles", Status: StatusError, Title: "ACP worker profiles", Detail: fmt.Sprintf("profile %q command is a directory", profile.ID), Fix: "Point acpAgents command at the worker executable"}}
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			return []Check{{ID: "acp.profiles", Status: StatusError, Title: "ACP worker profiles", Detail: fmt.Sprintf("profile %q executable is not executable", profile.ID), Fix: "chmod +x the configured worker"}}
		}
	}
	if enabled == 0 {
		check.Detail = "all configured profiles are disabled"
		return []Check{check}
	}
	// Report the parallel base-workspace opt-in explicitly. MothX cannot
	// confine an external Agent's own tools to the declared write scope, so an
	// operator who enables this accepts a real risk that must stay visible.
	for _, profile := range settings.ACPAgents {
		if !profile.Enabled || !profile.EffectiveAllowParallelBaseWorkspaceWrites() {
			continue
		}
		return []Check{
			{ID: "acp.profiles", Status: StatusOK, Title: "ACP worker profiles", Detail: fmt.Sprintf("%d enabled profile(s) validated", enabled)},
			{ID: "acp.profiles.parallel_base_writes", Status: StatusWarn, Title: "Parallel base-workspace writes enabled",
				Detail: fmt.Sprintf("profile %q allows write-role workers to modify the session workspace concurrently", profile.ID),
				Fix:    "Ensure the tasks partition the workspace by module, or remove allowParallelBaseWorkspaceWrites to serialize base-workspace writers"},
		}
	}
	return []Check{{ID: "acp.profiles", Status: StatusOK, Title: "ACP worker profiles", Detail: fmt.Sprintf("%d enabled profile(s) validated", enabled)}}
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

// checkSessionIntegrity performs the on-demand counterpart to the startup
// quick_check. It is read-only, so doctor remains safe to run while another
// MothX process is serving the same database.
func checkSessionIntegrity(settings *config.Settings) Check {
	if settings == nil {
		return Check{ID: "sessions.integrity", Status: StatusSkip, Title: "Session database integrity", Detail: "settings unavailable"}
	}
	path := session.RootDatabasePath(settings.GetSessionDir())
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Check{ID: "sessions.integrity", Status: StatusSkip, Title: "Session database integrity", Detail: path + " (not created yet)"}
	} else if err != nil {
		return Check{ID: "sessions.integrity", Status: StatusError, Title: "Session database integrity", Detail: err.Error()}
	}
	if err := database.VerifyIntegrity(path); err != nil {
		return Check{
			ID: "sessions.integrity", Status: StatusError, Title: "Session database integrity",
			Detail: err.Error(),
			Fix:    sessionIntegrityFix(err),
		}
	}
	return Check{ID: "sessions.integrity", Status: StatusOK, Title: "Session database integrity", Detail: path}
}

// sessionIntegrityFix describes only actions that can actually help. Startup can
// repair stale secondary indexes, but not arbitrary table damage; if startup
// checks are disabled, restarting alone will not repair anything.
func sessionIntegrityFix(err error) string {
	if !database.IsIndexOnlyIntegrityError(err) {
		return "Back up sessions.db and restore a known-good backup; do not delete the database"
	}
	if database.StartupQuickCheckEnabled() {
		return "Back up sessions.db, then restart MothX so startup can repair stale indexes; do not delete the database"
	}
	return "Back up sessions.db, unset MOTHX_SQLITE_QUICK_CHECK, then restart MothX so startup can repair stale indexes; do not delete the database"
}

// checkSessionContentStore reports sessions whose canonical EventStore is missing
// content that only the legacy SQLite transcript still holds. This is the state a
// mixed-build migration leaves behind; the repair adopts the delta instead of
// deleting it. The check is strictly read-only.
func checkSessionContentStore(settings *config.Settings) Check {
	if settings == nil {
		return Check{ID: "sessions.content_store", Status: StatusSkip, Title: "Session content store", Detail: "settings unavailable"}
	}
	dir := settings.GetSessionDir()
	path := session.RootDatabasePath(dir)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Check{ID: "sessions.content_store", Status: StatusSkip, Title: "Session content store", Detail: path + " (not created yet)"}
	} else if err != nil {
		return Check{ID: "sessions.content_store", Status: StatusError, Title: "Session content store", Detail: err.Error()}
	}
	splits, err := session.ListSplitContentStoreSessions(dir)
	if err != nil {
		return Check{
			ID: "sessions.content_store", Status: StatusWarn, Title: "Session content store",
			Detail: "could not inspect session content stores: " + err.Error(),
			Fix:    "Back up the session directory and retry; do not delete sessions.db or the event-store directory",
		}
	}
	if len(splits) == 0 {
		return Check{ID: "sessions.content_store", Status: StatusOK, Title: "Session content store", Detail: "every migrated session is fully covered by the event log"}
	}
	var uncovered int64
	ids := make([]string, 0, len(splits))
	for i, split := range splits {
		uncovered += split.UncoveredRows
		if i < 5 {
			ids = append(ids, split.SessionID)
		}
	}
	detail := fmt.Sprintf("%d session(s), %d legacy row(s) not covered by the event log: %s", len(splits), uncovered, strings.Join(ids, ", "))
	if len(splits) > len(ids) {
		detail += fmt.Sprintf(" (+%d more)", len(splits)-len(ids))
	}
	return Check{
		ID: "sessions.content_store", Status: StatusWarn, Title: "Session content store", Detail: detail,
		Fix: "Quit every MothX process, back up the session directory, then run `mothx doctor --repair-session-content all` (the delta is adopted into the event log before anything is retired)",
	}
}

// checkSessionTurnBoundaries reports sessions whose canonical EventStore owns the
// transcript but whose conversation-turn control rows address no matching turn
// marker. These turns can be read but not forked: the boundary is the only fork
// point, and inferring one would fabricate history. The check is read-only and
// reports only sessions already on the event store, because a legacy session is
// bounded by `entries.seq` and has no projection marker to match.
func checkSessionTurnBoundaries(settings *config.Settings) Check {
	check := Check{ID: "sessions.turn_boundaries", Status: StatusSkip, Title: "Session turn boundaries", Detail: "settings unavailable"}
	if settings == nil {
		return check
	}
	dir := settings.GetSessionDir()
	path := session.RootDatabasePath(dir)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		check.Detail = path + " (not created yet)"
		return check
	} else if err != nil {
		check.Status, check.Detail = StatusError, err.Error()
		return check
	}
	sessions, err := session.ListUnmappedTurnBoundarySessions(dir)
	if err != nil {
		check.Status = StatusWarn
		check.Detail = "could not inspect session turn boundaries: " + err.Error()
		check.Fix = "Back up the session directory and retry; do not delete sessions.db or the event-store directory"
		return check
	}
	if len(sessions) == 0 {
		check.Status, check.Detail = StatusOK, "every migrated turn boundary addresses its own marker"
		return check
	}
	var turns int64
	ids := make([]string, 0, 5)
	for i, item := range sessions {
		turns += item.UnmappedTurns
		if i < 5 {
			ids = append(ids, item.SessionID)
		}
	}
	detail := fmt.Sprintf("%d session(s), %d turn(s) with no matching marker: %s", len(sessions), turns, strings.Join(ids, ", "))
	if len(sessions) > len(ids) {
		detail += fmt.Sprintf(" (+%d more)", len(sessions)-len(ids))
	}
	return Check{
		ID: "sessions.turn_boundaries", Status: StatusWarn, Title: "Session turn boundaries",
		Detail: detail,
		Fix:    "Content was lost before the event log owned it and cannot be reconstructed. These turns still read normally but cannot be forked; keep the sessions.db backup and do not delete the event-store directory",
	}
}

// SessionContentRepair is the outcome of one split-store repair attempt.
type SessionContentRepair struct {
	SessionID      string `json:"sessionId"`
	CWD            string `json:"cwd,omitempty"`
	AdoptedEntries int    `json:"adoptedEntries"`
	RetiredEntries int64  `json:"retiredEntries"`
	Deferred       bool   `json:"deferred,omitempty"`
	Error          string `json:"error,omitempty"`
}

// sessionContentRepairTimeout bounds one attempt. Adoption only appends a bounded
// delta, so a wedged database must not hang the command forever.
const sessionContentRepairTimeout = 5 * time.Minute

// RepairSessionContent adopts the split-store delta of one session, or of every
// split session when target is "all". It never deletes content: adoption appends
// the uncovered rows to the canonical event log and only then retires the covered
// duplicate. A session another process is running is reported as deferred, and a
// failure in one session does not stop the rest.
func RepairSessionContent(ctx context.Context, settings *config.Settings, target string) []SessionContentRepair {
	if settings == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sessionDir := settings.GetSessionDir()
	path := session.RootDatabasePath(sessionDir)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []SessionContentRepair{{Error: err.Error()}}
	}
	target = strings.TrimSpace(target)
	var candidates []session.SplitStoreSession
	if target == "" || strings.EqualFold(target, "all") {
		splits, err := session.ListSplitContentStoreSessions(sessionDir)
		if err != nil {
			return []SessionContentRepair{{Error: err.Error()}}
		}
		candidates = splits
	} else {
		candidates = []session.SplitStoreSession{{SessionID: target}}
	}
	repairs := make([]SessionContentRepair, 0, len(candidates))
	for _, candidate := range candidates {
		attemptCtx, cancel := context.WithTimeout(ctx, sessionContentRepairTimeout)
		result, err := session.AdoptLegacyContentByID(attemptCtx, sessionDir, candidate.SessionID)
		cancel()
		repair := SessionContentRepair{
			SessionID: candidate.SessionID, CWD: candidate.CWD,
			AdoptedEntries: result.AdoptedEntries, RetiredEntries: result.RetiredEntries, Deferred: result.Deferred,
		}
		if err != nil {
			repair.Error = err.Error()
			repair.Deferred = false
		}
		repairs = append(repairs, repair)
	}
	return repairs
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

// checkWorkerBridge verifies the Router worker MCP bridge preconditions without
// launching a worker: the process can bind a tcp4 loopback ephemeral port, and
// every profile's driver/bridge/override configuration is valid. Remote
// profiles are reported as unreachable rather than as errors, because a remote
// worker is a supported configuration without the loopback bridge.
func checkWorkerBridge(settings *config.Settings) []Check {
	skip := Check{ID: "acp.worker_bridge", Status: StatusSkip, Title: "Router worker bridge", Detail: "no ACP worker profiles configured"}
	if settings == nil || len(settings.ACPAgents) == 0 {
		return []Check{skip}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return []Check{{ID: "acp.worker_bridge", Status: StatusError, Title: "Router worker bridge",
			Detail: "loopback tcp4 bind failed: " + err.Error(),
			Fix:    "Permit local loopback binds; the worker bridge cannot use a public URL or stdio"}}
	}
	address := listener.Addr().String()
	_ = listener.Close()
	checks := []Check{{ID: "acp.worker_bridge", Status: StatusOK, Title: "Router worker bridge",
		Detail: "loopback tcp4 bind ok (" + address + ")"}}
	checks = append(checks, checkWorkerBridgeProtocol()...)
	for _, profile := range settings.ACPAgents {
		if !profile.Enabled {
			continue
		}
		if err := profile.Validate(); err != nil {
			checks = append(checks, Check{ID: "acp.worker_bridge", Status: StatusError, Title: "Router worker bridge",
				Detail: fmt.Sprintf("profile %q is invalid: %v", profile.ID, err),
				Fix:    "Fix the profile driver/mcpBridge/capabilityOverrides fields"})
			continue
		}
		switch {
		case profile.IsRemote():
			checks = append(checks, Check{ID: "acp.worker_bridge", Status: StatusWarn, Title: "Router worker bridge",
				Detail: fmt.Sprintf("profile %q is remote; the loopback bridge is unreachable", profile.ID)})
		case profile.EffectiveMCPBridge() == config.ACPMCPBridgeOff:
			checks = append(checks, Check{ID: "acp.worker_bridge", Status: StatusSkip, Title: "Router worker bridge",
				Detail: fmt.Sprintf("profile %q disables the bridge (mcpBridge=off)", profile.ID)})
		default:
			checks = append(checks, Check{ID: "acp.worker_bridge", Status: StatusOK, Title: "Router worker bridge",
				Detail: fmt.Sprintf("profile %q driver=%s bridge=auto", profile.ID, profile.EffectiveDriver())})
		}
	}
	return checks
}

// checkWorkerBridgeProtocol exercises the real bridge authorization and
// transport contract on an ephemeral loopback port: a missing token is 401, an
// SSE request is 406, and an authorized initialize is 200. It uses no session
// data and never logs the token.
func checkWorkerBridgeProtocol() []Check {
	server := agentruntime.NewWorkerBridgeServer()
	server.SetIdleTimeout(60 * time.Millisecond)
	defer server.Close()
	scope := &agentruntime.WorkerBridgeScope{
		SessionID: "doctor", ChildID: "doctor-probe",
		Capabilities: workerdriver.WorkerCapabilities{MCPHTTP: true},
	}
	token, payload, err := server.Register(scope)
	if err != nil {
		return []Check{{ID: "acp.worker_bridge.protocol", Status: StatusError, Title: "Worker bridge protocol",
			Detail: err.Error(), Fix: "Report this as a bug: the loopback bridge could not start"}}
	}
	encoded, _ := json.Marshal(payload)
	var entries []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(encoded, &entries); err != nil || len(entries) == 0 || entries[0].URL == "" {
		return []Check{{ID: "acp.worker_bridge.protocol", Status: StatusError, Title: "Worker bridge protocol",
			Detail: "bridge payload had no usable loopback URL"}}
	}
	url := entries[0].URL
	probe := func(tokenValue, accept, body string) (int, error) {
		request, requestErr := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
		if requestErr != nil {
			return 0, requestErr
		}
		request.Header.Set("Content-Type", "application/json")
		if accept != "" {
			request.Header.Set("Accept", accept)
		}
		if tokenValue != "" {
			request.Header.Set("Authorization", "Bearer "+tokenValue)
		}
		response, doErr := http.DefaultClient.Do(request)
		if doErr != nil {
			return 0, doErr
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode, nil
	}
	checks := make([]Check, 0, 3)
	if status, err := probe("", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`); err != nil || status != http.StatusUnauthorized {
		checks = append(checks, Check{ID: "acp.worker_bridge.protocol", Status: StatusError, Title: "Worker bridge protocol",
			Detail: fmt.Sprintf("missing token returned %d (err=%v), want 401", status, err), Fix: "The bridge must reject requests without a per-child token"})
	} else {
		checks = append(checks, Check{ID: "acp.worker_bridge.protocol", Status: StatusOK, Title: "Worker bridge protocol", Detail: "401 without a token"})
	}
	if status, err := probe(token, "text/event-stream", `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{}}`); err != nil || status != http.StatusNotAcceptable {
		checks = append(checks, Check{ID: "acp.worker_bridge.protocol", Status: StatusError, Title: "Worker bridge protocol",
			Detail: fmt.Sprintf("SSE request returned %d (err=%v), want 406", status, err), Fix: "The bridge must not open an SSE stream"})
	} else {
		checks = append(checks, Check{ID: "acp.worker_bridge.protocol", Status: StatusOK, Title: "Worker bridge protocol", Detail: "406 for text/event-stream"})
	}
	if status, err := probe(token, "", `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{}}`); err != nil || status != http.StatusOK {
		checks = append(checks, Check{ID: "acp.worker_bridge.protocol", Status: StatusError, Title: "Worker bridge protocol",
			Detail: fmt.Sprintf("authorized initialize returned %d (err=%v), want 200", status, err)})
	} else {
		checks = append(checks, Check{ID: "acp.worker_bridge.protocol", Status: StatusOK, Title: "Worker bridge protocol", Detail: "authorized initialize returned 200"})
	}

	// Idle shutdown: after the last token is revoked, the listener must close
	// within the idle window and stop accepting connections. A long-lived
	// process must not retain an unused ephemeral port.
	port := server.Port()
	server.Revoke(token)
	idleClosed := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if server.Port() == 0 {
			idleClosed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !idleClosed {
		checks = append(checks, Check{ID: "acp.worker_bridge.protocol", Status: StatusError, Title: "Worker bridge protocol",
			Detail: "listener stayed bound after the last worker token was revoked",
			Fix:    "The idle timer must close the loopback listener instead of retaining the port"})
	} else {
		connection, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 150*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			checks = append(checks, Check{ID: "acp.worker_bridge.protocol", Status: StatusError, Title: "Worker bridge protocol",
				Detail: fmt.Sprintf("idle listener still accepted a connection on port %d", port)})
		} else {
			checks = append(checks, Check{ID: "acp.worker_bridge.protocol", Status: StatusOK, Title: "Worker bridge protocol",
				Detail: "idle listener closed after token revocation"})
		}
	}
	return checks
}

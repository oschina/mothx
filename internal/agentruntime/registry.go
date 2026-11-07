package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/browser"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/mcp"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/skills"
	"github.com/oschina/mothx/internal/tools"
)

// RegistryMutator is an adapter policy callback for tools that cannot yet be
// represented as core Runtime capabilities.
type RegistryMutator func(*tools.Registry) error

// RegistryPolicy controls shared registry construction without letting adapters
// own its sandbox, workdir, or lifecycle.
type RegistryPolicy struct {
	RegisterDefaults bool
	EnablePlanTool   *bool
	SkillsMgr        *skills.Manager
	Browser          bool
	// ImageGeneration exposes the image-generation tool when settings enable
	// it. Entries opt in; the settings gate remains the shared owner of the
	// switch, so resource refresh can reconcile it.
	ImageGeneration bool
	// Question exposes the interactive question tool. Entries map it to their
	// protocol surface (for example ACP request_permission); it is not part of
	// the default tool surface.
	Question bool
	Mutators []RegistryMutator
}

// BuildRegistry creates the base registry and applies explicit adapter tool
// policy. It is the only registry construction API for non-test adapters.
func BuildRegistry(workDir string, sandboxMgr *sandbox.Manager, settings *config.Settings, policy RegistryPolicy) (*tools.Registry, error) {
	if workDir == "" {
		return nil, fmt.Errorf("registry work directory is required")
	}
	var active sandbox.Sandbox
	if sandboxMgr != nil {
		active = sandboxMgr.GetActive()
	}
	registry := tools.NewRegistry(workDir, active)
	if policy.RegisterDefaults {
		if policy.EnablePlanTool == nil {
			registry.RegisterDefaults()
		} else {
			registry.RegisterDefaultsWithPlanTool(*policy.EnablePlanTool)
		}
	}
	if policy.SkillsMgr != nil {
		registry.Register(tools.NewSkillRefTool(policy.SkillsMgr))
	}
	if policy.Browser {
		browser.RegisterTool(registry)
	}
	if policy.ImageGeneration && settings != nil && settings.IsImageGenerationEnabled() {
		registry.Register(tools.NewImageGenerationTool(settings))
	}
	if settings != nil && settings.IsWebSearchEnabled() {
		registry.Register(tools.NewWebSearchTool(settings))
		if settings.WebSearchBackend() == config.WebSearchBackendDeepSeek {
			registry.Register(tools.NewWebFetchTool(settings))
		} else {
			registry.Remove("web_fetch")
		}
	}
	if policy.Question {
		registry.Register(tools.NewQuestionTool(registry))
	}
	for _, mutate := range policy.Mutators {
		if mutate == nil {
			continue
		}
		if err := mutate(registry); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// MCPPolicy describes adapter-specific MCP transport behavior while Runtime
// owns client connection and release.
type MCPPolicy struct {
	Servers   []mcp.ServerConfig
	Callbacks mcp.Callbacks
	Optional  bool
	// OverallTimeout bounds the whole connection group. Zero uses only the
	// per-operation timeouts. It lets an entry point cap how long a set of slow
	// configured servers may collectively delay a run without changing the
	// per-server timeout semantics.
	OverallTimeout time.Duration
	OnError        func(error)
}

// ConnectMCP connects policy servers to this Runtime's registry. Strict policy
// returns errors; optional policy records them via OnError and leaves the
// Runtime usable without MCP clients.
func (r *SessionRuntime) ConnectMCP(ctx context.Context, policy MCPPolicy) error {
	if r == nil || r.Registry == nil {
		return fmt.Errorf("runtime registry is required")
	}
	servers := make([]mcp.ServerConfig, 0, len(policy.Servers))
	for _, server := range policy.Servers {
		if config.MCPServerEnabled(server) {
			servers = append(servers, server)
		}
	}
	if len(servers) == 0 {
		return nil
	}
	clients, failures, err := mcp.ConnectServersWithOptions(ctx, servers, r.Registry, policy.Callbacks, mcp.ConnectOptions{
		Optional:       policy.Optional,
		OverallTimeout: policy.OverallTimeout,
	})
	if err != nil {
		if policy.Optional {
			if policy.OnError != nil {
				policy.OnError(err)
			}
			return nil
		}
		return err
	}
	// Publish under the runtime lock: a deferred connection can run concurrently
	// with Shutdown, and Close reads MCPClients under the same lock.
	descriptions := make([]string, 0, len(failures))
	for _, failure := range failures {
		descriptions = append(descriptions, failure.Error())
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		mcp.CloseClients(clients)
		return fmt.Errorf("agent runtime is closed")
	}
	r.MCPClients = clients
	// Assign unconditionally so a later clean connect clears a prior failure set
	// instead of leaving a stale warning surface behind.
	r.mcpFailures = descriptions
	r.mu.Unlock()
	if len(failures) > 0 && policy.OnError != nil {
		for _, failure := range failures {
			policy.OnError(failure.Err)
		}
	}
	return nil
}

// ConnectConfiguredMCP loads the standard global/project MCP configuration,
// appends explicitly negotiated protocol servers, and applies the same
// strict/optional connection behavior as ConnectMCP. This keeps adapters from
// creating a second configuration resolution path while still honoring ACP's
// standard mcpServers request field.
func (r *SessionRuntime) ConnectConfiguredMCP(ctx context.Context, policy MCPPolicy) error {
	if r == nil {
		return fmt.Errorf("runtime is required")
	}
	servers, err := mcp.LoadConfiguredServers(r.WorkDir)
	if err != nil {
		if policy.Optional {
			if policy.OnError != nil {
				policy.OnError(err)
			}
			return nil
		}
		return err
	}
	policy.Servers = append(servers, policy.Servers...)
	return r.ConnectMCP(ctx, policy)
}

// ReloadConfiguredMCP reconnects the configured MCP servers and replaces their
// tools on the shared registry without restarting the process. It is the
// Runtime-owned hot-reload path: an entry point only decides when to reload
// and how to project the outcome.
//
// The reload is safe against a live run. A running Agent keeps the tool
// surface it froze at build time, so replacing the registry entries cannot
// change an in-flight turn. The next build observes the new tool set.
func (r *SessionRuntime) ReloadConfiguredMCP(ctx context.Context, policy MCPPolicy) error {
	if r == nil {
		return fmt.Errorf("runtime is required")
	}
	if err := r.ensureOpen(); err != nil {
		return err
	}
	servers, err := mcp.LoadConfiguredServers(r.WorkDir)
	if err != nil {
		return fmt.Errorf("load MCP configuration: %w", err)
	}
	policy.Servers = append(servers, policy.Servers...)
	return r.reloadMCP(ctx, policy)
}

// reloadMCP connects the given servers and swaps the Runtime-owned MCP tools
// on the shared registry. A server that fails to reconnect keeps its previous
// client and tools, so a partial failure does not tear down the working
// configuration; only the clients that were actually replaced (or whose server
// was removed/disabled) are closed.
func (r *SessionRuntime) reloadMCP(ctx context.Context, policy MCPPolicy) error {
	if r == nil || r.Registry == nil {
		return fmt.Errorf("runtime registry is required")
	}
	enabled := make([]mcp.ServerConfig, 0, len(policy.Servers))
	enabledNames := make(map[string]struct{}, len(policy.Servers))
	for _, server := range policy.Servers {
		if config.MCPServerEnabled(server) {
			enabled = append(enabled, server)
			enabledNames[strings.TrimSpace(server.Name)] = struct{}{}
		}
	}
	if len(enabled) == 0 {
		// Nothing is configured anymore: drop the existing MCP tools and
		// release the clients so the session reflects the new configuration.
		r.releaseMCPTools()
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return fmt.Errorf("agent runtime is closed")
		}
		r.MCPClients = nil
		r.mcpFailures = nil
		r.mu.Unlock()
		return nil
	}
	clients, failures, err := mcp.ConnectServersWithOptions(ctx, enabled, r.Registry, policy.Callbacks, mcp.ConnectOptions{
		Optional:       policy.Optional,
		OverallTimeout: policy.OverallTimeout,
	})
	if err != nil {
		if policy.Optional {
			if policy.OnError != nil {
				policy.OnError(err)
			}
			return nil
		}
		return err
	}
	descriptions := make([]string, 0, len(failures))
	for _, failure := range failures {
		descriptions = append(descriptions, failure.Error())
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		mcp.CloseClients(clients)
		return fmt.Errorf("agent runtime is closed")
	}
	previous := r.MCPClients
	next, released := reconcileMCPReload(r.Registry, previous, clients, failures, enabledNames)
	r.MCPClients = next
	r.mcpFailures = descriptions
	r.mu.Unlock()
	// The new connections are live and failed servers kept their old client, so
	// only the replaced/removed clients are released now.
	mcp.CloseClients(released)
	if len(failures) > 0 && policy.OnError != nil {
		for _, failure := range failures {
			policy.OnError(failure.Err)
		}
	}
	return nil
}

// reconcileMCPReload computes the Runtime's next client list after a reload.
// A failed server is represented by its previous client, which stays connected,
// so its advertised tools remain usable. A server that disappeared from (or was
// disabled in) the configuration loses its owned tools even though the connect
// path only replaces configured owners.
func reconcileMCPReload(registry *tools.Registry, previous, connected []*mcp.Client, failures []mcp.ServerConnectFailure, enabledNames map[string]struct{}) (next, released []*mcp.Client) {
	previousByName := make(map[string]*mcp.Client, len(previous))
	for _, client := range previous {
		if client != nil {
			previousByName[client.Name()] = client
		}
	}
	next = append(next, connected...)
	retained := make(map[*mcp.Client]struct{})
	for _, failure := range failures {
		client := previousByName[strings.TrimSpace(failure.Name)]
		if client == nil {
			continue
		}
		if _, kept := retained[client]; kept {
			continue
		}
		retained[client] = struct{}{}
		next = append(next, client)
	}
	for _, client := range previous {
		if client == nil {
			continue
		}
		if _, kept := retained[client]; kept {
			continue
		}
		if _, stillEnabled := enabledNames[strings.TrimSpace(client.Name())]; !stillEnabled && registry != nil {
			registry.ReplaceOwned(mcp.ToolOwner(client.Name()), nil)
		}
		released = append(released, client)
	}
	return next, released
}

// releaseMCPTools removes the MCP-owned tools from the shared registry and
// closes the Runtime-owned clients. It is used when the configuration no
// longer contains any enabled server.
func (r *SessionRuntime) releaseMCPTools() {
	if r == nil || r.Registry == nil {
		return
	}
	r.mu.Lock()
	clients := r.MCPClients
	r.MCPClients = nil
	r.mu.Unlock()
	for _, client := range clients {
		r.Registry.ReplaceOwned(mcp.ToolOwner(client.Name()), nil)
	}
	mcp.CloseClients(clients)
}

// CloseMCPClients releases clients held by legacy adapter aliases during migration.
func CloseMCPClients(clients []*mcp.Client) {
	mcp.CloseClients(clients)
}

// registryExposesImageGeneration reports whether the assembled registry
// includes the image-generation tool. SessionRuntime keeps this build-time
// capability so resource refresh reconciles only the settings gate of an entry
// that actually exposes the tool, without flipping tool policy for entries
// that never had it.
func registryExposesImageGeneration(registry *tools.Registry) bool {
	if registry == nil {
		return false
	}
	_, ok := registry.Get("image_generation")
	return ok
}

// DefaultPlanToolPolicy returns the configured plan-tool setting.
func DefaultPlanToolPolicy(settings *config.Settings) *bool {
	if settings == nil {
		return nil
	}
	enabled := settings.IsPlanToolEnabled()
	return &enabled
}

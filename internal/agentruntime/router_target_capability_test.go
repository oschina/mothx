package agentruntime

import (
	"strings"
	"testing"

	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/tools"
)

// newCapabilitySummaryRuntime builds a Router Runtime with an explicit worker
// profile and bridge state, then publishes the model-facing tool surface.
func newCapabilitySummaryRuntime(t *testing.T, profile config.ACPAgentProfile, bridge *WorkerBridgeServer) (*SessionRuntime, *tools.Registry) {
	t.Helper()
	runtime := newRouterBuildRuntime(t)
	settings := runtime.SettingsSnapshot()
	profile.Enabled = true
	settings.ACPAgents = append(settings.ACPAgents, profile)
	runtime.mu.Lock()
	runtime.resourceSettings = settings
	runtime.Mode = ModeRouter
	runtime.WorkerBridge = bridge
	runtime.mu.Unlock()
	if err := runtime.SetRouterBinding(RouterBinding{
		ACPProfileID:     profile.ID,
		ApprovalStrategy: config.ACPPermissionAutoAllowOnce,
	}); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(t.TempDir(), nil)
	// RefreshRouterTarget publishes through the Runtime-owned registry, so the
	// fixture adopts the registry it synchronizes instead of holding a stale one.
	runtime.Registry = registry
	SynchronizeToolGroups(runtime, registry, ToolGroupPolicy{Delegate: true, Mode: ModeRouter}, agent.NewAgentManager(nil))
	return runtime, registry
}

func routerWorkerSummary(t *testing.T, registry *tools.Registry) (agent.RouterTarget, string) {
	t.Helper()
	tool, ok := registry.Get("delegate_worker_mgr")
	if !ok {
		t.Fatal("delegate_worker_mgr was not registered")
	}
	mgr, ok := tool.(*agent.DelegateWorkerMgrTool)
	if !ok {
		t.Fatalf("delegate_worker_mgr has unexpected type %T", tool)
	}
	target, ok := mgr.RouterTarget()
	if !ok {
		t.Fatal("the activated Router worker was not published")
	}
	return target, mgr.Description()
}

func advertisedWorkerCapabilities() map[string]any {
	return map[string]any{
		"sessionCapabilities": map[string]any{
			"resume": map[string]any{},
			"load":   map[string]any{},
		},
		"promptCapabilities": map[string]any{
			"image":           true,
			"embeddedContext": true,
		},
		"mcpCapabilities": map[string]any{"http": true},
	}
}

// The model-facing summary must be a projection of the resolved capability set,
// not a literal "report/ask" chosen from profile config. A live bridge plus an
// advertised media/resume set must produce the full bounded hint vocabulary.
func TestRouterTargetSummaryUsesResolvedCapabilities(t *testing.T) {
	bridge := NewWorkerBridgeServer()
	t.Cleanup(func() { _ = bridge.Close() })
	runtime, registry := newCapabilitySummaryRuntime(t, config.ACPAgentProfile{
		ID: "opencode", Command: []string{"/opt/opencode", "acp"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		TerminalAccess:   true,
	}, bridge)
	runtime.recordWorkerAdvertisedCapabilities("opencode", advertisedWorkerCapabilities())
	SynchronizeToolGroups(runtime, registry, ToolGroupPolicy{Delegate: true, Mode: ModeRouter}, agent.NewAgentManager(nil))

	target, description := routerWorkerSummary(t, registry)
	summary := target.Summary["opencode"]
	for _, want := range []string{"report/ask", "attachments", "write", "terminal", "resume"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q is missing resolved capability %q", summary, want)
		}
	}
	if !strings.Contains(description, "Worker capabilities:") || !strings.Contains(description, "opencode="+summary) {
		t.Fatalf("tool description does not carry the summary %q: %s", summary, description)
	}

	// Re-resolving after the bridge closes must drop report/ask immediately; the
	// published summary is derived state, not a cached literal.
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	SynchronizeToolGroups(runtime, registry, ToolGroupPolicy{Delegate: true, Mode: ModeRouter}, agent.NewAgentManager(nil))
	target, description = routerWorkerSummary(t, registry)
	summary = target.Summary["opencode"]
	if strings.Contains(summary, "report/ask") {
		t.Fatalf("summary kept report/ask after the bridge closed: %q", summary)
	}
	if !strings.Contains(summary, "no live listener") {
		t.Fatalf("summary does not name the closed bridge: %q", summary)
	}
	if strings.Contains(description, "report/ask") {
		t.Fatalf("description kept report/ask after the bridge closed: %s", description)
	}
}

// A capabilityOverrides entry that tightens mcpHttp must remove report/ask from
// the model-visible description even though a bridge is live.
func TestRouterTargetSummaryRespectsCapabilityOverrides(t *testing.T) {
	bridge := NewWorkerBridgeServer()
	t.Cleanup(func() { _ = bridge.Close() })
	runtime, registry := newCapabilitySummaryRuntime(t, config.ACPAgentProfile{
		ID: "opencode", Command: []string{"/opt/opencode", "acp"},
		PermissionPolicy:    config.ACPPermissionAutoAllowOnce,
		TerminalAccess:      true,
		CapabilityOverrides: map[string]bool{"mcpHttp": false},
	}, bridge)
	runtime.recordWorkerAdvertisedCapabilities("opencode", advertisedWorkerCapabilities())
	SynchronizeToolGroups(runtime, registry, ToolGroupPolicy{Delegate: true, Mode: ModeRouter}, agent.NewAgentManager(nil))

	target, description := routerWorkerSummary(t, registry)
	summary := target.Summary["opencode"]
	if strings.Contains(summary, "report/ask") {
		t.Fatalf("summary still advertises report/ask after mcpHttp=false: %q", summary)
	}
	if !strings.Contains(summary, "capabilityOverrides") {
		t.Fatalf("summary does not explain the override degradation: %q", summary)
	}
	if strings.Contains(description, "report/ask") {
		t.Fatalf("model-visible description still advertises report/ask: %s", description)
	}
	// The remaining resolved capabilities are still projected.
	for _, want := range []string{"attachments", "write", "terminal", "resume"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q dropped the unrelated capability %q", summary, want)
		}
	}
}

// A closed bridge is not available even before the first registration. The
// summary must fall back to the non-bridge hints and explain the missing
// listener instead of advertising report/ask.
func TestRouterTargetSummaryRespectsClosedBridge(t *testing.T) {
	bridge := NewWorkerBridgeServer()
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, registry := newCapabilitySummaryRuntime(t, config.ACPAgentProfile{
		ID: "opencode", Command: []string{"/opt/opencode", "acp"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}, bridge)
	runtime.recordWorkerAdvertisedCapabilities("opencode", advertisedWorkerCapabilities())
	SynchronizeToolGroups(runtime, registry, ToolGroupPolicy{Delegate: true, Mode: ModeRouter}, agent.NewAgentManager(nil))

	target, description := routerWorkerSummary(t, registry)
	summary := target.Summary["opencode"]
	if strings.Contains(summary, "report/ask") {
		t.Fatalf("summary advertised report/ask with a closed bridge: %q", summary)
	}
	if !strings.Contains(summary, "no live listener") {
		t.Fatalf("summary does not name the closed bridge: %q", summary)
	}
	if strings.Contains(description, "report/ask") {
		t.Fatalf("model-visible description advertised report/ask with a closed bridge: %s", description)
	}
	for _, want := range []string{"attachments", "write", "resume"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q dropped the known capability %q", summary, want)
		}
	}
}

// Remote transports and mcpBridge=off never receive injection, so their
// summaries must not advertise report/ask regardless of advertised MCP HTTP.
// A real worker initialize must republish the Router target on its own. The
// adapter synchronizes tool groups at activation, before any worker exists; the
// next BuildAgent freezes the prompt, so the description has to pick up the
// negotiated attachment/resume/report capabilities through the Runtime refresh,
// never through another manual SynchronizeToolGroups call.
func TestWorkerInitializeRepublishesRouterTargetWithoutToolSync(t *testing.T) {
	bridge := NewWorkerBridgeServer()
	t.Cleanup(func() { _ = bridge.Close() })
	profile := config.ACPAgentProfile{
		ID: "opencode", Command: []string{"/opt/opencode", "acp"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		TerminalAccess:   true,
	}
	runtime, registry := newCapabilitySummaryRuntime(t, profile, bridge)

	target, description := routerWorkerSummary(t, registry)
	if summary := target.Summary[profile.ID]; strings.Contains(summary, "attachments") || strings.Contains(summary, "resume") {
		t.Fatalf("pre-initialize summary already advertises negotiated capabilities: %q", summary)
	}
	if strings.Contains(description, "attachments") || strings.Contains(description, "resume") {
		t.Fatalf("pre-initialize description already advertises negotiated capabilities: %s", description)
	}

	advertised := advertisedWorkerCapabilities()
	proxy := &acpChildProxy{runtime: runtime, profile: profile, mode: ModeRouter}
	proxy.recordCapabilities(&acpRemoteWorker{
		init: acpclient.InitializeResult{AgentCapabilities: advertised},
		caps: resolveWorkerCapabilities(profile, advertised, workerCapabilityPolicy{
			BridgeAvailable: true, FSReadAuthorized: true, FSWriteAuthorized: true, TerminalAuthorized: true,
		}),
	})

	// No SynchronizeToolGroups call between initialize and this assertion: the
	// production refresh path must have updated the already-registered tool.
	target, description = routerWorkerSummary(t, registry)
	summary := target.Summary[profile.ID]
	for _, want := range []string{"report/ask", "attachments", "write", "terminal", "resume"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("initialize did not refresh summary capability %q: %q", want, summary)
		}
	}
	if !strings.Contains(description, "opencode="+summary) {
		t.Fatalf("initialize did not refresh the tool description: %s", description)
	}
}

// Advertisement loss must be explained: a worker that advertises no prompt
// media or resume support is not broken, but the model must be told why those
// surfaces are missing instead of guessing from silence.
func TestRouterTargetSummaryExplainsUnadvertisedCapabilities(t *testing.T) {
	bridge := NewWorkerBridgeServer()
	t.Cleanup(func() { _ = bridge.Close() })
	profile := config.ACPAgentProfile{
		ID: "opencode", Command: []string{"/opt/opencode", "acp"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		TerminalAccess:   true,
	}
	runtime, registry := newCapabilitySummaryRuntime(t, profile, bridge)
	runtime.recordWorkerAdvertisedCapabilities(profile.ID, map[string]any{
		"mcpCapabilities": map[string]any{"http": true},
	})
	SynchronizeToolGroups(runtime, registry, ToolGroupPolicy{Delegate: true, Mode: ModeRouter}, agent.NewAgentManager(nil))

	target, _ := routerWorkerSummary(t, registry)
	summary := target.Summary[profile.ID]
	for _, want := range []string{
		"attachments: worker did not advertise image/audio/embeddedContext",
		"resume: worker did not advertise resume/load",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q is missing an actionable advertisement reason %q", summary, want)
		}
	}
}

// capabilityOverrides losses must name every removed non-bridge surface with
// the override key, so a model can distinguish policy from worker limitation.
func TestRouterTargetSummaryExplainsCapabilityOverrideLosses(t *testing.T) {
	bridge := NewWorkerBridgeServer()
	t.Cleanup(func() { _ = bridge.Close() })
	profile := config.ACPAgentProfile{
		ID: "opencode", Command: []string{"/opt/opencode", "acp"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		TerminalAccess:   true,
		CapabilityOverrides: map[string]bool{
			"promptImage": false, "promptAudio": false, "promptEmbeddedContext": false,
			"sessionResume": false, "sessionLoad": false,
			"fsWriteText": false, "terminal": false,
		},
	}
	runtime, registry := newCapabilitySummaryRuntime(t, profile, bridge)
	runtime.recordWorkerAdvertisedCapabilities(profile.ID, advertisedWorkerCapabilities())
	SynchronizeToolGroups(runtime, registry, ToolGroupPolicy{Delegate: true, Mode: ModeRouter}, agent.NewAgentManager(nil))

	target, _ := routerWorkerSummary(t, registry)
	summary := target.Summary[profile.ID]
	for _, want := range []string{
		"attachments: ", "promptImage: disabled by capabilityOverrides", "promptEmbeddedContext: disabled by capabilityOverrides",
		"resume: ", "sessionResume: disabled by capabilityOverrides", "sessionLoad: disabled by capabilityOverrides",
		"write: fsWriteText: disabled by capabilityOverrides",
		"terminal: terminal: disabled by capabilityOverrides",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q is missing an actionable override reason %q", summary, want)
		}
	}
	if !strings.Contains(summary, "report/ask") {
		// A live bridge still grants report/ask; only non-bridge losses changed.
		t.Fatalf("summary dropped the unaffected bridge capability: %q", summary)
	}
}

// Remote transports and mcpBridge=off never receive injection, so their
// summaries must not advertise report/ask regardless of advertised MCP HTTP.
func TestRouterTargetSummaryRespectsRemoteAndDisabledBridge(t *testing.T) {
	cases := map[string]config.ACPAgentProfile{
		"remote": {
			ID: "remote", Transport: string(config.ACPAgentTransportWebSocket),
			URL: "wss://worker.example.test/acp", PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		},
		"off": {
			ID: "off", Command: []string{"/opt/off", "acp"}, MCPBridge: config.ACPMCPBridgeOff,
			PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		},
	}
	for name, profile := range cases {
		t.Run(name, func(t *testing.T) {
			bridge := NewWorkerBridgeServer()
			t.Cleanup(func() { _ = bridge.Close() })
			runtime, registry := newCapabilitySummaryRuntime(t, profile, bridge)
			runtime.recordWorkerAdvertisedCapabilities(profile.ID, advertisedWorkerCapabilities())
			SynchronizeToolGroups(runtime, registry, ToolGroupPolicy{Delegate: true, Mode: ModeRouter}, agent.NewAgentManager(nil))

			target, description := routerWorkerSummary(t, registry)
			summary := target.Summary[profile.ID]
			if strings.Contains(summary, "report/ask") {
				t.Fatalf("%s summary advertised report/ask: %q", name, summary)
			}
			if name == "remote" && !strings.Contains(summary, "remote transport") {
				t.Fatalf("remote summary lacks an actionable reason: %q", summary)
			}
			if name == "off" && !strings.Contains(summary, "mcpBridge=off") {
				t.Fatalf("off summary lacks an actionable reason: %q", summary)
			}
			if strings.Contains(description, "report/ask") {
				t.Fatalf("%s description advertised report/ask: %s", name, description)
			}
		})
	}
}

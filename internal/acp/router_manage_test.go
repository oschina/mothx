package acp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// acpRouterTestLead satisfies the manager parent validation for a direct child
// construction test.
type acpRouterTestLead struct{ id agentpkg.AgentID }

func (a *acpRouterTestLead) ID() agentpkg.AgentID       { return a.id }
func (a *acpRouterTestLead) ParentID() agentpkg.AgentID { return "" }
func (a *acpRouterTestLead) Run(context.Context, string) <-chan agentpkg.Event {
	ch := make(chan agentpkg.Event)
	close(ch)
	return ch
}
func (a *acpRouterTestLead) RunWithMessages(ctx context.Context, _ []agentpkg.Message) <-chan agentpkg.Event {
	return a.Run(ctx, "")
}
func (a *acpRouterTestLead) Abort()                                  {}
func (a *acpRouterTestLead) GetMessages() []agentpkg.Message         { return nil }
func (a *acpRouterTestLead) SetMessages([]agentpkg.Message)          {}
func (a *acpRouterTestLead) GetContext() *agentpkg.AgentContext      { return nil }
func (a *acpRouterTestLead) SetContext(*agentpkg.AgentContext)       {}
func (a *acpRouterTestLead) GetContextUsage() *agentpkg.ContextUsage { return nil }
func (a *acpRouterTestLead) LoadHistoryMessages([]agentpkg.Message)  {}
func (a *acpRouterTestLead) HandleApprovalResponse(string, bool)     {}

// newRouterManageFixture builds an ACP server with one persisted session and a
// configured external ACP worker, so the Desktop-facing Router management
// methods can be exercised through the real activation path.
func newRouterManageFixture(t *testing.T, output *syncedBuffer) (*server, string) {
	t.Helper()
	configDir := t.TempDir()
	sessionDir := filepath.Join(configDir, "sessions")
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
	}}
	t.Setenv("MOTHX_DIR", configDir)
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}

	srv := newPhase1FixtureServer(output)
	srv.settings = settings

	mgr := session.New(sessionDir, sessionDir)
	if err := mgr.InitWithID("router-session"); err != nil {
		t.Fatal(err)
	}
	registry, err := agentruntime.BuildRegistry(sessionDir, nil, settings, agentruntime.RegistryPolicy{RegisterDefaults: true})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := agentruntime.AttachSessionResources(agentruntime.AttachedResources{
		ID: "router-session", Source: agentruntime.SourceACP, WorkDir: sessionDir,
		Manager: mgr, Registry: registry, Settings: settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model", Name: "model", ContextWindow: 128000}}, nil)
	if err := runtime.ConfigureSession(p, "mock", p.Models()[0], agentruntime.ModeYolo, ""); err != nil {
		t.Fatal(err)
	}
	srv.sessions["router-session"] = &sessionRuntime{runtime: runtime, id: "router-session", mgr: mgr, registry: registry}
	return srv, "router-session"
}

// The Desktop management projection must bind the worker through the shared
// Runtime activation path, so the persisted binding and the Router mode are
// the canonical ones.
func TestManageRouterActivateBindsWorkerThroughRuntime(t *testing.T) {
	output := &syncedBuffer{}
	srv, sessionID := newRouterManageFixture(t, output)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/router/activate", map[string]any{
		"sessionId":  sessionID,
		"profileIds": []string{"codex"},
	}))
	if result["enabled"] != true {
		t.Fatalf("router state = %#v, want enabled", result)
	}
	if result["primaryProfile"] != "codex" {
		t.Fatalf("primary profile = %v, want codex", result["primaryProfile"])
	}
	if result["mode"] != agentruntime.ModeRouter {
		t.Fatalf("mode = %v, want router", result["mode"])
	}

	// The binding must be persisted so the next session attach replays it.
	rt := srv.sessionRuntime(sessionID)
	binding, ok := rt.runtime.RouterBinding()
	if !ok || binding.ACPProfileID != "codex" {
		t.Fatalf("persisted binding = %#v (ok=%t)", binding, ok)
	}
	// Activation must leave the session with a manager that owns the Router
	// binding and a registry that exposes the delegation surface.
	if rt.agentMgr == nil {
		t.Fatal("Router activation did not create the session manager")
	}
	if _, ok := rt.registry.Get("delegate_worker_mgr"); !ok {
		t.Fatal("Router activation did not install delegate_subagent")
	}
}

// The worker catalog is the same shared Runtime projection the TUI renders.
func TestManageRouterStateProjectsBinding(t *testing.T) {
	output := &syncedBuffer{}
	srv, sessionID := newRouterManageFixture(t, output)

	before := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/router/state", map[string]any{
		"sessionId": sessionID,
	}))
	if before["enabled"] != false {
		t.Fatalf("router state before activation = %#v, want disabled", before)
	}

	manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/router/activate", map[string]any{
		"sessionId":  sessionID,
		"profileIds": []string{"codex"},
	}))
	after := manageFixtureResult(t, callManageFixture(t, srv, output, 3, "mothx/manage/router/state", map[string]any{
		"sessionId": sessionID,
	}))
	if after["enabled"] != true {
		t.Fatalf("router state after activation = %#v, want enabled", after)
	}
	ids, _ := after["profileIds"].([]any)
	if len(ids) != 1 || ids[0] != "codex" {
		t.Fatalf("profileIds = %#v", after["profileIds"])
	}
}

// Leaving Router returns the session to an ordinary mode while keeping the
// binding available for a later re-entry.
func TestManageRouterDeactivateLeavesRouterMode(t *testing.T) {
	output := &syncedBuffer{}
	srv, sessionID := newRouterManageFixture(t, output)

	manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/router/activate", map[string]any{
		"sessionId":  sessionID,
		"profileIds": []string{"codex"},
	}))
	result := manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/router/deactivate", map[string]any{
		"sessionId": sessionID,
	}))
	if result["mode"] == agentruntime.ModeRouter {
		t.Fatalf("router state after deactivation = %#v, want a non-router mode", result)
	}
}

// An unbound or unknown worker must be refused, and the refusal must not leave
// a binding behind.
func TestManageRouterActivateRefusesUnknownWorker(t *testing.T) {
	output := &syncedBuffer{}
	srv, sessionID := newRouterManageFixture(t, output)

	code, _ := manageFixtureError(t, callManageFixture(t, srv, output, 1, "mothx/manage/router/activate", map[string]any{
		"sessionId":  sessionID,
		"profileIds": []string{"missing"},
	}))
	if code == "" {
		t.Fatal("an unknown worker was bound to a Router session")
	}
	rt := srv.sessionRuntime(sessionID)
	if _, ok := rt.runtime.RouterBinding(); ok {
		t.Fatal("a refused activation left a binding behind")
	}
}

// An empty worker list is a caller error, not an implicit "use the default".
func TestManageRouterActivateRequiresAWorker(t *testing.T) {
	output := &syncedBuffer{}
	srv, sessionID := newRouterManageFixture(t, output)

	code, _ := manageFixtureError(t, callManageFixture(t, srv, output, 1, "mothx/manage/router/activate", map[string]any{
		"sessionId": sessionID,
	}))
	if code == "" {
		t.Fatal("Router activation accepted an empty worker list")
	}
}

// The projection must never carry profile argv, environment names, or
// credentials.
func TestManageRouterStateOmitsSecrets(t *testing.T) {
	output := &syncedBuffer{}
	srv, sessionID := newRouterManageFixture(t, output)
	manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/router/activate", map[string]any{
		"sessionId":  sessionID,
		"profileIds": []string{"codex"},
	}))
	message := callManageFixture(t, srv, output, 2, "mothx/manage/router/state", map[string]any{
		"sessionId": sessionID,
	})
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "/opt/codex") || strings.Contains(text, "OPENAI_API_KEY") {
		t.Fatalf("the router projection leaked configuration details: %s", text)
	}
}

var _ = context.Background

// Leaving Router must remove the Router-only delegation surface, not just flip
// the mode. Otherwise the next prompt still exposes subagent_spawn and its
// manager no longer has a registered lead.
func TestManageRouterDeactivateRemovesRouterTools(t *testing.T) {
	output := &syncedBuffer{}
	srv, sessionID := newRouterManageFixture(t, output)
	rt := srv.sessionRuntime(sessionID)

	manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/router/activate", map[string]any{
		"sessionId":  sessionID,
		"profileIds": []string{"codex"},
	}))
	if _, ok := rt.registry.Get("delegate_worker_mgr"); !ok {
		t.Fatal("activation did not install the Router delegation surface")
	}
	if _, ok := rt.registry.Get("delegate_worker_mgr"); !ok {
		t.Fatal("activation did not install the Router delegate tool")
	}

	manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/router/deactivate", map[string]any{
		"sessionId": sessionID,
	}))
	if _, ok := rt.registry.Get("delegate_worker_mgr"); ok {
		t.Fatal("deactivation left subagent_spawn installed")
	}
	if _, ok := rt.registry.Get("delegate_worker_mgr"); ok {
		t.Fatal("deactivation left delegate_subagent installed")
	}
}

// The live profile catalog is published on the server's settings plane, but an
// already-built Router manager keeps its own ACP child-backend snapshot. The
// refresh path must push the new snapshot into that manager so the next worker
// delegation observes the edited model.
func TestRefreshRouterWorkerManagersUpdatesIdleRouterManager(t *testing.T) {
	output := &syncedBuffer{}
	srv, sessionID := newRouterManageFixture(t, output)

	manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/router/activate", map[string]any{
		"sessionId":  sessionID,
		"profileIds": []string{"codex"},
	}))
	rt := srv.sessionRuntime(sessionID)
	if rt == nil || rt.agentMgr == nil {
		t.Fatal("activated Router session has no manager")
	}

	// Publish the edit without going through the profile patch handler, so this
	// test exercises refreshRouterWorkerManagers directly.
	live := *srv.currentSettings()
	live.ACPAgents = append([]config.ACPAgentProfile(nil), live.ACPAgents...)
	live.ACPAgents[0].Model = "fast-model"
	srv.publishSettings(&live)
	srv.refreshRouterWorkerManagers()

	rt.agentMgr.Register(&acpRouterTestLead{id: "lead"})
	child, err := rt.agentMgr.Create(agent.AgentOptions{
		ParentID: "lead", WorkDir: rt.runtime.WorkDir, ExternalAgent: "codex",
		ExternalTask: "inspect the refreshed model", ExternalRole: "investigate",
		ExternalDeliverable: []string{"findings"},
	})
	if err != nil {
		t.Fatalf("create delegated worker: %v", err)
	}
	status, ok := rt.agentMgr.Status(child.ID())
	if !ok {
		t.Fatal("delegated worker has no manager status")
	}
	if status.Metadata["model"] != "fast-model" {
		t.Fatalf("delegated worker metadata = %#v, want the refreshed model", status.Metadata)
	}
	if err := rt.agentMgr.Destroy(child.ID()); err != nil {
		t.Fatalf("destroy delegated worker: %v", err)
	}
}

// The profile patch surface edits only behavior metadata. Argv, environment,
// and trust flags stay untouched, and an invalid patch never persists.
func TestManageACPProfilePatchUpdatesBehaviorMetadata(t *testing.T) {
	output := &syncedBuffer{}
	srv, _ := newRouterManageFixture(t, output)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/acp/profiles/patch", map[string]any{
		"id": "codex", "model": "fast-model", "driver": "codex-acp", "mcpBridge": "off",
		"capabilityOverrides": map[string]bool{"promptImage": false},
	}))
	profiles, _ := result["profiles"].([]any)
	if len(profiles) != 1 {
		t.Fatalf("profiles = %#v", result["profiles"])
	}
	profile, _ := profiles[0].(map[string]any)
	if profile["driver"] != "codex-acp" || profile["mcpBridge"] != "off" || profile["model"] != "fast-model" {
		t.Fatalf("patched projection = %#v", profile)
	}
	if _, leaked := profile["command"]; leaked {
		t.Fatalf("profile projection leaked argv: %#v", profile)
	}
	settings, err := config.LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.ACPAgents) != 1 {
		t.Fatalf("settings profiles = %#v", settings.ACPAgents)
	}
	stored := settings.ACPAgents[0]
	if stored.EffectiveDriver() != "codex-acp" || stored.EffectiveMCPBridge() != config.ACPMCPBridgeOff || stored.EffectiveModel() != "fast-model" {
		t.Fatalf("patch was not persisted: %#v", stored)
	}
	if len(stored.Command) != 2 || stored.Command[0] != "/opt/codex/bin/codex" {
		t.Fatalf("patch changed argv: %#v", stored.Command)
	}

	// The published configuration plane must carry the edit too, otherwise a
	// later session/new/load (and Desktop management read) keeps the old model.
	live := srv.currentSettings()
	if live == nil || len(live.ACPAgents) != 1 || live.ACPAgents[0].EffectiveModel() != "fast-model" {
		t.Fatalf("live settings profile = %#v, want the edited model", live)
	}
	if live.ACPAgents[0].EffectiveDriver() != "codex-acp" || live.ACPAgents[0].EffectiveMCPBridge() != config.ACPMCPBridgeOff {
		t.Fatalf("live settings profile lost the other behavior metadata: %#v", live.ACPAgents[0])
	}

	// Clearing the model must round-trip through the published plane and mean
	// "use the third-party Agent's own default".
	cleared := manageFixtureResult(t, callManageFixture(t, srv, output, 3, "mothx/manage/acp/profiles/patch", map[string]any{
		"id": "codex", "model": "",
	}))
	clearedProfiles, _ := cleared["profiles"].([]any)
	if len(clearedProfiles) != 1 {
		t.Fatalf("cleared profiles = %#v", cleared["profiles"])
	}
	clearedProfile, _ := clearedProfiles[0].(map[string]any)
	if model, present := clearedProfile["model"]; present && model != "" {
		t.Fatalf("cleared projection = %#v, want an empty model", clearedProfile)
	}
	if live := srv.currentSettings(); live == nil || len(live.ACPAgents) != 1 || live.ACPAgents[0].EffectiveModel() != "" {
		t.Fatalf("live settings after clearing = %#v, want an empty model", live)
	}

	raw := callManageFixture(t, srv, output, 2, "mothx/manage/acp/profiles/patch", map[string]any{
		"id": "codex", "capabilityOverrides": map[string]bool{"promptImage": true},
	})
	if raw["error"] == nil {
		t.Fatalf("widening override was accepted: %#v", raw)
	}
}

package acp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

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
	if _, ok := rt.registry.Get("subagent_spawn"); !ok {
		t.Fatal("activation did not install the Router delegation surface")
	}
	if _, ok := rt.registry.Get("delegate_subagent"); !ok {
		t.Fatal("activation did not install the Router delegate tool")
	}

	manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/router/deactivate", map[string]any{
		"sessionId": sessionID,
	}))
	if _, ok := rt.registry.Get("subagent_spawn"); ok {
		t.Fatal("deactivation left subagent_spawn installed")
	}
	if _, ok := rt.registry.Get("delegate_subagent"); ok {
		t.Fatal("deactivation left delegate_subagent installed")
	}
}

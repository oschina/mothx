package agentruntime

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// newRouterBuildRuntime builds a real shared Runtime over a persisted session so
// the construction boundary can be exercised the same way adapters do.
func newRouterBuildRuntime(t *testing.T) *SessionRuntime {
	t.Helper()
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = sessionDir
	settings.SkillsDir = t.TempDir()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	runtime, err := (Builder{Settings: settings}).Build(t.Context(), BuildOptions{
		ID: manager.GetHeader().ID, Source: SourceTUI, WorkDir: workDir, Manager: manager,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

func mockBuildModel() (provider.Provider, string, *provider.Model) {
	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model", ContextWindow: 128000}}, nil)
	return p, "mock", p.Models()[0]
}

// TestBuildAgentRejectsRouterWithoutBinding is the execution-layer fence: hiding
// Router from adapter switchers is not authorization, so construction must fail
// closed. A Router Agent with no worker binding exposes the read-only Router tool
// surface while delegate_subagent can only fail.
func TestBuildAgentRejectsRouterWithoutBinding(t *testing.T) {
	runtime := newRouterBuildRuntime(t)
	p, providerName, model := mockBuildModel()

	if _, ok := runtime.RouterBinding(); ok {
		t.Fatal("fixture unexpectedly has a Router binding")
	}
	agent, err := runtime.BuildAgent(AgentBuildOptions{
		Provider: p, ProviderName: providerName, Model: model, Mode: ModeRouter,
	})
	if err == nil {
		t.Fatalf("BuildAgent built a Router agent with no RouterBinding (id=%s)", agent.ID())
	}
	if !strings.Contains(err.Error(), "Router mode requires an activated ACP worker profile") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBuildAgentAllowsRouterWithBinding proves the fence rejects only the
// unbound case: an activated worker must still be able to build.
func TestBuildAgentAllowsRouterWithBinding(t *testing.T) {
	runtime := newRouterBuildRuntime(t)
	p, providerName, model := mockBuildModel()

	if err := runtime.SetRouterBinding(RouterBinding{
		ACPProfileID: "codex", ApprovalStrategy: config.ACPPermissionAutoAllowOnce,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.BuildAgent(AgentBuildOptions{
		Provider: p, ProviderName: providerName, Model: model, Mode: ModeRouter,
	}); err != nil {
		t.Fatalf("BuildAgent rejected an activated Router session: %v", err)
	}
}

// TestBuildAgentRejectsRouterWhenBindingIsInvalid proves a persisted but
// unusable binding (interactive approval) does not unlock the Router surface.
func TestBuildAgentRejectsRouterWhenBindingIsInvalid(t *testing.T) {
	runtime := newRouterBuildRuntime(t)
	p, providerName, model := mockBuildModel()

	// An interactive strategy is not a legal Router binding, so RouterBinding
	// reports absent and construction must fail closed.
	if _, err := runtime.Manager.AppendRouterBinding("codex", string(config.ACPPermissionInteractive), ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.RouterBinding(); ok {
		t.Fatal("an interactive Router binding was reported as usable")
	}
	if agent, err := runtime.BuildAgent(AgentBuildOptions{
		Provider: p, ProviderName: providerName, Model: model, Mode: ModeRouter,
	}); err == nil {
		t.Fatalf("BuildAgent accepted an invalid Router binding (id=%s)", agent.ID())
	}
}

// TestBuildAgentStillAllowsNonRouterModes guards against the fence over-reaching.
func TestBuildAgentStillAllowsNonRouterModes(t *testing.T) {
	runtime := newRouterBuildRuntime(t)
	p, providerName, model := mockBuildModel()

	for _, mode := range []string{ModePlan, ModeAgent, ModeYolo, ModeOS} {
		if _, err := runtime.BuildAgent(AgentBuildOptions{
			Provider: p, ProviderName: providerName, Model: model, Mode: mode,
		}); err != nil {
			t.Fatalf("BuildAgent rejected mode %q: %v", mode, err)
		}
	}
}

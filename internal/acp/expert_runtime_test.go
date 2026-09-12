package acp

import (
	"testing"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/tools"
)

func TestRegisterTeamExpertToolsUsesSessionManager(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = t.TempDir()
	mgr, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: workDir, SessionDir: settings.SessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := mgr.SetExpertBinding("software-company"); err != nil {
		t.Fatalf("bind team expert: %v", err)
	}

	registry := tools.NewRegistry(workDir, sandbox.NewNoneSandbox())
	registry.RegisterDefaults()
	runtime, err := agentruntime.AttachSessionResources(agentruntime.AttachedResources{
		Source: agentruntime.SourceACP, WorkDir: workDir, Manager: mgr, Registry: registry, Settings: settings,
	})
	if err != nil {
		t.Fatalf("attach runtime: %v", err)
	}
	defer runtime.Close()
	model := &provider.Model{ID: "m1", Name: "M1"}
	p := provider.NewMockProvider("mock", []*provider.Model{model}, nil)
	if err := runtime.ConfigureSession(p, "mock", model, agentruntime.ModeYolo, ""); err != nil {
		t.Fatalf("configure session: %v", err)
	}

	srv := &server{settings: settings, allow: &config.AllowConfig{}}
	agentMgr, err := srv.registerTeamExpertTools(runtime, registry)
	if err != nil {
		t.Fatalf("register team tools: %v", err)
	}
	if agentMgr == nil || agentMgr.Members == nil {
		t.Fatal("team expert must receive a session-scoped manager")
	}
	if _, ok := agentMgr.Members.Get("software-engineer"); !ok {
		t.Fatalf("team manager members = %v, missing software-engineer", agentMgr.Members.IDs())
	}
	if _, ok := registry.Get("subagent_spawn"); !ok {
		t.Fatal("team registry missing subagent_spawn")
	}
}

func TestRefreshSessionExpertToolsTracksRuntimeBinding(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = t.TempDir()
	mgr, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: workDir, SessionDir: settings.SessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	registry := tools.NewRegistry(workDir, sandbox.NewNoneSandbox())
	registry.RegisterDefaults()
	runtime, err := agentruntime.AttachSessionResources(agentruntime.AttachedResources{
		Source: agentruntime.SourceACP, WorkDir: workDir, Manager: mgr, Registry: registry, Settings: settings,
	})
	if err != nil {
		t.Fatalf("attach runtime: %v", err)
	}
	defer runtime.Close()
	model := &provider.Model{ID: "m1", Name: "M1"}
	p := provider.NewMockProvider("mock", []*provider.Model{model}, nil)
	if err := runtime.ConfigureSession(p, "mock", model, agentruntime.ModeYolo, ""); err != nil {
		t.Fatalf("configure session: %v", err)
	}
	srv := &server{settings: settings, allow: &config.AllowConfig{}}
	rt := &sessionRuntime{runtime: runtime, registry: registry}

	if err := runtime.SetConfigOption(agentruntime.ConfigOptionExpert, "software-company"); err != nil {
		t.Fatalf("bind team expert: %v", err)
	}
	if err := srv.refreshSessionExpertTools(rt); err != nil {
		t.Fatalf("install team expert tools: %v", err)
	}
	if rt.agentMgr == nil {
		t.Fatal("team binding did not install a session manager")
	}
	if _, ok := registry.Get("subagent_spawn"); !ok {
		t.Fatal("team binding did not install subagent_spawn")
	}

	if err := runtime.SetConfigOption(agentruntime.ConfigOptionExpert, ""); err != nil {
		t.Fatalf("unbind team expert: %v", err)
	}
	if err := srv.refreshSessionExpertTools(rt); err != nil {
		t.Fatalf("remove team expert tools: %v", err)
	}
	if rt.agentMgr != nil {
		t.Fatal("unbound session kept a team agent manager")
	}
	// Every canonical sub-agent tool must be gone, not just the spawn tool.
	for _, name := range agent.SubAgentToolNames() {
		if _, ok := registry.Get(name); ok {
			t.Fatalf("unbound session kept team %s", name)
		}
	}
}

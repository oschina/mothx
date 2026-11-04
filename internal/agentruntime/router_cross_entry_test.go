package agentruntime

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

// The Router MVP deliberately exposes activation controls only in the TUI and
// CLI. Every other entry point must still honor a Router session that was
// persisted elsewhere: the resolved mode, the read-only tool fence, and the
// bound worker allowlist are Runtime facts, not adapter projections.
//
// These tests attach one persisted Router session through each entry point and
// prove that only the protocol/wire details differ.

// routerCrossEntryFixture is one persisted Router session plus the settings
// every entry point would read.
type routerCrossEntryFixture struct {
	sessionDir string
	workDir    string
	settings   *config.Settings
	sessionID  string
}

func newRouterCrossEntryFixture(t *testing.T) *routerCrossEntryFixture {
	t.Helper()
	workDir, sessionDir := t.TempDir(), t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = sessionDir
	settings.SkillsDir = t.TempDir()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID:               "codex",
		Command:          []string{"/opt/codex/bin/codex", "acp"},
		Enabled:          true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
	}}

	manager, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	runtime, err := (Builder{Settings: settings}).Build(context.Background(), BuildOptions{
		ID: manager.GetHeader().ID, Source: SourceTUI, WorkDir: workDir, Manager: manager,
	})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	t.Cleanup(runtime.Close)

	if err := runtime.ActivateRouter(context.Background(), sessionDir, settings, RouterBinding{
		ACPProfileID:     "codex",
		ApprovalStrategy: config.ACPPermissionAutoAllowOnce,
	}); err != nil {
		t.Fatalf("activate router: %v", err)
	}
	return &routerCrossEntryFixture{
		sessionDir: sessionDir,
		workDir:    workDir,
		settings:   settings,
		sessionID:  manager.GetHeader().ID,
	}
}

// attachAs reopens the persisted session as another entry point would.
func (f *routerCrossEntryFixture) attachAs(t *testing.T, source RuntimeSource) *SessionRuntime {
	t.Helper()
	manager, err := session.OpenByIDExact(f.sessionDir, f.sessionID)
	if err != nil {
		t.Fatalf("open session for %s: %v", source, err)
	}
	runtime, err := (Builder{Settings: f.settings}).Build(context.Background(), BuildOptions{
		ID: f.sessionID, Source: source, WorkDir: f.workDir, Manager: manager,
	})
	if err != nil {
		t.Fatalf("build %s runtime: %v", source, err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

// routerToolSurface returns the tool names an entry point would expose to the
// model in Router mode.
//
// The Registry holds every registered tool regardless of mode; the Router
// fence lives in ModeTools, which selects the frozen surface an Agent
// advertises. The shared installer plus the mode-aware ModeTools is what
// makes the surface identical across entry points.
func routerToolSurface(t *testing.T, runtime *SessionRuntime) []string {
	t.Helper()
	registry := runtime.Registry
	if registry == nil {
		t.Fatal("runtime has no registry")
	}
	manager := agent.NewAgentManager(nil)
	SynchronizeToolGroups(runtime, registry, ToolGroupPolicy{Delegate: true, Mode: ModeRouter}, manager)

	names := make([]string, 0)
	for _, definition := range registry.ModeTools(ModeRouter) {
		names = append(names, definition.Name)
	}
	sort.Strings(names)
	return names
}

// TestRouterCrossEntryHonorsPersistedMode proves a Router session persisted by
// the TUI is still a Router session when another entry point opens it. The
// activation control is a TUI/CLI affordance; the mode itself is a Runtime
// fact that every adapter must honor.
func TestRouterCrossEntryHonorsPersistedMode(t *testing.T) {
	fixture := newRouterCrossEntryFixture(t)

	for _, source := range []RuntimeSource{SourceTUI, SourceCLI, SourceACP, SourceWebUI} {
		runtime := fixture.attachAs(t, source)
		_, _, _, mode, _ := runtime.ConfigSnapshot()
		if mode != ModeRouter {
			t.Fatalf("%s resolved mode = %q, want %q", source, mode, ModeRouter)
		}
		binding, ok := runtime.RouterBinding()
		if !ok {
			t.Fatalf("%s lost the Router binding", source)
		}
		if binding.ACPProfileID != "codex" {
			t.Fatalf("%s bound worker = %q, want codex", source, binding.ACPProfileID)
		}
		if ids := binding.ProfileIDs(); len(ids) != 1 || ids[0] != "codex" {
			t.Fatalf("%s bound allowlist = %#v, want [codex]", source, ids)
		}
	}
}

// TestRouterCrossEntrySharesOneToolSurface proves every entry point projects
// the same Router tool surface. The surface is resolved by the shared Runtime
// installer, so an adapter cannot accidentally widen or narrow it.
func TestRouterCrossEntrySharesOneToolSurface(t *testing.T) {
	fixture := newRouterCrossEntryFixture(t)

	var reference []string
	for _, source := range []RuntimeSource{SourceTUI, SourceCLI, SourceACP, SourceWebUI} {
		runtime := fixture.attachAs(t, source)
		surface := routerToolSurface(t, runtime)
		if reference == nil {
			reference = surface
			// The Router surface is the read-only lead surface plus the
			// delegation tools bound to the activated worker. It must never
			// contain a local mutation or process tool.
			for _, name := range surface {
				switch name {
				case "write", "edit", "insert", "bash", "jobs", "kill":
					t.Fatalf("Router surface exposes %q: %#v", name, surface)
				}
			}
			for _, want := range []string{"read", "grep", "ls", "delegate_subagent", "subagent_spawn"} {
				if !containsRouterTool(surface, want) {
					t.Fatalf("Router surface is missing %q: %#v", want, surface)
				}
			}
			continue
		}
		if strings.Join(surface, ",") != strings.Join(reference, ",") {
			t.Fatalf("%s tool surface = %#v, want %#v", source, surface, reference)
		}
	}
}

// TestRouterCrossEntryBuildsIdenticalAgentSurface proves the frozen Agent
// configuration is identical across entry points: the same Router prompt and
// the same advertised tool definitions, so only the wire format differs.
func TestRouterCrossEntryBuildsIdenticalAgentSurface(t *testing.T) {
	fixture := newRouterCrossEntryFixture(t)

	var referencePrompt string
	var referenceTools []string
	for _, source := range []RuntimeSource{SourceTUI, SourceCLI, SourceACP, SourceWebUI} {
		runtime := fixture.attachAs(t, source)
		p, providerName, model := mockBuildModel()
		agentInstance, err := runtime.BuildAgent(AgentBuildOptions{
			Provider: p, ProviderName: providerName, Model: model, Mode: ModeRouter,
		})
		if err != nil {
			t.Fatalf("%s build router agent: %v", source, err)
		}
		prompt, tools := agentInstance.FrozenSurface()
		if referencePrompt == "" {
			referencePrompt = prompt
			referenceTools = tools
			if !strings.Contains(prompt, "## Mode: ROUTER") {
				t.Fatalf("Router prompt does not render the Router section: %q", prompt)
			}
			if strings.Contains(prompt, "## Mode: YOLO") || strings.Contains(prompt, "## Mode: AGENT") {
				t.Fatalf("Router prompt contains an ordinary mode section: %q", prompt)
			}
			for _, name := range tools {
				switch name {
				case "write", "edit", "insert", "bash", "jobs", "kill":
					t.Fatalf("Router agent advertises %q: %#v", name, tools)
				}
			}
			continue
		}
		if prompt != referencePrompt {
			t.Fatalf("%s rendered a different Router prompt", source)
		}
		if strings.Join(tools, ",") != strings.Join(referenceTools, ",") {
			t.Fatalf("%s advertised tools = %#v, want %#v", source, tools, referenceTools)
		}
	}
}

// TestRouterCrossEntryRejectsUnboundActivation proves the Router fence is a
// Runtime fact too: an entry point that has no activation control still
// cannot enter Router without an eligible binding, and the persisted binding
// is the only way in.
func TestRouterCrossEntryRejectsUnboundActivation(t *testing.T) {
	fixture := newRouterCrossEntryFixture(t)

	// An entry point that resolves Router without a binding must fail closed.
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	runtime, err := (Builder{Settings: fixture.settings}).Build(context.Background(), BuildOptions{
		ID: manager.GetHeader().ID, Source: SourceACP, WorkDir: workDir, Manager: manager,
	})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	t.Cleanup(runtime.Close)

	// Both grant points must refuse Router without a bound worker: the mode
	// write path and the Agent construction boundary. Resolving a mode name is
	// not a grant, so only these two are fenced.
	if _, err := runtime.BuildAgent(AgentBuildOptions{Mode: ModeRouter}); err == nil {
		t.Fatal("an unbound entry point built a Router agent")
	}
	// Mode resolution alone may name Router; the fence lives at the two places
	// that actually grant it: the mode write path and Agent construction.
	if err := runtime.SetMode(ModeRouter); err == nil {
		t.Fatal("an unbound entry point persisted Router mode")
	}
	// The failed write must not have left Router persisted behind.
	if _, _, _, mode, _ := runtime.ConfigSnapshot(); mode == ModeRouter {
		t.Fatal("a refused Router activation persisted the mode anyway")
	}
}

func containsRouterTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

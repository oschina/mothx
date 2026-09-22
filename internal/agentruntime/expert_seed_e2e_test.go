package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
)

// TestSeededTeamExpertEndToEnd exercises the full Phase 1 chain against the
// real embedded seed bundle: persisted header binding -> runtime assembly
// (identity/roster/member defs from the shipped software-company pack) ->
// adapter-style tool registration -> BuildAgent -> subagent_spawn(member=...)
// execution -> terminal completion landing in the session mailbox as a
// system-injected [MEMBER_COMPLETION] steering message. It is the behavioral
// acceptance for "专家团 = 内容包 × 命名 agent 定义原语 × 邮箱触达".
func TestSeededTeamExpertEndToEnd(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false

	mgr, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "m1", Name: "M1"}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "ENGINEER-E2E-RESULT"},
		{Type: provider.StreamDone, StopReason: "stop"},
	})
	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{WorkDir: workDir, Manager: mgr})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer runtime.Close()
	runtime.Provider = mockProvider
	runtime.ProviderName = "mock"
	runtime.Model = mockProvider.Models()[0]

	// Bind the real builtin seed through the session header.
	if err := runtime.SetExpert("software-company"); err != nil {
		t.Fatalf("SetExpert: %v", err)
	}
	if got := mgr.GetExpertID(); got != "software-company" {
		t.Fatalf("persisted header binding = %q", got)
	}
	if !SubAgentToolsEnabled(runtime, false) {
		t.Fatal("team seed must force sub-agent capability")
	}
	binding, mailbox := runtime.ExpertState()
	if binding == nil || !binding.Team || len(binding.MemberDefs) != 4 {
		t.Fatalf("binding = %+v, want team with 4 members", binding)
	}
	if !strings.Contains(binding.IdentityPrompt, "一人公司") || !strings.Contains(binding.IdentityPrompt, "禁止代写") {
		t.Fatal("identity prompt must carry the shipped lead SOP")
	}
	for _, want := range []string{"司南", "墨矩", "柯德", "甄严", "subagent_wait", "hub-and-spoke"} {
		if !strings.Contains(binding.RosterPrompt, want) {
			t.Fatalf("roster prompt missing %q:\n%s", want, binding.RosterPrompt)
		}
	}
	var engineer *agent.MemberDef
	for _, def := range binding.MemberDefs {
		if def.ID == "software-engineer" {
			engineer = def
		}
	}
	if engineer == nil || engineer.DisplayName != "柯德" || engineer.Mode != "yolo" || engineer.MaxIterations != 80 || len(engineer.Tools) != 6 {
		t.Fatalf("engineer def from seed = %+v", engineer)
	}

	// Adapter-style wiring (mirrors cmd/mothx main.go): shared manager,
	// gated tool registration, main agent build, spawn execution.
	agentMgr, err := NewAgentManager(AgentManagerOptions{Runtime: runtime, Provider: mockProvider, Model: runtime.Model, Settings: settings})
	if err != nil {
		t.Fatalf("new agent manager: %v", err)
	}
	agent.RegisterSubAgentTools(runtime.Registry, agentMgr)
	spawnTool, ok := runtime.Registry.Get("subagent_spawn")
	if !ok {
		t.Fatal("subagent_spawn not registered despite team expert")
	}
	if _, ok := runtime.Registry.Get("subagent_wait"); !ok {
		t.Fatal("subagent_wait missing from team tool surface")
	}
	main, err := runtime.BuildAgent(AgentBuildOptions{ID: "main", Mode: "yolo", Settings: settings})
	if err != nil {
		t.Fatalf("build main agent: %v", err)
	}
	// Mirrors adapter behavior: the session's parent agent is registered in
	// the shared manager so spawned children resolve their parent.
	agentMgr.Register(agent.NewAgentAdapter(main))
	ctx := agent.ContextWithAgentID(context.Background(), main.ID())
	ctx = agent.ContextWithParentMode(ctx, "yolo")

	result, err := spawnTool.Execute(ctx, map[string]any{
		"member": "software-engineer",
		"task":   "write hello world",
	})
	if err != nil {
		t.Fatalf("spawn execute: %v", err)
	}
	if !strings.Contains(result.Text, `"status":"running"`) && !strings.Contains(result.Text, "handle") {
		t.Fatalf("spawn result = %q", result.Text)
	}

	// The completion must surface in the runtime mailbox carrying the seed
	// display name and the mock final response (async child: poll budget).
	deadline := time.Now().Add(10 * time.Second)
	for !mailbox.HasPending() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	messages := mailbox.DrainSteering()
	if len(messages) != 1 {
		t.Fatalf("mailbox messages = %d, want 1", len(messages))
	}
	msg := messages[0]
	if !msg.SystemInjected || msg.Role != "user" {
		t.Fatalf("completion message = role %q systemInjected %v", msg.Role, msg.SystemInjected)
	}
	for _, want := range []string{"[MEMBER_COMPLETION]", "software-engineer", "柯德", "status: done", "ENGINEER-E2E-RESULT"} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("completion content missing %q:\n%s", want, msg.Content)
		}
	}

	// Replacing a bound expert must fork instead of mutating this branch.
	if err := runtime.SetExpert("frontend-developer"); !errors.Is(err, ErrExpertSwitchRequiresFork) {
		t.Fatalf("in-place switch error = %v, want ErrExpertSwitchRequiresFork", err)
	}
	if got := mgr.GetExpertID(); got != "software-company" {
		t.Fatalf("in-place switch changed source binding to %q", got)
	}

	// Unbind clears everything at the single resolver.
	if err := runtime.SetExpert(""); err != nil {
		t.Fatalf("unbind expert: %v", err)
	}
	binding, _ = runtime.ExpertState()
	if binding != nil {
		t.Fatal("binding must be nil after unbind")
	}
	if SubAgentToolsEnabled(runtime, false) {
		t.Fatal("capability force must follow the unbind")
	}
	if got := mgr.GetExpertID(); got != "" {
		t.Fatalf("persisted binding after unbind = %q", got)
	}
}

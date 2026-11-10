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

// TestSeededRoleBasedTeamEndToEnd is the same behavioral chain as
// TestSeededTeamExpertEndToEnd, but against one of the role-based bundles added
// with the ECC absorption pass. It proves the new catalog is not only a valid
// manifest: the lead gets the roster, a member can be spawned by ID, and the
// completion lands in the session mailbox with the duty-based display name.
func TestSeededRoleBasedTeamEndToEnd(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false

	mgr, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "m1", Name: "M1"}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "BACKEND-E2E-RESULT"},
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

	if err := runtime.SetExpert("backend-platform"); err != nil {
		t.Fatalf("SetExpert: %v", err)
	}
	if got := mgr.GetExpertID(); got != "backend-platform" {
		t.Fatalf("persisted header binding = %q", got)
	}
	if !SubAgentToolsEnabled(runtime, false) {
		t.Fatal("role-based team seed must force sub-agent capability")
	}
	binding, mailbox := runtime.ExpertState()
	if binding == nil || !binding.Team || len(binding.MemberDefs) != 4 {
		t.Fatalf("binding = %+v, want team with 4 members", binding)
	}
	if !strings.Contains(binding.IdentityPrompt, "后端平台团队") || !strings.Contains(binding.IdentityPrompt, "调度纪律") {
		t.Fatalf("identity prompt missing the backend lead persona:\n%s", binding.IdentityPrompt)
	}
	for _, want := range []string{
		"API 与契约负责人", "数据库与迁移负责人", "服务运行时负责人", "后端验证负责人",
		"api-contract-engineer", "database-engineer", "service-runtime-engineer", "backend-test-engineer",
		"subagent_spawn",
	} {
		if !strings.Contains(binding.RosterPrompt, want) {
			t.Fatalf("roster prompt missing %q:\n%s", want, binding.RosterPrompt)
		}
	}

	agentMgr, err := NewAgentManager(AgentManagerOptions{Runtime: runtime, Provider: mockProvider, Model: runtime.Model, Settings: settings})
	if err != nil {
		t.Fatalf("new agent manager: %v", err)
	}
	agent.RegisterSubAgentTools(runtime.Registry, agentMgr)
	spawnTool, ok := runtime.Registry.Get("subagent_spawn")
	if !ok {
		t.Fatal("subagent_spawn not registered despite team expert")
	}
	main, err := runtime.BuildAgent(AgentBuildOptions{ID: "main", Mode: "yolo", Settings: settings})
	if err != nil {
		t.Fatalf("build main agent: %v", err)
	}
	agentMgr.Register(agent.NewAgentAdapter(main))
	ctx := agent.ContextWithAgentID(context.Background(), main.ID())
	ctx = agent.ContextWithParentMode(ctx, "yolo")

	result, err := spawnTool.Execute(ctx, map[string]any{
		"member": "backend-test-engineer",
		"task":   "verify the focused backend behavior",
	})
	if err != nil {
		t.Fatalf("spawn execute: %v", err)
	}
	if !strings.Contains(result.Text, "handle") {
		t.Fatalf("spawn result = %q", result.Text)
	}

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
	for _, want := range []string{"[MEMBER_COMPLETION]", "backend-test-engineer", "后端验证负责人", "status: done", "BACKEND-E2E-RESULT"} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("completion content missing %q:\n%s", want, msg.Content)
		}
	}

	if err := runtime.SetExpert(""); err != nil {
		t.Fatalf("unbind expert: %v", err)
	}
	if binding, _ := runtime.ExpertState(); binding != nil {
		t.Fatal("binding must be nil after unbind")
	}
	if got := mgr.GetExpertID(); got != "" {
		t.Fatalf("persisted binding after unbind = %q", got)
	}
}

// TestSeededRoleBasedTeamCatalogRosters binds every role-based bundle and
// asserts the Runtime injects the shipped roster and member definitions. It is
// deliberately lighter than the full spawn test: it catches catalog-level
// regressions (missing member, wrong display name, lost roster entry) for all
// twelve teams without starting twelve child runs.
func TestSeededRoleBasedTeamCatalogRosters(t *testing.T) {
	catalog := map[string][]string{
		"backend-platform":      {"api-contract-engineer", "database-engineer", "service-runtime-engineer", "backend-test-engineer"},
		"web-frontend":          {"react-next-engineer", "vue-angular-engineer", "design-system-engineer", "frontend-performance-engineer"},
		"mobile-crossplatform":  {"android-kmp-engineer", "flutter-engineer", "react-native-engineer", "ios-swift-engineer"},
		"systems-lowlevel":      {"rust-engineer", "cpp-engineer", "go-systems-engineer"},
		"enterprise-jvm-dotnet": {"java-spring-engineer", "kotlin-engineer", "dotnet-engineer", "php-ruby-engineer"},
		"qa-testing":            {"tdd-engineer", "e2e-engineer", "regression-analyst", "silent-failure-auditor"},
		"security-compliance":   {"appsec-engineer", "agent-security-engineer", "supply-chain-auditor", "compliance-reviewer"},
		"devops-sre":            {"kubernetes-engineer", "cicd-release-engineer", "observability-engineer", "config-environment-engineer"},
		"data-ml-ai":            {"mlops-engineer", "pytorch-engineer", "retrieval-recsys-engineer", "llm-eval-engineer"},
		"product-architecture":  {"product-planner", "architecture-designer", "code-explorer", "spec-miner"},
		"docs-research":         {"docs-governance-editor", "codebase-onboarding-writer", "deep-research-analyst"},
		"agent-platform":        {"agent-architect", "harness-engineer", "loop-operator", "eval-engineer"},
	}

	skills := map[string]string{
		"backend-platform":      "backend-change-review",
		"web-frontend":          "frontend-quality-gate",
		"mobile-crossplatform":  "mobile-release-readiness",
		"systems-lowlevel":      "systems-safety-review",
		"enterprise-jvm-dotnet": "enterprise-transaction-review",
		"qa-testing":            "flaky-test-triage",
		"security-compliance":   "agent-tool-threat-model",
		"devops-sre":            "release-rollback-checklist",
		"data-ml-ai":            "llm-eval-gate",
		"product-architecture":  "brownfield-spec-mining",
		"docs-research":         "research-citation-check",
		"agent-platform":        "agent-action-space-review",
	}

	for team, members := range catalog {
		t.Run(team, func(t *testing.T) {
			workDir := t.TempDir()
			settings := config.DefaultSettings()
			settings.ContextFiles.Enabled = false
			mgr, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: t.TempDir()})
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{WorkDir: workDir, Manager: mgr})
			if err != nil {
				t.Fatalf("build runtime: %v", err)
			}
			defer runtime.Close()

			if err := runtime.SetExpert(team); err != nil {
				t.Fatalf("SetExpert: %v", err)
			}
			binding, _ := runtime.ExpertState()
			if binding == nil || !binding.Team {
				t.Fatalf("binding = %+v, want a team", binding)
			}
			if skill := runtime.SkillsMgr.Get(skills[team]); skill == nil || skill.Source != "expert" {
				t.Fatalf("bundle skill %q = %+v, want an expert skill", skills[team], skill)
			}
			if len(binding.MemberDefs) != len(members) {
				t.Fatalf("member defs = %d, want %d", len(binding.MemberDefs), len(members))
			}
			for _, id := range members {
				var def *agent.MemberDef
				for _, candidate := range binding.MemberDefs {
					if candidate.ID == id {
						def = candidate
						break
					}
				}
				if def == nil {
					t.Fatalf("missing member def %q", id)
				}
				if def.Role != "member" || !strings.HasSuffix(def.DisplayName, "负责人") {
					t.Fatalf("member %q = role %q display %q", id, def.Role, def.DisplayName)
				}
				if !strings.Contains(binding.RosterPrompt, id) || !strings.Contains(binding.RosterPrompt, def.DisplayName) {
					t.Fatalf("roster missing member %q (%q):\n%s", id, def.DisplayName, binding.RosterPrompt)
				}
			}
		})
	}
}

package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
)

// writeExpertFixtures creates a team bundle ("studio": lead + 2 members) and
// a single-persona agent bundle ("solo") under the project layer
// (<workDir>/.mothx/experts).
func writeExpertFixtures(t *testing.T, workDir string) {
	t.Helper()
	writeBundle := func(name, expertType, persona string, members ...string) {
		dir := filepath.Join(workDir, ".mothx", "experts", name)
		if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o700); err != nil {
			t.Fatalf("mkdir bundle: %v", err)
		}
		var team string
		if len(members) > 0 {
			var quoted []string
			for _, m := range members {
				quoted = append(quoted, `"`+m+`"`)
			}
			team = `,"teamInfo":{"leadAgent":"` + persona + `","memberAgents":[` + strings.Join(quoted, ",") + `]},"members":[{"id":"` + persona + `","name":{"zh":"总监","en":"Boss"},"role":"lead"}`
			for _, m := range members {
				team += `,{"id":"` + m + `","name":{"zh":"成员-` + m + `","en":"Member"},"role":"member"}`
			}
			team += `]`
		}
		agentName := ""
		if expertType == "agent" {
			agentName = `,"agentName":"` + persona + `"`
		}
		manifest := `{"schemaVersion":1,"name":"` + name + `","expertType":"` + expertType + `"` + agentName + `,"displayName":{"zh":"` + name + `","en":"` + name + `"}` + team + `}`
		if err := os.WriteFile(filepath.Join(dir, "expert.json"), []byte(manifest), 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		writePersona := func(id, role, body string, extra string) {
			content := "---\nname: " + id + "\nrole: " + role + "\ndescription: persona " + id + "\n" + extra + "---\n" + body + "\n"
			if err := os.WriteFile(filepath.Join(dir, "agents", id+".md"), []byte(content), 0o600); err != nil {
				t.Fatalf("write persona: %v", err)
			}
		}
		writePersona(persona, "lead", "LEAD-BODY-"+name, "")
		for _, m := range members {
			writePersona(m, "member", "MEMBER-BODY-"+m, "mode: plan\ntools: [read, grep]\nmax_iterations: 42\n")
		}
	}
	writeBundle("studio", "team", "studio-lead", "studio-a", "studio-b")
	writeBundle("solo", "agent", "solo-lead")
}

func testBuilder() Builder {
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	return Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}
}

func TestBuilderWiresExpertBindingFromSessionHeader(t *testing.T) {
	workDir := t.TempDir()
	writeExpertFixtures(t, workDir)
	sessionDir := t.TempDir()
	mgr, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := mgr.SetExpertBinding("studio"); err != nil {
		t.Fatalf("bind expert: %v", err)
	}

	runtime, err := testBuilder().Build(context.Background(), BuildOptions{WorkDir: workDir, Manager: mgr})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer runtime.Close()

	binding := runtime.Expert
	if binding == nil {
		t.Fatal("expert binding not resolved")
	}
	if binding.ID != "studio" || binding.Type != "team" || !binding.Team {
		t.Fatalf("binding = %+v, want studio/team/team=true", binding)
	}
	if !strings.Contains(binding.IdentityPrompt, "## Expert Identity") || !strings.Contains(binding.IdentityPrompt, "LEAD-BODY-studio") {
		t.Fatalf("identity prompt missing lead body: %q", binding.IdentityPrompt)
	}
	if !strings.Contains(binding.IdentityPrompt, "changes only when the session expert is bound") {
		t.Fatal("identity prompt missing authority statement")
	}
	if !strings.Contains(binding.RosterPrompt, "- studio-a") || !strings.Contains(binding.RosterPrompt, "subagent_spawn(member:") {
		t.Fatalf("roster prompt malformed: %q", binding.RosterPrompt)
	}
	if len(binding.MemberDefs) != 2 {
		t.Fatalf("member defs = %d, want 2", len(binding.MemberDefs))
	}
	def := binding.MemberDefs[0]
	if def.Mode != "plan" || def.MaxIterations != 42 || len(def.Tools) != 2 || !strings.HasPrefix(def.Prompt, "MEMBER-BODY-") {
		t.Fatalf("member def capability mapping wrong: %+v", def)
	}
}

func TestBuilderLoadsBoundBuiltinExpertSkills(t *testing.T) {
	workDir := t.TempDir()
	mgr, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := mgr.SetExpertBinding("frontend-developer"); err != nil {
		t.Fatalf("bind builtin expert: %v", err)
	}

	runtime, err := testBuilder().Build(context.Background(), BuildOptions{WorkDir: workDir, Manager: mgr})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer runtime.Close()

	skill := runtime.SkillsMgr.Get("frontend-review")
	if skill == nil {
		t.Fatal("builtin expert skill frontend-review was not loaded")
	}
	if skill.Source != "expert" {
		t.Fatalf("expert skill source = %q, want expert", skill.Source)
	}
	if _, ok := runtime.Registry.Get("skill_ref"); !ok {
		t.Fatal("skill_ref missing after expert skill resource assembly")
	}
}

func TestSetExpertPreflightFailureDoesNotPersistBrokenBinding(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	mgr, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	runtime, err := testBuilder().Build(context.Background(), BuildOptions{WorkDir: workDir, Manager: mgr})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer runtime.Close()

	if err := runtime.SetExpert("does-not-exist"); err == nil || !strings.Contains(err.Error(), "resolve expert binding") {
		t.Fatalf("SetExpert missing bundle error = %v", err)
	}
	if got := mgr.GetExpertID(); got != "" {
		t.Fatalf("failed bind persisted expert id %q", got)
	}
	if binding, _ := runtime.ExpertState(); binding != nil {
		t.Fatalf("failed bind changed runtime binding: %+v", binding)
	}
	reopened, err := session.OpenByIDExact(sessionDir, mgr.GetHeader().ID)
	if err != nil {
		t.Fatalf("reopen after failed bind: %v", err)
	}
	if got := reopened.GetExpertID(); got != "" {
		t.Fatalf("reopened failed bind expert id %q", got)
	}
}

func TestSetExpertRehydratesPackageSkills(t *testing.T) {
	workDir := t.TempDir()
	mgr, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	runtime, err := testBuilder().Build(context.Background(), BuildOptions{WorkDir: workDir, Manager: mgr})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer runtime.Close()

	if err := runtime.SetExpert("frontend-developer"); err != nil {
		t.Fatalf("set expert: %v", err)
	}
	if skill := runtime.SkillsMgr.Get("frontend-review"); skill == nil || skill.Source != "expert" {
		t.Fatalf("bound expert skill = %+v, want expert package skill", skill)
	}
	if err := runtime.SetExpert(""); err != nil {
		t.Fatalf("unbind expert: %v", err)
	}
	if skill := runtime.SkillsMgr.Get("frontend-review"); skill != nil {
		t.Fatalf("expert package skill remained after unbind: %+v", skill)
	}
}

func TestForkWithExpertPreservesSourceAndBindsChild(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("expert-fork-source"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetExpertBinding("studio"); err != nil {
		t.Fatal(err)
	}
	if err := session.StartConversationTurn(sessionDir, session.ConversationTurn{ID: "expert-fork-turn", SessionID: "expert-fork-source", IntentID: "intent", RunID: "run"}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AppendMessage(provider.NewUserMessage("fork me")); err != nil {
		t.Fatal(err)
	}
	if err := session.EndConversationTurn(sessionDir, "expert-fork-source", "expert-fork-turn", "completed", "stop", time.Now()); err != nil {
		t.Fatal(err)
	}

	result, err := ForkWithExpert(context.Background(), sessionDir, ForkOptions{SourceSessionID: "expert-fork-source", RequestID: "switch-to-solo"}, "solo")
	if err != nil {
		t.Fatalf("fork with expert: %v", err)
	}
	child, err := session.OpenByIDExact(sessionDir, result.SessionID)
	if err != nil {
		t.Fatalf("open child: %v", err)
	}
	if got := child.GetExpertID(); got != "solo" {
		t.Fatalf("child expert = %q, want solo", got)
	}
	if got := mgr.GetExpertID(); got != "studio" {
		t.Fatalf("source expert = %q, want studio", got)
	}
}

func TestBuilderMissingOrInvalidExpertIsHardError(t *testing.T) {
	workDir := t.TempDir()
	writeExpertFixtures(t, workDir)
	sessionDir := t.TempDir()
	mgr, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := mgr.SetExpertBinding("ghost"); err != nil {
		t.Fatalf("bind ghost: %v", err)
	}
	if _, err := testBuilder().Build(context.Background(), BuildOptions{WorkDir: workDir, Manager: mgr}); err == nil || !strings.Contains(err.Error(), "resolve expert binding") {
		t.Fatalf("build with missing expert err = %v, want resolve expert binding", err)
	}
	if err := mgr.SetExpertBinding("studio"); err != nil {
		t.Fatalf("rebind studio: %v", err)
	}
	runtime, err := testBuilder().Build(context.Background(), BuildOptions{WorkDir: workDir, Manager: mgr})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer runtime.Close()
	if err := runtime.SetExpert(""); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if runtime.Expert != nil {
		t.Fatal("expert still bound after unbind")
	}
	if got := mgr.GetExpertID(); got != "" {
		t.Fatalf("persisted binding = %q, want empty", got)
	}
	if err := runtime.SetExpert("solo"); err != nil {
		t.Fatalf("bind solo: %v", err)
	}
	runtime.mu.RLock()
	binding := runtime.Expert
	runtime.mu.RUnlock()
	if binding == nil || binding.Team {
		t.Fatalf("solo binding = %+v, want agent type, Team=false", binding)
	}
	if binding.RosterPrompt != "" || len(binding.MemberDefs) != 0 {
		t.Fatal("agent-type binding must not carry roster or members")
	}
}

func TestSessionHasTeamExpertTruthTable(t *testing.T) {
	workDir := t.TempDir()
	writeExpertFixtures(t, workDir)
	sessionDir := t.TempDir()
	mgr, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if SessionHasTeamExpert(workDir, nil) {
		t.Fatal("nil manager must not report team expert")
	}
	if SessionHasTeamExpert(workDir, mgr) {
		t.Fatal("unbound session must not report team expert")
	}
	if err := mgr.SetExpertBinding("solo"); err != nil {
		t.Fatalf("bind solo: %v", err)
	}
	if SessionHasTeamExpert(workDir, mgr) {
		t.Fatal("agent-type expert must not report team")
	}
	if err := mgr.SetExpertBinding("ghost"); err != nil {
		t.Fatalf("bind ghost: %v", err)
	}
	if SessionHasTeamExpert(workDir, mgr) {
		t.Fatal("missing bundle must not report team")
	}
	if err := mgr.SetExpertBinding("studio"); err != nil {
		t.Fatalf("bind studio: %v", err)
	}
	if !SessionHasTeamExpert(workDir, mgr) {
		t.Fatal("team expert binding must report team")
	}
}

func TestSubAgentToolsEnabled(t *testing.T) {
	if SubAgentToolsEnabled(nil, false) {
		t.Fatal("nil runtime must not enable tools")
	}
	if !SubAgentToolsEnabled(nil, true) {
		t.Fatal("adapter request must keep tools enabled")
	}
	runtime := &SessionRuntime{Expert: &ExpertBinding{Team: true}}
	if !SubAgentToolsEnabled(runtime, false) {
		t.Fatal("team expert must force tools even when not requested")
	}
	agentOnly := &SessionRuntime{Expert: &ExpertBinding{Type: "agent"}}
	if SubAgentToolsEnabled(agentOnly, false) {
		t.Fatal("agent expert must not force tools")
	}
}

func TestProjectExpertBuild(t *testing.T) {
	identity, roster := projectExpertBuild(nil, &AgentBuildOptions{})
	if identity != "" || roster != "" {
		t.Fatal("nil binding must produce empty prompts")
	}
	opts := AgentBuildOptions{}
	if _, _ = projectExpertBuild(&ExpertBinding{Type: "agent"}, &opts); opts.MultiAgent {
		t.Fatal("agent-type binding must not force multi-agent")
	}
	opts = AgentBuildOptions{}
	identity, roster = projectExpertBuild(&ExpertBinding{Team: true, IdentityPrompt: "ID", RosterPrompt: "RS"}, &opts)
	if !opts.MultiAgent || identity != "ID" || roster != "RS" {
		t.Fatalf("team binding = opts.MultiAgent %v identity %q roster %q", opts.MultiAgent, identity, roster)
	}
}

func TestComposeSteeringOrderAndNoop(t *testing.T) {
	if composeSteering(nil, nil) != nil {
		t.Fatal("no mailbox and no adapter source must stay nil (default-path invariance)")
	}
	adapter := func() []provider.Message {
		return []provider.Message{provider.NewUserMessage("ADAPTER-STEERING")}
	}
	// Adapter-only wiring keeps behavior identical to the passthrough contract.
	merged := composeSteering(nil, adapter)()
	if len(merged) != 1 || merged[0].Content != "ADAPTER-STEERING" {
		t.Fatalf("adapter-only merge = %+v", merged)
	}
	mailbox := agent.NewMemberMailbox()
	mailbox.Enqueue(agent.MemberCompletion{MemberID: "studio-a", DisplayName: "成员A", Status: "done", Payload: "RESULT-A"})
	mailbox.Enqueue(agent.MemberCompletion{MemberID: "studio-b", Status: "error", Payload: "BOOM"})
	combined := composeSteering(mailbox, adapter)
	first := combined()
	if len(first) != 3 {
		t.Fatalf("combined drain len = %d, want adapter + 2 completions", len(first))
	}
	if first[0].Content != "ADAPTER-STEERING" {
		t.Fatal("adapter steering must keep its existing order (first)")
	}
	if !strings.Contains(first[1].Content, "[MEMBER_COMPLETION]") || !strings.Contains(first[1].Content, "studio-a") {
		t.Fatalf("first completion = %q", first[1].Content)
	}
	if !first[1].SystemInjected || !first[2].SystemInjected {
		t.Fatal("member completions must be flagged system-injected (never user intent)")
	}
	if !strings.Contains(first[2].Content, "下一步") {
		t.Fatalf("error completion missing next-step hint: %q", first[2].Content)
	}
	// Drain consumed the mailbox: the next iteration yields only adapter output.
	second := combined()
	if len(second) != 1 || second[0].Content != "ADAPTER-STEERING" {
		t.Fatalf("post-drain merge = %+v, want adapter only", second)
	}
	// Empty mailbox, no adapter source: nil result keeps the loop no-op.
	emptyOnly := composeSteering(mailbox, nil)
	if got := emptyOnly(); got != nil {
		t.Fatalf("empty mailbox drain = %+v, want nil", got)
	}
}

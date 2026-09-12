package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/esm"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

// newExpertCommandApp assembles the same Runtime and manager path used by the
// production TUI. Keeping this as an integration-style helper makes the
// command tests cover session persistence, expert resource rehydration and
// the Runtime-owned forced-team capability together.
func newExpertCommandApp(t *testing.T) *App {
	t.Helper()
	workDir, sessionDir := t.TempDir(), t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = sessionDir
	settings.SkillsDir = t.TempDir()

	manager, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{
		WorkDir: workDir, SessionDir: sessionDir,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	runtime, err := (agentruntime.Builder{Settings: settings}).Build(context.Background(), agentruntime.BuildOptions{
		ID: manager.GetHeader().ID, Source: agentruntime.SourceTUI, WorkDir: workDir, Manager: manager,
	})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	t.Cleanup(runtime.Close)

	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model", ContextWindow: 128000}}, nil)
	app := NewApp(p, p.Models()[0], settings, manager, runtime.Registry, "", runtime.ExtraContext, runtime.RuleContent,
		runtime.SkillsMgr, "yolo", false, false, nil, nil, nil)
	app.SetRuntime(runtime)
	if err := app.refreshExpertAwareAgentManager(); err != nil {
		t.Fatalf("assemble agent manager: %v", err)
	}
	return app
}

func TestExpertCommandListsShowsBindsAndUnbindsRuntimeExpert(t *testing.T) {
	app := newExpertCommandApp(t)

	app.handleCommand("/expert list")
	listed := stripANSI(strings.Join(app.messages, "\n"))
	for _, id := range []string{"frontend-developer", "software-company"} {
		if !strings.Contains(listed, id) {
			t.Fatalf("expert list = %q, want %q", listed, id)
		}
	}

	app.handleCommand("/expert show software-company")
	shown := stripANSI(app.messages[len(app.messages)-1])
	for _, want := range []string{"Expert: software-company", "Type: team", "software-engineer"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("expert show = %q, want %q", shown, want)
		}
	}

	app.handleCommand("/expert bind software-company")
	if got := app.session.GetExpertID(); got != "software-company" {
		t.Fatalf("persisted expert = %q, want software-company", got)
	}
	if !app.runtime.TeamExpertActive() || !app.agentManagementEnabled() {
		t.Fatal("bound team expert must force TUI agent-management capability")
	}
	if _, ok := app.registry.Get("subagent_spawn"); !ok {
		t.Fatal("team expert must project Runtime sub-agent tools into the TUI registry")
	}
	if app.agentMgr == nil || app.agentMgr.Members == nil || app.agentMgr.ExpertID != "software-company" {
		t.Fatalf("agent manager expert context = %#v, want software-company roster", app.agentMgr)
	}
	app.width = 160
	if footer := stripANSI(app.expertFooter()); !strings.Contains(footer, "Team:software-company 4 members") {
		t.Fatalf("expert footer = %q", footer)
	}

	app.handleCommand("/expert unbind")
	if got := app.session.GetExpertID(); got != "" {
		t.Fatalf("persisted expert after unbind = %q, want empty", got)
	}
	if app.runtime.TeamExpertActive() || app.agentManagementEnabled() {
		t.Fatal("unbound app with no explicit multi-agent flags must disable team capability")
	}
	// Every canonical sub-agent tool must be gone, not just the spawn tool: a
	// hand-copied removal list in the adapter is the N6 class of bug.
	for _, name := range agent.SubAgentToolNames() {
		if _, ok := app.registry.Get(name); ok {
			t.Fatalf("sub-agent tool %s remained after the team expert was unbound", name)
		}
	}
}

func TestExpertCommandSwitchForksAndProjectsMemberLifecycle(t *testing.T) {
	app := newExpertCommandApp(t)
	app.handleCommand("/expert bind software-company")
	if got := app.session.GetExpertID(); got != "software-company" {
		t.Fatalf("bind team expert = %q", got)
	}
	sourceID := app.session.GetHeader().ID

	// Background member events carry canonical persona metadata. The TUI only
	// projects these events; it does not derive member state from package files.
	app.handleAgentEvent(agent.Event{
		Type: agent.EventAgentStart, AgentID: "member-run", MemberID: "software-engineer",
		MemberDisplayName: "Kede", MemberEmoji: "🛠️",
	})
	if footer := stripANSI(app.expertFooter()); !strings.Contains(footer, "Team:software-company 1/4 active") {
		t.Fatalf("running team footer = %q", footer)
	}
	app.handleAgentEvent(agent.Event{
		Type: agent.EventRunFinished, AgentID: "member-run", MemberID: "software-engineer",
		MemberDisplayName: "Kede", MemberEmoji: "🛠️", Status: agent.TaskSuccess,
	})
	projected := stripANSI(strings.Join(app.messages, "\n"))
	for _, want := range []string{"Member started: 🛠️ Kede (software-engineer)", "Member success: 🛠️ Kede (software-engineer)"} {
		if !strings.Contains(projected, want) {
			t.Fatalf("member projection = %q, want %q", projected, want)
		}
	}

	// Forks intentionally require an immutable, completed conversation
	// boundary. Create one through the manager rather than bypassing the
	// canonical session API.
	if err := app.session.StartConversationTurn("expert-command-turn", "intent", "run"); err != nil {
		t.Fatalf("start conversation turn: %v", err)
	}
	if _, err := app.session.AppendMessage(provider.NewUserMessage("preserve this history")); err != nil {
		t.Fatalf("append source message: %v", err)
	}
	if err := app.session.EndConversationTurn("expert-command-turn", "completed", "stop"); err != nil {
		t.Fatalf("complete conversation turn: %v", err)
	}

	app.handleCommand("/expert switch frontend-developer")
	if app.session.GetHeader().ID == sourceID {
		t.Fatal("expert switch must open a forked session, not rewrite the source")
	}
	if got := app.session.GetExpertID(); got != "frontend-developer" {
		t.Fatalf("forked expert = %q, want frontend-developer", got)
	}
	if app.runtime.TeamExpertActive() || app.agentManagementEnabled() {
		t.Fatal("agent expert child must not retain the source team's forced capability")
	}
	source, err := session.OpenByIDExact(app.getSessionDir(), sourceID)
	if err != nil {
		t.Fatalf("open source session: %v", err)
	}
	if got := source.GetExpertID(); got != "software-company" {
		t.Fatalf("source expert mutated to %q", got)
	}

}

func TestExpertCommandRejectsIdentityChangeDuringActiveRun(t *testing.T) {
	app := newExpertCommandApp(t)
	app.isThinking = true
	app.handleCommand("/expert bind software-company")
	if got := app.session.GetExpertID(); got != "" {
		t.Fatalf("active run changed expert binding to %q", got)
	}
	if message := stripANSI(app.messages[len(app.messages)-1]); !strings.Contains(message, "Cannot change expert while a run or member is active") {
		t.Fatalf("active-run rejection = %q", message)
	}
}

// A member terminal event is only a background projection. It must neither
// finish the lead's tracked ESM run nor start an idle continuation; completed
// member output reaches the lead through the Runtime-owned mailbox/steering
// boundary instead.
func TestMemberTerminalDoesNotFinishOrContinueESMRun(t *testing.T) {
	app := newExpertCommandApp(t)
	app.handleCommand("/expert bind software-company")
	store := app.ensureESMStore()
	if _, err := store.Create(context.Background(), app.currentSessionID(), "finish the team delivery"); err != nil {
		t.Fatalf("create ESM objective: %v", err)
	}

	app.isThinking = true
	app.prepareESMRun()
	app.esmMu.Lock()
	trackedBefore := app.esmRunTracked
	runIDBefore := app.esmRunID
	app.esmMu.Unlock()
	if !trackedBefore || runIDBefore == "" {
		t.Fatalf("active lead ESM state = tracked:%v run:%q", trackedBefore, runIDBefore)
	}

	app.handleAgentEvent(agent.Event{
		Type: agent.EventRunFinished, AgentID: "member-terminal", MemberID: "software-engineer",
		ExpertID: "software-company", MemberDisplayName: "Kede", Status: agent.TaskSuccess,
	})

	if !app.isThinking {
		t.Fatal("member terminal event ended the active lead run")
	}
	if app.runTerminalHandled {
		t.Fatal("member terminal event was treated as the lead terminal event")
	}
	app.esmMu.Lock()
	trackedAfter := app.esmRunTracked
	runIDAfter := app.esmRunID
	app.esmMu.Unlock()
	if !trackedAfter || runIDAfter != runIDBefore {
		t.Fatalf("member terminal changed ESM lead state: tracked:%v run:%q", trackedAfter, runIDAfter)
	}
	obj, err := store.Get(context.Background(), app.currentSessionID())
	if err != nil {
		t.Fatalf("get ESM objective: %v", err)
	}
	if obj.Status != esm.StatusActive {
		t.Fatalf("member terminal changed ESM objective status to %q", obj.Status)
	}
}

// The TUI half of the cross-entry ESM idle gate: autonomous continuation may
// only start from a genuinely idle session. User input and unresolved
// decisions retain ownership of the next execution boundary, while a paused
// objective remains non-runnable even after the UI becomes idle.
func TestESMIdleContinuationGateRejectsBusyAndNonRunnableStates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*App)
	}{
		{"lead run active", func(app *App) { app.isThinking = true }},
		{"manual compaction", func(app *App) { app.manualCompactionActive = true }},
		{"approval pending", func(app *App) { app.waitingForApproval = true }},
		{"question pending", func(app *App) { app.waitingForQuestion = true }},
		{"queued input", func(app *App) {
			app.inputQueueMu.Lock()
			app.inputQueue = append(app.inputQueue, InputEvent{})
			app.inputQueueMu.Unlock()
		}},
		{"composer input", func(app *App) { app.input.SetValue("keep my next turn") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newExpertCommandApp(t)
			store := app.ensureESMStore()
			if _, err := store.Create(context.Background(), app.currentSessionID(), "continue only after this session is idle"); err != nil {
				t.Fatalf("create ESM objective: %v", err)
			}
			tc.setup(app)
			if cmd := app.startESMContinuationIfIdle(); cmd != nil {
				t.Fatal("busy TUI state started an autonomous ESM continuation")
			}
			app.esmMu.Lock()
			tracked, runID := app.esmRunTracked, app.esmRunID
			app.esmMu.Unlock()
			if tracked || runID != "" {
				t.Fatalf("busy TUI state prepared an ESM run: tracked=%v run=%q", tracked, runID)
			}
		})
	}

	app := newExpertCommandApp(t)
	store := app.ensureESMStore()
	if _, err := store.Create(context.Background(), app.currentSessionID(), "paused objectives cannot continue"); err != nil {
		t.Fatalf("create paused ESM objective: %v", err)
	}
	if _, err := store.Pause(context.Background(), app.currentSessionID()); err != nil {
		t.Fatalf("pause ESM objective: %v", err)
	}
	if cmd := app.startESMContinuationIfIdle(); cmd != nil {
		t.Fatal("paused ESM objective started a TUI continuation")
	}
}

package agentruntime

import (
	"context"
	"testing"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/tools"
)

// mailboxBuildFixture attaches a team-capable session runtime whose mailbox
// reports a running member and already holds one member completion, so a build
// that wrongly owns the session mailbox both blocks and steals the notification.
func mailboxBuildFixture(t *testing.T) (*SessionRuntime, *provider.MockProvider) {
	t.Helper()
	workDir := t.TempDir()
	manager := session.New(workDir, t.TempDir())
	if err := manager.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	registry := tools.NewRegistry(workDir, sandbox.NewNoneSandbox())
	runtime, err := AttachSessionResources(AttachedResources{
		Source: SourceTUI, WorkDir: workDir, Manager: manager, Registry: registry,
	})
	if err != nil {
		t.Fatalf("attach session resources: %v", err)
	}
	t.Cleanup(runtime.Close)
	if runtime.Mailbox == nil {
		t.Fatal("runtime mailbox is not initialized")
	}
	runtime.Mailbox.SetRunningPredicate(func() bool { return true })
	runtime.Mailbox.Enqueue(agent.MemberCompletion{
		Kind: agent.MemberItemCompletion, MemberID: "member-1", DisplayName: "Engineer",
		Status: agent.MemberStatusDone, Payload: "member finished",
	})

	mock := provider.NewMockProvider("mock", []*provider.Model{{ID: "model1", Name: "Model 1"}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "answer"},
		{Type: provider.StreamDone, StopReason: "stop"},
	})
	return runtime, mock
}

// runAgentToTerminalStatus runs one prompt to completion and returns the status
// of the canonical terminal event. The bounded context keeps a regression that
// wrongly waits for members from hanging the test: the wait ends as TaskCanceled
// when the context expires.
func runAgentToTerminalStatus(t *testing.T, a *agent.Agent, prompt string) agent.TaskStatus {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	status := agent.TaskStatus("")
	for event := range a.Run(ctx, prompt) {
		if event.Type == agent.EventRunFinished {
			status = event.Status
		}
	}
	return status
}

func mailboxBuildOptions(id string, mock *provider.MockProvider) AgentBuildOptions {
	return AgentBuildOptions{ID: agentpkg.AgentID(id), Provider: mock, ProviderName: "mock", Model: mock.Models()[0], Mode: ModeYolo}
}

// TestTransientAgentsDoNotInheritTheLeadMemberWait guards the mailbox ownership
// of the shared build path: BuildTransientAgent (side questions, knowledge-base
// indexing) is called with a nil session manager, so it must not receive the
// team lead's follow-up hook. With the hook it would block on the session's
// running members for up to MemberFollowUpWaitMax and swallow member
// notifications that belong to the lead.
func TestTransientAgentsDoNotInheritTheLeadMemberWait(t *testing.T) {
	runtime, mock := mailboxBuildFixture(t)

	transient, err := runtime.BuildTransientAgent(runtime.Registry, mailboxBuildOptions("side-question", mock))
	if err != nil {
		t.Fatalf("build transient agent: %v", err)
	}

	// A build that wrongly inherited the member wait would block on the running
	// member until the run context expires and end as TaskCanceled.
	if status := runAgentToTerminalStatus(t, transient, "side question"); status != agent.TaskSuccess {
		t.Fatalf("transient agent terminal status = %q, want %q", status, agent.TaskSuccess)
	}
	if !runtime.Mailbox.HasPending() {
		t.Fatal("transient agent consumed a member notification that belongs to the lead")
	}
}

// TestAuxiliaryBuildsDoNotInheritTheLeadMemberWait guards the ownership rule for
// builds that run on the session's own manager without being the session's lead
// (the legacy knowledge librarian query bridge): they must neither wait for the
// session's members nor drain their notifications.
func TestAuxiliaryBuildsDoNotInheritTheLeadMemberWait(t *testing.T) {
	runtime, mock := mailboxBuildFixture(t)

	opts := mailboxBuildOptions("knowledge-librarian", mock)
	opts.AuxiliaryRole = true
	auxiliary, err := runtime.BuildAgent(opts)
	if err != nil {
		t.Fatalf("build auxiliary agent: %v", err)
	}

	if status := runAgentToTerminalStatus(t, auxiliary, "query the knowledge base"); status != agent.TaskSuccess {
		t.Fatalf("auxiliary agent terminal status = %q, want %q", status, agent.TaskSuccess)
	}
	if !runtime.Mailbox.HasPending() {
		t.Fatal("auxiliary build consumed a member notification that belongs to the lead")
	}
	if calls := mock.GetCallCount(); calls != 1 {
		t.Fatalf("auxiliary build provider calls = %d, want 1 (no member notification injected)", calls)
	}
}

// TestNonTeamSessionDeliversMemberNotificationsWithoutWaiting guards N8: a
// session without a bound expert team still installs the session mailbox, so a
// member's blocking question reaches the lead (and subagent_answer can resolve
// it instead of the member blocking until its own timeout), while the lead's run
// must still be allowed to end without waiting for members.
func TestNonTeamSessionDeliversMemberNotificationsWithoutWaiting(t *testing.T) {
	runtime, mock := mailboxBuildFixture(t)
	manager, err := NewAgentManager(AgentManagerOptions{
		Runtime: runtime, Provider: mock, Model: mock.Models()[0],
		Settings: config.DefaultSettings(), MultiAgentEnabled: true,
	})
	if err != nil {
		t.Fatalf("new agent manager: %v", err)
	}
	if manager.Mailbox == nil {
		t.Fatal("a session without a bound team did not install the session mailbox")
	}
	manager.NotifyMemberQuestion("member-1", "Engineer", "question-member-1-1", "Which database?", nil)
	if !manager.Mailbox.HasPending() {
		t.Fatal("member question was dropped before the lead could see it")
	}

	// The manager installs its own "any child running" predicate; this fixture has
	// no real children, so it reports the simulated running member instead. A lead
	// that wrongly waited would block until the run context expires and end as
	// TaskCanceled instead of TaskSuccess.
	runtime.Mailbox.SetRunningPredicate(func() bool { return true })
	lead, err := runtime.BuildAgent(mailboxBuildOptions("session-lead", mock))
	if err != nil {
		t.Fatalf("build session lead: %v", err)
	}
	if status := runAgentToTerminalStatus(t, lead, "continue"); status != agent.TaskSuccess {
		t.Fatalf("non-team lead terminal status = %q, want %q (it must not hold the run open for members)", status, agent.TaskSuccess)
	}
	if manager.Mailbox.HasPending() {
		t.Fatal("lead did not drain the member notification")
	}
}

// TestTeamBoundSessionKeepsTheMemberWait is the counterpart of that gate: a
// session bound to a team expert may still hold its run open for members.
func TestTeamBoundSessionKeepsTheMemberWait(t *testing.T) {
	runtime, mock := mailboxBuildFixture(t)
	// The queued completion is consumed at the run's first iteration; the wait is
	// driven by the still-running member predicate alone.
	runtime.Mailbox.DrainSteering()
	if err := runtime.SetExpert("software-company"); err != nil {
		t.Fatalf("bind team expert: %v", err)
	}
	manager, err := NewAgentManager(AgentManagerOptions{
		Runtime: runtime, Provider: mock, Model: mock.Models()[0],
		Settings: config.DefaultSettings(), MultiAgentEnabled: true,
	})
	if err != nil {
		t.Fatalf("new agent manager: %v", err)
	}
	if manager.Mailbox == nil {
		t.Fatal("team session did not install the session mailbox")
	}
	// The manager installs its own "any child running" predicate; this fixture has
	// no real children, so it reports the simulated running member instead.
	runtime.Mailbox.SetRunningPredicate(func() bool { return true })

	lead, err := runtime.BuildAgent(mailboxBuildOptions("team-lead", mock))
	if err != nil {
		t.Fatalf("build team lead: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() {
		for range lead.Run(ctx, "continue") {
		}
		close(finished)
	}()
	select {
	case <-finished:
		t.Fatal("team lead ended its run while its members were still running")
	case <-time.After(time.Second):
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled team lead did not finish")
	}
}

// TestSessionLeadBuildKeepsTheMemberMailbox is the positive counterpart: the
// session's conversational lead must still drain member notifications and see
// the terminal status they carry. Without it the ownership guards above could
// pass by removing the hooks from every build.
func TestSessionLeadBuildKeepsTheMemberMailbox(t *testing.T) {
	runtime, mock := mailboxBuildFixture(t)
	// The lead owns the mailbox: a completion is a notification for this run, not
	// a reason to wait for members.
	runtime.Mailbox.SetRunningPredicate(func() bool { return false })

	lead, err := runtime.BuildAgent(mailboxBuildOptions("session-lead", mock))
	if err != nil {
		t.Fatalf("build session lead: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status := agent.TaskStatus("")
	for event := range lead.Run(ctx, "continue") {
		if event.Type == agent.EventRunFinished {
			status = event.Status
		}
	}
	if status != agent.TaskSuccess {
		t.Fatalf("lead terminal status = %q, want %q", status, agent.TaskSuccess)
	}
	if runtime.Mailbox.HasPending() {
		t.Fatal("session lead did not drain the member notification")
	}
}

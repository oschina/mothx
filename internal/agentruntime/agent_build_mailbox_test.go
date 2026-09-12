package agentruntime

import (
	"context"
	"testing"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/agent"
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	for range transient.Run(ctx, "side question") {
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("transient agent run took %s: it waited for the session's members instead of finishing", elapsed)
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	for range auxiliary.Run(ctx, "query the knowledge base") {
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("auxiliary agent run took %s: it waited for the session's members instead of finishing", elapsed)
	}
	if !runtime.Mailbox.HasPending() {
		t.Fatal("auxiliary build consumed a member notification that belongs to the lead")
	}
	if calls := mock.GetCallCount(); calls != 1 {
		t.Fatalf("auxiliary build provider calls = %d, want 1 (no member notification injected)", calls)
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

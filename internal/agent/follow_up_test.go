package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/tools"
)

// TestLoopInjectsFollowUpMessagesInsteadOfStopping guards the team-lead wake
// path: when a member completion (or question) arrives while the lead is
// producing its final turn, the follow-up hook must keep the run open and hand
// the message to the lead instead of the run ending and canceling the members.
func TestLoopInjectsFollowUpMessagesInsteadOfStopping(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "m1", Name: "Model 1"}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "lead turn"},
		{Type: provider.StreamDone, StopReason: "stop"},
	})

	const followUpText = "[MEMBER_COMPLETION] member finished after the lead's last iteration"
	drains := 0
	a := NewWithLoopConfig(AgentLoopConfig{
		Config: Config{
			ID:       "lead",
			Provider: mockProvider,
			Model:    mockProvider.Models()[0],
			Mode:     "yolo",
		},
		GetFollowUpMessages: func(context.Context) []provider.Message {
			drains++
			if drains == 1 {
				return []provider.Message{provider.NewSystemInjectedUserMessage(followUpText)}
			}
			return nil
		},
	}, tools.NewRegistry(t.TempDir(), nil))

	var (
		injected bool
		terminal TaskStatus
	)
	for event := range a.Run(context.Background(), "start") {
		if event.Type == EventMessageStart && strings.Contains(event.Message.Content, followUpText) {
			injected = true
		}
		if event.Type == EventRunFinished {
			terminal = event.Status
		}
	}

	if !injected {
		t.Fatal("follow-up message was never injected into the conversation")
	}
	if terminal != TaskSuccess {
		t.Fatalf("terminal status = %q, want %q", terminal, TaskSuccess)
	}
	if got := mockProvider.GetCallCount(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (the run must continue after the follow-up instead of stopping)", got)
	}
}

// TestMemberFollowUpsWaitsForRunningChildren guards the confirmed team
// semantics: the lead's run must not end (and cancel its members) while member
// work is still in flight; the hook blocks until a notification arrives and
// then hands it to the lead.
func TestMemberFollowUpsWaitsForRunningChildren(t *testing.T) {
	mgr, mbox := newMemberTestManager(t, nil, nil)
	factory := mgr.factory
	if factory == nil {
		t.Fatal("member test manager has no factory installed")
	}

	hook := ComposeFollowUps(mbox, nil)
	if messages := hook(context.Background()); len(messages) != 0 {
		t.Fatalf("follow-ups with no running children = %d messages, want 0", len(messages))
	}

	mgr.mu.Lock()
	mgr.statuses["agent-child-1"] = ManagedAgentStatus{ID: "agent-child-1", State: "running"}
	mgr.parentOf["agent-child-1"] = "agent-lead"
	mgr.mu.Unlock()
	if !mbox.RunningChildren() {
		t.Fatal("member context did not install the running-children probe")
	}

	done := make(chan []provider.Message, 1)
	go func() { done <- hook(context.Background()) }()
	select {
	case messages := <-done:
		t.Fatalf("follow-ups returned %d messages while a child was still running", len(messages))
	case <-time.After(100 * time.Millisecond):
	}

	mbox.Enqueue(MemberCompletion{MemberID: "agent-child-1", DisplayName: "Alice", Status: MemberStatusDone, Payload: "member result"})
	select {
	case messages := <-done:
		if len(messages) != 1 || !strings.Contains(messages[0].Content, "[MEMBER_COMPLETION]") {
			t.Fatalf("follow-ups = %#v, want the member completion", messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow-ups did not return after the member completion")
	}

	// A cancelled run must not park the loop waiting for members.
	mgr.mu.Lock()
	mgr.statuses["agent-child-1"] = ManagedAgentStatus{ID: "agent-child-1", State: "running"}
	mgr.parentOf["agent-child-1"] = "agent-lead"
	mgr.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan []provider.Message, 1)
	go func() { cancelled <- hook(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("member follow-ups ignored context cancellation")
	}
}

// TestDelegateSubAgentExcludesQuestionTool guards the blocking-delegate
// contract: the caller is parked inside the delegate tool call, so a child
// question could never be answered.
func TestDelegateSubAgentExcludesQuestionTool(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mgr.Create(AgentOptions{ID: "main"})

	result, err := NewDelegateSubAgentTool(mgr).Execute(ContextWithAgentID(context.Background(), "main"), map[string]any{"task": "summarize"})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		t.Fatalf("parse delegate result: %v", err)
	}
	handle, _ := parsed["handle"].(string)
	if handle == "" {
		t.Fatal("delegate result is missing the handle")
	}
	child, ok := mgr.Get(agentpkg.AgentID(handle))
	if !ok {
		t.Fatal("delegated child is no longer inspectable")
	}
	adapter, ok := child.(*AgentAdapter)
	if !ok || adapter.inner == nil {
		t.Fatalf("unexpected delegated child type %T", child)
	}
	if _, ok := adapter.inner.registry.Get("question"); ok {
		t.Fatal("delegate child must not expose the question tool")
	}
}

// TestComposeFollowUpsKeepsUserSteeringResponsiveWhileMembersRun guards the
// "wait for members without losing the user" contract: a lead parked in the
// member wait must still return as soon as adapter steering (for example a
// queued user message) is available.
func TestComposeFollowUpsKeepsUserSteeringResponsiveWhileMembersRun(t *testing.T) {
	mbox := NewMemberMailbox()
	mbox.SetRunningPredicate(func() bool { return true })

	var mu sync.Mutex
	var pending []provider.Message
	adapter := func() []provider.Message {
		mu.Lock()
		defer mu.Unlock()
		messages := pending
		pending = nil
		return messages
	}
	hook := ComposeFollowUps(mbox, adapter)

	done := make(chan []provider.Message, 1)
	go func() { done <- hook(context.Background()) }()
	select {
	case messages := <-done:
		t.Fatalf("hook returned %#v while members were running and no steering was pending", messages)
	case <-time.After(100 * time.Millisecond):
	}

	mu.Lock()
	pending = []provider.Message{provider.NewUserMessage("change of plan")}
	mu.Unlock()
	select {
	case messages := <-done:
		if len(messages) != 1 || messages[0].Content != "change of plan" {
			t.Fatalf("steering = %#v, want the queued user message", messages)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("user steering did not wake the lead while members were running")
	}
}

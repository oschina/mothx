package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/tools"
)

func TestRequestQuestionReturnsOnContextCancel(t *testing.T) {
	a := New(Config{Mode: "plan"}, tools.NewRegistry(t.TempDir(), nil))
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan Event, 1)

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if answer := a.RequestQuestion(ctx, ch, "pick one", []string{"a", "b"}, ""); answer != "" {
		t.Fatalf("answer = %q, want empty on context cancel", answer)
	}

	a.questionMu.Lock()
	pending := len(a.pendingQuestions)
	a.questionMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending questions leaked: %d", pending)
	}
}

func TestRequestQuestionStillAnswers(t *testing.T) {
	a := New(Config{ID: "agent-question", Mode: "plan"}, tools.NewRegistry(t.TempDir(), nil))
	ch := make(chan Event, 1)
	go func() {
		// Question IDs embed the agent ID, so the responder must use the ID the
		// request actually published instead of a hardcoded "question-1".
		ev := <-ch
		a.HandleQuestionResponse(ev.QuestionID, "option a")
	}()
	if answer := a.RequestQuestion(context.Background(), ch, "pick one", []string{"a", "b"}, ""); answer != "option a" {
		t.Fatalf("answer = %q, want %q", answer, "option a")
	}
}

// TestRequestToolApprovalIDsAreUniqueAcrossAgents guards the multi-agent
// decision registry: a lead and each sub-agent count their own approvals, so
// per-instance counters alone would both emit "approval-1" and the second
// request would be dropped as a duplicate by front ends that key decisions by
// ID (TUI/WebUI durable decision records).
func TestRequestToolApprovalIDsAreUniqueAcrossAgents(t *testing.T) {
	lead := New(Config{ID: "agent-lead", Mode: "agent"}, tools.NewRegistry(t.TempDir(), nil))
	child := New(Config{ID: "agent-child", Mode: "agent"}, tools.NewRegistry(t.TempDir(), nil))
	leadCh := make(chan Event, 1)
	childCh := make(chan Event, 1)
	leadResult := make(chan bool, 1)
	childResult := make(chan bool, 1)

	go func() {
		leadResult <- lead.RequestToolApproval(context.Background(), leadCh, "call-1", "bash", map[string]any{"command": "ls"})
	}()
	go func() {
		childResult <- child.RequestToolApproval(context.Background(), childCh, "call-2", "bash", map[string]any{"command": "ls"})
	}()

	leadEvent := waitApprovalEvent(t, leadCh)
	childEvent := waitApprovalEvent(t, childCh)
	if leadEvent.ApprovalID == childEvent.ApprovalID {
		t.Fatalf("approval IDs collided across agents: %q", leadEvent.ApprovalID)
	}
	if !strings.Contains(leadEvent.ApprovalID, "agent-lead") {
		t.Fatalf("lead approval ID %q does not embed the agent ID", leadEvent.ApprovalID)
	}
	if !strings.Contains(childEvent.ApprovalID, "agent-child") {
		t.Fatalf("child approval ID %q does not embed the agent ID", childEvent.ApprovalID)
	}

	lead.HandleApprovalResponse(leadEvent.ApprovalID, true)
	child.HandleApprovalResponse(childEvent.ApprovalID, true)
	for name, result := range map[string]chan bool{"lead": leadResult, "child": childResult} {
		select {
		case approved := <-result:
			if !approved {
				t.Fatalf("%s approval was not granted", name)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s approval did not return after a response", name)
		}
	}
}

// TestRequestToolApprovalReturnsOnContextCancel guards the run-cancellation
// path: a cancelled run must unblock an approval wait, otherwise a child agent
// parked here keeps its parent's tool batch (BoundedParallel.wg.Wait) blocked
// and the run never reaches a terminal state.
func TestRequestToolApprovalReturnsOnContextCancel(t *testing.T) {
	a := New(Config{ID: "agent-cancel", Mode: "agent"}, tools.NewRegistry(t.TempDir(), nil))
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan Event, 1)
	result := make(chan bool, 1)

	go func() {
		result <- a.RequestToolApproval(ctx, ch, "call-1", "bash", map[string]any{"command": "ls"})
	}()
	ev := waitApprovalEvent(t, ch)
	cancel()

	select {
	case approved := <-result:
		if approved {
			t.Fatal("approval unexpectedly granted after context cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("RequestToolApproval did not return on context cancel")
	}
	a.approvalMu.Lock()
	pending := len(a.pendingApprovals)
	a.approvalMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending approvals leaked: %d", pending)
	}
	// A late response for a cancelled request must be a no-op, not a block.
	a.HandleApprovalResponse(ev.ApprovalID, true)
}

func waitApprovalEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		if ev.Type != EventToolApprovalRequest {
			t.Fatalf("event type = %v, want approval request", ev.Type)
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an approval request event")
		return Event{}
	}
}

// TestRequestEventsDoNotParkWithoutAConsumerWhenCanceled guards the request
// sends themselves: the cancellation select in RequestToolApproval/
// RequestQuestion is only reachable after the request event is delivered, so a
// bare channel send used to park a cancelled run forever when its consumer had
// stopped reading. Both requests now go through the context-aware send.
func TestRequestEventsDoNotParkWithoutAConsumerWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	approvalAgent := New(Config{ID: "agent-approval-send", Mode: "agent"}, tools.NewRegistry(t.TempDir(), nil))
	// The loop always registers the run context before it can request anything.
	approvalAgent.setRunContext(ctx)
	cancel()

	approvalCh := make(chan Event) // unbuffered, no reader
	approvalDone := make(chan struct{})
	go func() {
		if approved := approvalAgent.RequestToolApproval(ctx, approvalCh, "call-1", "bash", map[string]any{"command": "ls"}); approved {
			t.Error("approval must not be granted for a cancelled run")
		}
		close(approvalDone)
	}()
	select {
	case <-approvalDone:
	case <-time.After(time.Second):
		t.Fatal("RequestToolApproval parked on the request send without a consumer")
	}

	questionAgent := New(Config{ID: "agent-question-send", Mode: "agent"}, tools.NewRegistry(t.TempDir(), nil))
	questionAgent.setRunContext(ctx)
	questionCh := make(chan Event)
	questionDone := make(chan struct{})
	go func() {
		if answer := questionAgent.RequestQuestion(ctx, questionCh, "pick one", []string{"a"}, ""); answer != "" {
			t.Errorf("question answer = %q, want empty for a cancelled run", answer)
		}
		close(questionDone)
	}()
	select {
	case <-questionDone:
	case <-time.After(time.Second):
		t.Fatal("RequestQuestion parked on the request send without a consumer")
	}
}

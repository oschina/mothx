package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/tools"
)

// TestMemberQuestionWakesLeadAndRendersSteering guards the "members ask the
// lead" contract: a member question must wake a blocked subagent_wait (the
// activity signal) and must be injected as an identifiable [MEMBER_QUESTION]
// steering message carrying the question ID the lead has to answer.
func TestMemberQuestionWakesLeadAndRendersSteering(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mbox := NewMemberMailbox()
	mgr.SetMemberContext(nil, mbox, "")

	mgr.NotifyMemberQuestion("agent-member-1", "Alice", "question-agent-member-1-1", "Which database?", []string{"postgres", "sqlite"})

	timedOut, err := mbox.WaitForActivity(context.Background(), 2*time.Second)
	if err != nil {
		t.Fatalf("WaitForActivity: %v", err)
	}
	if timedOut {
		t.Fatal("member question did not signal activity; a blocked subagent_wait would never return")
	}

	pending := mbox.PendingSummary()
	if len(pending) != 1 {
		t.Fatalf("pending summary = %+v, want one entry", pending)
	}
	if pending[0].Status != MemberStatusQuestion || pending[0].QuestionID != "question-agent-member-1-1" {
		t.Fatalf("pending entry = %+v, want a question with its question ID", pending[0])
	}

	steering := mbox.DrainSteering()
	if len(steering) != 1 {
		t.Fatalf("steering = %d messages, want 1", len(steering))
	}
	content := steering[0].Content
	for _, want := range []string{"[MEMBER_QUESTION]", "Which database?", "question-agent-member-1-1", "subagent_answer"} {
		if !strings.Contains(content, want) {
			t.Fatalf("steering message %q is missing %q", content, want)
		}
	}
}

// TestForwardMemberQuestionRoutesToLead verifies the sub-agent forwarding path:
// a member question is projected on the parent stream (adapters render it as
// "member asks the lead") and queued for the lead instead of being dropped.
func TestForwardMemberQuestionRoutesToLead(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mbox := NewMemberMailbox()
	mgr.SetMemberContext(nil, mbox, "")
	parentCh := make(chan Event, 1)

	forwardMemberQuestion(context.Background(), mgr, parentCh, "agent-child", agentpkg.Event{
		Type:            agentpkg.EventQuestionRequest,
		QuestionID:      "question-agent-child-1",
		QuestionText:    "Which one?",
		QuestionOptions: []string{"a", "b"},
	}, nil)

	select {
	case ev := <-parentCh:
		if ev.Type != EventQuestionRequest || ev.AgentID != "agent-child" || ev.QuestionID != "question-agent-child-1" {
			t.Fatalf("forwarded event = %+v", ev)
		}
	default:
		t.Fatal("member question was not projected on the parent stream")
	}
	if !mbox.HasPending() {
		t.Fatal("member question was not queued for the lead")
	}
}

// TestSubAgentAnswerToolResolvesMemberQuestion guards the lead-side answer path:
// without it a member parked in the question tool can never continue.
func TestSubAgentAnswerToolResolvesMemberQuestion(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	member := New(Config{ID: "member-1", Mode: "yolo"}, tools.NewRegistry(t.TempDir(), nil))
	mgr.Register(NewAgentAdapter(member))

	answerCh := make(chan string, 1)
	member.questionMu.Lock()
	member.pendingQuestions["question-member-1-1"] = answerCh
	member.questionMu.Unlock()

	tool := NewSubAgentAnswerTool(mgr)
	if _, err := tool.Execute(context.Background(), map[string]any{
		"handle": "member-1", "question_id": "question-member-1-1", "answer": "use sqlite",
	}); err != nil {
		t.Fatalf("subagent_answer: %v", err)
	}
	select {
	case answer := <-answerCh:
		if answer != "use sqlite" {
			t.Fatalf("answer = %q, want %q", answer, "use sqlite")
		}
	case <-time.After(time.Second):
		t.Fatal("member question was not resolved")
	}

	if _, err := tool.Execute(context.Background(), map[string]any{
		"handle": "missing", "question_id": "q", "answer": "a",
	}); err == nil {
		t.Fatal("expected an error for an unknown member handle")
	}
	if _, err := tool.Execute(context.Background(), map[string]any{
		"handle": "member-1", "answer": "a",
	}); err == nil {
		t.Fatal("expected an error when question_id is missing")
	}
}

// TestSubAgentAnswerToolRejectsQuestionsThatAreNotPending guards the delivery
// report of subagent_answer: HandleQuestionResponse ignores unknown IDs, so a
// lead would otherwise be told "Answered ..." for a question that was already
// resolved, expired, or never belonged to that member — and could end its run
// believing the member had been unblocked.
func TestSubAgentAnswerToolRejectsQuestionsThatAreNotPending(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	member := New(Config{ID: "member-2", Mode: "yolo"}, tools.NewRegistry(t.TempDir(), nil))
	mgr.Register(NewAgentAdapter(member))
	tool := NewSubAgentAnswerTool(mgr)

	// Unknown question ID.
	if _, err := tool.Execute(context.Background(), map[string]any{
		"handle": "member-2", "question_id": "question-member-2-404", "answer": "a",
	}); err == nil {
		t.Fatal("expected an error for a question that is not pending")
	}

	// Answered once, the same ID must not report success again.
	answerCh := make(chan string, 1)
	member.questionMu.Lock()
	member.pendingQuestions["question-member-2-1"] = answerCh
	member.questionMu.Unlock()
	if _, err := tool.Execute(context.Background(), map[string]any{
		"handle": "member-2", "question_id": "question-member-2-1", "answer": "use sqlite",
	}); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{
		"handle": "member-2", "question_id": "question-member-2-1", "answer": "use sqlite again",
	}); err == nil {
		t.Fatal("expected an error for a question that was already answered")
	}
}

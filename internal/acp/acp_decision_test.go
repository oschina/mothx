package acp

import (
	"context"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
)

func TestACPDecisionServiceLifecycle(t *testing.T) {
	s := &server{sessions: map[string]*sessionRuntime{}}
	rt := &sessionRuntime{
		id:        "session-1",
		execution: &agentruntime.ExecutionRuntime{},
		decisions: &agentruntime.DecisionService{},
	}
	s.sessions[rt.id] = rt
	if _, err := rt.execution.Begin(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	rt.promptID = "run-1"

	s.registerDecision(rt.id, "approval-1", agentruntime.DecisionApproval)
	if got := len(rt.decisions.Pending()); got != 1 {
		t.Fatalf("pending decisions = %d, want 1", got)
	}
	s.resolveDecision(rt.id, "approval-1", agentruntime.DecisionApproval, "allow-once", "resolved")
	if got := len(rt.decisions.Pending()); got != 0 {
		t.Fatalf("pending decisions after resolve = %d, want 0", got)
	}

	s.registerDecision(rt.id, "question-1", agentruntime.DecisionQuestion)
	s.clearSessionDecisions(rt.id)
	if got := len(rt.decisions.Pending()); got != 0 {
		t.Fatalf("pending decisions after clear = %d, want 0", got)
	}
}

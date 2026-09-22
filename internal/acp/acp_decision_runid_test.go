package acp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
)

func TestACPDecisionUsesCanonicalDurableRunID(t *testing.T) {
	s := &server{sessions: map[string]*sessionRuntime{}}
	rt := &sessionRuntime{
		id:        "session-1",
		promptID:  "prompt-7",
		runID:     "acp_prompt-7",
		decisions: &agentruntime.DecisionService{},
	}
	s.sessions[rt.id] = rt

	s.registerDecision(rt.id, "approval-1", agentruntime.DecisionApproval)
	pending := rt.decisions.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending decisions = %#v, want one decision", pending)
	}
	if pending[0].RunID != rt.runID {
		t.Fatalf("decision RunID = %q, want canonical durable RunID %q", pending[0].RunID, rt.runID)
	}
}

func TestACPDecisionResolveRetainsPendingWhenPersistenceFails(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	settings := &config.Settings{SessionDir: blocked}
	s := &server{settings: settings, sessions: map[string]*sessionRuntime{}}
	rt := &sessionRuntime{
		id:        "session-1",
		promptID:  "prompt-7",
		runID:     "acp_prompt-7",
		decisions: &agentruntime.DecisionService{},
	}
	s.sessions[rt.id] = rt
	s.registerDecision(rt.id, "approval-1", agentruntime.DecisionApproval)

	if err := s.resolveDecision(rt.id, "approval-1", agentruntime.DecisionApproval, "allow-once", "resolved"); err == nil {
		t.Fatal("resolveDecision unexpectedly succeeded when persistence was unavailable")
	}
	if pending := rt.decisions.Pending(); len(pending) != 1 {
		t.Fatalf("persistence failure consumed pending decision: %#v", pending)
	}

	settings.SessionDir = t.TempDir()
	if err := s.resolveDecision(rt.id, "approval-1", agentruntime.DecisionApproval, "allow-once", "resolved"); err != nil {
		t.Fatalf("retry resolveDecision: %v", err)
	}
	if pending := rt.decisions.Pending(); len(pending) != 0 {
		t.Fatalf("successful retry left pending decision: %#v", pending)
	}
}

package tui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/tools"
)

// TestFinishApprovalPersistsResolvedDecision guards the decision ledger: an
// approval answered in the TUI used to bypass the Runtime decision service, so
// the durable record stayed "pending" for the rest of the run (and was later
// rewritten as timed_out). The resolution must be persisted and the request
// consumed.
func TestFinishApprovalPersistsResolvedDecision(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := sess.GetHeader().ID

	run := newTUIRun(sessionID, sessionDir)
	run.execution.SetRunStore(agentruntime.RunStore{SessionDir: sessionDir})
	if _, err := run.execution.Begin(context.Background(), run.id); err != nil {
		t.Fatalf("begin run: %v", err)
	}

	app := NewApp(nil, &provider.Model{ID: "test"}, config.DefaultSettings(), sess, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	app.run = run
	memberAgent := agent.New(agent.Config{ID: "agent-main", Mode: "agent"}, tools.NewRegistry(t.TempDir(), nil))
	app.agent = memberAgent

	// Mirror the event handler: register the decision and bind the resume
	// callback that unblocks the asking agent.
	const approvalID = "approval-agent-main-1"
	if err := run.registerDecision(approvalID, agentruntime.DecisionApproval); err != nil {
		t.Fatalf("register decision: %v", err)
	}
	if err := run.decisions.Bind(approvalID, func(value string) error {
		memberAgent.HandleApprovalResponse(approvalID, value != "false")
		return nil
	}); err != nil {
		t.Fatalf("bind decision: %v", err)
	}
	app.currentApproval = pendingApproval{approvalID: approvalID, toolName: "bash"}
	app.pendingApprovalID = approvalID

	app.finishApproval(true, "approved", false)

	if pending := run.decisions.Pending(); len(pending) != 0 {
		t.Fatalf("pending decisions = %#v, want the approval to be resolved", pending)
	}
	events, err := session.ListSessionRunEvents(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("list session run events: %v", err)
	}
	resolved := 0
	for _, event := range events {
		if event.EventType != "decision_resolved" {
			continue
		}
		var data struct {
			Decision agentruntime.DecisionRecord `json:"decision"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatalf("unmarshal decision event: %v", err)
		}
		if data.Decision.ID == approvalID && data.Decision.Status == "resolved" && data.Decision.Value == "true" {
			resolved++
		}
	}
	if resolved != 1 {
		t.Fatalf("decision_resolved events for %s = %d, want 1 (events=%d)", approvalID, resolved, len(events))
	}
	// The run's terminal cleanup must not rewrite an answered approval.
	run.clearDecisions("timed_out")
	events, err = session.ListSessionRunEvents(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("list session run events after clear: %v", err)
	}
	for _, event := range events {
		if event.EventType != "decision_timed_out" {
			continue
		}
		t.Fatalf("answered approval was rewritten as timed_out: %s", string(event.Data))
	}
}

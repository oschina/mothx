package tui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/session"
)

func TestRecoverTUIOrphanedDecisionsTerminalizesPending(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	run := agentruntime.DurableRun{ID: "tui-orphan", SessionID: mgr.GetHeader().ID, WorkDir: mgr.GetHeader().Cwd, Source: "tui", Model: "m1", Mode: "agent", Status: "running", StartedAt: time.Now()}
	if err := (agentruntime.RunStore{SessionDir: sessionDir}).Create(run); err != nil {
		t.Fatal(err)
	}
	request := agentruntime.DecisionRequest{ID: "question-1", SessionID: run.SessionID, RunID: run.ID, Kind: agentruntime.DecisionQuestion}
	record, err := agentruntime.NewDecisionRequestRecordWithDeadline(request, nil, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"decision": record})
	if _, err := (agentruntime.SessionRunEventSink{SessionDir: sessionDir}).Record(agentruntime.RunEvent{SessionID: run.SessionID, RunID: run.ID, EventType: "decision_pending", Source: "tui", Status: "pending", Data: data}); err != nil {
		t.Fatal(err)
	}
	if err := recoverTUIOrphanedDecisions(sessionDir, run.SessionID); err != nil {
		t.Fatal(err)
	}
	events, err := session.ListSessionRunEvents(sessionDir, run.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	latest := ""
	for _, event := range events {
		var envelope struct {
			Decision agentruntime.DecisionRecord `json:"decision"`
		}
		if json.Unmarshal(event.Data, &envelope) == nil && envelope.Decision.ID == request.ID {
			latest = envelope.Decision.Status
		}
	}
	if latest != "cancelled" {
		t.Fatalf("latest decision status = %q", latest)
	}
	storedRun, err := agentruntime.GetDurableRun(t.Context(), sessionDir, run.ID)
	if err != nil || storedRun == nil || storedRun.Status != "failed" {
		t.Fatalf("stored run = %#v, err=%v", storedRun, err)
	}
}

func TestRecoverTUIOrphanedDecisionsDoesNotReviveResolved(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	run := agentruntime.DurableRun{ID: "tui-resolved", SessionID: mgr.GetHeader().ID, WorkDir: mgr.GetHeader().Cwd, Source: "tui", Status: "running", StartedAt: time.Now()}
	if err := (agentruntime.RunStore{SessionDir: sessionDir}).Create(run); err != nil {
		t.Fatal(err)
	}
	request := agentruntime.DecisionRequest{ID: "approval-1", SessionID: run.SessionID, RunID: run.ID, Kind: agentruntime.DecisionApproval}
	pending, _ := agentruntime.NewDecisionRequestRecord(request, nil)
	resolved, _ := agentruntime.NewDecisionResolutionRecord(request, agentruntime.DecisionResolution{ID: request.ID, Kind: request.Kind, Status: "resolved", Value: "allow"}, nil)
	for i, record := range []agentruntime.DecisionRecord{pending, resolved} {
		data, _ := json.Marshal(map[string]any{"decision": record})
		if _, err := (agentruntime.SessionRunEventSink{SessionDir: sessionDir}).Record(agentruntime.RunEvent{SessionID: run.SessionID, RunID: run.ID, EventType: []string{"decision_pending", "decision_resolved"}[i], Source: "tui", Status: record.Status, Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	if err := recoverTUIOrphanedDecisions(sessionDir, run.SessionID); err != nil {
		t.Fatal(err)
	}
	events, _ := session.ListSessionRunEvents(sessionDir, run.SessionID)
	count := 0
	for _, event := range events {
		if event.EventType == "decision_cancelled" {
			count++
		}
	}
	if count != 0 {
		t.Fatalf("resolved decision was cancelled during recovery")
	}
}

package acp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

func TestACPRehydrateDoesNotReviveResolvedDecision(t *testing.T) {
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	mgr := session.New(t.TempDir(), settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	s := &server{settings: settings}
	s.persistDecisionRecordWithDeadline(mgr.GetHeader().ID, "run-1", "approval-1", agentruntime.DecisionApproval, "pending", "", map[string]any{"tool": "bash"}, time.Now().Add(time.Minute))
	s.persistDecisionRecord(mgr.GetHeader().ID, "run-1", "approval-1", agentruntime.DecisionApproval, "allow-once", "resolved", map[string]any{"value": "allow-once"})
	release, ok := session.TryLockRuntime(settings.GetSessionDir(), mgr.GetHeader().ID)
	if !ok {
		t.Fatal("acquire historical session lease")
	}
	release()
	rt := &sessionRuntime{id: mgr.GetHeader().ID, mgr: mgr, execution: &agentruntime.ExecutionRuntime{}, decisions: &agentruntime.DecisionService{}}
	if err := s.rehydrateSessionDecisions(rt); err != nil {
		t.Fatal(err)
	}
	if pending := rt.decisions.Pending(); len(pending) != 0 {
		t.Fatalf("resolved decision revived: %#v", pending)
	}
}

func TestACPRehydrateTerminalizesOfflinePendingDecision(t *testing.T) {
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	mgr := session.New(t.TempDir(), settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	s := &server{settings: settings}
	s.persistDecisionRecordWithDeadline(mgr.GetHeader().ID, "run-1", "question-1", agentruntime.DecisionQuestion, "pending", "", map[string]any{"question": "continue?"}, time.Now().Add(time.Minute))
	release, ok := session.TryLockRuntime(settings.GetSessionDir(), mgr.GetHeader().ID)
	if !ok {
		t.Fatal("acquire historical session lease")
	}
	release()
	rt := &sessionRuntime{id: mgr.GetHeader().ID, mgr: mgr, execution: &agentruntime.ExecutionRuntime{}, decisions: &agentruntime.DecisionService{}}
	if err := s.rehydrateSessionDecisions(rt); err != nil {
		t.Fatal(err)
	}
	events, err := session.ListSessionRunEvents(settings.GetSessionDir(), mgr.GetHeader().ID)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	for _, ev := range events {
		var envelope struct {
			Decision agentruntime.DecisionRecord `json:"decision"`
		}
		if json.Unmarshal(ev.Data, &envelope) == nil && envelope.Decision.ID == "question-1" {
			status = envelope.Decision.Status
		}
	}
	if status != "cancelled" {
		t.Fatalf("latest status = %q, want cancelled", status)
	}
}

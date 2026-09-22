package tui

import (
	"encoding/json"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/session"
)

func TestTUIRunDecisionRecordPersistence(t *testing.T) {
	sessionDir := t.TempDir()
	sess := session.New(t.TempDir(), sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatal(err)
	}
	run := newTUIRun(sess.GetHeader().ID, sessionDir)
	if err := run.registerDecision("approval-1", agentruntime.DecisionApproval); err != nil {
		t.Fatal(err)
	}
	events, err := session.ListSessionRunEvents(sessionDir, sess.GetHeader().ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "decision_pending" {
		t.Fatalf("events = %#v", events)
	}
	var data struct {
		Decision agentruntime.DecisionRecord `json:"decision"`
	}
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Decision.ID != "approval-1" || data.Decision.Kind != agentruntime.DecisionApproval {
		t.Fatalf("record = %#v", data.Decision)
	}
}

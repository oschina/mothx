package acp

import (
	"encoding/json"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

func TestACPDecisionRecordPersistence(t *testing.T) {
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	mgr := session.New(t.TempDir(), settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	s := &server{settings: settings}
	s.persistDecisionRecord(mgr.GetHeader().ID, "run-1", "question-1", agentruntime.DecisionQuestion, "pending", "", map[string]any{"question": "continue?"})
	events, err := session.ListSessionRunEvents(settings.GetSessionDir(), mgr.GetHeader().ID)
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
	if data.Decision.ID != "question-1" || data.Decision.Kind != agentruntime.DecisionQuestion || data.Decision.Status != "pending" {
		t.Fatalf("record = %#v", data.Decision)
	}
}

package channels

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/session"
)

func TestChannelDecisionRecordPersistence(t *testing.T) {
	sessionDir := t.TempDir()
	sess := session.New(t.TempDir(), sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	channel := &ChannelSession{ID: sess.GetHeader().ID, Platform: "wechat", Mode: "yolo", runID: "run-1"}
	d := &Dispatcher{sessionDir: sessionDir}

	d.persistChannelDecisionRequest(channel, "question-1", agentruntime.DecisionQuestion, map[string]any{"question": "continue?"})
	d.persistChannelDecision(channel, "question-1", agentruntime.DecisionQuestion, "cancelled", "", nil)
	events, err := session.ListSessionRunEvents(sessionDir, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].EventType != "decision_pending" || events[1].EventType != "decision_cancelled" {
		t.Fatalf("events = %#v", events)
	}
	var data struct {
		Decision agentruntime.DecisionRecord `json:"decision"`
	}
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Decision.ID != "question-1" || data.Decision.Kind != agentruntime.DecisionQuestion || data.Decision.Status != "pending" {
		t.Fatalf("decision record = %#v", data.Decision)
	}
	if events[0].Timestamp.IsZero() || events[0].Timestamp.After(time.Now().Add(time.Second)) {
		t.Fatalf("invalid event timestamp: %v", events[0].Timestamp)
	}
}

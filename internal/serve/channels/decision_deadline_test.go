package channels

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/session"
)

func TestChannelDecisionRequestPersistsImmediateDeadline(t *testing.T) {
	d := &Dispatcher{sessionDir: t.TempDir()}
	mgr := session.New(t.TempDir(), d.sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	sess := &ChannelSession{ID: mgr.GetHeader().ID, Platform: "wechat", Mode: "yolo", runID: "run-1"}
	d.persistChannelDecisionRequestWithDeadline(sess, "question-1", agentruntime.DecisionQuestion, map[string]any{"question": "continue?"}, time.Now())
	events, err := session.ListSessionRunEvents(d.sessionDir, sess.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, err=%v", events, err)
	}
	var envelope struct {
		Decision agentruntime.DecisionRecord `json:"decision"`
	}
	if err := json.Unmarshal(events[0].Data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Decision.ExpiresAt.IsZero() {
		t.Fatal("channel decision deadline was not persisted")
	}
	if pending := agentruntime.ReplayDecisions([]agentruntime.DecisionRecord{envelope.Decision}); len(pending) != 0 {
		t.Fatalf("immediate channel decision remained pending: %#v", pending)
	}
}

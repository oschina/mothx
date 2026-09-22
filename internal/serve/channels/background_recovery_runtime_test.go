package channels

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func TestChannelRecoveryUsesRuntimeDeliveryProjection(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	entryID, err := mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "recovered"}}))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sink := agentruntime.SessionRunEventSink{SessionDir: sessionDir}
	for _, event := range []agentruntime.RunEvent{
		{SessionID: mgr.GetHeader().ID, RunID: "run-1", EventType: "finished", Source: "channel:wechat", Status: "completed", Timestamp: now, Data: json.RawMessage(`{"channelDeliveryPending":true,"assistantEntryId":"` + entryID + `"}`)},
	} {
		if _, err := sink.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	d := &Dispatcher{sessionDir: sessionDir}
	channel := &ChannelSession{Platform: "wechat", UserID: "user", Manager: mgr}
	var deliveries []string
	d.reconcileCompletedBackgroundRun(channel, func(text string) { deliveries = append(deliveries, text) })
	if len(deliveries) != 1 || deliveries[0] != "recovered" {
		t.Fatalf("deliveries = %#v", deliveries)
	}
	events, err := session.ListSessionRunEvents(sessionDir, mgr.GetHeader().ID)
	if err != nil {
		t.Fatal(err)
	}
	pending := agentruntime.ReplayDeliveries(events)
	if len(pending) != 0 {
		t.Fatalf("pending after reconciliation = %#v", pending)
	}
}

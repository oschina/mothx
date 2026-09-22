package agentruntime

import (
	"testing"

	"github.com/oschina/mothx/internal/session"
)

func TestReplayDeliveriesPendingAndReconciled(t *testing.T) {
	events := []session.SessionRunEvent{
		{SessionID: "session-1", RunID: "run-1", EventType: "finished", Status: "completed", Data: []byte(`{"channelDeliveryPending":true,"assistantEntryId":"entry-1"}`)},
		{SessionID: "session-1", RunID: "run-2", EventType: "finished", Status: "completed", Data: []byte(`{"channelDeliveryPending":true,"assistantEntryId":"entry-2"}`)},
		{SessionID: "session-1", RunID: "run-2", EventType: "channel_delivery_reconciled", Status: "delivered"},
	}
	pending := ReplayDeliveries(events)
	if len(pending) != 1 {
		t.Fatalf("pending = %#v", pending)
	}
	if pending["run-1"].AssistantEntry != "entry-1" {
		t.Fatalf("run-1 = %#v", pending["run-1"])
	}
}

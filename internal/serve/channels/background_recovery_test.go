package channels

import (
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func TestReconcileCompletedBackgroundRunAfterRestart(t *testing.T) {
	sessionDir := t.TempDir()
	sess := session.New(t.TempDir(), sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	entryID, err := sess.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "background answer"}}))
	if err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	now := time.Now()
	if _, err := (agentruntime.SessionRunEventSink{SessionDir: sessionDir}).Record(agentruntime.RunEvent{
		SessionID: sess.GetHeader().ID, RunID: "run-channel-restart", EventType: "tool_progress", Source: "channel:wechat", Status: "completed", Timestamp: now,
		Data: []byte(`{"tool":"read","status":"completed","summary":"file read"}`),
	}); err != nil {
		t.Fatalf("save tool progress event: %v", err)
	}
	if _, err := (agentruntime.SessionRunEventSink{SessionDir: sessionDir}).Record(agentruntime.RunEvent{
		SessionID: sess.GetHeader().ID, RunID: "run-channel-restart", EventType: "finished", Source: "channel:wechat", Status: "completed", Timestamp: now.Add(time.Millisecond),
		Data: []byte(`{"channelDeliveryPending":true,"assistantEntryId":"` + entryID + `"}`),
	}); err != nil {
		t.Fatalf("save pending event: %v", err)
	}
	d := &Dispatcher{sessionDir: sessionDir}
	channel := &ChannelSession{Platform: "wechat", UserID: "user", Manager: sess}
	var deliveries []string
	d.reconcileCompletedBackgroundRun(channel, func(text string) { deliveries = append(deliveries, text) })
	if len(deliveries) != 2 || !strings.Contains(deliveries[0], "read") || !strings.Contains(deliveries[1], "background answer") {
		t.Fatalf("deliveries = %#v", deliveries)
	}
	d.reconcileCompletedBackgroundRun(channel, func(text string) { deliveries = append(deliveries, text) })
	if len(deliveries) != 2 {
		t.Fatalf("delivery repeated after reconciliation: %#v", deliveries)
	}
}

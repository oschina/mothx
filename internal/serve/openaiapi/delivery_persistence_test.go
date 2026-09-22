package openaiapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/session"
)

func TestFinalizeChannelBackgroundWritesRuntimeDeliveryPayload(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("channel-delivery-finalize", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	runID := "channel-delivery-run"
	now := time.Now()
	if err := session.SaveSessionRun(srv.settings.GetSessionDir(), session.SessionRun{
		ID: runID, SessionID: sess.ID, WorkDir: sess.WorkDir, Source: "channel:wechat",
		Model: "test", Mode: "yolo", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.SaveResponseTurn(srv.settings.GetSessionDir(), session.ResponseTurn{
		SessionID: sess.ID, LocalTurnID: runID, ResponseID: "response-1", Provider: "openai",
		API: "openai-responses", Model: "test", StateMode: "replay", Status: "completed",
		ResponseSummary: json.RawMessage(`{"usage":{"totalTokens":1}}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.SaveResponseItem(srv.settings.GetSessionDir(), session.ResponseItemArchive{
		SessionID: sess.ID, LocalTurnID: runID, ResponseID: "response-1", ItemID: "message-1",
		ItemType: "message", SanitizedJSON: json.RawMessage(`{"type":"message","content":[{"type":"output_text","text":"channel result"}]}`),
	}); err != nil {
		t.Fatal(err)
	}
	status := srv.finalizeResponsesBackgroundResult(sess, runID, "test", "yolo", &session.ResponseRun{
		SessionID: sess.ID, LocalRunID: "remote-run", LocalTurnID: runID, ResponseID: "response-1", State: "completed",
	}, false)
	if status != "completed" {
		t.Fatalf("status = %q", status)
	}
	events, err := session.ListSessionRunEvents(srv.settings.GetSessionDir(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending := agentruntime.ReplayDeliveries(events)
	record, ok := pending[runID]
	if !ok || record.AssistantEntry == "" || record.Source != "channel:wechat" {
		t.Fatalf("pending deliveries = %#v", pending)
	}
}

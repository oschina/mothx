package acp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
)

// TestWorkerMessagesProjectsLogAndPendingQuestions proves the ACP adapter is a
// read-only projection of Runtime-owned state: the JSONL evidence log and the
// durable decision ledger, with no token, endpoint, or mutation surface.
func TestWorkerMessagesProjectsLogAndPendingQuestions(t *testing.T) {
	output := &syncedBuffer{}
	srv, sessionID := newRouterManageFixture(t, output)
	rt := srv.sessions[sessionID]

	log, err := rt.runtime.WorkerMessageLog()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := log.Create(agentruntime.WorkerMessage{
		SessionID: sessionID, ChildID: "child-1", Kind: agentruntime.WorkerMessageFinding, Body: "found a race",
	}); err != nil {
		t.Fatal(err)
	}

	request := agentruntime.DecisionRequest{
		ID: "wq_test", RunID: "run-1", SessionID: sessionID, Kind: agentruntime.DecisionQuestion,
	}
	sessionDir := rt.mgr.GetSessionDir()
	if _, err := agentruntime.RecordDecisionEvent(agentruntime.SessionRunEventSink{SessionDir: sessionDir}, agentruntime.DecisionTransition{
		Request: request, Status: agentruntime.DecisionStatusPending,
		Payload:   map[string]any{"question": "which file?", "options": []string{"a.go", "b.go"}, "childId": "child-1"},
		ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	output.Reset()
	params, _ := json.Marshal(map[string]any{"sessionId": sessionID})
	srv.handleWorkerMessages(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "mothx/worker/messages", Params: params})
	var response map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var candidate map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &candidate); err != nil {
			continue
		}
		if candidate["id"] != nil {
			response = candidate
			break
		}
	}
	if response == nil {
		t.Fatalf("no response: %q", output.String())
	}
	result := manageFixtureResult(t, response)
	messages, _ := result["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", result["messages"])
	}
	message, _ := messages[0].(map[string]any)
	if message["kind"] != agentruntime.WorkerMessageFinding || message["body"] != "found a race" {
		t.Fatalf("message projection = %#v", message)
	}
	questions, _ := result["pendingQuestions"].([]any)
	if len(questions) != 1 {
		t.Fatalf("pendingQuestions = %#v", result["pendingQuestions"])
	}
	question, _ := questions[0].(map[string]any)
	if question["question"] != "which file?" || question["childId"] != "child-1" {
		t.Fatalf("question projection = %#v", question)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"Bearer ", "Authorization"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("projection leaked %q: %s", forbidden, encoded)
		}
	}
}

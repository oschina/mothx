package agentruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/config"
)

func TestACPWorkerPoolUsesRemoteWebSocketProfileWithoutLauncher(t *testing.T) {
	var connections atomic.Int32
	var sessionSeq atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connections.Add(1)
		defer conn.Close()
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request acpclient.Message
			if err := json.Unmarshal(frame, &request); err != nil {
				continue
			}
			switch request.Method {
			case "initialize":
				writeRemoteACPResponse(t, conn, request.ID, acpclient.InitializeResult{ProtocolVersion: acpclient.ProtocolVersion})
			case "session/new":
				id := "remote-session-" + strconv.Itoa(int(sessionSeq.Add(1)))
				writeRemoteACPResponse(t, conn, request.ID, acpclient.NewSessionResult{SessionID: id})
			case "session/prompt":
				sessionID := acpclient.DecodeSessionID(request.Params)
				update := map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sessionID,
						"update": map[string]any{
							"sessionUpdate": "agent_message_chunk",
							"content":       map[string]any{"type": "text", "text": "output:" + sessionID},
						},
					},
				}
				encoded, _ := json.Marshal(update)
				_ = conn.WriteMessage(websocket.TextMessage, encoded)
				writeRemoteACPResponse(t, conn, request.ID, acpclient.PromptResult{StopReason: "end_turn"})
			}
		}
	}))
	defer server.Close()

	profile := config.ACPAgentProfile{
		ID:        "remote-helper",
		Transport: string(config.ACPAgentTransportWebSocket),
		URL:       "ws" + strings.TrimPrefix(server.URL, "http"),
		Enabled:   true, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}
	workDir := t.TempDir()
	pool := newACPConnectionPool()
	defer pool.Close()

	run := func() ACPWorkerResult {
		result, err := (ACPWorkerBridge{}).Run(context.Background(), ACPWorkerInvocation{
			Profile: profile, WorkDir: workDir, pool: pool,
			Contract: SubagentTaskContract{
				Objective: "inspect this task", Role: SubagentRoleInvestigate,
				ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
				TargetProfile:        profile.ID,
			},
		})
		if err != nil {
			t.Errorf("bridge Run: %v", err)
		}
		return result
	}

	results := make(chan ACPWorkerResult, 2)
	for range 2 {
		go func() { results <- run() }()
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case result := <-results:
			if result.SessionID == "" || result.Output != "output:"+result.SessionID {
				t.Fatalf("result = %#v", result)
			}
			seen[result.SessionID] = true
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for remote WebSocket workers")
		}
	}
	if len(seen) != 2 {
		t.Fatalf("sessions = %#v, want two isolated sessions", seen)
	}
	if connections.Load() != 1 {
		t.Fatalf("WebSocket connections = %d, want one reused connection", connections.Load())
	}
}

func TestACPWorkerPoolSharesOneConnectionAcrossReadAndWriteSessions(t *testing.T) {
	var connections atomic.Int32
	var writeAdvertised atomic.Bool
	var sessionSeq atomic.Int32
	var responsesMu sync.Mutex
	writeResponses := map[string]int{}
	workDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connections.Add(1)
		defer conn.Close()
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request acpclient.Message
			if err := json.Unmarshal(frame, &request); err != nil {
				continue
			}
			switch request.Method {
			case "initialize":
				var params struct {
					ClientCapabilities map[string]any `json:"clientCapabilities"`
				}
				_ = json.Unmarshal(request.Params, &params)
				if fs, ok := params.ClientCapabilities["fs"].(map[string]any); ok {
					if enabled, _ := fs["writeTextFile"].(bool); enabled {
						writeAdvertised.Store(true)
					}
				}
				writeRemoteACPResponse(t, conn, request.ID, acpclient.InitializeResult{ProtocolVersion: acpclient.ProtocolVersion})
			case "session/new":
				id := "shared-session-" + strconv.Itoa(int(sessionSeq.Add(1)))
				writeRemoteACPResponse(t, conn, request.ID, acpclient.NewSessionResult{SessionID: id})
			case "session/prompt":
				sessionID := acpclient.DecodeSessionID(request.Params)
				requestParams, _ := json.Marshal(map[string]any{
					"sessionId": sessionID,
					"path":      workDir + "/src/from-agent.txt",
					"content":   "agent write",
				})
				reverse, _ := json.Marshal(acpclient.Message{
					JSONRPC: "2.0",
					ID:      json.RawMessage(strconv.Quote("write-" + sessionID)),
					Method:  "fs/write_text_file",
					Params:  requestParams,
				})
				if err := conn.WriteMessage(websocket.TextMessage, reverse); err == nil {
					_, responseFrame, readErr := conn.ReadMessage()
					code := 0
					if readErr == nil {
						var response acpclient.Message
						if json.Unmarshal(responseFrame, &response) == nil && response.Error != nil {
							code = response.Error.Code
						}
					}
					responsesMu.Lock()
					writeResponses[sessionID] = code
					responsesMu.Unlock()
				}
				params, _ := json.Marshal(map[string]any{
					"sessionId": sessionID,
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"type": "text", "text": "output:" + sessionID},
					},
				})
				update, _ := json.Marshal(acpclient.Message{
					JSONRPC: "2.0",
					Method:  "session/update",
					Params:  params,
				})
				_ = conn.WriteMessage(websocket.TextMessage, update)
				writeRemoteACPResponse(t, conn, request.ID, acpclient.PromptResult{StopReason: "end_turn"})
			}
		}
	}))
	defer server.Close()

	profile := config.ACPAgentProfile{
		ID:        "shared-helper",
		Transport: string(config.ACPAgentTransportWebSocket),
		URL:       "ws" + strings.TrimPrefix(server.URL, "http"),
		Enabled:   true, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}
	pool := newACPConnectionPool()
	defer pool.Close()

	readInvocation := ACPWorkerInvocation{
		Profile: profile, WorkDir: workDir, pool: pool, ReadTextFile: true,
		Contract: SubagentTaskContract{
			Objective: "inspect this task", Role: SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
			TargetProfile:        profile.ID,
		},
	}
	readInvocation.HandleRequest = func(_ context.Context, message acpclient.Message) (any, *acpclient.RPCError) {
		if message.Method != "fs/write_text_file" {
			return nil, &acpclient.RPCError{Code: -32601, Message: "unexpected method"}
		}
		return nil, &acpclient.RPCError{Code: -32601, Message: "fs/write_text_file is not available for this task"}
	}
	writeInvocation := ACPWorkerInvocation{
		Profile: profile, WorkDir: workDir, pool: pool, ReadTextFile: true,
		Contract: SubagentTaskContract{
			Objective: "implement this task", Role: SubagentRoleImplement,
			WriteScope: []string{"src"}, Verification: "go test ./...",
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableChangedFiles},
			TargetProfile:        profile.ID,
		},
	}
	writeInvocation.HandleRequest = func(_ context.Context, message acpclient.Message) (any, *acpclient.RPCError) {
		if message.Method != "fs/write_text_file" {
			return nil, &acpclient.RPCError{Code: -32601, Message: "unexpected method"}
		}
		return nil, nil
	}
	if acpConnectionKey(readInvocation, nil) != acpConnectionKey(writeInvocation, nil) {
		t.Fatal("read and write sessions with the same profile/workdir did not share a connection key")
	}

	seen := map[string]bool{}
	// The two turns run sequentially so the fake server can read exactly one
	// reverse-request response while each prompt is in flight; the connection
	// still has to be reused across the two isolated sessions.
	for _, run := range []struct {
		role       string
		invocation ACPWorkerInvocation
	}{{"read", readInvocation}, {"write", writeInvocation}} {
		result, err := (ACPWorkerBridge{}).Run(context.Background(), run.invocation)
		if err != nil {
			t.Fatalf("%s bridge Run: %v", run.role, err)
		}
		if result.SessionID == "" || result.Output != "output:"+result.SessionID {
			t.Fatalf("%s result = %#v", run.role, result)
		}
		responsesMu.Lock()
		code := writeResponses[result.SessionID]
		responsesMu.Unlock()
		if run.role == "read" && code != -32601 {
			t.Fatalf("read-only session write response = %d, want -32601", code)
		}
		if run.role == "write" && code != 0 {
			t.Fatalf("write-role session write response = %d, want success", code)
		}
		seen[result.SessionID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("sessions = %#v, want two isolated sessions", seen)
	}
	if connections.Load() != 1 {
		t.Fatalf("connections = %d, want one shared initialized connection", connections.Load())
	}
	if !writeAdvertised.Load() {
		t.Fatal("shared connection did not advertise the Runtime-enforced write bridge")
	}
}

func writeRemoteACPResponse(t *testing.T, conn *websocket.Conn, id json.RawMessage, result any) {
	t.Helper()
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	encoded, err := json.Marshal(acpclient.Message{JSONRPC: "2.0", ID: id, Result: encodedResult})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, encoded); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

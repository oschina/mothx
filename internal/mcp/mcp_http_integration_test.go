package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/tools"
)

func TestConnectMCPServersHTTPRegistersAndExecutes(t *testing.T) {
	var mu sync.Mutex
	var sampled bool
	var notified bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req RPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad json"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"protocolVersion": mcpProtocolVersion},
			})
		case "notifications/initialized":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": map[string]any{}})
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"tools": []map[string]any{
						{
							"name":        "echo",
							"description": "echo tool",
							"inputSchema": map[string]any{"type": "object"},
						},
					},
				},
			})
		case "resources/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"resources": []map[string]any{
						{"uri": "file://README.md", "name": "readme"},
					},
				},
			})
		case "prompts/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"prompts": []map[string]any{
						{"name": "summarize", "description": "summarize prompt"},
					},
				},
			})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "ok"}},
				},
			})
		case "resources/read":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"contents": []map[string]any{{"type": "text", "text": "resource-body"}},
				},
			})
		case "prompts/get":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"description": "prompt-desc",
					"messages": []map[string]any{
						{"role": "user", "content": map[string]any{"type": "text", "text": "prompt-text"}},
					},
				},
			})
		case "sampling/createMessage":
			mu.Lock()
			sampled = true
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "sampled"}},
				},
			})
		case "notifications/progress":
			mu.Lock()
			notified = true
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": map[string]any{}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	registry := tools.NewRegistry(tmp, sandbox.NewNoneSandbox())
	registry.RegisterDefaults()

	clients, err := ConnectServers(context.Background(), []ServerConfig{
		{Name: "mock-http", Type: "http", URL: srv.URL},
	}, registry, Callbacks{
		OnNotification: func(serverName, method string, params json.RawMessage) {
			if serverName == "mock-http" && method == "notifications/progress" {
				mu.Lock()
				notified = true
				mu.Unlock()
			}
		},
		OnSamplingCreateMessage: func(ctx context.Context, serverName string, params json.RawMessage) (json.RawMessage, *RPCError) {
			if serverName != "mock-http" {
				return nil, &RPCError{Code: -32000, Message: "bad server"}
			}
			mu.Lock()
			sampled = true
			mu.Unlock()
			return json.RawMessage(`{"content":[{"type":"text","text":"sampled"}]}`), nil
		},
	})
	if err != nil {
		t.Fatalf("ConnectServers failed: %v", err)
	}
	defer CloseClients(clients)
	if len(clients) != 1 {
		t.Fatalf("expected 1 client, got %d", len(clients))
	}

	var gotTool, gotResource, gotPrompt tools.Tool
	for _, tdef := range registry.All() {
		switch {
		case strings.Contains(tdef.Name(), "_echo"):
			gotTool = tdef
		case strings.Contains(tdef.Name(), "_resource_"):
			gotResource = tdef
		case strings.Contains(tdef.Name(), "_prompt_"):
			gotPrompt = tdef
		}
	}
	if gotTool == nil || gotResource == nil || gotPrompt == nil {
		t.Fatalf("expected tool/resource/prompt registrations, got tool=%v resource=%v prompt=%v", gotTool != nil, gotResource != nil, gotPrompt != nil)
	}

	if _, err := gotTool.Execute(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("tool execute failed: %v", err)
	}
	resOut, err := gotResource.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("resource execute failed: %v", err)
	}
	if !strings.Contains(resOut.Text, "resource-body") {
		t.Fatalf("unexpected resource output: %q", resOut.Text)
	}
	promptOut, err := gotPrompt.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("prompt execute failed: %v", err)
	}
	if !strings.Contains(promptOut.Text, "prompt-text") {
		t.Fatalf("unexpected prompt output: %q", promptOut.Text)
	}

	clients[0].handleInboundRequest(RPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "sampling/createMessage",
		Params:  json.RawMessage(`{"messages":[{"role":"user","content":"hi"}]}`),
	})
	clients[0].handleInboundRequest(RPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/progress",
		Params:  json.RawMessage(`{"progress":0.5}`),
	})
	mu.Lock()
	wasSampled := sampled
	wasNotified := notified
	mu.Unlock()
	if !wasSampled {
		t.Fatal("expected sampling callback to be triggered")
	}
	if !wasNotified {
		t.Fatal("expected notification callback to be triggered")
	}
}

func TestMCPHTTPSessionIDHeaderRoundTrip(t *testing.T) {
	const sid = "sid-123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req RPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("Mcp-Session-Id") == "" {
			w.Header().Set("Mcp-Session-Id", sid)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  map[string]any{"tools": []any{}},
		})
	}))
	defer srv.Close()

	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.RegisterDefaults()
	clients, err := ConnectServers(context.Background(), []ServerConfig{
		{Name: "sid-server", Type: "http", URL: srv.URL},
	}, registry, Callbacks{})
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer CloseClients(clients)
	if clients[0].sessionID != sid {
		t.Fatalf("expected session id %q, got %q", sid, clients[0].sessionID)
	}
}

func TestMCPHTTPRejectsMismatchedResponseID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		name:       "wrong-id",
		transport:  "http",
		httpClient: srv.Client(),
		httpURL:    srv.URL,
		ctx:        ctx,
		cancel:     cancel,
	}
	defer client.Close()

	_, err := client.callHTTP(context.Background(), "tools/list", nil)
	if err == nil || !strings.Contains(err.Error(), "does not match request id") {
		t.Fatalf("callHTTP error = %v, want mismatched response ID", err)
	}
}

func TestMCPHTTPPropagatesRuntimeOperationID(t *testing.T) {
	const operationID = "tool:stable-mcp-operation"
	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Idempotency-Key")
		defer r.Body.Close()
		var req RPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{name: "operation-id", transport: "http", httpClient: srv.Client(), httpURL: srv.URL, ctx: ctx, cancel: cancel}
	defer client.Close()
	if _, err := client.callHTTP(tools.ContextWithOperationID(context.Background(), operationID), "tools/call", map[string]any{"name": "write"}); err != nil {
		t.Fatalf("callHTTP: %v", err)
	}
	select {
	case got := <-seen:
		if got != operationID {
			t.Fatalf("Idempotency-Key = %q, want %q", got, operationID)
		}
	case <-time.After(time.Second):
		t.Fatal("MCP request was not observed")
	}
}

func TestMCPHTTPCloseCancelsInflightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		name:       "cancel-http",
		transport:  "http",
		httpClient: srv.Client(),
		httpURL:    srv.URL,
		ctx:        ctx,
		cancel:     cancel,
	}
	done := make(chan error, 1)
	go func() {
		_, err := client.callHTTP(context.Background(), "tools/list", nil)
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("HTTP request did not start")
	}
	client.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("in-flight request succeeded after client close")
		}
	case <-time.After(time.Second):
		t.Fatal("client close did not cancel in-flight HTTP request")
	}
}

func TestConnectMCPServersReturnsResourceDiscoveryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req RPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": mcpProtocolVersion}
		case "tools/list":
			response["result"] = map[string]any{"tools": []any{}}
		case "resources/list":
			response["error"] = map[string]any{"code": -32000, "message": "resource backend unavailable"}
		default:
			response["result"] = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	_, err := ConnectServers(context.Background(), []ServerConfig{
		{Name: "discovery-error", Type: "http", URL: srv.URL},
	}, registry, Callbacks{})
	if err == nil || !strings.Contains(err.Error(), "resource backend unavailable") {
		t.Fatalf("ConnectServers error = %v, want resource discovery failure", err)
	}
}

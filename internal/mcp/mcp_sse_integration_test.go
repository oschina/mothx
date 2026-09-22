package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/tools"
)

func TestMCPServerSSECallFlow(t *testing.T) {
	var (
		mu          sync.Mutex
		messageReqs []RPCRequest
		streamW     http.ResponseWriter
		flusher     http.Flusher
	)

	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Mcp-Session-Id", "sse-sid")
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("response writer does not support flush")
		}
		mu.Lock()
		streamW = w
		flusher = f
		mu.Unlock()
		<-r.Context().Done()
	}))
	defer stream.Close()

	message := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req RPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "bad json"})
			return
		}
		mu.Lock()
		messageReqs = append(messageReqs, req)
		readyW := streamW
		readyF := flusher
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "sse-sid")

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
						{"name": "echo", "description": "sse echo", "inputSchema": map[string]any{"type": "object"}},
					},
				},
			})
		case "resources/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"resources": []map[string]any{}},
			})
		case "prompts/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"prompts": []map[string]any{}},
			})
		case "tools/call":
			if readyW != nil && readyF != nil {
				writeSSEJSON(readyW, readyF, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"content": []map[string]any{{"type": "text", "text": "sse-ok"}},
					},
				})
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": map[string]any{}})
		}
	}))
	defer message.Close()

	reg := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	reg.RegisterDefaults()
	clients, err := ConnectServers(context.Background(), []ServerConfig{
		{
			Name:       "sse-server",
			Type:       "sse",
			URL:        stream.URL,
			MessageURL: message.URL,
		},
	}, reg, Callbacks{})
	if err != nil {
		t.Fatalf("ConnectServers sse failed: %v", err)
	}
	defer CloseClients(clients)

	var echoTool tools.Tool
	for _, tt := range reg.All() {
		if strings.Contains(tt.Name(), "_echo") {
			echoTool = tt
			break
		}
	}
	if echoTool == nil {
		t.Fatal("expected sse echo tool registration")
	}
	out, err := echoTool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("sse tool execute failed: %v", err)
	}
	if !strings.Contains(out.Text, "sse-ok") {
		t.Fatalf("unexpected sse tool output: %q", out.Text)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(messageReqs) == 0 {
		t.Fatal("expected posts to messageUrl")
	}
	if clients[0].sessionID != "sse-sid" {
		t.Fatalf("expected sessionID from stream/header, got %q", clients[0].sessionID)
	}
}

func TestMCPServerSSENotificationCallback(t *testing.T) {
	var (
		mu         sync.Mutex
		gotMethods []string
		readyOnce  sync.Once
	)
	streamReady := make(chan struct{})
	notifyCh := make(chan map[string]any, 1)
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, _ := w.(http.Flusher)
		readyOnce.Do(func() { close(streamReady) })
		select {
		case msg := <-notifyCh:
			writeSSEJSON(w, f, msg)
			<-r.Context().Done()
		case <-r.Context().Done():
		}
	}))
	defer stream.Close()

	message := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req RPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		// Keep initialize/list calls deterministic via direct response to avoid stream-ready races.
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"protocolVersion": mcpProtocolVersion},
			})
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"tools": []any{}},
			})
		case "resources/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"resources": []any{}},
			})
		case "prompts/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"prompts": []any{}},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": map[string]any{}})
		}
	}))
	defer message.Close()

	reg := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	reg.RegisterDefaults()
	clients, err := ConnectServers(context.Background(), []ServerConfig{
		{Name: "notify-sse", Type: "sse", URL: stream.URL, MessageURL: message.URL},
	}, reg, Callbacks{
		OnNotification: func(serverName, method string, params json.RawMessage) {
			mu.Lock()
			defer mu.Unlock()
			gotMethods = append(gotMethods, method)
		},
	})
	if err != nil {
		t.Fatalf("connect sse failed: %v", err)
	}
	defer CloseClients(clients)

	select {
	case <-streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting sse stream ready")
	}
	notifyCh <- map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
		"params":  map[string]any{"progress": 0.5},
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		ok := len(gotMethods) > 0
		mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting notification callback")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotMethods[0] != "notifications/progress" {
		t.Fatalf("unexpected notification method: %v", gotMethods)
	}
}

func writeSSEJSON(w http.ResponseWriter, fl http.Flusher, v any) {
	b, _ := json.Marshal(v)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
	fl.Flush()
}

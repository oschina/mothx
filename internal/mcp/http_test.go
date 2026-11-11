package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type httpTestHandler struct {
	tools []ServerTool
	calls []string
}

func (h *httpTestHandler) ListTools(context.Context) ([]ServerTool, error) {
	return h.tools, nil
}

func (h *httpTestHandler) CallTool(_ context.Context, name string, args json.RawMessage) (ServerToolResult, error) {
	h.calls = append(h.calls, name+"|"+string(args))
	return ServerToolResult{Content: []ServerContent{{Type: "text", Text: "ok"}}}, nil
}

func (h *httpTestHandler) ServerName() string { return "mothx-worker-test" }

func TestHTTPHandlerSupportsMCPLifecycle(t *testing.T) {
	handler := &httpTestHandler{tools: []ServerTool{{Name: "worker_mgr", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	server := httptest.NewServer(NewHTTPHandler(handler))
	defer server.Close()

	initialize := postMCP(t, server.URL, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if !strings.Contains(initialize, `"mothx-worker-test"`) {
		t.Fatalf("initialize response = %s", initialize)
	}

	list := postMCP(t, server.URL, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if !strings.Contains(list, `"worker_mgr"`) {
		t.Fatalf("tools/list response = %s", list)
	}

	call := postMCP(t, server.URL, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"worker_mgr","arguments":{"action":"context"}}}`)
	if !strings.Contains(call, `"ok"`) {
		t.Fatalf("tools/call response = %s", call)
	}
	if len(handler.calls) != 1 || !strings.HasPrefix(handler.calls[0], "worker_mgr|") {
		t.Fatalf("handler calls = %#v", handler.calls)
	}
}

func TestHTTPHandlerNotificationAndProtocolErrors(t *testing.T) {
	server := httptest.NewServer(NewHTTPHandler(&httpTestHandler{}))
	defer server.Close()

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("notification status = %d, want 202", response.StatusCode)
	}

	bad := postMCPStatus(t, server.URL, `not-json`, http.StatusBadRequest)
	if !strings.Contains(bad, `"parse error"`) {
		t.Fatalf("parse error body = %s", bad)
	}
}

func TestHTTPHandlerRejectsSSEAndNonJSON(t *testing.T) {
	server := httptest.NewServer(NewHTTPHandler(&httpTestHandler{}))
	defer server.Close()

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotAcceptable {
		t.Fatalf("SSE-only status = %d, want 406", response.StatusCode)
	}

	// A normal Streamable HTTP Accept advertises both; serving JSON is the
	// documented compatibility choice.
	request, _ = http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"initialize"}`))
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("mixed Accept status = %d, want 200 JSON", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "text/plain")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("non-JSON status = %d, want 415", response.StatusCode)
	}

	response, err = http.Get(server.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", response.StatusCode)
	}
}

func TestHTTPHandlerRejectsOversizedBody(t *testing.T) {
	server := httptest.NewServer(NewHTTPHandler(&httpTestHandler{}))
	defer server.Close()
	huge := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"pad":"` +
		strings.Repeat("a", maxHTTPRequestBody+10) + `"}}`
	body := postMCPStatus(t, server.URL, huge, http.StatusRequestEntityTooLarge)
	if body == "" {
		t.Fatal("oversized request was accepted")
	}
}

func postMCP(t *testing.T, baseURL, body string) string {
	t.Helper()
	return postMCPStatus(t, baseURL, body, http.StatusOK)
}

func postMCPStatus(t *testing.T, baseURL, body string, want int) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var builder strings.Builder
	buffer := make([]byte, 4096)
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			builder.Write(buffer[:count])
		}
		if readErr != nil {
			break
		}
	}
	if response.StatusCode != want {
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, want, builder.String())
	}
	return builder.String()
}

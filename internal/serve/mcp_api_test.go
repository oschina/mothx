package serve

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestMCPConfigHandlerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	rt := &channelRuntime{}

	put := httptest.NewRequest(http.MethodPut, "/api/mcp", bytes.NewBufferString(`{"mcpServers":[{"name":" local ","command":"server"}]}`))
	putResult := httptest.NewRecorder()
	rt.handleMCPConfigAtPath(putResult, put, path, nil)
	if putResult.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", putResult.Code, putResult.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/api/mcp", nil)
	getResult := httptest.NewRecorder()
	rt.handleMCPConfigAtPath(getResult, get, path, nil)
	if getResult.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", getResult.Code, getResult.Body.String())
	}
	if got, want := getResult.Body.String(), `{"mcpServers":[{"name":"local","type":"stdio","command":"server"}]}`+"\n"; got != want {
		t.Fatalf("GET body = %s, want %s", got, want)
	}
}

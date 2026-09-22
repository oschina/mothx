package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
	channels "github.com/oschina/mothx/internal/serve/channels"
	"github.com/oschina/mothx/internal/session"
)

func TestChannelToolsHTTPRoundTripUsesCatalogAndGeneration(t *testing.T) {
	sessionDir := t.TempDir()
	bound, err := session.CreateBound(t.TempDir(), sessionDir, "wechat", "tools-http")
	if err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	dispatcher, err := channels.NewDispatcher(channels.DefaultConfig(), settings, "test", nil, nil)
	if err != nil {
		t.Fatalf("create dispatcher: %v", err)
	}
	dispatcher.SetIdentityLocks(session.NewIdentityLocks())
	rt := &channelRuntime{cfg: DefaultConfig(), dispatcher: dispatcher, sessionDir: sessionDir, identityMux: session.NewIdentityLocks()}
	handler := rt.handleSessionByID(&fakeActiveSessionManager{})
	path := "/api/sessions/" + bound.GetHeader().ID + "/channel-tools"
	getReq := httptest.NewRequest(http.MethodGet, path, nil)
	getResp := httptest.NewRecorder()
	handler.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET channel-tools status = %d, body = %s", getResp.Code, getResp.Body.String())
	}
	var state channelToolsResponse
	if err := json.Unmarshal(getResp.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Generation != 0 || state.Platform != "wechat" || len(state.Tools) == 0 {
		t.Fatalf("initial tool state = %#v", state)
	}
	selections := make([]channelToolSelection, 0, len(state.Tools))
	for _, tool := range state.Tools {
		selections = append(selections, channelToolSelection{Name: tool.Name, Enabled: tool.Available && tool.RequestedEnabled})
	}
	body, _ := json.Marshal(map[string]any{"tools": selections})
	putReq := httptest.NewRequest(http.MethodPut, path, strings.NewReader(string(body)))
	putResp := httptest.NewRecorder()
	handler.ServeHTTP(putResp, putReq)
	if putResp.Code != http.StatusOK {
		t.Fatalf("PUT channel-tools status = %d, body = %s", putResp.Code, putResp.Body.String())
	}
	var updated channelToolsResponse
	if err := json.Unmarshal(putResp.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Generation != 1 || updated.SessionID != bound.GetHeader().ID {
		t.Fatalf("updated tool state = %#v", updated)
	}
}

func TestChannelToolsStateReportsNextRunWhileRuntimeLockIsHeld(t *testing.T) {
	sessionDir := t.TempDir()
	rt := &channelRuntime{sessionDir: sessionDir}
	release := session.LockRuntime(sessionDir, "active-session")
	defer release()
	if got := rt.channelToolsAppliesTo("active-session"); got != "next_run" {
		t.Fatalf("appliesTo = %q, want next_run", got)
	}
}

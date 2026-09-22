package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func TestLifecycleHTTPRejectsBoundDeleteAndTransfersBinding(t *testing.T) {
	sessionDir := t.TempDir()
	bound, err := session.CreateBound(t.TempDir(), sessionDir, "wechat", "identity-http")
	if err != nil {
		t.Fatal(err)
	}
	target := session.New(t.TempDir(), sessionDir)
	if err := target.InitWithID("target-http"); err != nil {
		t.Fatal(err)
	}
	rt := &channelRuntime{cfg: DefaultConfig(), sessionDir: sessionDir, identityMux: session.NewIdentityLocks()}
	fake := &fakeActiveSessionManager{deleted: true}
	handler := rt.handleSessionByID(fake)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+bound.GetHeader().ID, nil)
	deleteResp := httptest.NewRecorder()
	handler.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusConflict {
		t.Fatalf("bound delete status = %d, body = %s", deleteResp.Code, deleteResp.Body.String())
	}
	var conflict map[string]any
	if err := json.Unmarshal(deleteResp.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict["error"].(map[string]any)["code"] != "session_bound" {
		t.Fatalf("delete conflict = %#v", conflict)
	}
	if fake.deletedID != "" {
		t.Fatal("bound delete reached the active-session primitive")
	}

	transferBody := `{"channelType":"wechat","channelId":"identity-http","fromSessionId":"` + bound.GetHeader().ID + `","toSessionId":"target-http"}`
	transferReq := httptest.NewRequest(http.MethodPut, "/api/sessions/target-http/bindings", strings.NewReader(transferBody))
	transferResp := httptest.NewRecorder()
	handler.ServeHTTP(transferResp, transferReq)
	if transferResp.Code != http.StatusOK {
		t.Fatalf("transfer status = %d, body = %s", transferResp.Code, transferResp.Body.String())
	}
	binding, err := session.FindBinding(sessionDir, "wechat", "identity-http")
	if err != nil {
		t.Fatal(err)
	}
	if binding == nil || binding.SessionID != "target-http" {
		t.Fatalf("binding after transfer = %#v", binding)
	}
}

func TestLifecycleHTTPDeleteConflictsWithResponsesRuntimeLock(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("responses-delete-conflict"); err != nil {
		t.Fatal(err)
	}
	rt := &channelRuntime{cfg: DefaultConfig(), sessionDir: sessionDir, identityMux: session.NewIdentityLocks()}
	fake := &fakeActiveSessionManager{deleted: true}
	release := session.LockRuntime(sessionDir, mgr.GetHeader().ID)
	defer release()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+mgr.GetHeader().ID, nil)
	deleteResp := httptest.NewRecorder()
	rt.handleSessionByID(fake).ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusConflict {
		t.Fatalf("delete status = %d, body = %s", deleteResp.Code, deleteResp.Body.String())
	}
	if !strings.Contains(deleteResp.Body.String(), "session_running") {
		t.Fatalf("delete conflict body = %s", deleteResp.Body.String())
	}
	if fake.deletedID != "" {
		t.Fatal("delete reached persistence while Responses runtime lock was held")
	}
}

func TestLifecycleHTTPForkSessionIsIdempotent(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("fork-http"); err != nil {
		t.Fatal(err)
	}
	if err := session.StartConversationTurn(sessionDir, session.ConversationTurn{ID: "turn-http", SessionID: "fork-http"}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "world"}})); err != nil {
		t.Fatal(err)
	}
	if err := session.EndConversationTurn(sessionDir, "fork-http", "turn-http", "completed", "stop", time.Now()); err != nil {
		t.Fatal(err)
	}
	rt := &channelRuntime{cfg: DefaultConfig(), sessionDir: sessionDir, identityMux: session.NewIdentityLocks()}
	key := "fork-http-key"
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/fork-http/fork", strings.NewReader(`{}`))
		req.Header.Set("Idempotency-Key", key)
		resp := httptest.NewRecorder()
		rt.handleSessionByID(&fakeActiveSessionManager{}).ServeHTTP(resp, req)
		return resp
	}
	first := request()
	if first.Code != http.StatusOK {
		t.Fatalf("fork status = %d, body = %s", first.Code, first.Body.String())
	}
	var result session.ForkResult
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SessionID == "" || result.ParentSessionID != "fork-http" {
		t.Fatalf("fork result = %#v", result)
	}
	second := request()
	var retry session.ForkResult
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &retry) != nil || retry.SessionID != result.SessionID {
		t.Fatalf("idempotent fork retry status=%d body=%s result=%#v", second.Code, second.Body.String(), retry)
	}
	missingKey := httptest.NewRequest(http.MethodPost, "/api/sessions/fork-http/fork", strings.NewReader(`{}`))
	missingResp := httptest.NewRecorder()
	rt.handleSessionByID(&fakeActiveSessionManager{}).ServeHTTP(missingResp, missingKey)
	if missingResp.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency key status = %d", missingResp.Code)
	}
}

func TestChannelConfigHTTPPatchMergesWritableLayer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.json")
	state := &ServeConfigState{
		Effective:     DefaultConfig(),
		WritablePath:  path,
		WritableLayer: ConfigLayerExplicit,
	}
	if err := atomicWritePrivateFile(path, []byte(`{"channels":{"wechat":{"enabled":false,"credPath":"old.json","autoTyping":true}},"features":{"wechat":false}}`)); err != nil {
		t.Fatal(err)
	}
	if err := state.Reload(); err != nil {
		t.Fatal(err)
	}
	rt := &channelRuntime{cfg: state.Effective, configState: state, sessionDir: dir}
	handler := rt.handleChannelConfigPatch(path, nil)
	req := httptest.NewRequest(http.MethodPatch, "/api/serve/config/channels/wechat", strings.NewReader(`{"credPath":"new.json"}`))
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", resp.Code, resp.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	wechat := root["channels"].(map[string]any)["wechat"].(map[string]any)
	if wechat["credPath"] != "new.json" || wechat["autoTyping"] != true || wechat["enabled"] != false {
		t.Fatalf("merged channel config = %#v", wechat)
	}
	if got := rt.configSnapshot().Channels.Wechat.CredPath; got != "new.json" {
		t.Fatalf("effective channel config credPath = %q, want new.json", got)
	}
}

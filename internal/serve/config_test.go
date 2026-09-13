package serve

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configpkg "github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/messaging/wechat"
	channels "github.com/startvibecoding/mothx/internal/serve/channels"
	openaiapi "github.com/startvibecoding/mothx/internal/serve/openaiapi"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/skillhub"
	"github.com/startvibecoding/mothx/internal/stats"
	webui "github.com/startvibecoding/mothx/ui"
)

type fakeActiveSessionManager struct {
	sessions       []openaiapi.ActiveSessionInfo
	messages       []openaiapi.SessionMessageEntry
	toolResult     *openaiapi.SessionToolResultDetail
	subagents      []openaiapi.SessionSubAgentInfo
	runEvents      []openaiapi.SessionRunEventEntry
	capEvents      []openaiapi.SessionCapabilityEventEntry
	overview       openaiapi.CapabilityOverview
	caps           *openaiapi.SessionCapabilities
	patch          openaiapi.SessionCapabilityPatch
	capsID         string
	toolResultID   string
	deletedID      string
	runsID         string
	runsLimit      int
	runs           []session.SessionRun
	submittedRun   bool
	deleted        bool
	webSearchAvail bool
	err            error
}

func TestArtifactSwitchesDefaultOffAndRoundTripIndependently(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.API.EnableArtifact || cfg.Channels.Artifact {
		t.Fatalf("artifact defaults = API %v, Channel %v; want both false", cfg.API.EnableArtifact, cfg.Channels.Artifact)
	}

	decoded, err := DecodeConfigBytes([]byte(`{"artifact":true,"channels":{"artifact":false}}`))
	if err != nil {
		t.Fatalf("decode WebUI artifact config: %v", err)
	}
	if !decoded.API.EnableArtifact || decoded.Channels.Artifact {
		t.Fatalf("decoded artifact switches = API %v, Channel %v", decoded.API.EnableArtifact, decoded.Channels.Artifact)
	}

	decoded, err = DecodeConfigBytes([]byte(`{"api":{"enableArtifact":false},"channels":{"artifact":true}}`))
	if err != nil {
		t.Fatalf("decode Channel artifact config: %v", err)
	}
	if decoded.API.EnableArtifact || !decoded.Channels.Artifact {
		t.Fatalf("decoded artifact switches = API %v, Channel %v", decoded.API.EnableArtifact, decoded.Channels.Artifact)
	}

	data, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("marshal artifact config: %v", err)
	}
	roundTrip, err := DecodeConfigBytes(data)
	if err != nil {
		t.Fatalf("round-trip artifact config: %v", err)
	}
	if roundTrip.API.EnableArtifact || !roundTrip.Channels.Artifact {
		t.Fatalf("round-trip artifact switches = API %v, Channel %v", roundTrip.API.EnableArtifact, roundTrip.Channels.Artifact)
	}
}

func (f *fakeActiveSessionManager) ListActiveSessions() []openaiapi.ActiveSessionInfo {
	return append([]openaiapi.ActiveSessionInfo(nil), f.sessions...)
}

func (f *fakeActiveSessionManager) DeleteActiveSession(id string) (bool, error) {
	f.deletedID = id
	return f.deleted, f.err
}

func (f *fakeActiveSessionManager) GetSessionMessages(id string) ([]openaiapi.SessionMessageEntry, error) {
	return append([]openaiapi.SessionMessageEntry(nil), f.messages...), f.err
}

func (f *fakeActiveSessionManager) GetSessionToolResult(id, toolCallID string) (*openaiapi.SessionToolResultDetail, error) {
	f.toolResultID = toolCallID
	return f.toolResult, f.err
}

func (f *fakeActiveSessionManager) GetSessionSubAgents(id string) ([]openaiapi.SessionSubAgentInfo, error) {
	return append([]openaiapi.SessionSubAgentInfo(nil), f.subagents...), f.err
}

func (f *fakeActiveSessionManager) GetSessionSubAgentMessages(id, agentID string) ([]openaiapi.SessionMessageEntry, error) {
	return append([]openaiapi.SessionMessageEntry(nil), f.messages...), f.err
}

func (f *fakeActiveSessionManager) GetSessionRunEvents(id string) ([]openaiapi.SessionRunEventEntry, error) {
	return append([]openaiapi.SessionRunEventEntry(nil), f.runEvents...), f.err
}

func (f *fakeActiveSessionManager) GetSessionCapabilityEvents(id string) ([]openaiapi.SessionCapabilityEventEntry, error) {
	return append([]openaiapi.SessionCapabilityEventEntry(nil), f.capEvents...), f.err
}

func (f *fakeActiveSessionManager) CapabilityOverview() openaiapi.CapabilityOverview {
	return f.overview
}

func (f *fakeActiveSessionManager) IsWebSearchAvailable() bool {
	return f.webSearchAvail
}

func (f *fakeActiveSessionManager) GetSessionCapabilities(id string) (*openaiapi.SessionCapabilities, error) {
	f.capsID = id
	return f.caps, f.err
}

func (f *fakeActiveSessionManager) GetSessionRuntime(id string) (*openaiapi.SessionRuntimeSnapshot, error) {
	f.capsID = id
	if f.caps == nil {
		return &openaiapi.SessionRuntimeSnapshot{SessionID: id, Capabilities: map[string]openaiapi.SessionCapabilityState{}}, f.err
	}
	return &openaiapi.SessionRuntimeSnapshot{
		SessionID:    f.caps.ID,
		Mode:         f.caps.Mode,
		Model:        f.caps.Model,
		WorkDir:      f.caps.WorkDir,
		Capabilities: map[string]openaiapi.SessionCapabilityState{},
	}, f.err
}

func (f *fakeActiveSessionManager) PatchSessionCapabilities(id string, patch openaiapi.SessionCapabilityPatch) (*openaiapi.SessionCapabilities, error) {
	f.capsID = id
	f.patch = patch
	return f.caps, f.err
}

func (f *fakeActiveSessionManager) PatchSessionRuntime(id string, patch openaiapi.SessionRuntimePatch) (*openaiapi.SessionRuntimeSnapshot, error) {
	f.capsID = id
	f.patch = openaiapi.SessionCapabilityPatch{}
	if patch.Mode != nil {
		f.patch.Mode = patch.Mode
	}
	if patch.Tools != nil {
		f.patch.WebSearch = patch.Tools.WebSearch
		f.patch.Browser = patch.Tools.Browser
		f.patch.A2AMaster = patch.Tools.A2AMaster
		f.patch.DelegateMode = patch.Tools.Delegate
		f.patch.MultiAgent = patch.Tools.MultiAgent
		f.patch.Workflows = patch.Tools.Workflows
	}
	if f.caps == nil {
		return &openaiapi.SessionRuntimeSnapshot{SessionID: id, Capabilities: map[string]openaiapi.SessionCapabilityState{}}, f.err
	}
	return &openaiapi.SessionRuntimeSnapshot{
		SessionID:    f.caps.ID,
		Mode:         f.caps.Mode,
		Model:        f.caps.Model,
		WorkDir:      f.caps.WorkDir,
		Capabilities: map[string]openaiapi.SessionCapabilityState{},
	}, f.err
}

func (f *fakeActiveSessionManager) ListSessionRuns(id string, limit int) ([]session.SessionRun, error) {
	f.runsID = id
	f.runsLimit = limit
	return append([]session.SessionRun(nil), f.runs...), f.err
}

func (f *fakeActiveSessionManager) HandleSubmitRun(w http.ResponseWriter, r *http.Request) {
	f.submittedRun = true
	w.WriteHeader(http.StatusAccepted)
}

func TestDefaultConfigEnablesCronWithoutMultiAgent(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Features.Cron || !cfg.Cron.Enabled {
		t.Fatalf("cron should be enabled by default, features=%#v cron=%#v", cfg.Features, cfg.Cron)
	}
	if cfg.Features.MultiAgent || cfg.API.EnableSubAgents {
		t.Fatalf("multi-agent should be disabled by default, features=%#v api=%#v", cfg.Features, cfg.API)
	}
}

func TestLoadConfigFrom_LegacyNestedSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.json")
	data := `{
		"api": {
			"listen": ":9090",
			"provider": "deepseek",
			"model": "deepseek-chat",
			"defaultMode": "agent",
			"sandbox": {"enabled": true, "level": "strict"},
			"enableSubAgents": true
		},
		"channels": {
			"wechat": {"enabled": true, "auto_typing": false},
			"feishu": {"enabled": true, "app_id": "app-id", "app_secret": "secret"}
		},
		"webUI": {"enabled": false, "dir": "custom-ui"},
		"memory": {"enabled": true},
		"agent": {"max_turns": 42}
	}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatalf("write serve config: %v", err)
	}

	cfg, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}

	if cfg.API.Listen != ":9090" {
		t.Fatalf("listen = %q, want :9090", cfg.API.Listen)
	}
	if cfg.API.Provider != "deepseek" {
		t.Fatalf("provider = %q, want deepseek", cfg.API.Provider)
	}
	if cfg.API.Model != "deepseek-chat" {
		t.Fatalf("model = %q, want deepseek-chat", cfg.API.Model)
	}
	if cfg.API.DefaultMode != "agent" {
		t.Fatalf("mode = %q, want agent", cfg.API.DefaultMode)
	}
	if !cfg.API.Sandbox.Enabled || cfg.API.Sandbox.Level != "strict" {
		t.Fatalf("sandbox = %#v, want enabled strict", cfg.API.Sandbox)
	}
	if !cfg.API.EnableSubAgents {
		t.Fatal("enableSubAgents should be true")
	}
	if !cfg.Channels.Wechat.Enabled || cfg.Channels.Wechat.AutoTyping {
		t.Fatalf("wechat config = %#v", cfg.Channels.Wechat)
	}
	if !cfg.Channels.Feishu.Enabled || cfg.Channels.Feishu.AppID != "app-id" || cfg.Channels.Feishu.AppSecret != "secret" {
		t.Fatalf("feishu config = %#v", cfg.Channels.Feishu)
	}
	if cfg.WebUI.Enabled || cfg.WebUI.Dir != "custom-ui" {
		t.Fatalf("webUI config = %#v", cfg.WebUI)
	}
	if !cfg.Memory.Enabled {
		t.Fatal("memory should be enabled")
	}
	if cfg.Agent.MaxTurns != 42 {
		t.Fatalf("agent max turns = %d, want 42", cfg.Agent.MaxTurns)
	}
}

func TestLoadConfigFrom_FlatSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.json")
	data := `{
		"listen": ":7777",
		"provider": "openai",
		"model": "gpt-4o",
		"mode": "agent",
		"defaultWorkDir": "/tmp/project",
		"thinking": "high",
		"auth": {
			"enabled": true,
			"tokens": ["sk-test"]
		},
		"features": {
			"webUI": true,
			"openaiAPI": true,
			"wechat": true,
			"feishu": true,
			"multiAgent": true,
			"memory": true
		},
		"sandbox": {
			"enabled": true,
			"level": "strict"
		},
		"allowedWorkDirs": ["/tmp/project"],
		"channels": {
			"wechat": {"autoTyping": false},
			"feishu": {"appId": "app-id", "appSecret": "secret"}
		},
		"session": {
			"idleTimeoutSeconds": 99,
			"maxSessions": 7
		},
		"toolVisibility": {"mode": "content", "detail": "expanded"},
		"systemPromptMode": "ignore",
		"requestTimeoutSeconds": 66,
		"maxConcurrentRequests": 5,
		"webSearch": true,
		"browser": true,
		"a2aMaster": true,
		"agent": {"maxTurns": 12}
	}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatalf("write serve config: %v", err)
	}

	cfg, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}

	if cfg.API.Listen != ":7777" {
		t.Fatalf("listen = %q, want :7777", cfg.API.Listen)
	}
	if cfg.API.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", cfg.API.Provider)
	}
	if cfg.API.Model != "gpt-4o" {
		t.Fatalf("model = %q, want gpt-4o", cfg.API.Model)
	}
	if cfg.API.DefaultMode != "agent" {
		t.Fatalf("mode = %q, want agent", cfg.API.DefaultMode)
	}
	if cfg.API.DefaultWorkDir != "/tmp/project" || cfg.API.GetWorkDir() != "/tmp/project" {
		t.Fatalf("defaultWorkDir = %q, effective = %q, want /tmp/project", cfg.API.DefaultWorkDir, cfg.API.GetWorkDir())
	}
	if !cfg.API.Auth.Enabled || len(cfg.API.Auth.Tokens) != 1 || cfg.API.Auth.Tokens[0] != "sk-test" {
		t.Fatalf("auth = %#v", cfg.API.Auth)
	}
	if !cfg.API.Sandbox.Enabled || cfg.API.Sandbox.Level != "strict" {
		t.Fatalf("sandbox = %#v", cfg.API.Sandbox)
	}
	if cfg.API.AllowedWorkDirs == nil || len(*cfg.API.AllowedWorkDirs) != 1 || (*cfg.API.AllowedWorkDirs)[0] != "/tmp/project" {
		t.Fatalf("allowedWorkDirs = %#v", cfg.API.AllowedWorkDirs)
	}
	if !cfg.API.EnableSubAgents {
		t.Fatal("enableSubAgents should be true")
	}
	if !cfg.Features.OpenAIAPI {
		t.Fatal("openaiAPI feature should be true")
	}
	if cfg.API.Session.IdleTimeoutSeconds != 99 || cfg.API.Session.MaxSessions != 7 {
		t.Fatalf("session config = %#v", cfg.API.Session)
	}
	if cfg.API.ToolVisibility.Mode != "content" || cfg.API.ToolVisibility.Detail != "expanded" {
		t.Fatalf("tool visibility = %#v", cfg.API.ToolVisibility)
	}
	if cfg.API.SystemPromptMode != "ignore" {
		t.Fatalf("system prompt mode = %q, want ignore", cfg.API.SystemPromptMode)
	}
	if cfg.API.RequestTimeoutSecs != 66 {
		t.Fatalf("timeout = %d, want 66", cfg.API.RequestTimeoutSecs)
	}
	if !cfg.API.EnableWebSearch {
		t.Fatal("webSearch should be enabled")
	}
	if !cfg.API.EnableBrowser {
		t.Fatal("browser should be enabled")
	}
	if !cfg.API.EnableA2AMaster {
		t.Fatal("a2aMaster should be enabled")
	}
	if cfg.API.MaxConcurrentReqs != 5 {
		t.Fatalf("max concurrent = %d, want 5", cfg.API.MaxConcurrentReqs)
	}
	if !cfg.Channels.Wechat.Enabled || cfg.Channels.Wechat.AutoTyping {
		t.Fatalf("wechat config = %#v", cfg.Channels.Wechat)
	}
	if !cfg.Channels.Feishu.Enabled || cfg.Channels.Feishu.AppID != "app-id" || cfg.Channels.Feishu.AppSecret != "secret" {
		t.Fatalf("feishu config = %#v", cfg.Channels.Feishu)
	}
	if !cfg.WebUI.Enabled {
		t.Fatal("webUI should be enabled")
	}
	if !cfg.Memory.Enabled {
		t.Fatal("memory should be enabled")
	}
	if cfg.Agent.MaxTurns != 12 {
		t.Fatalf("agent max turns = %d, want 12", cfg.Agent.MaxTurns)
	}
}

func TestDecodeConfigBytes_LegacyWorkDirMapsToDefaultWorkDir(t *testing.T) {
	cfg, err := DecodeConfigBytes([]byte(`{"workDir":"/tmp/legacy-project"}`))
	if err != nil {
		t.Fatalf("DecodeConfigBytes: %v", err)
	}
	if cfg.API.DefaultWorkDir != "/tmp/legacy-project" || cfg.API.GetWorkDir() != "/tmp/legacy-project" {
		t.Fatalf("defaultWorkDir = %q, effective = %q, want /tmp/legacy-project", cfg.API.DefaultWorkDir, cfg.API.GetWorkDir())
	}
	if cfg.API.WorkingDir != "" {
		t.Fatalf("legacy WorkingDir should be normalized away, got %q", cfg.API.WorkingDir)
	}
}

func TestMarshalConfigWritesDefaultWorkDir(t *testing.T) {
	cfg := DefaultConfig()
	cfg.API.WorkingDir = "/tmp/legacy-project"

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal marshaled config: %v", err)
	}
	if got["defaultWorkDir"] != "/tmp/legacy-project" {
		t.Fatalf("defaultWorkDir = %#v, want /tmp/legacy-project; json=%s", got["defaultWorkDir"], string(data))
	}
	if _, ok := got["workDir"]; ok {
		t.Fatalf("legacy workDir should not be emitted: %s", string(data))
	}
}

func TestDecodeConfigBytes_FlatSchema(t *testing.T) {
	data := []byte(`{
		"listen": ":8081",
		"features": {
			"webUI": false,
			"openaiAPI": false
		},
		"channels": {
			"wechat": {"autoTyping": false}
		}
	}`)

	cfg, err := DecodeConfigBytes(data)
	if err != nil {
		t.Fatalf("DecodeConfigBytes: %v", err)
	}
	if cfg.API.Listen != ":8081" {
		t.Fatalf("listen = %q, want :8081", cfg.API.Listen)
	}
	if cfg.Features.WebUI {
		t.Fatal("webUI feature should be false")
	}
	if cfg.Features.OpenAIAPI {
		t.Fatal("openaiAPI feature should be false")
	}
	if cfg.Channels.Wechat.AutoTyping {
		t.Fatal("wechat autoTyping should be false")
	}
}

func TestInitConfig_WritesFlatTemplate(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("MOTHX_CONFIG_DIR", "")
	t.Setenv("MOTHX_DIR", "")

	path, err := InitConfig(false)
	if err != nil {
		t.Fatalf("InitConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"listen": "127.0.0.1:7872"`) {
		t.Fatalf("generated config missing flat listen field:\n%s", text)
	}
	if !strings.Contains(text, `"features": {`) {
		t.Fatalf("generated config missing features block:\n%s", text)
	}
	if !strings.Contains(text, `"openaiAPI": true`) {
		t.Fatalf("generated config missing openaiAPI feature flag:\n%s", text)
	}
	if !strings.Contains(text, `"auth": {`) {
		t.Fatalf("generated config missing auth block:\n%s", text)
	}
	// The template token is what the startup/init warnings point at; keeping the
	// same literal in the generated file is what makes that check work.
	if !strings.Contains(text, PlaceholderAuthToken) {
		t.Fatalf("generated config missing the placeholder auth token:\n%s", text)
	}
	generatedCfg, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if len(generatedCfg.API.Auth.Tokens) != 1 || !IsPlaceholderAuthToken(generatedCfg.API.Auth.Tokens[0]) {
		t.Fatalf("generated config auth tokens = %v, want the placeholder token", generatedCfg.API.Auth.Tokens)
	}
	// The template ships with auth disabled; the warning matters as soon as an
	// operator turns auth on without replacing the token.
	generatedCfg.API.Auth.Enabled = true
	if !UsesPlaceholderAuthToken(generatedCfg) {
		t.Fatal("enabling auth on the generated template must be detected as still using the placeholder token")
	}
	if !strings.Contains(text, `"defaultWorkDir":`) || strings.Contains(text, `"workDir":`) {
		t.Fatalf("generated config should use defaultWorkDir instead of legacy workDir:\n%s", text)
	}
	var generated map[string]any
	if err := json.Unmarshal(data, &generated); err != nil {
		t.Fatalf("decode generated config: %v", err)
	}
	if _, ok := generated["allowedWorkDirs"]; ok {
		t.Fatalf("generated config should leave selectable workdirs unrestricted by default:\n%s", text)
	}
	if strings.Contains(text, `"api": {`) {
		t.Fatalf("generated config should prefer flat schema, got legacy api block:\n%s", text)
	}
}

func TestInitConfigForProject_WritesProjectTemplate(t *testing.T) {
	tempHome := t.TempDir()
	workDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	})

	t.Setenv("HOME", tempHome)
	t.Setenv("MOTHX_DIR", "")

	path, err := InitConfigForProject(true, false)
	if err != nil {
		t.Fatalf("InitConfigForProject: %v", err)
	}

	want := filepath.Join(".mothx", "serve.json")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected project config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempHome, ".mothx", "serve.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no global serve config, stat err=%v", err)
	}
}

func TestHandleChannels_ReturnsStableEntries(t *testing.T) {
	rt := &channelRuntime{
		cfg: &Config{
			Channels: ChannelConfig{
				Wechat: channels.WechatConfig{Enabled: true},
				Feishu: channels.FeishuConfig{Enabled: false},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	w := httptest.NewRecorder()
	rt.handleChannels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var statuses []channelStatus
	if err := json.NewDecoder(w.Body).Decode(&statuses); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("status count = %d, want 2", len(statuses))
	}
	if statuses[0] != (channelStatus{Name: "wechat", Enabled: true, Connected: false}) {
		t.Fatalf("wechat status = %#v", statuses[0])
	}
	if statuses[1] != (channelStatus{Name: "feishu", Enabled: false, Connected: false}) {
		t.Fatalf("feishu status = %#v", statuses[1])
	}
}

func TestHandleWechatLoginGetReturnsStoredCredentials(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "wechat-credentials.json")
	if err := wechat.SaveCredentials(&wechat.Credentials{
		Token:     "token",
		BaseURL:   wechat.DefaultBaseURL,
		AccountID: "bot-id",
		UserID:    "user-id",
	}, credPath); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	rt := &channelRuntime{
		cfg: &Config{
			Channels: ChannelConfig{
				Wechat: channels.WechatConfig{CredPath: credPath, Enabled: true},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/channels/wechat/login", nil)
	w := httptest.NewRecorder()
	rt.handleWechatLogin(filepath.Join(t.TempDir(), "serve.json")).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got wechatLoginStatus
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.State != "confirmed" || !got.Enabled || !got.LoggedIn || got.UserID != "user-id" {
		t.Fatalf("login status = %#v", got)
	}
}

func TestHandleWechatLoginQRServesCurrentSessionImage(t *testing.T) {
	sess, _ := newWechatLoginSession()
	t.Cleanup(sess.cancelLogin)
	sess.update("pending", func() {
		sess.qrURL = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	})
	rt := &channelRuntime{cfg: DefaultConfig(), wechatLogin: sess}

	status := rt.wechatLoginSnapshot()
	if !strings.HasPrefix(status.QRURL, "/api/channels/wechat/login/qr?") {
		t.Fatalf("qr url = %q, want local proxy URL", status.QRURL)
	}
	if !strings.HasPrefix(status.QROpenURL, "data:image/png;base64,") {
		t.Fatalf("qr open url = %q, want data image URL", status.QROpenURL)
	}

	req := httptest.NewRequest(http.MethodGet, status.QRURL, nil)
	w := httptest.NewRecorder()
	rt.handleWechatLoginQR(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content-type = %q, want image/png", got)
	}
	if w.Body.Len() == 0 {
		t.Fatal("expected QR image body")
	}
}

func TestHandleWechatLoginQRExtractsImageFromHTMLPage(t *testing.T) {
	const pngData = "\x89PNG\r\n\x1a\nqr"
	var pageRequested bool
	var imageRequested bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			pageRequested = true
			http.SetCookie(w, &http.Cookie{Name: "qr", Value: "ok"})
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><body><img class="qr" src="/qr.png"></body></html>`))
		case "/qr.png":
			imageRequested = true
			cookie, err := r.Cookie("qr")
			if err != nil || cookie.Value != "ok" {
				http.Error(w, "missing QR cookie", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(pngData))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	sess, _ := newWechatLoginSession()
	t.Cleanup(sess.cancelLogin)
	sess.update("pending", func() {
		sess.qrURL = upstream.URL + "/login"
	})
	rt := &channelRuntime{cfg: DefaultConfig(), wechatLogin: sess}

	req := httptest.NewRequest(http.MethodGet, "/api/channels/wechat/login/qr", nil)
	w := httptest.NewRecorder()
	rt.handleWechatLoginQR(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !pageRequested || !imageRequested {
		t.Fatalf("pageRequested=%v imageRequested=%v, want both true", pageRequested, imageRequested)
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content-type = %q, want image/png", got)
	}
	if got := w.Body.String(); got != pngData {
		t.Fatalf("body = %q, want %q", got, pngData)
	}
}

func TestHandleWechatLoginQRReturnsBase64DataURL(t *testing.T) {
	const pngData = "\x89PNG\r\n\x1a\nqr"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "qr", Value: "ok"})
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><body><img src="/qr.png"></body></html>`))
		case "/qr.png":
			cookie, err := r.Cookie("qr")
			if err != nil || cookie.Value != "ok" {
				http.Error(w, "missing QR cookie", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(pngData))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	sess, _ := newWechatLoginSession()
	t.Cleanup(sess.cancelLogin)
	sess.update("pending", func() {
		sess.qrURL = upstream.URL + "/login"
	})
	rt := &channelRuntime{cfg: DefaultConfig(), wechatLogin: sess}

	req := httptest.NewRequest(http.MethodGet, "/api/channels/wechat/login/qr?format=base64", nil)
	w := httptest.NewRecorder()
	rt.handleWechatLoginQR(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got wechatLoginQRResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ContentType != "image/png" {
		t.Fatalf("content-type = %q, want image/png", got.ContentType)
	}
	if !strings.HasPrefix(got.DataURL, "data:image/png;base64,") {
		t.Fatalf("data URL = %q, want image/png data URL", got.DataURL)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.Base64)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if string(decoded) != pngData {
		t.Fatalf("decoded data = %q, want %q", string(decoded), pngData)
	}
}

func TestRegisterServeRoutes_WebUIDisabledStillServesManageAPI(t *testing.T) {
	rt := &channelRuntime{
		cfg: &Config{
			Features: FeatureConfig{WebUI: false, OpenAIAPI: true},
			WebUI:    WebUIConfig{Enabled: false, Dir: "ui/dist"},
		},
	}

	mux := http.NewServeMux()
	registerServeRoutes(mux, rt, "/tmp/serve.json")

	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/channels status = %d, want 200", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("/ status = %d, want 404 when webUI disabled", w.Code)
	}
}

func TestRegisterServeRoutes_SessionsRequireAPIServer(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	mux := http.NewServeMux()
	registerServeRoutes(mux, rt, "/tmp/serve.json")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("/api/sessions status = %d, want 503", w.Code)
	}
}

func TestSkillHubRouteRequiresAPIServer(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	req := httptest.NewRequest(http.MethodGet, "/api/skillhub/markets", nil)
	w := httptest.NewRecorder()
	rt.handleSkillHub(nil)(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("SkillHub status = %d, want 503", w.Code)
	}
}

func TestParseSkillHubPathSupportsNamespacedIDs(t *testing.T) {
	market, id, err := parseSkillHubPath("clawhub.ai/openclaw/git")
	if err != nil || market != skillhub.MarketClawHub || id != "openclaw/git" {
		t.Fatalf("parseSkillHubPath() = %q, %q, %v", market, id, err)
	}
	if _, _, err := parseSkillHubPath("unknown/demo"); err == nil {
		t.Fatal("unsupported market was accepted")
	}
}

func TestHandleServeConfigPutKeepsSQLiteCronStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")
	cfg := DefaultConfig()
	cfg.Features.Cron = true
	cfg.Cron.Enabled = true
	rt := &channelRuntime{cfg: cfg, cronStore: cron.NewSQLiteCronStore(dir), cronStorePath: dbPath, sessionDir: dir}

	body := `{"features":{"cron":true},"cron":{"enabled":true}}`
	req := httptest.NewRequest(http.MethodPut, "/api/serve/config", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.handleServeConfig(filepath.Join(dir, "serve.json")).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if rt.cronStorePath != dbPath {
		t.Fatalf("cronStorePath = %q, want %q", rt.cronStorePath, dbPath)
	}
	if rt.cronStore == nil {
		t.Fatal("cron store should be available after config update")
	}
}

func TestDecodeConfigBytes_NestedRuntimeToolFlags(t *testing.T) {
	cfg, err := DecodeConfigBytes([]byte(`{
		"webSearch": false,
		"browser": false,
		"a2aMaster": false,
		"api": {
			"enableWebSearch": true,
			"enableBrowser": true,
			"enableA2AMaster": true,
			"enableDelegate": true,
			"enableWorkflows": true
		}
	}`))
	if err != nil {
		t.Fatalf("DecodeConfigBytes: %v", err)
	}
	if cfg.API.EnableWebSearch || cfg.API.EnableBrowser || cfg.API.EnableA2AMaster {
		t.Fatalf("top-level flat tool flags should override nested api flags: %#v", cfg.API)
	}
	if !cfg.API.EnableDelegate || !cfg.API.EnableWorkflows {
		t.Fatalf("nested delegate/workflow flags should be decoded: %#v", cfg.API)
	}

	cfg, err = DecodeConfigBytes([]byte(`{
		"api": {
			"enableWebSearch": true,
			"enableBrowser": true,
			"enableA2AMaster": true
		}
	}`))
	if err != nil {
		t.Fatalf("DecodeConfigBytes nested only: %v", err)
	}
	if !cfg.API.EnableWebSearch || !cfg.API.EnableBrowser || !cfg.API.EnableA2AMaster {
		t.Fatalf("nested api tool flags should be honored when flat flags are absent: %#v", cfg.API)
	}
}

func TestHandleServeConfigPutPersistsWebSearchFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.json")
	cfg := DefaultConfig()
	rt := &channelRuntime{cfg: cfg, sessionDir: dir}

	req := httptest.NewRequest(http.MethodPut, "/api/serve/config", strings.NewReader(`{
		"features": {"webUI": true, "openAIAPI": true},
		"api": {"enableWebSearch": true}
	}`))
	w := httptest.NewRecorder()
	rt.handleServeConfig(path).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !rt.cfg.API.EnableWebSearch {
		t.Fatal("runtime web search should be enabled")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(data), `"webSearch": true`) {
		t.Fatalf("saved config should persist flat webSearch flag, got:\n%s", string(data))
	}
}

func TestHandleStatus_ReturnsRuntimeSummary(t *testing.T) {
	cfg := DefaultConfig()
	cfg.API.Listen = "127.0.0.1:9090"
	cfg.Features.OpenAIAPI = false
	cfg.Features.Wechat = true
	cfg.Channels.Wechat.Enabled = true
	rt := &channelRuntime{cfg: cfg}
	sessions := &fakeActiveSessionManager{
		sessions: []openaiapi.ActiveSessionInfo{{ID: "s1"}, {ID: "s2"}},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	rt.handleStatus(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"openaiAPI":false`) || !strings.Contains(body, `"feishu":false`) {
		t.Fatalf("status response should include false feature flags: %s", body)
	}
	var got serveStatus
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != "ok" || got.Listen != "127.0.0.1:9090" {
		t.Fatalf("status response = %#v", got)
	}
	if got.Features.OpenAIAPI || !got.Features.Wechat {
		t.Fatalf("features = %#v", got.Features)
	}
	if got.Sessions != 2 {
		t.Fatalf("sessions = %d, want 2", got.Sessions)
	}
	if len(got.Channels) != 2 || !got.Channels[0].Enabled {
		t.Fatalf("channels = %#v", got.Channels)
	}
}

func TestHandleStatusReflectsSettingsWebSearch(t *testing.T) {
	cfg := DefaultConfig()
	rt := &channelRuntime{cfg: cfg}
	sessions := &fakeActiveSessionManager{webSearchAvail: true}

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	rt.handleStatus(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got serveStatus
	if err := json.NewDecoder(strings.NewReader(w.Body.String())).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Features.WebSearch {
		t.Fatalf("expected features.webSearch = true when session manager reports availability, got %#v", got.Features)
	}
}

func TestHandleStatsSummaryReturnsStatsSummary(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New("/tmp/stats-project", sessionDir)
	if err := mgr.InitWithID("stats-session"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	if err := mgr.RecordUsage("openai", "serve", "gpt-test", 11, 7, 18, 120); err != nil {
		t.Fatalf("record usage: %v", err)
	}

	rt := &channelRuntime{cfg: DefaultConfig()}
	req := httptest.NewRequest(http.MethodGet, "/api/stats/summary", nil)
	w := httptest.NewRecorder()
	rt.handleStats(sessionDir).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var got stats.Summary
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.TotalRequests != 1 || got.InputTokens != 11 || got.OutputTokens != 7 || got.TotalTokens != 18 {
		t.Fatalf("summary = %#v", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/stats/by-model", nil)
	w = httptest.NewRecorder()
	rt.handleStats(sessionDir).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("by-model status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var byModel []stats.Aggregate
	if err := json.NewDecoder(w.Body).Decode(&byModel); err != nil {
		t.Fatalf("decode by-model response: %v", err)
	}
	if len(byModel) != 1 || byModel[0].Model != "gpt-test" || byModel[0].TotalTokens != 18 {
		t.Fatalf("by-model = %#v", byModel)
	}
}

func TestHandleStatsSummaryMissingDBReturnsZeroSummary(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	req := httptest.NewRequest(http.MethodGet, "/api/stats/summary", nil)
	w := httptest.NewRecorder()
	rt.handleStats(t.TempDir()).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var got stats.Summary
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.TotalRequests != 0 || got.TotalTokens != 0 {
		t.Fatalf("summary = %#v", got)
	}
}

func TestHandleSessions_ReturnsSessions(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{
		sessions: []openaiapi.ActiveSessionInfo{{ID: "s1", WorkDir: "/tmp/a"}},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	rt.handleSessions(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got struct {
		Sessions []openaiapi.ActiveSessionInfo `json:"sessions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "s1" || got.Sessions[0].WorkDir != "/tmp/a" {
		t.Fatalf("sessions = %#v", got.Sessions)
	}
}

func TestHandleSessions_ScopeActive(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{
		sessions: []openaiapi.ActiveSessionInfo{
			{ID: "active", WorkDir: "/tmp/a", Active: true},
			{ID: "history", WorkDir: "/tmp/b", Active: false},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?scope=active", nil)
	w := httptest.NewRecorder()
	rt.handleSessions(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got struct {
		Sessions []openaiapi.ActiveSessionInfo `json:"sessions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "active" {
		t.Fatalf("sessions = %#v", got.Sessions)
	}
}

func TestHandleCapabilities_ReturnsOverview(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{
		overview: openaiapi.CapabilityOverview{
			Modes: []string{"plan", "agent", "yolo", "os"},
			Features: map[string]openaiapi.CapabilityFeature{
				"browser": {Available: true, Default: true},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/capabilities", nil)
	w := httptest.NewRecorder()
	rt.handleCapabilities(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got openaiapi.CapabilityOverview
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Modes) != 4 || !got.Features["browser"].Default {
		t.Fatalf("overview = %#v", got)
	}
}

func TestHandleSessionByID_GetCapabilities(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{
		caps: &openaiapi.SessionCapabilities{ID: "s1", Mode: "agent", Browser: true},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/capabilities", nil)
	w := httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if sessions.capsID != "s1" {
		t.Fatalf("capsID = %q, want s1", sessions.capsID)
	}
	var got openaiapi.SessionCapabilities
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ID != "s1" || got.Mode != "agent" || !got.Browser {
		t.Fatalf("capabilities = %#v", got)
	}
}

func TestHandleSessionByID_PatchCapabilities(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{
		caps: &openaiapi.SessionCapabilities{ID: "s1", Mode: "agent", Browser: true},
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/s1/capabilities", strings.NewReader(`{"mode":"agent","browser":true}`))
	w := httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if sessions.capsID != "s1" {
		t.Fatalf("capsID = %q, want s1", sessions.capsID)
	}
	if sessions.patch.Mode == nil || *sessions.patch.Mode != "agent" {
		t.Fatalf("patch mode = %#v", sessions.patch.Mode)
	}
	if sessions.patch.Browser == nil || !*sessions.patch.Browser {
		t.Fatalf("patch browser = %#v", sessions.patch.Browser)
	}
}

func TestHandleSessionByID_ActiveAlias(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{
		sessions: []openaiapi.ActiveSessionInfo{
			{ID: "active", WorkDir: "/tmp/a", Active: true},
			{ID: "history", WorkDir: "/tmp/b", Active: false},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/active", nil)
	w := httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got struct {
		Sessions []openaiapi.ActiveSessionInfo `json:"sessions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "active" {
		t.Fatalf("sessions = %#v", got.Sessions)
	}
}

func TestHandleSessionByID_DeletesActiveSession(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{deleted: true}

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/s1", nil)
	w := httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if sessions.deletedID != "s1" {
		t.Fatalf("deletedID = %q, want s1", sessions.deletedID)
	}
	var got struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ID != "s1" || !got.Deleted {
		t.Fatalf("response = %#v", got)
	}
}

func TestHandleSessionByID_ReturnsSubAgents(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{subagents: []openaiapi.SessionSubAgentInfo{{ID: "child-1", Status: "done", Active: false}}}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/subagents", nil)
	w := httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("subagents status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Subagents []openaiapi.SessionSubAgentInfo `json:"subagents"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode subagents: %v", err)
	}
	if len(got.Subagents) != 1 || got.Subagents[0].ID != "child-1" || got.Subagents[0].Status != "done" {
		t.Fatalf("subagents = %#v", got.Subagents)
	}
}

func TestHandleSessionByID_ReturnsSubAgentMessages(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{messages: []openaiapi.SessionMessageEntry{{ID: "m1", AgentID: "child-1", Role: "assistant", Content: "hello"}}}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/subagents/child-1/messages", nil)
	w := httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sub-agent messages status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Messages []openaiapi.SessionMessageEntry `json:"messages"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].AgentID != "child-1" || got.Messages[0].Content != "hello" {
		t.Fatalf("messages = %#v", got.Messages)
	}
}

func TestHandleSessionByID_SubAgentRoutesRejectUnknownAndWrongMethods(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{err: openaiapi.ErrSessionNotFound}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/missing/subagents", nil)
	w := httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/sessions/s1/subagents", nil)
	w = httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method status = %d, want 405", w.Code)
	}
}

func TestHandleSessionByID_ReturnsToolResultDetail(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{
		toolResult: &openaiapi.SessionToolResultDetail{
			ToolCallID: "call-1",
			ToolName:   "bash",
			Content:    "full output",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/tool-results/call-1", nil)
	w := httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if sessions.toolResultID != "call-1" {
		t.Fatalf("toolResultID = %q, want call-1", sessions.toolResultID)
	}
	var got openaiapi.SessionToolResultDetail
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ToolCallID != "call-1" || got.Content != "full output" {
		t.Fatalf("detail = %#v", got)
	}
}

func TestHandleSessionByID_ReturnsRunEvents(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{
		runEvents: []openaiapi.SessionRunEventEntry{
			{ID: "evt-1", SessionID: "s1", RunID: "run-1", EventType: "started", Status: "running"},
			{ID: "evt-2", SessionID: "s1", RunID: "run-1", EventType: "finished", Status: "completed"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/run-events", nil)
	w := httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Events []openaiapi.SessionRunEventEntry `json:"events"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Events) != 2 || got.Events[0].RunID != "run-1" || got.Events[1].EventType != "finished" {
		t.Fatalf("events = %#v", got.Events)
	}
}

func TestHandleSessionByID_ReturnsCapabilityEvents(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{
		capEvents: []openaiapi.SessionCapabilityEventEntry{
			{ID: "evt-1", SessionID: "s1", RunID: "run-1", EventType: "changed", Capability: "browser", OldValue: "false", NewValue: "true"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/capability-events", nil)
	w := httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Events []openaiapi.SessionCapabilityEventEntry `json:"events"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].Capability != "browser" || got.Events[0].NewValue != "true" {
		t.Fatalf("events = %#v", got.Events)
	}
}

func TestHandleSessionByID_SessionRunsRoutes(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	sessions := &fakeActiveSessionManager{
		runs: []session.SessionRun{
			{ID: "run-1", SessionID: "s1", Status: "completed"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/runs?limit=5", nil)
	w := httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET runs status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if sessions.runsID != "s1" || sessions.runsLimit != 5 {
		t.Fatalf("ListSessionRuns args = (%q, %d), want (s1, 5)", sessions.runsID, sessions.runsLimit)
	}
	var got struct {
		SessionID string               `json:"sessionId"`
		Runs      []session.SessionRun `json:"runs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.SessionID != "s1" || len(got.Runs) != 1 || got.Runs[0].ID != "run-1" {
		t.Fatalf("runs response = %#v", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/sessions/s1/runs", strings.NewReader(`{"message":"hi"}`))
	w = httptest.NewRecorder()
	rt.handleSessionByID(sessions).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("POST runs status = %d, want 202", w.Code)
	}
	if !sessions.submittedRun {
		t.Fatal("POST /api/sessions/s1/runs should delegate to HandleSubmitRun")
	}
}

func TestHandleMemoryDisabled(t *testing.T) {

	cfg := DefaultConfig()
	cfg.Features.Memory = false
	rt := &channelRuntime{cfg: cfg}

	req := httptest.NewRequest(http.MethodGet, "/api/memory", nil)
	w := httptest.NewRecorder()
	rt.handleMemory(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("GET body = %s", w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/memory", strings.NewReader(`{"content":"# Memory"}`))
	w = httptest.NewRecorder()
	rt.handleMemory(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("PUT status = %d, want 403", w.Code)
	}
}

func TestHandleMemoryReadWrite(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Features.Memory = true
	cfg.Memory.Enabled = true
	cfg.Memory.Path = filepath.Join(t.TempDir(), "memory.md")
	rt := &channelRuntime{cfg: cfg}

	req := httptest.NewRequest(http.MethodPut, "/api/memory", strings.NewReader(`{"content":"# Memory\n\nhello"}`))
	w := httptest.NewRecorder()
	rt.handleMemory(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", w.Code, w.Body.String())
	}
	if data, err := os.ReadFile(cfg.Memory.Path); err != nil || string(data) != "# Memory\n\nhello" {
		t.Fatalf("memory file = %q, err = %v", data, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/memory", nil)
	w = httptest.NewRecorder()
	rt.handleMemory(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", w.Code)
	}
	var got struct {
		Enabled bool   `json:"enabled"`
		Path    string `json:"path"`
		Source  string `json:"source"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode memory response: %v", err)
	}
	if !got.Enabled || got.Path != cfg.Memory.Path || got.Source != "explicit" || got.Content != "# Memory\n\nhello" {
		t.Fatalf("memory response = %#v", got)
	}
}

func TestHandleCronDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Features.Cron = false
	rt := &channelRuntime{cfg: cfg}

	req := httptest.NewRequest(http.MethodGet, "/api/cron", nil)
	w := httptest.NewRecorder()
	rt.handleCron(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("GET body = %s", w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/cron", strings.NewReader(`{"name":"n","prompt":"p"}`))
	w = httptest.NewRecorder()
	rt.handleCron(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("POST status = %d, want 403", w.Code)
	}
}

func TestHandleCronCreateListUpdateDelete(t *testing.T) {
	sessionDir := t.TempDir()
	path := filepath.Join(sessionDir, "sessions.db")
	sessionID := "cron-session"
	sessionWorkDir := t.TempDir()
	mgr := session.New(sessionWorkDir, sessionDir)
	if err := mgr.InitWithID(sessionID); err != nil {
		t.Fatalf("init session: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Features.Cron = true
	cfg.Cron.Enabled = true
	rt := &channelRuntime{cfg: cfg, cronStore: cron.NewSQLiteCronStore(sessionDir), cronStorePath: path, sessionDir: sessionDir}

	req := httptest.NewRequest(http.MethodPost, "/api/cron", strings.NewReader(`{"sessionId":"cron-session","name":"daily","prompt":"summarize","schedule":"@daily","mode":"agent"}`))
	w := httptest.NewRecorder()
	rt.handleCron(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", w.Code, w.Body.String())
	}
	var created struct {
		Job cron.CronJob `json:"job"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created job: %v", err)
	}
	if created.Job.ID == "" || created.Job.OneShot || created.Job.NextRun.IsZero() || created.Job.Mode != "agent" {
		t.Fatalf("created job = %#v", created.Job)
	}
	if created.Job.WorkDir != sessionWorkDir {
		t.Fatalf("created job workDir = %q, want %q", created.Job.WorkDir, sessionWorkDir)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/cron?sessionId="+sessionID, nil)
	w = httptest.NewRecorder()
	rt.handleCron(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", w.Code)
	}
	var status cronAPIResponse
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("decode cron status: %v", err)
	}
	if !status.Enabled || status.Path != path || len(status.Jobs) != 1 || status.Jobs[0].ID != created.Job.ID {
		t.Fatalf("cron status = %#v", status)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/cron/"+created.Job.ID+"?sessionId="+sessionID, strings.NewReader(`{"enabled":false}`))
	w = httptest.NewRecorder()
	rt.handleCronByID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", w.Code, w.Body.String())
	}
	updated, err := rt.cronStore.Get(created.Job.ID)
	if err != nil {
		t.Fatalf("get updated job: %v", err)
	}
	if updated.Enabled {
		t.Fatalf("job should be disabled: %#v", updated)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/cron/"+created.Job.ID+"?sessionId="+sessionID, nil)
	w = httptest.NewRecorder()
	rt.handleCronByID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %s", w.Code, w.Body.String())
	}
	jobs, err := rt.cronStore.List()
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("jobs after delete = %#v", jobs)
	}
}

func TestHandleCronCreateRejectsInvalidSchedule(t *testing.T) {
	sessionDir := t.TempDir()
	path := filepath.Join(sessionDir, "sessions.db")
	cfg := DefaultConfig()
	cfg.Features.Cron = true
	cfg.Cron.Enabled = true
	rt := &channelRuntime{cfg: cfg, cronStore: cron.NewSQLiteCronStore(sessionDir), cronStorePath: path, sessionDir: sessionDir}

	req := httptest.NewRequest(http.MethodPost, "/api/cron", strings.NewReader(`{"sessionId":"cron-session","name":"bad","prompt":"run","schedule":"never"}`))
	w := httptest.NewRecorder()
	rt.handleCron(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

func TestHandleCronRedactsA2AToken(t *testing.T) {
	sessionDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Features.Cron = true
	cfg.Cron.Enabled = true
	rt := &channelRuntime{cfg: cfg, cronStore: cron.NewSQLiteCronStore(sessionDir), sessionDir: sessionDir}

	secret := "a2a-secret-value"
	req := httptest.NewRequest(http.MethodPost, "/api/cron", strings.NewReader(`{"sessionId":"cron-session","name":"remote","prompt":"run","a2aTarget":"https://example.test","a2aToken":"`+secret+`"}`))
	w := httptest.NewRecorder()
	rt.handleCron(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Fatalf("response leaked A2A token: %s", w.Body.String())
	}
}

func TestHandleCronRejectsWorkDirOutsideAllowlist(t *testing.T) {
	sessionDir := t.TempDir()
	allowedDir := t.TempDir()
	outsideDir := t.TempDir()
	allowed := []string{allowedDir}
	cfg := DefaultConfig()
	cfg.Features.Cron = true
	cfg.Cron.Enabled = true
	cfg.API.AllowedWorkDirs = &allowed
	rt := &channelRuntime{cfg: cfg, cronStore: cron.NewSQLiteCronStore(sessionDir), sessionDir: sessionDir}

	body := `{"sessionId":"cron-session","name":"outside","prompt":"run","workDir":"` + outsideDir + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/cron", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.handleCron(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", w.Code, w.Body.String())
	}
}

func TestLogHubPublishesLogEvents(t *testing.T) {
	hub := newLogHub()
	defer hub.close()
	ch, _, unsubscribe := hub.subscribe()
	defer unsubscribe()

	if _, err := hub.Write([]byte("serve log line\n")); err != nil {
		t.Fatalf("write log hub: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Type != "log" || ev.Message != "serve log line" || ev.Timestamp.IsZero() {
			t.Fatalf("log event = %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log event")
	}
}

func TestLogHubReplaysRecentHistory(t *testing.T) {
	hub := newLogHub()
	defer hub.close()
	hub.historySize = 2

	hub.publish(serveLogEvent{Type: "log", Message: "first", Timestamp: time.Now()})
	hub.publish(serveLogEvent{Type: "log", Message: "second", Timestamp: time.Now()})
	hub.publish(serveLogEvent{Type: "log", Message: "third", Timestamp: time.Now()})

	_, history, unsubscribe := hub.subscribe()
	defer unsubscribe()

	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	if history[0].Message != "second" || history[1].Message != "third" {
		t.Fatalf("history = %#v", history)
	}
}

func TestUIHandlerMissingAssetsReturnsServiceUnavailable(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	uiHandler(filepath.Join(t.TempDir(), "missing-dist")).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Web UI assets not found") {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestUIHandlerServesEmbeddedDefault(t *testing.T) {
	if _, err := fs.Stat(webui.DistFS(), "index.html"); err != nil {
		t.Skip("embedded Web UI not built; run `make ui-build` to exercise this path")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	uiHandler(defaultWebUIDir).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "MothX Serve") {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestUIHandlerServesIndexFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<main>ok</main>"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/nested/route", nil)
	w := httptest.NewRecorder()
	uiHandler(dir).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<main>ok</main>") {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestHandleWebUIReflectsCurrentConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<main>ok</main>"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	rt := &channelRuntime{cfg: &Config{WebUI: WebUIConfig{Enabled: false, Dir: dir}}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	rt.handleWebUI(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled status = %d, want 404", w.Code)
	}

	rt.cfg.WebUI.Enabled = true
	w = httptest.NewRecorder()
	rt.handleWebUI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("enabled status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<main>ok</main>") {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestHandleBrowseUsesDefaultWorkDirAsStartWithoutAllowlist(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "child"), 0700); err != nil {
		t.Fatalf("create child: %v", err)
	}
	cfg := DefaultConfig()
	cfg.API.DefaultWorkDir = workDir
	rt := &channelRuntime{cfg: cfg}

	req := httptest.NewRequest(http.MethodGet, "/api/browse", nil)
	w := httptest.NewRecorder()
	rt.handleBrowse(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("default status = %d, body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Path       string `json:"path"`
		Parent     string `json:"parent"`
		Selectable bool   `json:"selectable"`
		Entries    []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode browse response: %v", err)
	}
	if got.Path != workDir {
		t.Fatalf("browse root path=%q, want %q", got.Path, workDir)
	}
	if !got.Selectable {
		t.Fatal("ordinary directory should be selectable")
	}
	if len(got.Entries) != 1 || got.Entries[0].Name != "child" {
		t.Fatalf("entries = %#v", got.Entries)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/browse?path="+url.QueryEscape(outside), nil)
	w = httptest.NewRecorder()
	rt.handleBrowse(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("outside status = %d, want 200 without allowlist; body = %s", w.Code, w.Body.String())
	}

	linkPath := filepath.Join(workDir, "outside-link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Logf("skip symlink browse assertion: %v", err)
	} else {
		req = httptest.NewRequest(http.MethodGet, "/api/browse?path="+url.QueryEscape(linkPath), nil)
		w = httptest.NewRecorder()
		rt.handleBrowse(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("symlink outside status = %d, want 200 without allowlist; body = %s", w.Code, w.Body.String())
		}
	}
}

func TestHandleSelectDirectoryUsesNativePickerAndValidatesSelection(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "project")
	if err := os.Mkdir(selected, 0700); err != nil {
		t.Fatalf("create selected directory: %v", err)
	}
	defaultPath := filepath.Join(root, "missing")
	var pickerDefault string
	rt := &channelRuntime{
		cfg: DefaultConfig(),
		nativeDirectoryPicker: func(_ context.Context, gotDefault string) (string, error) {
			pickerDefault = gotDefault
			return selected, nil
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/select-directory", bytes.NewBufferString(`{"defaultPath":"`+defaultPath+`"}`))
	w := httptest.NewRecorder()
	rt.handleSelectDirectory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if pickerDefault != root {
		t.Fatalf("picker default = %q, want nearest existing directory %q", pickerDefault, root)
	}
	var got struct {
		Canceled bool   `json:"canceled"`
		Path     string `json:"path"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Canceled || got.Path != selected {
		t.Fatalf("response = %#v, want selected path %q", got, selected)
	}
}

func TestHandleSelectDirectoryReturnsCanceledPickerResult(t *testing.T) {
	rt := &channelRuntime{
		cfg:                   DefaultConfig(),
		nativeDirectoryPicker: func(context.Context, string) (string, error) { return "", nil },
	}
	req := httptest.NewRequest(http.MethodPost, "/api/select-directory", nil)
	w := httptest.NewRecorder()
	rt.handleSelectDirectory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Canceled bool `json:"canceled"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Canceled {
		t.Fatal("expected canceled picker result")
	}
}

func TestHandleBrowseFallsBackWhenDefaultWorkDirMissing(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "projects", "missing")
	if err := os.MkdirAll(filepath.Join(parent, "child"), 0700); err != nil {
		t.Fatalf("create child: %v", err)
	}

	cfg := DefaultConfig()
	cfg.API.DefaultWorkDir = missing
	rt := &channelRuntime{cfg: cfg}

	req := httptest.NewRequest(http.MethodGet, "/api/browse", nil)
	w := httptest.NewRecorder()
	rt.handleBrowse(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("default missing status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode browse response: %v", err)
	}
	if got.Path != parent {
		t.Fatalf("browse path = %q, want nearest existing parent %q", got.Path, parent)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/browse?path="+url.QueryEscape(missing), nil)
	w = httptest.NewRecorder()
	rt.handleBrowse(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("explicit missing status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

func TestHandleBrowseUsesAllowedWorkDirs(t *testing.T) {
	allowedRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(allowedRoot, "repo"), 0700); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	cfg := DefaultConfig()
	cfg.API.DefaultWorkDir = t.TempDir()
	allowed := []string{allowedRoot}
	cfg.API.AllowedWorkDirs = &allowed
	rt := &channelRuntime{cfg: cfg}

	req := httptest.NewRequest(http.MethodGet, "/api/browse?path="+url.QueryEscape(filepath.Join(allowedRoot, "repo")), nil)
	w := httptest.NewRecorder()
	rt.handleBrowse(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("allowed status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestResolveWebUIDirRelativeToWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	uiDir := filepath.Join(dir, "custom-ui")
	if err := os.MkdirAll(uiDir, 0700); err != nil {
		t.Fatalf("create ui dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "index.html"), []byte("<main>ok</main>"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	t.Chdir(dir)

	got := resolveWebUIDir("custom-ui")
	if got != uiDir {
		t.Fatalf("resolveWebUIDir = %q, want %q", got, uiDir)
	}
}

func TestHandleServeConfig_PutAcceptsFlatSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.json")
	rt := &channelRuntime{cfg: DefaultConfig()}

	body := `{
		"listen": ":9099",
		"features": {
			"webUI": false,
			"openaiAPI": false,
			"wechat": true
		},
		"channels": {
			"wechat": {"autoTyping": false}
		}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/serve/config", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.handleServeConfig(path).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if rt.cfg.API.Listen != ":9099" {
		t.Fatalf("listen = %q, want :9099", rt.cfg.API.Listen)
	}
	if rt.cfg.Features.WebUI {
		t.Fatal("webUI feature should be false after PUT")
	}
	if rt.cfg.Features.OpenAIAPI {
		t.Fatal("openaiAPI feature should be false after PUT")
	}
	if !rt.cfg.Channels.Wechat.Enabled {
		t.Fatal("wechat should be enabled after PUT")
	}
	if rt.cfg.Channels.Wechat.AutoTyping {
		t.Fatal("wechat autoTyping should be false after PUT")
	}

	saved, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("LoadConfigFrom(saved): %v", err)
	}
	if saved.API.Listen != ":9099" || saved.Features.OpenAIAPI {
		t.Fatalf("saved config = %#v", saved)
	}
}

func TestApplyRuntimeFeaturesOverridesLegacyRuntimeFields(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Features.Wechat = false
	cfg.Features.Feishu = false
	cfg.Features.Cron = false
	cfg.Features.Memory = false
	cfg.Features.MultiAgent = false
	cfg.Features.WebUI = false
	cfg.Channels.Wechat.Enabled = true
	cfg.Channels.Feishu.Enabled = true
	cfg.Cron.Enabled = true
	cfg.Memory.Enabled = true
	cfg.API.EnableSubAgents = true
	cfg.WebUI.Enabled = true

	applyRuntimeFeatures(cfg)

	if cfg.Channels.Wechat.Enabled {
		t.Fatal("wechat should be disabled by features")
	}
	if cfg.Channels.Feishu.Enabled {
		t.Fatal("feishu should be disabled by features")
	}
	if cfg.Cron.Enabled {
		t.Fatal("cron should be disabled by features")
	}
	if cfg.Memory.Enabled {
		t.Fatal("memory should be disabled by features")
	}
	if cfg.API.EnableSubAgents {
		t.Fatal("multi-agent should be disabled by features")
	}
	if cfg.WebUI.Enabled {
		t.Fatal("webUI should be disabled by features")
	}
}

func TestApplyOverridesPreservesMultiAgentFlagThroughFeatureSync(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Features.MultiAgent = false
	cfg.API.EnableSubAgents = false

	applyOverrides(cfg, RunOptions{MultiAgent: true})
	applyRuntimeFeatures(cfg)

	if !cfg.Features.MultiAgent {
		t.Fatal("multi-agent feature should be enabled by CLI override")
	}
	if !cfg.API.EnableSubAgents {
		t.Fatal("API subagents should remain enabled after feature sync")
	}
}

func TestApplyOverridesEnablesExtendedRuntimeTools(t *testing.T) {
	cfg := DefaultConfig()

	applyOverrides(cfg, RunOptions{WebSearch: true, Browser: true, Artifact: true, A2AMaster: true})

	if !cfg.API.EnableWebSearch {
		t.Fatal("web search should be enabled")
	}
	if !cfg.API.EnableBrowser {
		t.Fatal("browser should be enabled")
	}
	if !cfg.API.EnableArtifact {
		t.Fatal("WebUI/API artifacts should be enabled")
	}
	if !cfg.API.EnableA2AMaster {
		t.Fatal("A2A master should be enabled")
	}
}

func TestApplyOverridesWebUIDirEnablesWebUI(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WebUI.Enabled = false
	cfg.Features.WebUI = false

	applyOverrides(cfg, RunOptions{WebUIDir: "/tmp/mothx-ui"})
	applyRuntimeFeatures(cfg)

	if cfg.WebUI.Dir != "/tmp/mothx-ui" {
		t.Fatalf("webUI dir = %q, want /tmp/mothx-ui", cfg.WebUI.Dir)
	}
	if !cfg.WebUI.Enabled || !cfg.Features.WebUI {
		t.Fatalf("webUI should be enabled, config = %#v features = %#v", cfg.WebUI, cfg.Features)
	}
}

func TestApplyOverridesWebUIDirCanOverrideDefaultPathFromDisk(t *testing.T) {
	cfg := DefaultConfig()

	applyOverrides(cfg, RunOptions{WebUIDir: defaultWebUIDir})

	if !filepath.IsAbs(cfg.WebUI.Dir) {
		t.Fatalf("webUI dir = %q, want absolute path", cfg.WebUI.Dir)
	}
	if useEmbeddedWebUI(cfg.WebUI.Dir) {
		t.Fatalf("webUI dir = %q should force disk assets", cfg.WebUI.Dir)
	}
}

func TestApplyOverridesPortForms(t *testing.T) {
	tests := []struct {
		name string
		port string
		want string
	}{
		{name: "port only", port: "9090", want: ":9090"},
		{name: "colon port", port: ":9090", want: ":9090"},
		{name: "host port", port: "127.0.0.1:9090", want: "127.0.0.1:9090"},
		{name: "all interfaces host port", port: "0.0.0.0:9090", want: "0.0.0.0:9090"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			applyOverrides(cfg, RunOptions{Port: tt.port})
			if cfg.API.Listen != tt.want {
				t.Fatalf("listen = %q, want %q", cfg.API.Listen, tt.want)
			}
		})
	}
}

func TestApplyOverridesUnsafeDisablesAuthAndExposesListen(t *testing.T) {
	cfg := DefaultConfig()
	cfg.API.Listen = ":9090"
	cfg.API.Auth = openaiapi.AuthConfig{Enabled: true, Tokens: []string{"sk-test"}}

	applyOverrides(cfg, RunOptions{Unsafe: true})

	if cfg.API.Auth.Enabled || len(cfg.API.Auth.Tokens) != 0 {
		t.Fatalf("auth = %#v, want disabled with no tokens", cfg.API.Auth)
	}
	if cfg.API.Listen != "0.0.0.0:9090" {
		t.Fatalf("listen = %q, want 0.0.0.0:9090", cfg.API.Listen)
	}
}

func TestDisplayListenAddrPreservesExplicitExternalBind(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{addr: ":8080", want: "127.0.0.1:8080"},
		{addr: "0.0.0.0:8080", want: "0.0.0.0:8080"},
		{addr: "127.0.0.1:8080", want: "127.0.0.1:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := displayListenAddr(tt.addr); got != tt.want {
				t.Fatalf("displayListenAddr(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestBuildConfigFromServeConfigAppliesFeatureGating(t *testing.T) {
	cfg := DefaultConfig()
	cfg.API.Provider = "openai"
	cfg.API.Model = "gpt-4o"
	cfg.API.DefaultWorkDir = "/tmp/project"
	cfg.API.Sandbox.Enabled = true
	cfg.API.EnableWebSearch = true
	cfg.API.EnableBrowser = true
	cfg.API.EnableA2AMaster = true
	cfg.Channels.Artifact = true
	cfg.Features.MultiAgent = true
	cfg.Features.Wechat = false
	cfg.Features.Feishu = true
	cfg.Features.Cron = false
	cfg.Features.Memory = true
	cfg.Channels.Wechat.Enabled = true
	cfg.Channels.Wechat.AutoTyping = false
	cfg.Channels.Feishu.Enabled = false
	cfg.Channels.Feishu.AppID = "app-id"
	cfg.Channels.Feishu.AppSecret = "secret"
	cfg.Cron.Enabled = true
	cfg.Memory.Enabled = false
	cfg.Memory.Path = "/tmp/memory.md"
	cfg.Agent.MaxTurns = 33

	hCfg := buildConfigFromServeConfig(cfg)

	if hCfg.DefaultProvider != "openai" || hCfg.DefaultModel != "gpt-4o" {
		t.Fatalf("provider/model = %q/%q", hCfg.DefaultProvider, hCfg.DefaultModel)
	}
	if hCfg.WorkDir != "/tmp/project" {
		t.Fatalf("workDir = %q, want /tmp/project", hCfg.WorkDir)
	}
	if !hCfg.Sandbox {
		t.Fatal("sandbox should be enabled")
	}
	if !hCfg.WebSearch {
		t.Fatal("web search should be enabled")
	}
	if !hCfg.Browser {
		t.Fatal("browser should be enabled")
	}
	if !hCfg.Artifact {
		t.Fatal("channel artifacts should be enabled independently from WebUI/API")
	}
	if !hCfg.A2AMaster {
		t.Fatal("A2A master should be enabled")
	}
	if !hCfg.MultiAgent {
		t.Fatal("multi-agent should be enabled")
	}
	if hCfg.Wechat.Enabled {
		t.Fatal("wechat should be disabled by features")
	}
	if !hCfg.Feishu.Enabled || hCfg.Feishu.AppID != "app-id" || hCfg.Feishu.AppSecret != "secret" {
		t.Fatalf("feishu = %#v", hCfg.Feishu)
	}
	if hCfg.Cron.Enabled {
		t.Fatal("cron should be disabled by features")
	}
	if !hCfg.Memory.Enabled || hCfg.Memory.Path != "/tmp/memory.md" {
		t.Fatalf("memory = %#v", hCfg.Memory)
	}
	if hCfg.Agent.MaxTurns != 33 {
		t.Fatalf("agent max turns = %d, want 33", hCfg.Agent.MaxTurns)
	}
}

func TestBuildCronStoreHonorsCronFeature(t *testing.T) {
	settings := configpkg.DefaultSettings()
	settings.SessionDir = t.TempDir()
	disabled := buildCronStore(&channels.Config{Cron: channels.CronConfig{Enabled: false}}, settings)
	if disabled != nil {
		t.Fatal("cron store should be nil when cron is disabled")
	}

	enabled := buildCronStore(&channels.Config{Cron: channels.CronConfig{Enabled: true}}, settings)
	if enabled == nil {
		t.Fatal("cron store should be created when cron is enabled")
	}
}

// TestPlaceholderAuthTokenDetection guards the warning contract for the token
// written by `mothx serve init-config`: it must be recognized as long as the
// operator has not replaced it, and the generated template must keep using the
// same constant so the check cannot drift.
func TestPlaceholderAuthTokenDetection(t *testing.T) {
	if !IsPlaceholderAuthToken(PlaceholderAuthToken) {
		t.Fatal("the generated template token must be recognized as a placeholder")
	}
	if !IsPlaceholderAuthToken("  " + PlaceholderAuthToken + "\n") {
		t.Fatal("surrounding whitespace must not hide a placeholder token")
	}
	if IsPlaceholderAuthToken("sk-private-token") || IsPlaceholderAuthToken("") {
		t.Fatal("a real or empty token must not be reported as the placeholder")
	}

	templateCfg := DefaultConfig()
	templateCfg.API.Auth.Enabled = true
	templateCfg.API.Auth.Tokens = []string{PlaceholderAuthToken}
	if !UsesPlaceholderAuthToken(templateCfg) {
		t.Fatal("a config that still ships the template token must trigger the warning")
	}

	replaced := DefaultConfig()
	replaced.API.Auth.Enabled = true
	replaced.API.Auth.Tokens = []string{"sk-private-token"}
	if UsesPlaceholderAuthToken(replaced) {
		t.Fatal("a replaced token must not trigger the warning")
	}

	disabled := DefaultConfig()
	disabled.API.Auth.Enabled = false
	disabled.API.Auth.Tokens = []string{PlaceholderAuthToken}
	if UsesPlaceholderAuthToken(disabled) {
		t.Fatal("auth-disabled configs must not warn about a token they do not use")
	}
	if UsesPlaceholderAuthToken(nil) {
		t.Fatal("nil config must not trigger the warning")
	}
}

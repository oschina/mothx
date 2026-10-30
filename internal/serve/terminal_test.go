package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/serve/openaiapi"
	"github.com/oschina/mothx/internal/terminal"
	"golang.org/x/net/websocket"
)

func TestEffectiveTerminalEnabled(t *testing.T) {
	cases := []struct {
		name    string
		listen  string
		auth    openaiapi.AuthConfig
		enabled *bool
		want    bool
		reason  string
	}{
		{name: "loopback", listen: "127.0.0.1:7872", want: true},
		{name: "localhost", listen: "localhost:7872", want: true},
		{name: "non-loopback no auth", listen: "0.0.0.0:7872", want: false, reason: "unsafe"},
		{name: "non-loopback auth", listen: "0.0.0.0:7872", auth: openaiapi.AuthConfig{Enabled: true, Tokens: []string{"secret"}}, want: true},
		{name: "non-loopback auth no token", listen: "0.0.0.0:7872", auth: openaiapi.AuthConfig{Enabled: true}, want: false, reason: "unsafe"},
		{name: "explicit disabled", listen: "127.0.0.1:7872", enabled: boolPtr(false), want: false, reason: "disabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.API.Listen = tc.listen
			cfg.API.Auth = tc.auth
			cfg.Terminal.Enabled = tc.enabled
			got, reason := EffectiveTerminalEnabled(cfg)
			if got != tc.want || reason != tc.reason {
				t.Fatalf("EffectiveTerminalEnabled() = (%v, %q), want (%v, %q)", got, reason, tc.want, tc.reason)
			}
		})
	}
}

func TestEffectiveTerminalEnabledWarnsPlaceholderToken(t *testing.T) {
	cfg := DefaultConfig()
	cfg.API.Listen = "0.0.0.0:7872"
	cfg.API.Auth = openaiapi.AuthConfig{Enabled: true, Tokens: []string{PlaceholderAuthToken}}
	got, reason := EffectiveTerminalEnabled(cfg)
	if !got || reason != "placeholder_token" {
		t.Fatalf("got (%v, %q), want (true, placeholder_token)", got, reason)
	}
}

func TestNormalizeTerminalConfig(t *testing.T) {
	cfg := TerminalConfig{
		MaxSessions:          100,
		MaxPerSession:        100,
		ScrollbackBytes:      1,
		MaxOutputBytesPerSec: 1,
		AllowedOrigins:       []string{"", "not-an-origin", "https://example.test"},
	}
	normalizeTerminalConfig(&cfg)
	if cfg.MaxSessions != 32 || cfg.MaxPerSession != 4 {
		t.Fatalf("limits = %d/%d", cfg.MaxSessions, cfg.MaxPerSession)
	}
	if cfg.ScrollbackBytes < 64<<10 || cfg.MaxOutputBytesPerSec < 64<<10 {
		t.Fatalf("byte limits = %d/%d", cfg.ScrollbackBytes, cfg.MaxOutputBytesPerSec)
	}
	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != "https://example.test" {
		t.Fatalf("origins = %#v", cfg.AllowedOrigins)
	}
}

func TestTerminalOriginAllowed(t *testing.T) {
	rt := &channelRuntime{cfg: DefaultConfig()}
	rt.cfg.API.Listen = "127.0.0.1:7872"
	req := httptest.NewRequest("GET", "http://127.0.0.1:7872/ws/terminals/t_1", nil)
	req.Host = "127.0.0.1:7872"
	if !rt.terminalOriginAllowed(req, "http://127.0.0.1:7872") {
		t.Fatal("expected same-origin to be allowed")
	}
	if rt.terminalOriginAllowed(req, "http://evil.test") {
		t.Fatal("expected cross-origin to be rejected")
	}
	rt.cfg.Terminal.AllowedOrigins = []string{"https://proxy.test"}
	if !rt.terminalOriginAllowed(req, "https://proxy.test") {
		t.Fatal("expected configured origin to be allowed")
	}
}

func boolPtr(v bool) *bool { return &v }

func TestTerminalHandshakeRejectsDisabledFeature(t *testing.T) {
	cfg := DefaultConfig()
	cfg.API.Listen = "127.0.0.1:7872"
	cfg.Terminal.Enabled = boolPtr(false)
	rt := &channelRuntime{cfg: cfg, terminals: terminal.New(cfg.Terminal.managerConfig())}
	defer rt.terminals.Shutdown()
	req := httptest.NewRequest("GET", "http://127.0.0.1:7872/ws/terminals/t_1", nil)
	req.Host = "127.0.0.1:7872"
	req.Header.Set("Origin", "http://127.0.0.1:7872")
	if err := rt.terminalHandshake(req); err == nil {
		t.Fatal("expected disabled terminal handshake to be rejected")
	}
	cfg.Terminal.Enabled = nil
	if err := rt.terminalHandshake(req); err != nil {
		t.Fatalf("enabled handshake err = %v, want nil", err)
	}
}

func TestTerminalTokenExposure(t *testing.T) {
	disabledAuth := DefaultConfig()
	if !terminalTokenExposed(httptest.NewRequest("POST", "/api/terminals", nil), disabledAuth) {
		t.Fatal("auth-disabled clients need a terminal token")
	}
	enabledAuth := DefaultConfig()
	enabledAuth.API.Auth = openaiapi.AuthConfig{Enabled: true, Tokens: []string{"secret"}}
	cookieReq := httptest.NewRequest("POST", "/api/terminals", nil)
	cookieReq.AddCookie(&http.Cookie{Name: "mothx_webui_auth", Value: "browser-session"})
	if terminalTokenExposed(cookieReq, enabledAuth) {
		t.Fatal("cookie-authenticated browsers must not receive a terminal token")
	}
	bearerReq := httptest.NewRequest("POST", "/api/terminals", nil)
	bearerReq.Header.Set("Authorization", "Bearer secret")
	if !terminalTokenExposed(bearerReq, enabledAuth) {
		t.Fatal("Bearer clients need a terminal token for non-browser attach")
	}
}

func TestTerminalCreateListDelete(t *testing.T) {
	if !terminal.Supported() {
		t.Skip("PTY not supported")
	}
	cfg := DefaultConfig()
	cfg.API.Listen = "127.0.0.1:7872"
	cfg.Terminal.Shell = "/bin/sh"
	rt := &channelRuntime{cfg: cfg, terminals: terminal.New(cfg.Terminal.managerConfig())}
	defer rt.terminals.Shutdown()
	dir := t.TempDir()
	sessions := &fakeActiveSessionManager{sessions: []openaiapi.ActiveSessionInfo{{ID: "s1", WorkDir: dir}}}

	body := bytes.NewBufferString(`{"sessionId":"s1","cols":80,"rows":24}`)
	rec := httptest.NewRecorder()
	rt.handleTerminals(sessions)(rec, httptest.NewRequest("POST", "/api/terminals", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created terminalInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.TerminalID == "" || created.TerminalToken == "" || created.SessionID != "s1" {
		t.Fatalf("created = %#v", created)
	}

	rec = httptest.NewRecorder()
	rt.handleTerminals(sessions)(rec, httptest.NewRequest("GET", "/api/terminals?sessionId=s1", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), created.TerminalID) {
		t.Fatalf("GET status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	rt.handleTerminalByID(rec, httptest.NewRequest("DELETE", "/api/terminals/"+created.TerminalID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEffectiveTerminalShell(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "shell")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write shell: %v", err)
	}
	notExecutable := filepath.Join(dir, "plain")
	if err := os.WriteFile(notExecutable, []byte("x"), 0o644); err != nil {
		t.Fatalf("write plain: %v", err)
	}
	fallback := "/bin/sh"
	cases := []struct{ shell, fallback, want string }{
		{shell: "", fallback: fallback, want: fallback},
		{shell: executable, fallback: fallback, want: executable},
		{shell: notExecutable, fallback: fallback, want: fallback},
		{shell: filepath.Join(dir, "missing"), fallback: fallback, want: fallback},
		{shell: "relative/with/slash", fallback: fallback, want: fallback},
		{shell: "", fallback: "", want: ""},
	}
	for _, tc := range cases {
		if got := effectiveTerminalShell(tc.shell, tc.fallback); got != tc.want {
			t.Fatalf("effectiveTerminalShell(%q, %q) = %q, want %q", tc.shell, tc.fallback, got, tc.want)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if resolved := effectiveTerminalShell("shell", ""); resolved != executable {
		t.Fatalf("resolvable basename = %q, want %q", resolved, executable)
	}
}

func TestTerminalConfigRoundTrip(t *testing.T) {
	cfg, err := DecodeConfigBytes([]byte(`{"terminal":{"enabled":false,"shell":"/bin/zsh","maxSessions":3,"allowedOrigins":["https://proxy.test"]}}`))
	if err != nil {
		t.Fatalf("DecodeConfigBytes: %v", err)
	}
	if cfg.Terminal.Enabled == nil || *cfg.Terminal.Enabled {
		t.Fatalf("enabled = %#v", cfg.Terminal.Enabled)
	}
	if cfg.Terminal.MaxSessions != 3 || cfg.Terminal.Shell != "/bin/zsh" || len(cfg.Terminal.AllowedOrigins) != 1 {
		t.Fatalf("terminal = %#v", cfg.Terminal)
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal marshaled config: %v", err)
	}
	tm, ok := raw["terminal"].(map[string]any)
	if !ok {
		t.Fatalf("marshaled config lost terminal section: %s", string(data))
	}
	if enabled, ok := tm["enabled"].(bool); !ok || enabled {
		t.Fatalf("marshaled enabled = %#v; json=%s", tm["enabled"], string(data))
	}
	if tm["shell"] != "/bin/zsh" || tm["maxSessions"] != float64(3) {
		t.Fatalf("marshaled terminal = %#v; json=%s", tm, string(data))
	}

	roundTripped, err := DecodeConfigBytes(data)
	if err != nil {
		t.Fatalf("DecodeConfigBytes(marshaled): %v", err)
	}
	if roundTripped.Terminal.Enabled == nil || *roundTripped.Terminal.Enabled {
		t.Fatalf("round-tripped enabled = %#v", roundTripped.Terminal.Enabled)
	}
	if roundTripped.Terminal.MaxSessions != 3 || roundTripped.Terminal.Shell != "/bin/zsh" || len(roundTripped.Terminal.AllowedOrigins) != 1 {
		t.Fatalf("round-tripped terminal = %#v", roundTripped.Terminal)
	}
}

func TestTerminalConfigMarshalKeepsAutoAndDefaults(t *testing.T) {
	cfg := decodeConfigBytesOrFatal(t, []byte(`{"terminal":{"maxPerSession":1}}`))
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal marshaled config: %v", err)
	}
	tm, ok := raw["terminal"].(map[string]any)
	if !ok {
		t.Fatalf("marshaled config lost terminal section: %s", string(data))
	}
	if _, ok := tm["enabled"]; ok {
		t.Fatalf("auto enabled should stay omitted: %s", string(data))
	}
	if tm["maxPerSession"] != float64(1) || tm["maxSessions"] != float64(8) {
		t.Fatalf("marshaled terminal = %#v; json=%s", tm, string(data))
	}
}

func decodeConfigBytesOrFatal(t *testing.T, data []byte) *Config {
	t.Helper()
	cfg, err := DecodeConfigBytes(data)
	if err != nil {
		t.Fatalf("DecodeConfigBytes: %v", err)
	}
	return cfg
}

func TestTerminalConfigSurvivesSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.json")
	cfg := DefaultConfig()
	disabled := false
	cfg.Terminal.Enabled = &disabled
	cfg.Terminal.Shell = "/bin/bash"
	cfg.Terminal.MaxSessions = 5
	cfg.Terminal.AllowedOrigins = []string{"https://proxy.test"}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	loaded, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if loaded.Terminal.Enabled == nil || *loaded.Terminal.Enabled {
		t.Fatalf("enabled = %#v", loaded.Terminal.Enabled)
	}
	if loaded.Terminal.Shell != "/bin/bash" || loaded.Terminal.MaxSessions != 5 {
		t.Fatalf("terminal = %#v", loaded.Terminal)
	}
	if len(loaded.Terminal.AllowedOrigins) != 1 || loaded.Terminal.AllowedOrigins[0] != "https://proxy.test" {
		t.Fatalf("allowedOrigins = %#v", loaded.Terminal.AllowedOrigins)
	}
}

func TestTerminalWebSocketRoundTrip(t *testing.T) {
	if !terminal.Supported() {
		t.Skip("PTY not supported")
	}
	cfg := DefaultConfig()
	cfg.API.Listen = "127.0.0.1:7872"
	cfg.Terminal.Shell = "/bin/sh"
	rt := &channelRuntime{cfg: cfg, terminals: terminal.New(cfg.Terminal.managerConfig())}
	defer rt.terminals.Shutdown()
	term, err := rt.terminals.Create("s1", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(rt.handleTerminalWebSocket))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/terminals/" + term.ID
	ws, err := websocket.Dial(wsURL, "", server.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(8 * time.Second))

	var out strings.Builder
	attached := false
	for i := 0; i < 6; i++ {
		var frame []byte
		if err := websocket.Message.Receive(ws, &frame); err != nil {
			t.Fatalf("initial receive: %v", err)
		}
		if len(frame) > 0 && frame[0] == terminalFrameControl {
			var control map[string]any
			_ = json.Unmarshal(frame[1:], &control)
			if control["type"] == "attached" {
				attached = true
			}
			if control["type"] == "replay-done" {
				break
			}
		}
	}
	if !attached {
		t.Fatal("did not receive attached control")
	}
	if err := websocket.Message.Send(ws, append([]byte{terminalFrameInput}, []byte("printf 'mothx-ws-ok\\n'\n")...)); err != nil {
		t.Fatalf("send input: %v", err)
	}
	for !strings.Contains(out.String(), "mothx-ws-ok") {
		var frame []byte
		if err := websocket.Message.Receive(ws, &frame); err != nil {
			t.Fatalf("receive output: %v (output=%q)", err, out.String())
		}
		if len(frame) > 0 && frame[0] == terminalFrameOutput {
			out.Write(frame[1:])
		}
	}

	// Text JSON frames are a compatibility/debug path for non-browser clients.
	resizeJSON, _ := json.Marshal(map[string]any{"type": "resize", "cols": 100, "rows": 30})
	if err := websocket.Message.Send(ws, string(resizeJSON)); err != nil {
		t.Fatalf("send text resize: %v", err)
	}
	inputJSON, _ := json.Marshal(map[string]any{"type": "input", "data": "printf 'mothx-text-ok\\n'\n"})
	if err := websocket.Message.Send(ws, string(inputJSON)); err != nil {
		t.Fatalf("send text input: %v", err)
	}
	for !strings.Contains(out.String(), "mothx-text-ok") {
		var frame []byte
		if err := websocket.Message.Receive(ws, &frame); err != nil {
			t.Fatalf("receive text-mode output: %v (output=%q)", err, out.String())
		}
		if len(frame) > 0 && frame[0] == terminalFrameOutput {
			out.Write(frame[1:])
		}
	}
	cols, rows := term.Size()
	if cols != 100 || rows != 30 {
		t.Fatalf("text resize = %dx%d, want 100x30", cols, rows)
	}
}

func TestTerminalUnavailableStatus(t *testing.T) {
	cases := []struct {
		reason string
		status int
		code   string
	}{
		{reason: "unsafe", status: http.StatusForbidden, code: "terminal_unsafe"},
		{reason: "unsupported", status: http.StatusNotImplemented, code: "terminal_unsupported"},
		{reason: "disabled", status: http.StatusForbidden, code: "terminal_disabled"},
		{reason: "", status: http.StatusForbidden, code: "terminal_disabled"},
	}
	for _, tc := range cases {
		status, code := terminalUnavailableStatus(tc.reason)
		if status != tc.status || code != tc.code {
			t.Fatalf("reason %q: got (%d,%q), want (%d,%q)", tc.reason, status, code, tc.status, tc.code)
		}
	}
}

package acp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
)

// Process-level wire coverage of the Phase 3 management plane (§6.3): every
// mothx/manage/* method round-trips over stdio NDJSON, secrets never appear
// in any wire message, and rejections carry stable structured codes.

// manageProcess wraps the shared phase-1 acpProcess helper and records every
// observed wire message so tests can pattern-scan the complete transcript for
// plaintext secrets at the end.
type manageProcess struct {
	*acpProcess
	seen []string
}

func startManageProcess(t *testing.T, configDir, workDir string, extraEnv ...string) *manageProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestACPStdioProcessHelper$")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "MOTHX_ACP_PROCESS_HELPER=1", "MOTHX_DIR="+configDir)
	cmd.Env = append(cmd.Env, extraEnv...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	base := &acpProcess{cmd: cmd, stdin: stdin, reader: bufio.NewReader(stdout)}
	return &manageProcess{acpProcess: base}
}

func (p *manageProcess) read(t *testing.T) map[string]any {
	t.Helper()
	message := p.readMessage(t)
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	p.seen = append(p.seen, string(encoded))
	return message
}

func (p *manageProcess) respondTo(t *testing.T, id float64) map[string]any {
	t.Helper()
	for {
		message := p.read(t)
		if got, ok := message["id"].(float64); ok && got == id {
			return message
		}
	}
}

func (p *manageProcess) result(t *testing.T, id float64) map[string]any {
	t.Helper()
	message := p.respondTo(t, id)
	if errObj := message["error"]; errObj != nil {
		t.Fatalf("request %v failed: %#v", id, errObj)
	}
	result, ok := message["result"].(map[string]any)
	if !ok {
		t.Fatalf("request %v result = %#v, want an object", id, message)
	}
	return result
}

func (p *manageProcess) failure(t *testing.T, id float64) (string, map[string]any) {
	t.Helper()
	message := p.respondTo(t, id)
	errObj, ok := message["error"].(map[string]any)
	if !ok {
		t.Fatalf("request %v = %#v, want an RPC error", id, message)
	}
	data, _ := errObj["data"].(map[string]any)
	code := ""
	if data != nil {
		code, _ = data["code"].(string)
	}
	return code, data
}

func (p *manageProcess) waitSessionEvent(t *testing.T, event string) map[string]any {
	t.Helper()
	for {
		message := p.read(t)
		if message["method"] != "_mothx/session_event" {
			continue
		}
		params, _ := message["params"].(map[string]any)
		if params != nil && params["event"] == event {
			return params
		}
	}
}

func (p *manageProcess) assertNoSecrets(t *testing.T, secrets ...string) {
	t.Helper()
	for _, line := range p.seen {
		for _, secret := range secrets {
			if secret != "" && strings.Contains(line, secret) {
				t.Fatalf("wire transcript leaks secret %q in message %s", secret, line)
			}
		}
	}
}

func writeManageWireSettings(t *testing.T, configDir, baseURL string, mutate func(*config.Settings)) {
	t.Helper()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "manage-wire"
	settings.DefaultModel = "wire-model"
	settings.DefaultMode = "yolo"
	settings.SessionDir = filepath.Join(configDir, "sessions")
	settings.Retry.Enabled = false
	settings.Providers = map[string]*config.ProviderConfig{
		"manage-wire": {
			APIKey:  "sk-wire-PLAINKEY-abcdef123456",
			BaseURL: baseURL,
			API:     "openai-chat",
			Models: []config.ModelConfig{
				{ID: "wire-model", Name: "Wire Model", ContextWindow: 32768, MaxTokens: 1024, Input: []string{"text"}},
				{ID: "wire-mini", Name: "Wire Mini", Input: []string{"text"}},
			},
		},
		"manage-down": {
			APIKey:  "sk-down-DOWNKEY-777666",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "down-model", Name: "Down Model"}},
		},
	}
	if mutate != nil {
		mutate(settings)
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
}

// acpSSETextWithUsage streams one text completion carrying token usage so the
// session run records a request_stats row for the stats projections.
func acpSSETextWithUsage(w http.ResponseWriter, chunkID, text string, input, output int) {
	w.Header().Set("Content-Type", "text/event-stream")
	content, _ := json.Marshal(text)
	fmt.Fprintf(w, "data: {\"id\":%q,\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%s},\"finish_reason\":null}]}\n\n", chunkID, content)
	fmt.Fprintf(w, "data: {\"id\":%q,\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n", chunkID, input, output, input+output)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// --- settings get/patch + providers list/test over the wire -----------------------

func TestACPStdioProcessManageSettingsProvidersAndSecrets(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	var providerCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		acpSSEText(w, "chatcmpl-manage", "pong")
	}))
	defer providerServer.Close()
	writeManageWireSettings(t, configDir, providerServer.URL+"/v1", nil)

	process := startManageProcess(t, configDir, workDir)
	defer process.closeAndWait(t)

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	initialize := process.result(t, 1)
	meta := initialize["_meta"].(map[string]any)[mothxExtensionNamespace].(map[string]any)
	rawFeatures, _ := meta["features"].([]any)
	features := map[string]bool{}
	for _, feature := range rawFeatures {
		if name, ok := feature.(string); ok {
			features[name] = true
		}
	}
	for _, want := range []string{"manageSettings", "manageApplicationSettings", "manageProviders", "manageProviderConfig", "manageSkills", "manageMcp", "manageCron", "manageStats", "manageMemory", "manageSkillHub", "manageSkillHubCatalog", "manageExperts"} {
		if !features[want] {
			t.Fatalf("initialize features = %#v, want %q", rawFeatures, want)
		}
	}

	// settings/get masks every configured key.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "mothx/manage/settings/get", "params": map[string]any{}})
	view := process.result(t, 2)
	if view["defaultProvider"] != "manage-wire" || view["defaultModel"] != "wire-model" || view["defaultMode"] != "yolo" {
		t.Fatalf("settings view = %#v", view)
	}
	providers, _ := view["providers"].([]any)
	var wireView map[string]any
	for _, entry := range providers {
		item, _ := entry.(map[string]any)
		if item["name"] == "manage-wire" {
			wireView = item
		}
	}
	if wireView == nil {
		t.Fatalf("manage-wire missing: %#v", providers)
	}
	if wireView["maskedKey"] != "sk-***456" || wireView["modelCount"] != float64(2) {
		t.Fatalf("manage-wire view = %#v", wireView)
	}
	process.assertNoSecrets(t, "PLAINKEY", "DOWNKEY", "sk-wire-PLAINKEY-abcdef123456")

	// settings/patch: scalar whitelist fields.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "mothx/manage/settings/patch", "params": map[string]any{
		"patch": map[string]any{"defaultModel": "wire-mini", "thinkingLevel": "high", "sandboxEnabled": true, "webSearchEnabled": true},
	}})
	view = process.result(t, 3)
	if view["defaultModel"] != "wire-mini" || view["thinkingLevel"] != "high" || view["sandboxEnabled"] != true || view["webSearchEnabled"] != true {
		t.Fatalf("patched view = %#v", view)
	}
	raw := readManageRawFile(t, config.GlobalSettingsPath())
	var sandbox map[string]any
	if err := json.Unmarshal(raw["sandbox"], &sandbox); err != nil || sandbox["enabled"] != true {
		t.Fatalf("persisted sandbox = %#v (%v)", sandbox, err)
	}
	var webSearch map[string]any
	if err := json.Unmarshal(raw["webSearch"], &webSearch); err != nil || webSearch["enabled"] != true {
		t.Fatalf("persisted webSearch = %#v (%v)", webSearch, err)
	}

	// settings/patch: privileged / non-whitelist fields are rejected.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "mothx/manage/settings/patch", "params": map[string]any{
		"patch": map[string]any{"memoryEnabled": true},
	}})
	code, data := process.failure(t, 4)
	if code != "settings_field_not_allowed" || data["field"] != "memoryEnabled" {
		t.Fatalf("memoryEnabled rejection = %q %#v", code, data)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "mothx/manage/settings/patch", "params": map[string]any{
		"patch": map[string]any{"defaultMode": "turbo"},
	}})
	if code, _ := process.failure(t, 5); code != "settings_field_invalid" {
		t.Fatalf("invalid mode code = %q", code)
	}

	// settings/patch: key rotation lands on disk but only masked on the wire.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "mothx/manage/settings/patch", "params": map[string]any{
		"patch": map[string]any{"providerKey": map[string]any{"name": "manage-wire", "key": "sk-rot-ROTKEY-999888"}},
	}})
	view = process.result(t, 6)
	providers, _ = view["providers"].([]any)
	for _, entry := range providers {
		item, _ := entry.(map[string]any)
		if item["name"] == "manage-wire" && item["maskedKey"] != "sk-***888" {
			t.Fatalf("rotated maskedKey = %#v", item["maskedKey"])
		}
	}
	persisted, err := os.ReadFile(config.GlobalSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persisted), "sk-rot-ROTKEY-999888") {
		t.Fatal("rotated key was not persisted to the settings file")
	}
	process.assertNoSecrets(t, "ROTKEY", "sk-rot-ROTKEY-999888")

	// providers/list projects the shared catalog.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "mothx/manage/providers/list", "params": map[string]any{}})
	catalog := process.result(t, 7)
	models, _ := catalog["models"].([]any)
	found := false
	for _, entry := range models {
		model, _ := entry.(map[string]any)
		if model["provider"] == "manage-wire" && model["id"] == "wire-mini" {
			found = true
		}
	}
	if !found {
		t.Fatalf("catalog = %#v, want manage-wire/wire-mini", models)
	}
	providerConfigs, _ := catalog["providerConfigs"].([]any)
	if len(providerConfigs) == 0 {
		t.Fatalf("catalog missing providerConfigs: %#v", catalog)
	}
	process.assertNoSecrets(t, "PLAINKEY", "DOWNKEY", "ROTKEY", "sk-rot-ROTKEY-999888")

	// providers/save/delete round-trip through the ACP process. API keys are
	// accepted only as write input and never echoed in either catalog response.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 11, "method": "mothx/manage/providers/save", "params": map[string]any{
		"id": "wire-custom", "apiKey": "sk-wire-custom-WIRECUSTOMKEY-123",
		"provider": map[string]any{
			"api": "openai-chat", "baseUrl": providerServer.URL + "/v1",
			"models": []any{map[string]any{"id": "custom-model", "name": "Custom Model", "input": []any{"text"}}},
		},
	}})
	catalog = process.result(t, 11)
	customFound := false
	for _, entry := range catalog["providers"].([]any) {
		item, _ := entry.(map[string]any)
		if item["name"] == "wire-custom" {
			customFound = item["maskedKey"] == "sk-***123"
		}
	}
	if !customFound {
		t.Fatalf("providers/save catalog = %#v", catalog)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 12, "method": "mothx/manage/providers/delete", "params": map[string]any{"id": "wire-custom"}})
	catalog = process.result(t, 12)
	for _, entry := range catalog["providers"].([]any) {
		if entry.(map[string]any)["name"] == "wire-custom" {
			t.Fatalf("providers/delete catalog still contains custom provider: %#v", catalog)
		}
	}
	process.assertNoSecrets(t, "WIRECUSTOMKEY", "sk-wire-custom-WIRECUSTOMKEY-123")

	// providers/test: success path against the fake provider. The default
	// model follows the earlier patch (wire-mini).
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 8, "method": "mothx/manage/providers/test", "params": map[string]any{"provider": "manage-wire"}})
	probe := process.result(t, 8)
	if probe["ok"] != true || probe["model"] != "wire-mini" {
		t.Fatalf("provider probe = %#v", probe)
	}
	if _, ok := probe["latencyMs"].(float64); !ok {
		t.Fatalf("provider probe latency = %#v", probe["latencyMs"])
	}
	if providerCalls.Load() == 0 {
		t.Fatal("provider probe never reached the fake provider")
	}

	// providers/test: failure path stays structured and secret-free.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "mothx/manage/providers/test", "params": map[string]any{"provider": "manage-down"}})
	probe = process.result(t, 9)
	if probe["ok"] != false {
		t.Fatalf("down probe = %#v, want ok=false", probe)
	}
	messageText, _ := probe["error"].(string)
	if strings.TrimSpace(messageText) == "" || strings.Contains(messageText, "DOWNKEY") {
		t.Fatalf("down probe error = %q", messageText)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 10, "method": "mothx/manage/providers/test", "params": map[string]any{"provider": "ghost"}})
	if code, _ := process.failure(t, 10); code != "provider_not_found" {
		t.Fatalf("ghost probe code = %q", code)
	}

	process.assertNoSecrets(t, "PLAINKEY", "DOWNKEY", "ROTKEY",
		"sk-wire-PLAINKEY-abcdef123456", "sk-down-DOWNKEY-777666", "sk-rot-ROTKEY-999888")
}

// --- skills / mcp / memory / stats over the wire ----------------------------------

func TestACPStdioProcessManageMCPProjectScope(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acpSSEText(w, "chatcmpl-manage-project-mcp", "pong")
	}))
	defer providerServer.Close()
	writeManageWireSettings(t, configDir, providerServer.URL+"/v1", nil)

	process := startManageProcess(t, configDir, workDir)
	defer process.closeAndWait(t)
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	process.result(t, 1)
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	sessionResult := process.result(t, 2)
	sessionID, _ := sessionResult["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("session/new result = %#v", sessionResult)
	}

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "mothx/manage/mcp/set", "params": map[string]any{
		"scope": "project", "sessionId": sessionID,
		"servers": []any{map[string]any{"name": "project-server", "type": "stdio", "command": "/bin/project"}},
	}})
	result := process.result(t, 3)
	projectPath := filepath.Join(workDir, config.ProjectMCPPath())
	if result["scope"] != "project" || result["sessionId"] != sessionID || result["path"] != projectPath {
		t.Fatalf("project MCP set response = %#v", result)
	}
	if _, err := config.LoadMCPConfig(projectPath); err != nil {
		t.Fatalf("project MCP file: %v", err)
	}

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "mothx/manage/mcp/list", "params": map[string]any{
		"scope": "project", "sessionId": sessionID,
	}})
	result = process.result(t, 4)
	servers, _ := result["servers"].([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["name"] != "project-server" {
		t.Fatalf("project MCP list response = %#v", result)
	}
}

func TestACPStdioProcessManageSkillsMcpMemoryStats(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acpSSEText(w, "chatcmpl-manage-aux", "pong")
	}))
	defer providerServer.Close()
	writeManageWireSettings(t, configDir, providerServer.URL+"/v1", nil)
	writeManageSkill(t, filepath.Join(configDir, "skills"), "wire-global", "global wire skill")
	writeManageSkill(t, filepath.Join(workDir, ".skills"), "wire-proj", "project wire skill")
	writeManageMCPFile(t, configDir)

	process := startManageProcess(t, configDir, workDir)
	defer process.closeAndWait(t)

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	process.result(t, 1)

	// skills/list discovers global + project skills with enabled defaults.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "mothx/manage/skills/list", "params": map[string]any{}})
	result := process.result(t, 2)
	entries, _ := result["skills"].([]any)
	byName := map[string]map[string]any{}
	for _, entry := range entries {
		item, _ := entry.(map[string]any)
		byName[item["name"].(string)] = item
	}
	if byName["wire-global"] == nil || byName["wire-proj"] == nil {
		t.Fatalf("skills = %#v", entries)
	}
	if byName["wire-global"]["source"] != "global" || byName["wire-proj"]["source"] != "project" {
		t.Fatalf("skill sources = %#v", byName)
	}
	if byName["wire-global"]["enabled"] != true {
		t.Fatalf("default enabled = %#v", byName["wire-global"])
	}

	// skills/set disables and the toggle is visible everywhere.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "mothx/manage/skills/set", "params": map[string]any{"name": "wire-global", "enabled": false}})
	result = process.result(t, 3)
	if result["name"] != "wire-global" || result["enabled"] != false {
		t.Fatalf("skills/set = %#v", result)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "mothx/manage/skills/list", "params": map[string]any{}})
	entries, _ = process.result(t, 4)["skills"].([]any)
	for _, entry := range entries {
		item, _ := entry.(map[string]any)
		if item["name"] == "wire-global" && item["enabled"] != false {
			t.Fatalf("disabled skill projects enabled: %#v", item)
		}
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "mothx/manage/settings/get", "params": map[string]any{}})
	view := process.result(t, 5)
	skillsDisabled, _ := view["skillsDisabled"].([]any)
	if len(skillsDisabled) != 1 || skillsDisabled[0] != "wire-global" {
		t.Fatalf("settings view skillsDisabled = %#v", skillsDisabled)
	}
	raw := readManageRawFile(t, config.GlobalSettingsPath())
	var skillsSection map[string]any
	if err := json.Unmarshal(raw["skills"], &skillsSection); err != nil {
		t.Fatalf("skills section not persisted: %v", err)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "mothx/manage/skills/set", "params": map[string]any{"name": "missing", "enabled": false}})
	if code, _ := process.failure(t, 6); code != "skill_not_found" {
		t.Fatalf("unknown skill code = %q", code)
	}
	// Re-enable and confirm the sparse section is dropped again.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "mothx/manage/skills/set", "params": map[string]any{"name": "wire-global", "enabled": true}})
	process.result(t, 7)
	raw = readManageRawFile(t, config.GlobalSettingsPath())
	if _, ok := raw["skills"]; ok {
		t.Fatalf("empty disabled list must drop the skills key: %#v", raw["skills"])
	}

	// mcp/list returns the complete local MCP configuration.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 8, "method": "mothx/manage/mcp/list", "params": map[string]any{}})
	result = process.result(t, 8)
	servers, _ := result["servers"].([]any)
	if len(servers) != 2 {
		t.Fatalf("mcp servers = %#v", servers)
	}
	keeper, _ := servers[0].(map[string]any)
	env, _ := keeper["env"].([]any)
	if keeper["name"] != "keeper" || len(env) != 1 || env[0].(map[string]any)["value"] != "mcp-super-secret" {
		t.Fatalf("keeper view = %#v", keeper)
	}

	// mcp/set fully replaces the list and updates complete local config values.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "mothx/manage/mcp/set", "params": map[string]any{
		"servers": []any{
			map[string]any{"name": "keeper", "command": "/bin/keep2", "enabled": false,
				"env":     []any{map[string]any{"name": "TOKEN", "value": "rotated-secret"}},
				"headers": []any{map[string]any{"name": "Authorization", "value": "Bearer rotated-header"}}},
			map[string]any{"name": "remote", "type": "http", "url": "https://mcp.example.org/x"},
		},
	}})
	result = process.result(t, 9)
	servers, _ = result["servers"].([]any)
	views := map[string]map[string]any{}
	for _, entry := range servers {
		item, _ := entry.(map[string]any)
		views[item["name"].(string)] = item
	}
	if len(views) != 2 || views["goner"] != nil {
		t.Fatalf("replacement servers = %#v", views)
	}
	if views["keeper"]["command"] != "/bin/keep2" || views["keeper"]["enabled"] != false {
		t.Fatalf("keeper after set = %#v", views["keeper"])
	}
	env, _ = views["keeper"]["env"].([]any)
	if len(env) != 1 || env[0].(map[string]any)["value"] != "rotated-secret" {
		t.Fatalf("keeper env was not updated: %#v", views["keeper"])
	}
	if views["remote"]["type"] != "http" || views["remote"]["url"] != "https://mcp.example.org/x" {
		t.Fatalf("remote = %#v", views["remote"])
	}
	saved, err := config.LoadMCPConfig(config.GlobalMCPPath())
	if err != nil {
		t.Fatal(err)
	}
	var keeperSaved *config.MCPServer
	for index := range saved.MCPServers {
		if saved.MCPServers[index].Name == "keeper" {
			keeperSaved = &saved.MCPServers[index]
		}
	}
	if keeperSaved == nil || len(keeperSaved.Env) != 1 || keeperSaved.Env[0].Value != "rotated-secret" {
		t.Fatalf("keeper env value was not persisted: %#v", keeperSaved)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 10, "method": "mothx/manage/mcp/set", "params": map[string]any{
		"servers": []any{map[string]any{"name": "x", "command": "/bin/x", "headers": []any{map[string]any{"name": "", "value": "x"}}}},
	}})
	if code, _ := process.failure(t, 10); code != "mcp_server_invalid" {
		t.Fatalf("invalid headers rejection = %q", code)
	}

	// memory get/put round trip against the global memory.md.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 11, "method": "mothx/manage/memory/get", "params": map[string]any{}})
	result = process.result(t, 11)
	if result["content"] != "" || result["size"] != float64(0) {
		t.Fatalf("empty memory get = %#v", result)
	}
	wantPath := filepath.Join(configDir, "memory.md")
	if result["path"] != wantPath {
		t.Fatalf("memory path = %#v, want %s", result["path"], wantPath)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 12, "method": "mothx/manage/memory/put", "params": map[string]any{"content": "# Wire Memory\n\nnote"}})
	result = process.result(t, 12)
	if result["size"] != float64(len("# Wire Memory\n\nnote")) {
		t.Fatalf("memory put = %#v", result)
	}
	if updated, _ := result["updatedAt"].(string); updated == "" {
		t.Fatalf("memory put updatedAt = %#v", result)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 13, "method": "mothx/manage/memory/get", "params": map[string]any{}})
	result = process.result(t, 13)
	if result["content"] != "# Wire Memory\n\nnote" {
		t.Fatalf("memory round trip = %#v", result)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 14, "method": "mothx/manage/memory/put", "params": map[string]any{"content": strings.Repeat("m", (1<<20)+1)}})
	if code, data := process.failure(t, 14); code != "memory_too_large" || data["maxBytes"] != float64(1<<20) {
		t.Fatalf("oversize memory = %q %#v", code, data)
	}

	// stats shapes with no recorded usage yet.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 15, "method": "mothx/manage/stats/summary", "params": map[string]any{}})
	summary := process.result(t, 15)
	if summary["sessions"] != float64(0) || summary["runs"] != float64(0) || summary["cost"] != float64(0) {
		t.Fatalf("empty summary = %#v", summary)
	}
	tokens, _ := summary["tokens"].(map[string]any)
	if tokens == nil || tokens["input"] != float64(0) || tokens["output"] != float64(0) {
		t.Fatalf("empty summary tokens = %#v", summary["tokens"])
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 16, "method": "mothx/manage/stats/timeseries", "params": map[string]any{}})
	series := process.result(t, 16)
	points, ok := series["points"].([]any)
	if !ok || len(points) != 0 || series["group"] != "day" {
		t.Fatalf("empty timeseries = %#v", series)
	}
	if _, ok := series["from"].(string); !ok {
		t.Fatalf("timeseries must echo the default window: %#v", series)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 17, "method": "mothx/manage/stats/timeseries", "params": map[string]any{"group": "year"}})
	if code, _ := process.failure(t, 17); code != "stats_group_invalid" {
		t.Fatalf("bad group code = %q", code)
	}

	process.assertNoSecrets(t, "PLAINKEY", "DOWNKEY")
}

// --- cron lifecycle + stats with recorded usage -----------------------------------

func TestACPStdioProcessManageCronRunCompletionAndStats(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acpSSETextWithUsage(w, "chatcmpl-manage-cron", "cron run finished", 11, 7)
	}))
	defer providerServer.Close()
	writeManageWireSettings(t, configDir, providerServer.URL+"/v1", nil)

	process := startManageProcess(t, configDir, workDir)
	defer process.closeAndWait(t)

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	process.result(t, 1)

	// cron/list starts the in-process scheduler idempotently.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "mothx/manage/cron/list", "params": map[string]any{}})
	result := process.result(t, 2)
	if result["enabled"] != true || result["running"] != true {
		t.Fatalf("cron runtime = %#v, want enabled and running", result)
	}
	jobs, _ := result["jobs"].([]any)
	if len(jobs) != 0 {
		t.Fatalf("initial jobs = %#v", jobs)
	}

	// Validation paths.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "mothx/manage/cron/create", "params": map[string]any{"name": "bad", "prompt": "p", "mode": "turbo"}})
	if code, _ := process.failure(t, 3); code != "cron_mode_invalid" {
		t.Fatalf("bad mode code = %q", code)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "mothx/manage/cron/create", "params": map[string]any{"name": "bad", "prompt": "p", "schedule": "@every soon"}})
	if code, _ := process.failure(t, 4); code != "cron_schedule_invalid" {
		t.Fatalf("bad schedule code = %q", code)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "mothx/manage/cron/create", "params": map[string]any{"name": "bad", "prompt": "p", "sessionId": "s"}})
	if code, data := process.failure(t, 5); code != "cron_field_not_allowed" || data["field"] != "sessionId" {
		t.Fatalf("whitelist rejection = %q %#v", code, data)
	}

	// Create a disabled one-shot so only the manual run can execute it.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "mothx/manage/cron/create", "params": map[string]any{
		"name": "wire-manual", "prompt": "say hello", "schedule": "", "mode": "yolo", "enabled": false,
	}})
	result = process.result(t, 6)
	job, _ := result["job"].(map[string]any)
	if job == nil {
		t.Fatalf("create result = %#v", result)
	}
	jobID, _ := job["id"].(string)
	if jobID == "" || job["oneshot"] != true || job["enabled"] != false {
		t.Fatalf("created job = %#v", job)
	}
	if job["workDir"] != workDir {
		t.Fatalf("created job workDir = %#v, want %s", job["workDir"], workDir)
	}
	if _, hasNext := job["nextRun"]; hasNext {
		t.Fatalf("one-shot job must not carry nextRun: %#v", job)
	}
	if _, hasToken := job["a2aToken"]; hasToken {
		t.Fatalf("job view must never project the a2a token: %#v", job)
	}

	// Manual run: immediate trigger + cron_completed projection.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "mothx/manage/cron/run", "params": map[string]any{"id": jobID}})
	result = process.result(t, 7)
	if result["ok"] != true || result["jobId"] != jobID || result["triggered"] != true {
		t.Fatalf("run result = %#v", result)
	}
	event := process.waitSessionEvent(t, "cron_completed")
	if event["jobId"] != jobID || event["status"] != "success" {
		t.Fatalf("cron_completed = %#v", event)
	}
	if _, hasSession := event["sessionId"]; hasSession {
		t.Fatalf("global cron jobs must not carry a sessionId: %#v", event)
	}

	// The run wrote back lastRun/lastStatus/runCount and auto-disabled the
	// one-shot job.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 8, "method": "mothx/manage/cron/list", "params": map[string]any{}})
	result = process.result(t, 8)
	jobs, _ = result["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("jobs after run = %#v", jobs)
	}
	job, _ = jobs[0].(map[string]any)
	if job["lastStatus"] != "success" || job["runCount"] != float64(1) || job["enabled"] != false {
		t.Fatalf("job after run = %#v", job)
	}
	lastRun, _ := job["lastRun"].(string)
	if lastRun == "" {
		t.Fatalf("job after run must carry lastRun: %#v", job)
	}
	if _, err := time.Parse(time.RFC3339, lastRun); err != nil {
		t.Fatalf("lastRun = %q, want RFC3339", lastRun)
	}

	// A second manual run overrides the disabled one-shot and re-executes.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "mothx/manage/cron/run", "params": map[string]any{"id": jobID}})
	process.result(t, 9)
	event = process.waitSessionEvent(t, "cron_completed")
	if event["jobId"] != jobID || event["status"] != "success" {
		t.Fatalf("second cron_completed = %#v", event)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 10, "method": "mothx/manage/cron/list", "params": map[string]any{}})
	jobs, _ = process.result(t, 10)["jobs"].([]any)
	job, _ = jobs[0].(map[string]any)
	if job["runCount"] != float64(2) {
		t.Fatalf("job after second run = %#v", job)
	}

	// Periodic create + update + remove round trip.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 11, "method": "mothx/manage/cron/create", "params": map[string]any{
		"name": "wire-daily", "prompt": "tick", "schedule": "@daily",
	}})
	result = process.result(t, 11)
	daily, _ := result["job"].(map[string]any)
	dailyID, _ := daily["id"].(string)
	if daily["oneshot"] != false || daily["enabled"] != true {
		t.Fatalf("daily job = %#v", daily)
	}
	if nextRun, _ := daily["nextRun"].(string); nextRun == "" {
		t.Fatalf("daily job must carry nextRun: %#v", daily)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 12, "method": "mothx/manage/cron/update", "params": map[string]any{
		"id": dailyID, "name": "wire-daily-2", "enabled": false,
	}})
	result = process.result(t, 12)
	updated, _ := result["job"].(map[string]any)
	if updated["name"] != "wire-daily-2" || updated["enabled"] != false || updated["schedule"] != "@daily" {
		t.Fatalf("updated job = %#v", updated)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 13, "method": "mothx/manage/cron/update", "params": map[string]any{"id": "missing", "name": "x"}})
	if code, _ := process.failure(t, 13); code != "cron_job_not_found" {
		t.Fatalf("unknown update code = %q", code)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 14, "method": "mothx/manage/cron/remove", "params": map[string]any{"id": dailyID}})
	result = process.result(t, 14)
	if result["deleted"] != true || result["id"] != dailyID {
		t.Fatalf("remove result = %#v", result)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 15, "method": "mothx/manage/cron/remove", "params": map[string]any{"id": dailyID}})
	if code, _ := process.failure(t, 15); code != "cron_job_not_found" {
		t.Fatalf("second remove code = %q", code)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 16, "method": "mothx/manage/cron/run", "params": map[string]any{"id": "missing"}})
	if code, _ := process.failure(t, 16); code != "cron_job_not_found" {
		t.Fatalf("unknown run code = %q", code)
	}

	// A session prompt with usage feeds the shared stats tables.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 20, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	sessionID := acpNewSessionID(t, process.respondTo(t, 20))
	process.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 21, "method": "session/prompt",
		"params": map[string]any{"sessionId": sessionID, "prompt": []map[string]any{{"type": "text", "text": "record usage"}}},
	})
	prompt := process.respondTo(t, 21)
	if errObj := prompt["error"]; errObj != nil {
		t.Fatalf("prompt failed: %#v", errObj)
	}

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 22, "method": "mothx/manage/stats/summary", "params": map[string]any{}})
	summary := process.result(t, 22)
	if sessions, _ := summary["sessions"].(float64); sessions < 1 {
		t.Fatalf("summary sessions = %#v", summary["sessions"])
	}
	if runs, _ := summary["runs"].(float64); runs < 1 {
		t.Fatalf("summary runs = %#v", summary["runs"])
	}
	tokens, _ := summary["tokens"].(map[string]any)
	if tokens == nil || tokens["input"].(float64) < 11 || tokens["output"].(float64) < 7 {
		t.Fatalf("summary tokens = %#v", summary["tokens"])
	}

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 23, "method": "mothx/manage/stats/timeseries", "params": map[string]any{}})
	series := process.result(t, 23)
	points, _ := series["points"].([]any)
	if len(points) == 0 {
		t.Fatalf("timeseries points = %#v, want at least one day bucket", series)
	}
	point, _ := points[len(points)-1].(map[string]any)
	if date, _ := point["date"].(string); date == "" {
		t.Fatalf("point = %#v, want a date label", point)
	}
	if runs, _ := point["runs"].(float64); runs < 1 {
		t.Fatalf("point runs = %#v", point)
	}
	if tokens, _ := point["tokens"].(float64); tokens < 18 {
		t.Fatalf("point tokens = %#v", point)
	}
	if _, ok := point["cost"].(float64); !ok {
		t.Fatalf("point cost = %#v", point["cost"])
	}

	process.assertNoSecrets(t, "PLAINKEY", "DOWNKEY", "sk-wire-PLAINKEY-abcdef123456")
}

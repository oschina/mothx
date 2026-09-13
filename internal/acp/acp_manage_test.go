package acp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/mcp"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/skills"
)

// Unit-level coverage of the Phase 3 management plane (mothx/manage/*). The
// process-level wire coverage lives in acp_manage_process_test.go.

func newManageFixtureServer(output *syncedBuffer, cwd string) *server {
	srv := newPhase1FixtureServer(output)
	srv.cwd = cwd
	return srv
}

func callManageFixture(t *testing.T, srv *server, output *syncedBuffer, id int, method string, params any) map[string]any {
	t.Helper()
	output.Reset()
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		raw = data
	}
	srv.handleManageRequest(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(strconv.Itoa(id)), Method: method, Params: raw})
	line := strings.TrimSpace(output.String())
	if line == "" {
		t.Fatalf("method %s produced no response", method)
	}
	var message map[string]any
	if err := json.Unmarshal([]byte(line), &message); err != nil {
		t.Fatalf("parse response of %s: %v (%q)", method, err, line)
	}
	return message
}

func manageFixtureResult(t *testing.T, message map[string]any) map[string]any {
	t.Helper()
	if errObj := message["error"]; errObj != nil {
		t.Fatalf("unexpected RPC error: %#v", errObj)
	}
	result, ok := message["result"].(map[string]any)
	if !ok {
		t.Fatalf("response = %#v, want an object result", message)
	}
	return result
}

func manageFixtureError(t *testing.T, message map[string]any) (string, map[string]any) {
	t.Helper()
	errObj, ok := message["error"].(map[string]any)
	if !ok {
		t.Fatalf("response = %#v, want an RPC error", message)
	}
	data, _ := errObj["data"].(map[string]any)
	code := ""
	if data != nil {
		code, _ = data["code"].(string)
	}
	return code, data
}

func manageRPCErrorData(rpcErr *mcp.RPCError) map[string]any {
	if rpcErr == nil {
		return nil
	}
	data, _ := rpcErr.Data.(map[string]any)
	return data
}

// writeManageSettings seeds an isolated global settings file. Retries stay
// disabled so failure-path probes return promptly.
func writeManageSettings(t *testing.T, configDir string, mutate func(*config.Settings)) *config.Settings {
	t.Helper()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.SessionDir = filepath.Join(configDir, "sessions")
	settings.Retry.Enabled = false
	settings.DefaultProvider = "manage-alpha"
	settings.DefaultModel = "alpha-model"
	settings.Providers = map[string]*config.ProviderConfig{
		"manage-alpha": {
			APIKey:  "sk-alpha-SUPERSECRET-987654",
			BaseURL: "https://alpha.example.com/v1",
			API:     "openai-chat",
			Models: []config.ModelConfig{
				{ID: "alpha-model", Name: "Alpha Model", Input: []string{"text"}},
				{ID: "alpha-mini", Name: "Alpha Mini", Input: []string{"text"}},
			},
		},
		"manage-broken": {
			APIKey:  "sk-broken-ALSOSECRET-111111",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "broken-model", Name: "Broken Model"}},
		},
	}
	if mutate != nil {
		mutate(settings)
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func readManageRawFile(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return raw
}

func manageFindProvider(t *testing.T, result map[string]any, name string) map[string]any {
	t.Helper()
	entries, _ := result["providers"].([]any)
	for _, entry := range entries {
		view, _ := entry.(map[string]any)
		if view["name"] == name {
			return view
		}
	}
	t.Fatalf("provider %q missing from view: %#v", name, entries)
	return nil
}

// --- secret masking helpers ----------------------------------------------------

func TestManageMaskSecretShapes(t *testing.T) {
	if got := manageMaskSecret("sk-alpha-SUPERSECRET-987654"); got != "sk-***654" {
		t.Fatalf("manageMaskSecret = %q, want sk-***654", got)
	}
	if got := manageMaskSecret("short"); got != "***" {
		t.Fatalf("short secret mask = %q, want ***", got)
	}
	if got := manageMaskSecret("1234567"); got != "123***567" {
		t.Fatalf("7-char mask = %q, want 123***567", got)
	}
	if strings.Contains(manageMaskSecret("sk-alpha-SUPERSECRET-987654"), "SUPERSECRET") {
		t.Fatal("masked value leaks the plaintext secret")
	}
}

func TestManageSecretUsableAndRedaction(t *testing.T) {
	for value, want := range map[string]bool{
		"":                     false,
		"   ":                  false,
		"${ANTHROPIC_API_KEY}": false,
		"!pass show":           false,
		"sk-real":              true,
	} {
		if got := manageSecretUsable(value); got != want {
			t.Fatalf("manageSecretUsable(%q) = %v, want %v", value, got, want)
		}
	}
	settings := &config.Settings{Providers: map[string]*config.ProviderConfig{
		"leaky": {APIKey: "sk-leaky-PLAINTEXT-abc"},
	}}
	message := manageRedactSecrets("upstream rejected sk-leaky-PLAINTEXT-abc after 1s", settings)
	if strings.Contains(message, "PLAINTEXT") {
		t.Fatalf("redaction failed: %q", message)
	}
	if !strings.Contains(message, "***") {
		t.Fatalf("redaction should mask, got %q", message)
	}
}

// --- settings get / patch -------------------------------------------------------

func TestManageSettingsGetMasksProviderKeys(t *testing.T) {
	configDir := t.TempDir()
	// Guarantee the built-in preset env fallbacks resolve to "no key" so the
	// null projection is deterministic on machines with ambient credentials.
	t.Setenv("ANTHROPIC_API_KEY", "")
	writeManageSettings(t, configDir, nil)
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/settings/get", map[string]any{}))
	if result["defaultProvider"] != "manage-alpha" || result["defaultModel"] != "alpha-model" {
		t.Fatalf("defaults = %#v", result)
	}
	if result["defaultMode"] != "yolo" {
		t.Fatalf("defaultMode = %#v, want the product default yolo", result["defaultMode"])
	}
	alpha := manageFindProvider(t, result, "manage-alpha")
	if alpha["maskedKey"] != "sk-***654" {
		t.Fatalf("alpha maskedKey = %#v, want sk-***654", alpha["maskedKey"])
	}
	if alpha["apiKeyConfigured"] != true || alpha["isDefault"] != true {
		t.Fatalf("alpha view = %#v", alpha)
	}
	if alpha["baseUrl"] != "https://alpha.example.com/v1" {
		t.Fatalf("alpha baseUrl = %#v", alpha["baseUrl"])
	}
	if alpha["modelCount"] != float64(2) {
		t.Fatalf("alpha modelCount = %#v, want 2", alpha["modelCount"])
	}
	broken := manageFindProvider(t, result, "manage-broken")
	if broken["maskedKey"] != "sk-***111" {
		t.Fatalf("broken maskedKey = %#v", broken["maskedKey"])
	}
	// Built-in preset providers without a resolved env key project null.
	anthropic := manageFindProvider(t, result, "anthropic")
	if anthropic["maskedKey"] != nil || anthropic["apiKeyConfigured"] != false {
		t.Fatalf("anthropic view = %#v, want a null maskedKey", anthropic)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SUPERSECRET", "ALSOSECRET", "sk-alpha-SUPERSECRET-987654", "sk-broken-ALSOSECRET-111111"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("settings view leaks secret %q: %s", secret, encoded)
		}
	}
	if _, ok := result["sandboxEnabled"].(bool); !ok {
		t.Fatalf("sandboxEnabled missing: %#v", result)
	}
	if _, ok := result["webSearchEnabled"].(bool); !ok {
		t.Fatalf("webSearchEnabled missing: %#v", result)
	}
	disabled, ok := result["skillsDisabled"].([]any)
	if !ok || len(disabled) != 0 {
		t.Fatalf("skillsDisabled = %#v, want an empty array", result["skillsDisabled"])
	}
}

func TestManageApplicationSettingsProjectionAndPatchStaySecretSafe(t *testing.T) {
	configDir := t.TempDir()
	writeManageSettings(t, configDir, func(settings *config.Settings) {
		settings.ContextFiles.ExtraFiles = []string{"TEAM.md"}
		settings.ImageGeneration.Token = "img-SUPERSECRET-098765"
		settings.Sandbox.PassEnv = []string{"PRIVATE_TOKEN", "ANOTHER_SECRET"}
		settings.Sandbox.AllowedRead = []string{"/workspace/read"}
	})
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	view := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/application/get", map[string]any{}))
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SUPERSECRET", "PRIVATE_TOKEN", "ANOTHER_SECRET"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("application projection leaked %q: %s", secret, encoded)
		}
	}
	image, _ := view["imageGeneration"].(map[string]any)
	if image["tokenConfigured"] != true {
		t.Fatalf("image generation projection = %#v, want configured token status only", image)
	}
	sandboxView, _ := view["sandbox"].(map[string]any)
	if _, exists := sandboxView["passEnv"]; exists {
		t.Fatalf("sandbox projection must not return passEnv: %#v", sandboxView)
	}

	updated := manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/application/patch", map[string]any{
		"patch": map[string]any{
			"defaults":        map[string]any{"defaultMode": "plan", "enablePlanTool": false, "enableArtifact": true, "enableACPArtifact": true, "authored": true, "updateCheck": false},
			"contextFiles":    map[string]any{"enabled": false, "extraFiles": []string{"AGENTS.md", "TEAM.md"}},
			"toolExecution":   map[string]any{"mode": "sequential", "maxConcurrency": 1},
			"imageGeneration": map[string]any{"enabled": true, "provider": "openai", "apiType": "openai-images", "baseUrl": "https://images.example/v1", "model": "image-model", "token": "img-REPLACED-SECRET"},
			"sandbox":         map[string]any{"enabled": true, "level": "bwrap", "allowNetwork": false, "allowedRead": []string{"/workspace/read"}, "allowedWrite": []string{"/workspace/write"}, "deniedPaths": []string{"/workspace/deny"}, "tmpSize": "200m", "protectGit": true},
			"approval":        map[string]any{"confirmBeforeWrite": true, "bashWhitelist": []string{"go "}, "bashBlacklist": []string{"rm "}},
		},
	}))
	updatedEncoded, err := json.Marshal(updated)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(updatedEncoded, []byte("REPLACED-SECRET")) {
		t.Fatalf("application patch response leaked image token: %s", updatedEncoded)
	}
	defaults, _ := updated["defaults"].(map[string]any)
	if defaults["defaultMode"] != "plan" || defaults["enablePlanTool"] != false || defaults["enableArtifact"] != true || defaults["enableACPArtifact"] != true {
		t.Fatalf("updated defaults = %#v", defaults)
	}
	if !srv.artifact {
		t.Fatal("ACP runtime artifact setting was not applied live")
	}

	raw := readManageRawFile(t, config.GlobalSettingsPath())
	for _, key := range []string{"defaultMode", "enableArtifact", "enableACPArtifact", "contextFiles", "toolExecution", "imageGeneration", "sandbox", "approval"} {
		if len(raw[key]) == 0 {
			t.Fatalf("settings file missing application key %q: %#v", key, raw)
		}
	}
	if !bytes.Contains(raw["imageGeneration"], []byte("REPLACED-SECRET")) {
		t.Fatalf("image token was not saved in its canonical config section: %s", raw["imageGeneration"])
	}

	code, _ := manageFixtureError(t, callManageFixture(t, srv, output, 3, "mothx/manage/application/patch", map[string]any{
		"patch": map[string]any{"sandbox": map[string]any{"passEnv": []string{"SHOULD_NOT_BE_ALLOWED"}}},
	}))
	if code != "application_field_not_allowed" {
		t.Fatalf("passEnv patch error = %q, want application_field_not_allowed", code)
	}
}

func TestManageSettingsPatchWhitelistRoundTrip(t *testing.T) {
	configDir := t.TempDir()
	writeManageSettings(t, configDir, func(settings *config.Settings) {
		settings.Sandbox.Level = "strict"
	})
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/settings/patch", map[string]any{
		"patch": map[string]any{
			"defaultModel":     "alpha-mini",
			"defaultMode":      "agent",
			"thinkingLevel":    "high",
			"sandboxEnabled":   true,
			"webSearchEnabled": true,
		},
	}))
	if result["defaultModel"] != "alpha-mini" || result["defaultMode"] != "agent" || result["thinkingLevel"] != "high" {
		t.Fatalf("patched view = %#v", result)
	}
	if result["sandboxEnabled"] != true || result["webSearchEnabled"] != true {
		t.Fatalf("patched toggles = %#v", result)
	}
	raw := readManageRawFile(t, config.GlobalSettingsPath())
	var sandbox map[string]any
	if err := json.Unmarshal(raw["sandbox"], &sandbox); err != nil {
		t.Fatalf("sandbox key: %v", err)
	}
	if sandbox["enabled"] != true || sandbox["level"] != "strict" {
		t.Fatalf("sandbox merge lost siblings: %#v", sandbox)
	}
	if _, ok := raw["allowedReadUnused"]; ok {
		t.Fatal("unexpected key")
	}
	// The provider map survives untouched when only scalars are patched.
	var providers map[string]map[string]any
	if err := json.Unmarshal(raw["providers"], &providers); err != nil {
		t.Fatalf("providers key: %v", err)
	}
	if providers["manage-alpha"]["apiKey"] != "sk-alpha-SUPERSECRET-987654" {
		t.Fatalf("scalar patch clobbered the provider key: %#v", providers["manage-alpha"])
	}
	var defaultThinking string
	_ = json.Unmarshal(raw["defaultThinkingLevel"], &defaultThinking)
	if defaultThinking != "high" {
		t.Fatalf("defaultThinkingLevel = %q", defaultThinking)
	}

	// providerKey / providerBaseUrl merge into the existing provider entry.
	result = manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/settings/patch", map[string]any{
		"patch": map[string]any{
			"providerKey":     map[string]any{"name": "manage-alpha", "key": "sk-rotated-NEWSECRET-555"},
			"providerBaseUrl": map[string]any{"name": "manage-alpha", "url": "https://alpha2.example.com/v1"},
		},
	}))
	alpha := manageFindProvider(t, result, "manage-alpha")
	if alpha["maskedKey"] != "sk-***555" {
		t.Fatalf("rotated maskedKey = %#v", alpha["maskedKey"])
	}
	if alpha["baseUrl"] != "https://alpha2.example.com/v1" || alpha["modelCount"] != float64(2) {
		t.Fatalf("rotated alpha view = %#v", alpha)
	}
	raw = readManageRawFile(t, config.GlobalSettingsPath())
	providers = map[string]map[string]any{}
	if err := json.Unmarshal(raw["providers"], &providers); err != nil {
		t.Fatal(err)
	}
	entry := providers["manage-alpha"]
	if entry["apiKey"] != "sk-rotated-NEWSECRET-555" || entry["baseUrl"] != "https://alpha2.example.com/v1" {
		t.Fatalf("provider merge = %#v", entry)
	}
	if entry["api"] != "openai-chat" {
		t.Fatalf("provider merge lost the api sibling: %#v", entry)
	}
	models, _ := entry["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("provider merge lost models: %#v", entry)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "NEWSECRET") || strings.Contains(string(encoded), "SUPERSECRET") {
		t.Fatalf("patch response leaks a plaintext key: %s", encoded)
	}
}

func TestManageSettingsPatchRejectsDisallowedAndInvalid(t *testing.T) {
	configDir := t.TempDir()
	writeManageSettings(t, configDir, nil)
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	cases := []struct {
		name  string
		patch map[string]any
		code  string
		field string
	}{
		{"memory feature belongs to serve.json", map[string]any{"memoryEnabled": true}, "settings_field_not_allowed", "memoryEnabled"},
		{"unknown field", map[string]any{"shellPath": "/bin/zsh"}, "settings_field_not_allowed", "shellPath"},
		{"invalid mode", map[string]any{"defaultMode": "turbo"}, "settings_field_invalid", "defaultMode"},
		{"invalid thinking", map[string]any{"thinkingLevel": "ultra"}, "settings_field_invalid", "thinkingLevel"},
		{"unknown provider key target", map[string]any{"providerKey": map[string]any{"name": "ghost", "key": "x"}}, "settings_field_invalid", "providerKey"},
		{"missing key value", map[string]any{"providerKey": map[string]any{"name": "manage-alpha"}}, "settings_field_invalid", "providerKey"},
		{"empty model", map[string]any{"defaultModel": "  "}, "settings_field_invalid", "defaultModel"},
		{"non-boolean toggle", map[string]any{"sandboxEnabled": "yes"}, "settings_field_invalid", "sandboxEnabled"},
	}
	for index, testCase := range cases {
		message := callManageFixture(t, srv, output, index+1, "mothx/manage/settings/patch", map[string]any{"patch": testCase.patch})
		code, data := manageFixtureError(t, message)
		if code != testCase.code {
			t.Fatalf("%s: code = %q, want %q (%#v)", testCase.name, code, testCase.code, message["error"])
		}
		if data == nil || data["field"] != testCase.field {
			t.Fatalf("%s: data = %#v, want field %q", testCase.name, data, testCase.field)
		}
	}

	// Empty patch is a protocol error.
	message := callManageFixture(t, srv, output, 90, "mothx/manage/settings/patch", map[string]any{"patch": map[string]any{}})
	if code, _ := manageFixtureError(t, message); code != "invalid_params" {
		t.Fatalf("empty patch code = %q", code)
	}
	// Rejections never touch the file.
	raw := readManageRawFile(t, config.GlobalSettingsPath())
	if _, ok := raw["memoryEnabled"]; ok {
		t.Fatal("rejected field was persisted")
	}
	var defaultModel string
	_ = json.Unmarshal(raw["defaultModel"], &defaultModel)
	if defaultModel != "alpha-model" {
		t.Fatalf("rejected patches mutated defaultModel: %q", defaultModel)
	}
}

// --- providers ------------------------------------------------------------------

func TestManageProvidersListProjectsCatalog(t *testing.T) {
	configDir := t.TempDir()
	writeManageSettings(t, configDir, nil)
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/providers/list", map[string]any{}))
	if result["defaultProvider"] != "manage-alpha" || result["defaultModel"] != "alpha-model" {
		t.Fatalf("catalog defaults = %#v", result)
	}
	alpha := manageFindProvider(t, result, "manage-alpha")
	if alpha["modelCount"] != float64(2) || alpha["maskedKey"] != "sk-***654" {
		t.Fatalf("alpha = %#v", alpha)
	}
	models, _ := result["models"].([]any)
	byProvider := map[string]int{}
	foundAlphaModel := false
	for _, entry := range models {
		model, _ := entry.(map[string]any)
		providerName, _ := model["provider"].(string)
		byProvider[providerName]++
		if providerName == "manage-alpha" && model["id"] == "alpha-model" {
			foundAlphaModel = true
		}
	}
	if !foundAlphaModel {
		t.Fatalf("catalog missing manage-alpha/alpha-model: %#v", models)
	}
	if byProvider["manage-alpha"] != 2 || byProvider["manage-broken"] != 1 {
		t.Fatalf("catalog counts = %#v", byProvider)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "SUPERSECRET") || strings.Contains(string(encoded), "ALSOSECRET") {
		t.Fatalf("catalog leaks a plaintext key: %s", encoded)
	}
}

func TestManageProvidersConfigSaveDeleteAndDiscover(t *testing.T) {
	configDir := t.TempDir()
	writeManageSettings(t, configDir, nil)
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	initial := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/providers/list", map[string]any{}))
	configs, _ := initial["providerConfigs"].([]any)
	if len(configs) == 0 {
		t.Fatalf("providerConfigs = %#v, want a secret-safe editable projection", initial)
	}
	encodedInitial, _ := json.Marshal(initial)
	if strings.Contains(string(encodedInitial), "SUPERSECRET") || strings.Contains(string(encodedInitial), "ALSOSECRET") {
		t.Fatalf("provider configuration projection leaked a key: %s", encodedInitial)
	}

	created := manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/providers/save", map[string]any{
		"id":     "desktop-custom",
		"apiKey": "sk-desktop-NEWSECRET-444",
		"provider": map[string]any{
			"vendor":         "custom",
			"api":            "openai-chat",
			"baseUrl":        "https://desktop.example.com/v1",
			"httpProxy":      "http://127.0.0.1:7890",
			"headers":        map[string]string{"Authorization": "Bearer header-HEADERSECRET"},
			"thinkingFormat": "openai",
			"models": []map[string]any{{
				"id": "desktop-model", "name": "Desktop Model", "reasoning": true,
				"contextWindow": 123456, "maxTokens": 4096, "input": []string{"text", "image"},
			}},
		},
	}))
	custom := manageFindProvider(t, created, "desktop-custom")
	if custom["modelCount"] != float64(1) || custom["maskedKey"] != "sk-***444" {
		t.Fatalf("saved custom provider = %#v", custom)
	}
	encodedCreated, _ := json.Marshal(created)
	if strings.Contains(string(encodedCreated), "NEWSECRET") || strings.Contains(string(encodedCreated), "HEADERSECRET") {
		t.Fatalf("save response leaked a secret: %s", encodedCreated)
	}
	raw := readManageRawFile(t, config.GlobalSettingsPath())
	var providers map[string]map[string]any
	if err := json.Unmarshal(raw["providers"], &providers); err != nil {
		t.Fatal(err)
	}
	if got := providers["desktop-custom"]["apiKey"]; got != "sk-desktop-NEWSECRET-444" {
		t.Fatalf("saved api key = %#v", got)
	}
	if got := providers["desktop-custom"]["api"]; got != "openai-chat" {
		t.Fatalf("saved provider config = %#v", providers["desktop-custom"])
	}
	if headers, _ := providers["desktop-custom"]["headers"].(map[string]any); headers["Authorization"] != "Bearer header-HEADERSECRET" {
		t.Fatalf("saved provider header = %#v", headers)
	}

	// A Desktop model or endpoint edit deliberately does not receive provider
	// headers, so it must preserve the existing secret header sibling.
	manageFixtureResult(t, callManageFixture(t, srv, output, 3, "mothx/manage/providers/save", map[string]any{
		"id": "desktop-custom",
		"provider": map[string]any{
			"baseUrl": "https://desktop-2.example.com/v1",
		},
	}))
	raw = readManageRawFile(t, config.GlobalSettingsPath())
	providers = map[string]map[string]any{}
	if err := json.Unmarshal(raw["providers"], &providers); err != nil {
		t.Fatal(err)
	}
	if headers, _ := providers["desktop-custom"]["headers"].(map[string]any); headers["Authorization"] != "Bearer header-HEADERSECRET" {
		t.Fatalf("provider edit erased secret header: %#v", providers["desktop-custom"])
	}

	message := callManageFixture(t, srv, output, 4, "mothx/manage/providers/save", map[string]any{
		"id": "desktop-custom",
		"provider": map[string]any{
			"api": "openai-chat", "baseUrl": "https://desktop.example.com/v1", "unknown": true,
		},
	})
	if code, _ := manageFixtureError(t, message); code != "provider_field_not_allowed" {
		t.Fatalf("unknown provider config field code = %q", code)
	}

	deleted := manageFixtureResult(t, callManageFixture(t, srv, output, 5, "mothx/manage/providers/delete", map[string]any{"id": "desktop-custom"}))
	for _, entry := range deleted["providers"].([]any) {
		if entry.(map[string]any)["name"] == "desktop-custom" {
			t.Fatalf("custom provider remained after delete: %#v", deleted)
		}
	}

	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("model discovery path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-discover-SECRET" {
			t.Errorf("discovery auth = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"discovered-model","name":"Discovered Model","context_window":64000,"max_tokens":8192}]}`))
	}))
	defer modelServer.Close()
	discovered := manageFixtureResult(t, callManageFixture(t, srv, output, 6, "mothx/manage/providers/discover", map[string]any{
		"api": "openai-chat", "baseUrl": modelServer.URL + "/v1", "apiKey": "sk-discover-SECRET",
	}))
	models, _ := discovered["models"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["id"] != "discovered-model" {
		t.Fatalf("discovered models = %#v", discovered)
	}
	encodedDiscovery, _ := json.Marshal(discovered)
	if strings.Contains(string(encodedDiscovery), "discover-SECRET") {
		t.Fatalf("discovery response leaked a key: %s", encodedDiscovery)
	}
}

func TestManageProvidersTestStructuredPaths(t *testing.T) {
	configDir := t.TempDir()
	writeManageSettings(t, configDir, nil)
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	message := callManageFixture(t, srv, output, 1, "mothx/manage/providers/test", map[string]any{})
	if code, _ := manageFixtureError(t, message); code != "invalid_params" {
		t.Fatalf("missing provider code = %q", code)
	}

	message = callManageFixture(t, srv, output, 2, "mothx/manage/providers/test", map[string]any{"provider": "ghost"})
	if code, _ := manageFixtureError(t, message); code != "provider_not_found" {
		t.Fatalf("unknown provider code = %q", code)
	}

	// The broken provider fails fast (connection refused, retries disabled)
	// and the error never carries key material.
	message = callManageFixture(t, srv, output, 3, "mothx/manage/providers/test", map[string]any{"provider": "manage-broken"})
	result := manageFixtureResult(t, message)
	if result["ok"] != false {
		t.Fatalf("broken provider result = %#v, want ok=false", result)
	}
	messageText, _ := result["error"].(string)
	if strings.TrimSpace(messageText) == "" {
		t.Fatalf("broken provider result = %#v, want an error message", result)
	}
	if strings.Contains(messageText, "ALSOSECRET") || strings.Contains(messageText, "sk-broken") {
		t.Fatalf("provider test error leaks key material: %q", messageText)
	}
}

// --- skills -----------------------------------------------------------------------

func writeManageSkill(t *testing.T, dir, name, description string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("# %s\n\n%s\n", name, description)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestManageSkillsListSetRoundTrip(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	writeManageSettings(t, configDir, nil)
	writeManageSkill(t, filepath.Join(configDir, "skills"), "global-gen", "global generator skill")
	writeManageSkill(t, filepath.Join(workDir, ".skills"), "proj-skill", "project skill")
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, workDir)
	srv.skillsMgr = skills.NewManagerWithProjectDirs(filepath.Join(configDir, "skills"), skills.ProjectSkillDirs(workDir))
	if err := srv.skillsMgr.Load(); err != nil {
		t.Fatal(err)
	}

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/skills/list", map[string]any{"cwd": workDir}))
	entries, _ := result["skills"].([]any)
	byName := map[string]map[string]any{}
	for _, entry := range entries {
		item, _ := entry.(map[string]any)
		byName[item["name"].(string)] = item
	}
	if len(byName) != 4 {
		t.Fatalf("skills = %#v, want both built-ins, global-gen and proj-skill", byName)
	}
	if byName[skills.ExpertCreaterSkillName]["source"] != "builtin" || byName["vibe-browser"]["source"] != "builtin" || byName["global-gen"]["source"] != "global" || byName["proj-skill"]["source"] != "project" {
		t.Fatalf("skill sources = %#v", byName)
	}
	if byName["global-gen"]["enabled"] != true || byName["proj-skill"]["enabled"] != true {
		t.Fatalf("default skills must be enabled: %#v", byName)
	}
	if byName["global-gen"]["description"] != "global-gen" {
		t.Fatalf("description = %#v", byName["global-gen"]["description"])
	}

	result = manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/skills/set", map[string]any{"name": "global-gen", "enabled": false, "cwd": workDir}))
	if result["name"] != "global-gen" || result["enabled"] != false {
		t.Fatalf("set result = %#v", result)
	}
	disabled, _ := result["skillsDisabled"].([]any)
	if len(disabled) != 1 || disabled[0] != "global-gen" {
		t.Fatalf("skillsDisabled = %#v", disabled)
	}
	raw := readManageRawFile(t, config.GlobalSettingsPath())
	var skillsSection map[string]any
	if err := json.Unmarshal(raw["skills"], &skillsSection); err != nil {
		t.Fatalf("skills key: %v", err)
	}
	list, _ := skillsSection["disabled"].([]any)
	if len(list) != 1 || list[0] != "global-gen" {
		t.Fatalf("persisted skills.disabled = %#v", skillsSection)
	}
	// The live runtime manager picked the toggle up without a reload.
	if !srv.skillsMgr.IsSkillDisabled("global-gen") || srv.skillsMgr.Get("global-gen") != nil {
		t.Fatal("live skills manager did not apply the disabled toggle")
	}
	if len(srv.skillsMgr.ListAll()) != 4 || len(srv.skillsMgr.List()) != 3 {
		t.Fatal("ListAll must keep disabled skills while List filters them")
	}

	result = manageFixtureResult(t, callManageFixture(t, srv, output, 3, "mothx/manage/skills/list", map[string]any{"cwd": workDir}))
	entries, _ = result["skills"].([]any)
	for _, entry := range entries {
		item, _ := entry.(map[string]any)
		if item["name"] == "global-gen" && item["enabled"] != false {
			t.Fatalf("disabled skill projects enabled: %#v", item)
		}
	}
	settingsView := manageFixtureResult(t, callManageFixture(t, srv, output, 4, "mothx/manage/settings/get", map[string]any{}))
	viewDisabled, _ := settingsView["skillsDisabled"].([]any)
	if len(viewDisabled) != 1 || viewDisabled[0] != "global-gen" {
		t.Fatalf("settings view skillsDisabled = %#v", viewDisabled)
	}

	// Re-enable removes the entry and keeps the sparse file sparse.
	result = manageFixtureResult(t, callManageFixture(t, srv, output, 5, "mothx/manage/skills/set", map[string]any{"name": "global-gen", "enabled": true, "cwd": workDir}))
	if result["enabled"] != true {
		t.Fatalf("re-enable result = %#v", result)
	}
	raw = readManageRawFile(t, config.GlobalSettingsPath())
	if _, ok := raw["skills"]; ok {
		t.Fatalf("empty disabled list must drop the skills key: %#v", raw["skills"])
	}
	if srv.skillsMgr.IsSkillDisabled("global-gen") || srv.skillsMgr.Get("global-gen") == nil {
		t.Fatal("live skills manager did not apply the re-enable")
	}

	message := callManageFixture(t, srv, output, 6, "mothx/manage/skills/set", map[string]any{"name": "missing-skill", "enabled": false, "cwd": workDir})
	if code, _ := manageFixtureError(t, message); code != "skill_not_found" {
		t.Fatalf("unknown skill code = %q", code)
	}
	message = callManageFixture(t, srv, output, 7, "mothx/manage/skills/set", map[string]any{"name": "global-gen", "cwd": workDir})
	if code, _ := manageFixtureError(t, message); code != "invalid_params" {
		t.Fatalf("missing enabled code = %q", code)
	}
	message = callManageFixture(t, srv, output, 8, "mothx/manage/skills/list", map[string]any{"cwd": "relative/dir"})
	if code, _ := manageFixtureError(t, message); code != "skills_unavailable" {
		t.Fatalf("relative cwd code = %q", code)
	}
}

func TestACPAvailableCommandsIncludeBuiltInExpertCreater(t *testing.T) {
	srv := newManageFixtureServer(&syncedBuffer{}, t.TempDir())
	srv.skillsMgr = skills.NewManager("", "")
	if err := srv.skillsMgr.Load(); err != nil {
		t.Fatal(err)
	}
	for _, command := range srv.availableCommands() {
		if command.Name == "/"+skills.ExpertCreaterSkillName {
			return
		}
	}
	t.Fatalf("available commands missing %q", "/"+skills.ExpertCreaterSkillName)
}

func TestManageSkillHubCatalogProjectsRuntimeOwnedTargets(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	settings := writeManageSettings(t, configDir, func(settings *config.Settings) {
		settings.SkillHub.DefaultMarket = "skillhub.cn"
		settings.SkillHub.DefaultInstallScope = "project"
	})
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, workDir)
	rt := &sessionRuntime{id: "catalog-session", runtime: &agentruntime.SessionRuntime{WorkDir: workDir}}
	srv.mu.Lock()
	srv.sessions[rt.id] = rt
	srv.mu.Unlock()

	targets := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/skillhub/targets", map[string]any{"sessionId": rt.id}))
	items, ok := targets["targets"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("targets = %#v, want configured project targets", targets)
	}
	first, _ := items[0].(map[string]any)
	path, _ := first["path"].(string)
	if !filepath.IsAbs(path) || !strings.HasPrefix(path, workDir+string(filepath.Separator)) {
		t.Fatalf("target path = %q, want an absolute project skills directory below %q", path, workDir)
	}

	markets := manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/skillhub/markets", map[string]any{"sessionId": rt.id}))
	if got, ok := markets["markets"].([]any); !ok || len(got) == 0 {
		t.Fatalf("markets = %#v, want configured SkillHub market projections", markets)
	}

	message := callManageFixture(t, srv, output, 3, "mothx/manage/skillhub/targets", map[string]any{})
	if code, _ := manageFixtureError(t, message); code != "skillhub_invalid_request" {
		t.Fatalf("missing sessionId error = %q", code)
	}
	message = callManageFixture(t, srv, output, 4, "mothx/manage/skillhub/install", map[string]any{"sessionId": rt.id, "market": "skillhub.cn", "id": "demo", "scope": "project", "targetDir": "relative"})
	if code, _ := manageFixtureError(t, message); code != "skillhub_invalid_request" {
		t.Fatalf("relative install target error = %q", code)
	}
	if settings.GetGlobalSkillsDir() == "" {
		t.Fatal("fixture must have a global skills dir")
	}
}

// --- mcp ------------------------------------------------------------------------

func writeManageMCPFile(t *testing.T, configDir string) {
	t.Helper()
	cfg := &config.MCPConfig{MCPServers: []config.MCPServer{
		{
			Name: "keeper", Type: "stdio", Command: "/bin/keep", Args: []string{"--old"},
			Env: []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}{{Name: "TOKEN", Value: "mcp-super-secret"}},
			Headers: []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}{{Name: "Authorization", Value: "Bearer hdr-secret"}},
		},
		{Name: "goner", Type: "stdio", Command: "/bin/gone"},
	}}
	if err := config.SaveMCPConfig(config.GlobalMCPPath(), cfg); err != nil {
		t.Fatal(err)
	}
}

func TestManageMCPListReturnsCompleteLocalConfig(t *testing.T) {
	configDir := t.TempDir()
	writeManageSettings(t, configDir, nil)
	writeManageMCPFile(t, configDir)
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/mcp/list", map[string]any{}))
	servers, _ := result["servers"].([]any)
	if len(servers) != 2 {
		t.Fatalf("servers = %#v", servers)
	}
	keeper, _ := servers[0].(map[string]any)
	if keeper["name"] != "keeper" || keeper["command"] != "/bin/keep" || keeper["enabled"] != true {
		t.Fatalf("keeper = %#v", keeper)
	}
	env, _ := keeper["env"].([]any)
	if len(env) != 1 || env[0].(map[string]any)["name"] != "TOKEN" || env[0].(map[string]any)["value"] != "mcp-super-secret" {
		t.Fatalf("env = %#v", env)
	}
	headers, _ := keeper["headers"].([]any)
	if len(headers) != 1 || headers[0].(map[string]any)["name"] != "Authorization" || headers[0].(map[string]any)["value"] != "Bearer hdr-secret" {
		t.Fatalf("headers = %#v", headers)
	}
}

func TestManageMCPSetReplacesAndMerges(t *testing.T) {
	configDir := t.TempDir()
	writeManageSettings(t, configDir, nil)
	writeManageMCPFile(t, configDir)
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/mcp/set", map[string]any{
		"servers": []any{
			map[string]any{"name": "keeper", "command": "/bin/keep2", "args": []any{"--new"}, "enabled": false,
				"env":     []any{map[string]any{"name": "TOKEN", "value": "rotated-secret"}},
				"headers": []any{map[string]any{"name": "Authorization", "value": "Bearer rotated-header"}}},
			map[string]any{"name": "remote", "type": "http", "url": "https://mcp.example.org/api"},
		},
	}))
	servers, _ := result["servers"].([]any)
	if len(servers) != 2 {
		t.Fatalf("servers = %#v, want the full replacement list", servers)
	}
	views := map[string]map[string]any{}
	for _, entry := range servers {
		view, _ := entry.(map[string]any)
		views[view["name"].(string)] = view
	}
	if _, ok := views["goner"]; ok {
		t.Fatalf("full replacement must drop absent servers: %#v", views)
	}
	keeper := views["keeper"]
	if keeper["command"] != "/bin/keep2" || keeper["enabled"] != false {
		t.Fatalf("keeper = %#v", keeper)
	}
	args, _ := keeper["args"].([]any)
	if len(args) != 1 || args[0] != "--new" {
		t.Fatalf("keeper args = %#v", args)
	}
	env, _ := keeper["env"].([]any)
	if len(env) != 1 || env[0].(map[string]any)["value"] != "rotated-secret" {
		t.Fatalf("keeper env = %#v", keeper)
	}
	remote := views["remote"]
	if remote["type"] != "http" || remote["url"] != "https://mcp.example.org/api" || remote["enabled"] != true {
		t.Fatalf("remote = %#v", remote)
	}
	// The full local ACP projection persists updated environment and headers.
	saved, err := config.LoadMCPConfig(config.GlobalMCPPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.MCPServers) != 2 {
		t.Fatalf("saved servers = %#v", saved.MCPServers)
	}
	var keeperSaved *config.MCPServer
	for index := range saved.MCPServers {
		if saved.MCPServers[index].Name == "keeper" {
			keeperSaved = &saved.MCPServers[index]
		}
	}
	if keeperSaved == nil || len(keeperSaved.Env) != 1 || keeperSaved.Env[0].Value != "rotated-secret" {
		t.Fatalf("keeper env was not updated: %#v", keeperSaved)
	}
	if len(keeperSaved.Headers) != 1 || keeperSaved.Headers[0].Value != "Bearer rotated-header" {
		t.Fatalf("keeper headers were not updated: %#v", keeperSaved)
	}
	if keeperSaved.Enabled == nil || *keeperSaved.Enabled {
		t.Fatalf("keeper enabled = %#v, want explicit false", keeperSaved.Enabled)
	}

	// Schema validation.
	message := callManageFixture(t, srv, output, 2, "mothx/manage/mcp/set", map[string]any{
		"servers": []any{map[string]any{"name": "x", "command": "/bin/x", "env": []any{map[string]any{"name": "", "value": "x"}}}},
	})
	if code, _ := manageFixtureError(t, message); code != "mcp_server_invalid" {
		t.Fatalf("invalid env code = %q", code)
	}
	message = callManageFixture(t, srv, output, 3, "mothx/manage/mcp/set", map[string]any{
		"servers": []any{map[string]any{"name": "no-command"}},
	})
	if code, _ := manageFixtureError(t, message); code != "mcp_server_invalid" {
		t.Fatalf("stdio without command code = %q", code)
	}
	message = callManageFixture(t, srv, output, 4, "mothx/manage/mcp/set", map[string]any{
		"servers": []any{map[string]any{"name": "dup", "command": "/bin/a"}, map[string]any{"name": "dup", "command": "/bin/b"}},
	})
	if code, _ := manageFixtureError(t, message); code != "mcp_server_invalid" {
		t.Fatalf("duplicate names code = %q", code)
	}
	message = callManageFixture(t, srv, output, 5, "mothx/manage/mcp/set", map[string]any{
		"servers": []any{map[string]any{"name": "bad", "type": "carrier-pigeon", "url": "https://x.example"}},
	})
	if code, _ := manageFixtureError(t, message); code != "mcp_server_invalid" {
		t.Fatalf("bad transport code = %q", code)
	}
	message = callManageFixture(t, srv, output, 6, "mothx/manage/mcp/set", map[string]any{})
	if code, _ := manageFixtureError(t, message); code != "invalid_params" {
		t.Fatalf("missing servers code = %q", code)
	}
	// Rejected sets leave the stored config unchanged.
	unchanged, err := config.LoadMCPConfig(config.GlobalMCPPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.MCPServers) != 2 {
		t.Fatalf("rejected sets mutated mcp.json: %#v", unchanged.MCPServers)
	}

	// An empty array clears every server.
	result = manageFixtureResult(t, callManageFixture(t, srv, output, 7, "mothx/manage/mcp/set", map[string]any{"servers": []any{}}))
	servers, present := result["servers"].([]any)
	if !present || len(servers) != 0 {
		t.Fatalf("clear-all result = %#v", result)
	}
}

func TestManageMCPProjectScopeUsesActiveSessionWorkDir(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	writeManageSettings(t, configDir, nil)
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, workDir)
	srv.sessions["project-session"] = &sessionRuntime{
		runtime: &agentruntime.SessionRuntime{WorkDir: workDir},
	}

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/mcp/set", map[string]any{
		"scope": "project", "sessionId": "project-session",
		"servers": []any{map[string]any{"name": "project-server", "type": "stdio", "command": "/bin/project"}},
	}))
	if result["scope"] != "project" || result["sessionId"] != "project-session" {
		t.Fatalf("project set response = %#v", result)
	}
	projectPath := filepath.Join(workDir, config.ProjectMCPPath())
	if result["path"] != projectPath {
		t.Fatalf("project path = %#v, want %s", result["path"], projectPath)
	}
	project, err := config.LoadMCPConfig(projectPath)
	if err != nil || len(project.MCPServers) != 1 || project.MCPServers[0].Name != "project-server" {
		t.Fatalf("project MCP config = %#v, err=%v", project, err)
	}
	if _, err := config.LoadMCPConfig(config.GlobalMCPPath()); !os.IsNotExist(err) {
		t.Fatalf("project set must not write global MCP config: %v", err)
	}

	result = manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/mcp/list", map[string]any{
		"scope": "project", "sessionId": "project-session",
	}))
	servers, _ := result["servers"].([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["name"] != "project-server" {
		t.Fatalf("project MCP list = %#v", result)
	}

	message := callManageFixture(t, srv, output, 3, "mothx/manage/mcp/list", map[string]any{
		"scope": "project", "sessionId": "missing-session",
	})
	if code, _ := manageFixtureError(t, message); code != "mcp_scope_invalid" {
		t.Fatalf("missing project session code = %q", code)
	}
}

// --- cron field validation --------------------------------------------------------

func TestManageCronWhitelistAndNormalization(t *testing.T) {
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, t.TempDir())

	if _, rpcErr := manageDecodeWhitelist(mustJSON(map[string]any{"name": "x", "workDir": "/"}), manageCronJobFields, "cron_field_not_allowed"); rpcErr == nil ||
		manageRPCErrorData(rpcErr)["code"] != "cron_field_not_allowed" || manageRPCErrorData(rpcErr)["field"] != "workDir" {
		t.Fatalf("whitelist rejection = %#v", rpcErr)
	}
	fields, rpcErr := manageDecodeWhitelist(mustJSON(map[string]any{"name": "nightly", "prompt": "run", "schedule": "@daily", "mode": "yolo", "enabled": true}), manageCronJobFields, "cron_field_not_allowed")
	if rpcErr != nil || len(fields) != 5 {
		t.Fatalf("whitelist acceptance = %#v %v", fields, rpcErr)
	}

	job, rpcErr := srv.manageCronJobFromFields(cron.CronJob{Enabled: true}, fields)
	if rpcErr != nil {
		t.Fatalf("normalization: %#v", rpcErr)
	}
	if job.Name != "nightly" || job.Mode != "yolo" || job.OneShot || job.NextRun.IsZero() {
		t.Fatalf("normalized job = %#v", job)
	}

	// Empty schedule is a one-shot with no next run.
	fields = map[string]json.RawMessage{"name": mustJSON("once"), "prompt": mustJSON("do")}
	job, rpcErr = srv.manageCronJobFromFields(cron.CronJob{Enabled: true}, fields)
	if rpcErr != nil {
		t.Fatalf("one-shot normalization: %#v", rpcErr)
	}
	if !job.OneShot || !job.NextRun.IsZero() || job.Mode != "yolo" {
		t.Fatalf("one-shot job = %#v", job)
	}

	cases := []struct {
		name   string
		base   cron.CronJob
		fields map[string]any
		code   string
	}{
		{"bad mode", cron.CronJob{}, map[string]any{"name": "n", "prompt": "p", "mode": "turbo"}, "cron_mode_invalid"},
		{"bad schedule", cron.CronJob{}, map[string]any{"name": "n", "prompt": "p", "schedule": "@every soon"}, "cron_schedule_invalid"},
		{"missing name", cron.CronJob{}, map[string]any{"prompt": "p"}, "cron_field_invalid"},
		{"missing prompt", cron.CronJob{}, map[string]any{"name": "n"}, "cron_field_invalid"},
		{"bad enabled type", cron.CronJob{}, map[string]any{"name": "n", "prompt": "p", "enabled": "yes"}, "cron_field_invalid"},
	}
	for _, testCase := range cases {
		raw := map[string]json.RawMessage{}
		for key, value := range testCase.fields {
			raw[key] = mustJSON(value)
		}
		_, rpcErr := srv.manageCronJobFromFields(testCase.base, raw)
		if rpcErr == nil || manageRPCErrorData(rpcErr)["code"] != testCase.code {
			t.Fatalf("%s: rpcErr = %#v, want %s", testCase.name, rpcErr, testCase.code)
		}
	}
}

// --- stats query mapping ------------------------------------------------------------

func TestManageStatsQueryMapping(t *testing.T) {
	query, rpcErr := manageStatsQuery(manageStatsRequest{})
	if rpcErr != nil || query.GroupBy != "day" || !query.From.IsZero() {
		t.Fatalf("default query = %#v %v", query, rpcErr)
	}
	query, rpcErr = manageStatsQuery(manageStatsRequest{From: "2026-01-02", To: "2026-01-03", Group: "week"})
	if rpcErr != nil || query.GroupBy != "week" {
		t.Fatalf("group mapping = %#v %v", query, rpcErr)
	}
	if query.From.Format("2006-01-02") != "2026-01-02" {
		t.Fatalf("from mapping = %#v", query.From)
	}
	// Date-only "to" includes the full day, matching the shared parser.
	if query.To.Format("2006-01-02") != "2026-01-04" {
		t.Fatalf("to mapping = %#v", query.To)
	}
	query, rpcErr = manageStatsQuery(manageStatsRequest{From: "2026-01-02T03:04:05Z"})
	if rpcErr != nil || query.From.Format(time.RFC3339) != "2026-01-02T03:04:05Z" {
		t.Fatalf("rfc3339 fallback = %#v %v", query, rpcErr)
	}
	if _, rpcErr := manageStatsQuery(manageStatsRequest{Group: "year"}); rpcErr == nil || manageRPCErrorData(rpcErr)["code"] != "stats_group_invalid" {
		t.Fatalf("bad group = %#v", rpcErr)
	}
	if _, rpcErr := manageStatsQuery(manageStatsRequest{From: "yesterday"}); rpcErr == nil || manageRPCErrorData(rpcErr)["code"] != "stats_time_invalid" {
		t.Fatalf("bad time = %#v", rpcErr)
	}
}

// --- memory ---------------------------------------------------------------------

func TestManageMemoryRoundTripAndLimit(t *testing.T) {
	configDir := t.TempDir()
	writeManageSettings(t, configDir, nil)
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/memory/get", map[string]any{}))
	if result["content"] != "" || result["size"] != float64(0) {
		t.Fatalf("empty memory get = %#v", result)
	}
	wantPath := filepath.Join(configDir, "memory.md")
	if result["path"] != wantPath || result["source"] != "explicit" {
		t.Fatalf("memory path resolution = %#v, want the global %s", result, wantPath)
	}

	result = manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/memory/put", map[string]any{"content": "# Memory\n\nhello world"}))
	if result["size"] != float64(len("# Memory\n\nhello world")) {
		t.Fatalf("put size = %#v", result)
	}
	if updated, _ := result["updatedAt"].(string); updated == "" {
		t.Fatalf("put updatedAt = %#v", result)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil || string(data) != "# Memory\n\nhello world" {
		t.Fatalf("memory file = %q (%v)", data, err)
	}
	result = manageFixtureResult(t, callManageFixture(t, srv, output, 3, "mothx/manage/memory/get", map[string]any{}))
	if result["content"] != "# Memory\n\nhello world" {
		t.Fatalf("round trip = %#v", result)
	}

	message := callManageFixture(t, srv, output, 4, "mothx/manage/memory/put", map[string]any{"content": strings.Repeat("a", manageMemoryMaxBytes+1)})
	code, errData := manageFixtureError(t, message)
	if code != "memory_too_large" {
		t.Fatalf("oversize code = %q", code)
	}
	if errData["maxBytes"] != float64(manageMemoryMaxBytes) || errData["size"] != float64(manageMemoryMaxBytes+1) {
		t.Fatalf("oversize data = %#v", errData)
	}
	message = callManageFixture(t, srv, output, 5, "mothx/manage/memory/put", map[string]any{})
	if code, _ := manageFixtureError(t, message); code != "invalid_params" {
		t.Fatalf("missing content code = %q", code)
	}
	// The rejected oversize put left the stored content untouched.
	data, err = os.ReadFile(wantPath)
	if err != nil || string(data) != "# Memory\n\nhello world" {
		t.Fatalf("memory file after rejections = %q (%v)", data, err)
	}
}

func TestManageMemoryFollowsServeConfigPath(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	writeManageSettings(t, configDir, nil)
	custom := filepath.Join(configDir, "custom", "agent-memory.md")
	serveConfig := map[string]any{"memory": map[string]any{"enabled": true, "path": custom}}
	data, err := json.Marshal(serveConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "serve.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, workDir)

	if !manageMemoryEnabled() {
		t.Fatal("memory should default to enabled")
	}
	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/memory/put", map[string]any{"content": "serve path memory"}))
	if result["path"] != custom {
		t.Fatalf("serve config memory path = %#v, want %s", result, custom)
	}
	stored, err := os.ReadFile(custom)
	if err != nil || string(stored) != "serve path memory" {
		t.Fatalf("serve path memory file = %q (%v)", stored, err)
	}

	// A serve config that disables the memory feature projects through the
	// settings view.
	serveConfig["memory"] = map[string]any{"enabled": false}
	data, _ = json.Marshal(serveConfig)
	if err := os.WriteFile(filepath.Join(configDir, "serve.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if manageMemoryEnabled() {
		t.Fatal("serve config features.memory=false must project as disabled")
	}
	view := manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/settings/get", map[string]any{}))
	if view["memoryEnabled"] != false {
		t.Fatalf("settings view memoryEnabled = %#v", view["memoryEnabled"])
	}
}

func TestManageKnowledgeBasesCreateScanQueryAndDelete(t *testing.T) {
	configDir := t.TempDir()
	settings := writeManageSettings(t, configDir, nil)
	source := filepath.Join(t.TempDir(), "product-notes")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "architecture.md"), []byte("# Architecture\n\nThe runtime owns durable runs.\n\n## Knowledge graph\n\nThe graph retains source evidence."), 0o644); err != nil {
		t.Fatal(err)
	}

	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)
	srv.settings = settings
	created := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/knowledge-bases/create", map[string]any{
		"knowledgeBase": map[string]any{
			"name": "Product notes", "rootDir": source, "preprocessProfile": "documents",
			"provider": "", "model": "", "mode": "yolo", "schedule": "manual", "enabled": true,
		},
	}))
	base, _ := created["knowledgeBase"].(map[string]any)
	baseID, _ := base["id"].(string)
	if baseID == "" || created["status"] != "unindexed" {
		t.Fatalf("create result = %#v", created)
	}

	listed := manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/knowledge-bases/list", map[string]any{}))
	items, _ := listed["knowledgeBases"].([]any)
	if len(items) != 1 {
		t.Fatalf("list = %#v, want one knowledge base", listed)
	}

	scanned := manageFixtureResult(t, callManageFixture(t, srv, output, 3, "mothx/manage/knowledge-bases/scan", map[string]any{"id": baseID}))
	if scanned["started"] != true || scanned["status"] != "indexing" {
		t.Fatalf("scan must start in the background and return immediately: %#v", scanned)
	}
	// Hosts poll list/status for progress; wait for the background scan commit.
	var polled map[string]any
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		polled = manageFixtureResult(t, callManageFixture(t, srv, output, 20, "mothx/manage/knowledge-bases/list", map[string]any{}))
		items, _ := polled["knowledgeBases"].([]any)
		if len(items) == 1 {
			view, _ := items[0].(map[string]any)
			if view["status"] == "completed" {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	polledItems, _ := polled["knowledgeBases"].([]any)
	if len(polledItems) != 1 {
		t.Fatalf("list after scan = %#v", polled)
	}
	polledView, _ := polledItems[0].(map[string]any)
	snapshot, _ := polledView["snapshot"].(map[string]any)
	if polledView["status"] != "completed" || snapshot["fileCount"] != float64(1) || snapshot["nodeCount"].(float64) < 2 {
		t.Fatalf("background scan result = %#v", polledView)
	}

	queried := manageFixtureResult(t, callManageFixture(t, srv, output, 4, "mothx/manage/knowledge-bases/query", map[string]any{"id": baseID, "query": "durable graph"}))
	graph, _ := queried["query"].(map[string]any)
	chunks, _ := graph["chunks"].([]any)
	if len(chunks) == 0 {
		t.Fatalf("query result = %#v, want source chunks", queried)
	}

	updated := manageFixtureResult(t, callManageFixture(t, srv, output, 5, "mothx/manage/knowledge-bases/update", map[string]any{
		"id": baseID,
		"knowledgeBase": map[string]any{
			"name": "Product notes", "rootDir": source, "preprocessProfile": "documents",
			"provider": "", "model": "", "mode": "plan", "schedule": "manual", "enabled": true,
		},
	}))
	updatedBase, _ := updated["knowledgeBase"].(map[string]any)
	if updated["status"] != "unindexed" || updatedBase["activeSnapshotId"] != nil {
		t.Fatalf("update must invalidate the old graph snapshot: %#v", updated)
	}
	code, _ := manageFixtureError(t, callManageFixture(t, srv, output, 6, "mothx/manage/knowledge-bases/query", map[string]any{"id": baseID, "query": "durable"}))
	if code != "knowledge_base_unindexed" {
		t.Fatalf("query after update error = %q, want knowledge_base_unindexed", code)
	}

	manageFixtureResult(t, callManageFixture(t, srv, output, 7, "mothx/manage/knowledge-bases/delete", map[string]any{"id": baseID}))
	if _, err := os.Stat(filepath.Join(source, "architecture.md")); err != nil {
		t.Fatalf("delete must preserve the knowledge source: %v", err)
	}
	if _, err := session.GetKnowledgeBase(t.Context(), settings.GetSessionDir(), baseID); !errors.Is(err, session.ErrKnowledgeBaseNotFound) {
		t.Fatalf("deleted base lookup = %v, want ErrKnowledgeBaseNotFound", err)
	}
}

func TestKnowledgeBaseScheduleUsesSharedCronAndDurableIndexRun(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "notes.md"), []byte("# Notes\n\nScheduled indexing uses the shared Cron lifecycle."), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := &config.Settings{SessionDir: sessionDir}
	srv := &server{settings: settings}
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Scheduled notes", RootDir: source, PreprocessProfile: "documents",
		Mode: "yolo", Schedule: "hourly", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := cron.NewSQLiteCronStore(sessionDir)
	if err := srv.syncKnowledgeBaseScheduleWithStore(store, base); err != nil {
		t.Fatal(err)
	}
	jobID := knowledgeBaseCronJobID(base.ID)
	job, err := store.Get(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Schedule != "@hourly" || job.NextRun.IsZero() || !job.Enabled {
		t.Fatalf("knowledge cron job = %#v", job)
	}
	scheduler := cron.NewSchedulerWithSessionDirAndHandler(store, nil, time.Hour, sessionDir, srv.runKnowledgeBaseCronJob)
	if err := scheduler.RunNow(jobID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		job, err = store.Get(jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.LastStatus == "success" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("knowledge cron job did not finish: %#v", job)
		}
		time.Sleep(5 * time.Millisecond)
	}
	indexed, err := session.GetKnowledgeBase(t.Context(), sessionDir, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := session.GetKnowledgeSnapshot(t.Context(), sessionDir, indexed.ActiveSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RunID == "" {
		t.Fatalf("scheduled snapshot lacks durable Run: %#v", snapshot)
	}
	run, err := agentruntime.GetDurableRun(t.Context(), sessionDir, snapshot.RunID)
	if err != nil || run == nil {
		t.Fatalf("load scheduled index Run: %#v, %v", run, err)
	}
	if run.Source != string(agentruntime.SourceCron) || run.Status != string(agentruntime.RunStateCompleted) {
		t.Fatalf("scheduled index Run = %#v", run)
	}
	base.Enabled = false
	if err := srv.syncKnowledgeBaseScheduleWithStore(store, base); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(jobID); err == nil {
		t.Fatal("disabled knowledge base retained its cron job")
	}
}

// --- routing and discovery ---------------------------------------------------------

func TestManageUnknownMethodIsStructured(t *testing.T) {
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, t.TempDir())
	message := callManageFixture(t, srv, output, 1, "mothx/manage/secrets/dump", map[string]any{})
	code, _ := manageFixtureError(t, message)
	if code != "manage_method_not_found" {
		t.Fatalf("unknown manage method code = %q", code)
	}
}

func TestInitializeFeaturesIncludeManageKeys(t *testing.T) {
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, t.TempDir())
	srv.version = "test"
	srv.handleInitialize(rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":1}`),
	})
	var message map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output.String())), &message); err != nil {
		t.Fatal(err)
	}
	result, _ := message["result"].(map[string]any)
	meta, _ := result["_meta"].(map[string]any)
	dev, _ := meta[mothxExtensionNamespace].(map[string]any)
	rawFeatures, _ := dev["features"].([]any)
	features := map[string]bool{}
	for _, feature := range rawFeatures {
		if name, ok := feature.(string); ok {
			features[name] = true
		}
	}
	for _, want := range []string{"manageSettings", "manageApplicationSettings", "manageProviders", "manageSkills", "manageMcp", "manageCron", "manageStats", "manageMemory", "manageSkillHub", "manageSkillHubCatalog", "manageExperts", "manageKnowledgeBases", "knowledgeGraphIndex", "knowledgeBaseContext"} {
		if !features[want] {
			t.Fatalf("features = %#v, want %q", rawFeatures, want)
		}
	}
	// Phase 0/1 discovery keys must survive (additive rule).
	for _, want := range []string{"artifactProjection", "attachmentFetch", "runStatus", "sessionMeta", "projects", "workspaceExtend", "decisionDeadline", "subagentEvents", "toolResultImages", "attachmentList"} {
		if !features[want] {
			t.Fatalf("features lost pre-existing key %q: %#v", want, rawFeatures)
		}
	}
	if result["protocolVersion"] != float64(protocolVersion) {
		t.Fatalf("protocol version changed: %#v", result["protocolVersion"])
	}
}

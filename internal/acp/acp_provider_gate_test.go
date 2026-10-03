package acp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
)

// A missing or unusable default provider must never take the ACP host down:
// management/configuration stay available and only execution entry points
// project the structured reason. These unit tests pin the gate and the
// management-plane refresh that adopts a newly configured key live.

func gateResponse(t *testing.T, out *bytes.Buffer) map[string]any {
	t.Helper()
	var message map[string]any
	if err := json.Unmarshal(out.Bytes(), &message); err != nil {
		t.Fatalf("response %q: %v", out.String(), err)
	}
	rpcErr, ok := message["error"].(map[string]any)
	if !ok {
		t.Fatalf("response = %#v, want error", message)
	}
	data, _ := rpcErr["data"].(map[string]any)
	if data == nil || data["code"] != "provider_unusable" || strings.TrimSpace(rpcErr["message"].(string)) == "" {
		t.Fatalf("error = %#v, want provider_unusable with message", rpcErr)
	}
	if fix, _ := data["fix"].(string); strings.TrimSpace(fix) == "" {
		t.Fatalf("error data = %#v, want fix", data)
	}
	return rpcErr
}

func TestProviderGateBlocksExecutionEntryPoints(t *testing.T) {
	cwd := t.TempDir()
	var out bytes.Buffer
	s := &server{
		w:       &out,
		cwd:     cwd,
		version: "0.3.1",
		settings: &config.Settings{
			DefaultProvider: "gated",
			Providers:       map[string]*config.ProviderConfig{},
		},
		providerStartup: &startupError{
			Code:    "provider_unusable",
			Message: "default provider gated has no API key",
			Fix:     "Configure the provider API key and base URL",
		},
		sessions: map[string]*sessionRuntime{
			"session-1": {runtime: &agentruntime.SessionRuntime{}},
		},
	}
	if !s.providerUnavailable() {
		t.Fatalf("server without providers must be gated")
	}

	s.handleNewSession(rpcRequest{ID: json.RawMessage("1"), Params: gateParams(t, map[string]any{"cwd": cwd})})
	gateResponse(t, &out)

	out.Reset()
	s.handlePrompt(rpcRequest{ID: json.RawMessage("2"), Params: gateParams(t, map[string]any{
		"sessionId": "session-1",
		"prompt":    []map[string]any{{"type": "text", "text": "hello"}},
	})})
	gateResponse(t, &out)
}

func TestRefreshProviderCatalogAdoptsConfiguredKey(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "gate-provider"
	settings.DefaultModel = "gate-model"
	settings.Providers = map[string]*config.ProviderConfig{
		"gate-provider": {
			APIKey:  "${GATE_PROVIDER_KEY}",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "gate-model", Name: "Gate Model"}},
		},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	s := &server{settings: loaded, cwd: t.TempDir()}

	s.refreshProviderCatalog()
	if s.p != nil || s.providerStartup == nil || !s.providerUnavailable() {
		t.Fatalf("after refresh without key: p=%v startup=%v", s.p, s.providerStartup)
	}

	t.Setenv("GATE_PROVIDER_KEY", "test-key")
	s.refreshProviderCatalog()
	if s.p == nil || s.providerStartup != nil || s.providerUnavailable() {
		t.Fatalf("after refresh with key: p=%v startup=%v", s.p, s.providerStartup)
	}
	if s.providerName != "gate-provider" || s.m == nil || s.m.ID != "gate-model" {
		t.Fatalf("refreshed selection = %q / %#v", s.providerName, s.m)
	}
}

func gateParams(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

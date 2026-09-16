package acp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/startvibecoding/mothx/internal/config"
)

func TestManageSkillHubGetTokenFree(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.SkillHub.Markets = []config.SkillHubMarketSettings{
		{ID: "skillhub.cn", Name: "SkillHub CN", SiteURL: "https://skillhub.cn", APIURL: "https://api.skillhub.cn", Enabled: true, APIToken: "sh-secret-123456"},
		{ID: "clawhub.ai", Name: "ClawHub", SiteURL: "https://clawhub.ai", Enabled: false, APIToken: ""},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/skillhub/get", map[string]any{}))
	if result["defaultMarket"] != "skillhub.cn" {
		t.Fatalf("defaultMarket = %#v, want skillhub.cn", result["defaultMarket"])
	}
	if result["defaultInstallScope"] != "project" {
		t.Fatalf("defaultInstallScope = %#v, want project", result["defaultInstallScope"])
	}
	handles, _ := result["officialHandles"].([]any)
	if len(handles) == 0 {
		t.Fatalf("officialHandles should be present")
	}
	markets, _ := result["markets"].([]any)
	if len(markets) != 2 {
		t.Fatalf("markets = %#v, want 2 entries", markets)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sh-secret-123456") {
		t.Fatalf("skillhub view leaks apiToken: %s", encoded)
	}
	cn := manageFindSkillHubMarket(t, result, "skillhub.cn")
	if cn["apiTokenConfigured"] != true {
		t.Fatalf("skillhub.cn apiTokenConfigured = %#v, want true", cn["apiTokenConfigured"])
	}
	claw := manageFindSkillHubMarket(t, result, "clawhub.ai")
	if claw["apiTokenConfigured"] != false {
		t.Fatalf("clawhub.ai apiTokenConfigured = %#v, want false", claw["apiTokenConfigured"])
	}
}

func TestManageSkillHubPatchRoundTrip(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.SkillHub.Markets = []config.SkillHubMarketSettings{
		{ID: "skillhub.cn", Name: "SkillHub CN", SiteURL: "https://skillhub.cn", APIURL: "https://api.skillhub.cn", Enabled: true, APIToken: "sh-secret-123456"},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	patch := map[string]any{
		"defaultMarket":       "clawhub.ai",
		"defaultInstallScope": "global",
		"officialHandles":     []string{"user_new"},
		"markets": []map[string]any{
			{"id": "skillhub.cn", "name": "SkillHub CN Updated", "siteURL": "https://skillhub.cn", "apiURL": "https://api.skillhub.cn", "enabled": true},
			{"id": "clawhub.ai", "name": "ClawHub", "siteURL": "https://clawhub.ai", "apiURL": "https://api.clawhub.ai", "enabled": true, "apiToken": "claw-secret-789"},
		},
	}
	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/skillhub/patch", map[string]any{"patch": patch}))
	if result["defaultMarket"] != "clawhub.ai" || result["defaultInstallScope"] != "global" {
		t.Fatalf("patch did not update defaults: %#v", result)
	}
	if encoded, _ := json.Marshal(result); strings.Contains(string(encoded), "sh-secret-123456") || strings.Contains(string(encoded), "claw-secret-789") {
		t.Fatalf("patch response leaks apiToken: %s", encoded)
	}

	raw := readManageRawFile(t, config.GlobalSettingsPath())
	skillHubRaw := raw["skillHub"]
	var skillHub map[string]json.RawMessage
	if err := json.Unmarshal(skillHubRaw, &skillHub); err != nil {
		t.Fatal(err)
	}
	var markets []map[string]json.RawMessage
	if err := json.Unmarshal(skillHub["markets"], &markets); err != nil {
		t.Fatal(err)
	}
	if len(markets) != 2 {
		t.Fatalf("saved markets = %#v, want 2", markets)
	}
	cn := markets[0]
	if string(cn["apiToken"]) != `"sh-secret-123456"` {
		t.Fatalf("skillhub.cn token not preserved, got %s", cn["apiToken"])
	}
	claw := markets[1]
	if string(claw["apiToken"]) != `"claw-secret-789"` {
		t.Fatalf("clawhub.ai token not saved, got %s", claw["apiToken"])
	}
}

func TestManageSkillHubPatchClearsToken(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.SkillHub.Markets = []config.SkillHubMarketSettings{
		{ID: "skillhub.cn", Name: "SkillHub CN", Enabled: true, APIToken: "sh-secret-123456"},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)

	patch := map[string]any{
		"markets": []map[string]any{{"id": "skillhub.cn", "name": "SkillHub CN", "enabled": true, "clearApiToken": true}},
	}
	manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/skillhub/patch", map[string]any{"patch": patch}))
	raw := readManageRawFile(t, config.GlobalSettingsPath())
	var skillHub map[string]json.RawMessage
	if err := json.Unmarshal(raw["skillHub"], &skillHub); err != nil {
		t.Fatal(err)
	}
	var markets []map[string]json.RawMessage
	if err := json.Unmarshal(skillHub["markets"], &markets); err != nil {
		t.Fatal(err)
	}
	if len(markets) != 1 {
		t.Fatalf("markets = %#v", markets)
	}
	if _, ok := markets[0]["apiToken"]; ok {
		t.Fatalf("apiToken should have been cleared, got %s", markets[0]["apiToken"])
	}
}

func TestManageSkillHubPatchValidation(t *testing.T) {
	cases := []struct {
		name  string
		patch map[string]any
		code  string
		field string
	}{
		{
			name:  "empty market id",
			patch: map[string]any{"markets": []map[string]any{{"id": "   ", "name": "x"}}},
			code:  "skillhub_field_invalid",
		},
		{
			name:  "duplicate market id",
			patch: map[string]any{"markets": []map[string]any{{"id": "a", "name": "A"}, {"id": "a", "name": "B"}}},
			code:  "skillhub_field_invalid",
		},
		{
			name:  "unknown top-level field",
			patch: map[string]any{"unknownField": "x"},
			code:  "skillhub_field_not_allowed",
		},
		{
			name:  "unknown market field",
			patch: map[string]any{"markets": []map[string]any{{"id": "a", "unknown": "x"}}},
			code:  "skillhub_market_field_not_allowed",
			field: "markets[0]",
		},
		{
			name:  "invalid install scope",
			patch: map[string]any{"defaultInstallScope": "workspace"},
			code:  "skillhub_field_invalid",
		},
		{
			name:  "invalid token type",
			patch: map[string]any{"markets": []map[string]any{{"id": "a", "apiToken": true}}},
			code:  "skillhub_field_invalid",
		},
		{
			name:  "conflicting token directives",
			patch: map[string]any{"markets": []map[string]any{{"id": "a", "apiToken": "secret", "clearApiToken": true}}},
			code:  "skillhub_field_invalid",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configDir := t.TempDir()
			t.Setenv("MOTHX_DIR", configDir)
			if err := config.SaveGlobalSettings(config.DefaultSettings()); err != nil {
				t.Fatal(err)
			}
			output := &syncedBuffer{}
			srv := newManageFixtureServer(output, configDir)
			message := callManageFixture(t, srv, output, 1, "mothx/manage/skillhub/patch", map[string]any{"patch": tc.patch})
			code, data := manageFixtureError(t, message)
			if code != tc.code {
				t.Fatalf("error code = %q, want %q; message = %#v", code, tc.code, message)
			}
			if tc.field != "" && data["field"] != tc.field {
				t.Fatalf("error field = %#v, want %q; data = %#v", data["field"], tc.field, data)
			}
		})
	}
}

func TestManageSkillHubRedaction(t *testing.T) {
	settings := &config.Settings{
		SkillHub: config.SkillHubSettings{
			Markets: []config.SkillHubMarketSettings{
				{ID: "skillhub.cn", APIToken: "sh-secret-abcdef"},
			},
		},
	}
	message := manageRedactSecrets("upstream rejected sh-secret-abcdef after 1s", settings)
	if strings.Contains(message, "sh-secret-abcdef") {
		t.Fatalf("redaction failed: %q", message)
	}
	if !strings.Contains(message, "***") {
		t.Fatalf("redaction should mask, got %q", message)
	}
}

func manageFindSkillHubMarket(t *testing.T, result map[string]any, id string) map[string]any {
	t.Helper()
	markets, _ := result["markets"].([]any)
	for _, entry := range markets {
		market, _ := entry.(map[string]any)
		if market["id"] == id {
			return market
		}
	}
	t.Fatalf("market %q missing from view: %#v", id, markets)
	return nil
}

func TestManageSkillHubPreservesUnknownFields(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	path := filepath.Join(configDir, "settings.json")
	data := []byte(`{
  "skillHub": {
    "defaultMarket": "skillhub.cn",
    "customSibling": "kept",
    "markets": [
      {"id": "skillhub.cn", "name": "SkillHub CN", "enabled": true, "apiToken": "sh-secret-123", "customField": "preserved"}
    ]
  }
}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, configDir)
	patch := map[string]any{
		"markets": []map[string]any{{"id": "skillhub.cn", "name": "SkillHub CN Updated", "enabled": true}},
	}
	manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/skillhub/patch", map[string]any{"patch": patch}))

	raw := readManageRawFile(t, path)
	var skillHub map[string]json.RawMessage
	if err := json.Unmarshal(raw["skillHub"], &skillHub); err != nil {
		t.Fatal(err)
	}
	if string(skillHub["customSibling"]) != `"kept"` {
		t.Fatalf("customSibling not preserved, got %s", skillHub["customSibling"])
	}
	var markets []map[string]json.RawMessage
	if err := json.Unmarshal(skillHub["markets"], &markets); err != nil {
		t.Fatal(err)
	}
	if len(markets) != 1 {
		t.Fatalf("markets = %#v", markets)
	}
	if string(markets[0]["customField"]) != `"preserved"` {
		t.Fatalf("customField not preserved, got %s", markets[0]["customField"])
	}
	if string(markets[0]["apiToken"]) != `"sh-secret-123"` {
		t.Fatalf("apiToken not preserved, got %s", markets[0]["apiToken"])
	}
}

func TestManageSkillHubDefaultMarketFallsBackToProductDefault(t *testing.T) {
	if got := manageSkillHubDefaultMarket(nil); got != "skillhub.cn" {
		t.Fatalf("nil settings defaultMarket = %q, want skillhub.cn", got)
	}
	blank := config.DefaultSettings()
	blank.SkillHub.DefaultMarket = "  "
	if got := manageSkillHubDefaultMarket(blank); got != "skillhub.cn" {
		t.Fatalf("blank defaultMarket = %q, want skillhub.cn", got)
	}
	custom := config.DefaultSettings()
	custom.SkillHub.DefaultMarket = "clawhub.ai"
	if got := manageSkillHubDefaultMarket(custom); got != "clawhub.ai" {
		t.Fatalf("configured defaultMarket = %q, want clawhub.ai", got)
	}
}

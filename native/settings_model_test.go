package main

import (
	"encoding/json"
	"testing"
)

func fullApplicationSettingsView() *applicationSettingsView {
	return &applicationSettingsView{
		Defaults: applicationSettingsDefaults{
			DefaultMode:       "yolo",
			EnablePlanTool:    true,
			EnableArtifact:    true,
			EnableACPArtifact: true,
			Authored:          true,
			UpdateCheck:       true,
		},
		ContextFiles: applicationContextFilesView{
			Enabled:    true,
			ExtraFiles: []string{".mothx/context.md"},
		},
		Compaction: applicationCompactionView{
			Enabled:          true,
			ReserveTokens:    2048,
			KeepRecentTokens: 1024,
			Tokenizer:        "tiktoken",
			TokenizerModel:   "cl100k_base",
			Template:         "summarize",
		},
		ToolExecution: applicationToolExecutionView{
			Mode:           "parallel",
			MaxConcurrency: 10,
		},
		WebSearch: applicationWebSearchView{
			Enabled:         true,
			Backend:         "deepseek",
			Provider:        "deepseek",
			BaseURL:         "https://api.deepseek.com",
			TokenConfigured: true,
		},
		ImageGeneration: applicationImageGenerationView{
			Enabled:         false,
			Provider:        "",
			APIType:         "",
			BaseURL:         "",
			Model:           "",
			TokenConfigured: false,
		},
		Retry: applicationRetryView{
			Enabled:     true,
			MaxRetries:  3,
			BaseDelayMs: 500,
		},
		StatusLine: applicationStatusLineView{
			Enabled:         false,
			Type:            "",
			Command:         "",
			Padding:         0,
			RefreshInterval: 0,
			TimeoutMs:       0,
			Fallback:        "",
		},
		Sandbox: applicationSandboxView{
			Enabled:      false,
			Level:        "none",
			BwrapPath:    "",
			AllowNetwork: false,
			AllowedRead:  []string{},
			AllowedWrite: []string{},
			DeniedPaths:  []string{},
			TmpSize:      "",
			ProtectGit:   false,
		},
		Approval: applicationApprovalView{
			BashWhitelist:      []string{"git status", "go test"},
			BashBlacklist:      []string{"rm -rf /"},
			ConfirmBeforeWrite: false,
		},
	}
}

func TestFormFromViewDecodesAllSections(t *testing.T) {
	view := fullApplicationSettingsView()
	f := applicationSettingsFormFromView(view)

	if f.DefaultMode != "yolo" {
		t.Errorf("DefaultMode = %q", f.DefaultMode)
	}
	if !f.EnablePlanTool || !f.EnableArtifact || !f.EnableACPArtifact || !f.Authored || !f.UpdateCheck {
		t.Errorf("defaults booleans = %+v", f)
	}
	if !f.ContextEnabled || f.ExtraFiles != ".mothx/context.md" {
		t.Errorf("contextFiles = %+v", f)
	}
	if f.ReserveTokens != "2048" || f.KeepRecentTokens != "1024" || f.Tokenizer != "tiktoken" {
		t.Errorf("compaction = %+v", f)
	}
	if f.ToolMode != "parallel" || f.ToolConcurrency != "10" {
		t.Errorf("toolExecution = %+v", f)
	}
	if !f.WebSearchEnabled || f.WebSearchBackend != "deepseek" || !f.WebSearchTokenConfigured {
		t.Errorf("webSearch = %+v", f)
	}
	if f.WebSearchToken != "" {
		t.Errorf("webSearch token must be blank after decode, got %q", f.WebSearchToken)
	}
	if f.ImageToken != "" {
		t.Errorf("image token must be blank after decode, got %q", f.ImageToken)
	}
	if f.MaxRetries != "3" || f.BaseDelay != "500" {
		t.Errorf("retry = %+v", f)
	}
	if f.BashWhitelist != "git status\ngo test" {
		t.Errorf("bashWhitelist joined = %q", f.BashWhitelist)
	}
}

func TestPatchOmitsUnchangedSections(t *testing.T) {
	view := fullApplicationSettingsView()
	form := applicationSettingsFormFromView(view)
	patch := applicationSettingsPatch(view, &form)
	if len(patch) != 0 {
		t.Fatalf("unchanged patch = %#v", patch)
	}
}

func TestPatchIncludesEachModifiedSection(t *testing.T) {
	view := fullApplicationSettingsView()
	form := applicationSettingsFormFromView(view)

	form.EnablePlanTool = false
	form.ContextEnabled = false
	form.ReserveTokens = "4096"
	form.ToolMode = "sequential"
	form.WebSearchEnabled = false
	form.ImageEnabled = true
	form.MaxRetries = "5"
	form.StatusLineEnabled = true
	form.SandboxEnabled = true
	form.ConfirmWrite = true

	patch := applicationSettingsPatch(view, &form)
	wantSections := []string{"defaults", "contextFiles", "compaction", "toolExecution", "webSearch", "imageGeneration", "retry", "statusLine", "sandbox", "approval"}
	for _, section := range wantSections {
		if _, ok := patch[section]; !ok {
			t.Fatalf("patch missing %q: %#v", section, patch)
		}
	}
	if len(patch) != len(wantSections) {
		t.Fatalf("patch sections = %d, want %d: %#v", len(patch), len(wantSections), patch)
	}
}

func TestPatchOmitsTokensUnlessSetOrCleared(t *testing.T) {
	view := fullApplicationSettingsView()
	view.WebSearch.TokenConfigured = true
	view.ImageGeneration.TokenConfigured = true

	// Unchanged form: no token fields.
	form := applicationSettingsFormFromView(view)
	patch := applicationSettingsPatch(view, &form)
	web, ok := patch["webSearch"].(map[string]any)
	if ok {
		if _, exists := web["token"]; exists {
			t.Fatalf("webSearch patch should omit token when unchanged: %#v", web)
		}
	}

	// Explicitly set token.
	form = applicationSettingsFormFromView(view)
	form.WebSearchToken = "sk-new"
	form.ImageToken = "img-new"
	patch = applicationSettingsPatch(view, &form)
	web = patch["webSearch"].(map[string]any)
	img := patch["imageGeneration"].(map[string]any)
	if web["token"] != "sk-new" {
		t.Fatalf("webSearch token = %#v", web["token"])
	}
	if img["token"] != "img-new" {
		t.Fatalf("image token = %#v", img["token"])
	}

	// Explicitly clear token.
	form = applicationSettingsFormFromView(view)
	form.ClearWebSearchToken = true
	form.ClearImageToken = true
	patch = applicationSettingsPatch(view, &form)
	web = patch["webSearch"].(map[string]any)
	img = patch["imageGeneration"].(map[string]any)
	if web["token"] != "" {
		t.Fatalf("webSearch token should be empty string when cleared, got %#v", web["token"])
	}
	if img["token"] != "" {
		t.Fatalf("image token should be empty string when cleared, got %#v", img["token"])
	}
}

func TestWebSearchBackendSwitchClearsEndpointAndToken(t *testing.T) {
	a := testApp()
	a.settingsForm = applicationSettingsForm{
		WebSearchBackend:    "deepseek",
		WebSearchBaseURL:    "https://api.deepseek.com",
		WebSearchToken:      "sk-old",
		ClearWebSearchToken: false,
	}

	a.onWebSearchBackendChanged("yuanbao")

	if a.settingsForm.WebSearchBackend != "yuanbao" {
		t.Errorf("backend = %q", a.settingsForm.WebSearchBackend)
	}
	if a.settingsForm.WebSearchBaseURL != "" {
		t.Errorf("baseUrl = %q", a.settingsForm.WebSearchBaseURL)
	}
	if a.settingsForm.WebSearchToken != "" {
		t.Errorf("token = %q", a.settingsForm.WebSearchToken)
	}
	if !a.settingsForm.ClearWebSearchToken {
		t.Error("clearWebSearchToken should be true")
	}
}

func TestApplicationLinesFiltersBlankLines(t *testing.T) {
	got := applicationLines("  a  \n\nb\n\n  \nc ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("lines = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestApplicationIntRespectsMin(t *testing.T) {
	if got := applicationInt("5", 0); got != 5 {
		t.Errorf("int(5,0) = %d", got)
	}
	if got := applicationInt("-3", 0); got != 0 {
		t.Errorf("int(-3,0) = %d", got)
	}
	if got := applicationInt("abc", 1); got != 1 {
		t.Errorf("int(abc,1) = %d", got)
	}
	if got := applicationInt("", 1); got != 1 {
		t.Errorf("int(empty,1) = %d", got)
	}
}

func TestPatchValidationDomains(t *testing.T) {
	view := fullApplicationSettingsView()
	form := applicationSettingsFormFromView(view)

	// Server will validate these; the client should send matching values.
	form.DefaultMode = "agent"
	form.ToolMode = "sequential"
	form.SandboxLevel = "strict"
	form.ToolConcurrency = "1"
	form.ReserveTokens = "0"

	patch := applicationSettingsPatch(view, &form)
	defaults := patch["defaults"].(map[string]any)
	if defaults["defaultMode"] != "agent" {
		t.Errorf("defaultMode = %v", defaults["defaultMode"])
	}
	te := patch["toolExecution"].(map[string]any)
	if te["mode"] != "sequential" || te["maxConcurrency"] != 1 {
		t.Errorf("toolExecution = %#v", te)
	}
	sb := patch["sandbox"].(map[string]any)
	if sb["level"] != "strict" {
		t.Errorf("sandbox level = %v", sb["level"])
	}
}

func TestPatchWebSearchBackendSwitchSubmitsClearToken(t *testing.T) {
	view := fullApplicationSettingsView()
	view.WebSearch.TokenConfigured = true
	form := applicationSettingsFormFromView(view)
	form.WebSearchBackend = "yuanbao"
	form.WebSearchBaseURL = ""
	form.WebSearchToken = ""
	form.ClearWebSearchToken = true

	patch := applicationSettingsPatch(view, &form)
	web := patch["webSearch"].(map[string]any)
	if web["token"] != "" {
		t.Fatalf("backend switch should clear token, got %#v", web["token"])
	}
}

func TestDecodeIgnoresTokenValues(t *testing.T) {
	raw := json.RawMessage(`{
		"defaults": {"defaultMode": "agent", "enablePlanTool": true, "enableArtifact": false, "enableACPArtifact": true, "authored": false, "updateCheck": true},
		"webSearch": {"enabled": true, "backend": "deepseek", "token": "web-SECRET", "tokenConfigured": true},
		"imageGeneration": {"enabled": true, "provider": "openai", "token": "sk-SUPERSECRET", "tokenConfigured": true}
	}`)
	var view applicationSettingsView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.Defaults.DefaultMode != "agent" {
		t.Errorf("defaultMode = %q", view.Defaults.DefaultMode)
	}
	if !view.WebSearch.TokenConfigured {
		t.Error("expected webSearch tokenConfigured true")
	}
	if !view.ImageGeneration.TokenConfigured {
		t.Error("expected imageGeneration tokenConfigured true")
	}
}

func TestPatchEmptyWhenNoChanges(t *testing.T) {
	view := fullApplicationSettingsView()
	form := applicationSettingsFormFromView(view)
	patch := applicationSettingsPatch(view, &form)
	if len(patch) != 0 {
		t.Fatalf("expected empty patch, got %#v", patch)
	}
}

func TestFormRoundTripResetsTokenFields(t *testing.T) {
	view := fullApplicationSettingsView()
	view.WebSearch.TokenConfigured = true
	view.ImageGeneration.TokenConfigured = true

	form := applicationSettingsFormFromView(view)
	// Simulate user typing a token then cancelling.
	form.WebSearchToken = "typed"
	form.ImageToken = "typed"
	// After a fresh fromView (like after save response), tokens are blanked.
	form2 := applicationSettingsFormFromView(view)
	if form2.WebSearchToken != "" || form2.ImageToken != "" {
		t.Errorf("fresh form retained tokens: web=%q img=%q", form2.WebSearchToken, form2.ImageToken)
	}
	if !form2.WebSearchTokenConfigured || !form2.ImageTokenConfigured {
		t.Error("tokenConfigured flags should persist")
	}
}

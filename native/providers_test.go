package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func providerTestCatalog() *providerCatalogView {
	cache := true
	maxImages := 3
	return &providerCatalogView{
		Providers: []providerSummary{
			{Name: "deepseek", MaskedKey: stringPtr("sk-***abc"), BaseURL: "https://api.deepseek.com", ModelCount: 2, APIKeyConfigured: true, IsDefault: true},
			{Name: "local", BaseURL: "http://127.0.0.1:8080", ModelCount: 1},
		},
		ProviderConfigs: []providerConfigView{
			{
				ID:               "deepseek",
				MaskedKey:        stringPtr("sk-***abc"),
				APIKeyConfigured: true,
				IsDefault:        true,
				GlobalOverride:   true,
				Provider: providerConfigShape{
					Vendor:              "deepseek",
					BaseURL:             "https://api.deepseek.com",
					ForceHTTP11:         true,
					API:                 "openai-chat",
					ThinkingFormat:      "deepseek",
					CacheControl:        &cache,
					MaxImagesPerRequest: &maxImages,
					Models: []providerModel{
						{ID: "deepseek-chat", Name: "DeepSeek Chat", Reasoning: true, ContextWindow: 128000, MaxTokens: 8192, Input: []string{"text"}},
						{ID: "deepseek-reasoner", Name: "DeepSeek Reasoner", Reasoning: true, ContextWindow: 256000, MaxTokens: 8192, Input: []string{"text"}},
					},
				},
			},
			{
				ID: "local",
				Provider: providerConfigShape{
					API:     "openai-chat",
					BaseURL: "http://127.0.0.1:8080",
					Models: []providerModel{
						{ID: "local-model", Name: "Local", ContextWindow: 32768, Input: []string{"text"}},
					},
				},
			},
		},
		Models: []providerCatalogModel{
			{ID: "deepseek-chat", Name: "DeepSeek Chat", Provider: "deepseek", Reasoning: true, ContextWindow: 128000, MaxTokens: 8192, Input: []string{"text"}},
			{ID: "deepseek-reasoner", Name: "DeepSeek Reasoner", Provider: "deepseek", Reasoning: true, ContextWindow: 256000, MaxTokens: 8192, Input: []string{"text"}},
			{ID: "local-model", Name: "Local", Provider: "local", ContextWindow: 32768, Input: []string{"text"}},
		},
		ModelDefaults: providerModelDefaults{
			Reasoning:     true,
			ContextWindow: 256000,
			Input:         []string{"text"},
		},
		DefaultProvider: "deepseek",
		DefaultModel:    "deepseek-chat",
	}
}

func providerTestSettings() *providerSettingsView {
	return &providerSettingsView{
		DefaultProvider: "deepseek",
		DefaultModel:    "deepseek-chat",
		ThinkingLevel:   "high",
		Providers: []providerSummary{
			{Name: "deepseek", MaskedKey: stringPtr("sk-***abc"), BaseURL: "https://api.deepseek.com", ModelCount: 2, APIKeyConfigured: true, IsDefault: true},
			{Name: "local", BaseURL: "http://127.0.0.1:8080", ModelCount: 1},
		},
	}
}

func stringPtr(s string) *string { return &s }

func installProviderFixture(a *nativeApp) {
	a.providers = newProviderPanelState()
	a.providers.Loaded = true
	a.providers.Catalog = providerTestCatalog()
	a.providers.Settings = providerTestSettings()
	a.normalizeProviderDefaults()
	a.selectProviderDraft("deepseek", false)
}

func providerTestApp(t *testing.T, features map[string]bool) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "providers"
	a.ensureSettingsTabVisible()
	installProviderFixture(a)
	return a
}

func fullProviderFeatures() map[string]bool {
	return map[string]bool{
		"manageApplicationSettings": true,
		"manageProviders":           true,
		"manageProviderConfig":      true,
		"manageSettings":            true,
	}
}

func TestProviderConfigDecodeRedactsSecrets(t *testing.T) {
	raw := json.RawMessage(`{
		"id": "deepseek",
		"provider": {
			"vendor": "deepseek",
			"baseUrl": "https://api.deepseek.com",
			"httpProxy": "http://127.0.0.1:7890",
			"forceHTTP11": true,
			"api": "openai-chat",
			"thinkingFormat": "deepseek",
			"cacheControl": true,
			"maxImagesPerRequest": 3,
			"models": [{"id":"deepseek-chat","name":"Chat","reasoning":true,"contextWindow":128000,"maxTokens":8192,"input":["text","image"]}]
		},
		"maskedKey": "sk-***abc",
		"apiKeyConfigured": true,
		"isDefault": true,
		"globalOverride": true
	}`)
	var view providerConfigView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.ID != "deepseek" || view.MaskedKey == nil || *view.MaskedKey != "sk-***abc" || !view.APIKeyConfigured {
		t.Fatalf("provider identity = %+v", view)
	}
	if view.Provider.Vendor != "deepseek" || view.Provider.HTTPProxy != "http://127.0.0.1:7890" || !view.Provider.ForceHTTP11 {
		t.Fatalf("provider connection = %+v", view.Provider)
	}
	if view.Provider.CacheControl == nil || !*view.Provider.CacheControl || view.Provider.MaxImagesPerRequest == nil || *view.Provider.MaxImagesPerRequest != 3 {
		t.Fatalf("provider advanced = %+v", view.Provider)
	}
	if len(view.Provider.Models) != 1 {
		t.Fatalf("models = %+v", view.Provider.Models)
	}
	model := view.Provider.Models[0]
	if model.ID != "deepseek-chat" || model.ContextWindow != 128000 || model.MaxTokens != 8192 || !model.Reasoning || len(model.Input) != 2 {
		t.Fatalf("model = %+v", model)
	}

	draft := providerDraftFromConfig(view)
	if draft.ID != "deepseek" || draft.APIKeyConfigured != true || draft.MaskedKey != "sk-***abc" {
		t.Fatalf("draft = %+v", draft)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"apiKey"`) {
		t.Fatalf("wire projection must not carry an apiKey field: %s", encoded)
	}
}

func TestProviderCatalogDecode(t *testing.T) {
	raw := json.RawMessage(`{
		"providers": [{"name":"deepseek","maskedKey":"sk-***abc","baseUrl":"https://api.deepseek.com","modelCount":2,"apiKeyConfigured":true,"isDefault":true}],
		"providerConfigs": [{"id":"deepseek","provider":{"api":"openai-chat","models":[{"id":"deepseek-chat"}]},"maskedKey":"sk-***abc","apiKeyConfigured":true,"isDefault":true,"globalOverride":true}],
		"models": [{"id":"deepseek-chat","name":"Chat","provider":"deepseek","reasoning":true,"contextWindow":128000,"maxTokens":8192,"input":["text"]}],
		"modelDefaults": {"reasoning":true,"contextWindow":256000,"maxTokens":8192,"input":["text"]},
		"defaultProvider": "deepseek",
		"defaultModel": "deepseek-chat"
	}`)
	var catalog providerCatalogView
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Providers) != 1 || catalog.Providers[0].Name != "deepseek" || !catalog.Providers[0].APIKeyConfigured {
		t.Fatalf("providers = %+v", catalog.Providers)
	}
	if len(catalog.ProviderConfigs) != 1 || catalog.ProviderConfigs[0].ID != "deepseek" || !catalog.ProviderConfigs[0].IsDefault || !catalog.ProviderConfigs[0].GlobalOverride {
		t.Fatalf("providerConfigs = %+v", catalog.ProviderConfigs)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].Provider != "deepseek" || catalog.Models[0].ContextWindow != 128000 || catalog.Models[0].MaxTokens != 8192 {
		t.Fatalf("models = %+v", catalog.Models)
	}
	if !catalog.ModelDefaults.Reasoning || catalog.ModelDefaults.ContextWindow != 256000 || len(catalog.ModelDefaults.Input) != 1 {
		t.Fatalf("modelDefaults = %+v", catalog.ModelDefaults)
	}
	if catalog.DefaultProvider != "deepseek" || catalog.DefaultModel != "deepseek-chat" {
		t.Fatalf("defaults = %+v", catalog)
	}
}

func TestProviderCatalogModelSelection(t *testing.T) {
	catalog := providerTestCatalog()
	models := providerModelsFor(catalog, "deepseek")
	if len(models) != 2 || models[0].ID != "deepseek-chat" || models[1].ID != "deepseek-reasoner" {
		t.Fatalf("deepseek models = %+v", models)
	}
	if got := providerModelIDsFor(catalog, "local"); len(got) != 1 || got[0] != "local-model" {
		t.Fatalf("local model ids = %v", got)
	}
	if got := providerDefaultModelFor(catalog, "deepseek", "deepseek-reasoner"); got != "deepseek-reasoner" {
		t.Fatalf("preferred model = %q", got)
	}
	if got := providerDefaultModelFor(catalog, "deepseek", "missing"); got != "deepseek-chat" {
		t.Fatalf("fallback model = %q", got)
	}
	if got := providerDefaultModelFor(catalog, "unknown", "x"); got != "" {
		t.Fatalf("unknown provider model = %q", got)
	}
}

func TestProviderCatalogFilteringAndConfiguredScope(t *testing.T) {
	panel := newProviderPanelState()
	panel.Catalog = providerTestCatalog()
	panel.Scope = providerScopeConfigured
	if got := (&nativeApp{providers: panel}).providerFilteredConfigs(); len(got) != 1 || got[0].ID != "deepseek" {
		t.Fatalf("configured providers = %+v", got)
	}
	app := &nativeApp{providers: panel}
	app.providers.Scope = providerScopeAll
	if got := app.providerFilteredConfigs(); len(got) != 2 {
		t.Fatalf("all providers = %+v", got)
	}
	app.providers.Search = "127.0.0.1"
	if got := app.providerFilteredConfigs(); len(got) != 1 || got[0].ID != "local" {
		t.Fatalf("searched providers = %+v", got)
	}
}

func TestProviderDefaultsPatchShape(t *testing.T) {
	patch, err := buildProviderDefaultsPatch("deepseek", "deepseek-chat", "high")
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 3 {
		t.Fatalf("patch keys = %#v", patch)
	}
	if patch["defaultProvider"] != "deepseek" || patch["defaultModel"] != "deepseek-chat" || patch["thinkingLevel"] != "high" {
		t.Fatalf("patch = %#v", patch)
	}
	if _, err := buildProviderDefaultsPatch("", "m", "high"); err == nil {
		t.Fatal("empty provider should be rejected")
	}
	if _, err := buildProviderDefaultsPatch("p", "", "high"); err == nil {
		t.Fatal("empty model should be rejected")
	}
	if _, err := buildProviderDefaultsPatch("p", "m", "unknown"); err == nil {
		t.Fatal("unknown thinking level should be rejected")
	}
}

func TestProviderSavePayloadOmitsKeyUnlessExplicit(t *testing.T) {
	draft := &providerDraft{
		ID:                  "sample",
		Vendor:              "sample",
		BaseURL:             "https://example.invalid",
		HTTPProxy:           "http://127.0.0.1:7890",
		API:                 "openai-chat",
		ThinkingFormat:      "sample",
		ForceHTTP11:         true,
		CacheControl:        true,
		MaxImagesPerRequest: "3",
		Models: []providerModelDraft{{
			ID: "sample-chat", Name: "Sample", Reasoning: true,
			ContextWindow: "128000", MaxTokens: "8192", Input: "text, image",
		}},
	}
	catalog := providerTestCatalog()
	request, err := buildProviderSaveRequest(draft, providerSecretDraft{}, "", catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := request["apiKey"]; present {
		t.Fatalf("ordinary save must omit apiKey: %#v", request)
	}
	provider := request["provider"].(map[string]any)
	if provider["cacheControl"] != true || provider["forceHTTP11"] != true || provider["maxImagesPerRequest"] != 3 {
		t.Fatalf("provider payload = %#v", provider)
	}
	models := provider["models"].([]map[string]any)
	if len(models) != 1 || models[0]["id"] != "sample-chat" || models[0]["contextWindow"] != 128000 || models[0]["maxTokens"] != 8192 {
		t.Fatalf("model payload = %#v", models)
	}
	input := models[0]["input"].([]string)
	if len(input) != 2 || input[0] != "text" || input[1] != "image" {
		t.Fatalf("model input = %#v", input)
	}

	explicit, err := buildProviderSaveRequest(draft, providerSecretDraft{APIKey: "  test-write-only-key  "}, "", catalog)
	if err != nil {
		t.Fatal(err)
	}
	if explicit["apiKey"] != "test-write-only-key" {
		t.Fatalf("explicit key = %#v", explicit["apiKey"])
	}
	cleared, err := buildProviderSaveRequest(draft, providerSecretDraft{ClearKey: true}, "", catalog)
	if err != nil {
		t.Fatal(err)
	}
	if cleared["apiKey"] != "" {
		t.Fatalf("clear key = %#v", cleared["apiKey"])
	}

	clearImages := *draft
	clearImages.MaxImagesPerRequest = ""
	withoutLimit, err := buildProviderSaveRequest(&clearImages, providerSecretDraft{}, "", catalog)
	if err != nil {
		t.Fatal(err)
	}
	if withoutLimit["provider"].(map[string]any)["maxImagesPerRequest"] != 0 {
		t.Fatalf("blank image limit should clear to zero: %#v", withoutLimit["provider"])
	}
}

func TestProviderSavePayloadIncludesRenameAndOmitsEmptySecret(t *testing.T) {
	draft := providerDraftFromConfig(providerTestCatalog().ProviderConfigs[0])
	draft.ID = "deepseek-renamed"
	request, err := buildProviderSaveRequest(&draft, providerSecretDraft{}, "deepseek", providerTestCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if request["previousId"] != "deepseek" || request["id"] != "deepseek-renamed" {
		t.Fatalf("rename payload = %#v", request)
	}
	if _, present := request["apiKey"]; present {
		t.Fatalf("rename must not send apiKey: %#v", request)
	}
}

func TestProviderValidationRejectsInvalidDraft(t *testing.T) {
	catalog := providerTestCatalog()
	cases := []struct {
		name  string
		draft providerDraft
	}{
		{"empty provider id", providerDraft{ID: ""}},
		{"provider slash", providerDraft{ID: "a/b"}},
		{"duplicate provider id", providerDraft{ID: "local"}},
		{"empty model id", providerDraft{ID: "new", Models: []providerModelDraft{{ID: ""}}}},
		{"duplicate model id", providerDraft{ID: "new", Models: []providerModelDraft{{ID: "m"}, {ID: "m"}}}},
		{"negative context", providerDraft{ID: "new", Models: []providerModelDraft{{ID: "m", ContextWindow: "-1"}}}},
		{"negative max tokens", providerDraft{ID: "new", Models: []providerModelDraft{{ID: "m", MaxTokens: "-2"}}}},
		{"non-numeric context", providerDraft{ID: "new", Models: []providerModelDraft{{ID: "m", ContextWindow: "many"}}}},
		{"bad image limit", providerDraft{ID: "new", MaxImagesPerRequest: "-2"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := validateProviderDraft(&tc.draft, catalog, ""); err == nil {
				t.Fatalf("draft %+v should fail validation", tc.draft)
			}
		})
	}
	valid := providerDraft{
		ID: "new", MaxImagesPerRequest: "-1",
		Models: []providerModelDraft{{ID: "m", ContextWindow: "0", MaxTokens: "0"}},
	}
	if err := validateProviderDraft(&valid, catalog, ""); err != nil {
		t.Fatalf("valid draft rejected: %v", err)
	}
}

func TestProviderCapabilityFallback(t *testing.T) {
	a := providerTestApp(t, map[string]bool{"manageProviders": true})
	caps := a.providerCapabilities()
	if !caps.Catalog || caps.Config || caps.Settings {
		t.Fatalf("caps = %+v", caps)
	}

	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"供应商目录", "deepseek", "测试连接"} {
		if !textPresent(tt, want) {
			t.Fatalf("fallback panel missing %q: %q", want, tt.Texts())
		}
	}
	for _, forbidden := range []string{"保存供应商", "API 密钥", "默认供应商与模型", "设为默认", "新建供应商"} {
		if tt.HasText(forbidden) {
			t.Fatalf("fallback panel exposed unavailable control %q: %q", forbidden, tt.Texts())
		}
	}
}

func TestProviderDefaultsRequireManageSettings(t *testing.T) {
	a := providerTestApp(t, map[string]bool{"manageProviders": true, "manageSettings": true})
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"默认供应商与模型", "保存默认值", "设为默认"} {
		if !textPresent(tt, want) {
			t.Fatalf("settings-backed fallback missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("保存供应商") {
		t.Fatalf("editor must stay hidden without manageProviderConfig: %q", tt.Texts())
	}
}

func TestProviderPanelHeadlessRendersCatalogEditorAndModels(t *testing.T) {
	a := providerTestApp(t, fullProviderFeatures())
	tt := ui.NewTester(a.view, 1180, 900)
	want := []string{
		"默认供应商与模型", "默认供应商", "默认模型", "思考级别", "保存默认值",
		"供应商目录", "新建供应商", "deepseek", "已配置", "保存供应商", "测试连接",
		"连接", "模型", "高级", "API 密钥", "Base URL", "HTTP 代理", "强制 HTTP/1.1",
	}
	for _, text := range want {
		if !textPresent(tt, text) {
			t.Fatalf("providers panel missing %q: %q", text, tt.Texts())
		}
	}
	if strings.Contains(strings.Join(tt.Texts(), " "), "sk-***abc") == false {
		t.Fatalf("masked key state should be visible without the secret: %q", tt.Texts())
	}
	if a.providers.Secret.APIKey != "" {
		t.Fatal("provider secret draft must begin empty")
	}

	// Switch to the model editor without any ACP call.
	a.providers.EditorTab = providerEditorModels
	tt.Frame()
	for _, text := range []string{"模型目录", "添加模型", "发现模型", "deepseek-chat", "上下文窗口", "最大 Tokens", "输入类型"} {
		if !textPresent(tt, text) {
			t.Fatalf("model editor missing %q: %q", text, tt.Texts())
		}
	}
}

func TestProviderDeleteRequiresNativeConfirmationAndBlocksDefault(t *testing.T) {
	a := providerTestApp(t, fullProviderFeatures())
	a.providers.Draft.IsDefault = true
	a.requestDeleteProvider()
	if a.providers.PendingDelete {
		t.Fatal("default provider must not open the delete confirmation")
	}
	if !a.providers.StatusIsError || !strings.Contains(a.providers.Status, "默认供应商不能移除") {
		t.Fatalf("default delete status = %q", a.providers.Status)
	}

	a.providers.Draft.IsDefault = false
	a.requestDeleteProvider()
	if !a.providers.PendingDelete {
		t.Fatal("non-default provider should open the confirmation dialog")
	}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, text := range []string{"重置或移除供应商配置？", "取消", "确认移除", "不会发送或显示任何 API 密钥"} {
		if !textPresent(tt, text) {
			t.Fatalf("delete dialog missing %q: %q", text, tt.Texts())
		}
	}
}

func TestProviderSecretScrubAndModelDefaults(t *testing.T) {
	message := providerScrubSecret("request failed for test-write-only-key", " test-write-only-key ")
	if strings.Contains(message, "test-write-only-key") || !strings.Contains(message, "***") {
		t.Fatalf("scrubbed message = %q", message)
	}
	if got := providerScrubSecret("plain error", ""); got != "plain error" {
		t.Fatalf("empty secret changed message = %q", got)
	}
	defaults := providerModelDefaultsFor(providerTestCatalog())
	if !defaults.Reasoning || defaults.ContextWindow != "256000" || defaults.Input != "text" {
		t.Fatalf("catalog model defaults = %+v", defaults)
	}
	empty := providerModelDefaultsFor(nil)
	if !empty.Reasoning || empty.ContextWindow != "256000" || empty.Input != "text" {
		t.Fatalf("fallback model defaults = %+v", empty)
	}
}

func TestProviderPanelRejectsStaleReloadSequence(t *testing.T) {
	a := providerTestApp(t, fullProviderFeatures())
	seq := atomic.LoadInt64(&a.providers.Seq)
	reloadSeq := atomic.LoadInt64(&a.reloadSeq)
	if !a.providerPanelCurrent(seq, reloadSeq) {
		t.Fatal("current provider request should be accepted")
	}
	atomic.AddInt64(&a.reloadSeq, 1)
	if a.providerPanelCurrent(seq, reloadSeq) {
		t.Fatal("a response from the previous ACP connection must be rejected")
	}
	a.closeSettings()
	if a.settingsOpen || a.providers.Loaded || a.providers.Draft != nil {
		t.Fatalf("closing settings must invalidate provider state: %+v", a.providers)
	}
}

func TestProviderSelectionAndTabExitInvalidateInflightState(t *testing.T) {
	a := providerTestApp(t, fullProviderFeatures())
	a.providers.Status = "正在测试连接…"
	a.providers.Testing = true
	seq := atomic.LoadInt64(&a.providers.Seq)
	a.selectProviderDraft("local", true)
	if atomic.LoadInt64(&a.providers.Seq) <= seq {
		t.Fatal("selection change must bump the provider request sequence")
	}
	if a.providers.Testing || a.providers.Draft == nil || a.providers.Draft.ID != "local" {
		t.Fatalf("selection change did not reset transient state: %+v", a.providers)
	}
	oldSeq := atomic.LoadInt64(&a.providers.Seq)
	a.setSettingsTab("application")
	if atomic.LoadInt64(&a.providers.Seq) <= oldSeq {
		t.Fatal("leaving the provider tab must invalidate in-flight state")
	}
	if a.providers.Loaded || a.providers.Draft != nil || a.providers.ActiveID != "" {
		t.Fatalf("provider state was not invalidated on tab exit: %+v", a.providers)
	}
	if a.providerPanelCurrent(oldSeq, atomic.LoadInt64(&a.reloadSeq)) {
		t.Fatal("stale provider response must be rejected after leaving the tab")
	}
}

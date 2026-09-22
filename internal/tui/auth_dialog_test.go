package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tui/components/editor"
	"github.com/oschina/mothx/internal/tui/i18n"
)

type retryConfigRecordingProvider struct {
	config *provider.RetryConfig
	model  *provider.Model
}

func (p *retryConfigRecordingProvider) Chat(context.Context, provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch
}

func (p *retryConfigRecordingProvider) Name() string              { return "retry-recording" }
func (p *retryConfigRecordingProvider) API() string               { return "openai-chat" }
func (p *retryConfigRecordingProvider) Models() []*provider.Model { return []*provider.Model{p.model} }
func (p *retryConfigRecordingProvider) GetModel(id string) *provider.Model {
	if p.model != nil && p.model.ID == id {
		return p.model
	}
	return nil
}
func (p *retryConfigRecordingProvider) SetRetryConfig(cfg *provider.RetryConfig) {
	p.config = cfg
}

func TestApplyRuntimeSettingsAfterSaveRefreshesProviderRetryConfig(t *testing.T) {
	settings := config.DefaultSettings()
	settings.Retry = config.RetrySettings{Enabled: false, MaxRetries: 7, BaseDelayMs: 1250}
	p := &retryConfigRecordingProvider{model: &provider.Model{ID: "m1"}}
	a := &App{settings: settings, provider: p, model: p.model}

	a.applyRuntimeSettingsAfterSave("retry.maxRetries", settings)

	if p.config == nil || p.config.Enabled || p.config.MaxRetries != 7 || p.config.BaseDelayMs != 1250 {
		t.Fatalf("provider retry config = %#v", p.config)
	}
}

func TestAuthBuildSettingsPreservesExistingModelConfig(t *testing.T) {
	temp := 0.7
	topP := 0.9
	s := config.DefaultSettings()
	a := &App{
		settings: s,
		auth: authDialogState{
			ProviderID: "deepseek-openai",
			SetDefault: true,
			Provider: providerEditState{
				API:         "openai-chat",
				BaseURL:     "https://api.deepseek.com",
				HTTPProxy:   "http://127.0.0.1:7890",
				ForceHTTP11: true,
				APIKey:      "test-key",
			},
			Models: map[string]*modelEditState{
				"deepseek-v4-pro": {
					ID:              "deepseek-v4-pro",
					Name:            "DeepSeek V4 Pro",
					ContextWindow:   200000,
					MaxTokens:       10000,
					MaxTokensEdited: true,
					Reasoning:       true,
					Input:           []string{"text", "image"},
					Temperature:     &temp,
					TopP:            &topP,
				},
				"custom-model": {
					ID:              "custom-model",
					Name:            "custom-model",
					ContextWindow:   200000,
					MaxTokens:       10000,
					MaxTokensEdited: true,
					Reasoning:       true,
					Input:           []string{"text", "image"},
					Temperature:     &temp,
					TopP:            &topP,
				},
			},
			ModelOrder: []string{"deepseek-v4-pro", "custom-model"},
		},
	}

	next, modelID := a.buildAuthSettings()
	if modelID != "deepseek-v4-pro" {
		t.Fatalf("modelID = %q, want deepseek-v4-pro", modelID)
	}
	pc := next.GetProviderConfig("deepseek-openai")
	if pc == nil || len(pc.Models) != 2 {
		t.Fatalf("models = %#v, want 2 models", pc)
	}
	if pc.HTTPProxy != "http://127.0.0.1:7890" {
		t.Fatalf("httpProxy = %q", pc.HTTPProxy)
	}
	if !pc.ForceHTTP11 {
		t.Fatal("forceHTTP11 = false, want true")
	}
	if !pc.Models[0].Reasoning || pc.Models[0].ContextWindow != 200000 || pc.Models[0].MaxTokens != 10000 {
		t.Fatalf("existing model config/overrides unexpected: %#v", pc.Models[0])
	}
	if pc.Models[0].Temperature == nil || *pc.Models[0].Temperature != 0.7 {
		t.Fatalf("temperature override missing: %#v", pc.Models[0].Temperature)
	}
	if pc.Models[0].TopP == nil || *pc.Models[0].TopP != 0.9 {
		t.Fatalf("top_p override missing: %#v", pc.Models[0].TopP)
	}
	if pc.Models[1].ID != "custom-model" || pc.Models[1].ContextWindow != 200000 || pc.Models[1].MaxTokens != 10000 {
		t.Fatalf("custom model default config unexpected: %#v", pc.Models[1])
	}
	if next.DefaultProvider != "deepseek-openai" || next.DefaultModel != "deepseek-v4-pro" {
		t.Fatalf("defaults = %s/%s", next.DefaultProvider, next.DefaultModel)
	}
}

func TestAuthBuildSettingsFromUsesProvidedBase(t *testing.T) {
	runtime := config.DefaultSettings()
	runtime.DefaultProvider = "project-provider"
	runtime.Providers["project-only"] = &config.ProviderConfig{API: "openai-chat", BaseURL: "https://project.test", APIKey: "project", Models: []config.ModelConfig{{ID: "project-model", Name: "Project"}}}
	global := &config.Settings{Providers: map[string]*config.ProviderConfig{"xiaomi": runtime.Providers["xiaomi"]}}
	a := &App{settings: runtime, auth: authDialogState{
		ProviderID: "openrouter",
		SetDefault: true,
		Provider: providerEditState{
			API:     "openai-chat",
			BaseURL: "https://openrouter.ai/api/v1",
			APIKey:  "test",
		},
		Models: map[string]*modelEditState{
			"z-ai/glm-4.5-air:free": {
				ID:              "z-ai/glm-4.5-air:free",
				Name:            "z-ai/glm-4.5-air:free",
				ContextWindow:   128000,
				MaxTokens:       8192,
				MaxTokensEdited: true,
				Input:           []string{"text"},
			},
		},
		ModelOrder: []string{"z-ai/glm-4.5-air:free"},
	}}

	next, _ := a.buildAuthSettingsFrom(global)
	if next.GetProviderConfig("project-only") != nil {
		t.Fatal("project-only provider leaked into global settings patch")
	}
	if next.GetProviderConfig("deepseek-openai") != nil {
		t.Fatal("default provider leaked into sparse global settings patch")
	}
	if next.GetProviderConfig("xiaomi") == nil || next.GetProviderConfig("openrouter") == nil {
		t.Fatalf("expected sparse global providers xiaomi/openrouter, got %#v", next.Providers)
	}
	if next.DefaultProvider != "openrouter" {
		t.Fatalf("DefaultProvider = %q, want openrouter", next.DefaultProvider)
	}
}

func TestAuthAPIKeyInputStaysSingleLine(t *testing.T) {
	a := &App{width: 48}
	a.auth = authDialogState{
		Open: true,
		View: authViewProviderCredentials,
		Provider: providerEditState{
			APIKey: "sk-" + strings.Repeat("very-long-token-segment", 8),
		},
		ParamField: "apiKey",
	}
	a.prepareAuthProviderInput()

	view := stripANSI(a.authInput.View())
	if strings.Count(view, "\n") != 0 {
		t.Fatalf("auth API key input wrapped into multiple lines:\n%s", view)
	}
}

func TestAuthExistingCustomProvidersRemainVisible(t *testing.T) {
	s := &config.Settings{Providers: map[string]*config.ProviderConfig{
		"xiaomi":   {},
		"gitee":    {},
		"gitee-cc": {},
		"doubao":   {},
	}}
	ids := sortedAuthProviderIDs(s)
	for _, want := range []string{"xiaomi", "gitee", "gitee-cc", "doubao"} {
		found := false
		for _, id := range ids {
			if id == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("provider %q missing from auth list: %#v", want, ids)
		}
	}
}

func TestAuthRenderUsesTranslator(t *testing.T) {
	a := &App{
		translator: i18n.New(i18n.LanguageZH),
		width:      80,
		auth: authDialogState{
			Open:           true,
			View:           authViewAddModelName,
			CurrentModelID: "model-x",
		},
	}

	if got := a.authTitle(authViewAddModelName); got != "添加模型 · 名称" {
		t.Fatalf("auth title = %q", got)
	}
	if got := a.authInputPrompt(authViewAddModelName); got != "输入“model-x”的显示名称（留空则使用 ID）：" {
		t.Fatalf("auth prompt = %q", got)
	}

	a.auth.View = authViewExistingProvider
	a.auth.Search = "missing"
	got := a.renderAuthDialog()
	if !strings.Contains(got, "搜索：missing") || !strings.Contains(got, "← 返回") {
		t.Fatalf("provider search view was not localized: %q", got)
	}
}

func TestAuthPreviewTruncationUsesTranslator(t *testing.T) {
	preview := strings.Repeat("line\n", authMaxPreviewVisibleLines+1)
	lines := renderAuthPreview(preview, i18n.New(i18n.LanguageZH))
	if got := lines[len(lines)-1]; !strings.Contains(got, "另有 1 行已隐藏") {
		t.Fatalf("preview truncation = %q", got)
	}
}
func TestOpenSettingsDialogShowsRootMenu(t *testing.T) {
	a := &App{
		settings: config.DefaultSettings(),
		width:    80,
		input:    editor.New(80),
	}

	a.openSettingsDialog(nil)
	if !a.auth.Open {
		t.Fatal("settings dialog not open")
	}
	if a.auth.View != authViewSettingsRoot {
		t.Fatalf("view = %v, want authViewSettingsRoot", a.auth.View)
	}
	if a.auth.Mode != "settings" {
		t.Fatalf("mode = %q, want settings", a.auth.Mode)
	}
	if a.auth.SetDefault {
		t.Fatal("settings dialog should not default provider edits to changing default model")
	}
	opts := a.authOptions()
	if len(opts) == 0 || opts[0].Value != "providers" {
		t.Fatalf("first setting option = %#v, want providers", opts)
	}
}

func TestSettingsRootProvidersBranchReturnsToRoot(t *testing.T) {
	a := &App{
		settings: config.DefaultSettings(),
		width:    80,
		input:    editor.New(80),
		auth: authDialogState{
			Open: true,
			View: authViewSettingsRoot,
			Mode: "settings",
		},
	}

	a.selectSettingsRoot("providers")
	if a.auth.View != authViewExistingProvider {
		t.Fatalf("view = %v, want authViewExistingProvider", a.auth.View)
	}
	if len(a.auth.Stack) != 1 || a.auth.Stack[0] != authViewSettingsRoot {
		t.Fatalf("stack = %#v, want root", a.auth.Stack)
	}

	a.popAuthView()
	if a.auth.View != authViewSettingsRoot {
		t.Fatalf("view after pop = %v, want authViewSettingsRoot", a.auth.View)
	}
}

func TestSettingsFieldPatchSavesGlobalTopLevelOnly(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MOTHX_DIR", tmpDir)
	path := filepath.Join(tmpDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"defaultProvider":"deepseek-openai"}`), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	a := &App{
		settings:  config.DefaultSettings(),
		width:     80,
		mode:      "agent",
		input:     editor.New(80),
		authInput: editor.New(80).SetValue("4242"),
		auth: authDialogState{
			Open:       true,
			View:       authViewSettingsBehavior,
			Mode:       "settings",
			ParamField: "maxContextTokens",
		},
	}

	if err := a.authSettingsSubmitInput(); err != nil {
		t.Fatalf("submit settings input: %v", err)
	}
	if a.auth.ParamField != "" {
		t.Fatalf("ParamField = %q, want cleared", a.auth.ParamField)
	}
	if a.settings.MaxContextTokens != 4242 {
		t.Fatalf("MaxContextTokens = %d, want 4242", a.settings.MaxContextTokens)
	}
	if a.mode != "agent" {
		t.Fatalf("mode = %q, want unchanged agent", a.mode)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, `"maxContextTokens": 4242`) || !strings.Contains(text, `"defaultProvider": "deepseek-openai"`) {
		t.Fatalf("settings patch missing expected fields:\n%s", text)
	}
	if strings.Contains(text, `"providers"`) || strings.Contains(text, `"statusLine"`) || strings.Contains(text, `"sandbox"`) {
		t.Fatalf("settings patch expanded unrelated top-level config:\n%s", text)
	}
}

func TestSettingsAuthoredToggleSavesGlobalValue(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MOTHX_DIR", tmpDir)
	path := filepath.Join(tmpDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"defaultProvider":"deepseek-openai"}`), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	a := &App{
		settings: config.DefaultSettings(),
		width:    80,
		mode:     "agent",
		input:    editor.New(80),
		auth: authDialogState{
			Open: true,
			View: authViewSettingsBehavior,
			Mode: "settings",
		},
	}

	a.selectSettingsFieldValue("authored")
	if !a.settings.Authored {
		t.Fatal("authored setting did not turn on")
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read enabled settings: %v", err)
	}
	if !strings.Contains(string(out), `"authored": true`) {
		t.Fatalf("enabled authored setting was not persisted: %s", out)
	}

	a.selectSettingsFieldValue("authored")
	if a.settings.Authored {
		t.Fatal("authored setting did not turn off")
	}
	out, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read disabled settings: %v", err)
	}
	if !strings.Contains(string(out), `"authored": false`) {
		t.Fatalf("disabled authored setting was not persisted: %s", out)
	}
}

func TestAuthTextInputsTrimOuterWhitespace(t *testing.T) {
	a := &App{settings: config.DefaultSettings(), width: 80}

	a.auth = authDialogState{
		Open: true,
		View: authViewCustomID,
	}
	a.authInput = editor.New(80).SetValue("  openrouter  ")
	a.submitAuthInput()
	if a.auth.ProviderID != "openrouter" {
		t.Fatalf("ProviderID = %q, want trimmed openrouter", a.auth.ProviderID)
	}

	a.auth = authDialogState{
		Open:       true,
		View:       authViewProviderCredentials,
		ParamField: "apiKey",
	}
	a.authInput = editor.New(80).SetValue("  sk-test with middle  space  ")
	if err := a.authProviderSubmitInput(); err != nil {
		t.Fatalf("submit api key: %v", err)
	}
	if got := a.auth.Provider.APIKey; got != "sk-test with middle  space" {
		t.Fatalf("APIKey = %q, want trimmed value with middle spaces preserved", got)
	}

	a.auth = authDialogState{
		Open:       true,
		View:       authViewHeadersEdit,
		ParamField: "headerKey",
		Provider:   providerEditState{},
	}
	a.authInput = editor.New(80).SetValue("  X-Test Header  ")
	if err := a.authProviderSubmitInput(); err != errStayInInput {
		t.Fatalf("submit header key err = %v, want errStayInInput", err)
	}
	if a.auth.ParamFieldKey != "X-Test Header" {
		t.Fatalf("ParamFieldKey = %q, want trimmed header key", a.auth.ParamFieldKey)
	}
	a.authInput = editor.New(80).SetValue("  Bearer abc  def  ")
	if err := a.authProviderSubmitInput(); err != nil {
		t.Fatalf("submit header value: %v", err)
	}
	if got := a.auth.Provider.Headers["X-Test Header"]; got != "Bearer abc  def" {
		t.Fatalf("header value = %q, want trimmed value with middle spaces preserved", got)
	}

	a.auth = authDialogState{
		Open:           true,
		View:           authViewModelBasics,
		ParamField:     "name",
		CurrentModelID: "m1",
		Models: map[string]*modelEditState{
			"m1": {ID: "m1", Name: "m1", Input: []string{"text"}},
		},
	}
	a.authInput = editor.New(80).SetValue("  Claude Sonnet  4  ")
	if err := a.authModelSubmitInput(); err != nil {
		t.Fatalf("submit model name: %v", err)
	}
	if got := a.auth.Models["m1"].Name; got != "Claude Sonnet  4" {
		t.Fatalf("model name = %q, want trimmed value with middle spaces preserved", got)
	}
}

func TestParseApprovalPrefixesTrimsLineBoundaries(t *testing.T) {
	got := parseApprovalPrefixes("  go test  \n\t echo hello  world \t\n\n  go test  \n")
	want := []string{"go test", "echo hello  world"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAuthSparsePatchPreservesExistingProviders(t *testing.T) {
	global := &config.Settings{Providers: map[string]*config.ProviderConfig{
		"xiaomi":   {API: "openai-chat", BaseURL: "https://old.xiaomi", APIKey: "old", Models: []config.ModelConfig{{ID: "mimo", Name: "MiMo"}}},
		"gitee":    {API: "openai-chat", BaseURL: "https://gitee.test", APIKey: "gitee", Models: []config.ModelConfig{{ID: "gitee-model", Name: "Gitee"}}},
		"gitee-cc": {API: "openai-chat", BaseURL: "https://cc.gitee.test", APIKey: "gitee-cc", Models: []config.ModelConfig{{ID: "cc-model", Name: "Gitee CC"}}},
		"doubao":   {API: "openai-chat", BaseURL: "https://ark.cn-beijing.volces.com/api/v3", APIKey: "doubao", Models: []config.ModelConfig{{ID: "doubao-model", Name: "Doubao"}}},
	}}
	a := &App{settings: config.DefaultSettings(), auth: authDialogState{
		ProviderID: "xiaomi",
		SetDefault: true,
		Provider: providerEditState{
			API:     "openai-chat",
			BaseURL: "https://new.xiaomi",
			APIKey:  "new",
		},
		Models: map[string]*modelEditState{
			"mimo": {ID: "mimo", Name: "MiMo", ContextWindow: 128000, MaxTokens: 8192, MaxTokensEdited: true, Input: []string{"text"}},
		},
		ModelOrder: []string{"mimo"},
	}}
	next, _ := a.buildAuthSettingsFrom(global)
	for _, want := range []string{"xiaomi", "gitee", "gitee-cc", "doubao"} {
		if next.GetProviderConfig(want) == nil {
			t.Fatalf("provider %q was dropped from sparse patch: %#v", want, next.Providers)
		}
	}
	if next.GetProviderConfig("openai") != nil || next.GetProviderConfig("deepseek-openai") != nil {
		t.Fatalf("default providers leaked into sparse patch: %#v", next.Providers)
	}
	if next.GetProviderConfig("xiaomi").BaseURL != "https://new.xiaomi" {
		t.Fatalf("xiaomi baseURL not updated: %#v", next.GetProviderConfig("xiaomi"))
	}
}

func TestAuthExistingProviderLoadsForceHTTP11(t *testing.T) {
	a := &App{
		settings: &config.Settings{Providers: map[string]*config.ProviderConfig{
			"custom": {API: "openai-chat", BaseURL: "https://custom.test", APIKey: "key", ForceHTTP11: true, Models: []config.ModelConfig{{ID: "model", Name: "Model"}}},
		}},
		auth: authDialogState{Open: true, View: authViewExistingProvider},
	}

	// Simulate selecting the provider from the list (cursor at "custom")
	a.auth.Cursor = 0
	a.selectAuthOption()
	if !a.auth.Provider.ForceHTTP11 {
		t.Fatal("ForceHTTP11 was not loaded from provider config")
	}
	if a.auth.View != authViewProviderGroupList {
		t.Fatalf("view = %v, want authViewProviderGroupList", a.auth.View)
	}
}

func TestAuthPushViewClearsStaleInputField(t *testing.T) {
	a := &App{
		auth: authDialogState{
			Open:          true,
			View:          authViewProviderGroupList,
			ParamField:    "apiKey",
			ParamFieldKey: "old",
		},
	}

	a.pushAuthView(authViewProviderAdvanced)
	if a.auth.View != authViewProviderAdvanced {
		t.Fatalf("view = %v, want authViewProviderAdvanced", a.auth.View)
	}
	if a.auth.ParamField != "" || a.auth.ParamFieldKey != "" {
		t.Fatalf("stale param field = %q/%q", a.auth.ParamField, a.auth.ParamFieldKey)
	}
	if a.authInputActive() {
		t.Fatal("advanced view should open as a menu, not an input")
	}
}

func TestAuthProviderToggleDoesNotLeaveInputActive(t *testing.T) {
	a := &App{auth: authDialogState{Open: true, View: authViewProviderAdvanced}}

	a.selectProviderFieldValue("cacheControl")
	if a.auth.Provider.CacheControl == nil || !*a.auth.Provider.CacheControl {
		t.Fatalf("cacheControl = %#v, want enabled", a.auth.Provider.CacheControl)
	}
	if a.auth.ParamField != "" || a.authInputActive() {
		t.Fatalf("toggle left input active: field=%q", a.auth.ParamField)
	}
}

func TestAuthProviderMaxImagesInput(t *testing.T) {
	a := &App{auth: authDialogState{Open: true, View: authViewProviderAdvanced}}
	a.auth.ParamField = "maxImagesPerRequest"
	a.authInput = editor.New(80).SetValue("-1")
	if err := a.authProviderSubmitInput(); err != nil {
		t.Fatalf("submit image limit: %v", err)
	}
	if a.auth.Provider.MaxImagesPerRequest != -1 {
		t.Fatalf("MaxImagesPerRequest = %d, want -1", a.auth.Provider.MaxImagesPerRequest)
	}

	a.auth.ParamField = "maxImagesPerRequest"
	a.authInput = editor.New(80).SetValue("invalid")
	if err := a.authProviderSubmitInput(); err == nil {
		t.Fatal("expected invalid image limit error")
	}
}

func TestAuthProviderSubMenuDoesNotCarryInputField(t *testing.T) {
	a := &App{
		auth: authDialogState{
			Open:       true,
			View:       authViewProviderProtocol,
			ParamField: "apiKey",
		},
	}

	a.selectProviderFieldValue("responses")
	if a.auth.View != authViewResponsesEdit {
		t.Fatalf("view = %v, want authViewResponsesEdit", a.auth.View)
	}
	if a.auth.ParamField != "" || a.authInputActive() {
		t.Fatalf("responses view opened as input: field=%q", a.auth.ParamField)
	}
}

func TestAuthProviderSortOrder(t *testing.T) {
	s := &config.Settings{Providers: map[string]*config.ProviderConfig{
		"google-gemini":      {},
		"anthropic":          {},
		"openai":             {},
		"doubao":             {},
		"xiaomi":             {},
		"deepseek-openai":    {},
		"moark":              {},
		"z-other":            {},
		"deepseek-anthropic": {},
	}}
	ids := sortedAuthProviderIDs(s)
	wantPrefix := []string{"moark", "deepseek-anthropic", "deepseek-openai", "xiaomi", "doubao", "openai", "anthropic", "google-gemini"}
	if len(ids) < len(wantPrefix) {
		t.Fatalf("ids too short: %#v", ids)
	}
	for i, want := range wantPrefix {
		if ids[i] != want {
			t.Fatalf("ids[%d] = %q, want %q (all: %#v)", i, ids[i], want, ids)
		}
	}
}

func TestAuthProviderSearchFilter(t *testing.T) {
	ids := []string{"moark", "deepseek-openai", "xiaomi", "openai", "moonshotai", "moonshotai-cn"}
	got := filterAuthProviderIDs(ids, "moon")
	want := []string{"moonshotai", "moonshotai-cn"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
	got = filterAuthProviderIDs(ids, "ai")
	if len(got) == 0 || got[0] != "deepseek-openai" {
		t.Fatalf("contains search should keep priority ordering, got %#v", got)
	}
}

func TestAuthBaseURLOptionsForProvider(t *testing.T) {
	for _, tc := range []struct {
		provider string
		wantMin  int
	}{
		{provider: "minimax", wantMin: 1},
		{provider: "zai", wantMin: 2},
		{provider: "alibaba-standard", wantMin: 4},
		{provider: "alibaba-coding-plan", wantMin: 2},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			opts := baseURLOptionsForProvider(tc.provider)
			if len(opts) < tc.wantMin {
				t.Fatalf("got %d options, want at least %d", len(opts), tc.wantMin)
			}
			for _, opt := range opts {
				if opt.Value == "" || opt.Title == "" {
					t.Fatalf("invalid option: %#v", opt)
				}
			}
		})
	}
}

func TestAuthVisibleRange(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cursor    int
		total     int
		wantStart int
		wantEnd   int
	}{
		{name: "short", cursor: 0, total: 3, wantStart: 0, wantEnd: 3},
		{name: "top", cursor: 0, total: 12, wantStart: 0, wantEnd: 5},
		{name: "middle", cursor: 6, total: 12, wantStart: 4, wantEnd: 9},
		{name: "bottom", cursor: 11, total: 12, wantStart: 7, wantEnd: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotStart, gotEnd := authVisibleRange(tc.cursor, tc.total, authMaxVisibleOptions)
			if gotStart != tc.wantStart || gotEnd != tc.wantEnd {
				t.Fatalf("range = %d:%d, want %d:%d", gotStart, gotEnd, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

func TestResolveProviderConfigMergesDefaults(t *testing.T) {
	// Unknown provider gets safe defaults
	pc := config.ResolveProviderConfig("unknown-provider", nil)
	if pc == nil {
		t.Fatal("expected non-nil provider config")
	}
	if pc.API != "openai-chat" {
		t.Fatalf("API = %q, want openai-chat", pc.API)
	}

	// Known provider gets built-in defaults
	pc = config.ResolveProviderConfig("deepseek-openai", nil)
	if pc == nil {
		t.Fatal("expected non-nil")
	}
	if pc.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("BaseURL = %q", pc.BaseURL)
	}
	defaults := config.DefaultProviderConfig("deepseek-openai")
	if defaults == nil {
		t.Fatal("expected deepseek built-in defaults")
	}
	if len(pc.Models) != len(defaults.Models) {
		t.Fatalf("Models = %d, want %d built-in defaults", len(pc.Models), len(defaults.Models))
	}
	for i, want := range defaults.Models {
		if pc.Models[i].ID != want.ID {
			t.Fatalf("Models[%d] = %q, want %q", i, pc.Models[i].ID, want.ID)
		}
	}

	// Runtime overrides take priority
	runtime := &config.Settings{Providers: map[string]*config.ProviderConfig{
		"deepseek-openai": {BaseURL: "https://custom.deepseek", APIKey: "override-key"},
	}}
	pc = config.ResolveProviderConfig("deepseek-openai", runtime)
	if pc.BaseURL != "https://custom.deepseek" {
		t.Fatalf("BaseURL = %q, want override", pc.BaseURL)
	}
	if pc.APIKey != "override-key" {
		t.Fatalf("APIKey = %q, want override", pc.APIKey)
	}
	// Models from built-in defaults should still be present in the same order.
	if len(pc.Models) != len(defaults.Models) {
		t.Fatalf("Models = %d, want %d (built-in defaults preserved)", len(pc.Models), len(defaults.Models))
	}
	for i, want := range defaults.Models {
		if pc.Models[i].ID != want.ID {
			t.Fatalf("Models[%d] = %q, want %q (built-in defaults preserved)", i, pc.Models[i].ID, want.ID)
		}
	}
}

func TestDefaultModelConfigLookup(t *testing.T) {
	mc := config.DefaultModelConfig("deepseek-openai", "deepseek-v4-flash")
	if mc == nil {
		t.Fatal("expected non-nil")
	}
	if mc.ContextWindow != 1000000 {
		t.Fatalf("ContextWindow = %d", mc.ContextWindow)
	}
	if !mc.Reasoning {
		t.Fatal("Reasoning should be true")
	}
	if mc.Cost == nil {
		t.Fatal("Cost should be set")
	}

	// Unknown model returns nil
	mc = config.DefaultModelConfig("deepseek-openai", "nonexistent")
	if mc != nil {
		t.Fatal("expected nil for unknown model")
	}
}

func TestProviderEditStateRoundTrip(t *testing.T) {
	original := config.ProviderConfig{
		APIKey:              "test-key",
		BaseURL:             "https://api.test.com/v1",
		API:                 "openai-chat",
		Vendor:              "test-vendor",
		HTTPProxy:           "http://proxy:8080",
		ForceHTTP11:         true,
		Headers:             map[string]string{"X-Custom": "value"},
		ThinkingFormat:      "deepseek",
		CacheControl:        config.BoolPtr(true),
		MaxImagesPerRequest: 7,
		Responses: config.ResponsesConfig{
			ReasoningSummary: "concise",
		},
		Models: []config.ModelConfig{{ID: "m1", Name: "Model 1"}},
	}

	pe := providerEditStateFrom(&original)
	result := pe.toConfig()

	if result.APIKey != original.APIKey {
		t.Fatalf("APIKey = %q", result.APIKey)
	}
	if result.BaseURL != original.BaseURL {
		t.Fatalf("BaseURL = %q", result.BaseURL)
	}
	if result.Vendor != original.Vendor {
		t.Fatalf("Vendor = %q", result.Vendor)
	}
	if !result.ForceHTTP11 {
		t.Fatal("ForceHTTP11 lost")
	}
	if result.Headers["X-Custom"] != "value" {
		t.Fatalf("Headers = %#v", result.Headers)
	}
	if result.ThinkingFormat != "deepseek" {
		t.Fatalf("ThinkingFormat = %q", result.ThinkingFormat)
	}
	if result.CacheControl == nil || !*result.CacheControl {
		t.Fatal("CacheControl lost")
	}
	if result.MaxImagesPerRequest != original.MaxImagesPerRequest {
		t.Fatalf("MaxImagesPerRequest = %d, want %d", result.MaxImagesPerRequest, original.MaxImagesPerRequest)
	}
	if result.Responses.ReasoningSummary != "concise" {
		t.Fatalf("Responses.ReasoningSummary = %q", result.Responses.ReasoningSummary)
	}
}

func TestModelEditStateRoundTrip(t *testing.T) {
	var original config.ModelConfig
	if err := json.Unmarshal([]byte(`{
		"id": "test-model",
		"name": "Test Model",
		"contextWindow": 200000,
		"maxTokens": 16000,
		"reasoning": true,
		"input": ["text", "image"],
		"temperature": 0.7,
		"top_p": 0.9,
		"cost": {"input": 0.5, "output": 1.0},
		"compat": {"thinkingFormat": "deepseek"}
	}`), &original); err != nil {
		t.Fatalf("unmarshal model: %v", err)
	}

	me := modelEditStateFromMC(&original)
	if me == nil {
		t.Fatal("expected non-nil")
	}
	result := me.toConfig()

	if result.ID != original.ID {
		t.Fatalf("ID = %q", result.ID)
	}
	if result.Name != original.Name {
		t.Fatalf("Name = %q", result.Name)
	}
	if result.ContextWindow != original.ContextWindow {
		t.Fatalf("ContextWindow = %d", result.ContextWindow)
	}
	if result.MaxTokens != original.MaxTokens {
		t.Fatalf("MaxTokens = %d", result.MaxTokens)
	}
	if !result.MaxTokensWasSet() {
		t.Fatal("MaxTokensWasSet = false, want true")
	}
	if !result.Reasoning {
		t.Fatal("Reasoning lost")
	}
	if len(result.Input) != 2 || result.Input[0] != "text" || result.Input[1] != "image" {
		t.Fatalf("Input = %#v", result.Input)
	}
	if result.Temperature == nil || *result.Temperature != 0.7 {
		t.Fatalf("Temperature = %#v", result.Temperature)
	}
	if result.TopP == nil || *result.TopP != 0.9 {
		t.Fatalf("TopP = %#v", result.TopP)
	}
	if result.Cost == nil || result.Cost.Input != 0.5 || result.Cost.Output != 1.0 {
		t.Fatalf("Cost = %#v", result.Cost)
	}
	if result.Compat == nil || result.Compat.ThinkingFormat != "deepseek" {
		t.Fatalf("Compat = %#v", result.Compat)
	}
}

func TestModelEditStatePreservesExplicitZeroMaxTokens(t *testing.T) {
	me := &modelEditState{ID: "test-model", Name: "Test Model", MaxTokensEdited: true}
	result := me.toConfig()
	if result.MaxTokens != 0 || !result.MaxTokensWasSet() {
		t.Fatalf("maxTokens = %d, explicitly set = %v; want 0, true", result.MaxTokens, result.MaxTokensWasSet())
	}
}
func TestModelEditStateDefaultName(t *testing.T) {
	original := config.ModelConfig{ID: "my-model", ContextWindow: 128000}
	me := modelEditStateFromMC(&original)
	if me.Name != "my-model" {
		t.Fatalf("Name = %q, should default to ID", me.Name)
	}
	result := me.toConfig()
	if result.Name != "my-model" {
		t.Fatalf("Name = %q after round-trip", result.Name)
	}
}

func TestInitAuthForProviderPopulatesStructuredState(t *testing.T) {
	s := config.DefaultSettings()
	a := &App{settings: s}
	a.auth = authDialogState{}
	a.initAuthForProvider("deepseek-openai")

	if a.auth.ProviderID != "deepseek-openai" {
		t.Fatalf("ProviderID = %q", a.auth.ProviderID)
	}
	if a.auth.Provider.API != "openai-chat" {
		t.Fatalf("Provider.API = %q", a.auth.Provider.API)
	}
	if a.auth.Provider.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("Provider.BaseURL = %q", a.auth.Provider.BaseURL)
	}
	defaults := config.DefaultProviderConfig("deepseek-openai")
	if defaults == nil {
		t.Fatal("expected deepseek built-in defaults")
	}
	if len(a.auth.ModelOrder) != len(defaults.Models) {
		t.Fatalf("ModelOrder = %d, want %d", len(a.auth.ModelOrder), len(defaults.Models))
	}
	if len(a.auth.Models) != len(defaults.Models) {
		t.Fatalf("Models = %d, want %d", len(a.auth.Models), len(defaults.Models))
	}
	for i, want := range defaults.Models {
		if a.auth.ModelOrder[i] != want.ID {
			t.Fatalf("ModelOrder[%d] = %q, want %q", i, a.auth.ModelOrder[i], want.ID)
		}
		if _, ok := a.auth.Models[want.ID]; !ok {
			t.Fatalf("Models missing built-in default %q", want.ID)
		}
	}
	// Check per-model params are preserved
	if me, ok := a.auth.Models["deepseek-v4-flash"]; ok {
		if me.ContextWindow != 1000000 {
			t.Fatalf("flash ContextWindow = %d", me.ContextWindow)
		}
		if me.MaxTokens != 384000 {
			t.Fatalf("flash MaxTokens = %d", me.MaxTokens)
		}
		if !me.Reasoning {
			t.Fatal("flash Reasoning should be true")
		}
		if !me.CostEnabled || me.CostInput == 0 {
			t.Fatal("flash Cost should be enabled with non-zero input")
		}
	}
}

func TestInitAuthForCustomUsesGenericTemplate(t *testing.T) {
	s := config.DefaultSettings()
	a := &App{settings: s}
	a.auth = authDialogState{}
	a.initAuthForCustom("my-local-llm")

	if a.auth.ProviderID != "my-local-llm" {
		t.Fatalf("ProviderID = %q", a.auth.ProviderID)
	}
	if a.auth.Provider.API != "openai-chat" {
		t.Fatalf("Provider.API = %q", a.auth.Provider.API)
	}
	if len(a.auth.Models) != 0 {
		t.Fatalf("Models = %d, want 0 for custom", len(a.auth.Models))
	}
}

func TestInitModelFromDefaultFallsBackToGeneric(t *testing.T) {
	s := config.DefaultSettings()
	a := &App{settings: s}
	a.auth = authDialogState{ProviderID: "deepseek-openai"}

	// Known model gets built-in defaults
	me := a.initModelFromDefault("deepseek-v4-flash")
	if me.ContextWindow != 1000000 {
		t.Fatalf("ContextWindow = %d, want 1000000", me.ContextWindow)
	}

	// Unknown model gets generic template
	me = a.initModelFromDefault("totally-unknown-model")
	if me.ContextWindow != 256000 {
		t.Fatalf("ContextWindow = %d, want %d", me.ContextWindow, 256000)
	}
	if me.MaxTokens != 0 {
		t.Fatalf("MaxTokens = %d, want 0 for unknown model", me.MaxTokens)
	}
	if !me.Reasoning {
		t.Fatal("Reasoning = false, want true for unknown model")
	}
}

func TestInitModelFromDefaultRuntimeOverridesBuiltin(t *testing.T) {
	s := &config.Settings{}
	data := []byte(`{
		"providers": {
			"deepseek-openai": {
				"api": "openai-chat",
				"baseUrl": "https://api.deepseek.com",
				"models": [{
					"id": "deepseek-v4-flash",
					"name": "Runtime Flash",
					"contextWindow": 42,
					"maxTokens": 24,
					"reasoning": false,
					"input": ["text"]
				}]
			}
		}
	}`)
	if err := json.Unmarshal(data, s); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	a := &App{settings: s}
	a.auth = authDialogState{ProviderID: "deepseek-openai"}

	me := a.initModelFromDefault("deepseek-v4-flash")
	if me.Name != "Runtime Flash" || me.ContextWindow != 42 || me.MaxTokens != 24 || me.Reasoning {
		t.Fatalf("runtime model override not used: %#v", me)
	}
}

func TestAuthSaveOmitsBuiltinModelMaxTokens(t *testing.T) {
	s := config.DefaultSettings()
	a := &App{settings: s}
	a.auth = authDialogState{}
	a.initAuthForProvider("alibaba-token-plan")

	next, _ := a.buildAuthSettingsFrom(s)
	pc := next.Providers["alibaba-token-plan"]
	if pc == nil {
		t.Fatal("alibaba-token-plan provider missing")
	}
	var kimi *config.ModelConfig
	for i := range pc.Models {
		if pc.Models[i].ID == "kimi-k2.6" {
			kimi = &pc.Models[i]
			break
		}
	}
	if kimi == nil {
		t.Fatal("kimi-k2.6 model missing")
	}
	if kimi.MaxTokens != 0 {
		t.Fatalf("kimi-k2.6 MaxTokens = %d, want 0/omitted for builtin default", kimi.MaxTokens)
	}
}

func TestSelectAPIChoicePreservesCustomBaseURL(t *testing.T) {
	a := &App{}
	a.auth = authDialogState{
		View:  authViewAPIChoice,
		Stack: []authView{authViewProviderGroupList},
		Provider: providerEditState{
			API:     "openai-chat",
			BaseURL: "https://api.deepseek.com",
		},
	}

	a.selectAPIChoice("openai-responses")
	if a.auth.Provider.API != "openai-responses" {
		t.Fatalf("API = %q", a.auth.Provider.API)
	}
	if a.auth.Provider.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("BaseURL was overwritten: %q", a.auth.Provider.BaseURL)
	}
}

func TestSelectAPIChoiceUpdatesEmptyOrOldDefaultBaseURL(t *testing.T) {
	a := &App{}
	a.auth = authDialogState{
		View:  authViewAPIChoice,
		Stack: []authView{authViewProviderGroupList},
		Provider: providerEditState{
			API:     "openai-chat",
			BaseURL: "https://api.openai.com/v1",
		},
	}

	a.selectAPIChoice("anthropic-messages")
	if a.auth.Provider.BaseURL != "https://api.anthropic.com" {
		t.Fatalf("BaseURL = %q, want anthropic default", a.auth.Provider.BaseURL)
	}
}

func TestSaveAuthProviderReloadsEffectiveProjectOverride(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Setenv("MOTHX_DIR", filepath.Join(tmpDir, "config"))

	if err := os.MkdirAll(filepath.Dir(config.ProjectSettingsPath()), 0700); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	projectSettings := []byte(`{
		"providers": {
			"projected": {
				"api": "openai-chat",
				"baseUrl": "https://project.test/v1",
				"models": [{"id": "m1", "name": "Project Model", "contextWindow": 222, "maxTokens": 111}]
			}
		}
	}`)
	if err := os.WriteFile(config.ProjectSettingsPath(), projectSettings, 0600); err != nil {
		t.Fatalf("write project settings: %v", err)
	}
	settings, err := config.LoadSettings()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	a := &App{settings: settings}
	a.auth = authDialogState{
		Open:       true,
		ProviderID: "projected",
		SetDefault: true,
		Provider: providerEditState{
			API:     "openai-chat",
			BaseURL: "https://global.test/v1",
		},
		Models: map[string]*modelEditState{
			"m1": {ID: "m1", Name: "Global Model", ContextWindow: 999, MaxTokens: 888, MaxTokensEdited: true, Input: []string{"text"}},
		},
		ModelOrder: []string{"m1"},
	}

	a.saveAuthProvider()
	if a.auth.Error != "" {
		t.Fatalf("save error: %s", a.auth.Error)
	}
	pc := a.settings.GetProviderConfig("projected")
	if pc == nil {
		t.Fatal("missing provider after reload")
	}
	if pc.BaseURL != "https://project.test/v1" {
		t.Fatalf("effective BaseURL = %q, want project override", pc.BaseURL)
	}
	globalSparse, err := config.LoadGlobalSettingsSparse()
	if err != nil {
		t.Fatalf("load global sparse: %v", err)
	}
	if got := globalSparse.GetProviderConfig("projected").BaseURL; got != "https://global.test/v1" {
		t.Fatalf("global BaseURL = %q, want saved global value", got)
	}
}

func TestAddModelIDAllowsSlashModelIDs(t *testing.T) {
	a := &App{settings: config.DefaultSettings()}
	a.auth = authDialogState{
		Open:       true,
		View:       authViewAddModelID,
		ProviderID: "openrouter",
		Models:     map[string]*modelEditState{},
	}
	a.authInput = editor.New(80).SetValue("anthropic/claude-sonnet-4.6")

	a.submitAuthInput()
	if a.auth.Error != "" {
		t.Fatalf("unexpected error: %s", a.auth.Error)
	}
	if a.auth.CurrentModelID != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("CurrentModelID = %q", a.auth.CurrentModelID)
	}
	if a.auth.View != authViewAddModelName {
		t.Fatalf("View = %v, want authViewAddModelName", a.auth.View)
	}
}

func TestAddModelIDRejectsDuplicate(t *testing.T) {
	a := &App{settings: config.DefaultSettings()}
	a.auth = authDialogState{
		Open:       true,
		View:       authViewAddModelID,
		ProviderID: "deepseek-openai",
		Models: map[string]*modelEditState{
			"deepseek-v4-flash": {ID: "deepseek-v4-flash"},
		},
	}
	a.authInput = editor.New(80).SetValue("deepseek-v4-flash")

	a.submitAuthInput()
	if a.auth.Error != "Model ID already exists." {
		t.Fatalf("Error = %q", a.auth.Error)
	}
}

func TestModelGroupDoneReturnsToPreviousView(t *testing.T) {
	a := &App{settings: config.DefaultSettings()}
	a.auth = authDialogState{
		Open:           true,
		View:           authViewModelGroupList,
		Stack:          []authView{authViewSettingsDetail},
		CurrentModelID: "m1",
		Models:         map[string]*modelEditState{"m1": {ID: "m1", Name: "M1", Input: []string{"text"}}},
		ModelOrder:     []string{"m1"},
	}
	a.auth.Cursor = len(a.authModelGroupOptions()) - 1

	a.selectAuthOption()
	if a.auth.View != authViewSettingsDetail {
		t.Fatalf("View = %v, want authViewSettingsDetail", a.auth.View)
	}
}

func TestAuthModelListShortcutDeletesSelectedModel(t *testing.T) {
	a := &App{settings: config.DefaultSettings()}
	a.auth = authDialogState{
		Open:           true,
		View:           authViewModelList,
		CurrentModelID: "m1",
		Models: map[string]*modelEditState{
			"m1": {ID: "m1", Name: "M1", Input: []string{"text"}},
			"m2": {ID: "m2", Name: "M2", Input: []string{"text"}},
		},
		ModelOrder: []string{"m1", "m2"},
		Cursor:     2,
	}

	handled, _ := a.handleAuthKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if !handled {
		t.Fatal("Backspace was not handled")
	}
	if _, ok := a.auth.Models["m1"]; ok {
		t.Fatal("m1 was not deleted")
	}
	if got := strings.Join(a.auth.ModelOrder, ","); got != "m2" {
		t.Fatalf("ModelOrder = %q, want m2", got)
	}
	if a.auth.CurrentModelID != "m2" {
		t.Fatalf("CurrentModelID = %q, want m2", a.auth.CurrentModelID)
	}

	handled, _ = a.handleAuthKey(tea.KeyMsg{Type: tea.KeyDelete})
	if !handled {
		t.Fatal("Delete was not handled")
	}
	if _, ok := a.auth.Models["m2"]; ok {
		t.Fatal("m2 was not deleted")
	}
	if len(a.auth.ModelOrder) != 0 {
		t.Fatalf("ModelOrder = %#v, want empty", a.auth.ModelOrder)
	}
	if a.auth.CurrentModelID != "" {
		t.Fatalf("CurrentModelID = %q, want empty", a.auth.CurrentModelID)
	}
}

func TestAuthModelListShortcutIgnoresActions(t *testing.T) {
	a := &App{settings: config.DefaultSettings()}
	a.auth = authDialogState{
		Open: true,
		View: authViewModelList,
		Models: map[string]*modelEditState{
			"m1": {ID: "m1", Name: "M1", Input: []string{"text"}},
		},
		ModelOrder: []string{"m1"},
		Cursor:     0,
	}

	a.handleAuthKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if _, ok := a.auth.Models["m1"]; !ok {
		t.Fatal("Add Model action should not delete a model")
	}

	a.auth.Cursor = len(a.authModelListOptions()) - 1
	a.handleAuthKey(tea.KeyMsg{Type: tea.KeyDelete})
	if _, ok := a.auth.Models["m1"]; !ok {
		t.Fatal("Done action should not delete a model")
	}
}

func TestBuildAuthSettingsFromStructuredState(t *testing.T) {
	s := config.DefaultSettings()
	a := &App{settings: s}
	a.auth = authDialogState{
		ProviderID: "deepseek-openai",
		SetDefault: true,
		Provider: providerEditState{
			APIKey:  "test-key",
			BaseURL: "https://api.deepseek.com",
			API:     "openai-chat",
			Vendor:  "deepseek",
		},
		Models: map[string]*modelEditState{
			"deepseek-v4-flash": {
				ID:              "deepseek-v4-flash",
				Name:            "DeepSeek V4 Flash",
				ContextWindow:   1000000,
				MaxTokens:       384000,
				MaxTokensEdited: true,
				Reasoning:       true,
				Input:           []string{"text"},
				CostEnabled:     true,
				CostInput:       0.14,
				CostOutput:      0.28,
			},
		},
		ModelOrder: []string{"deepseek-v4-flash"},
	}

	next, modelID := a.buildAuthSettings()
	if modelID != "deepseek-v4-flash" {
		t.Fatalf("modelID = %q", modelID)
	}
	pc := next.GetProviderConfig("deepseek-openai")
	if pc == nil {
		t.Fatal("provider config missing")
	}
	if pc.APIKey != "test-key" {
		t.Fatalf("APIKey = %q", pc.APIKey)
	}
	if pc.Vendor != "deepseek" {
		t.Fatalf("Vendor = %q", pc.Vendor)
	}
	if len(pc.Models) != 1 {
		t.Fatalf("Models = %d", len(pc.Models))
	}
	m := pc.Models[0]
	if m.ContextWindow != 1000000 || m.MaxTokens != 384000 {
		t.Fatalf("ctx/max = %d/%d", m.ContextWindow, m.MaxTokens)
	}
	if m.Cost == nil || m.Cost.Input != 0.14 {
		t.Fatalf("Cost = %#v", m.Cost)
	}
	if next.DefaultProvider != "deepseek-openai" || next.DefaultModel != "deepseek-v4-flash" {
		t.Fatalf("defaults = %s/%s", next.DefaultProvider, next.DefaultModel)
	}
}

func TestRenderAuthPreviewTruncates(t *testing.T) {
	var preview string
	for i := 0; i < authMaxPreviewVisibleLines+5; i++ {
		preview += "line\n"
	}
	lines := renderAuthPreview(preview)
	if len(lines) != authMaxPreviewVisibleLines+1 {
		t.Fatalf("rendered %d lines, want %d", len(lines), authMaxPreviewVisibleLines+1)
	}
	if lines[len(lines)-1] == "line" {
		t.Fatalf("expected truncation marker, got %q", lines[len(lines)-1])
	}
}

func TestCycleTriState(t *testing.T) {
	// nil → true
	p := cycleTriState(nil)
	if p == nil || !*p {
		t.Fatal("nil → true")
	}
	// true → false
	p = cycleTriState(p)
	if p == nil || *p {
		t.Fatal("true → false")
	}
	// false → nil
	p = cycleTriState(p)
	if p != nil {
		t.Fatal("false → nil")
	}
}

func TestToggleModelTriState(t *testing.T) {
	a := &App{
		auth: authDialogState{
			CurrentModelID: "m1",
			Models: map[string]*modelEditState{
				"m1": {ID: "m1", Compat: compatEditState{}},
			},
			ParamFieldKey: "tristate",
		},
	}

	// supportsDeveloperRole: nil → true
	a.auth.ParamField = "supportsDeveloperRole"
	a.toggleModelTriState("supportsDeveloperRole")
	me := a.auth.Models["m1"]
	if me.Compat.SupportsDeveloperRole == nil || !*me.Compat.SupportsDeveloperRole {
		t.Fatal("nil → true")
	}
	if !me.Compat.Active {
		t.Fatal("Active should be set")
	}

	// true → false
	a.toggleModelTriState("supportsDeveloperRole")
	if me.Compat.SupportsDeveloperRole == nil || *me.Compat.SupportsDeveloperRole {
		t.Fatal("true → false")
	}

	// false → nil
	a.toggleModelTriState("supportsDeveloperRole")
	if me.Compat.SupportsDeveloperRole != nil {
		t.Fatal("false → nil")
	}

	// Unknown field should be no-op
	a.toggleModelTriState("unknownField")
}

func TestModelCompatTriStateDoesNotEnterInputMode(t *testing.T) {
	a := &App{
		auth: authDialogState{
			Open:           true,
			View:           authViewModelCompat,
			CurrentModelID: "m1",
			Models: map[string]*modelEditState{
				"m1": {ID: "m1", Compat: compatEditState{}},
			},
		},
	}

	a.selectModelFieldValue("supportsDeveloperRole")
	me := a.auth.Models["m1"]
	if me.Compat.SupportsDeveloperRole == nil || !*me.Compat.SupportsDeveloperRole {
		t.Fatalf("supportsDeveloperRole = %#v, want enabled", me.Compat.SupportsDeveloperRole)
	}
	if a.auth.ParamField != "" || a.auth.ParamFieldKey != "" || a.authInputActive() {
		t.Fatalf("compat tristate left input active: field=%q key=%q", a.auth.ParamField, a.auth.ParamFieldKey)
	}
}

func TestCostEnabledToggle(t *testing.T) {
	me := &modelEditState{
		ID:          "m1",
		CostEnabled: false,
		CostInput:   0.5,
		CostOutput:  1.0,
	}

	// Cost disabled → cost should be nil
	mc := me.toConfig()
	if mc.Cost != nil {
		t.Fatal("cost should be nil when disabled")
	}

	// Cost enabled → cost should be set
	me.CostEnabled = true
	mc = me.toConfig()
	if mc.Cost == nil || mc.Cost.Input != 0.5 || mc.Cost.Output != 1.0 {
		t.Fatalf("cost = %#v, want input=0.5 output=1.0", mc.Cost)
	}
}

func TestHeadersEditFlow(t *testing.T) {
	a := &App{
		auth: authDialogState{
			Provider: providerEditState{},
		},
	}

	pe := &a.auth.Provider
	if pe.Headers != nil {
		t.Fatal("headers should start nil")
	}
	pe.Headers = map[string]string{"X-Custom": "value1"}
	if pe.Headers["X-Custom"] != "value1" {
		t.Fatal("header not added")
	}

	// Edit header
	pe.Headers["X-Custom"] = "value2"
	if pe.Headers["X-Custom"] != "value2" {
		t.Fatal("header not edited")
	}

	// Delete header
	delete(pe.Headers, "X-Custom")
	if len(pe.Headers) != 0 {
		t.Fatal("header not deleted")
	}
	pe.Headers = nil
	mc := pe.toConfig()
	if mc.Headers != nil {
		t.Fatal("headers should be nil after clearing")
	}
}

func TestCompatEditStateActiveCount(t *testing.T) {
	ce := compatEditState{Active: false}
	if ce.activeCount() != 0 {
		t.Fatal("inactive compat should have 0 count")
	}

	ce.Active = true
	ce.ThinkingFormat = "deepseek"
	ce.RequiresReasoningContentOnAssistant = true
	b := true
	ce.SupportsDeveloperRole = &b
	ce.MaxTokensField = "max_completion_tokens"
	if ce.activeCount() != 4 {
		t.Fatalf("activeCount = %d, want 4", ce.activeCount())
	}
}

func TestCompatResetToAuto(t *testing.T) {
	ce := compatEditState{
		Active:                              true,
		ThinkingFormat:                      "deepseek",
		RequiresReasoningContentOnAssistant: true,
		SupportsDeveloperRole:               config.BoolPtr(true),
	}
	ce = compatEditState{}
	if ce.Active {
		t.Fatal("Active should be false after reset")
	}
	if ce.ThinkingFormat != "" {
		t.Fatal("ThinkingFormat should be empty after reset")
	}
	if ce.SupportsDeveloperRole != nil {
		t.Fatal("SupportsDeveloperRole should be nil after reset")
	}
}

func TestPreviewBuildFoldedJSONMultipleModels(t *testing.T) {
	temp := 0.7
	s := &config.Settings{
		DefaultProvider: "test",
		DefaultModel:    "m1",
		Providers: map[string]*config.ProviderConfig{
			"test": {
				API: "openai-chat",
				Models: []config.ModelConfig{
					{ID: "m1", Name: "Model 1", Cost: &config.CostConfig{Input: 0.5}, Compat: &config.ModelCompat{ThinkingFormat: "deepseek"}},
					{ID: "m2", Name: "Model 2", Cost: &config.CostConfig{Input: 1.0}, Temperature: &temp},
				},
			},
		},
	}

	result := previewBuildFoldedJSON(s, "test", false)
	costCount := strings.Count(result, previewFoldMarker+"cost")
	if costCount != 2 {
		t.Fatalf("expected 2 cost fold markers, got %d", costCount)
	}
	compatCount := strings.Count(result, previewFoldMarker+"compat")
	if compatCount != 1 {
		t.Fatalf("expected 1 compat fold marker, got %d", compatCount)
	}

	// Collapsed rendering — may be truncated, so check raw folded output
	exp := previewExpansion{}
	collapsed := renderFoldedPreview(result, exp)
	// At least one ▶ cost should be visible (the other may be truncated)
	if !strings.Contains(collapsed, "▶ cost") {
		t.Fatal("collapsed should contain ▶ cost")
	}
	if !strings.Contains(collapsed, "▶ compat") {
		t.Fatal("collapsed should contain ▶ compat")
	}

	// Expand cost — all cost values should be visible in the raw JSON
	exp.CostExpand = true
	expanded := renderFoldedPreview(result, exp)
	if strings.Contains(expanded, "▶ cost") {
		t.Fatal("cost should be expanded")
	}
}

func TestPreviewFoldMaskedKey(t *testing.T) {
	s := &config.Settings{
		DefaultProvider: "test",
		Providers: map[string]*config.ProviderConfig{
			"test": {
				APIKey: "secret-key-12345",
				API:    "openai-chat",
				Models: []config.ModelConfig{
					{ID: "m1", Cost: &config.CostConfig{Input: 0.5}},
				},
			},
		},
	}

	result := previewBuildFoldedJSON(s, "test", true)
	if strings.Contains(result, "secret-key-12345") {
		t.Fatal("API key should be masked")
	}
	if !strings.Contains(result, "****") {
		t.Fatal("masked key should contain ****")
	}
}

func TestAuthSettingsOptionsLocalized(t *testing.T) {
	settings := config.DefaultSettings()
	settings.SessionDir = ""
	settings.Sandbox.AllowedRead = []string{"/src", "/docs"}

	tests := []struct {
		name           string
		language       i18n.Language
		wantRootTitle  string
		wantFieldTitle string
		wantEmpty      string
		wantEntries    string
		wantPrompt     string
		wantInvalidInt string
	}{
		{name: "english", language: i18n.LanguageEN, wantRootTitle: "Providers", wantFieldTitle: "Enable Plan Tool", wantEmpty: "(unset)", wantEntries: "2 entries", wantPrompt: "Enter max context tokens (0 = unset):", wantInvalidInt: "Invalid non-negative integer"},
		{name: "chinese", language: i18n.LanguageZH, wantRootTitle: "Provider", wantFieldTitle: "启用计划工具", wantEmpty: "（未设置）", wantEntries: "2 项", wantPrompt: "输入最大上下文 Token（0 = 未设置）：", wantInvalidInt: "无效的非负整数"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &App{settings: settings, translator: i18n.New(tt.language)}
			root := a.authSettingsRootOptions()
			if got := root[0].Title; got != tt.wantRootTitle {
				t.Fatalf("root title = %q, want %q", got, tt.wantRootTitle)
			}
			behavior := a.authSettingsTopLevelOptions(authViewSettingsBehavior)
			if got := behavior[1].Title; got != tt.wantFieldTitle {
				t.Fatalf("field title = %q, want %q", got, tt.wantFieldTitle)
			}
			paths := a.authSettingsTopLevelOptions(authViewSettingsPaths)
			if got := paths[0].Description; got != tt.wantEmpty {
				t.Fatalf("empty summary = %q, want %q", got, tt.wantEmpty)
			}
			sandbox := a.authSettingsTopLevelOptions(authViewSettingsSandbox)
			if got := sandbox[3].Description; got != tt.wantEntries {
				t.Fatalf("list summary = %q, want %q", got, tt.wantEntries)
			}
			a.auth.ParamField = "maxContextTokens"
			if got := a.authSettingsInputPrompt(); got != tt.wantPrompt {
				t.Fatalf("prompt = %q, want %q", got, tt.wantPrompt)
			}
			if _, err := a.parseNonNegativeInt("-1"); err == nil || err.Error() != tt.wantInvalidInt {
				t.Fatalf("invalid integer error = %v, want %q", err, tt.wantInvalidInt)
			}
		})
	}
}

func TestToolExecutionSettingsDefaultsAndLabels(t *testing.T) {
	settings := config.DefaultSettings()
	defaults := effectiveToolExecutionSettings(settings)
	if defaults.Mode != "parallel" || defaults.MaxConcurrency != 10 {
		t.Fatalf("tool execution defaults = %#v, want parallel/10", defaults)
	}

	a := &App{settings: settings, translator: i18n.New(i18n.LanguageEN)}
	opts := a.authSettingsTopLevelOptions(authViewSettingsBehavior)
	var modeTitle, modeDescription, concurrencyTitle string
	for _, option := range opts {
		switch option.Value {
		case "toolExecution.mode":
			modeTitle = option.Title
			modeDescription = option.Description
		case "toolExecution.maxConcurrency":
			concurrencyTitle = option.Title
		}
	}
	if modeTitle != "Tool Execution Mode" || concurrencyTitle != "Max Concurrent Tools" {
		t.Fatalf("tool execution labels = %q/%q", modeTitle, concurrencyTitle)
	}
	if modeDescription != "parallel (local only; provider may batch)" {
		t.Fatalf("tool execution mode description = %q", modeDescription)
	}
	a.auth.ParamField = "toolExecution.maxConcurrency"
	if got := a.authSettingsInputPrompt(); got != "Enter maximum concurrent tools:" {
		t.Fatalf("tool execution prompt = %q", got)
	}
}

func TestDefaultThinkingLevelCycle(t *testing.T) {
	got := cycleString("medium", []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}, "medium")
	if got != "high" {
		t.Fatalf("default thinking level cycle = %q, want high", got)
	}
}

func TestAuthResponsesToolControlInput(t *testing.T) {
	a := &App{
		translator: i18n.New(i18n.LanguageEN),
		auth: authDialogState{
			Open: true,
			View: authViewResponsesEdit,
			Provider: providerEditState{Responses: responsesEditState{
				ToolControl: config.ResponsesToolControlConfig{
					Choice:   "required",
					Parallel: config.BoolPtr(false),
					MaxCalls: 5,
				},
			}},
		},
	}

	seen := map[string]bool{}
	for _, opt := range a.authResponsesOptions() {
		seen[opt.Value] = true
	}
	for _, want := range []string{"toolChoice", "toolParallel", "toolMaxCalls"} {
		if !seen[want] {
			t.Fatalf("responses form is missing field %q", want)
		}
	}

	a.auth.ParamField = "toolChoice"
	if got := a.authProviderInputValue(); got != "required" {
		t.Fatalf("tool choice input seed = %q, want required", got)
	}
	a.authInput = editor.New(80).SetValue("none")
	if err := a.authProviderSubmitInput(); err != nil {
		t.Fatalf("submit tool choice: %v", err)
	}
	if a.auth.Provider.Responses.ToolControl.Choice != "none" {
		t.Fatalf("tool choice = %q, want none", a.auth.Provider.Responses.ToolControl.Choice)
	}
	if a.auth.Provider.Responses.ToolControl.MaxCalls != 5 {
		t.Fatalf("unrelated tool control field changed: %d", a.auth.Provider.Responses.ToolControl.MaxCalls)
	}

	// A forced tool name must not contain whitespace or quotes.
	a.auth.ParamField = "toolChoice"
	a.authInput = editor.New(80).SetValue("bash tool")
	if err := a.authProviderSubmitInput(); err == nil {
		t.Fatal("expected invalid tool choice error")
	}

	a.auth.ParamField = "toolMaxCalls"
	if got := a.authProviderInputValue(); got != "5" {
		t.Fatalf("max calls input seed = %q, want 5", got)
	}
	a.authInput = editor.New(80).SetValue("12")
	if err := a.authProviderSubmitInput(); err != nil {
		t.Fatalf("submit max calls: %v", err)
	}
	if a.auth.Provider.Responses.ToolControl.MaxCalls != 12 {
		t.Fatalf("max calls = %d, want 12", a.auth.Provider.Responses.ToolControl.MaxCalls)
	}

	a.auth.ParamField = "toolMaxCalls"
	a.authInput = editor.New(80).SetValue("-1")
	if err := a.authProviderSubmitInput(); err == nil {
		t.Fatal("expected invalid max calls error")
	}

	if cfg := a.auth.Provider.Responses.toConfig(); cfg.ToolControl.Choice != "none" || cfg.ToolControl.MaxCalls != 12 {
		t.Fatalf("tool control lost on write-back: %#v", cfg.ToolControl)
	}
}

func TestAuthResponsesToolParallelTriStateDoesNotEnterInputMode(t *testing.T) {
	a := &App{
		translator: i18n.New(i18n.LanguageEN),
		auth:       authDialogState{Open: true, View: authViewResponsesEdit},
	}

	a.selectProviderFieldValue("toolParallel")
	p := a.auth.Provider.Responses.ToolControl.Parallel
	if p == nil || !*p {
		t.Fatalf("toolParallel = %#v, want enabled", p)
	}
	if a.auth.ParamField != "" || a.authInputActive() {
		t.Fatalf("tri-state toggle left input active: field=%q", a.auth.ParamField)
	}

	a.selectProviderFieldValue("toolParallel")
	if p := a.auth.Provider.Responses.ToolControl.Parallel; p == nil || *p {
		t.Fatalf("toolParallel = %#v, want disabled", p)
	}
	a.selectProviderFieldValue("toolParallel")
	if a.auth.Provider.Responses.ToolControl.Parallel != nil {
		t.Fatal("toolParallel should return to auto")
	}
}

func TestModelCompatToolChoiceRoundTripAndToggle(t *testing.T) {
	mc := &config.ModelConfig{ID: "m1", Compat: &config.ModelCompat{
		SupportsToolChoice:        config.BoolPtr(false),
		SupportsParallelToolCalls: config.BoolPtr(true),
	}}
	me := modelEditStateFromMC(mc)
	if me.Compat.SupportsToolChoice == nil || *me.Compat.SupportsToolChoice {
		t.Fatalf("supportsToolChoice = %#v, want false", me.Compat.SupportsToolChoice)
	}
	if me.Compat.SupportsParallelToolCalls == nil || !*me.Compat.SupportsParallelToolCalls {
		t.Fatalf("supportsParallelToolCalls = %#v, want true", me.Compat.SupportsParallelToolCalls)
	}
	out := me.toConfig()
	if out.Compat == nil {
		t.Fatal("compat lost on write-back")
	}
	if out.Compat.SupportsToolChoice == nil || *out.Compat.SupportsToolChoice {
		t.Fatalf("written supportsToolChoice = %#v, want false", out.Compat.SupportsToolChoice)
	}
	if *out.Compat.SupportsParallelToolCalls != *me.Compat.SupportsParallelToolCalls {
		t.Fatal("written supportsParallelToolCalls changed")
	}

	a := &App{
		translator: i18n.New(i18n.LanguageEN),
		auth: authDialogState{
			Open:           true,
			View:           authViewModelCompat,
			CurrentModelID: "m1",
			Models:         map[string]*modelEditState{"m1": me},
		},
	}
	seen := map[string]bool{}
	for _, opt := range a.authModelCompatOptions() {
		seen[opt.Value] = true
	}
	for _, want := range []string{"supportsToolChoice", "supportsParallelToolCalls"} {
		if !seen[want] {
			t.Fatalf("compat form is missing field %q", want)
		}
	}

	// Cycle is auto → enabled → disabled → auto, so a stored false first
	// returns to auto before it becomes enabled again.
	a.selectModelFieldValue("supportsToolChoice")
	if me.Compat.SupportsToolChoice != nil {
		t.Fatalf("supportsToolChoice = %#v, want auto after toggling disabled", me.Compat.SupportsToolChoice)
	}
	a.selectModelFieldValue("supportsToolChoice")
	if p := me.Compat.SupportsToolChoice; p == nil || !*p {
		t.Fatalf("supportsToolChoice = %#v, want enabled after toggle", p)
	}
	if a.auth.ParamField != "" || a.authInputActive() {
		t.Fatalf("compat toggle left input active: field=%q", a.auth.ParamField)
	}
}

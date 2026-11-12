package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

// This file owns the Native projection of the ACP provider management
// contract (mothx/manage/providers/list|save|delete|discover|test and the
// settings default-provider patch). Wire types mirror internal/acp/manage.go
// exactly. API keys are never decoded or projected: the response only carries
// a maskedKey/apiKeyConfigured hint, and the editable key lives in a transient
// draft that is cleared on selection changes, saves, and page exits.

const (
	providerScopeConfigured = "configured"
	providerScopeAll        = "all"

	providerDefaultThinking = "medium"
)

const (
	providerEditorConnection = iota
	providerEditorModels
	providerEditorAdvanced
)

var providerAPIChoices = []string{
	"openai-chat",
	"openai-responses",
	"anthropic-messages",
	"google-gemini",
	"google-vertex",
}

var providerThinkingLevels = []string{
	"off",
	"minimal",
	"low",
	"medium",
	"high",
	"xhigh",
	"max",
}

// providerModel is one editable model row in a provider catalog response.
type providerModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	Reasoning     bool     `json:"reasoning,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Input         []string `json:"input,omitempty"`
}

// providerCatalogModel is the shared model catalog entry: a providerModel plus
// the provider it belongs to.
type providerCatalogModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	Reasoning     bool     `json:"reasoning,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Input         []string `json:"input,omitempty"`
}

// providerConfigShape mirrors config.ProviderConfig as projected by ACP. The
// secret-bearing fields (apiKey, headers, responses) are intentionally absent.
type providerConfigShape struct {
	Vendor              string          `json:"vendor,omitempty"`
	BaseURL             string          `json:"baseUrl,omitempty"`
	HTTPProxy           string          `json:"httpProxy,omitempty"`
	ForceHTTP11         bool            `json:"forceHTTP11,omitempty"`
	API                 string          `json:"api,omitempty"`
	ThinkingFormat      string          `json:"thinkingFormat,omitempty"`
	CacheControl        *bool           `json:"cacheControl,omitempty"`
	MaxImagesPerRequest *int            `json:"maxImagesPerRequest,omitempty"`
	Models              []providerModel `json:"models,omitempty"`
}

// providerConfigView is one editable provider from providerConfigs.
type providerConfigView struct {
	ID               string              `json:"id"`
	Provider         providerConfigShape `json:"provider"`
	MaskedKey        *string             `json:"maskedKey"`
	APIKeyConfigured bool                `json:"apiKeyConfigured"`
	IsDefault        bool                `json:"isDefault,omitempty"`
	GlobalOverride   bool                `json:"globalOverride,omitempty"`
}

// providerSummary is the compact provider list shared by settings/get and
// providers/list.
type providerSummary struct {
	Name             string  `json:"name"`
	MaskedKey        *string `json:"maskedKey"`
	BaseURL          string  `json:"baseUrl,omitempty"`
	ModelCount       int     `json:"modelCount"`
	APIKeyConfigured bool    `json:"apiKeyConfigured"`
	IsDefault        bool    `json:"isDefault,omitempty"`
	UsageCount       int     `json:"usageCount,omitempty"`
	LastUsedAt       string  `json:"lastUsedAt,omitempty"`
}

// providerModelDefaults is the catalog's suggested default row for a model
// added to a provider.
type providerModelDefaults struct {
	Reasoning     bool     `json:"reasoning"`
	ContextWindow int      `json:"contextWindow"`
	MaxTokens     int      `json:"maxTokens"`
	Input         []string `json:"input"`
}

// providerCatalogView mirrors mothx/manage/providers/list.
type providerCatalogView struct {
	Providers       []providerSummary      `json:"providers"`
	ProviderConfigs []providerConfigView   `json:"providerConfigs"`
	Models          []providerCatalogModel `json:"models"`
	ModelDefaults   providerModelDefaults  `json:"modelDefaults"`
	DefaultProvider string                 `json:"defaultProvider"`
	DefaultModel    string                 `json:"defaultModel"`
}

// providerSettingsView is the safe subset of mothx/manage/settings/get used by
// the providers panel. Only defaults plus the compact provider list are needed.
type providerSettingsView struct {
	DefaultProvider string            `json:"defaultProvider"`
	DefaultModel    string            `json:"defaultModel"`
	ThinkingLevel   string            `json:"thinkingLevel"`
	Providers       []providerSummary `json:"providers"`
}

type providerTestResultView struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latencyMs,omitempty"`
	Error     string `json:"error,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
}

type providerDiscoverResultView struct {
	Models []providerModel `json:"models"`
}

// providerModelDraft keeps numeric fields as strings so the native editor can
// distinguish empty from zero and reject invalid input before sending.
type providerModelDraft struct {
	ID            string
	Name          string
	Reasoning     bool
	ContextWindow string
	MaxTokens     string
	Input         string
}

// providerDraft is the editable, non-secret provider state.
type providerDraft struct {
	ID                  string
	Vendor              string
	BaseURL             string
	HTTPProxy           string
	API                 string
	ThinkingFormat      string
	ForceHTTP11         bool
	CacheControl        bool
	MaxImagesPerRequest string
	Models              []providerModelDraft

	MaskedKey        string
	APIKeyConfigured bool
	IsDefault        bool
	GlobalOverride   bool
}

type providerSecretDraft struct {
	APIKey   string
	ClearKey bool
}

type providerPanelState struct {
	Settings    *providerSettingsView
	Catalog     *providerCatalogView
	Loaded      bool
	Loading     bool
	Err         string
	SettingsErr string

	DefaultsProvider string
	DefaultsModel    string
	DefaultsThinking string
	DefaultsSaving   bool

	ActiveID      string
	OriginalID    string
	Draft         *providerDraft
	Secret        providerSecretDraft
	EditorTab     int
	Scope         string
	Search        string
	Saving        bool
	Deleting      bool
	Testing       bool
	Status        string
	StatusIsError bool

	PendingDelete bool

	Discovering        bool
	DiscoverOpen       bool
	DiscoverSearch     string
	DiscoverCandidates []providerModel
	DiscoverSelected   map[string]*bool

	// Seq guards every asynchronous provider call. Bumping it invalidates any
	// in-flight load/save/delete/test/discover response, which is how selection
	// changes, page exits, and reconnects prevent stale state from landing.
	Seq int64
}

type providerCapabilitySet struct {
	Catalog  bool
	Settings bool
	Config   bool
}

func newProviderPanelState() providerPanelState {
	return providerPanelState{
		Scope:            providerScopeConfigured,
		DefaultsThinking: providerDefaultThinking,
	}
}

func providerModelDefaultsFor(catalog *providerCatalogView) providerModelDraft {
	reasoning := true
	contextWindow := 256000
	maxTokens := ""
	input := "text"
	if catalog != nil {
		d := catalog.ModelDefaults
		reasoning = d.Reasoning
		if d.ContextWindow > 0 {
			contextWindow = d.ContextWindow
		}
		if d.MaxTokens > 0 {
			maxTokens = strconv.Itoa(d.MaxTokens)
		}
		if len(d.Input) > 0 {
			input = strings.Join(d.Input, ", ")
		}
	}
	return providerModelDraft{
		Reasoning:     reasoning,
		ContextWindow: strconv.Itoa(contextWindow),
		MaxTokens:     maxTokens,
		Input:         input,
	}
}

func providerDraftFromConfig(view providerConfigView) providerDraft {
	d := providerDraft{
		ID:               view.ID,
		Vendor:           view.Provider.Vendor,
		BaseURL:          view.Provider.BaseURL,
		HTTPProxy:        view.Provider.HTTPProxy,
		API:              view.Provider.API,
		ThinkingFormat:   view.Provider.ThinkingFormat,
		ForceHTTP11:      view.Provider.ForceHTTP11,
		MaskedKey:        "",
		APIKeyConfigured: view.APIKeyConfigured,
		IsDefault:        view.IsDefault,
		GlobalOverride:   view.GlobalOverride,
	}
	if view.MaskedKey != nil {
		d.MaskedKey = *view.MaskedKey
	}
	if view.Provider.CacheControl != nil {
		d.CacheControl = *view.Provider.CacheControl
	}
	if view.Provider.MaxImagesPerRequest != nil {
		d.MaxImagesPerRequest = strconv.Itoa(*view.Provider.MaxImagesPerRequest)
	}
	d.Models = make([]providerModelDraft, 0, len(view.Provider.Models))
	for _, model := range view.Provider.Models {
		d.Models = append(d.Models, providerModelDraft{
			ID:            model.ID,
			Name:          model.Name,
			Reasoning:     model.Reasoning,
			ContextWindow: optionalIntString(model.ContextWindow),
			MaxTokens:     optionalIntString(model.MaxTokens),
			Input:         strings.Join(model.Input, ", "),
		})
	}
	return d
}

func providerConfigByID(catalog *providerCatalogView, id string) *providerConfigView {
	if catalog == nil {
		return nil
	}
	for i := range catalog.ProviderConfigs {
		if catalog.ProviderConfigs[i].ID == id {
			return &catalog.ProviderConfigs[i]
		}
	}
	return nil
}

func providerModelsFor(catalog *providerCatalogView, provider string) []providerCatalogModel {
	if catalog == nil {
		return nil
	}
	out := make([]providerCatalogModel, 0)
	seen := make(map[string]bool)
	for _, model := range catalog.Models {
		id := strings.TrimSpace(model.ID)
		if model.Provider != provider || id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, model)
	}
	return out
}

func providerModelIDsFor(catalog *providerCatalogView, provider string) []string {
	models := providerModelsFor(catalog, provider)
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func providerDefaultModelFor(catalog *providerCatalogView, provider, preferred string) string {
	models := providerModelsFor(catalog, provider)
	if preferred != "" {
		for _, model := range models {
			if model.ID == preferred {
				return preferred
			}
		}
	}
	if len(models) > 0 {
		return models[0].ID
	}
	return ""
}

func providerUniqueID(catalog *providerCatalogView) string {
	used := make(map[string]bool)
	if catalog != nil {
		for _, config := range catalog.ProviderConfigs {
			used[config.ID] = true
		}
	}
	if !used["provider"] {
		return "provider"
	}
	for i := 2; ; i++ {
		id := fmt.Sprintf("provider-%d", i)
		if !used[id] {
			return id
		}
	}
}

func providerSelectOptions(current string, options []string) []string {
	out := make([]string, 0, len(options)+1)
	seen := false
	for _, option := range options {
		if option == current {
			seen = true
		}
		out = append(out, option)
	}
	if current != "" && !seen {
		out = append([]string{current}, out...)
	}
	return out
}

func providerStringList(input string) []string {
	parts := strings.Split(input, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func optionalIntString(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func parseProviderOptionalInt(value string) (int, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false, localizedError("必须是整数")
	}
	return parsed, true, nil
}

// validateProviderDraft enforces identity and numeric minimums client-side so
// a malformed editor never reaches ACP.
func validateProviderDraft(draft *providerDraft, catalog *providerCatalogView, originalID string) error {
	if draft == nil {
		return localizedError("供应商草稿不存在")
	}
	id := strings.TrimSpace(draft.ID)
	if id == "" {
		return localizedError("供应商 ID 不能为空")
	}
	if strings.Contains(id, "/") {
		return localizedError("供应商 ID 不能包含 /")
	}
	if catalog != nil {
		for _, config := range catalog.ProviderConfigs {
			if config.ID == id && config.ID != originalID {
				return localizedErrorf("供应商 ID %q 已存在", id)
			}
		}
	}
	if originalID != "" && originalID != id {
		original := providerConfigByID(catalog, originalID)
		if original == nil || !original.GlobalOverride {
			return localizedError("该供应商不支持重命名，请先保存其全局覆盖配置")
		}
	}
	if value, present, err := parseProviderOptionalInt(draft.MaxImagesPerRequest); err != nil {
		return localizedError("每次请求最大图片数必须是整数")
	} else if present && value < -1 {
		return localizedError("每次请求最大图片数不能小于 -1")
	}
	seen := make(map[string]bool)
	for _, model := range draft.Models {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			return localizedError("模型 ID 不能为空")
		}
		if seen[modelID] {
			return localizedErrorf("模型 ID %q 重复", modelID)
		}
		seen[modelID] = true
		if value, present, err := parseProviderOptionalInt(model.ContextWindow); err != nil {
			return localizedErrorf("模型 %s 的上下文窗口必须是整数", modelID)
		} else if present && value < 0 {
			return localizedErrorf("模型 %s 的上下文窗口不能为负数", modelID)
		}
		if value, present, err := parseProviderOptionalInt(model.MaxTokens); err != nil {
			return localizedErrorf("模型 %s 的最大 Tokens 必须是整数", modelID)
		} else if present && value < 0 {
			return localizedErrorf("模型 %s 的最大 Tokens 不能为负数", modelID)
		}
	}
	return nil
}

func providerModelsPayload(draft *providerDraft) ([]map[string]any, error) {
	models := make([]map[string]any, 0, len(draft.Models))
	for _, model := range draft.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			return nil, localizedError("模型 ID 不能为空")
		}
		entry := map[string]any{"id": id, "reasoning": model.Reasoning}
		if name := strings.TrimSpace(model.Name); name != "" {
			entry["name"] = name
		}
		if value, present, err := parseProviderOptionalInt(model.ContextWindow); err != nil {
			return nil, localizedErrorf("模型 %s 的上下文窗口必须是整数", id)
		} else if present {
			entry["contextWindow"] = value
		}
		if value, present, err := parseProviderOptionalInt(model.MaxTokens); err != nil {
			return nil, localizedErrorf("模型 %s 的最大 Tokens 必须是整数", id)
		} else if present {
			entry["maxTokens"] = value
		}
		if input := providerStringList(model.Input); len(input) > 0 {
			entry["input"] = input
		}
		models = append(models, entry)
	}
	return models, nil
}

// buildProviderSaveRequest returns the exact ACP params for providers/save.
// The apiKey field is present only when the user typed a new key or explicitly
// asked to clear it; an ordinary save never round-trips a secret.
func buildProviderSaveRequest(draft *providerDraft, secret providerSecretDraft, previousID string, catalog *providerCatalogView) (map[string]any, error) {
	if err := validateProviderDraft(draft, catalog, previousID); err != nil {
		return nil, err
	}
	models, err := providerModelsPayload(draft)
	if err != nil {
		return nil, err
	}
	provider := map[string]any{
		"vendor":         strings.TrimSpace(draft.Vendor),
		"api":            strings.TrimSpace(draft.API),
		"baseUrl":        strings.TrimSpace(draft.BaseURL),
		"httpProxy":      strings.TrimSpace(draft.HTTPProxy),
		"forceHTTP11":    draft.ForceHTTP11,
		"thinkingFormat": strings.TrimSpace(draft.ThinkingFormat),
		"cacheControl":   draft.CacheControl,
		"models":         models,
	}
	if value, _, err := parseProviderOptionalInt(draft.MaxImagesPerRequest); err != nil {
		return nil, localizedError("每次请求最大图片数必须是整数")
	} else {
		if value < -1 {
			return nil, localizedError("每次请求最大图片数不能小于 -1")
		}
		// Send zero explicitly when the field is blank: the server preserves an
		// omitted provider-level field, so omission would make clearing impossible.
		provider["maxImagesPerRequest"] = value
	}
	id := strings.TrimSpace(draft.ID)
	request := map[string]any{"id": id, "provider": provider}
	previousID = strings.TrimSpace(previousID)
	if previousID != "" && previousID != id {
		request["previousId"] = previousID
	}
	if key := strings.TrimSpace(secret.APIKey); key != "" {
		request["apiKey"] = key
	} else if secret.ClearKey {
		request["apiKey"] = ""
	}
	return request, nil
}

// buildProviderDefaultsPatch is the settings/patch shape for the global
// default provider/model/thinking selectors.
func buildProviderDefaultsPatch(provider, model, thinking string) (map[string]any, error) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	thinking = strings.TrimSpace(thinking)
	if provider == "" {
		return nil, localizedError("请选择默认供应商")
	}
	if model == "" {
		return nil, localizedError("请选择默认模型")
	}
	if thinking == "" {
		thinking = providerDefaultThinking
	}
	allowed := false
	for _, level := range providerThinkingLevels {
		if level == thinking {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, localizedErrorf("思考级别 %q 无效", thinking)
	}
	return map[string]any{
		"defaultProvider": provider,
		"defaultModel":    model,
		"thinkingLevel":   thinking,
	}, nil
}

func (a *nativeApp) providerCapabilities() providerCapabilitySet {
	catalog := a.featureEnabled("manageProviders")
	return providerCapabilitySet{
		Catalog:  catalog,
		Settings: a.featureEnabled("manageSettings"),
		Config:   catalog && a.featureEnabled("manageProviderConfig"),
	}
}

func (a *nativeApp) providerPanelCurrent(seq, reloadSeq int64) bool {
	return a.settingsOpen && a.settingsTab == "providers" &&
		seq == atomic.LoadInt64(&a.providers.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// invalidateProviderPanel drops transient state and makes every in-flight
// provider response stale. Loaded is reset so re-entering the tab reloads.
func (a *nativeApp) invalidateProviderPanel() {
	p := &a.providers
	atomic.AddInt64(&p.Seq, 1)
	p.Loaded = false
	p.Loading = false
	p.Saving = false
	p.Deleting = false
	p.Testing = false
	p.Discovering = false
	p.PendingDelete = false
	p.DiscoverOpen = false
	p.DiscoverCandidates = nil
	p.DiscoverSelected = nil
	p.Secret = providerSecretDraft{}
	p.Draft = nil
	p.ActiveID = ""
	p.OriginalID = ""
	p.Status = ""
	p.StatusIsError = false
}

// startProviderPanelLoad starts one capability-gated load. It is called from
// the UI thread on tab entry.
func (a *nativeApp) startProviderPanelLoad() {
	if !a.settingsOpen || a.settingsTab != "providers" {
		return
	}
	p := &a.providers
	if p.Loading {
		return
	}
	caps := a.providerCapabilities()
	if !caps.Catalog {
		p.Loaded = true
		p.Err = "当前运行时不支持供应商管理（manageProviders）。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	p.Loading = true
	p.Err = ""
	p.SettingsErr = ""
	p.Status = ""
	p.StatusIsError = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadProviderPanel(seq, startReloadSeq, caps)
}

func (a *nativeApp) loadProviderPanel(seq, startReloadSeq int64, caps providerCapabilitySet) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.providerPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.providers
			p.Loading = false
			p.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	var settings *providerSettingsView
	settingsErr := ""
	if caps.Settings {
		raw, err := client.call("mothx/manage/settings/get", map[string]any{})
		if err != nil {
			settingsErr = "读取默认供应商设置失败：" + err.Error()
		} else {
			var view providerSettingsView
			if err := json.Unmarshal(raw, &view); err != nil {
				settingsErr = "默认供应商设置响应无效：" + err.Error()
			} else {
				settings = &view
			}
		}
	}
	var catalog *providerCatalogView
	catalogErr := ""
	raw, err := client.call("mothx/manage/providers/list", map[string]any{})
	if err != nil {
		catalogErr = "读取供应商目录失败：" + err.Error()
	} else {
		var view providerCatalogView
		if err := json.Unmarshal(raw, &view); err != nil {
			catalogErr = "供应商目录响应无效：" + err.Error()
		} else {
			catalog = &view
		}
	}
	a.dispatch(func() {
		if !a.providerPanelCurrent(seq, startReloadSeq) {
			return
		}
		p := &a.providers
		p.Loading = false
		p.SettingsErr = settingsErr
		if settings != nil {
			p.Settings = settings
		}
		if catalog == nil {
			if p.Catalog == nil {
				p.Loaded = false
				p.Err = catalogErr
				return
			}
			p.Err = catalogErr
			return
		}
		p.Catalog = catalog
		p.Loaded = true
		p.Err = ""
		a.normalizeProviderDefaults()
		if p.Draft == nil {
			a.selectInitialProvider(false)
		}
	})
}

func (a *nativeApp) normalizeProviderDefaults() {
	p := &a.providers
	providers := a.providerConfigIDs()
	if len(providers) == 0 {
		p.DefaultsProvider = ""
		p.DefaultsModel = ""
		if p.DefaultsThinking == "" {
			p.DefaultsThinking = providerDefaultThinking
		}
		return
	}
	validProvider := false
	for _, id := range providers {
		if id == p.DefaultsProvider {
			validProvider = true
			break
		}
	}
	if !validProvider {
		preferred := ""
		if p.Settings != nil {
			preferred = p.Settings.DefaultProvider
		}
		if preferred == "" && p.Catalog != nil {
			preferred = p.Catalog.DefaultProvider
		}
		if preferred == "" {
			preferred = providers[0]
		}
		p.DefaultsProvider = preferred
	}
	preferredModel := ""
	if p.Settings != nil {
		preferredModel = p.Settings.DefaultModel
	}
	if preferredModel == "" && p.Catalog != nil {
		preferredModel = p.Catalog.DefaultModel
	}
	p.DefaultsModel = providerDefaultModelFor(p.Catalog, p.DefaultsProvider, p.DefaultsModel)
	if p.DefaultsModel == "" && preferredModel != "" {
		p.DefaultsModel = providerDefaultModelFor(p.Catalog, p.DefaultsProvider, preferredModel)
	}
	if p.DefaultsThinking == "" {
		if p.Settings != nil && p.Settings.ThinkingLevel != "" {
			p.DefaultsThinking = p.Settings.ThinkingLevel
		} else {
			p.DefaultsThinking = providerDefaultThinking
		}
	}
}

func (a *nativeApp) providerConfigIDs() []string {
	p := &a.providers
	if p.Catalog != nil {
		ids := make([]string, 0, len(p.Catalog.ProviderConfigs))
		for _, config := range p.Catalog.ProviderConfigs {
			if config.ID != "" {
				ids = append(ids, config.ID)
			}
		}
		return ids
	}
	if p.Settings != nil {
		ids := make([]string, 0, len(p.Settings.Providers))
		for _, provider := range p.Settings.Providers {
			if provider.Name != "" {
				ids = append(ids, provider.Name)
			}
		}
		return ids
	}
	return nil
}

func (a *nativeApp) selectInitialProvider(invalidate bool) {
	p := &a.providers
	candidates := a.providerConfigIDs()
	if len(candidates) == 0 {
		p.ActiveID = ""
		p.OriginalID = ""
		p.Draft = nil
		return
	}
	initial := ""
	if p.Settings != nil && p.Settings.DefaultProvider != "" {
		initial = p.Settings.DefaultProvider
	}
	if initial == "" && p.Catalog != nil {
		initial = p.Catalog.DefaultProvider
	}
	if initial == "" {
		for _, config := range p.Catalog.ProviderConfigs {
			if config.IsDefault || config.APIKeyConfigured {
				initial = config.ID
				break
			}
		}
	}
	if initial == "" {
		initial = candidates[0]
	}
	a.selectProviderDraft(initial, invalidate)
}

func (a *nativeApp) selectProviderDraft(id string, invalidate bool) {
	p := &a.providers
	config := providerConfigByID(p.Catalog, id)
	if config == nil {
		return
	}
	if invalidate {
		atomic.AddInt64(&p.Seq, 1)
		p.Loading = false
		p.Saving = false
		p.Deleting = false
		p.Testing = false
		p.Discovering = false
		p.PendingDelete = false
		p.DiscoverOpen = false
		p.DiscoverCandidates = nil
		p.DiscoverSelected = nil
	}
	draft := providerDraftFromConfig(*config)
	p.ActiveID = config.ID
	p.OriginalID = config.ID
	p.Draft = &draft
	p.Secret = providerSecretDraft{}
	p.EditorTab = providerEditorConnection
	p.Status = ""
	p.StatusIsError = false
}

func (a *nativeApp) startNewProviderDraft() {
	p := &a.providers
	if p.Catalog == nil {
		return
	}
	atomic.AddInt64(&p.Seq, 1)
	p.Loading = false
	p.Saving = false
	p.Deleting = false
	p.Testing = false
	p.Discovering = false
	p.PendingDelete = false
	p.DiscoverOpen = false
	p.DiscoverCandidates = nil
	p.DiscoverSelected = nil
	id := providerUniqueID(p.Catalog)
	p.ActiveID = id
	p.OriginalID = ""
	p.Draft = &providerDraft{ID: id, API: "openai-chat", Models: []providerModelDraft{}}
	p.Secret = providerSecretDraft{}
	p.EditorTab = providerEditorConnection
	p.Status = "正在编辑新供应商；保存后才会出现在目录中。"
	p.StatusIsError = false
}

func (a *nativeApp) saveProviderDraft() {
	p := &a.providers
	caps := a.providerCapabilities()
	if !caps.Config {
		p.Status = "当前运行时不支持编辑供应商配置（manageProviderConfig）。"
		p.StatusIsError = true
		return
	}
	if p.Draft == nil {
		return
	}
	request, err := buildProviderSaveRequest(p.Draft, p.Secret, p.OriginalID, p.Catalog)
	if err != nil {
		p.Status = err.Error()
		p.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		p.Status = "保存失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.Saving = true
	p.Status = "正在保存供应商…"
	p.StatusIsError = false
	savedID := strings.TrimSpace(p.Draft.ID)
	go func() {
		raw, err := client.call("mothx/manage/providers/save", request)
		a.dispatch(func() {
			if !a.providerPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.providers
			p.Saving = false
			if err != nil {
				p.Status = "保存供应商失败：" + providerScrubSecret(err.Error(), p.Secret.APIKey)
				p.StatusIsError = true
				return
			}
			var catalog providerCatalogView
			if err := json.Unmarshal(raw, &catalog); err != nil {
				p.Status = "保存后供应商目录响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.Catalog = &catalog
			p.Loaded = true
			p.Err = ""
			a.applyCatalogDefaults()
			if p.Settings != nil {
				p.Settings.DefaultProvider = catalog.DefaultProvider
				p.Settings.DefaultModel = catalog.DefaultModel
			}
			a.selectProviderDraft(savedID, false)
			p.Status = "供应商已保存"
			p.StatusIsError = false
			a.normalizeProviderDefaults()
		})
	}()
}

func (a *nativeApp) applyCatalogDefaults() {
	p := &a.providers
	if p.Catalog == nil {
		return
	}
	if p.Settings != nil {
		p.Settings.DefaultProvider = p.Catalog.DefaultProvider
		p.Settings.DefaultModel = p.Catalog.DefaultModel
	}
}

func (a *nativeApp) requestDeleteProvider() {
	p := &a.providers
	if p.Draft == nil {
		return
	}
	if p.Draft.IsDefault {
		p.Status = "默认供应商不能移除，请先切换默认供应商。"
		p.StatusIsError = true
		return
	}
	if !p.Draft.GlobalOverride {
		p.Status = "该供应商没有可移除的全局覆盖配置。"
		p.StatusIsError = true
		return
	}
	p.PendingDelete = true
}

func (a *nativeApp) cancelDeleteProvider() {
	a.providers.PendingDelete = false
}

func (a *nativeApp) confirmDeleteProvider() {
	p := &a.providers
	p.PendingDelete = false
	caps := a.providerCapabilities()
	if !caps.Config {
		p.Status = "当前运行时不支持移除供应商配置。"
		p.StatusIsError = true
		return
	}
	if p.Draft == nil {
		return
	}
	if p.Draft.IsDefault {
		p.Status = "默认供应商不能移除，请先切换默认供应商。"
		p.StatusIsError = true
		return
	}
	if !p.Draft.GlobalOverride {
		p.Status = "该供应商没有可移除的全局覆盖配置。"
		p.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		p.Status = "移除失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	id := strings.TrimSpace(p.Draft.ID)
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.Deleting = true
	p.Status = "正在移除供应商覆盖…"
	p.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/providers/delete", map[string]any{"id": id})
		a.dispatch(func() {
			if !a.providerPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.providers
			p.Deleting = false
			if err != nil {
				p.Status = "移除供应商失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var catalog providerCatalogView
			if err := json.Unmarshal(raw, &catalog); err != nil {
				p.Status = "移除后供应商目录响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.Catalog = &catalog
			p.Loaded = true
			p.Err = ""
			a.applyCatalogDefaults()
			p.ActiveID = ""
			p.OriginalID = ""
			p.Draft = nil
			p.Secret = providerSecretDraft{}
			a.normalizeProviderDefaults()
			a.selectInitialProvider(false)
			p.Status = "供应商配置已移除"
			p.StatusIsError = false
		})
	}()
}

func (a *nativeApp) testProviderDraft() {
	p := &a.providers
	caps := a.providerCapabilities()
	if !caps.Catalog {
		p.Status = "当前运行时不支持供应商连接测试。"
		p.StatusIsError = true
		return
	}
	if p.Draft == nil {
		return
	}
	id := strings.TrimSpace(p.Draft.ID)
	if providerConfigByID(p.Catalog, id) == nil {
		p.Status = "请先保存供应商后再测试连接。"
		p.StatusIsError = true
		return
	}
	modelID := ""
	for _, model := range p.Draft.Models {
		if modelID = strings.TrimSpace(model.ID); modelID != "" {
			break
		}
	}
	if modelID == "" {
		modelID = providerDefaultModelFor(p.Catalog, id, "")
	}
	client := a.acp
	if client == nil {
		p.Status = "连接测试失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	params := map[string]any{"provider": id}
	if modelID != "" {
		params["model"] = modelID
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.Testing = true
	p.Status = "正在测试连接…"
	p.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/providers/test", params)
		a.dispatch(func() {
			if !a.providerPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.providers
			p.Testing = false
			if err != nil {
				p.Status = "连接测试失败：" + providerScrubSecret(err.Error(), p.Secret.APIKey)
				p.StatusIsError = true
				return
			}
			var result providerTestResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				p.Status = "连接测试响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			if result.OK {
				p.Status = Tf("连接测试成功（%s ms）", strconv.FormatInt(result.LatencyMS, 10))
				p.StatusIsError = false
				return
			}
			message := result.Error
			if message == "" {
				message = "未知错误"
			}
			p.Status = "连接测试失败：" + providerScrubSecret(message, p.Secret.APIKey)
			p.StatusIsError = true
		})
	}()
}

func (a *nativeApp) saveProviderDefaults() {
	p := &a.providers
	caps := a.providerCapabilities()
	if !caps.Settings {
		p.Status = "当前运行时不支持保存默认供应商（manageSettings）。"
		p.StatusIsError = true
		return
	}
	patch, err := buildProviderDefaultsPatch(p.DefaultsProvider, p.DefaultsModel, p.DefaultsThinking)
	if err != nil {
		p.Status = err.Error()
		p.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		p.Status = "保存失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.DefaultsSaving = true
	p.Status = "正在保存默认供应商与模型…"
	p.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/settings/patch", map[string]any{"patch": patch})
		a.dispatch(func() {
			if !a.providerPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.providers
			p.DefaultsSaving = false
			if err != nil {
				p.Status = "保存默认值失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view providerSettingsView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "保存后默认设置响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.Settings = &view
			if p.Catalog != nil {
				p.Catalog.DefaultProvider = view.DefaultProvider
				p.Catalog.DefaultModel = view.DefaultModel
				for i := range p.Catalog.ProviderConfigs {
					p.Catalog.ProviderConfigs[i].IsDefault = p.Catalog.ProviderConfigs[i].ID == view.DefaultProvider
				}
				for i := range p.Catalog.Providers {
					p.Catalog.Providers[i].IsDefault = p.Catalog.Providers[i].Name == view.DefaultProvider
				}
			}
			if p.Draft != nil {
				p.Draft.IsDefault = strings.TrimSpace(p.Draft.ID) == view.DefaultProvider
			}
			p.Status = "默认供应商、模型与思考级别已保存"
			p.StatusIsError = false
		})
	}()
}

func (a *nativeApp) discoverProviderModels() {
	p := &a.providers
	caps := a.providerCapabilities()
	if !caps.Config {
		p.Status = "当前运行时不支持模型发现（manageProviderConfig）。"
		p.StatusIsError = true
		return
	}
	if p.Draft == nil {
		return
	}
	baseURL := strings.TrimSpace(p.Draft.BaseURL)
	if baseURL == "" {
		p.Status = "发现模型需要先填写 Base URL。"
		p.StatusIsError = true
		return
	}
	api := strings.TrimSpace(p.Draft.API)
	if api == "" {
		api = "openai-chat"
	}
	client := a.acp
	if client == nil {
		p.Status = "发现模型失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	params := map[string]any{
		"api":         api,
		"baseUrl":     baseURL,
		"apiKey":      strings.TrimSpace(p.Secret.APIKey),
		"httpProxy":   strings.TrimSpace(p.Draft.HTTPProxy),
		"forceHTTP11": p.Draft.ForceHTTP11,
	}
	existing := make(map[string]bool)
	for _, model := range p.Draft.Models {
		if id := strings.TrimSpace(model.ID); id != "" {
			existing[id] = true
		}
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.Discovering = true
	p.Status = "正在发现模型…"
	p.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/providers/discover", params)
		a.dispatch(func() {
			if !a.providerPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.providers
			p.Discovering = false
			if err != nil {
				p.Status = "发现模型失败：" + providerScrubSecret(err.Error(), p.Secret.APIKey)
				p.StatusIsError = true
				return
			}
			var result providerDiscoverResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				p.Status = "模型发现响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			candidates := make([]providerModel, 0)
			seen := make(map[string]bool)
			for _, model := range result.Models {
				id := strings.TrimSpace(model.ID)
				if id == "" || existing[id] || seen[id] {
					continue
				}
				seen[id] = true
				model.ID = id
				if strings.TrimSpace(model.Name) == "" {
					model.Name = id
				}
				if len(model.Input) == 0 {
					model.Input = []string{"text"}
				}
				candidates = append(candidates, model)
			}
			if len(candidates) == 0 {
				p.Status = "未发现新的模型"
				p.StatusIsError = false
				return
			}
			p.DiscoverCandidates = candidates
			p.DiscoverSelected = make(map[string]*bool, len(candidates))
			for _, model := range candidates {
				selected := false
				p.DiscoverSelected[model.ID] = &selected
			}
			p.DiscoverSearch = ""
			p.DiscoverOpen = true
			p.Status = ""
			p.StatusIsError = false
		})
	}()
}

func (a *nativeApp) closeDiscoverDialog() {
	p := &a.providers
	p.DiscoverOpen = false
	p.DiscoverCandidates = nil
	p.DiscoverSelected = nil
	p.DiscoverSearch = ""
}

func (a *nativeApp) addDiscoveredModels() {
	p := &a.providers
	if p.Draft == nil {
		a.closeDiscoverDialog()
		return
	}
	existing := make(map[string]bool)
	for _, model := range p.Draft.Models {
		if id := strings.TrimSpace(model.ID); id != "" {
			existing[id] = true
		}
	}
	added := 0
	for _, candidate := range p.DiscoverCandidates {
		checked := p.DiscoverSelected[candidate.ID]
		if checked == nil || !*checked || existing[candidate.ID] {
			continue
		}
		p.Draft.Models = append(p.Draft.Models, providerModelDraft{
			ID:            candidate.ID,
			Name:          candidate.Name,
			Reasoning:     candidate.Reasoning,
			ContextWindow: optionalIntString(candidate.ContextWindow),
			MaxTokens:     optionalIntString(candidate.MaxTokens),
			Input:         strings.Join(candidate.Input, ", "),
		})
		existing[candidate.ID] = true
		added++
	}
	p.Status = Tf("已添加 %d 个模型", added)
	p.StatusIsError = false
	a.closeDiscoverDialog()
}

func (a *nativeApp) providerFilteredConfigs() []providerConfigView {
	p := &a.providers
	if p.Catalog == nil {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(p.Search))
	out := make([]providerConfigView, 0, len(p.Catalog.ProviderConfigs)+1)
	for _, config := range p.Catalog.ProviderConfigs {
		if p.Scope == providerScopeConfigured && !config.APIKeyConfigured {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				config.ID, config.Provider.Vendor, config.Provider.BaseURL, providerMaskedKey(config.MaskedKey),
			}, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, config)
	}
	if p.Draft != nil && providerConfigByID(p.Catalog, p.Draft.ID) == nil {
		out = append(out, providerConfigView{ID: p.Draft.ID, APIKeyConfigured: false})
	}
	return out
}

func providerMaskedKey(masked *string) string {
	if masked == nil {
		return ""
	}
	return *masked
}

// providerScrubSecret defends the status line even if an upstream error text
// happens to echo the transient draft key. The key is never logged elsewhere.
func providerScrubSecret(message, secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) < 4 || message == "" {
		return message
	}
	return strings.ReplaceAll(message, secret, "***")
}

func providerSummaryList(p *providerPanelState, settingsAvailable bool) []providerSummary {
	if settingsAvailable && p.Settings != nil && len(p.Settings.Providers) > 0 {
		return p.Settings.Providers
	}
	if p.Catalog != nil {
		return p.Catalog.Providers
	}
	return nil
}

func (a *nativeApp) testProviderByName(name string) {
	p := &a.providers
	caps := a.providerCapabilities()
	if !caps.Catalog {
		p.Status = "当前运行时不支持供应商连接测试。"
		p.StatusIsError = true
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	client := a.acp
	if client == nil {
		p.Status = "连接测试失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	params := map[string]any{"provider": name}
	if modelID := providerDefaultModelFor(p.Catalog, name, ""); modelID != "" {
		params["model"] = modelID
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.Testing = true
	p.Status = Tf("正在测试 %s 的连接…", name)
	p.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/providers/test", params)
		a.dispatch(func() {
			if !a.providerPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.providers
			p.Testing = false
			if err != nil {
				p.Status = "连接测试失败：" + providerScrubSecret(err.Error(), p.Secret.APIKey)
				p.StatusIsError = true
				return
			}
			var result providerTestResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				p.Status = "连接测试响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			if result.OK {
				p.Status = Tf("连接测试成功（%s ms）", strconv.FormatInt(result.LatencyMS, 10))
				p.StatusIsError = false
				return
			}
			message := result.Error
			if message == "" {
				message = "未知错误"
			}
			p.Status = "连接测试失败：" + providerScrubSecret(message, p.Secret.APIKey)
			p.StatusIsError = true
		})
	}()
}

func (a *nativeApp) setDefaultProvider(name string) {
	p := &a.providers
	caps := a.providerCapabilities()
	if !caps.Settings {
		p.Status = "当前运行时不支持设置默认供应商（manageSettings）。"
		p.StatusIsError = true
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	client := a.acp
	if client == nil {
		p.Status = "设置默认供应商失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.Status = "正在设置默认供应商…"
	p.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/settings/patch", map[string]any{
			"patch": map[string]any{"defaultProvider": name},
		})
		a.dispatch(func() {
			if !a.providerPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.providers
			if err != nil {
				p.Status = "设置默认供应商失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view providerSettingsView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "默认供应商响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.Settings = &view
			if p.Catalog != nil {
				p.Catalog.DefaultProvider = view.DefaultProvider
				p.Catalog.DefaultModel = view.DefaultModel
				for i := range p.Catalog.ProviderConfigs {
					p.Catalog.ProviderConfigs[i].IsDefault = p.Catalog.ProviderConfigs[i].ID == view.DefaultProvider
				}
				for i := range p.Catalog.Providers {
					p.Catalog.Providers[i].IsDefault = p.Catalog.Providers[i].Name == view.DefaultProvider
				}
			}
			p.Status = "默认供应商已设置为 " + name
			p.StatusIsError = false
		})
	}()
}

func providerConfigSummary(config providerConfigView) providerSummary {
	return providerSummary{
		Name:             config.ID,
		MaskedKey:        config.MaskedKey,
		BaseURL:          config.Provider.BaseURL,
		ModelCount:       len(config.Provider.Models),
		APIKeyConfigured: config.APIKeyConfigured,
		IsDefault:        config.IsDefault,
	}
}

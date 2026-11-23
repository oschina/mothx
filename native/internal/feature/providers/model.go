// Package providers owns the Native projection of the ACP provider catalog and
// management contract (mothx/manage/providers/* and settings defaults).
package providers

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
)

// This file owns the Native projection of the ACP provider management
// contract (mothx/manage/providers/list|save|delete|discover|test and the
// settings default-provider patch). Wire types mirror internal/acp/manage.go
// exactly. API keys are never decoded or projected: the response only carries
// a maskedKey/apiKeyConfigured hint, and the editable key lives in a transient
// draft that is cleared on selection changes, saves, and page exits.

const (
	ScopeConfigured = "configured"
	ScopeAll        = "all"

	providerDefaultThinking = "medium"
)

const (
	providerEditorConnection = iota
	EditorModels
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

// Model is one editable model row in a provider catalog response.
type Model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	Reasoning     bool     `json:"reasoning,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Input         []string `json:"input,omitempty"`
}

// CatalogModel is the shared model catalog entry: a Model plus
// the provider it belongs to.
type CatalogModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	Reasoning     bool     `json:"reasoning,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Input         []string `json:"input,omitempty"`
}

// ConfigShape mirrors config.ProviderConfig as projected by ACP. The
// secret-bearing fields (apiKey, headers, responses) are intentionally absent.
type ConfigShape struct {
	Vendor              string  `json:"vendor,omitempty"`
	BaseURL             string  `json:"baseUrl,omitempty"`
	HTTPProxy           string  `json:"httpProxy,omitempty"`
	ForceHTTP11         bool    `json:"forceHTTP11,omitempty"`
	API                 string  `json:"api,omitempty"`
	ThinkingFormat      string  `json:"thinkingFormat,omitempty"`
	CacheControl        *bool   `json:"cacheControl,omitempty"`
	MaxImagesPerRequest *int    `json:"maxImagesPerRequest,omitempty"`
	Models              []Model `json:"models,omitempty"`
}

// ConfigView is one editable provider from providerConfigs.
type ConfigView struct {
	ID               string      `json:"id"`
	Provider         ConfigShape `json:"provider"`
	MaskedKey        *string     `json:"maskedKey"`
	APIKeyConfigured bool        `json:"apiKeyConfigured"`
	IsDefault        bool        `json:"isDefault,omitempty"`
	GlobalOverride   bool        `json:"globalOverride,omitempty"`
}

// Summary is the compact provider list shared by settings/get and
// providers/list.
type Summary struct {
	Name             string  `json:"name"`
	MaskedKey        *string `json:"maskedKey"`
	BaseURL          string  `json:"baseUrl,omitempty"`
	ModelCount       int     `json:"modelCount"`
	APIKeyConfigured bool    `json:"apiKeyConfigured"`
	IsDefault        bool    `json:"isDefault,omitempty"`
	UsageCount       int     `json:"usageCount,omitempty"`
	LastUsedAt       string  `json:"lastUsedAt,omitempty"`
}

// ModelDefaults is the catalog's suggested default row for a model
// added to a provider.
type ModelDefaults struct {
	Reasoning     bool     `json:"reasoning"`
	ContextWindow int      `json:"contextWindow"`
	MaxTokens     int      `json:"maxTokens"`
	Input         []string `json:"input"`
}

// CatalogView mirrors mothx/manage/providers/list.
type CatalogView struct {
	Providers       []Summary      `json:"providers"`
	ProviderConfigs []ConfigView   `json:"providerConfigs"`
	Models          []CatalogModel `json:"models"`
	ModelDefaults   ModelDefaults  `json:"modelDefaults"`
	DefaultProvider string         `json:"defaultProvider"`
	DefaultModel    string         `json:"defaultModel"`
}

// SettingsView is the safe subset of mothx/manage/settings/get used by
// the providers panel. Only defaults plus the compact provider list are needed.
type SettingsView struct {
	DefaultProvider string    `json:"defaultProvider"`
	DefaultModel    string    `json:"defaultModel"`
	ThinkingLevel   string    `json:"thinkingLevel"`
	Providers       []Summary `json:"providers"`
}

type TestResultView struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latencyMs,omitempty"`
	Error     string `json:"error,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
}

type DiscoverResultView struct {
	Models []Model `json:"models"`
}

// ModelDraft keeps numeric fields as strings so the native editor can
// distinguish empty from zero and reject invalid input before sending.
type ModelDraft struct {
	ID            string
	Name          string
	Reasoning     bool
	ContextWindow string
	MaxTokens     string
	Input         string
}

// Draft is the editable, non-secret provider p.
type Draft struct {
	ID                  string
	Vendor              string
	BaseURL             string
	HTTPProxy           string
	API                 string
	ThinkingFormat      string
	ForceHTTP11         bool
	CacheControl        bool
	MaxImagesPerRequest string
	Models              []ModelDraft

	MaskedKey        string
	APIKeyConfigured bool
	IsDefault        bool
	GlobalOverride   bool
}

type SecretDraft struct {
	APIKey   string
	ClearKey bool
}

type State struct {
	Settings    *SettingsView
	Catalog     *CatalogView
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
	Draft         *Draft
	Secret        SecretDraft
	EditorTab     int
	Scope         string
	Search        string
	Saving        bool
	Deleting      bool
	Testing       bool
	Status        string
	StatusIsError bool

	PendingDelete bool

	// Fallback quick-set-key dialog. Only used when manageSettings is available
	// without manageProviders, matching Desktop's degraded provider panel.
	FallbackKeyOpen   bool
	FallbackKeyName   string
	FallbackKeyValue  string
	FallbackKeySaving bool
	FallbackKeyErr    string

	Discovering        bool
	DiscoverOpen       bool
	DiscoverSearch     string
	DiscoverCandidates []Model
	DiscoverSelected   map[string]*bool

	// Seq guards every asynchronous provider call. Bumping it invalidates any
	// in-flight load/save/delete/test/discover response, which is how selection
	// changes, page exits, and reconnects prevent stale state from landing.
	Seq int64
}

type CapabilitySet struct {
	Catalog  bool
	Settings bool
	Config   bool
}

type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{State: State{
		Scope:            ScopeConfigured,
		DefaultsThinking: providerDefaultThinking,
	}}
}

func ModelDefaultsFor(catalog *CatalogView) ModelDraft {
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
	return ModelDraft{
		Reasoning:     reasoning,
		ContextWindow: strconv.Itoa(contextWindow),
		MaxTokens:     maxTokens,
		Input:         input,
	}
}

func DraftFromConfig(view ConfigView) Draft {
	d := Draft{
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
	d.Models = make([]ModelDraft, 0, len(view.Provider.Models))
	for _, model := range view.Provider.Models {
		d.Models = append(d.Models, ModelDraft{
			ID:            model.ID,
			Name:          model.Name,
			Reasoning:     model.Reasoning,
			ContextWindow: OptionalIntString(model.ContextWindow),
			MaxTokens:     OptionalIntString(model.MaxTokens),
			Input:         strings.Join(model.Input, ", "),
		})
	}
	return d
}

func ConfigByID(catalog *CatalogView, id string) *ConfigView {
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

func ModelsFor(catalog *CatalogView, provider string) []CatalogModel {
	if catalog == nil {
		return nil
	}
	out := make([]CatalogModel, 0)
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

func ModelIDsFor(catalog *CatalogView, provider string) []string {
	models := ModelsFor(catalog, provider)
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func DefaultModelFor(catalog *CatalogView, provider, preferred string) string {
	models := ModelsFor(catalog, provider)
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

func UniqueID(catalog *CatalogView) string {
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

func SelectOptions(current string, options []string) []string {
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

func StringList(input string) []string {
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

func OptionalIntString(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func ParseOptionalInt(value string) (int, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false, i18n.LocalizedError("必须是整数")
	}
	return parsed, true, nil
}

// ValidateDraft enforces identity and numeric minimums client-side so
// a malformed editor never reaches ACP.
func ValidateDraft(draft *Draft, catalog *CatalogView, originalID string) error {
	if draft == nil {
		return i18n.LocalizedError("供应商草稿不存在")
	}
	id := strings.TrimSpace(draft.ID)
	if id == "" {
		return i18n.LocalizedError("供应商 ID 不能为空")
	}
	if strings.Contains(id, "/") {
		return i18n.LocalizedError("供应商 ID 不能包含 /")
	}
	if catalog != nil {
		for _, config := range catalog.ProviderConfigs {
			if config.ID == id && config.ID != originalID {
				return i18n.LocalizedErrorf("供应商 ID %q 已存在", id)
			}
		}
	}
	if originalID != "" && originalID != id {
		original := ConfigByID(catalog, originalID)
		if original == nil || !original.GlobalOverride {
			return i18n.LocalizedError("该供应商不支持重命名，请先保存其全局覆盖配置")
		}
	}
	if value, present, err := ParseOptionalInt(draft.MaxImagesPerRequest); err != nil {
		return i18n.LocalizedError("每次请求最大图片数必须是整数")
	} else if present && value < -1 {
		return i18n.LocalizedError("每次请求最大图片数不能小于 -1")
	}
	seen := make(map[string]bool)
	for _, model := range draft.Models {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			return i18n.LocalizedError("模型 ID 不能为空")
		}
		if seen[modelID] {
			return i18n.LocalizedErrorf("模型 ID %q 重复", modelID)
		}
		seen[modelID] = true
		if value, present, err := ParseOptionalInt(model.ContextWindow); err != nil {
			return i18n.LocalizedErrorf("模型 %s 的上下文窗口必须是整数", modelID)
		} else if present && value < 0 {
			return i18n.LocalizedErrorf("模型 %s 的上下文窗口不能为负数", modelID)
		}
		if value, present, err := ParseOptionalInt(model.MaxTokens); err != nil {
			return i18n.LocalizedErrorf("模型 %s 的最大 Tokens 必须是整数", modelID)
		} else if present && value < 0 {
			return i18n.LocalizedErrorf("模型 %s 的最大 Tokens 不能为负数", modelID)
		}
	}
	return nil
}

func ModelsPayload(draft *Draft) ([]map[string]any, error) {
	models := make([]map[string]any, 0, len(draft.Models))
	for _, model := range draft.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			return nil, i18n.LocalizedError("模型 ID 不能为空")
		}
		entry := map[string]any{"id": id, "reasoning": model.Reasoning}
		if name := strings.TrimSpace(model.Name); name != "" {
			entry["name"] = name
		}
		if value, present, err := ParseOptionalInt(model.ContextWindow); err != nil {
			return nil, i18n.LocalizedErrorf("模型 %s 的上下文窗口必须是整数", id)
		} else if present {
			entry["contextWindow"] = value
		}
		if value, present, err := ParseOptionalInt(model.MaxTokens); err != nil {
			return nil, i18n.LocalizedErrorf("模型 %s 的最大 Tokens 必须是整数", id)
		} else if present {
			entry["maxTokens"] = value
		}
		if input := StringList(model.Input); len(input) > 0 {
			entry["input"] = input
		}
		models = append(models, entry)
	}
	return models, nil
}

// BuildSaveRequest returns the exact ACP params for providers/save.
// The apiKey field is present only when the user typed a new key or explicitly
// asked to clear it; an ordinary save never round-trips a secret.
func BuildSaveRequest(draft *Draft, secret SecretDraft, previousID string, catalog *CatalogView) (map[string]any, error) {
	if err := ValidateDraft(draft, catalog, previousID); err != nil {
		return nil, err
	}
	models, err := ModelsPayload(draft)
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
	if value, _, err := ParseOptionalInt(draft.MaxImagesPerRequest); err != nil {
		return nil, i18n.LocalizedError("每次请求最大图片数必须是整数")
	} else {
		if value < -1 {
			return nil, i18n.LocalizedError("每次请求最大图片数不能小于 -1")
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

// BuildDefaultsPatch is the settings/patch shape for the global
// default provider/model/thinking selectors.
func BuildDefaultsPatch(provider, model, thinking string) (map[string]any, error) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	thinking = strings.TrimSpace(thinking)
	if provider == "" {
		return nil, i18n.LocalizedError("请选择默认供应商")
	}
	if model == "" {
		return nil, i18n.LocalizedError("请选择默认模型")
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
		return nil, i18n.LocalizedErrorf("思考级别 %q 无效", thinking)
	}
	return map[string]any{
		"defaultProvider": provider,
		"defaultModel":    model,
		"thinkingLevel":   thinking,
	}, nil
}

func (p *Panel) Capabilities(h host.Base) CapabilitySet {
	catalog := h.FeatureEnabled("manageProviders")
	return CapabilitySet{
		Catalog:  catalog,
		Settings: h.FeatureEnabled("manageSettings"),
		Config:   catalog && h.FeatureEnabled("manageProviderConfig"),
	}
}

func (p *Panel) Current(h host.Base, seq, reloadSeq int64) bool {
	return h.SettingsOpen() && h.SettingsTab() == "providers" &&
		seq == atomic.LoadInt64(&p.Seq) &&
		reloadSeq == h.ReloadSequence()
}

// Invalidate drops transient state and makes every in-flight
// provider response stale. Loaded is reset so re-entering the tab reloads.
func (p *Panel) Invalidate() {
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
	p.Secret = SecretDraft{}
	p.Draft = nil
	p.ActiveID = ""
	p.OriginalID = ""
	p.Status = ""
	p.StatusIsError = false
	p.CloseFallbackKey()
}

// StartLoad starts one capability-gated load. It is called from
// the UI thread on tab entry.
func (p *Panel) StartLoad(h host.Base) {
	if !h.SettingsOpen() || h.SettingsTab() != "providers" {
		return
	}
	if p.Loading {
		return
	}
	caps := p.Capabilities(h)
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
	startReloadSeq := h.ReloadSequence()
	go p.load(h, seq, startReloadSeq, caps)
}

func (p *Panel) load(h host.Base, seq, startReloadSeq int64, caps CapabilitySet) {
	client := h.ACP()
	if client == nil {
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Loading = false
			p.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	var settings *SettingsView
	settingsErr := ""
	if caps.Settings {
		raw, err := client.Call("mothx/manage/settings/get", map[string]any{})
		if err != nil {
			settingsErr = "读取默认供应商设置失败：" + err.Error()
		} else {
			var view SettingsView
			if err := json.Unmarshal(raw, &view); err != nil {
				settingsErr = "默认供应商设置响应无效：" + err.Error()
			} else {
				settings = &view
			}
		}
	}
	var catalog *CatalogView
	catalogErr := ""
	raw, err := client.Call("mothx/manage/providers/list", map[string]any{})
	if err != nil {
		catalogErr = "读取供应商目录失败：" + err.Error()
	} else {
		var view CatalogView
		if err := json.Unmarshal(raw, &view); err != nil {
			catalogErr = "供应商目录响应无效：" + err.Error()
		} else {
			catalog = &view
		}
	}
	h.Dispatch(func() {
		if !p.Current(h, seq, startReloadSeq) {
			return
		}
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
		p.NormalizeDefaults()
		if p.Draft == nil {
			p.SelectInitial(false)
		}
	})
}

func (p *Panel) NormalizeDefaults() {
	providers := p.ConfigIDs()
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
	p.DefaultsModel = DefaultModelFor(p.Catalog, p.DefaultsProvider, p.DefaultsModel)
	if p.DefaultsModel == "" && preferredModel != "" {
		p.DefaultsModel = DefaultModelFor(p.Catalog, p.DefaultsProvider, preferredModel)
	}
	if p.DefaultsThinking == "" {
		if p.Settings != nil && p.Settings.ThinkingLevel != "" {
			p.DefaultsThinking = p.Settings.ThinkingLevel
		} else {
			p.DefaultsThinking = providerDefaultThinking
		}
	}
}

func (p *Panel) ConfigIDs() []string {
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

func (p *Panel) SelectInitial(invalidate bool) {
	candidates := p.ConfigIDs()
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
	p.SelectDraft(initial, invalidate)
}

func (p *Panel) SelectDraft(id string, invalidate bool) {
	config := ConfigByID(p.Catalog, id)
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
	draft := DraftFromConfig(*config)
	p.ActiveID = config.ID
	p.OriginalID = config.ID
	p.Draft = &draft
	p.Secret = SecretDraft{}
	p.EditorTab = providerEditorConnection
	p.Status = ""
	p.StatusIsError = false
}

func (p *Panel) StartNewDraft() {
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
	id := UniqueID(p.Catalog)
	p.ActiveID = id
	p.OriginalID = ""
	p.Draft = &Draft{ID: id, API: "openai-chat", Models: []ModelDraft{}}
	p.Secret = SecretDraft{}
	p.EditorTab = providerEditorConnection
	p.Status = "正在编辑新供应商；保存后才会出现在目录中。"
	p.StatusIsError = false
}

func (p *Panel) Save(h host.Base) {
	caps := p.Capabilities(h)
	if !caps.Config {
		p.Status = "当前运行时不支持编辑供应商配置（manageProviderConfig）。"
		p.StatusIsError = true
		return
	}
	if p.Draft == nil {
		return
	}
	request, err := BuildSaveRequest(p.Draft, p.Secret, p.OriginalID, p.Catalog)
	if err != nil {
		p.Status = err.Error()
		p.StatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "保存失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Saving = true
	p.Status = "正在保存供应商…"
	p.StatusIsError = false
	savedID := strings.TrimSpace(p.Draft.ID)
	go func() {
		raw, err := client.Call("mothx/manage/providers/save", request)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Saving = false
			if err != nil {
				p.Status = "保存供应商失败：" + ScrubSecret(err.Error(), p.Secret.APIKey)
				p.StatusIsError = true
				return
			}
			var catalog CatalogView
			if err := json.Unmarshal(raw, &catalog); err != nil {
				p.Status = "保存后供应商目录响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.Catalog = &catalog
			p.Loaded = true
			p.Err = ""
			p.ApplyCatalogDefaults()
			if p.Settings != nil {
				p.Settings.DefaultProvider = catalog.DefaultProvider
				p.Settings.DefaultModel = catalog.DefaultModel
			}
			p.SelectDraft(savedID, false)
			p.Status = "供应商已保存"
			p.StatusIsError = false
			p.NormalizeDefaults()
		})
	}()
}

func (p *Panel) ApplyCatalogDefaults() {
	if p.Catalog == nil {
		return
	}
	if p.Settings != nil {
		p.Settings.DefaultProvider = p.Catalog.DefaultProvider
		p.Settings.DefaultModel = p.Catalog.DefaultModel
	}
}

func (p *Panel) RequestDelete() {
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

func (p *Panel) CancelDelete() {
	p.PendingDelete = false
}

func (p *Panel) ConfirmDelete(h host.Base) {
	p.PendingDelete = false
	caps := p.Capabilities(h)
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
	client := h.ACP()
	if client == nil {
		p.Status = "移除失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	id := strings.TrimSpace(p.Draft.ID)
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Deleting = true
	p.Status = "正在移除供应商覆盖…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/providers/delete", map[string]any{"id": id})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Deleting = false
			if err != nil {
				p.Status = "移除供应商失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var catalog CatalogView
			if err := json.Unmarshal(raw, &catalog); err != nil {
				p.Status = "移除后供应商目录响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.Catalog = &catalog
			p.Loaded = true
			p.Err = ""
			p.ApplyCatalogDefaults()
			p.ActiveID = ""
			p.OriginalID = ""
			p.Draft = nil
			p.Secret = SecretDraft{}
			p.NormalizeDefaults()
			p.SelectInitial(false)
			p.Status = "供应商配置已移除"
			p.StatusIsError = false
		})
	}()
}

func (p *Panel) TestDraft(h host.Base) {
	caps := p.Capabilities(h)
	if !caps.Catalog {
		p.Status = "当前运行时不支持供应商连接测试。"
		p.StatusIsError = true
		return
	}
	if p.Draft == nil {
		return
	}
	id := strings.TrimSpace(p.Draft.ID)
	if ConfigByID(p.Catalog, id) == nil {
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
		modelID = DefaultModelFor(p.Catalog, id, "")
	}
	client := h.ACP()
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
	startReloadSeq := h.ReloadSequence()
	p.Testing = true
	p.Status = "正在测试连接…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/providers/test", params)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Testing = false
			if err != nil {
				p.Status = "连接测试失败：" + ScrubSecret(err.Error(), p.Secret.APIKey)
				p.StatusIsError = true
				return
			}
			var result TestResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				p.Status = "连接测试响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			if result.OK {
				p.Status = i18n.Tf("连接测试成功（%s ms）", strconv.FormatInt(result.LatencyMS, 10))
				p.StatusIsError = false
				return
			}
			message := result.Error
			if message == "" {
				message = "未知错误"
			}
			p.Status = "连接测试失败：" + ScrubSecret(message, p.Secret.APIKey)
			p.StatusIsError = true
		})
	}()
}

func (p *Panel) SaveDefaults(h host.Base) {
	caps := p.Capabilities(h)
	if !caps.Settings {
		p.Status = "当前运行时不支持保存默认供应商（manageSettings）。"
		p.StatusIsError = true
		return
	}
	patch, err := BuildDefaultsPatch(p.DefaultsProvider, p.DefaultsModel, p.DefaultsThinking)
	if err != nil {
		p.Status = err.Error()
		p.StatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "保存失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.DefaultsSaving = true
	p.Status = "正在保存默认供应商与模型…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/settings/patch", map[string]any{"patch": patch})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.DefaultsSaving = false
			if err != nil {
				p.Status = "保存默认值失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view SettingsView
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

func (p *Panel) DiscoverModels(h host.Base) {
	caps := p.Capabilities(h)
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
	client := h.ACP()
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
	startReloadSeq := h.ReloadSequence()
	p.Discovering = true
	p.Status = "正在发现模型…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/providers/discover", params)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Discovering = false
			if err != nil {
				p.Status = "发现模型失败：" + ScrubSecret(err.Error(), p.Secret.APIKey)
				p.StatusIsError = true
				return
			}
			var result DiscoverResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				p.Status = "模型发现响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			candidates := make([]Model, 0)
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

func (p *Panel) CloseDiscover() {
	p.DiscoverOpen = false
	p.DiscoverCandidates = nil
	p.DiscoverSelected = nil
	p.DiscoverSearch = ""
}

func (p *Panel) AddDiscoveredModels() {
	if p.Draft == nil {
		p.CloseDiscover()
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
		p.Draft.Models = append(p.Draft.Models, ModelDraft{
			ID:            candidate.ID,
			Name:          candidate.Name,
			Reasoning:     candidate.Reasoning,
			ContextWindow: OptionalIntString(candidate.ContextWindow),
			MaxTokens:     OptionalIntString(candidate.MaxTokens),
			Input:         strings.Join(candidate.Input, ", "),
		})
		existing[candidate.ID] = true
		added++
	}
	p.Status = i18n.Tf("已添加 %d 个模型", added)
	p.StatusIsError = false
	p.CloseDiscover()
}

func (p *Panel) FilteredConfigs() []ConfigView {
	if p.Catalog == nil {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(p.Search))
	out := make([]ConfigView, 0, len(p.Catalog.ProviderConfigs)+1)
	for _, config := range p.Catalog.ProviderConfigs {
		if p.Scope == ScopeConfigured && !config.APIKeyConfigured {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				config.ID, config.Provider.Vendor, config.Provider.BaseURL, MaskedKey(config.MaskedKey),
			}, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, config)
	}
	if p.Draft != nil && ConfigByID(p.Catalog, p.Draft.ID) == nil {
		out = append(out, ConfigView{ID: p.Draft.ID, APIKeyConfigured: false})
	}
	return out
}

func MaskedKey(masked *string) string {
	if masked == nil {
		return ""
	}
	return *masked
}

// ScrubSecret defends the status line even if an upstream error text
// happens to echo the transient draft key. The key is never logged elsewhere.
func ScrubSecret(message, secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) < 4 || message == "" {
		return message
	}
	return strings.ReplaceAll(message, secret, "***")
}

func SummaryList(p *State, settingsAvailable bool) []Summary {
	if settingsAvailable && p.Settings != nil && len(p.Settings.Providers) > 0 {
		return p.Settings.Providers
	}
	if p.Catalog != nil {
		return p.Catalog.Providers
	}
	return nil
}

func (p *Panel) TestByName(h host.Base, name string) {
	caps := p.Capabilities(h)
	if !caps.Catalog {
		p.Status = "当前运行时不支持供应商连接测试。"
		p.StatusIsError = true
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "连接测试失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	params := map[string]any{"provider": name}
	if modelID := DefaultModelFor(p.Catalog, name, ""); modelID != "" {
		params["model"] = modelID
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Testing = true
	p.Status = i18n.Tf("正在测试 %s 的连接…", name)
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/providers/test", params)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Testing = false
			if err != nil {
				p.Status = "连接测试失败：" + ScrubSecret(err.Error(), p.Secret.APIKey)
				p.StatusIsError = true
				return
			}
			var result TestResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				p.Status = "连接测试响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			if result.OK {
				p.Status = i18n.Tf("连接测试成功（%s ms）", strconv.FormatInt(result.LatencyMS, 10))
				p.StatusIsError = false
				return
			}
			message := result.Error
			if message == "" {
				message = "未知错误"
			}
			p.Status = "连接测试失败：" + ScrubSecret(message, p.Secret.APIKey)
			p.StatusIsError = true
		})
	}()
}

func (p *Panel) SetDefault(h host.Base, name string) {
	caps := p.Capabilities(h)
	if !caps.Settings {
		p.Status = "当前运行时不支持设置默认供应商（manageSettings）。"
		p.StatusIsError = true
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "设置默认供应商失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Status = "正在设置默认供应商…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/settings/patch", map[string]any{
			"patch": map[string]any{"defaultProvider": name},
		})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			if err != nil {
				p.Status = "设置默认供应商失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view SettingsView
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

// OpenFallbackKey starts the degraded-panel quick key prompt. The key
// stays write-only: it lives in this transient dialog state and is cleared on
// success, cancel, or page invalidation.
func (p *Panel) OpenFallbackKey(name string) {
	p.FallbackKeyOpen = true
	p.FallbackKeyName = strings.TrimSpace(name)
	p.FallbackKeyValue = ""
	p.FallbackKeyErr = ""
	p.FallbackKeySaving = false
}

func (p *Panel) CloseFallbackKey() {
	p.FallbackKeyOpen = false
	p.FallbackKeyName = ""
	p.FallbackKeyValue = ""
	p.FallbackKeyErr = ""
	p.FallbackKeySaving = false
}

func (p *Panel) SaveFallbackKey(h host.Base) {
	caps := p.Capabilities(h)
	if !caps.Settings {
		p.FallbackKeyErr = "当前运行时不支持设置供应商密钥（manageSettings）。"
		return
	}
	name := strings.TrimSpace(p.FallbackKeyName)
	key := strings.TrimSpace(p.FallbackKeyValue)
	if name == "" {
		p.FallbackKeyErr = "供应商名称不能为空。"
		return
	}
	if key == "" {
		p.FallbackKeyErr = "供应商密钥不能为空。"
		return
	}
	client := h.ACP()
	if client == nil {
		p.FallbackKeyErr = "保存供应商密钥失败：尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.FallbackKeySaving = true
	p.FallbackKeyErr = ""
	go func() {
		raw, err := client.Call("mothx/manage/settings/patch", map[string]any{
			"patch": map[string]any{
				"providerKey": map[string]any{"name": name, "key": key},
			},
		})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.FallbackKeySaving = false
			if err != nil {
				p.FallbackKeyErr = "保存供应商密钥失败：" + err.Error()
				return
			}
			var view SettingsView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.FallbackKeyErr = "保存供应商密钥失败：响应无效"
				return
			}
			p.Settings = &view
			p.CloseFallbackKey()
			p.Status = "供应商密钥已保存"
			p.StatusIsError = false
		})
	}()
}

func ConfigSummary(config ConfigView) Summary {
	return Summary{
		Name:             config.ID,
		MaskedKey:        config.MaskedKey,
		BaseURL:          config.Provider.BaseURL,
		ModelCount:       len(config.Provider.Models),
		APIKeyConfigured: config.APIKeyConfigured,
		IsDefault:        config.IsDefault,
	}
}

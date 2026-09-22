package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/sandbox"
)

// Verbose controls whether config loading prints diagnostic messages to stderr.
var Verbose bool

// Settings holds all configuration for vibecoding.
type Settings struct {
	Providers            map[string]*ProviderConfig `json:"providers,omitempty"`
	DefaultProvider      string                     `json:"defaultProvider,omitempty"`
	DefaultModel         string                     `json:"defaultModel,omitempty"`
	DefaultThinkingLevel string                     `json:"defaultThinkingLevel,omitempty"`
	DefaultMode          string                     `json:"defaultMode,omitempty"`
	Authored             bool                       `json:"authored,omitempty"`
	TUILang              string                     `json:"tuilang,omitempty"`
	ToolExecution        ToolExecutionSettings      `json:"toolExecution,omitempty"`
	StatusLine           StatusLineSettings         `json:"statusLine,omitempty"`
	EnablePlanTool       *bool                      `json:"enablePlanTool,omitempty"`
	EnableArtifact       *bool                      `json:"enableArtifact,omitempty"`
	EnableACPArtifact    *bool                      `json:"enableACPArtifact,omitempty"`
	WebSearch            WebSearchSettings          `json:"webSearch"`
	ImageGeneration      ImageGenerationSettings    `json:"imageGeneration"`
	MaxContextTokens     int                        `json:"maxContextTokens,omitempty"`
	ContextFiles         ContextFilesSettings       `json:"contextFiles"`
	SkillsDir            string                     `json:"skillsDir,omitempty"`
	Skills               *SkillsSettings            `json:"skills,omitempty"`
	SkillHub             SkillHubSettings           `json:"skillHub,omitempty"`
	Compaction           CompactionSettings         `json:"compaction"`
	Sandbox              SandboxSettings            `json:"sandbox"`
	SessionDir           string                     `json:"sessionDir,omitempty"`
	ShellPath            string                     `json:"shellPath,omitempty"`
	ShellCommandPrefix   string                     `json:"shellCommandPrefix,omitempty"`
	Theme                string                     `json:"theme,omitempty"`
	Retry                RetrySettings              `json:"retry"`
	Approval             ApprovalSettings           `json:"approval"`
	Maintenance          *MaintenanceSettings       `json:"maintenance,omitempty"`
	UpdateCheck          *bool                      `json:"updateCheck,omitempty"` // nil/true = check npm for updates on startup, false = disabled
}

// MaintenanceSettings configures Runtime-owned housekeeping that is scheduled
// through the shared cron lifecycle but is not a user automation task. Absent
// means every default applies, and the defaults keep the previous behavior:
// attachment storage reconciliation runs once a day.
type MaintenanceSettings struct {
	// ReclaimAttachmentStorage gates the private attachment-store reconciliation.
	// nil/true = enabled, false = the scheduled job is removed and the work is
	// refused even if a stale job row still exists.
	ReclaimAttachmentStorage *bool `json:"reclaimAttachmentStorage,omitempty"`
	// StorageReconcileSchedule overrides the cadence of that reconciliation
	// (for example "@every 12h" or "0 4 * * *"). Empty uses the Runtime default,
	// and an invalid value falls back to it with a cron log line.
	StorageReconcileSchedule string `json:"storageReconcileSchedule,omitempty"`
}

// MarshalJSON keeps sparse settings files sparse. encoding/json does not omit
// a zero-value struct with omitempty, so handle the toolExecution object
// explicitly while preserving the existing Settings value API.
func (s Settings) MarshalJSON() ([]byte, error) {
	type settingsJSON Settings
	data, err := json.Marshal(settingsJSON(s))
	if err != nil {
		return nil, err
	}
	if s.ToolExecution.Mode != "" || s.ToolExecution.MaxConcurrency != 0 {
		return data, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	delete(raw, "toolExecution")
	return json.Marshal(raw)
}

func (s *Settings) UnmarshalJSON(data []byte) error {
	type settingsJSON Settings
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	providersRaw, hasProviders := raw["providers"]
	delete(raw, "providers")

	withoutProviders, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	aux := settingsJSON(*s)
	if err := json.Unmarshal(withoutProviders, &aux); err != nil {
		return err
	}
	*s = Settings(aux)

	if !hasProviders {
		return nil
	}
	var providerEntries map[string]json.RawMessage
	if err := json.Unmarshal(providersRaw, &providerEntries); err != nil {
		return err
	}
	if s.Providers == nil {
		s.Providers = map[string]*ProviderConfig{}
	}
	for id, providerData := range providerEntries {
		pc := s.Providers[id]
		if pc == nil {
			pc = &ProviderConfig{}
		}
		if err := json.Unmarshal(providerData, pc); err != nil {
			return err
		}
		s.Providers[id] = pc
	}
	return nil
}

type ProviderConfig struct {
	Vendor         string            `json:"vendor,omitempty"`    // Explicit vendor adapter (Decision 12/13)
	APIKey         string            `json:"apiKey,omitempty"`    // API key or env/shell reference
	BaseURL        string            `json:"baseUrl,omitempty"`   // API base URL
	HTTPProxy      string            `json:"httpProxy,omitempty"` // optional per-provider HTTP proxy URL, e.g. http://127.0.0.1:7890
	ForceHTTP11    bool              `json:"forceHTTP11,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"` // optional per-provider HTTP headers
	API            string            `json:"api,omitempty"`
	ThinkingFormat string            `json:"thinkingFormat,omitempty"` // "", "openai", "anthropic", "deepseek", "xiaomi"
	CacheControl   *bool             `json:"cacheControl,omitempty"`   // enable Anthropic prompt caching (nil/false=off, true=on; set true for Claude models)
	// MaxImagesPerRequest limits image blocks sent to OpenAI-compatible APIs.
	// Zero uses the provider default; -1 disables the client-side limit.
	MaxImagesPerRequest int             `json:"maxImagesPerRequest,omitempty"`
	Responses           ResponsesConfig `json:"responses,omitempty"`
	Models              []ModelConfig   `json:"models"`

	fieldSet map[string]bool `json:"-"`
}

type ResponsesConfig struct {
	ReasoningSummary     string                          `json:"reasoningSummary,omitempty"`     // "auto" (default), "concise", or "detailed"
	ReasoningContext     string                          `json:"reasoningContext,omitempty"`     // auto, current_turn, all_turns
	ReasoningMode        string                          `json:"reasoningMode,omitempty"`        // standard, pro
	PromptCacheEnabled   *bool                           `json:"promptCacheEnabled,omitempty"`   // nil/true = on, false = off
	PromptCacheKey       string                          `json:"promptCacheKey,omitempty"`       // optional explicit cache key; defaults to provider/model stable key
	PromptCacheRetention string                          `json:"promptCacheRetention,omitempty"` // optional OpenAI prompt cache retention value
	PromptCacheMode      string                          `json:"promptCacheMode,omitempty"`      // implicit, explicit
	PromptCacheTTL       string                          `json:"promptCacheTTL,omitempty"`       // e.g. 5m, 1h
	SafetyIdentifier     string                          `json:"safetyIdentifier,omitempty"`
	Metadata             map[string]string               `json:"metadata,omitempty"`
	StateMode            string                          `json:"stateMode,omitempty"` // replay, previous_response_id, conversation
	Store                *bool                           `json:"store,omitempty"`
	Conversation         string                          `json:"conversation,omitempty"`
	Truncation           string                          `json:"truncation,omitempty"`
	Background           *bool                           `json:"background,omitempty"`
	Include              []string                        `json:"include,omitempty"`
	ServiceTier          string                          `json:"serviceTier,omitempty"`
	StructuredOutput     ResponsesStructuredOutputConfig `json:"structuredOutput,omitempty"`
	ToolControl          ResponsesToolControlConfig      `json:"toolControl,omitempty"`
	HostedTools          ResponsesHostedToolsConfig      `json:"hostedTools,omitempty"`
}

type ResponsesStructuredOutputConfig struct {
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

type ResponsesToolControlConfig struct {
	Choice   string `json:"choice,omitempty"`
	Parallel *bool  `json:"parallel,omitempty"`
	MaxCalls int    `json:"maxCalls,omitempty"`
}

type ResponsesHostedToolsConfig struct {
	WebSearch       map[string]any   `json:"webSearch,omitempty"`
	FileSearch      map[string]any   `json:"fileSearch,omitempty"`
	CodeInterpreter map[string]any   `json:"codeInterpreter,omitempty"`
	ComputerUse     map[string]any   `json:"computerUse,omitempty"`
	ImageGeneration map[string]any   `json:"imageGeneration,omitempty"`
	RemoteMCP       []map[string]any `json:"remoteMCP,omitempty"`
}

type WebSearchSettings struct {
	Enabled      *bool  `json:"enabled,omitempty"`
	Provider     string `json:"provider,omitempty"`
	ProviderType string `json:"providerType,omitempty"`
	Model        string `json:"model,omitempty"`
}

// DefaultToolExecutionMaxConcurrency is the default number of local tool
// calls that may execute concurrently in one agent turn.
const DefaultToolExecutionMaxConcurrency = 10

// ToolExecutionSettings controls local function/custom tool execution. It is
// separate from provider Responses tool controls, which govern provider-side
// call generation and quotas rather than local workers.
type ToolExecutionSettings struct {
	Mode           string `json:"mode,omitempty"`
	MaxConcurrency int    `json:"maxConcurrency,omitempty"`
}

// EffectiveMode returns the normalized local tool execution mode.
func (s ToolExecutionSettings) EffectiveMode() string {
	if strings.EqualFold(strings.TrimSpace(s.Mode), "sequential") {
		return "sequential"
	}
	return "parallel"
}

// EffectiveMaxConcurrency returns the configured local tool concurrency,
// using the product default when the setting is omitted or non-positive.
func (s ToolExecutionSettings) EffectiveMaxConcurrency() int {
	if s.MaxConcurrency <= 0 {
		return DefaultToolExecutionMaxConcurrency
	}
	return s.MaxConcurrency
}

// ImageGenerationSettings configures the standalone local image_generation
// tool. The endpoint and credential are independent from the chat provider.
type ImageGenerationSettings struct {
	Enabled  *bool  `json:"enabled,omitempty"`
	Provider string `json:"provider,omitempty"`
	APIType  string `json:"apiType,omitempty"`
	BaseURL  string `json:"baseUrl,omitempty"`
	Token    string `json:"token,omitempty"`
	Model    string `json:"model,omitempty"`
}

type SkillHubMarketSettings struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	SiteURL  string `json:"siteURL,omitempty"`
	APIURL   string `json:"apiURL,omitempty"`
	Enabled  bool   `json:"enabled"`
	APIToken string `json:"apiToken,omitempty"`
}

type SkillHubSettings struct {
	DefaultMarket       string                   `json:"defaultMarket,omitempty"`
	DefaultInstallScope string                   `json:"defaultInstallScope,omitempty"`
	OfficialHandles     []string                 `json:"officialHandles,omitempty"`
	Markets             []SkillHubMarketSettings `json:"markets,omitempty"`
}

const DefaultSkillHubOfficialHandle = "user_0064faa7"

type StatusLineSettings struct {
	Enabled         bool   `json:"enabled,omitempty"`
	Type            string `json:"type,omitempty"`
	Command         string `json:"command,omitempty"`
	Padding         int    `json:"padding,omitempty"`
	RefreshInterval int    `json:"refreshInterval,omitempty"`
	TimeoutMs       int    `json:"timeoutMs,omitempty"`
	Fallback        string `json:"fallback,omitempty"`
}

type ModelConfig struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Reasoning     bool         `json:"reasoning,omitempty"`
	ContextWindow int          `json:"contextWindow,omitempty"`
	MaxTokens     int          `json:"maxTokens,omitempty"`
	Temperature   *float64     `json:"temperature,omitempty"` // nil = use API default
	TopP          *float64     `json:"top_p,omitempty"`       // nil = use API default
	Cost          *CostConfig  `json:"cost,omitempty"`
	Input         []string     `json:"input,omitempty"`
	Compat        *ModelCompat `json:"compat,omitempty"` // Vendor compatibility flags (Decision 14)

	fieldSet map[string]bool `json:"-"`
}

type CostConfig struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead,omitempty"`
	CacheWrite float64 `json:"cacheWrite,omitempty"`
}

func (pc *ProviderConfig) UnmarshalJSON(data []byte) error {
	type providerConfigJSON ProviderConfig
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	aux := providerConfigJSON(*pc)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*pc = ProviderConfig(aux)
	pc.fieldSet = cloneFieldSet(pc.fieldSet)
	if pc.fieldSet == nil {
		pc.fieldSet = make(map[string]bool, len(raw))
	}
	for k := range raw {
		pc.fieldSet[k] = true
	}
	return nil
}

func (mc *ModelConfig) UnmarshalJSON(data []byte) error {
	type modelConfigJSON ModelConfig
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	aux := modelConfigJSON(*mc)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*mc = ModelConfig(aux)
	mc.fieldSet = cloneFieldSet(mc.fieldSet)
	if mc.fieldSet == nil {
		mc.fieldSet = make(map[string]bool, len(raw))
	}
	for k := range raw {
		mc.fieldSet[k] = true
	}
	return nil
}

func configFieldWasSet(fields map[string]bool, name string) bool {
	return fields != nil && fields[name]
}

func markConfigField(fields map[string]bool, name string) map[string]bool {
	if fields == nil {
		fields = make(map[string]bool, 1)
	}
	fields[name] = true
	return fields
}

func (mc ModelConfig) MaxTokensWasSet() bool {
	return configFieldWasSet(mc.fieldSet, "maxTokens")
}

// SetMaxTokens records an explicit output-token setting. A value of zero means
// use the provider default and must remain distinguishable from an omitted value.
func (mc *ModelConfig) SetMaxTokens(value int) {
	mc.MaxTokens = value
	mc.fieldSet = markConfigField(mc.fieldSet, "maxTokens")
}

// MarshalJSON preserves an explicitly configured zero maxTokens value. This
// lets the TUI and WebUI express "do not send an output token limit" while
// keeping ordinary omitted values sparse.
func (mc ModelConfig) MarshalJSON() ([]byte, error) {
	type modelConfigJSON ModelConfig
	data, err := json.Marshal(modelConfigJSON(mc))
	if err != nil || !mc.MaxTokensWasSet() || mc.MaxTokens != 0 {
		return data, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	raw["maxTokens"] = json.RawMessage("0")
	return json.Marshal(raw)
}

// ModelCompat defines per-model compatibility flags (Decision 14).
// Reference: pi/packages/ai/src/models.generated.ts compat field
type ModelCompat struct {
	// Thinking/reasoning
	ThinkingFormat                              string `json:"thinkingFormat,omitempty"`
	RequiresReasoningContentOnAssistant         bool   `json:"requiresReasoningContentOnAssistant,omitempty"`
	RequiresReasoningContentOnAssistantMessages bool   `json:"requiresReasoningContentOnAssistantMessages,omitempty"`
	ForceAdaptiveThinking                       bool   `json:"forceAdaptiveThinking,omitempty"`
	// ParseReasoningInContent extracts reasoning wrapped in <think>...</think>
	// tags from the content stream (for models that inline thinking in the body
	// instead of using a separate reasoning_content field).
	ParseReasoningInContent bool `json:"parseReasoningInContent,omitempty"`

	// API parameter compatibility
	SupportsDeveloperRole      *bool           `json:"supportsDeveloperRole,omitempty"`
	SupportsStore              *bool           `json:"supportsStore,omitempty"`
	SupportsResponses          *bool           `json:"supportsResponses,omitempty"`
	SupportsPreviousResponseID *bool           `json:"supportsPreviousResponseId,omitempty"`
	SupportsConversation       *bool           `json:"supportsConversation,omitempty"`
	SupportsBackground         *bool           `json:"supportsBackground,omitempty"`
	SupportsStructuredOutput   *bool           `json:"supportsStructuredOutput,omitempty"`
	SupportsServiceTier        *bool           `json:"supportsServiceTier,omitempty"`
	SupportsParallelToolCalls  *bool           `json:"supportsParallelToolCalls,omitempty"`
	SupportsToolChoice         *bool           `json:"supportsToolChoice,omitempty"`
	SupportsHostedTools        map[string]bool `json:"supportsHostedTools,omitempty"`
	SupportedInclude           []string        `json:"supportedInclude,omitempty"`
	SupportsReasoningEffort    *bool           `json:"supportsReasoningEffort,omitempty"`
	SupportsStrictMode         *bool           `json:"supportsStrictMode,omitempty"`
	MaxTokensField             string          `json:"maxTokensField,omitempty"`
	// DisableSamplingParams omits temperature/top_p from requests. It defaults
	// to true (nil): sampling parameters are only sent when explicitly set to
	// false for models that accept them.
	DisableSamplingParams *bool `json:"disableSamplingParams,omitempty"`

	// Cache
	SupportsCacheControlOnTools *bool `json:"supportsCacheControlOnTools,omitempty"`
	SupportsLongCacheRetention  *bool `json:"supportsLongCacheRetention,omitempty"`
	SupportsPromptCacheKey      *bool `json:"supportsPromptCacheKey,omitempty"`
	SupportsReasoningSummary    *bool `json:"supportsReasoningSummary,omitempty"`
	SendSessionAffinityHeaders  bool  `json:"sendSessionAffinityHeaders,omitempty"`

	// Streaming
	SupportsEagerToolInputStreaming *bool `json:"supportsEagerToolInputStreaming,omitempty"`
}

// BoolPtr returns a pointer to the given bool value.
func BoolPtr(v bool) *bool { return &v }

type ContextFilesSettings struct {
	Enabled    bool     `json:"enabled"`
	ExtraFiles []string `json:"extraFiles,omitempty"`
}

// SkillsSettings controls skill activation. Names listed in Disabled are
// hidden from every skill discovery surface (system prompt listing, /skill
// commands, management views) after the next skills load. A nil or absent
// section keeps every discovered skill enabled, preserving the historical
// behavior of settings files written before this field existed.
type SkillsSettings struct {
	Disabled []string `json:"disabled,omitempty"`
}

// SkillsDisabled returns the configured disabled skill names. It is nil-safe
// and returns a copy so callers cannot mutate the settings value.
func (s *Settings) SkillsDisabled() []string {
	if s == nil || s.Skills == nil || len(s.Skills.Disabled) == 0 {
		return nil
	}
	return append([]string(nil), s.Skills.Disabled...)
}

type CompactionSettings struct {
	Enabled          bool   `json:"enabled"`
	ReserveTokens    int    `json:"reserveTokens"`
	KeepRecentTokens int    `json:"keepRecentTokens"`
	Tokenizer        string `json:"tokenizer,omitempty"`
	TokenizerModel   string `json:"tokenizerModel,omitempty"`
	Template         string `json:"template,omitempty"`
}

type SandboxSettings struct {
	Enabled      bool     `json:"enabled"`
	Level        string   `json:"level"`
	BwrapPath    string   `json:"bwrapPath,omitempty"`
	AllowNetwork bool     `json:"allowNetwork"`
	AllowedRead  []string `json:"allowedRead,omitempty"`
	AllowedWrite []string `json:"allowedWrite,omitempty"`
	DeniedPaths  []string `json:"deniedPaths,omitempty"`
	PassEnv      []string `json:"passEnv,omitempty"`
	TmpSize      string   `json:"tmpSize,omitempty"`
	ProtectGit   bool     `json:"protectGit,omitempty"`
}

func (s SandboxSettings) Options() sandbox.Options {
	return sandbox.Options{
		BwrapPath: s.BwrapPath, AllowNetwork: s.AllowNetwork,
		AllowedRead: s.AllowedRead, AllowedWrite: s.AllowedWrite,
		DeniedPaths: s.DeniedPaths, PassEnv: s.PassEnv, TmpSize: s.TmpSize,
		ProtectGit: s.ProtectGit,
	}
}

type RetrySettings struct {
	Enabled     bool `json:"enabled"`
	MaxRetries  int  `json:"maxRetries"`
	BaseDelayMs int  `json:"baseDelayMs"`
}

type ApprovalSettings struct {
	// BashWhitelist is a list of command prefixes that auto-approve in agent mode
	BashWhitelist []string `json:"bashWhitelist,omitempty"`
	// BashBlacklist is a list of command prefixes that always require approval (even in yolo mode if configured)
	BashBlacklist []string `json:"bashBlacklist,omitempty"`
	// ConfirmBeforeWrite requires user approval before write/edit tools run in agent mode.
	ConfirmBeforeWrite *bool `json:"confirmBeforeWrite,omitempty"`
}

var defaultProviderConfigs = map[string]*ProviderConfig{
	"anthropic": &ProviderConfig{
		BaseURL: "https://api.anthropic.com",
		APIKey:  "${ANTHROPIC_API_KEY}",
		API:     "anthropic-messages",
		Models: []ModelConfig{
			{ID: "claude-3-5-haiku-20241022", Name: "Claude Haiku 3.5", ContextWindow: 200000, MaxTokens: 8192, Cost: &CostConfig{Input: 0.8, Output: 4, CacheRead: 0.08, CacheWrite: 1}, Input: []string{"text", "image"}},
			{ID: "claude-3-5-haiku-latest", Name: "Claude Haiku 3.5 (latest)", ContextWindow: 200000, MaxTokens: 8192, Cost: &CostConfig{Input: 0.8, Output: 4, CacheRead: 0.08, CacheWrite: 1}, Input: []string{"text", "image"}},
			{ID: "claude-3-5-sonnet-20240620", Name: "Claude Sonnet 3.5", ContextWindow: 200000, MaxTokens: 8192, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
			{ID: "claude-3-5-sonnet-20241022", Name: "Claude Sonnet 3.5 v2", ContextWindow: 200000, MaxTokens: 8192, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
			{ID: "claude-3-7-sonnet-20250219", Name: "Claude Sonnet 3.7", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
			{ID: "claude-3-haiku-20240307", Name: "Claude Haiku 3", ContextWindow: 200000, MaxTokens: 4096, Cost: &CostConfig{Input: 0.25, Output: 1.25, CacheRead: 0.03, CacheWrite: 0.3}, Input: []string{"text", "image"}},
			{ID: "claude-3-opus-20240229", Name: "Claude Opus 3", ContextWindow: 200000, MaxTokens: 4096, Cost: &CostConfig{Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75}, Input: []string{"text", "image"}},
			{ID: "claude-3-sonnet-20240229", Name: "Claude Sonnet 3", ContextWindow: 200000, MaxTokens: 4096, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 0.3}, Input: []string{"text", "image"}},
			{ID: "claude-fable-5", Name: "Claude Fable 5", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Cost: &CostConfig{Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5}, Input: []string{"text", "image"}},
			{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5 (latest)", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25}, Input: []string{"text", "image"}},
			{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25}, Input: []string{"text", "image"}},
			{ID: "claude-opus-4-0", Name: "Claude Opus 4 (latest)", Reasoning: true, ContextWindow: 200000, MaxTokens: 32000, Cost: &CostConfig{Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75}, Input: []string{"text", "image"}},
			{ID: "claude-opus-4-1", Name: "Claude Opus 4.1 (latest)", Reasoning: true, ContextWindow: 200000, MaxTokens: 32000, Cost: &CostConfig{Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75}, Input: []string{"text", "image"}},
			{ID: "claude-opus-4-1-20250805", Name: "Claude Opus 4.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 32000, Cost: &CostConfig{Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75}, Input: []string{"text", "image"}},
			{ID: "claude-opus-4-20250514", Name: "Claude Opus 4", Reasoning: true, ContextWindow: 200000, MaxTokens: 32000, Cost: &CostConfig{Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75}, Input: []string{"text", "image"}},
			{ID: "claude-opus-4-5", Name: "Claude Opus 4.5 (latest)", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}, Input: []string{"text", "image"}},
			{ID: "claude-opus-4-5-20251101", Name: "Claude Opus 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}, Input: []string{"text", "image"}},
			{ID: "claude-opus-4-6", Name: "Claude Opus 4.6", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}, Input: []string{"text", "image"}},
			{ID: "claude-opus-4-7", Name: "Claude Opus 4.7", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}, Input: []string{"text", "image"}},
			{ID: "claude-opus-4-8", Name: "Claude Opus 4.8", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}, Input: []string{"text", "image"}},
			{ID: "claude-sonnet-4-0", Name: "Claude Sonnet 4 (latest)", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
			{ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
			{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5 (latest)", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
			{ID: "claude-sonnet-4-5-20250929", Name: "Claude Sonnet 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
			{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", Reasoning: true, ContextWindow: 1000000, MaxTokens: 64000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
		},
	},
	"deepseek-anthropic": &ProviderConfig{
		BaseURL: "https://api.deepseek.com/anthropic",
		APIKey:  "${DEEPSEEK_API_KEY}",
		API:     "anthropic-messages",
		Models: []ModelConfig{
			{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Cost: &CostConfig{Input: 0.14, Output: 0.28, CacheRead: 0.0028}, Input: []string{"text"}},
			{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Cost: &CostConfig{Input: 0.435, Output: 0.87, CacheRead: 0.003625}, Input: []string{"text"}},
			{ID: "deepseek-v4-flash-vision-exp", Name: "DeepSeek V4 Flash Vision Exp", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		},
	},
	"deepseek-openai": &ProviderConfig{
		BaseURL: "https://api.deepseek.com",
		APIKey:  "${DEEPSEEK_API_KEY}",
		API:     "openai-chat",
		Models: []ModelConfig{
			{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Cost: &CostConfig{Input: 0.14, Output: 0.28, CacheRead: 0.0028}, Input: []string{"text"}},
			{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Cost: &CostConfig{Input: 0.435, Output: 0.87, CacheRead: 0.003625}, Input: []string{"text"}},
			{ID: "deepseek-v4-flash-vision-exp", Name: "DeepSeek V4 Flash Vision Exp", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		},
	},
	"openai": &ProviderConfig{
		BaseURL:             "https://api.openai.com/v1",
		APIKey:              "${OPENAI_API_KEY}",
		API:                 "openai-responses",
		MaxImagesPerRequest: 1500,
		Headers:             map[string]string{"User-Agent": "codex_cli_rs/0.144.4"},
		Models: []ModelConfig{
			{ID: "gpt-4", Name: "GPT-4", ContextWindow: 8192, MaxTokens: 8192, Cost: &CostConfig{Input: 30, Output: 60}, Input: []string{"text"}},
			{ID: "gpt-4-turbo", Name: "GPT-4 Turbo", ContextWindow: 128000, MaxTokens: 4096, Cost: &CostConfig{Input: 10, Output: 30}, Input: []string{"text", "image"}},
			{ID: "gpt-4.1", Name: "GPT-4.1", ContextWindow: 1047576, MaxTokens: 32768, Cost: &CostConfig{Input: 2, Output: 8, CacheRead: 0.5}, Input: []string{"text", "image"}},
			{ID: "gpt-4.1-mini", Name: "GPT-4.1 mini", ContextWindow: 1047576, MaxTokens: 32768, Cost: &CostConfig{Input: 0.4, Output: 1.6, CacheRead: 0.1}, Input: []string{"text", "image"}},
			{ID: "gpt-4.1-nano", Name: "GPT-4.1 nano", ContextWindow: 1047576, MaxTokens: 32768, Cost: &CostConfig{Input: 0.1, Output: 0.4, CacheRead: 0.025}, Input: []string{"text", "image"}},
			{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 2.5, Output: 10, CacheRead: 1.25}, Input: []string{"text", "image"}},
			{ID: "gpt-4o-2024-05-13", Name: "GPT-4o (2024-05-13)", ContextWindow: 128000, MaxTokens: 4096, Cost: &CostConfig{Input: 5, Output: 15}, Input: []string{"text", "image"}},
			{ID: "gpt-4o-2024-08-06", Name: "GPT-4o (2024-08-06)", ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 2.5, Output: 10, CacheRead: 1.25}, Input: []string{"text", "image"}},
			{ID: "gpt-4o-2024-11-20", Name: "GPT-4o (2024-11-20)", ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 2.5, Output: 10, CacheRead: 1.25}, Input: []string{"text", "image"}},
			{ID: "gpt-4o-mini", Name: "GPT-4o mini", ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 0.15, Output: 0.6, CacheRead: 0.075}, Input: []string{"text", "image"}},
			{ID: "gpt-5", Name: "GPT-5", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125}, Input: []string{"text", "image"}},
			{ID: "gpt-5-chat-latest", Name: "GPT-5 Chat Latest", ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125}, Input: []string{"text", "image"}},
			{ID: "gpt-5-codex", Name: "GPT-5-Codex", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125}, Input: []string{"text", "image"}},
			{ID: "gpt-5-mini", Name: "GPT-5 Mini", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.25, Output: 2, CacheRead: 0.025}, Input: []string{"text", "image"}},
			{ID: "gpt-5-nano", Name: "GPT-5 Nano", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.05, Output: 0.4, CacheRead: 0.005}, Input: []string{"text", "image"}},
			{ID: "gpt-5-pro", Name: "GPT-5 Pro", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 15, Output: 120}, Input: []string{"text", "image"}},
			{ID: "gpt-5.1", Name: "GPT-5.1", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125}, Input: []string{"text", "image"}},
			{ID: "gpt-5.1-chat-latest", Name: "GPT-5.1 Chat", Reasoning: true, ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125}, Input: []string{"text", "image"}},
			{ID: "gpt-5.1-codex", Name: "GPT-5.1 Codex", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125}, Input: []string{"text", "image"}},
			{ID: "gpt-5.1-codex-max", Name: "GPT-5.1 Codex Max", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125}, Input: []string{"text", "image"}},
			{ID: "gpt-5.1-codex-mini", Name: "GPT-5.1 Codex mini", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.25, Output: 2, CacheRead: 0.025}, Input: []string{"text", "image"}},
			{ID: "gpt-5.2", Name: "GPT-5.2", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 1.75, Output: 14, CacheRead: 0.175}, Input: []string{"text", "image"}},
			{ID: "gpt-5.2-chat-latest", Name: "GPT-5.2 Chat", Reasoning: true, ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 1.75, Output: 14, CacheRead: 0.175}, Input: []string{"text", "image"}},
			{ID: "gpt-5.2-codex", Name: "GPT-5.2 Codex", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 1.75, Output: 14, CacheRead: 0.175}, Input: []string{"text", "image"}},
			{ID: "gpt-5.2-pro", Name: "GPT-5.2 Pro", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 21, Output: 168}, Input: []string{"text", "image"}},
			{ID: "gpt-5.3-chat-latest", Name: "GPT-5.3 Chat (latest)", ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 1.75, Output: 14, CacheRead: 0.175}, Input: []string{"text", "image"}},
			{ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 1.75, Output: 14, CacheRead: 0.175}, Input: []string{"text", "image"}},
			{ID: "gpt-5.3-codex-spark", Name: "GPT-5.3 Codex Spark", Reasoning: true, ContextWindow: 128000, MaxTokens: 32000, Cost: &CostConfig{Input: 1.75, Output: 14, CacheRead: 0.175}, Input: []string{"text", "image"}},
			{ID: "gpt-5.4", Name: "GPT-5.4", Reasoning: true, ContextWindow: 272000, MaxTokens: 128000, Cost: &CostConfig{Input: 2.5, Output: 15, CacheRead: 0.25}, Input: []string{"text", "image"}},
			{ID: "gpt-5.4-mini", Name: "GPT-5.4 mini", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.75, Output: 4.5, CacheRead: 0.075}, Input: []string{"text", "image"}},
			{ID: "gpt-5.4-nano", Name: "GPT-5.4 nano", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.2, Output: 1.25, CacheRead: 0.02}, Input: []string{"text", "image"}},
			{ID: "gpt-5.4-pro", Name: "GPT-5.4 Pro", Reasoning: true, ContextWindow: 1050000, MaxTokens: 128000, Cost: &CostConfig{Input: 30, Output: 180}, Input: []string{"text", "image"}},
			{ID: "gpt-5.5", Name: "GPT-5.5", Reasoning: true, ContextWindow: 272000, MaxTokens: 128000, Cost: &CostConfig{Input: 5, Output: 30, CacheRead: 0.5}, Input: []string{"text", "image"}},
			{ID: "gpt-5.5-pro", Name: "GPT-5.5 Pro", Reasoning: true, ContextWindow: 1050000, MaxTokens: 128000, Cost: &CostConfig{Input: 30, Output: 180}, Input: []string{"text", "image"}},
			{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", Reasoning: true, Input: []string{"text", "image"}},
			{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", Reasoning: true, Input: []string{"text", "image"}},
			{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", Reasoning: true, Input: []string{"text", "image"}},
			{ID: "o1", Name: "o1", Reasoning: true, ContextWindow: 200000, MaxTokens: 100000, Cost: &CostConfig{Input: 15, Output: 60, CacheRead: 7.5}, Input: []string{"text", "image"}},
			{ID: "o1-pro", Name: "o1-pro", Reasoning: true, ContextWindow: 200000, MaxTokens: 100000, Cost: &CostConfig{Input: 150, Output: 600}, Input: []string{"text", "image"}},
			{ID: "o3", Name: "o3", Reasoning: true, ContextWindow: 200000, MaxTokens: 100000, Cost: &CostConfig{Input: 2, Output: 8, CacheRead: 0.5}, Input: []string{"text", "image"}},
			{ID: "o3-deep-research", Name: "o3-deep-research", Reasoning: true, ContextWindow: 200000, MaxTokens: 100000, Cost: &CostConfig{Input: 10, Output: 40, CacheRead: 2.5}, Input: []string{"text", "image"}},
			{ID: "o3-mini", Name: "o3-mini", Reasoning: true, ContextWindow: 200000, MaxTokens: 100000, Cost: &CostConfig{Input: 1.1, Output: 4.4, CacheRead: 0.55}, Input: []string{"text"}},
			{ID: "o3-pro", Name: "o3-pro", Reasoning: true, ContextWindow: 200000, MaxTokens: 100000, Cost: &CostConfig{Input: 20, Output: 80}, Input: []string{"text", "image"}},
			{ID: "o4-mini", Name: "o4-mini", Reasoning: true, ContextWindow: 200000, MaxTokens: 100000, Cost: &CostConfig{Input: 1.1, Output: 4.4, CacheRead: 0.275}, Input: []string{"text", "image"}},
			{ID: "o4-mini-deep-research", Name: "o4-mini-deep-research", Reasoning: true, ContextWindow: 200000, MaxTokens: 100000, Cost: &CostConfig{Input: 2, Output: 8, CacheRead: 0.5}, Input: []string{"text", "image"}},
		},
	},
	"codeok": &ProviderConfig{
		Vendor:  "codeok",
		BaseURL: "https://www.codeok.cc/v1",
		APIKey:  "${CODEOK_API_KEY}",
		API:     "openai-responses",
		Headers: map[string]string{"User-Agent": "codex_cli_rs/0.144.4"},
		Models: []ModelConfig{
			{ID: "gpt-5.5", Name: "GPT-5.5", Reasoning: true, ContextWindow: 272000, MaxTokens: 128000, Input: []string{"text", "image"}},
			{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", Reasoning: true, Input: []string{"text", "image"}},
			{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", Reasoning: true, Input: []string{"text", "image"}},
			{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", Reasoning: true, Input: []string{"text", "image"}},
		},
	},
	"yescode": &ProviderConfig{
		Vendor:  "yescode",
		BaseURL: "https://co.yes.vg/v1",
		APIKey:  "${YESCODE_API_KEY}",
		API:     "openai-responses",
		Headers: map[string]string{"User-Agent": "codex_cli_rs/0.144.4"},
		Models: []ModelConfig{
			{ID: "gpt-5.5", Name: "GPT-5.5", Reasoning: true, ContextWindow: 272000, MaxTokens: 128000, Input: []string{"text", "image"}},
			{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", Reasoning: true, Input: []string{"text", "image"}},
			{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", Reasoning: true, Input: []string{"text", "image"}},
			{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", Reasoning: true, Input: []string{"text", "image"}},
		},
	},
	"google-gemini": &ProviderConfig{
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/models",
		APIKey:  "${GOOGLE_API_KEY}",
		API:     "google-gemini",
		Models: []ModelConfig{
			{ID: "gemini-2.0-flash", Name: "Gemini 2.0 Flash", ContextWindow: 1048576, MaxTokens: 8192, Cost: &CostConfig{Input: 0.1, Output: 0.4, CacheRead: 0.025}, Input: []string{"text", "image"}},
			{ID: "gemini-2.0-flash-lite", Name: "Gemini 2.0 Flash-Lite", ContextWindow: 1048576, MaxTokens: 8192, Cost: &CostConfig{Input: 0.075, Output: 0.3}, Input: []string{"text", "image"}},
			{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.3, Output: 2.5, CacheRead: 0.03}, Input: []string{"text", "image"}},
			{ID: "gemini-2.5-flash-lite", Name: "Gemini 2.5 Flash-Lite", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.1, Output: 0.4, CacheRead: 0.01}, Input: []string{"text", "image"}},
			{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125}, Input: []string{"text", "image"}},
			{ID: "gemini-3-flash-preview", Name: "Gemini 3 Flash Preview", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.5, Output: 3, CacheRead: 0.05}, Input: []string{"text", "image"}},
			{ID: "gemini-3-pro-preview", Name: "Gemini 3 Pro Preview", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 2, Output: 12, CacheRead: 0.2}, Input: []string{"text", "image"}},
			{ID: "gemini-3.1-flash-lite", Name: "Gemini 3.1 Flash Lite", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.25, Output: 1.5, CacheRead: 0.025}, Input: []string{"text", "image"}},
			{ID: "gemini-3.1-flash-lite-preview", Name: "Gemini 3.1 Flash Lite Preview", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.25, Output: 1.5, CacheRead: 0.025}, Input: []string{"text", "image"}},
			{ID: "gemini-3.1-pro-preview", Name: "Gemini 3.1 Pro Preview", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 2, Output: 12, CacheRead: 0.2}, Input: []string{"text", "image"}},
			{ID: "gemini-3.1-pro-preview-customtools", Name: "Gemini 3.1 Pro Preview Custom Tools", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 2, Output: 12, CacheRead: 0.2}, Input: []string{"text", "image"}},
			{ID: "gemini-3.5-flash", Name: "Gemini 3.5 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 1.5, Output: 9, CacheRead: 0.15}, Input: []string{"text", "image"}},
			{ID: "gemini-flash-latest", Name: "Gemini Flash Latest", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 1.5, Output: 9, CacheRead: 0.15}, Input: []string{"text", "image"}},
			{ID: "gemini-flash-lite-latest", Name: "Gemini Flash-Lite Latest", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.25, Output: 1.5, CacheRead: 0.025}, Input: []string{"text", "image"}},
			{ID: "gemma-4-26b-a4b-it", Name: "Gemma 4 26B A4B IT", Reasoning: true, ContextWindow: 262144, MaxTokens: 32768, Input: []string{"text", "image"}},
			{ID: "gemma-4-31b-it", Name: "Gemma 4 31B IT", Reasoning: true, ContextWindow: 262144, MaxTokens: 32768, Input: []string{"text", "image"}},
		},
	},
	"google-vertex": &ProviderConfig{
		BaseURL: "https://aiplatform.googleapis.com/v1/publishers/google/models",
		APIKey:  "${GOOGLE_CLOUD_API_KEY}",
		API:     "google-vertex",
		Models: []ModelConfig{
			{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.3, Output: 2.5, CacheRead: 0.03}, Input: []string{"text", "image"}},
			{ID: "gemini-2.5-flash-lite", Name: "Gemini 2.5 Flash-Lite", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.1, Output: 0.4, CacheRead: 0.01}, Input: []string{"text", "image"}},
			{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125}, Input: []string{"text", "image"}},
			{ID: "gemini-3-flash-preview", Name: "Gemini 3 Flash Preview", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.5, Output: 3, CacheRead: 0.05}, Input: []string{"text", "image"}},
			{ID: "gemini-3.1-flash-lite", Name: "Gemini 3.1 Flash Lite", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.25, Output: 1.5, CacheRead: 0.025}, Input: []string{"text", "image"}},
			{ID: "gemini-3.1-pro-preview", Name: "Gemini 3.1 Pro Preview", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 2, Output: 12, CacheRead: 0.2}, Input: []string{"text", "image"}},
			{ID: "gemini-3.1-pro-preview-customtools", Name: "Gemini 3.1 Pro Preview Custom Tools", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 2, Output: 12, CacheRead: 0.2}, Input: []string{"text", "image"}},
			{ID: "gemini-3.5-flash", Name: "Gemini 3.5 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 1.5, Output: 9, CacheRead: 0.15}, Input: []string{"text", "image"}},
			{ID: "gemini-flash-latest", Name: "Gemini Flash Latest", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 1.5, Output: 9, CacheRead: 0.15}, Input: []string{"text", "image"}},
			{ID: "gemini-flash-lite-latest", Name: "Gemini Flash-Lite Latest", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.25, Output: 1.5, CacheRead: 0.025}, Input: []string{"text", "image"}},
		},
	},
	"xiaomi": {BaseURL: "https://api.xiaomimimo.com/v1", APIKey: "${XIAOMI_API_KEY}", API: "openai-chat", ThinkingFormat: "xiaomi", Models: []ModelConfig{
		{ID: "mimo-v2.5", Name: "MiMo-V2.5", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 0.4, Output: 2, CacheRead: 0.08}, Input: []string{"text", "image"}},
		{ID: "mimo-v2.5-pro", Name: "MiMo-V2.5-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 1, Output: 3, CacheRead: 0.2}, Input: []string{"text"}},
		{ID: "mimo-v2.5-pro-ultraspeed", Name: "MiMo-V2.5-Pro-UltraSpeed", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 1.305, Output: 2.61, CacheRead: 0.0108}, Input: []string{"text"}},
		{ID: "mimo-v2.6-pro", Name: "MiMo-V2.6-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "mimo-v2.6-flash", Name: "MiMo-V2.6-Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text", "image"}},
	}},
	"volcengine-agentplan": {Vendor: "volcengine-agentplan", BaseURL: "https://ark.cn-beijing.volces.com/api/plan/v3", APIKey: "${VOLCENGINE_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "ark-code-latest", Name: "Ark Code Latest", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "doubao-seed-2.1-turbo", Name: "Doubao Seed 2.1 Turbo", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "doubao-seed-evolving", Name: "Doubao Seed Evolving", Reasoning: true, ContextWindow: 1000000, MaxTokens: 100000, Input: []string{"text", "image"}},
		{ID: "doubao-seed-2-0-lite", Name: "Doubao Seed 2.0 Lite", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "doubao-seed-2-0-mini", Name: "Doubao Seed 2.0 Mini", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "glm-5.3", Name: "GLM 5.3", Reasoning: true, ContextWindow: 1000000, Input: []string{"text"}},
		{ID: "glm-5.3-flash", Name: "GLM 5.3 Flash", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "kimi-k2.7-code", Name: "Kimi K2.7 Code", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 100000, Input: []string{"text", "image"}},
		{ID: "deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 100000, Input: []string{"text", "image"}},
		{ID: "minimax-m3", Name: "MiniMax M3", Reasoning: true, ContextWindow: 1048576, MaxTokens: 100000, Input: []string{"text", "image"}},
		{ID: "minimax-m2.7", Name: "MiniMax M2.7", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "kimi-k2.6", Name: "Kimi K2.6", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text", "image"}},
		{ID: "kimi-k3", Name: "Kimi K3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 100000, Input: []string{"text", "image"}},
	}},
	"volcengine-codingplan": {Vendor: "volcengine-codingplan", BaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3", APIKey: "${VOLCENGINE_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "ark-code-latest", Name: "Ark Code Latest", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "doubao-seed-2.1-turbo", Name: "Doubao Seed 2.1 Turbo", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "doubao-seed-evolving", Name: "Doubao Seed Evolving", Reasoning: true, ContextWindow: 1000000, MaxTokens: 100000, Input: []string{"text", "image"}},
		{ID: "doubao-seed-2-0-lite", Name: "Doubao Seed 2.0 Lite", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "doubao-seed-2-0-mini", Name: "Doubao Seed 2.0 Mini", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "glm-5.3", Name: "GLM 5.3", Reasoning: true, ContextWindow: 1000000, Input: []string{"text"}},
		{ID: "glm-5.3-flash", Name: "GLM 5.3 Flash", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "kimi-k2.7-code", Name: "Kimi K2.7 Code", Reasoning: true, ContextWindow: 262144, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 100000, Input: []string{"text"}},
		{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 100000, Input: []string{"text", "image"}},
		{ID: "deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 100000, Input: []string{"text", "image"}},
		{ID: "minimax-m3", Name: "MiniMax M3", Reasoning: true, ContextWindow: 1048576, MaxTokens: 100000, Input: []string{"text", "image"}},
	}},
	"volcengine": {Vendor: "volcengine", BaseURL: "https://ark.cn-beijing.volces.com/api/v3", APIKey: "${VOLCENGINE_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "doubao-seed-2-1-turbo-260628", Name: "Doubao Seed 2.1 Turbo", ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text"}}, {ID: "doubao-seed-evolving", Name: "Doubao Seed Evolving", ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image"}}, {ID: "doubao-seed-2-1-pro-260628", Name: "Doubao Seed 2.1 Pro", ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image"}}, {ID: "glm-5.3", Name: "GLM 5.3", Reasoning: true, ContextWindow: 1000000, Input: []string{"text"}}, {ID: "glm-5.3-flash", Name: "GLM 5.3 Flash", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}}, {ID: "deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 262144, Input: []string{"text", "image"}}}},
	"openrouter": {Vendor: "openrouter", BaseURL: "https://openrouter.ai/api/v1", APIKey: "${OPENROUTER_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "anthropic/claude-sonnet-4.6", Name: "Claude Sonnet 4.6", Reasoning: true, ContextWindow: 1000000, MaxTokens: 64000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
		{ID: "anthropic/claude-opus-4.8", Name: "Claude Opus 4.8", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}, Input: []string{"text", "image"}},
		{ID: "anthropic/claude-sonnet-4.5", Name: "Claude Sonnet 4.5", Reasoning: true, ContextWindow: 1000000, MaxTokens: 64000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
		{ID: "anthropic/claude-haiku-4.5", Name: "Claude Haiku 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25}, Input: []string{"text", "image"}},
		{ID: "openai/gpt-5.5", Name: "GPT-5.5", Reasoning: true, ContextWindow: 1050000, MaxTokens: 128000, Cost: &CostConfig{Input: 5, Output: 30, CacheRead: 0.5}, Input: []string{"text", "image"}},
		{ID: "openai/gpt-5.5-pro", Name: "GPT-5.5 Pro", Reasoning: true, ContextWindow: 1050000, MaxTokens: 128000, Cost: &CostConfig{Input: 30, Output: 180, CacheWrite: 0}, Input: []string{"text", "image"}},
		{ID: "openai/gpt-5.4", Name: "GPT-5.4", Reasoning: true, ContextWindow: 1050000, MaxTokens: 128000, Cost: &CostConfig{Input: 2.5, Output: 15, CacheRead: 0.25}, Input: []string{"text", "image"}},
		{ID: "google/gemini-3.5-flash", Name: "Gemini 3.5 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 1.5, Output: 9, CacheRead: 0.15, CacheWrite: 0.083333}, Input: []string{"text", "image"}},
		{ID: "google/gemini-2.5-pro", Name: "Gemini 2.5 Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125, CacheWrite: 0.375}, Input: []string{"text", "image"}},
		{ID: "deepseek/deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.09, Output: 0.18, CacheRead: 0.02}, Input: []string{"text"}},
		{ID: "deepseek/deepseek-v4-pro", Name: "DeepSeek V4 Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 384000, Cost: &CostConfig{Input: 0.435, Output: 0.87, CacheRead: 0.003625}, Input: []string{"text"}},
		{ID: "qwen/qwen3.7-plus", Name: "Qwen 3.7 Plus", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Cost: &CostConfig{Input: 0.32, Output: 1.28, CacheRead: 0.064, CacheWrite: 0.4}, Input: []string{"text", "image"}},
		{ID: "moonshotai/kimi-k2.7-code", Name: "Kimi K2.7 Code", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.612, Output: 3.069, CacheRead: 0.1296}, Input: []string{"text"}},
		{ID: "minimax/minimax-m3", Name: "MiniMax M3", Reasoning: true, ContextWindow: 1048576, MaxTokens: 4096, Cost: &CostConfig{Input: 0.3, Output: 1.2, CacheRead: 0.06}, Input: []string{"text", "image"}},
		{ID: "meta-llama/llama-4-scout", Name: "Llama 4 Scout", ContextWindow: 10000000, MaxTokens: 16384, Cost: &CostConfig{Input: 0.1, Output: 0.3, CacheWrite: 0}, Input: []string{"text", "image"}},
		{ID: "z-ai/glm-5", Name: "GLM 5", Reasoning: true, ContextWindow: 200000, MaxTokens: 4096, Cost: &CostConfig{Input: 0.6, Output: 1.9, CacheRead: 0.119}, Input: []string{"text"}},
		{ID: "z-ai/glm-5.3", Name: "GLM 5.3", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 1.2, Output: 4.1, CacheRead: 0.2}, Input: []string{"text"}},
		{ID: "x-ai/grok-4.3", Name: "Grok 4.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 4096, Cost: &CostConfig{Input: 1.25, Output: 2.5, CacheRead: 0.2}, Input: []string{"text", "image"}},
	}},
	"openrouter-free-models": {Vendor: "openrouter", BaseURL: "https://openrouter.ai/api/v1", APIKey: "${OPENROUTER_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "google/gemma-4-26b-a4b-it", Name: "Google: Gemma 4 26B A4B (free)", Reasoning: true, ContextWindow: 262144, MaxTokens: 32768, Cost: &CostConfig{Input: 0, Output: 0}, Input: []string{"text", "image", "video"}},
		{ID: "google/gemma-4-31b-it", Name: "Google: Gemma 4 31B (free)", Reasoning: true, ContextWindow: 262144, MaxTokens: 32768, Cost: &CostConfig{Input: 0, Output: 0}, Input: []string{"text", "image", "video"}},
		{ID: "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning", Name: "NVIDIA: Nemotron 3 Nano Omni (free)", Reasoning: true, ContextWindow: 256000, MaxTokens: 65536, Cost: &CostConfig{Input: 0, Output: 0}, Input: []string{"text", "image", "video"}},
		{ID: "nvidia/nemotron-3-super-120b-a12b", Name: "NVIDIA: Nemotron 3 Super (free)", Reasoning: true, ContextWindow: 1000000, MaxTokens: 262144, Cost: &CostConfig{Input: 0, Output: 0}, Input: []string{"text"}},
		{ID: "nvidia/nemotron-3-ultra-550b-a55b", Name: "NVIDIA: Nemotron 3 Ultra (free)", Reasoning: true, ContextWindow: 1000000, MaxTokens: 10000, Cost: &CostConfig{Input: 0, Output: 0}, Input: []string{"text"}},
		{ID: "nvidia/nemotron-3.5-content-safety", Name: "NVIDIA: Nemotron 3.5 Content Safety (free)", Reasoning: true, ContextWindow: 128000, MaxTokens: 8192, Cost: &CostConfig{Input: 0, Output: 0}, Input: []string{"text", "image"}},
		{ID: "nvidia/nemotron-nano-12b-v2-vl", Name: "NVIDIA: Nemotron Nano 12B 2 VL (free)", Reasoning: true, ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 0, Output: 0}, Input: []string{"text", "image", "video"}},
		{ID: "openai/gpt-oss-20b", Name: "OpenAI: gpt-oss-20b (free)", Reasoning: true, ContextWindow: 131072, MaxTokens: 32768, Cost: &CostConfig{Input: 0, Output: 0}, Input: []string{"text"}},
		{ID: "poolside/laguna-m.1", Name: "Poolside: Laguna M.1 (free)", Reasoning: true, ContextWindow: 262144, MaxTokens: 32768, Cost: &CostConfig{Input: 0, Output: 0}, Input: []string{"text"}},
		{ID: "poolside/laguna-xs-2.1", Name: "Poolside: Laguna XS 2.1 (free)", Reasoning: true, ContextWindow: 262144, MaxTokens: 32768, Cost: &CostConfig{Input: 0, Output: 0}, Input: []string{"text"}},
		{ID: "tencent/hy3", Name: "Tencent: Hy3 (free)", Reasoning: true, ContextWindow: 262144, MaxTokens: 38106, Cost: &CostConfig{Input: 0, Output: 0}, Input: []string{"text"}},
	}},
	"minimax": {Vendor: "minimax", BaseURL: "https://api.minimaxi.com/anthropic", APIKey: "${MINIMAX_API_KEY}", API: "anthropic-messages", Models: []ModelConfig{{ID: "MiniMax-M3", Name: "MiniMax-M3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Input: []string{"text", "image", "video"}}, {ID: "MiniMax-M2.7", Name: "MiniMax-M2.7", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Input: []string{"text"}}, {ID: "MiniMax-M2.7-highspeed", Name: "MiniMax-M2.7-highspeed", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Input: []string{"text"}}, {ID: "MiniMax-M2.5", Name: "MiniMax-M2.5", Reasoning: true, ContextWindow: 196608, MaxTokens: 131072, Input: []string{"text"}}, {ID: "MiniMax-M2.5-highspeed", Name: "MiniMax-M2.5-highspeed", Reasoning: true, ContextWindow: 196608, MaxTokens: 131072, Input: []string{"text"}}}},
	"zai":     {Vendor: "zai", BaseURL: "https://api.z.ai/api/coding/paas/v4", APIKey: "${ZAI_API_KEY}", API: "openai-chat", ThinkingFormat: "zai", Models: []ModelConfig{{ID: "glm-4.5-air", Name: "GLM-4.5-Air", Reasoning: true, ContextWindow: 131072, MaxTokens: 98304, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-4.7", Name: "GLM-4.7", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-5-turbo", Name: "GLM-5-Turbo", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-5.1", Name: "GLM-5.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-5.3", Name: "GLM-5.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-5.3-flash", Name: "GLM-5.3-Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text", "image"}}, {ID: "glm-5v-turbo", Name: "GLM-5V-Turbo", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text", "image"}}}},
	"modelscope": {BaseURL: "https://api-inference.modelscope.cn/v1", APIKey: "${MODELSCOPE_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "deepseek-ai/DeepSeek-V4-Flash-0731", Name: "DeepSeek-V4-Flash-0731", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}},
		{ID: "deepseek-ai/DeepSeek-V4-Pro", Name: "DeepSeek-V4-Pro", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}},
		{ID: "deepseek-ai/DeepSeek-V4-Pro-0813", Name: "DeepSeek-V4-Pro-0813", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}},
		{ID: "MedAIBase/AntAngelMed", Name: "AntAngelMed", Reasoning: false, ContextWindow: 131072, MaxTokens: 16384, Input: []string{"text"}},
		{ID: "meituan-longcat/LongCat-Flash-Lite", Name: "LongCat-Flash-Lite", Reasoning: false, ContextWindow: 262144, MaxTokens: 32768, Input: []string{"text"}},
		{ID: "MiniMax/MiniMax-M1-80k", Name: "MiniMax-M1-80k", Reasoning: true, ContextWindow: 1000000, MaxTokens: 80000, Input: []string{"text"}},
		{ID: "MiniMax/MiniMax-M3", Name: "MiniMax-M3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Input: []string{"text", "image", "video"}},
		{ID: "mistralai/Mistral-Large-Instruct-2407", Name: "Mistral-Large-Instruct-2407", Reasoning: false, ContextWindow: 131072, MaxTokens: 32768, Input: []string{"text"}},
		{ID: "MusePublic/Qwen-Image-Edit", Name: "Qwen-Image-Edit", Reasoning: false, ContextWindow: 32768, MaxTokens: 16384, Input: []string{"text", "image"}},
		{ID: "opencompass/CompassJudger-1-32B-Instruct", Name: "CompassJudger-1-32B-Instruct", Reasoning: false, ContextWindow: 16384, MaxTokens: 4096, Input: []string{"text"}},
		{ID: "OpenGVLab/InternVL3_5-241B-A28B", Name: "InternVL3.5-241B-A28B", Reasoning: false, ContextWindow: 65536, MaxTokens: 16384, Input: []string{"text", "image", "video"}},
		{ID: "PaddlePaddle/ERNIE-4.5-0.3B-PT", Name: "ERNIE-4.5-0.3B-PT", Reasoning: false, ContextWindow: 131072, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "PaddlePaddle/ERNIE-4.5-21B-A3B-PT", Name: "ERNIE-4.5-21B-A3B-PT", Reasoning: false, ContextWindow: 131072, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "PaddlePaddle/ERNIE-4.5-300B-A47B-PT", Name: "ERNIE-4.5-300B-A47B-PT", Reasoning: false, ContextWindow: 131072, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "PaddlePaddle/ERNIE-4.5-VL-28B-A3B-PT", Name: "ERNIE-4.5-VL-28B-A3B-PT", Reasoning: false, ContextWindow: 131072, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "Qwen/Qwen-Image-Edit", Name: "Qwen-Image-Edit", Reasoning: false, ContextWindow: 32768, MaxTokens: 16384, Input: []string{"text", "image"}},
		{ID: "Qwen/Qwen3-14B", Name: "Qwen3-14B", Reasoning: false, ContextWindow: 131072, MaxTokens: 38912, Input: []string{"text"}},
		{ID: "Qwen/Qwen3-235B-A22B", Name: "Qwen3-235B-A22B", Reasoning: false, ContextWindow: 131072, MaxTokens: 38912, Input: []string{"text"}},
		{ID: "Qwen/Qwen3-235B-A22B-Instruct-2507", Name: "Qwen3-235B-A22B-Instruct-2507", Reasoning: false, ContextWindow: 262144, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "Qwen/Qwen3-235B-A22B-Thinking-2507", Name: "Qwen3-235B-A22B-Thinking-2507", Reasoning: true, ContextWindow: 262144, MaxTokens: 81920, Input: []string{"text"}},
		{ID: "Qwen/Qwen3-30B-A3B", Name: "Qwen3-30B-A3B", Reasoning: false, ContextWindow: 131072, MaxTokens: 38912, Input: []string{"text"}},
		{ID: "Qwen/Qwen3-30B-A3B-Thinking-2507", Name: "Qwen3-30B-A3B-Thinking-2507", Reasoning: true, ContextWindow: 262144, MaxTokens: 81920, Input: []string{"text"}},
		{ID: "Qwen/Qwen3-4B", Name: "Qwen3-4B", Reasoning: false, ContextWindow: 131072, MaxTokens: 38912, Input: []string{"text"}},
		{ID: "Qwen/Qwen3-8B", Name: "Qwen3-8B", Reasoning: false, ContextWindow: 131072, MaxTokens: 38912, Input: []string{"text"}},
		{ID: "Qwen/Qwen3-Coder-30B-A3B-Instruct", Name: "Qwen3-Coder-30B-A3B-Instruct", Reasoning: false, ContextWindow: 262144, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "Qwen/Qwen3-Next-80B-A3B-Instruct", Name: "Qwen3-Next-80B-A3B-Instruct", Reasoning: false, ContextWindow: 262144, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "Qwen/Qwen3-Next-80B-A3B-Thinking", Name: "Qwen3-Next-80B-A3B-Thinking", Reasoning: true, ContextWindow: 262144, MaxTokens: 81920, Input: []string{"text"}},
		{ID: "Qwen/Qwen3-VL-235B-A22B-Instruct", Name: "Qwen3-VL-235B-A22B-Instruct", Reasoning: false, ContextWindow: 262144, MaxTokens: 32768, Input: []string{"text", "image", "video"}},
		{ID: "Qwen/Qwen3-VL-8B-Instruct", Name: "Qwen3-VL-8B-Instruct", Reasoning: false, ContextWindow: 262144, MaxTokens: 32768, Input: []string{"text", "image", "video"}},
		{ID: "Qwen/Qwen3-VL-8B-Thinking", Name: "Qwen3-VL-8B-Thinking", Reasoning: true, ContextWindow: 262144, MaxTokens: 40960, Input: []string{"text", "image", "video"}},
		{ID: "Qwen/Qwen3.5-122B-A10B", Name: "Qwen3.5-122B-A10B", Reasoning: true, ContextWindow: 262144, MaxTokens: 81920, Input: []string{"text", "image", "video"}},
		{ID: "Qwen/Qwen3.5-27B", Name: "Qwen3.5-27B", Reasoning: true, ContextWindow: 262144, MaxTokens: 81920, Input: []string{"text", "image", "video"}},
		{ID: "Qwen/Qwen3.5-35B-A3B", Name: "Qwen3.5-35B-A3B", Reasoning: true, ContextWindow: 262144, MaxTokens: 81920, Input: []string{"text", "image", "video"}},
		{ID: "Qwen/Qwen3.5-397B-A17B", Name: "Qwen3.5-397B-A17B", Reasoning: true, ContextWindow: 1000000, MaxTokens: 130000, Input: []string{"text"}},
		{ID: "Qwen/Qwen3.8-27B", Name: "Qwen3.8-27B", Reasoning: true, ContextWindow: 262144, MaxTokens: 131072, Input: []string{"text", "image", "video"}},
		{ID: "Shanghai_AI_Laboratory/Intern-S1", Name: "Intern-S1", Reasoning: true, ContextWindow: 131072, MaxTokens: 32768, Input: []string{"text", "image", "video"}},
		{ID: "Shanghai_AI_Laboratory/Intern-S1-mini", Name: "Intern-S1-mini", Reasoning: true, ContextWindow: 131072, MaxTokens: 32768, Input: []string{"text", "image", "video"}},
		{ID: "Shanghai_AI_Laboratory/Intern-S2-Preview", Name: "Intern-S2-Preview", Reasoning: true, ContextWindow: 131072, MaxTokens: 32768, Input: []string{"text", "image"}},
		{ID: "stepfun-ai/Step-3.5-Flash", Name: "Step-3.5-Flash", Reasoning: true, ContextWindow: 262144, MaxTokens: 32768, Input: []string{"text"}},
		{ID: "stepfun-ai/Step-3.7-Flash", Name: "Step-3.7-Flash", Reasoning: true, ContextWindow: 262144, MaxTokens: 32768, Input: []string{"text", "image"}},
		{ID: "Tencent-Hunyuan/Hy3", Name: "Hy3", Reasoning: true, ContextWindow: 262144, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "XGenerationLab/XiYanSQL-QwenCoder-32B-2412", Name: "XiYanSQL-QwenCoder-32B-2412", Reasoning: false, ContextWindow: 32768, MaxTokens: 16384, Input: []string{"text"}},
		{ID: "XGenerationLab/XiYanSQL-QwenCoder-32B-2504", Name: "XiYanSQL-QwenCoder-32B-2504", Reasoning: false, ContextWindow: 32768, MaxTokens: 16384, Input: []string{"text"}},
		{ID: "ZhipuAI/GLM-4.7-Flash", Name: "GLM-4.7-Flash", Reasoning: true, ContextWindow: 262144, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "ZhipuAI/GLM-5.2", Name: "GLM-5.2", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text"}},
	}},
	"alibaba-coding-plan": {Vendor: "bailian", BaseURL: "https://coding.dashscope.aliyuncs.com/v1", APIKey: "${BAILIAN_CODING_PLAN_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "qwen3.5-plus", Name: "Qwen3.5 Plus", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image", "video"}}, {ID: "qwen3.6-plus", Name: "Qwen3.6 Plus", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image", "video"}}, {ID: "qwen3.7-plus", Name: "Qwen3.7 Plus", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}}, {ID: "glm-5", Name: "GLM-5", Reasoning: true, ContextWindow: 200000, MaxTokens: 32768, Input: []string{"text"}}, {ID: "kimi-k2.5", Name: "Kimi-K2.5", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image", "video"}}, {ID: "MiniMax-M2.5", Name: "MiniMax-M2.5", Reasoning: true, ContextWindow: 196608, MaxTokens: 131072, Input: []string{"text"}}, {ID: "qwen3-coder-plus", Name: "Qwen3 Coder Plus", ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text"}}, {ID: "qwen3-coder-next", Name: "Qwen3 Coder Next", ContextWindow: 262144, MaxTokens: 65536, Input: []string{"text"}}, {ID: "qwen3-max-2026-01-23", Name: "Qwen3 Max", Reasoning: true, ContextWindow: 262144, MaxTokens: 65536, Input: []string{"text"}}, {ID: "glm-4.7", Name: "GLM-4.7", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text"}}}},
	"alibaba-token-plan":  {Vendor: "bailian", BaseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", APIKey: "${BAILIAN_TOKEN_PLAN_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "qwen3.8-max-preview", Name: "Qwen3.8 Max Preview", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text"}}, {ID: "qwen3.6-plus", Name: "Qwen3.6 Plus", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}}, {ID: "qwen3.7-max", Name: "Qwen3.7 Max", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text"}}, {ID: "qwen3.7-plus", Name: "Qwen3.7 Plus", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}}, {ID: "qwen3.6-flash", Name: "Qwen3.6 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}}, {ID: "deepseek-v4-pro", Name: "DeepSeek-V4-Pro", ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}}, {ID: "deepseek-v4-flash", Name: "DeepSeek-V4-Flash", ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}}, {ID: "deepseek-v3.2", Name: "DeepSeek-V3.2", Reasoning: true, ContextWindow: 131072, MaxTokens: 65536, Input: []string{"text"}}, {ID: "kimi-k2.6", Name: "Kimi-K2.6", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image", "video"}}, {ID: "kimi-k2.5", Name: "Kimi-K2.5", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image", "video"}}, {ID: "glm-5.3", Name: "GLM-5.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text"}}, {ID: "glm-5.1", Name: "GLM-5.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text"}}, {ID: "glm-5", Name: "GLM-5", Reasoning: true, ContextWindow: 200000, MaxTokens: 32768, Input: []string{"text"}}, {ID: "MiniMax-M2.5", Name: "MiniMax-M2.5", ContextWindow: 196608, MaxTokens: 131072, Input: []string{"text"}}}},
	"gitee": {Vendor: "gitee", BaseURL: "https://ai.gitee.com/v1", APIKey: "${GITEE_API_KEY}", API: "openai-chat", MaxImagesPerRequest: 5, Models: []ModelConfig{
		{ID: "auto", Name: "Auto", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "glm-5", Name: "GLM-5", Reasoning: true, ContextWindow: 200000, MaxTokens: 32768, Input: []string{"text"}},
		{ID: "glm-5.1", Name: "GLM-5.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "glm-5.3", Name: "GLM-5.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "glm-5.3-flash", Name: "GLM-5.3-Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "ernie-5.0-thinking", Name: "ERNIE-5.0-Thinking", Reasoning: true, ContextWindow: 131072, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "qwen3.5-flash", Name: "Qwen3.5 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "qwen3.6-flash", Name: "Qwen3.6 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "qwen3.6-plus", Name: "Qwen3.6 Plus", Reasoning: true, ContextWindow: 65536, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "qwen3.7-plus", Name: "Qwen3.7 Plus", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "step-3.7-flash", Name: "Step 3.7 Flash", ContextWindow: 262144, MaxTokens: 16384, Input: []string{"text", "image"}},
		{ID: "qwen3.7-max", Name: "Qwen3.7 Max", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "qwen3.8-max", Name: "Qwen3.8 Max", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "qwen3.8-max-0902", Name: "Qwen3.8 Max 0902", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "qwen3.8-flash", Name: "Qwen3.8 Flash", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "qwen3.8-27b", Name: "Qwen3.8-27B", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image", "video"}},
		{ID: "qwen3.8-omni-flash", Name: "Qwen3.8-Omni-Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text", "image", "audio", "video"}},
		{ID: "deepseek-v4-flash", Name: "DeepSeek-V4-Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}},
		{ID: "deepseek-v4-flash-0731", Name: "DeepSeek-V4-Flash-0731", Reasoning: true, ContextWindow: 1000000, Input: []string{"text"}},
		{ID: "deepseek-v4.1-flash", Name: "DeepSeek-V4.1-Flash", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "deepseek-v4-pro", Name: "DeepSeek-V4-Pro", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}},
		{ID: "deepseek-v4-pro-0813", Name: "DeepSeek-V4-Pro-0813", Reasoning: true, ContextWindow: 1000000, Input: []string{"text"}},
		{ID: "kimi-k2.5", Name: "Kimi-K2.5", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image", "video"}},
		{ID: "kimi-k2.6", Name: "Kimi-K2.6", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image", "video"}},
		{ID: "kimi-k2.7-code", Name: "Kimi-K2.7-Code", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text"}},
		{ID: "kimi-k3", Name: "Kimi-K3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 262144, Input: []string{"text", "image"}},
		{ID: "minimax-m2.7", Name: "MiniMax-M2.7", Reasoning: true, ContextWindow: 262144, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "minimax-m3", Name: "MiniMax-M3", Reasoning: true, ContextWindow: 1048576, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "mimo-v2.5-pro", Name: "MiMo-V2.5-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "gemma-4-26b-a4b-it", Name: "Gemma-4-26B-A4B-IT", Reasoning: true, ContextWindow: 131072, MaxTokens: 32768, Input: []string{"text", "image"}},
	}},
	"huawei": {Vendor: "huawei", BaseURL: "https://api.modelarts-maas.com/openai/v1", APIKey: "${HUAWEI_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "openpangu-2.0-flash", Name: "openPangu-2.0-Flash", Reasoning: true, ContextWindow: 524288, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "glm-5.3", Name: "GLM-5.3", Reasoning: true, ContextWindow: 202752, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "glm-5.1", Name: "GLM-5.1", Reasoning: true, ContextWindow: 202752, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "kimi-k2.6", Name: "Kimi-K2.6", Reasoning: true, ContextWindow: 262144, MaxTokens: 98304, Input: []string{"text", "image"}},
		{ID: "glm-5", Name: "GLM-5", Reasoning: true, ContextWindow: 202752, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "deepseek-v4-pro", Name: "DeepSeek-V4-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "deepseek-v4-flash", Name: "DeepSeek-V4-Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "qwen3-235b-a22b", Name: "Qwen3-235B-A22B", Reasoning: true, ContextWindow: 131072, MaxTokens: 32768, Input: []string{"text", "image"}},
	}},
	"huawei-plan": {Vendor: "huawei-plan", BaseURL: "https://api.modelarts-maas.com/plan/v2", APIKey: "${HUAWEI_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "glm-5", Name: "GLM-5", Reasoning: true, ContextWindow: 202752, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "glm-5.1", Name: "GLM-5.1", Reasoning: true, ContextWindow: 202752, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "kimi-k2.6", Name: "Kimi-K2.6", Reasoning: true, ContextWindow: 262144, MaxTokens: 98304, Input: []string{"text", "image"}},
		{ID: "deepseek-v3.2", Name: "DeepSeek-V3.2", Reasoning: true, ContextWindow: 131072, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "deepseek-v4-flash", Name: "DeepSeek-V4-Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text"}},
	}},
	"mthreads-plan": {Vendor: "mthreads-plan", BaseURL: "https://coding-plan-endpoint.kuaecloud.net/v1", APIKey: "${MTHREADS_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "glm-4.7", Name: "GLM-4.7", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text", "image"}},
	}},
	"ctyun-plan": {Vendor: "ctyun-plan", BaseURL: "https://wishub-x6.ctyun.cn/coding/v1", APIKey: "${CTYUN_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "glm-5-turbo", Name: "GLM-5-Turbo", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "glm-5-pro", Name: "GLM-5-Pro", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "deepseek-v3.2-pro", Name: "DeepSeek-V3.2-Pro", Reasoning: true, ContextWindow: 131072, MaxTokens: 65536, Input: []string{"text"}},
	}},
	"jd-plan": {Vendor: "jd-plan", BaseURL: "https://agentrs.jd.com/api/saas/openai-u/v1", APIKey: "${JD_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "glm-5", Name: "GLM-5", Reasoning: true, ContextWindow: 200000, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "glm-5.1", Name: "GLM-5.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "glm-5.3", Name: "GLM-5.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "qwen3.7-plus", Name: "Qwen3.7 Plus", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "qwen3.7-max", Name: "Qwen3.7 Max", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "deepseek-v4-flash", Name: "DeepSeek-V4-Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "deepseek-v4-pro", Name: "DeepSeek-V4-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "kimi-k2.6", Name: "Kimi-K2.6", Reasoning: true, ContextWindow: 262144, MaxTokens: 98304, Input: []string{"text", "image"}},
		{ID: "minimax-m2.7", Name: "MiniMax-M2.7", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "joyai-llm-flash", Name: "JoyAI-LLM-Flash", ContextWindow: 128000, MaxTokens: 32768, Input: []string{"text"}},
	}},
	"moark": {Vendor: "gitee", BaseURL: "https://api.moark.com/v1", APIKey: "${MOARK_API_KEY}", API: "openai-chat", MaxImagesPerRequest: 5, Models: []ModelConfig{
		{ID: "auto", Name: "Auto", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "glm-5", Name: "GLM-5", Reasoning: true, ContextWindow: 200000, MaxTokens: 32768, Input: []string{"text"}},
		{ID: "glm-5.1", Name: "GLM-5.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "glm-5.3", Name: "GLM-5.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "glm-5.3-flash", Name: "GLM-5.3-Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "ernie-5.0-thinking", Name: "ERNIE-5.0-Thinking", Reasoning: true, ContextWindow: 131072, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "qwen3.5-flash", Name: "Qwen3.5 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "qwen3.6-flash", Name: "Qwen3.6 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "qwen3.6-plus", Name: "Qwen3.6 Plus", Reasoning: true, ContextWindow: 65536, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "qwen3.7-plus", Name: "Qwen3.7 Plus", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "step-3.7-flash", Name: "Step 3.7 Flash", ContextWindow: 262144, MaxTokens: 16384, Input: []string{"text", "image"}},
		{ID: "qwen3.7-max", Name: "Qwen3.7 Max", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "qwen3.8-max", Name: "Qwen3.8 Max", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "qwen3.8-max-0902", Name: "Qwen3.8 Max 0902", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "qwen3.8-flash", Name: "Qwen3.8 Flash", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "qwen3.8-27b", Name: "Qwen3.8-27B", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image", "video"}},
		{ID: "qwen3.8-omni-flash", Name: "Qwen3.8-Omni-Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text", "image", "audio", "video"}},
		{ID: "deepseek-v4-flash", Name: "DeepSeek-V4-Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}},
		{ID: "deepseek-v4-flash-0731", Name: "DeepSeek-V4-Flash-0731", Reasoning: true, ContextWindow: 1000000, Input: []string{"text"}},
		{ID: "deepseek-v4.1-flash", Name: "DeepSeek-V4.1-Flash", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "deepseek-v4-pro", Name: "DeepSeek-V4-Pro", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}},
		{ID: "deepseek-v4-pro-0813", Name: "DeepSeek-V4-Pro-0813", Reasoning: true, ContextWindow: 1000000, Input: []string{"text"}},
		{ID: "kimi-k2.5", Name: "Kimi-K2.5", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image", "video"}},
		{ID: "kimi-k2.6", Name: "Kimi-K2.6", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image", "video"}},
		{ID: "kimi-k2.7-code", Name: "Kimi-K2.7-Code", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text"}},
		{ID: "kimi-k3", Name: "Kimi-K3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 262144, Input: []string{"text", "image"}},
		{ID: "minimax-m2.7", Name: "MiniMax-M2.7", Reasoning: true, ContextWindow: 262144, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "minimax-m3", Name: "MiniMax-M3", Reasoning: true, ContextWindow: 1048576, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "mimo-v2.5-pro", Name: "MiMo-V2.5-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "gemma-4-26b-a4b-it", Name: "Gemma-4-26B-A4B-IT", Reasoning: true, ContextWindow: 131072, MaxTokens: 32768, Input: []string{"text", "image"}},
	}},
	"alibaba-standard": {Vendor: "bailian", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", APIKey: "${DASHSCOPE_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "qwen3.6-plus", Name: "Qwen3.6 Plus", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}}, {ID: "qwen3.7-plus", Name: "Qwen3.7 Plus", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text", "image"}}, {ID: "qwen3.7-max", Name: "Qwen3.7 Max", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text"}}, {ID: "glm-5.1", Name: "GLM-5.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text"}}, {ID: "deepseek-v4-pro", Name: "DeepSeek-V4-Pro", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text", "image", "video"}}, {ID: "deepseek-v4-flash", Name: "DeepSeek-V4-Flash", ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}}}},
	"ant-ling":         {BaseURL: "https://api.ant-ling.com/v1", APIKey: "${ANT_LING_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "Ling-2.6-1T", Name: "Ling 2.6 1T", ContextWindow: 262144, MaxTokens: 65536, Cost: &CostConfig{Input: 0.06, Output: 0.25, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "Ling-2.6-flash", Name: "Ling 2.6 Flash", ContextWindow: 262144, MaxTokens: 65536, Cost: &CostConfig{Input: 0.01, Output: 0.02, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "Ring-2.6-1T", Name: "Ring 2.6 1T", Reasoning: true, ContextWindow: 262144, MaxTokens: 65536, Cost: &CostConfig{Input: 0.06, Output: 0.25, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}}},
	"agnes":            {Vendor: "agnes", BaseURL: "https://apihub.agnes-ai.com/v1", APIKey: "${AGNES_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "agnes-2.5-flash", Name: "Agnes 2.5 Flash", Reasoning: true, ContextWindow: 200000, Input: []string{"text", "image"}}, {ID: "agnes-2.5-pro", Name: "Agnes 2.5 Pro", Reasoning: true, ContextWindow: 256000, Input: []string{"text", "image"}}, {ID: "agnes-3.0-flash", Name: "Agnes 3.0 Flash", Reasoning: true, ContextWindow: 512000, MaxTokens: 65535, Input: []string{"text", "image"}}}},
	"agnes-cn":         {Vendor: "agnes", BaseURL: "https://api.agnes-ai.cn/v1", APIKey: "${AGNES_CN_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "agnes-2.5-flash", Name: "Agnes 2.5 Flash", Reasoning: true, ContextWindow: 200000, Input: []string{"text", "image"}}, {ID: "agnes-2.5-pro", Name: "Agnes 2.5 Pro", Reasoning: true, ContextWindow: 256000, Input: []string{"text", "image"}}, {ID: "agnes-3.0-flash", Name: "Agnes 3.0 Flash", Reasoning: true, ContextWindow: 512000, MaxTokens: 65535, Input: []string{"text", "image"}}}},
	"cerebras":         {BaseURL: "https://api.cerebras.ai/v1", APIKey: "${CEREBRAS_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "gpt-oss-120b", Name: "GPT OSS 120B", Reasoning: true, ContextWindow: 131072, MaxTokens: 40960, Cost: &CostConfig{Input: 0.35, Output: 0.75, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "zai-glm-4.7", Name: "Z.AI GLM-4.7", Reasoning: true, ContextWindow: 131072, MaxTokens: 40960, Cost: &CostConfig{Input: 2.25, Output: 2.75, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}}},
	"groq": {BaseURL: "https://api.groq.com/openai/v1", APIKey: "${GROQ_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "llama-3.1-8b-instant", Name: "Llama 3.1 8B", ContextWindow: 131072, MaxTokens: 131072, Cost: &CostConfig{Input: 0.05, Output: 0.08}, Input: []string{"text"}},
		{ID: "llama-3.3-70b-versatile", Name: "Llama 3.3 70B", ContextWindow: 131072, MaxTokens: 32768, Cost: &CostConfig{Input: 0.59, Output: 0.79}, Input: []string{"text"}},
		{ID: "meta-llama/llama-4-scout-17b-16e-instruct", Name: "Llama 4 Scout 17B 16E", ContextWindow: 131072, MaxTokens: 8192, Cost: &CostConfig{Input: 0.11, Output: 0.34}, Input: []string{"text", "image"}},
		{ID: "openai/gpt-oss-120b", Name: "GPT OSS 120B", Reasoning: true, ContextWindow: 131072, MaxTokens: 65536, Cost: &CostConfig{Input: 0.15, Output: 0.6, CacheRead: 0.075}, Input: []string{"text"}},
		{ID: "openai/gpt-oss-20b", Name: "GPT OSS 20B", Reasoning: true, ContextWindow: 131072, MaxTokens: 65536, Cost: &CostConfig{Input: 0.075, Output: 0.3, CacheRead: 0.0375}, Input: []string{"text"}},
		{ID: "openai/gpt-oss-safeguard-20b", Name: "Safety GPT OSS 20B", Reasoning: true, ContextWindow: 131072, MaxTokens: 65536, Cost: &CostConfig{Input: 0.075, Output: 0.3, CacheRead: 0.037}, Input: []string{"text"}},
		{ID: "qwen/qwen3-32b", Name: "Qwen3-32B", Reasoning: true, ContextWindow: 131072, MaxTokens: 40960, Cost: &CostConfig{Input: 0.29, Output: 0.59}, Input: []string{"text"}},
	}},
	"moonshotai":    {BaseURL: "https://api.moonshot.ai/v1", APIKey: "${MOONSHOTAI_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "kimi-k2-0711-preview", Name: "Kimi K2 0711", ContextWindow: 131072, MaxTokens: 16384, Cost: &CostConfig{Input: 0.6, Output: 2.5, CacheRead: 0.15, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2-0905-preview", Name: "Kimi K2 0905", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.6, Output: 2.5, CacheRead: 0.15, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2-thinking", Name: "Kimi K2 Thinking", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.6, Output: 2.5, CacheRead: 0.15, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2-thinking-turbo", Name: "Kimi K2 Thinking Turbo", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 1.15, Output: 8, CacheRead: 0.15, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2-turbo-preview", Name: "Kimi K2 Turbo", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 2.4, Output: 10, CacheRead: 0.6, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2.7-code", Name: "Kimi K2.7 Code", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.95, Output: 4, CacheRead: 0.19, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2.7-code-highspeed", Name: "Kimi K2.7 Code HighSpeed", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 1.9, Output: 8, CacheRead: 0.38, CacheWrite: 0}, Input: []string{"text"}}}},
	"moonshotai-cn": {BaseURL: "https://api.moonshot.cn/v1", APIKey: "${MOONSHOTAI_CN_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "kimi-k2-0711-preview", Name: "Kimi K2 0711", ContextWindow: 131072, MaxTokens: 16384, Cost: &CostConfig{Input: 0.6, Output: 2.5, CacheRead: 0.15, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2-0905-preview", Name: "Kimi K2 0905", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.6, Output: 2.5, CacheRead: 0.15, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2-thinking", Name: "Kimi K2 Thinking", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.6, Output: 2.5, CacheRead: 0.15, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2-thinking-turbo", Name: "Kimi K2 Thinking Turbo", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 1.15, Output: 8, CacheRead: 0.15, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2-turbo-preview", Name: "Kimi K2 Turbo", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 2.4, Output: 10, CacheRead: 0.6, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2.7-code", Name: "Kimi K2.7 Code", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.95, Output: 4, CacheRead: 0.19, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2.7-code-highspeed", Name: "Kimi K2.7 Code HighSpeed", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 1.9, Output: 8, CacheRead: 0.38, CacheWrite: 0}, Input: []string{"text"}}}},
	"nvidia":        {BaseURL: "https://integrate.api.nvidia.com/v1", APIKey: "${NVIDIA_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "meta/llama-3.1-70b-instruct", Name: "Llama 3.1 70b Instruct", ContextWindow: 128000, MaxTokens: 4096, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "meta/llama-3.1-8b-instruct", Name: "Llama 3.1 8B Instruct", ContextWindow: 16000, MaxTokens: 4096, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "meta/llama-3.2-11b-vision-instruct", Name: "Llama 3.2 11b Vision Instruct", ContextWindow: 128000, MaxTokens: 4096, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text", "image"}}, {ID: "meta/llama-3.2-90b-vision-instruct", Name: "Llama-3.2-90B-Vision-Instruct", ContextWindow: 128000, MaxTokens: 8192, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text", "image"}}, {ID: "meta/llama-3.3-70b-instruct", Name: "Llama 3.3 70b Instruct", ContextWindow: 128000, MaxTokens: 4096, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}}},
	"together":      {BaseURL: "https://api.together.ai/v1", APIKey: "${TOGETHER_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "MiniMaxAI/MiniMax-M2.7", Name: "MiniMax-M2.7", Reasoning: true, ContextWindow: 202752, MaxTokens: 131072, Cost: &CostConfig{Input: 0.3, Output: 1.2, CacheRead: 0.06, CacheWrite: 0}, Input: []string{"text"}}, {ID: "MiniMaxAI/MiniMax-M3", Name: "MiniMax-M3", Reasoning: true, ContextWindow: 524288, MaxTokens: 250000, Cost: &CostConfig{Input: 0.3, Output: 1.2, CacheRead: 0.06, CacheWrite: 0}, Input: []string{"text", "image"}}, {ID: "Qwen/Qwen2.5-7B-Instruct-Turbo", Name: "Qwen 2.5 7B Instruct Turbo", ContextWindow: 32768, MaxTokens: 32768, Cost: &CostConfig{Input: 0.3, Output: 0.3, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "Qwen/Qwen3-235B-A22B-Instruct-2507-tput", Name: "Qwen3 235B A22B Instruct 2507 FP8", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.2, Output: 0.6, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "Qwen/Qwen3.5-397B-A17B", Name: "Qwen3.5 397B A17B", Reasoning: true, ContextWindow: 262144, MaxTokens: 130000, Cost: &CostConfig{Input: 0.6, Output: 3.6, CacheRead: 0, CacheWrite: 0}, Input: []string{"text", "image"}}}},
	"xai": {BaseURL: "https://api.x.ai/v1", APIKey: "${XAI_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "grok-3", Name: "Grok 3", ContextWindow: 131072, MaxTokens: 8192, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.75}, Input: []string{"text"}},
		{ID: "grok-3-fast", Name: "Grok 3 Fast", ContextWindow: 131072, MaxTokens: 8192, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 1.25}, Input: []string{"text"}},
		{ID: "grok-4.20-0309-non-reasoning", Name: "Grok 4.20 (Non-Reasoning)", ContextWindow: 1000000, MaxTokens: 30000, Cost: &CostConfig{Input: 1.25, Output: 2.5, CacheRead: 0.2}, Input: []string{"text", "image"}},
		{ID: "grok-4.20-0309-reasoning", Name: "Grok 4.20 (Reasoning)", Reasoning: true, ContextWindow: 1000000, MaxTokens: 30000, Cost: &CostConfig{Input: 1.25, Output: 2.5, CacheRead: 0.2}, Input: []string{"text", "image"}},
		{ID: "grok-4.3", Name: "Grok 4.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 30000, Cost: &CostConfig{Input: 1.25, Output: 2.5, CacheRead: 0.2}, Input: []string{"text", "image"}},
		{ID: "grok-build-0.1", Name: "Grok Build 0.1", Reasoning: true, ContextWindow: 256000, MaxTokens: 256000, Cost: &CostConfig{Input: 1, Output: 2, CacheRead: 0.2}, Input: []string{"text", "image"}},
		{ID: "grok-code-fast-1", Name: "Grok Code Fast 1", ContextWindow: 32768, MaxTokens: 8192, Cost: &CostConfig{Input: 0.2, Output: 1.5, CacheRead: 0.02}, Input: []string{"text"}},
	}},
	"kimi-coding":           {BaseURL: "https://api.kimi.com/coding/v1", APIKey: "${KIMI_CODING_API_KEY}", API: "openai-chat", ThinkingFormat: "kimi", Headers: map[string]string{"User-Agent": "opencode/1.17.18"}, Models: []ModelConfig{{ID: "kimi-for-coding", Name: "Kimi For Coding", Reasoning: true, ContextWindow: 262144, MaxTokens: 32768, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text", "image"}}, {ID: "kimi-k2-thinking", Name: "Kimi K2 Thinking", Reasoning: true, ContextWindow: 262144, MaxTokens: 32768, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "k3", Name: "Kimi K3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text", "image"}}, {ID: "k3-256k", Name: "Kimi K3 256K", Reasoning: true, ContextWindow: 262144, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text", "image"}}}},
	"fireworks":             {BaseURL: "https://api.fireworks.ai/inference", APIKey: "${FIREWORKS_API_KEY}", API: "anthropic-messages", Models: []ModelConfig{{ID: "accounts/fireworks/models/deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Cost: &CostConfig{Input: 0.14, Output: 0.28, CacheRead: 0.03, CacheWrite: 0}, Input: []string{"text"}}, {ID: "accounts/fireworks/models/deepseek-v4-pro", Name: "DeepSeek V4 Pro", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Cost: &CostConfig{Input: 1.74, Output: 3.48, CacheRead: 0.145, CacheWrite: 0}, Input: []string{"text"}}, {ID: "accounts/fireworks/models/glm-5p1", Name: "GLM 5.1", Reasoning: true, ContextWindow: 202800, MaxTokens: 131072, Cost: &CostConfig{Input: 1.4, Output: 4.4, CacheRead: 0.26, CacheWrite: 0}, Input: []string{"text"}}, {ID: "accounts/fireworks/models/kimi-k2p7-code", Name: "Kimi K2.7 Code", Reasoning: true, ContextWindow: 262000, MaxTokens: 262000, Cost: &CostConfig{Input: 0.95, Output: 4, CacheRead: 0.19, CacheWrite: 0}, Input: []string{"text"}}, {ID: "accounts/fireworks/routers/kimi-k2p7-code-fast", Name: "Kimi K2.7 Code Fast", Reasoning: true, ContextWindow: 262000, MaxTokens: 262000, Cost: &CostConfig{Input: 2, Output: 8, CacheRead: 0.38, CacheWrite: 0}, Input: []string{"text"}}, {ID: "accounts/fireworks/models/gpt-oss-120b", Name: "GPT OSS 120B", Reasoning: true, ContextWindow: 131072, MaxTokens: 32768, Cost: &CostConfig{Input: 0.15, Output: 0.6, CacheRead: 0.01, CacheWrite: 0}, Input: []string{"text"}}, {ID: "accounts/fireworks/models/gpt-oss-20b", Name: "GPT OSS 20B", Reasoning: true, ContextWindow: 131072, MaxTokens: 32768, Cost: &CostConfig{Input: 0.07, Output: 0.3, CacheRead: 0.035, CacheWrite: 0}, Input: []string{"text"}}}},
	"huggingface":           {BaseURL: "https://router.huggingface.co/v1", APIKey: "${HUGGINGFACE_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "MiniMaxAI/MiniMax-M2.1", Name: "MiniMax-M2.1", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Cost: &CostConfig{Input: 0.3, Output: 1.2, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "MiniMaxAI/MiniMax-M2.5", Name: "MiniMax-M2.5", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Cost: &CostConfig{Input: 0.3, Output: 1.2, CacheRead: 0.03, CacheWrite: 0}, Input: []string{"text"}}, {ID: "MiniMaxAI/MiniMax-M2.7", Name: "MiniMax-M2.7", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Cost: &CostConfig{Input: 0.3, Output: 1.2, CacheRead: 0.06, CacheWrite: 0}, Input: []string{"text"}}, {ID: "Qwen/Qwen3-235B-A22B-Thinking-2507", Name: "Qwen3-235B-A22B-Thinking-2507", Reasoning: true, ContextWindow: 262144, MaxTokens: 131072, Cost: &CostConfig{Input: 0.3, Output: 3, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "Qwen/Qwen3-Coder-480B-A35B-Instruct", Name: "Qwen3-Coder-480B-A35B-Instruct", ContextWindow: 262144, MaxTokens: 66536, Cost: &CostConfig{Input: 2, Output: 2, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}}},
	"xiaomi-token-plan-ams": {BaseURL: "https://token-plan-ams.xiaomimimo.com/v1", APIKey: "${XIAOMI_TOKEN_PLAN_AMS_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "mimo-v2.5", Name: "MiMo-V2.5", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 0.4, Output: 2, CacheRead: 0.08, CacheWrite: 0}, Input: []string{"text", "image"}}, {ID: "mimo-v2.5-pro", Name: "MiMo-V2.5-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 1, Output: 3, CacheRead: 0.2, CacheWrite: 0}, Input: []string{"text"}}, {ID: "mimo-v2.5-pro-ultraspeed", Name: "MiMo-V2.5-Pro-UltraSpeed", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 1.305, Output: 2.61, CacheRead: 0.0108, CacheWrite: 0}, Input: []string{"text"}}, {ID: "mimo-v2.6-pro", Name: "MiMo-V2.6-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text", "image"}}, {ID: "mimo-v2.6-flash", Name: "MiMo-V2.6-Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text", "image"}}}},
	"xiaomi-token-plan-cn":  {BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", APIKey: "${XIAOMI_TOKEN_PLAN_CN_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "mimo-v2.5", Name: "MiMo-V2.5", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 0.4, Output: 2, CacheRead: 0.08, CacheWrite: 0}, Input: []string{"text", "image"}}, {ID: "mimo-v2.5-pro", Name: "MiMo-V2.5-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 1, Output: 3, CacheRead: 0.2, CacheWrite: 0}, Input: []string{"text"}}, {ID: "mimo-v2.5-pro-ultraspeed", Name: "MiMo-V2.5-Pro-UltraSpeed", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 1.305, Output: 2.61, CacheRead: 0.0108, CacheWrite: 0}, Input: []string{"text"}}, {ID: "mimo-v2.6-pro", Name: "MiMo-V2.6-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text", "image"}}, {ID: "mimo-v2.6-flash", Name: "MiMo-V2.6-Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text", "image"}}}},
	"xiaomi-token-plan-sgp": {BaseURL: "https://token-plan-sgp.xiaomimimo.com/v1", APIKey: "${XIAOMI_TOKEN_PLAN_SGP_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "mimo-v2.5", Name: "MiMo-V2.5", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 0.4, Output: 2, CacheRead: 0.08, CacheWrite: 0}, Input: []string{"text", "image"}}, {ID: "mimo-v2.5-pro", Name: "MiMo-V2.5-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 1, Output: 3, CacheRead: 0.2, CacheWrite: 0}, Input: []string{"text"}}, {ID: "mimo-v2.5-pro-ultraspeed", Name: "MiMo-V2.5-Pro-UltraSpeed", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Cost: &CostConfig{Input: 1.305, Output: 2.61, CacheRead: 0.0108, CacheWrite: 0}, Input: []string{"text"}}, {ID: "mimo-v2.6-pro", Name: "MiMo-V2.6-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text", "image"}}, {ID: "mimo-v2.6-flash", Name: "MiMo-V2.6-Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text", "image"}}}},
	"zai-coding-cn":         {Vendor: "zai", BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", APIKey: "${ZAI_CODING_CN_API_KEY}", API: "openai-chat", ThinkingFormat: "zai", Models: []ModelConfig{{ID: "glm-4.5-air", Name: "GLM-4.5-Air", Reasoning: true, ContextWindow: 131072, MaxTokens: 98304, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-4.7", Name: "GLM-4.7", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-5-turbo", Name: "GLM-5-Turbo", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-5.1", Name: "GLM-5.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-5.3", Name: "GLM-5.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-5.3-flash", Name: "GLM-5.3-Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text", "image"}}, {ID: "glm-5v-turbo", Name: "GLM-5V-Turbo", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text", "image"}}}},
	"longcat":               {Vendor: "longcat", BaseURL: "https://api.longcat.chat/openai", APIKey: "${LONGCAT_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "LongCat-2.0", Name: "LongCat-2.0", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text"}}}},
	"longcat-anthropic":     {Vendor: "longcat", BaseURL: "https://api.longcat.chat/anthropic", APIKey: "${LONGCAT_ANTHROPIC_API_KEY}", API: "anthropic-messages", Models: []ModelConfig{{ID: "LongCat-2.0", Name: "LongCat-2.0", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text"}}}},
	"minimax-anthropic":     {BaseURL: "https://api.minimaxi.com/anthropic", APIKey: "${MINIMAX_ANTHROPIC_API_KEY}", API: "anthropic-messages", Models: []ModelConfig{{ID: "MiniMax-M2.7", Name: "MiniMax-M2.7", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Cost: &CostConfig{Input: 0.3, Output: 1.2, CacheRead: 0.06, CacheWrite: 0.375}, Input: []string{"text"}}, {ID: "MiniMax-M2.7-highspeed", Name: "MiniMax-M2.7-highspeed", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Cost: &CostConfig{Input: 0.6, Output: 2.4, CacheRead: 0.06, CacheWrite: 0.375}, Input: []string{"text"}}, {ID: "MiniMax-M3", Name: "MiniMax-M3", Reasoning: true, ContextWindow: 512000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.6, Output: 2.4, CacheRead: 0.12, CacheWrite: 0}, Input: []string{"text", "image"}}}},
	"minimax-cn-anthropic":  {BaseURL: "https://api.minimaxi.com/anthropic", APIKey: "${MINIMAX_CN_ANTHROPIC_API_KEY}", API: "anthropic-messages", Models: []ModelConfig{{ID: "MiniMax-M2.7", Name: "MiniMax-M2.7", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Cost: &CostConfig{Input: 0.3, Output: 1.2, CacheRead: 0.06, CacheWrite: 0.375}, Input: []string{"text"}}, {ID: "MiniMax-M2.7-highspeed", Name: "MiniMax-M2.7-highspeed", Reasoning: true, ContextWindow: 204800, MaxTokens: 131072, Cost: &CostConfig{Input: 0.6, Output: 2.4, CacheRead: 0.06, CacheWrite: 0.375}, Input: []string{"text"}}, {ID: "MiniMax-M3", Name: "MiniMax-M3", Reasoning: true, ContextWindow: 512000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.6, Output: 2.4, CacheRead: 0.12, CacheWrite: 0}, Input: []string{"text", "image"}}}},
	"opencode":              {BaseURL: "https://opencode.ai/zen/v1", APIKey: "${OPENCODE_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "big-pickle", Name: "Big Pickle", Reasoning: true, ContextWindow: 200000, MaxTokens: 32000, Cost: &CostConfig{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}, Input: []string{"text"}}, {ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25}, Input: []string{"text", "image"}}, {ID: "claude-opus-4-1", Name: "Claude Opus 4.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 32000, Cost: &CostConfig{Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75}, Input: []string{"text", "image"}}, {ID: "claude-opus-4-5", Name: "Claude Opus 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}, Input: []string{"text", "image"}}, {ID: "claude-opus-4-6", Name: "Claude Opus 4.6", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}, Input: []string{"text", "image"}}}},
	"opencode-go":           {BaseURL: "https://opencode.ai/zen/go/v1", APIKey: "${OPENCODE_GO_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Cost: &CostConfig{Input: 0.14, Output: 0.28, CacheRead: 0.0028, CacheWrite: 0}, Input: []string{"text"}}, {ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Cost: &CostConfig{Input: 1.74, Output: 3.48, CacheRead: 0.0145, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-5", Name: "GLM-5", Reasoning: true, ContextWindow: 200000, MaxTokens: 32768, Cost: &CostConfig{Input: 1, Output: 3.2, CacheRead: 0.2, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-5.1", Name: "GLM-5.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 32768, Cost: &CostConfig{Input: 1.4, Output: 4.4, CacheRead: 0.26, CacheWrite: 0}, Input: []string{"text"}}, {ID: "glm-5.3", Name: "GLM-5.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Cost: &CostConfig{Input: 1.4, Output: 4.4, CacheRead: 0.26, CacheWrite: 0}, Input: []string{"text"}}, {ID: "kimi-k2.6", Name: "Kimi K2.6", Reasoning: true, ContextWindow: 262144, MaxTokens: 65536, Cost: &CostConfig{Input: 0.95, Output: 4, CacheRead: 0.16, CacheWrite: 0}, Input: []string{"text", "image"}}, {ID: "kimi-k2.7-code", Name: "Kimi K2.7 Code", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.95, Output: 4, CacheRead: 0.19, CacheWrite: 0}, Input: []string{"text"}}}},
	"vercel-ai-gateway": {BaseURL: "https://ai-gateway.vercel.sh", APIKey: "${VERCEL_AI_GATEWAY_API_KEY}", API: "anthropic-messages", Models: []ModelConfig{
		{ID: "anthropic/claude-sonnet-4.6", Name: "Claude Sonnet 4.6", Reasoning: true, ContextWindow: 1000000, MaxTokens: 64000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
		{ID: "anthropic/claude-opus-4.8", Name: "Claude Opus 4.8", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}, Input: []string{"text", "image"}},
		{ID: "anthropic/claude-sonnet-4.5", Name: "Claude Sonnet 4.5", Reasoning: true, ContextWindow: 1000000, MaxTokens: 64000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
		{ID: "anthropic/claude-haiku-4.5", Name: "Claude Haiku 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25}, Input: []string{"text", "image"}},
		{ID: "openai/gpt-5.5", Name: "GPT-5.5", Reasoning: true, ContextWindow: 1050000, MaxTokens: 128000, Cost: &CostConfig{Input: 5, Output: 30, CacheRead: 0.5}, Input: []string{"text", "image"}},
		{ID: "openai/gpt-5.4", Name: "GPT-5.4", Reasoning: true, ContextWindow: 1050000, MaxTokens: 128000, Cost: &CostConfig{Input: 2.5, Output: 15, CacheRead: 0.25}, Input: []string{"text", "image"}},
		{ID: "google/gemini-3.5-flash", Name: "Gemini 3.5 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 1.5, Output: 9, CacheRead: 0.15, CacheWrite: 0.083333}, Input: []string{"text", "image"}},
		{ID: "deepseek/deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 0.09, Output: 0.18, CacheRead: 0.02}, Input: []string{"text"}},
		{ID: "deepseek/deepseek-v4-pro", Name: "DeepSeek V4 Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 384000, Cost: &CostConfig{Input: 0.435, Output: 0.87, CacheRead: 0.003625}, Input: []string{"text"}},
		{ID: "alibaba/qwen3.6-plus", Name: "Qwen3.6 Plus", Reasoning: true, ContextWindow: 65536, MaxTokens: 65536, Cost: &CostConfig{Input: 0.325, Output: 1.95, CacheWrite: 0.40625}, Input: []string{"text", "image"}},
		{ID: "minimax/minimax-m3", Name: "MiniMax M3", Reasoning: true, ContextWindow: 1048576, MaxTokens: 4096, Cost: &CostConfig{Input: 0.3, Output: 1.2, CacheRead: 0.06}, Input: []string{"text", "image"}},
		{ID: "moonshotai/kimi-k2.7-code", Name: "Kimi K2.7 Code", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.612, Output: 3.069, CacheRead: 0.1296}, Input: []string{"text"}},
		{ID: "xai/grok-4.3", Name: "Grok 4.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 4096, Cost: &CostConfig{Input: 1.25, Output: 2.5, CacheRead: 0.2}, Input: []string{"text", "image"}},
		{ID: "zai/glm-5.3", Name: "GLM 5.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Cost: &CostConfig{Input: 1.2, Output: 4.1, CacheRead: 0.2}, Input: []string{"text"}},
	}},
	"mistral": {Vendor: "mistral", BaseURL: "https://api.mistral.ai/v1", APIKey: "${MISTRAL_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "codestral-latest", Name: "Codestral (latest)", ContextWindow: 256000, MaxTokens: 4096, Cost: &CostConfig{Input: 0.3, Output: 0.9, CacheRead: 0.03}, Input: []string{"text"}},
		{ID: "devstral-2512", Name: "Devstral 2", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.4, Output: 2, CacheRead: 0.04}, Input: []string{"text"}},
		{ID: "devstral-latest", Name: "Devstral 2", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.4, Output: 2, CacheRead: 0.04}, Input: []string{"text"}},
		{ID: "devstral-medium-2507", Name: "Devstral Medium", ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.4, Output: 2, CacheRead: 0.04}, Input: []string{"text"}},
		{ID: "devstral-medium-latest", Name: "Devstral 2 (latest)", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.4, Output: 2, CacheRead: 0.04}, Input: []string{"text"}},
		{ID: "devstral-small-2505", Name: "Devstral Small 2505", ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.1, Output: 0.3, CacheRead: 0.01}, Input: []string{"text"}},
		{ID: "devstral-small-2507", Name: "Devstral Small", ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.1, Output: 0.3, CacheRead: 0.01}, Input: []string{"text"}},
		{ID: "labs-devstral-small-2512", Name: "Devstral Small 2", ContextWindow: 256000, MaxTokens: 256000, Input: []string{"text", "image"}},
		{ID: "magistral-medium-latest", Name: "Magistral Medium (latest)", Reasoning: true, ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 2, Output: 5, CacheRead: 0.2}, Input: []string{"text"}},
		{ID: "magistral-small", Name: "Magistral Small", Reasoning: true, ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.5, Output: 1.5, CacheRead: 0.05}, Input: []string{"text"}},
		{ID: "ministral-3b-latest", Name: "Ministral 3B (latest)", ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.04, Output: 0.04, CacheRead: 0.004}, Input: []string{"text"}},
		{ID: "ministral-8b-latest", Name: "Ministral 8B (latest)", ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.1, Output: 0.1, CacheRead: 0.01}, Input: []string{"text"}},
		{ID: "mistral-large-2411", Name: "Mistral Large 2.1", ContextWindow: 131072, MaxTokens: 16384, Cost: &CostConfig{Input: 2, Output: 6, CacheRead: 0.2}, Input: []string{"text"}},
		{ID: "mistral-large-2512", Name: "Mistral Large 3", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.5, Output: 1.5, CacheRead: 0.05}, Input: []string{"text", "image"}},
		{ID: "mistral-large-latest", Name: "Mistral Large (latest)", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.5, Output: 1.5, CacheRead: 0.05}, Input: []string{"text", "image"}},
		{ID: "mistral-medium-2505", Name: "Mistral Medium 3", ContextWindow: 131072, MaxTokens: 131072, Cost: &CostConfig{Input: 0.4, Output: 2, CacheRead: 0.04}, Input: []string{"text", "image"}},
		{ID: "mistral-medium-2508", Name: "Mistral Medium 3.1", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.4, Output: 2, CacheRead: 0.04}, Input: []string{"text", "image"}},
		{ID: "mistral-medium-2604", Name: "Mistral Medium 3.5", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 1.5, Output: 7.5, CacheRead: 0.15}, Input: []string{"text", "image"}},
		{ID: "mistral-medium-3.5", Name: "Mistral Medium 3.5", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 1.5, Output: 7.5}, Input: []string{"text", "image"}},
		{ID: "mistral-medium-latest", Name: "Mistral Medium (latest)", ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.4, Output: 2, CacheRead: 0.04}, Input: []string{"text", "image"}},
		{ID: "mistral-nemo", Name: "Mistral Nemo", ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.15, Output: 0.15, CacheRead: 0.015}, Input: []string{"text"}},
		{ID: "mistral-small-2506", Name: "Mistral Small 3.2", ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 0.1, Output: 0.3, CacheRead: 0.01}, Input: []string{"text", "image"}},
		{ID: "mistral-small-2603", Name: "Mistral Small 4", Reasoning: true, ContextWindow: 256000, MaxTokens: 256000, Cost: &CostConfig{Input: 0.15, Output: 0.6, CacheRead: 0.015}, Input: []string{"text", "image"}},
		{ID: "mistral-small-latest", Name: "Mistral Small (latest)", Reasoning: true, ContextWindow: 256000, MaxTokens: 256000, Cost: &CostConfig{Input: 0.15, Output: 0.6, CacheRead: 0.015}, Input: []string{"text", "image"}},
		{ID: "open-mistral-7b", Name: "Mistral 7B", ContextWindow: 8000, MaxTokens: 8000, Cost: &CostConfig{Input: 0.25, Output: 0.25, CacheRead: 0.025}, Input: []string{"text"}},
		{ID: "open-mistral-nemo", Name: "Open Mistral Nemo", ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.15, Output: 0.15, CacheRead: 0.015}, Input: []string{"text"}},
		{ID: "open-mixtral-8x22b", Name: "Mixtral 8x22B", ContextWindow: 64000, MaxTokens: 64000, Cost: &CostConfig{Input: 2, Output: 6, CacheRead: 0.2}, Input: []string{"text"}},
		{ID: "open-mixtral-8x7b", Name: "Mixtral 8x7B", ContextWindow: 32000, MaxTokens: 32000, Cost: &CostConfig{Input: 0.7, Output: 0.7, CacheRead: 0.07}, Input: []string{"text"}},
		{ID: "pixtral-12b", Name: "Pixtral 12B", ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.15, Output: 0.15, CacheRead: 0.015}, Input: []string{"text", "image"}},
		{ID: "pixtral-large-latest", Name: "Pixtral Large (latest)", ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 2, Output: 6, CacheRead: 0.2}, Input: []string{"text", "image"}},
	}},
	"github-copilot": {Vendor: "github-copilot", BaseURL: "https://api.individual.githubcopilot.com", APIKey: "${COPILOT_GITHUB_TOKEN}", API: "openai-chat", Models: []ModelConfig{
		{ID: "claude-sonnet-4.6", Name: "Claude Sonnet 4.6", Reasoning: true, ContextWindow: 1000000, MaxTokens: 32000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
		{ID: "claude-opus-4.8", Name: "Claude Opus 4.8", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}, Input: []string{"text", "image"}},
		{ID: "claude-sonnet-4.5", Name: "Claude Sonnet 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 32000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
		{ID: "claude-haiku-4.5", Name: "Claude Haiku 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25}, Input: []string{"text", "image"}},
		{ID: "claude-fable-5", Name: "Claude Fable 5", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Cost: &CostConfig{Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5}, Input: []string{"text", "image"}},
		{ID: "gpt-5.5", Name: "GPT-5.5", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 5, Output: 30, CacheRead: 0.5}, Input: []string{"text", "image"}},
		{ID: "gpt-5.4", Name: "GPT-5.4", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 2.5, Output: 15, CacheRead: 0.25}, Input: []string{"text", "image"}},
		{ID: "gpt-5.2", Name: "GPT-5.2", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 1.75, Output: 14, CacheRead: 0.175}, Input: []string{"text", "image"}},
		{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Reasoning: true, ContextWindow: 128000, MaxTokens: 64000, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125}, Input: []string{"text", "image"}},
		{ID: "gemini-3.5-flash", Name: "Gemini 3.5 Flash", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Cost: &CostConfig{Input: 1.5, Output: 9, CacheRead: 0.15}, Input: []string{"text", "image"}},
	}},
	"cloudflare-ai-gateway": {Vendor: "cloudflare-ai-gateway", BaseURL: "https://gateway.ai.cloudflare.com/v1/{ACCOUNT_ID}/{GATEWAY_ID}", APIKey: "${CLOUDFLARE_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "anthropic/claude-sonnet-4.6", Name: "Claude Sonnet 4.6", Reasoning: true, ContextWindow: 1000000, MaxTokens: 64000, Cost: &CostConfig{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, Input: []string{"text", "image"}},
		{ID: "anthropic/claude-opus-4.8", Name: "Claude Opus 4.8", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Cost: &CostConfig{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}, Input: []string{"text", "image"}},
		{ID: "openai/gpt-5.4", Name: "GPT-5.4", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 2.5, Output: 15, CacheRead: 0.25}, Input: []string{"text", "image"}},
		{ID: "openai/gpt-5.2", Name: "GPT-5.2", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Cost: &CostConfig{Input: 1.75, Output: 14, CacheRead: 0.175}, Input: []string{"text", "image"}},
		{ID: "google/gemini-2.5-pro", Name: "Gemini 2.5 Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 1.25, Output: 10, CacheRead: 0.125}, Input: []string{"text", "image"}},
		{ID: "google/gemini-3.5-flash", Name: "Gemini 3.5 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Cost: &CostConfig{Input: 1.5, Output: 9, CacheRead: 0.15}, Input: []string{"text", "image"}},
		{ID: "meta-llama/llama-4-scout", Name: "Llama 4 Scout", ContextWindow: 10000000, MaxTokens: 16384, Cost: &CostConfig{Input: 0.1, Output: 0.3}, Input: []string{"text", "image"}},
	}},
	"cloudflare-workers-ai": {Vendor: "cloudflare-workers-ai", BaseURL: "https://api.cloudflare.com/client/v4/accounts/{ACCOUNT_ID}/ai/v1", APIKey: "${CLOUDFLARE_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "@cf/meta/llama-4-scout-17b-16e-instruct", Name: "Llama 4 Scout 17B", ContextWindow: 131000, MaxTokens: 16384, Cost: &CostConfig{Input: 0.27, Output: 0.85}, Input: []string{"text", "image"}},
		{ID: "@cf/meta/llama-3.3-70b-instruct-fp8-fast", Name: "Llama 3.3 70B Instruct FP8 Fast", ContextWindow: 24000, MaxTokens: 24000, Cost: &CostConfig{Input: 0.293, Output: 2.253}, Input: []string{"text"}},
		{ID: "@cf/google/gemma-4-26b-a4b-it", Name: "Gemma 4 26B A4B IT", Reasoning: true, ContextWindow: 256000, MaxTokens: 16384, Cost: &CostConfig{Input: 0.1, Output: 0.3}, Input: []string{"text", "image"}},
		{ID: "@cf/mistralai/mistral-small-3.1-24b-instruct", Name: "Mistral Small 3.1 24B", ContextWindow: 128000, MaxTokens: 128000, Cost: &CostConfig{Input: 0.351, Output: 0.555}, Input: []string{"text"}},
		{ID: "@cf/openai/gpt-oss-120b", Name: "GPT OSS 120B", Reasoning: true, ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 0.35, Output: 0.75}, Input: []string{"text"}},
		{ID: "@cf/openai/gpt-oss-20b", Name: "GPT OSS 20B", Reasoning: true, ContextWindow: 128000, MaxTokens: 16384, Cost: &CostConfig{Input: 0.2, Output: 0.3}, Input: []string{"text"}},
		{ID: "@cf/moonshotai/kimi-k2.7-code", Name: "Kimi K2.7 Code", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Cost: &CostConfig{Input: 0.95, Output: 4, CacheRead: 0.19}, Input: []string{"text"}},
		{ID: "@cf/zai-org/glm-5.3", Name: "GLM 5.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Cost: &CostConfig{Input: 1.4, Output: 4.4, CacheRead: 0.26}, Input: []string{"text"}},
	}},
	"tencent-hy-plan":           {Vendor: "tencent-hy-plan", BaseURL: "https://api.lkeap.cloud.tencent.com/plan/v3", APIKey: "${TENCENT_HY_PLAN_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "hy3", Name: "Hunyuan 3", Reasoning: true, ContextWindow: 262144, MaxTokens: 65536, Input: []string{"text"}}}},
	"tencent-hy-plan-anthropic": {Vendor: "tencent-hy-plan", BaseURL: "https://api.lkeap.cloud.tencent.com/plan/anthropic", APIKey: "${TENCENT_HY_PLAN_ANTHROPIC_API_KEY}", API: "anthropic-messages", Models: []ModelConfig{{ID: "hy3", Name: "Hunyuan 3", Reasoning: true, ContextWindow: 262144, MaxTokens: 65536, Input: []string{"text"}}}},
	"qianfan-code-plan":         {Vendor: "qianfan", BaseURL: "https://qianfan.baidubce.com/v2", APIKey: "${QIANFAN_CODE_PLAN_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "qianfan-code-latest", Name: "Qianfan Code Latest", Reasoning: true, ContextWindow: 1000000, MaxTokens: 65536, Input: []string{"text"}}, {ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text", "image"}}, {ID: "glm-5.1", Name: "GLM 5.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text", "image"}}, {ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text", "image"}}}},
	"qianfan-token-plan":        {Vendor: "qianfan", BaseURL: "https://qianfan.baidubce.com/v2/tokenplan/personal", APIKey: "${QIANFAN_TOKEN_PLAN_API_KEY}", API: "openai-chat", Models: []ModelConfig{{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text", "image"}}, {ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text", "image"}}, {ID: "glm-5.3", Name: "GLM 5.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text"}}, {ID: "glm-5.1", Name: "GLM 5.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text", "image"}}, {ID: "kimi-k2.6", Name: "Kimi-K2.6", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image", "video"}}, {ID: "ernie-5.1", Name: "ERNIE-5.1", Reasoning: true, ContextWindow: 131072, MaxTokens: 65536, Input: []string{"text"}}}},
	"amazon-bedrock": {Vendor: "amazon-bedrock", BaseURL: "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1", APIKey: "${AWS_BEARER_TOKEN_BEDROCK}", API: "openai-chat", Models: []ModelConfig{
		{ID: "anthropic.claude-sonnet-4-6-v1", Name: "Claude Sonnet 4.6", Reasoning: true, ContextWindow: 1000000, MaxTokens: 64000, Input: []string{"text", "image"}},
		{ID: "anthropic.claude-opus-4-8", Name: "Claude Opus 4.8", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "anthropic.claude-sonnet-4-5-20250929-v1:0", Name: "Claude Sonnet 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Input: []string{"text", "image"}},
		{ID: "anthropic.claude-haiku-4-5-20251001-v1:0", Name: "Claude Haiku 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Input: []string{"text", "image"}},
		{ID: "anthropic.claude-fable-5", Name: "Claude Fable 5", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "amazon.nova-pro-v1:0", Name: "Amazon Nova Pro", ContextWindow: 300000, MaxTokens: 5120, Input: []string{"text", "image"}},
		{ID: "amazon.nova-micro-v1:0", Name: "Amazon Nova Micro", ContextWindow: 128000, MaxTokens: 5120, Input: []string{"text"}},
		{ID: "amazon.nova-lite-v1:0", Name: "Amazon Nova Lite", ContextWindow: 300000, MaxTokens: 5120, Input: []string{"text", "image"}},
		{ID: "deepseek.v3.2", Name: "DeepSeek V3.2", ContextWindow: 131072, MaxTokens: 16384, Input: []string{"text"}},
		{ID: "deepseek.r1-v1:0", Name: "DeepSeek R1", Reasoning: true, ContextWindow: 131072, MaxTokens: 16384, Input: []string{"text"}},
	}},
	"stepfun": {BaseURL: "https://api.stepfun.com/step_plan/v1", APIKey: "${STEPFUN_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "step-3.7-flash", Name: "Step 3.7 Flash", ContextWindow: 262144, MaxTokens: 16384, Input: []string{"text", "image"}},
	}},
	"amd-radeon": {Vendor: "amd-radeon", BaseURL: "https://developer.amd.com.cn/radeon/api/v1", APIKey: "${AMD_RADEON_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "DeepSeek-V4-Flash", Name: "DeepSeek V4 Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}},
		{ID: "Qwen3.8-Flash-Next", Name: "Qwen3.8 Flash Next", Reasoning: true, ContextWindow: 1000000, Input: []string{"text"}},
	}},
	"bai": {Vendor: "bai", BaseURL: "https://api.b.ai/v1", APIKey: "${BAI_API_KEY}", API: "openai-chat", Models: []ModelConfig{
		{ID: "minimax-m3", Name: "MiniMax-M3", Reasoning: true, ContextWindow: 1048576, MaxTokens: 128000, Input: []string{"text", "image", "video"}},
		{ID: "minimax-m2.7", Name: "MiniMax-M2.7", Reasoning: true, ContextWindow: 262144, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "glm-5.1", Name: "GLM-5.1", Reasoning: true, ContextWindow: 200000, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "glm-5.2", Name: "GLM-5.2", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", Reasoning: true, Input: []string{"text", "image"}},
		{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", Reasoning: true, Input: []string{"text", "image"}},
		{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", Reasoning: true, Input: []string{"text", "image"}},
		{ID: "gpt-5.5", Name: "GPT-5.5", Reasoning: true, ContextWindow: 1050000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "gpt-5.5-instant", Name: "GPT-5.5 Instant", Reasoning: true, ContextWindow: 128000, MaxTokens: 16384, Input: []string{"text", "image"}},
		{ID: "gpt-5.4", Name: "GPT-5.4", Reasoning: true, ContextWindow: 1050000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "gpt-5.2", Name: "GPT-5.2", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "gpt-5.4-pro", Name: "GPT-5.4 Pro", Reasoning: true, ContextWindow: 1050000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "gpt-5.4-mini", Name: "GPT-5.4 Mini", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "gpt-5-mini", Name: "GPT-5 Mini", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "gpt-5.4-nano", Name: "GPT-5.4 Nano", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "gpt-5-nano", Name: "GPT-5 Nano", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "claude-fable-5.1", Name: "Claude Fable 5.1", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "claude-fable-5", Name: "Claude Fable 5", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "claude-opus-5", Name: "Claude Opus 5", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "claude-opus-4.8", Name: "Claude Opus 4.8", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "claude-opus-4.7", Name: "Claude Opus 4.7", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "claude-opus-4.6", Name: "Claude Opus 4.6", Reasoning: true, ContextWindow: 1000000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "claude-opus-4.5", Name: "Claude Opus 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Input: []string{"text", "image"}},
		{ID: "claude-sonnet-5", Name: "Claude Sonnet 5", Reasoning: true, ContextWindow: 1000000, MaxTokens: 64000, Input: []string{"text", "image"}},
		{ID: "claude-sonnet-4.6", Name: "Claude Sonnet 4.6", Reasoning: true, ContextWindow: 1000000, MaxTokens: 64000, Input: []string{"text", "image"}},
		{ID: "claude-sonnet-4.5", Name: "Claude Sonnet 4.5", Reasoning: true, ContextWindow: 1000000, MaxTokens: 64000, Input: []string{"text", "image"}},
		{ID: "claude-haiku-4.5", Name: "Claude Haiku 4.5", Reasoning: true, ContextWindow: 200000, MaxTokens: 64000, Input: []string{"text", "image"}},
		{ID: "gemini-3.1-pro", Name: "Gemini 3.1 Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "gemini-3-flash", Name: "Gemini 3 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "gemini-3.5-flash-lite", Name: "Gemini 3.5 Flash Lite", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "gemini-3.6-flash", Name: "Gemini 3.6 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "hy3", Name: "Hunyuan 3", Reasoning: true, ContextWindow: 262144, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "mimo-v2.5-pro", Name: "MiMo-V2.5-Pro", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "glm-5.3-flash", Name: "GLM-5.3-Flash", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "glm-5.3-flashx", Name: "GLM-5.3-FlashX", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "kimi-k3", Name: "Kimi K3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 262144, Input: []string{"text", "image"}},
		{ID: "glm-5.3", Name: "GLM-5.3", Reasoning: true, ContextWindow: 1000000, MaxTokens: 131072, Input: []string{"text"}},
		{ID: "kimi-k2.8-preview", Name: "Kimi K2.8 Preview", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image"}},
		{ID: "kimi-k2.6", Name: "Kimi K2.6", Reasoning: true, ContextWindow: 262144, MaxTokens: 262144, Input: []string{"text", "image", "video"}},
		{ID: "mimo-v2.5", Name: "MiMo-V2.5", Reasoning: true, ContextWindow: 1048576, MaxTokens: 131072, Input: []string{"text", "image"}},
		{ID: "hy4-preview", Name: "Hunyuan 4 Preview", Reasoning: true, ContextWindow: 262144, MaxTokens: 65536, Input: []string{"text"}},
		{ID: "qwen3.8-flash", Name: "Qwen3.8 Flash", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "qwen3.8-max", Name: "Qwen3.8 Max", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "qwen3.8-27b", Name: "Qwen3.8-27B", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image", "video"}},
		{ID: "deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash", Reasoning: true, ContextWindow: 1000000, Input: []string{"text", "image"}},
		{ID: "gpt-6-astra", Name: "GPT-6 Astra", Reasoning: true, ContextWindow: 400000, MaxTokens: 128000, Input: []string{"text", "image"}},
		{ID: "gemini-3.8-flash", Name: "Gemini 3.8 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Input: []string{"text", "image"}},
		{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, Input: []string{"text"}},
		{ID: "gemini-3.5-flash", Name: "Gemini 3.5 Flash", Reasoning: true, ContextWindow: 1048576, MaxTokens: 65536, Input: []string{"text", "image"}},
	}},
}

func DefaultSettings() *Settings {
	return &Settings{
		Providers:            cloneProviderConfigs(defaultProviderConfigs),
		DefaultProvider:      "deepseek-openai",
		DefaultModel:         "deepseek-v4-flash",
		DefaultThinkingLevel: "medium",
		DefaultMode:          "yolo",
		Authored:             false,
		TUILang:              "auto",
		ToolExecution:        ToolExecutionSettings{Mode: "parallel", MaxConcurrency: DefaultToolExecutionMaxConcurrency},
		StatusLine: StatusLineSettings{
			Enabled:   false,
			Type:      "command",
			Padding:   0,
			TimeoutMs: 800,
			Fallback:  "builtin",
		},
		EnablePlanTool:    boolPtr(true),
		EnableArtifact:    boolPtr(false),
		EnableACPArtifact: boolPtr(false),
		WebSearch:         WebSearchSettings{Enabled: boolPtr(false), Provider: "openai", ProviderType: "openai-responses"},
		ImageGeneration:   ImageGenerationSettings{Enabled: boolPtr(false), Provider: "openai", APIType: "openai-images", BaseURL: "https://api.openai.com/v1", Model: "gpt-image-1"},
		ContextFiles:      ContextFilesSettings{Enabled: true},
		SkillsDir:         platform.SkillsDir(),
		SkillHub: SkillHubSettings{
			DefaultMarket:       "skillhub.cn",
			DefaultInstallScope: "project",
			OfficialHandles:     []string{DefaultSkillHubOfficialHandle},
		},
		Compaction: CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000},
		Sandbox: SandboxSettings{
			Enabled:     false,
			Level:       "none",
			AllowedRead: platform.SandboxPaths(),
			DeniedPaths: platform.DeniedPaths(),
			PassEnv:     platform.DefaultEnvVars(),
			TmpSize:     "100m",
			ProtectGit:  true,
		},
		SessionDir: platform.SessionDir(),
		Theme:      "dark",
		Retry:      RetrySettings{Enabled: true, MaxRetries: 5, BaseDelayMs: 3000},
		Approval: ApprovalSettings{
			BashWhitelist:      []string{"go ", "make ", "git ", "npm ", "yarn ", "node ", "python ", "pip "},
			ConfirmBeforeWrite: boolPtr(true),
		},
	}
}

func defaultSettingsFile() *Settings {
	s := DefaultSettings()
	s.Providers = nil
	return s
}

func cloneProviderConfigs(src map[string]*ProviderConfig) map[string]*ProviderConfig {
	out := make(map[string]*ProviderConfig, len(src))
	for name, pc := range src {
		out[name] = cloneProviderConfig(pc)
	}
	return out
}

func cloneProviderConfig(src *ProviderConfig) *ProviderConfig {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Headers = CloneStringMap(src.Headers)
	dst.CacheControl = CloneBoolPtr(src.CacheControl)
	dst.Responses = cloneResponsesConfig(src.Responses)
	dst.Models = cloneModelConfigs(src.Models)
	dst.fieldSet = cloneFieldSet(src.fieldSet)
	return &dst
}

func cloneResponsesConfig(src ResponsesConfig) ResponsesConfig {
	src.Metadata = CloneStringMap(src.Metadata)
	src.PromptCacheEnabled = CloneBoolPtr(src.PromptCacheEnabled)
	src.Store = CloneBoolPtr(src.Store)
	src.Background = CloneBoolPtr(src.Background)
	src.Include = CloneStringSlice(src.Include)
	src.StructuredOutput.Strict = CloneBoolPtr(src.StructuredOutput.Strict)
	src.StructuredOutput.Schema = cloneRawMessage(src.StructuredOutput.Schema)
	src.ToolControl.Parallel = CloneBoolPtr(src.ToolControl.Parallel)
	src.HostedTools.WebSearch = cloneAnyMap(src.HostedTools.WebSearch)
	src.HostedTools.FileSearch = cloneAnyMap(src.HostedTools.FileSearch)
	src.HostedTools.CodeInterpreter = cloneAnyMap(src.HostedTools.CodeInterpreter)
	src.HostedTools.ComputerUse = cloneAnyMap(src.HostedTools.ComputerUse)
	src.HostedTools.ImageGeneration = cloneAnyMap(src.HostedTools.ImageGeneration)
	src.HostedTools.RemoteMCP = cloneAnyMapSlice(src.HostedTools.RemoteMCP)
	return src
}

func cloneRawMessage(src json.RawMessage) json.RawMessage {
	if src == nil {
		return nil
	}
	dst := make(json.RawMessage, len(src))
	copy(dst, src)
	return dst
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return nil
	}
	var dst map[string]any
	if err := json.Unmarshal(data, &dst); err != nil {
		return nil
	}
	return dst
}

func cloneAnyMapSlice(src []map[string]any) []map[string]any {
	if src == nil {
		return nil
	}
	dst := make([]map[string]any, len(src))
	for i := range src {
		dst[i] = cloneAnyMap(src[i])
	}
	return dst
}

func cloneModelConfigs(src []ModelConfig) []ModelConfig {
	if src == nil {
		return nil
	}
	out := make([]ModelConfig, len(src))
	for i, model := range src {
		out[i] = cloneModelConfig(model)
	}
	return out
}

func cloneModelConfig(src ModelConfig) ModelConfig {
	src.Temperature = CloneFloat64Ptr(src.Temperature)
	src.TopP = CloneFloat64Ptr(src.TopP)
	if src.Cost != nil {
		cost := *src.Cost
		src.Cost = &cost
	}
	src.Input = CloneStringSlice(src.Input)
	src.Compat = cloneModelCompat(src.Compat)
	src.fieldSet = cloneFieldSet(src.fieldSet)
	return src
}

func cloneFieldSet(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneModelCompat(src *ModelCompat) *ModelCompat {
	if src == nil {
		return nil
	}
	dst := *src
	dst.SupportsDeveloperRole = CloneBoolPtr(src.SupportsDeveloperRole)
	dst.SupportsStore = CloneBoolPtr(src.SupportsStore)
	dst.SupportsResponses = CloneBoolPtr(src.SupportsResponses)
	dst.SupportsPreviousResponseID = CloneBoolPtr(src.SupportsPreviousResponseID)
	dst.SupportsConversation = CloneBoolPtr(src.SupportsConversation)
	dst.SupportsBackground = CloneBoolPtr(src.SupportsBackground)
	dst.SupportsStructuredOutput = CloneBoolPtr(src.SupportsStructuredOutput)
	dst.SupportsServiceTier = CloneBoolPtr(src.SupportsServiceTier)
	dst.SupportsParallelToolCalls = CloneBoolPtr(src.SupportsParallelToolCalls)
	dst.SupportsToolChoice = CloneBoolPtr(src.SupportsToolChoice)
	if src.SupportsHostedTools != nil {
		dst.SupportsHostedTools = make(map[string]bool, len(src.SupportsHostedTools))
		for key, value := range src.SupportsHostedTools {
			dst.SupportsHostedTools[key] = value
		}
	}
	dst.SupportedInclude = CloneStringSlice(src.SupportedInclude)
	dst.SupportsReasoningEffort = CloneBoolPtr(src.SupportsReasoningEffort)
	dst.SupportsStrictMode = CloneBoolPtr(src.SupportsStrictMode)
	dst.SupportsCacheControlOnTools = CloneBoolPtr(src.SupportsCacheControlOnTools)
	dst.SupportsLongCacheRetention = CloneBoolPtr(src.SupportsLongCacheRetention)
	dst.SupportsPromptCacheKey = CloneBoolPtr(src.SupportsPromptCacheKey)
	dst.SupportsReasoningSummary = CloneBoolPtr(src.SupportsReasoningSummary)
	dst.SupportsEagerToolInputStreaming = CloneBoolPtr(src.SupportsEagerToolInputStreaming)
	dst.DisableSamplingParams = CloneBoolPtr(src.DisableSamplingParams)
	return &dst
}

// CloneStringMap returns a deep copy of a string map, or nil if src is nil.
func CloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// CloneStringSlice returns a deep copy of a string slice, or nil if src is nil.
func CloneStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// CloneBoolPtr returns a deep copy of a bool pointer, or nil if src is nil.
func CloneBoolPtr(src *bool) *bool {
	if src == nil {
		return nil
	}
	v := *src
	return &v
}

// CloneFloat64Ptr returns a deep copy of a float64 pointer, or nil if src is nil.
func CloneFloat64Ptr(src *float64) *float64 {
	if src == nil {
		return nil
	}
	v := *src
	return &v
}

// NormalizeSamplingPtr returns nil if src points to zero; otherwise returns a clone of src.
// This prevents zero-valued temperature/top_p from being serialized to API requests,
// which some providers reject.
func NormalizeSamplingPtr(src *float64) *float64 {
	if src == nil {
		return nil
	}
	if *src == 0 {
		return nil
	}
	return CloneFloat64Ptr(src)
}

func boolPtr(v bool) *bool {
	return &v
}

func ConfigDir() string {
	return platform.ConfigDir()
}

func GlobalSettingsPath() string {
	return filepath.Join(ConfigDir(), "settings.json")
}

func ProjectSettingsPath() string {
	return ProjectPath("settings.json")
}

func LoadSettings() (*Settings, error) {
	s, _, err := LoadSettingsWithMeta()
	return s, err
}

// LoadSettingsFor loads the same settings schema as LoadSettings, but applies
// project settings from cwd instead of the process working directory. It is
// intentionally side-effect free so diagnostics can inspect an arbitrary cwd
// without changing process-global state or creating a config file.
func LoadSettingsFor(cwd string) (*Settings, error) {
	if cwd == "" {
		cwd = "."
	}
	s := DefaultSettings()
	globalPath := GlobalSettingsPath()
	if data, err := os.ReadFile(globalPath); err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			return nil, fmt.Errorf("parse global settings %s: %w", globalPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read global settings %s: %w", globalPath, err)
	}

	projectPath := ProjectPathFor(cwd, "settings.json")
	if data, err := os.ReadFile(projectPath); err == nil {
		candidate := *s
		if err := json.Unmarshal(data, &candidate); err != nil {
			return nil, fmt.Errorf("parse project settings %s: %w", projectPath, err)
		}
		s = &candidate
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read project settings %s: %w", projectPath, err)
	}

	if v := os.Getenv("VIBECODING_PROVIDER"); v != "" {
		s.DefaultProvider = v
	}
	if v := os.Getenv("VIBECODING_MODEL"); v != "" {
		s.DefaultModel = v
	}
	if v := os.Getenv("VIBECODING_MODE"); v != "" {
		s.DefaultMode = v
	}
	if v := os.Getenv("VIBECODING_THINKING"); v != "" {
		s.DefaultThinkingLevel = v
	}
	return s, nil
}

// LoadMeta describes side effects and paths from settings loading.
type LoadMeta struct {
	CreatedGlobalConfig bool
	GlobalSettingsPath  string
}

// LoadSettingsWithMeta loads settings and reports whether the global settings
// file was created during this call. The loaded schema is the same as LoadSettings.
func LoadSettingsWithMeta() (*Settings, LoadMeta, error) {
	s := DefaultSettings()
	meta := LoadMeta{GlobalSettingsPath: GlobalSettingsPath()}

	created, err := ensureConfigExists()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create config: %v\n", err)
	} else {
		meta.CreatedGlobalConfig = created
	}

	globalPath := GlobalSettingsPath()
	if Verbose {
		fmt.Fprintf(os.Stderr, "[config] Loading global settings: %s\n", globalPath)
	}
	if data, err := os.ReadFile(globalPath); err == nil {
		candidate := *s
		if err := json.Unmarshal(data, &candidate); err != nil {
			if backupErr := backupCorruptSettings(globalPath); backupErr != nil {
				return nil, meta, fmt.Errorf("parse global settings: %w (backup failed: %v)", err, backupErr)
			}
			fmt.Fprintf(os.Stderr, "Warning: invalid global settings backed up; using defaults: %v\n", err)
		} else {
			s = &candidate
		}
		if Verbose {
			fmt.Fprintf(os.Stderr, "[config] Loaded global settings\n")
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Warning: could not read global settings %s: %v\n", globalPath, err)
	} else if Verbose {
		fmt.Fprintf(os.Stderr, "[config] Global settings not found: %s\n", globalPath)
	}

	projectPath := ProjectSettingsPath()
	if Verbose {
		fmt.Fprintf(os.Stderr, "[config] Loading project settings: %s\n", projectPath)
	}
	if data, err := os.ReadFile(projectPath); err == nil {
		candidate := *s
		if err := json.Unmarshal(data, &candidate); err != nil {
			if backupErr := backupCorruptSettings(projectPath); backupErr != nil {
				return nil, meta, fmt.Errorf("parse project settings: %w (backup failed: %v)", err, backupErr)
			}
			fmt.Fprintf(os.Stderr, "Warning: invalid project settings backed up and ignored: %v\n", err)
		} else {
			s = &candidate
		}
		if Verbose {
			fmt.Fprintf(os.Stderr, "[config] Loaded project settings\n")
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Warning: could not read project settings %s: %v\n", projectPath, err)
	} else if Verbose {
		fmt.Fprintf(os.Stderr, "[config] Project settings not found: %s\n", projectPath)
		// Detect common typo: .mothx/setting.json (singular)
		if _, err2 := os.Stat(ProjectPath("setting.json")); err2 == nil {
			fmt.Fprintf(os.Stderr, "[config] Found %s (singular) — expected %s (plural). Please rename the file.\n", ProjectPath("setting.json"), projectPath)
		}
	}

	if v := os.Getenv("VIBECODING_PROVIDER"); v != "" {
		s.DefaultProvider = v
	}
	if v := os.Getenv("VIBECODING_MODEL"); v != "" {
		s.DefaultModel = v
	}
	if v := os.Getenv("VIBECODING_MODE"); v != "" {
		s.DefaultMode = v
	}
	if v := os.Getenv("VIBECODING_THINKING"); v != "" {
		s.DefaultThinkingLevel = v
	}

	return s, meta, nil
}

func backupCorruptSettings(path string) error {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve settings path %s: %w", path, err)
	}

	stamp := time.Now().Format("20060102-150405")
	backupPath := absolutePath + ".bak_" + stamp
	for i := 1; ; i++ {
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			break
		}
		backupPath = fmt.Sprintf("%s.bak_%s_%d", absolutePath, stamp, i)
	}
	if err := os.Rename(absolutePath, backupPath); err != nil {
		return fmt.Errorf("rename %s to %s: %w", absolutePath, backupPath, err)
	}
	fmt.Fprintf(os.Stderr, "Warning: corrupt settings backed up to %s\n", backupPath)
	return nil
}

func ensureConfigExists() (bool, error) {
	configDir := ConfigDir()
	settingsPath := GlobalSettingsPath()

	if _, err := os.Stat(settingsPath); err == nil {
		return false, nil
	}

	if err := os.MkdirAll(configDir, 0700); err != nil {
		return false, fmt.Errorf("create config directory: %w", err)
	}

	data, err := json.MarshalIndent(defaultSettingsFile(), "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal default settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, data, 0600); err != nil {
		return false, fmt.Errorf("write settings file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Created default config: %s\n", settingsPath)
	return true, nil
}

// LoadGlobalSettingsOrDefault loads only the global settings file over defaults.
// It intentionally does not apply project settings or environment overrides, so
// callers that need a full runnable global config can avoid persisting runtime state.
func LoadGlobalSettingsOrDefault() (*Settings, error) {
	s := DefaultSettings()
	globalPath := GlobalSettingsPath()
	if data, err := os.ReadFile(globalPath); err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			return nil, fmt.Errorf("parse global settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read global settings %s: %w", globalPath, err)
	}
	return s, nil
}

// LoadGlobalSettingsSparse loads only fields explicitly present in the global
// settings file. If the file does not exist, it returns an empty Settings.
// Use this for patch-style writes so defaults are not expanded into settings.json.
func LoadGlobalSettingsSparse() (*Settings, error) {
	s := &Settings{}
	globalPath := GlobalSettingsPath()
	if data, err := os.ReadFile(globalPath); err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			return nil, fmt.Errorf("parse global settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read global settings %s: %w", globalPath, err)
	}
	if s.Providers == nil {
		s.Providers = map[string]*ProviderConfig{}
	}
	return s, nil
}

// LoadProjectSettingsSparse loads only fields explicitly present in the project
// settings file. If the file does not exist, it returns an empty Settings.
func LoadProjectSettingsSparse() (*Settings, error) {
	s := &Settings{}
	projectPath := ProjectSettingsPath()
	if data, err := os.ReadFile(projectPath); err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			return nil, fmt.Errorf("parse project settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read project settings %s: %w", projectPath, err)
	}
	if s.Providers == nil {
		s.Providers = map[string]*ProviderConfig{}
	}
	return s, nil
}

// SaveGlobalSettings writes settings.json atomically with private permissions.
func SaveGlobalSettings(s *Settings) error {
	if s == nil {
		return fmt.Errorf("settings is nil")
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	return writeGlobalSettingsData(data)
}

// SaveGlobalSettingsPatch updates only the given top-level keys in the global
// settings file. It preserves keys that are already present without expanding
// defaults into settings.json.
func SaveGlobalSettingsPatch(updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	existing := map[string]json.RawMessage{}
	settingsPath := GlobalSettingsPath()
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parse global settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read global settings %s: %w", settingsPath, err)
	}
	// Output limits are configured per model. Drop the retired global setting
	// whenever this sparse file is rewritten.
	delete(existing, "maxOutputTokens")
	for key, value := range updates {
		if key == "" {
			continue
		}
		if value == nil {
			delete(existing, key)
			continue
		}
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("marshal settings key %s: %w", key, err)
		}
		existing[key] = data
	}
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings patch: %w", err)
	}
	return writeGlobalSettingsData(data)
}

func writeGlobalSettingsData(data []byte) error {
	configDir := ConfigDir()
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	settingsPath := GlobalSettingsPath()
	tmp, err := os.CreateTemp(configDir, "settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp settings: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp settings: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp settings: %w", err)
	}
	if err := os.Rename(tmpName, settingsPath); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}

func IsProjectDir(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	for _, marker := range []string{".git", ProjectDirName, "go.mod", "package.json", "pyproject.toml", "Cargo.toml"} {
		if _, err := os.Stat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}

// SaveProjectSettingsPatch updates only the given top-level keys in the project
// settings file without expanding defaults.
func SaveProjectSettingsPatch(updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	projectDir := filepath.Dir(ProjectSettingsPath())
	if err := os.MkdirAll(projectDir, 0700); err != nil {
		return fmt.Errorf("create project config directory: %w", err)
	}
	existing := map[string]json.RawMessage{}
	settingsPath := ProjectSettingsPath()
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parse project settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read project settings %s: %w", settingsPath, err)
	}
	delete(existing, "maxOutputTokens")
	for key, value := range updates {
		if key == "" {
			continue
		}
		if value == nil {
			delete(existing, key)
			continue
		}
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("marshal settings key %s: %w", key, err)
		}
		existing[key] = data
	}
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal project settings patch: %w", err)
	}
	tmp, err := os.CreateTemp(projectDir, "settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp project settings: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp project settings: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp project settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp project settings: %w", err)
	}
	if err := os.Rename(tmpName, settingsPath); err != nil {
		return fmt.Errorf("replace project settings: %w", err)
	}
	return nil
}

// SaveProjectSettings writes .mothx/settings.json atomically with private permissions.
func SaveProjectSettings(s *Settings) error {
	if s == nil {
		return fmt.Errorf("settings is nil")
	}
	projectDir := filepath.Dir(ProjectSettingsPath())
	if err := os.MkdirAll(projectDir, 0700); err != nil {
		return fmt.Errorf("create project config directory: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	settingsPath := ProjectSettingsPath()
	tmp, err := os.CreateTemp(projectDir, "settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp settings: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp settings: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp settings: %w", err)
	}
	if err := os.Rename(tmpName, settingsPath); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}

func (s *Settings) ResolveKey(providerName string) string {
	// 1. Use apiKey from provider config (supports ${VAR} env references)
	if pc, ok := s.Providers[providerName]; ok && pc != nil && pc.APIKey != "" {
		return resolveKeyValue(pc.APIKey)
	}
	// 2. Fallback: apiKey from the built-in provider preset (supports ${VAR}
	// env references). Unresolved ${VAR} placeholders fall through so the
	// derived env-var lookup below still applies.
	if pc := DefaultProviderConfig(providerName); pc != nil && pc.APIKey != "" {
		if v := resolveKeyValue(pc.APIKey); v != "" && !strings.HasPrefix(v, "${") && !strings.HasPrefix(v, "!") {
			return v
		}
	}
	// 3. Fallback: derive env var from provider name, e.g. "deepseek-openai" → "DEEPSEEK_OPENAI_API_KEY"
	envVar := providerToEnvVar(providerName)
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return ""
}

// ResolveProviderHeaders resolves configured per-provider HTTP header values.
// Header values use the same env-var and shell-command resolution rules as apiKey.
func (s *Settings) ResolveProviderHeaders(providerName string) map[string]string {
	if s == nil {
		return nil
	}
	// Merge built-in preset headers (e.g. kimi-coding's User-Agent) with the
	// runtime provider entry; a configured headers map replaces the preset.
	pc := ResolveProviderConfig(providerName, s)
	if pc == nil || len(pc.Headers) == 0 {
		return nil
	}
	headers := make(map[string]string, len(pc.Headers))
	for name, value := range pc.Headers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		headers[name] = resolveKeyValue(value)
	}
	return headers
}

// providerToEnvVar converts a provider name to a conventional environment variable name.
// e.g. "deepseek-openai" → "DEEPSEEK_OPENAI_API_KEY", "my-provider" → "MY_PROVIDER_API_KEY".
func providerToEnvVar(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_API_KEY"
}

func resolveKeyValue(key string) string {
	if strings.HasPrefix(key, "!") {
		if os.Getenv("VIBECODING_ALLOW_SHELL_CONFIG") != "1" {
			return key
		}
		return resolveShellCommand(key[1:])
	}

	// Handle ${VAR} syntax: look up the variable name inside ${}
	envName := key
	if strings.HasPrefix(key, "${") && strings.HasSuffix(key, "}") {
		envName = key[2 : len(key)-1]
	}

	if !strings.Contains(envName, " ") {
		if v := os.Getenv(envName); v != "" {
			return v
		}
	}

	return key
}

func (s *Settings) GetProviderConfig(name string) *ProviderConfig {
	return s.Providers[name]
}

func (s *Settings) GetModelConfig(providerName, modelID string) *ModelConfig {
	pc := s.GetProviderConfig(providerName)
	if pc == nil {
		return nil
	}
	for _, m := range pc.Models {
		if m.ID == modelID {
			return &m
		}
	}
	return nil
}

func resolveShellCommand(cmd string) string {
	if cmd == "" {
		return ""
	}
	var out []byte
	var err error
	if platform.IsWindows() {
		out, err = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", cmd).Output()
	} else {
		out, err = exec.Command("sh", "-c", cmd).Output()
	}
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (s *Settings) GetShell() string {
	if s.ShellPath != "" {
		return s.ShellPath
	}
	return platform.DefaultShell()
}

func (s *Settings) GetSessionDir() string {
	if s.SessionDir != "" {
		return platform.ExpandHome(s.SessionDir)
	}
	return platform.SessionDir()
}

func (s *Settings) GetGlobalSkillsDir() string {
	if s.SkillsDir != "" {
		return platform.ExpandHome(s.SkillsDir)
	}
	return platform.SkillsDir()
}

func (s *Settings) IsPlanToolEnabled() bool {
	if s.EnablePlanTool == nil {
		return true
	}
	return *s.EnablePlanTool
}

// IsArtifactEnabled reports whether terminal (TUI and print-mode CLI)
// sessions may publish generated artifacts. The capability is opt-in.
func (s *Settings) IsArtifactEnabled() bool {
	return s != nil && s.EnableArtifact != nil && *s.EnableArtifact
}

// IsACPArtifactEnabled reports whether ACP/Desktop sessions may publish
// generated artifacts. It is intentionally independent from terminal use.
func (s *Settings) IsACPArtifactEnabled() bool {
	return s != nil && s.EnableACPArtifact != nil && *s.EnableACPArtifact
}

// IsUpdateCheckEnabled reports whether startup update checks against the npm
// registry are enabled. Defaults to true when unset.
func (s *Settings) IsUpdateCheckEnabled() bool {
	if s == nil || s.UpdateCheck == nil {
		return true
	}
	return *s.UpdateCheck
}

// IsAttachmentStorageReclaimEnabled reports whether the Runtime-owned
// reconciliation of unreferenced attachment storage is scheduled and allowed to
// run. It defaults to true when unset, matching updateCheck: the work is bounded
// by the attachment retention window and only removes bytes the durable store no
// longer claims, so it is on unless an operator turns it off.
func (s *Settings) IsAttachmentStorageReclaimEnabled() bool {
	if s == nil || s.Maintenance == nil || s.Maintenance.ReclaimAttachmentStorage == nil {
		return true
	}
	return *s.Maintenance.ReclaimAttachmentStorage
}

// AttachmentStorageReclaimSchedule returns the configured cadence for the
// attachment storage reconciliation, or "" when the Runtime default applies.
// The value is intentionally not validated here: the scheduler that owns the
// schedule grammar validates it, so config does not grow a second parser.
func (s *Settings) AttachmentStorageReclaimSchedule() string {
	if s == nil || s.Maintenance == nil {
		return ""
	}
	return strings.TrimSpace(s.Maintenance.StorageReconcileSchedule)
}

func (s *Settings) IsWebSearchEnabled() bool {
	if s == nil || s.WebSearch.Enabled == nil {
		return false
	}
	return *s.WebSearch.Enabled
}

func (s *Settings) IsImageGenerationEnabled() bool {
	if s == nil || s.ImageGeneration.Enabled == nil {
		return false
	}
	return *s.ImageGeneration.Enabled
}

// EffectiveImageGeneration returns the standalone image-generation config,
// filling omitted endpoint/API/token fields from the selected provider.
func (s *Settings) EffectiveImageGeneration() ImageGenerationSettings {
	if s == nil {
		return ImageGenerationSettings{}
	}
	cfg := s.ImageGeneration
	if cfg.Provider == "" {
		cfg.Provider = "openai"
	}
	if pc := ResolveProviderConfig(cfg.Provider, s); pc != nil {
		if cfg.BaseURL == "" {
			cfg.BaseURL = pc.BaseURL
		}
		if cfg.APIType == "" {
			cfg.APIType = pc.API
		}
		if cfg.Token == "" {
			cfg.Token = pc.APIKey
		}
	}
	if cfg.APIType == "" {
		cfg.APIType = "openai-images"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-image-1"
	}
	cfg.Token = resolveKeyValue(cfg.Token)
	return cfg
}

// ResolveImageGenerationToken resolves environment/shell references in the
// standalone image-generation token without exposing the resolver itself.
func (s *Settings) ResolveImageGenerationToken() string {
	return s.EffectiveImageGeneration().Token
}

func mergeWebSearchSettings(base, override WebSearchSettings) WebSearchSettings {
	if override.Enabled != nil {
		base.Enabled = boolPtr(*override.Enabled)
	}
	if override.Provider != "" {
		base.Provider = override.Provider
		if override.ProviderType == "" {
			base.ProviderType = ""
		}
	}
	if override.ProviderType != "" {
		base.ProviderType = override.ProviderType
	}
	if override.Model != "" {
		base.Model = override.Model
	}
	return normalizeWebSearchSettings(base)
}

func normalizeWebSearchSettings(cfg WebSearchSettings) WebSearchSettings {
	if cfg.Enabled == nil {
		cfg.Enabled = boolPtr(false)
	}
	if cfg.Provider == "" {
		cfg.Provider = "openai"
	}
	if cfg.ProviderType == "" {
		switch cfg.Provider {
		case "anthropic":
			cfg.ProviderType = "anthropic-messages"
		default:
			cfg.ProviderType = "openai-responses"
		}
	}
	return cfg
}

// DefaultProviderConfigs returns a deep copy of all built-in provider presets.
// The returned map is safe for callers to modify without affecting the global defaults.
func DefaultProviderConfigs() map[string]*ProviderConfig {
	return cloneProviderConfigs(defaultProviderConfigs)
}

// DefaultProviderConfig returns a deep copy of a single built-in provider preset,
// or nil if the provider ID has no built-in default.
func DefaultProviderConfig(providerID string) *ProviderConfig {
	src, ok := defaultProviderConfigs[providerID]
	if !ok || src == nil {
		return nil
	}
	return cloneProviderConfig(src)
}

// DefaultModelConfig returns a deep copy of a specific model's built-in config
// under a given provider. Returns nil if the provider or model is unknown.
func DefaultModelConfig(providerID, modelID string) *ModelConfig {
	pc, ok := defaultProviderConfigs[providerID]
	if !ok || pc == nil {
		return nil
	}
	for i := range pc.Models {
		if pc.Models[i].ID == modelID {
			cm := cloneModelConfig(pc.Models[i])
			return &cm
		}
	}
	return nil
}

// ResolveProviderConfig merges built-in provider defaults with runtime overrides.
// Priority: runtime settings > built-in defaults > safe generic defaults.
func ResolveProviderConfig(providerID string, runtime *Settings) *ProviderConfig {
	base := DefaultProviderConfig(providerID)
	if base == nil {
		base = &ProviderConfig{API: "openai-chat"}
	}
	if runtime != nil {
		if existing, ok := runtime.Providers[providerID]; ok && existing != nil {
			base = mergeProviderConfig(base, existing)
		}
	}
	return base
}

// ResolveModelConfig merges built-in model defaults with runtime overrides.
func ResolveModelConfig(providerID, modelID string, runtime *Settings) *ModelConfig {
	base := DefaultModelConfig(providerID, modelID)
	if runtime != nil && runtime.Providers != nil {
		if existing := runtime.GetModelConfig(providerID, modelID); existing != nil {
			if base == nil {
				cm := cloneModelConfig(*existing)
				return &cm
			}
			merged := mergeModelConfig(*base, *existing)
			return &merged
		}
	}
	if base != nil {
		return base
	}
	if runtime != nil {
		if existing := runtime.GetModelConfig(providerID, modelID); existing != nil {
			cm := cloneModelConfig(*existing)
			return &cm
		}
	}
	return nil
}

// mergeProviderConfig overlays non-zero fields from `overlay` onto `base`.
// nil *bool fields in overlay are treated as "unset" and do not overwrite base.
func mergeProviderConfig(base, overlay *ProviderConfig) *ProviderConfig {
	if overlay == nil {
		return base
	}
	if base == nil {
		return cloneProviderConfig(overlay)
	}
	result := cloneProviderConfig(base)
	if configFieldWasSet(overlay.fieldSet, "apiKey") || (overlay.fieldSet == nil && overlay.APIKey != "") {
		result.APIKey = overlay.APIKey
	}
	if configFieldWasSet(overlay.fieldSet, "baseUrl") || (overlay.fieldSet == nil && overlay.BaseURL != "") {
		result.BaseURL = overlay.BaseURL
	}
	if configFieldWasSet(overlay.fieldSet, "api") || (overlay.fieldSet == nil && overlay.API != "") {
		result.API = overlay.API
	}
	if configFieldWasSet(overlay.fieldSet, "vendor") || (overlay.fieldSet == nil && overlay.Vendor != "") {
		result.Vendor = overlay.Vendor
	}
	if configFieldWasSet(overlay.fieldSet, "httpProxy") || (overlay.fieldSet == nil && overlay.HTTPProxy != "") {
		result.HTTPProxy = overlay.HTTPProxy
	}
	if configFieldWasSet(overlay.fieldSet, "forceHTTP11") || (overlay.fieldSet == nil && overlay.ForceHTTP11) {
		result.ForceHTTP11 = overlay.ForceHTTP11
	}
	if configFieldWasSet(overlay.fieldSet, "thinkingFormat") || (overlay.fieldSet == nil && overlay.ThinkingFormat != "") {
		result.ThinkingFormat = overlay.ThinkingFormat
	}
	if configFieldWasSet(overlay.fieldSet, "cacheControl") || (overlay.fieldSet == nil && overlay.CacheControl != nil) {
		result.CacheControl = CloneBoolPtr(overlay.CacheControl)
	}
	if configFieldWasSet(overlay.fieldSet, "maxImagesPerRequest") || (overlay.fieldSet == nil && overlay.MaxImagesPerRequest != 0) {
		result.MaxImagesPerRequest = overlay.MaxImagesPerRequest
	}
	if configFieldWasSet(overlay.fieldSet, "headers") || (overlay.fieldSet == nil && len(overlay.Headers) > 0) {
		result.Headers = CloneStringMap(overlay.Headers)
	}
	if configFieldWasSet(overlay.fieldSet, "responses") || responsesConfigHasValues(overlay.Responses) {
		result.Responses = cloneResponsesConfig(overlay.Responses)
	}
	if configFieldWasSet(overlay.fieldSet, "models") || (overlay.fieldSet == nil && len(overlay.Models) > 0) {
		result.Models = mergeModelConfigs(result.Models, overlay.Models)
	}
	return result
}

func responsesConfigHasValues(c ResponsesConfig) bool {
	return c.ReasoningSummary != "" ||
		c.ReasoningContext != "" ||
		c.ReasoningMode != "" ||
		c.PromptCacheEnabled != nil ||
		c.PromptCacheKey != "" ||
		c.PromptCacheRetention != "" ||
		c.PromptCacheMode != "" ||
		c.PromptCacheTTL != "" ||
		c.SafetyIdentifier != "" ||
		len(c.Metadata) > 0 ||
		c.StateMode != "" ||
		c.Store != nil ||
		c.Conversation != "" ||
		c.Truncation != "" ||
		c.Background != nil ||
		len(c.Include) > 0 ||
		c.ServiceTier != "" ||
		c.StructuredOutput.Name != "" ||
		c.StructuredOutput.Description != "" ||
		c.StructuredOutput.Strict != nil ||
		len(c.StructuredOutput.Schema) > 0 ||
		c.ToolControl.Choice != "" ||
		c.ToolControl.Parallel != nil ||
		c.ToolControl.MaxCalls != 0 ||
		len(c.HostedTools.WebSearch) > 0 ||
		len(c.HostedTools.FileSearch) > 0 ||
		len(c.HostedTools.CodeInterpreter) > 0 ||
		len(c.HostedTools.ComputerUse) > 0 ||
		len(c.HostedTools.ImageGeneration) > 0 ||
		len(c.HostedTools.RemoteMCP) > 0
}

// mergeModelConfigs combines built-in and runtime model lists by model ID.
// Runtime entries take precedence for matching IDs; built-in-only entries remain
// available with their complete preset parameters. Runtime order is preserved,
// followed by built-in models that were not configured explicitly.
func mergeModelConfigs(builtin, runtime []ModelConfig) []ModelConfig {
	result := make([]ModelConfig, 0, len(runtime)+len(builtin))
	seen := make(map[string]struct{}, len(runtime)+len(builtin))
	for _, model := range runtime {
		if model.ID == "" {
			continue
		}
		result = append(result, cloneModelConfig(model))
		seen[model.ID] = struct{}{}
	}
	for _, model := range builtin {
		if model.ID == "" {
			continue
		}
		if _, exists := seen[model.ID]; exists {
			continue
		}
		result = append(result, cloneModelConfig(model))
		seen[model.ID] = struct{}{}
	}
	return result
}

// mergeModelConfig overlays non-zero fields from `overlay` onto `base`.
func mergeModelConfig(base, overlay ModelConfig) ModelConfig {
	result := cloneModelConfig(base)
	if configFieldWasSet(overlay.fieldSet, "id") || (overlay.fieldSet == nil && overlay.ID != "") {
		result.ID = overlay.ID
	}
	if configFieldWasSet(overlay.fieldSet, "name") || (overlay.fieldSet == nil && overlay.Name != "") {
		result.Name = overlay.Name
	}
	if configFieldWasSet(overlay.fieldSet, "contextWindow") || (overlay.fieldSet == nil && overlay.ContextWindow > 0) {
		result.ContextWindow = overlay.ContextWindow
	}
	if configFieldWasSet(overlay.fieldSet, "maxTokens") || (overlay.fieldSet == nil && overlay.MaxTokens > 0) {
		result.MaxTokens = overlay.MaxTokens
		result.fieldSet = markConfigField(result.fieldSet, "maxTokens")
	}
	if configFieldWasSet(overlay.fieldSet, "reasoning") || (overlay.fieldSet == nil && overlay.Reasoning) {
		result.Reasoning = overlay.Reasoning
	}
	if configFieldWasSet(overlay.fieldSet, "input") || (overlay.fieldSet == nil && len(overlay.Input) > 0) {
		result.Input = CloneStringSlice(overlay.Input)
	}
	if configFieldWasSet(overlay.fieldSet, "temperature") || (overlay.fieldSet == nil && overlay.Temperature != nil) {
		result.Temperature = CloneFloat64Ptr(overlay.Temperature)
	}
	if configFieldWasSet(overlay.fieldSet, "top_p") || (overlay.fieldSet == nil && overlay.TopP != nil) {
		result.TopP = CloneFloat64Ptr(overlay.TopP)
	}
	if configFieldWasSet(overlay.fieldSet, "cost") || (overlay.fieldSet == nil && overlay.Cost != nil) {
		if overlay.Cost == nil {
			result.Cost = nil
		} else {
			c := *overlay.Cost
			result.Cost = &c
		}
	}
	if configFieldWasSet(overlay.fieldSet, "compat") || (overlay.fieldSet == nil && overlay.Compat != nil) {
		result.Compat = cloneModelCompat(overlay.Compat)
	}
	return result
}

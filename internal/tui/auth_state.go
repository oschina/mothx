package tui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/oschina/mothx/internal/config"
)

// providerEditState holds editable fields for a ProviderConfig.
// It mirrors config.ProviderConfig but adds UI-specific metadata.
type providerEditState struct {
	APIKey              string
	BaseURL             string
	API                 string
	Vendor              string
	HTTPProxy           string
	ForceHTTP11         bool
	Headers             map[string]string // nil means "no custom headers"
	ThinkingFormat      string            // "", "openai", "anthropic", "deepseek", "xiaomi", "zai"
	CacheControl        *bool             // nil = auto, true = on, false = off
	MaxImagesPerRequest int               // 0 = provider default, -1 = unlimited

	// Responses sub-config
	Responses responsesEditState
}

type responsesEditState struct {
	ReasoningSummary     string // "auto", "concise", "detailed"
	PromptCacheEnabled   *bool
	PromptCacheKey       string
	PromptCacheRetention string
	StateMode            string
	Store                *bool
	Conversation         string
	Truncation           string
	Background           *bool
	Include              []string
	ServiceTier          string
	StructuredOutput     config.ResponsesStructuredOutputConfig
	ToolControl          config.ResponsesToolControlConfig
	HostedTools          config.ResponsesHostedToolsConfig
}

// Compat mirrors config.ModelCompat for editing.
type compatEditState struct {
	ThinkingFormat                              string
	RequiresReasoningContentOnAssistant         bool
	RequiresReasoningContentOnAssistantMessages bool
	ForceAdaptiveThinking                       bool
	ParseReasoningInContent                     bool
	SupportsDeveloperRole                       *bool
	SupportsStore                               *bool
	SupportsReasoningEffort                     *bool
	SupportsStrictMode                          *bool
	MaxTokensField                              string
	DisableSamplingParams                       *bool
	SupportsCacheControlOnTools                 *bool
	SupportsLongCacheRetention                  *bool
	SupportsPromptCacheKey                      *bool
	SupportsReasoningSummary                    *bool
	SendSessionAffinityHeaders                  bool
	SupportsEagerToolInputStreaming             *bool
	SupportsToolChoice                          *bool
	SupportsParallelToolCalls                   *bool

	// Track whether any compat field was explicitly edited
	Active bool
}

// modelEditState holds editable fields for a single ModelConfig.
type modelEditState struct {
	ID   string
	Name string

	ContextWindow   int
	MaxTokens       int
	MaxTokensEdited bool
	Reasoning       bool
	Input           []string

	Temperature *float64
	TopP        *float64

	CostEnabled bool
	CostInput   float64
	CostOutput  float64
	CacheRead   float64
	CacheWrite  float64

	Compat compatEditState
}

// --- Conversion helpers ---

func providerEditStateFrom(pc *config.ProviderConfig) providerEditState {
	if pc == nil {
		return providerEditState{API: "openai-chat"}
	}
	pe := providerEditState{
		APIKey:              pc.APIKey,
		BaseURL:             pc.BaseURL,
		API:                 pc.API,
		Vendor:              pc.Vendor,
		HTTPProxy:           pc.HTTPProxy,
		ForceHTTP11:         pc.ForceHTTP11,
		Headers:             config.CloneStringMap(pc.Headers),
		ThinkingFormat:      pc.ThinkingFormat,
		CacheControl:        config.CloneBoolPtr(pc.CacheControl),
		MaxImagesPerRequest: pc.MaxImagesPerRequest,
	}
	pe.Responses = responsesEditStateFrom(&pc.Responses)
	if pe.API == "" {
		pe.API = "openai-chat"
	}
	return pe
}

func responsesEditStateFrom(rc *config.ResponsesConfig) responsesEditState {
	if rc == nil {
		return responsesEditState{}
	}
	return responsesEditState{
		ReasoningSummary:     rc.ReasoningSummary,
		PromptCacheEnabled:   config.CloneBoolPtr(rc.PromptCacheEnabled),
		PromptCacheKey:       rc.PromptCacheKey,
		PromptCacheRetention: rc.PromptCacheRetention,
		StateMode:            rc.StateMode,
		Store:                config.CloneBoolPtr(rc.Store),
		Conversation:         rc.Conversation,
		Truncation:           rc.Truncation,
		Background:           config.CloneBoolPtr(rc.Background),
		Include:              config.CloneStringSlice(rc.Include),
		ServiceTier:          rc.ServiceTier,
		StructuredOutput:     rc.StructuredOutput,
		ToolControl:          rc.ToolControl,
		HostedTools:          rc.HostedTools,
	}
}

func compatEditStateFrom(mc *config.ModelCompat) compatEditState {
	if mc == nil {
		return compatEditState{}
	}
	ce := compatEditState{
		ThinkingFormat:                              mc.ThinkingFormat,
		RequiresReasoningContentOnAssistant:         mc.RequiresReasoningContentOnAssistant,
		RequiresReasoningContentOnAssistantMessages: mc.RequiresReasoningContentOnAssistantMessages,
		ForceAdaptiveThinking:                       mc.ForceAdaptiveThinking,
		ParseReasoningInContent:                     mc.ParseReasoningInContent,
		SupportsDeveloperRole:                       config.CloneBoolPtr(mc.SupportsDeveloperRole),
		SupportsStore:                               config.CloneBoolPtr(mc.SupportsStore),
		SupportsReasoningEffort:                     config.CloneBoolPtr(mc.SupportsReasoningEffort),
		SupportsStrictMode:                          config.CloneBoolPtr(mc.SupportsStrictMode),
		MaxTokensField:                              mc.MaxTokensField,
		DisableSamplingParams:                       config.CloneBoolPtr(mc.DisableSamplingParams),
		SupportsCacheControlOnTools:                 config.CloneBoolPtr(mc.SupportsCacheControlOnTools),
		SupportsLongCacheRetention:                  config.CloneBoolPtr(mc.SupportsLongCacheRetention),
		SupportsPromptCacheKey:                      config.CloneBoolPtr(mc.SupportsPromptCacheKey),
		SupportsReasoningSummary:                    config.CloneBoolPtr(mc.SupportsReasoningSummary),
		SendSessionAffinityHeaders:                  mc.SendSessionAffinityHeaders,
		SupportsEagerToolInputStreaming:             config.CloneBoolPtr(mc.SupportsEagerToolInputStreaming),
		SupportsToolChoice:                          config.CloneBoolPtr(mc.SupportsToolChoice),
		SupportsParallelToolCalls:                   config.CloneBoolPtr(mc.SupportsParallelToolCalls),
		Active:                                      true,
	}
	return ce
}

func modelEditStateFromMC(mc *config.ModelConfig) *modelEditState {
	if mc == nil {
		return nil
	}
	me := &modelEditState{
		ID:              mc.ID,
		Name:            mc.Name,
		ContextWindow:   mc.ContextWindow,
		MaxTokens:       mc.MaxTokens,
		MaxTokensEdited: mc.MaxTokensWasSet(),
		Reasoning:       mc.Reasoning,
		Input:           config.CloneStringSlice(mc.Input),
		Temperature:     config.CloneFloat64Ptr(mc.Temperature),
		TopP:            config.CloneFloat64Ptr(mc.TopP),
	}
	if mc.Cost != nil {
		me.CostEnabled = true
		me.CostInput = mc.Cost.Input
		me.CostOutput = mc.Cost.Output
		me.CacheRead = mc.Cost.CacheRead
		me.CacheWrite = mc.Cost.CacheWrite
	}
	if mc.Compat != nil {
		me.Compat = compatEditStateFrom(mc.Compat)
	}
	if me.Name == "" {
		me.Name = mc.ID
	}
	if len(me.Input) == 0 {
		me.Input = []string{"text"}
	}
	return me
}

// --- Write-back: convert edit states to config ---

func (pe *providerEditState) toConfig() config.ProviderConfig {
	pc := config.ProviderConfig{
		APIKey:              pe.APIKey,
		BaseURL:             pe.BaseURL,
		API:                 pe.API,
		Vendor:              pe.Vendor,
		HTTPProxy:           pe.HTTPProxy,
		ForceHTTP11:         pe.ForceHTTP11,
		Headers:             config.CloneStringMap(pe.Headers),
		ThinkingFormat:      pe.ThinkingFormat,
		CacheControl:        config.CloneBoolPtr(pe.CacheControl),
		MaxImagesPerRequest: pe.MaxImagesPerRequest,
	}
	pc.Responses = pe.Responses.toConfig()
	return pc
}

func (re *responsesEditState) toConfig() config.ResponsesConfig {
	return config.ResponsesConfig{
		ReasoningSummary:     re.ReasoningSummary,
		PromptCacheEnabled:   config.CloneBoolPtr(re.PromptCacheEnabled),
		PromptCacheKey:       re.PromptCacheKey,
		PromptCacheRetention: re.PromptCacheRetention,
		StateMode:            re.StateMode,
		Store:                config.CloneBoolPtr(re.Store),
		Conversation:         re.Conversation,
		Truncation:           re.Truncation,
		Background:           config.CloneBoolPtr(re.Background),
		Include:              config.CloneStringSlice(re.Include),
		ServiceTier:          re.ServiceTier,
		StructuredOutput:     re.StructuredOutput,
		ToolControl:          re.ToolControl,
		HostedTools:          re.HostedTools,
	}
}

func (ce *compatEditState) toConfig() *config.ModelCompat {
	if !ce.Active {
		return nil
	}
	return &config.ModelCompat{
		ThinkingFormat:                              ce.ThinkingFormat,
		RequiresReasoningContentOnAssistant:         ce.RequiresReasoningContentOnAssistant,
		RequiresReasoningContentOnAssistantMessages: ce.RequiresReasoningContentOnAssistantMessages,
		ForceAdaptiveThinking:                       ce.ForceAdaptiveThinking,
		ParseReasoningInContent:                     ce.ParseReasoningInContent,
		SupportsDeveloperRole:                       config.CloneBoolPtr(ce.SupportsDeveloperRole),
		SupportsStore:                               config.CloneBoolPtr(ce.SupportsStore),
		SupportsReasoningEffort:                     config.CloneBoolPtr(ce.SupportsReasoningEffort),
		SupportsStrictMode:                          config.CloneBoolPtr(ce.SupportsStrictMode),
		MaxTokensField:                              ce.MaxTokensField,
		DisableSamplingParams:                       config.CloneBoolPtr(ce.DisableSamplingParams),
		SupportsCacheControlOnTools:                 config.CloneBoolPtr(ce.SupportsCacheControlOnTools),
		SupportsLongCacheRetention:                  config.CloneBoolPtr(ce.SupportsLongCacheRetention),
		SupportsPromptCacheKey:                      config.CloneBoolPtr(ce.SupportsPromptCacheKey),
		SupportsReasoningSummary:                    config.CloneBoolPtr(ce.SupportsReasoningSummary),
		SendSessionAffinityHeaders:                  ce.SendSessionAffinityHeaders,
		SupportsEagerToolInputStreaming:             config.CloneBoolPtr(ce.SupportsEagerToolInputStreaming),
		SupportsToolChoice:                          config.CloneBoolPtr(ce.SupportsToolChoice),
		SupportsParallelToolCalls:                   config.CloneBoolPtr(ce.SupportsParallelToolCalls),
	}
}

func (me *modelEditState) toConfig() config.ModelConfig {
	mc := config.ModelConfig{
		ID:            me.ID,
		Name:          me.Name,
		ContextWindow: me.ContextWindow,
		Reasoning:     me.Reasoning,
		Input:         config.CloneStringSlice(me.Input),
		Temperature:   config.CloneFloat64Ptr(me.Temperature),
		TopP:          config.CloneFloat64Ptr(me.TopP),
	}
	if me.MaxTokensEdited {
		mc.SetMaxTokens(me.MaxTokens)
	}
	if me.Name == "" {
		mc.Name = me.ID
	}
	if me.CostEnabled {
		mc.Cost = &config.CostConfig{
			Input:      me.CostInput,
			Output:     me.CostOutput,
			CacheRead:  me.CacheRead,
			CacheWrite: me.CacheWrite,
		}
	}
	if me.Compat.Active {
		mc.Compat = me.Compat.toConfig()
	}
	return mc
}

// --- Summary strings for rendering ---

func (pe *providerEditState) summaryShort() string {
	parts := []string{}
	if pe.API != "" {
		parts = append(parts, "api="+pe.API)
	}
	if pe.Vendor != "" {
		parts = append(parts, "vendor="+pe.Vendor)
	}
	if pe.BaseURL != "" {
		parts = append(parts, "url="+shortURL(pe.BaseURL))
	}
	if pe.HTTPProxy != "" {
		parts = append(parts, "proxy=yes")
	} else {
		parts = append(parts, "proxy=none")
	}
	if pe.CacheControl == nil {
		// auto — omit for brevity
	} else if *pe.CacheControl {
		parts = append(parts, "cache=on")
	} else {
		parts = append(parts, "cache=off")
	}
	if len(pe.Headers) > 0 {
		parts = append(parts, "hdrs="+strconv.Itoa(len(pe.Headers)))
	}
	if pe.ThinkingFormat != "" {
		parts = append(parts, "think="+pe.ThinkingFormat)
	}
	if pe.ForceHTTP11 {
		parts = append(parts, "force-h1")
	}
	return strings.Join(parts, "  ")
}

func (me *modelEditState) summaryShort() string {
	parts := []string{}
	parts = append(parts, "ctx="+authItoa(me.ContextWindow))
	parts = append(parts, "max="+authItoa(me.MaxTokens))
	if me.Reasoning {
		parts = append(parts, "reasoning")
	}
	parts = append(parts, "in="+strings.Join(me.Input, ","))
	if me.Temperature != nil {
		parts = append(parts, "t="+f64s(*me.Temperature))
	}
	if me.TopP != nil {
		parts = append(parts, "p="+f64s(*me.TopP))
	}
	if me.CostEnabled && (me.CostInput > 0 || me.CostOutput > 0) {
		parts = append(parts, "cost")
	}
	if me.Compat.Active {
		parts = append(parts, "compat")
	}
	return strings.Join(parts, "  ")
}

func (ce *compatEditState) activeCount() int {
	if !ce.Active {
		return 0
	}
	n := 0
	if ce.ThinkingFormat != "" {
		n++
	}
	// Count bool fields that differ from default false/bool
	bools := []bool{
		ce.RequiresReasoningContentOnAssistant,
		ce.RequiresReasoningContentOnAssistantMessages,
		ce.ForceAdaptiveThinking,
		ce.ParseReasoningInContent,
		ce.SendSessionAffinityHeaders,
	}
	for _, b := range bools {
		if b {
			n++
		}
	}
	ptrBools := []*bool{
		ce.SupportsDeveloperRole, ce.SupportsStore, ce.SupportsReasoningEffort,
		ce.SupportsStrictMode, ce.DisableSamplingParams, ce.SupportsCacheControlOnTools, ce.SupportsLongCacheRetention,
		ce.SupportsPromptCacheKey, ce.SupportsReasoningSummary, ce.SupportsEagerToolInputStreaming,
		ce.SupportsToolChoice, ce.SupportsParallelToolCalls,
	}
	for _, p := range ptrBools {
		if p != nil {
			n++
		}
	}
	if ce.MaxTokensField != "" {
		n++
	}
	return n
}

// --- Utility functions ---

func shortURL(s string) string {
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if idx := strings.Index(s, "/"); idx > 0 {
		s = s[:idx]
	}
	if len(s) > 30 {
		s = s[:27] + "..."
	}
	return s
}

func authItoa(v int) string {
	if v == 0 {
		return "auto"
	}
	return strconv.Itoa(v)
}

func f64s(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// sortedModelIDs returns model IDs in stable insertion order.
func sortedModelIDs(order []string, models map[string]*modelEditState) []string {
	_ = models
	out := make([]string, len(order))
	copy(out, order)
	return out
}

// modelIDsFromConfig returns model IDs in the order they appear.
func modelIDsFromConfig(models []config.ModelConfig) []string {
	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return ids
}

// sortModelIDsByRef sorts modelIDs according to a reference order.
// IDs not in ref are appended at the end alphabetically.
func sortModelIDsByRef(ids, ref []string) []string {
	refIndex := map[string]int{}
	for i, id := range ref {
		refIndex[id] = i
	}
	sorted := append([]string(nil), ids...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, oki := refIndex[sorted[i]]
		rj, okj := refIndex[sorted[j]]
		if oki && okj {
			return ri < rj
		}
		if oki {
			return true
		}
		if okj {
			return false
		}
		return sorted[i] < sorted[j]
	})
	return sorted
}

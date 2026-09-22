package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CacheControl represents cache control hints for prompt caching.
type CacheControl struct {
	Type string `json:"type"` // "ephemeral" for breakpoint markers
}

// ContentBlock represents a block of content in a message.
type ContentBlock struct {
	Type         string         `json:"type"` // "text", "image", "file", "audio", "video", "thinking", "toolCall"
	Text         string         `json:"text,omitempty"`
	Thinking     string         `json:"thinking,omitempty"`
	Signature    string         `json:"signature,omitempty"` // required for thinking block replay
	Image        *ImageContent  `json:"image,omitempty"`
	Audio        *AudioContent  `json:"audio,omitempty"`
	Video        *VideoContent  `json:"video,omitempty"`
	File         *FileContent   `json:"file,omitempty"`
	ToolCall     *ToolCallBlock `json:"toolCall,omitempty"`
	CacheControl *CacheControl  `json:"cache_control,omitempty"` // cache breakpoint marker
}

// FileContent identifies an existing provider file or carries an inline file
// payload for APIs that support file content blocks.
type FileContent struct {
	ID          string `json:"id,omitempty"`
	URL         string `json:"url,omitempty"`
	Data        string `json:"data,omitempty"` // base64 encoded
	Filename    string `json:"filename,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Size        *int   `json:"size,omitempty"`
}

// ImageContent represents an image in a message.
type ImageContent struct {
	Data           string  `json:"data"`     // base64 encoded
	MimeType       string  `json:"mimeType"` // e.g. "image/png"
	Width          int     `json:"width,omitempty"`
	Height         int     `json:"height,omitempty"`
	Bytes          int     `json:"bytes,omitempty"`
	OriginalWidth  int     `json:"originalWidth,omitempty"`
	OriginalHeight int     `json:"originalHeight,omitempty"`
	OriginalBytes  int     `json:"originalBytes,omitempty"`
	Detail         string  `json:"detail,omitempty"` // "auto", "fast", "detail", "raw"
	Scale          float64 `json:"scale,omitempty"`
	Cropped        bool    `json:"cropped,omitempty"`
	CropX          int     `json:"cropX,omitempty"`
	CropY          int     `json:"cropY,omitempty"`
	CropWidth      int     `json:"cropWidth,omitempty"`
	CropHeight     int     `json:"cropHeight,omitempty"`
}

// AudioContent carries inline audio input for models with native audio
// understanding. Data is the base64-encoded payload; URL is an alternative
// transport for wire formats that accept a remote or data URL reference.
// Audio input is user-turn only: assistant messages never carry audio blocks.
type AudioContent struct {
	MimeType string `json:"mimeType,omitempty"` // e.g. "audio/wav"
	Format   string `json:"format,omitempty"`   // wire format hint: "wav", "mp3", ...
	Data     string `json:"data,omitempty"`     // base64 encoded
	URL      string `json:"url,omitempty"`
	Bytes    int    `json:"bytes,omitempty"`
}

// VideoContent carries inline video input for models with native video
// understanding. Data is the base64-encoded payload; URL is an alternative
// transport for wire formats that accept a remote or data URL reference.
// Video input is user-turn only: assistant messages never carry video blocks.
type VideoContent struct {
	MimeType string `json:"mimeType,omitempty"` // e.g. "video/mp4"
	Data     string `json:"data,omitempty"`     // base64 encoded
	URL      string `json:"url,omitempty"`
	Bytes    int    `json:"bytes,omitempty"`
}

// ToolCallBlock represents a tool call in an assistant message.
type ToolCallBlock struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Kind             string          `json:"kind,omitempty"` // function or custom
	Input            string          `json:"input,omitempty"`
	Arguments        json.RawMessage `json:"arguments"`
	InvalidArguments string          `json:"invalidArguments,omitempty"`
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`
}

// ErrInvalidToolCallArguments indicates that a tool call's original argument
// payload was not valid JSON and therefore was not executed as supplied.
var ErrInvalidToolCallArguments = errors.New("invalid tool-call JSON arguments")

// NormalizeToolCallArguments makes streamed tool-call arguments safe to
// persist and replay. Go 1.27's JSON v2 encoder validates json.RawMessage
// values (jsontext.Value); a non-nil empty value therefore fails with
// "unexpected end of JSON input". Providers can legitimately produce an
// empty argument stream for a no-argument tool, so represent that as an empty
// object. For malformed non-empty arguments, preserve the original payload in
// InvalidArguments for diagnostics and replace the executable payload with an
// empty object. Callers must treat the returned error as a failed tool call and
// must not guess at the original side effect.
func NormalizeToolCallArguments(tc *ToolCallBlock) (args map[string]any, repaired bool, err error) {
	if tc == nil {
		return nil, false, nil
	}
	if tc.Arguments == nil {
		return nil, false, nil
	}
	if len(tc.Arguments) == 0 {
		tc.Arguments = json.RawMessage(`{}`)
		return map[string]any{}, true, nil
	}
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		if tc.InvalidArguments == "" {
			tc.InvalidArguments = string(tc.Arguments)
		}
		tc.Arguments = json.RawMessage(`{}`)
		return nil, true, fmt.Errorf("%w: %v", ErrInvalidToolCallArguments, err)
	}
	return args, false, nil
}

// NormalizeMessage returns a deep-enough copy of msg for safe persistence and
// replay. It repairs every embedded tool call while leaving the caller's
// message and argument buffers untouched. The returned notices identify tool
// calls that were repaired so the Agent can make the recovery visible to the
// model instead of silently changing a request.
func NormalizeMessage(msg Message) (normalized Message, notices []string) {
	normalized = msg
	if len(msg.Contents) == 0 {
		return normalized, nil
	}
	normalized.Contents = make([]ContentBlock, len(msg.Contents))
	for i, block := range msg.Contents {
		cloned := block
		if block.Image != nil {
			image := *block.Image
			cloned.Image = &image
		}
		if block.File != nil {
			file := *block.File
			cloned.File = &file
		}
		if block.Audio != nil {
			audio := *block.Audio
			cloned.Audio = &audio
		}
		if block.Video != nil {
			video := *block.Video
			cloned.Video = &video
		}
		if block.CacheControl != nil {
			cache := *block.CacheControl
			cloned.CacheControl = &cache
		}
		if block.ToolCall != nil {
			call := *block.ToolCall
			if block.ToolCall.Arguments != nil {
				call.Arguments = make(json.RawMessage, len(block.ToolCall.Arguments))
				copy(call.Arguments, block.ToolCall.Arguments)
			}
			_, repaired, err := NormalizeToolCallArguments(&call)
			if repaired {
				notice := fmt.Sprintf("tool %q", call.Name)
				if err != nil {
					notice += ": invalid JSON arguments"
				} else {
					notice += ": empty arguments"
				}
				notices = append(notices, notice)
			}
			cloned.ToolCall = &call
		}
		normalized.Contents[i] = cloned
	}
	return normalized, notices
}

// Message represents a conversation message.
type Message struct {
	Role           string         `json:"role"`                  // "user", "assistant", "toolResult"
	Content        string         `json:"content,omitempty"`     // simple text content
	Contents       []ContentBlock `json:"contents,omitempty"`    // rich content blocks
	Attachments    []Attachment   `json:"attachments,omitempty"` // provider-neutral output artifacts
	ToolCallID     string         `json:"toolCallId,omitempty"`  // for toolResult
	ToolName       string         `json:"toolName,omitempty"`    // for toolResult
	ToolKind       string         `json:"toolKind,omitempty"`    // function or custom
	IsError        bool           `json:"isError,omitempty"`     // for toolResult
	Timestamp      time.Time      `json:"timestamp"`
	Usage          *Usage         `json:"usage,omitempty"`          // token usage from API response
	SystemInjected bool           `json:"systemInjected,omitempty"` // true for injected messages (session context, compression instructions) - skipped by cache markers
}

// NewUserMessage creates a simple user text message.
func NewUserMessage(text string) Message {
	return Message{
		Role:      "user",
		Content:   text,
		Timestamp: time.Now(),
	}
}

// NewSystemInjectedUserMessage creates a system-injected user message (skipped by cache markers).
func NewSystemInjectedUserMessage(text string) Message {
	return Message{
		Role:           "user",
		Content:        text,
		Timestamp:      time.Now(),
		SystemInjected: true,
	}
}

// NewAssistantMessage creates an assistant message with content blocks.
func NewAssistantMessage(contents []ContentBlock) Message {
	return Message{
		Role:      "assistant",
		Contents:  contents,
		Timestamp: time.Now(),
	}
}

// NewToolResultMessage creates a tool result message.
func NewToolResultMessage(toolCallID, toolName, content string, isError bool) Message {
	return Message{
		Role:       "toolResult",
		Content:    content,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		IsError:    isError,
		Timestamp:  time.Now(),
	}
}

// NewToolResultMessageWithContents creates a tool result message with rich content blocks.
// If contents is nil or empty, it falls back to using the text parameter.
func NewToolResultMessageWithContents(toolCallID, toolName, text string, contents []ContentBlock, isError bool) Message {
	msg := Message{
		Role:       "toolResult",
		ToolCallID: toolCallID,
		ToolName:   toolName,
		IsError:    isError,
		Timestamp:  time.Now(),
	}
	if len(contents) > 0 {
		msg.Contents = contents
		// Also set Content for backward compatibility (display/logging)
		msg.Content = text
	} else {
		msg.Content = text
	}
	return msg
}

// Usage represents token usage and cost information.
type Usage struct {
	Input       int  `json:"input"`
	Output      int  `json:"output"`
	Reasoning   int  `json:"reasoning,omitempty"`
	CacheRead   int  `json:"cacheRead"`
	CacheWrite  int  `json:"cacheWrite"`
	TotalTokens int  `json:"totalTokens"`
	Cost        Cost `json:"cost"`
}

// PromptTokens returns the provider-reported prompt token count for the turn.
// For OpenAI-compatible APIs this is the full prompt footprint. For Anthropic,
// Input is normalized to the non-cached prompt portion, so callers that need the
// full prompt footprint should use TotalInputTokens instead.
func (u *Usage) PromptTokens() int {
	if u == nil {
		return 0
	}
	if u.TotalTokens > 0 {
		prompt := u.TotalTokens - u.Output
		if prompt > 0 {
			return prompt
		}
	}
	return u.Input
}

// TotalInputTokens returns the full input footprint for the turn, including
// cache reads and cache writes when those are reported separately.
func (u *Usage) TotalInputTokens() int {
	if u == nil {
		return 0
	}
	if u.TotalTokens > 0 {
		totalInput := u.TotalTokens - u.Output
		if totalInput > 0 {
			return totalInput
		}
	}
	return u.Input + u.CacheRead + u.CacheWrite
}

// CacheInfo returns a short display string for cache activity (e.g. "Cache: 75%"),
// or an empty string when there is no cache data to show.
//
// Cache percentage uses the full prompt footprint as the denominator so the
// value means "what portion of this turn's prompt came from cache".
func (u *Usage) CacheInfo() string {
	if u == nil {
		return ""
	}
	totalInputTokens := u.TotalInputTokens()
	switch {
	case totalInputTokens > 0 && u.CacheRead > 0:
		pct := float64(u.CacheRead) / float64(totalInputTokens) * 100
		if pct > 100 {
			pct = 100
		}
		return fmt.Sprintf("Cache: %.0f%%", pct)
	case u.CacheWrite > 0 && u.CacheRead == 0:
		return fmt.Sprintf("CacheWrite: %d", u.CacheWrite)
	case totalInputTokens > 0 && u.CacheRead == 0 && u.CacheWrite == 0:
		return "Cache: 0%"
	default:
		return ""
	}
}

// TurnClassification describes whether a completed stream turn produced a
// meaningful response or was effectively empty (a likely provider error such
// as HTTP 200 with placeholder usage and no content). It is vendor- and
// protocol-agnostic so the agent loop can use it as a universal fallback
// regardless of which provider/model is in use.
type TurnClassification int

const (
	// TurnMeaningful means the turn produced content (text/thinking/toolCall)
	// or the model explicitly signalled a normal stop.
	TurnMeaningful TurnClassification = iota
	// TurnEmpty means the turn produced no content AND usage looks like a
	// placeholder/error sentinel with no explicit stop reason. This is the
	// signature of a provider returning an empty/error response (e.g. some
	// OpenAI-compatible gateways return usage {1,1,2} on empty/error bodies).
	TurnEmpty
)

// ClassifyTurn inspects the accumulated turn output and reports whether it is
// a meaningful response or an effectively empty one. It is the shared,
// vendor-agnostic signal the agent loop uses to decide whether a "no tool
// call" turn is a legitimate model-chosen stop or a provider failure that
// should be retried instead of silently ending the session.
//
// The judgement rests on two cross-vendor invariants:
//   - any real response carries at least the prompt tokens in usage.Input,
//     so Input<=1 (or nil usage) with empty content cannot be a normal stop;
//   - a model that genuinely chose to stop reports an explicit stop reason
//     (stop/end_turn/finish/...), which we honour even with empty content.
func ClassifyTurn(text, think string, toolCalls []ToolCallBlock, usage *Usage, stopReason string) TurnClassification {
	if text != "" || think != "" || len(toolCalls) > 0 {
		return TurnMeaningful
	}
	if IsStubUsage(usage) && !isDefiniteStopReason(stopReason) {
		return TurnEmpty
	}
	return TurnMeaningful
}

// IsStubUsage reports whether a Usage looks like a placeholder/error sentinel.
// Nil usage or implausibly small values (total<=2, or input<=1 && output<=1)
// cannot occur on a real response because the prompt alone is always >= the
// context tokens, so this is safe across all vendors/protocols.
func IsStubUsage(u *Usage) bool {
	if u == nil {
		return true
	}
	// A real response's input always carries at least the prompt tokens
	// (thousands+), so Input<=1 is impossible outside a placeholder/error body.
	// TotalTokens<=2 catches the same case when a gateway reports only the
	// aggregate. Output is intentionally not used as a signal: a model may
	// legitimately emit very few output tokens.
	return u.Input <= 1 || u.TotalTokens <= 2
}

// FormatUsage renders a Usage for inclusion in error/status messages.
func FormatUsage(u *Usage) string {
	if u == nil {
		return "nil"
	}
	return fmt.Sprintf("{input:%d output:%d total:%d}", u.Input, u.Output, u.TotalTokens)
}

// isDefiniteStopReason reports whether stopReason is an explicit, provider-
// reported normal-stop signal. When the model explicitly said "I'm done", an
// empty body is (rarely) legitimate and must not be treated as an error.
func isDefiniteStopReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "stop", "end_turn", "finish", "complete", "completed", "ended":
		return true
	default:
		return false
	}
}

// Cost represents the monetary cost of a request.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// UncachedInputTokens returns input tokens to charge at the regular input
// rate. Anthropic reports Input separately from cache reads and writes, while
// OpenAI-compatible and Google APIs include cache reads in Input. TotalTokens
// lets us distinguish those wire formats without leaking provider types into
// shared accounting.
func (u *Usage) UncachedInputTokens() int {
	input := u.Input
	// Google can report reasoning tokens separately while including them in
	// total_token_count, whereas OpenAI includes reasoning in output tokens.
	// Account for either representation when comparing aggregate input.
	totalInput := u.TotalTokens - u.Output
	if totalInput != u.Input && u.Reasoning > 0 {
		totalInput -= u.Reasoning
	}
	if totalInput > 0 && totalInput == u.Input {
		input -= u.CacheRead + u.CacheWrite
	}
	if input < 0 {
		return 0
	}
	return input
}

// CalculateCost computes the cost based on the model's pricing.
func (u *Usage) CalculateCost(model *Model) {
	if model == nil {
		return
	}
	c := Cost{
		Input:      float64(u.UncachedInputTokens()) / 1_000_000 * model.Cost.Input,
		Output:     float64(u.Output) / 1_000_000 * model.Cost.Output,
		CacheRead:  float64(u.CacheRead) / 1_000_000 * model.Cost.CacheRead,
		CacheWrite: float64(u.CacheWrite) / 1_000_000 * model.Cost.CacheWrite,
	}
	c.Total = c.Input + c.Output + c.CacheRead + c.CacheWrite
	u.Cost = c
}

// ModelPricing represents the cost per million tokens for a model.
type ModelPricing struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// Model represents a model available from a provider.
type Model struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Provider      string       `json:"provider"`
	Reasoning     bool         `json:"reasoning"` // supports extended thinking
	Input         []string     `json:"input"`     // "text", "image", "audio", "video"
	Cost          ModelPricing `json:"cost"`
	ContextWindow int          `json:"contextWindow"`         // max context tokens
	MaxTokens     int          `json:"maxTokens"`             // max output tokens
	MaxTokensSet  bool         `json:"-"`                     // true when maxTokens came from user/runtime config
	Temperature   *float64     `json:"temperature,omitempty"` // nil = use API default
	TopP          *float64     `json:"topP,omitempty"`        // nil = use API default
	Compat        *ModelCompat `json:"compat,omitempty"`
}

// SupportsInput reports whether the model declares support for one input
// modality ("text", "image", "audio", "video"). It is the shared capability
// resolver used by Agent Core and the Runtime input pipeline; adapters must not
// re-implement modality checks against Model.Input.
func (m *Model) SupportsInput(kind string) bool {
	if m == nil {
		return false
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return false
	}
	for _, input := range m.Input {
		if strings.EqualFold(strings.TrimSpace(input), kind) {
			return true
		}
	}
	return false
}

// ModelCompat captures vendor-specific behavior flags for otherwise compatible APIs.
type ModelCompat struct {
	ThinkingFormat                      string `json:"thinkingFormat,omitempty"`
	RequiresReasoningContentOnAssistant bool   `json:"requiresReasoningContentOnAssistant,omitempty"`
	ForceAdaptiveThinking               bool   `json:"forceAdaptiveThinking,omitempty"`
	// ParseReasoningInContent extracts <think>...</think> wrapped reasoning from
	// the content stream for models that inline thinking in the body.
	ParseReasoningInContent bool `json:"parseReasoningInContent,omitempty"`

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

	SupportsCacheControlOnTools *bool `json:"supportsCacheControlOnTools,omitempty"`
	SupportsLongCacheRetention  *bool `json:"supportsLongCacheRetention,omitempty"`
	SupportsPromptCacheKey      *bool `json:"supportsPromptCacheKey,omitempty"`
	SupportsReasoningSummary    *bool `json:"supportsReasoningSummary,omitempty"`
	SendSessionAffinityHeaders  bool  `json:"sendSessionAffinityHeaders,omitempty"`

	SupportsEagerToolInputStreaming *bool `json:"supportsEagerToolInputStreaming,omitempty"`
}

// SamplingParamsDisabled reports whether sampling parameters (temperature/
// top_p) should be omitted from requests for the model. It defaults to true:
// params are only sent when the model's compat explicitly sets
// DisableSamplingParams to false.
func SamplingParamsDisabled(m *Model) bool {
	if m == nil || m.Compat == nil || m.Compat.DisableSamplingParams == nil {
		return true
	}
	return *m.Compat.DisableSamplingParams
}

// ThinkingLevel represents the depth of reasoning.
type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
	ThinkingMax     ThinkingLevel = "max"
)

// NormalizeThinkingLevel ensures a valid thinking level is returned.
// Empty or invalid values fall back to ThinkingMedium for reasoning models.
func NormalizeThinkingLevel(level ThinkingLevel) ThinkingLevel {
	switch level {
	case ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax:
		return level
	case "":
		// Empty string falls back to medium (reasonable default for reasoning models)
		return ThinkingMedium
	default:
		// Invalid value falls back to medium
		return ThinkingMedium
	}
}

// ToolDefinition describes a tool available to the model.
type ToolDefinition struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Parameters   json.RawMessage `json:"parameters"`       // JSON Schema
	Kind         string          `json:"kind,omitempty"`   // function (default), custom, or hosted
	Format       json.RawMessage `json:"format,omitempty"` // custom tool text/grammar format
	Provider     string          `json:"provider,omitempty"`
	ProviderType string          `json:"providerType,omitempty"`
	Model        string          `json:"model,omitempty"`
}

// Attachment carries protocol-neutral generated files, citations, artifacts,
// and hosted tool outputs alongside a stream event.
type Attachment struct {
	Kind        string         `json:"kind"` // citation, file, image, artifact, tool_result
	Name        string         `json:"name,omitempty"`
	URL         string         `json:"url,omitempty"`
	MediaType   string         `json:"mediaType,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	ProviderRef string         `json:"providerRef,omitempty"`
}

// StreamEventType identifies the type of a streaming event.
type StreamEventType int

const (
	StreamStart          StreamEventType = iota // Stream started
	StreamTextDelta                             // Text content delta
	StreamThinkDelta                            // Thinking content delta
	StreamThinkSignature                        // Thinking block signature (for multi-turn replay)
	StreamToolCall                              // Tool call event
	StreamUsage                                 // Usage statistics
	StreamDone                                  // Stream completed
	StreamError                                 // Error occurred
	StreamHostedItem                            // Hosted Responses item lifecycle event
	StreamRetry                                 // Retry attempt in progress
)

// StreamEvent represents a single event from a streaming response.
type StreamEvent struct {
	Type              StreamEventType
	TextDelta         string         // for StreamTextDelta
	ThinkDelta        string         // for StreamThinkDelta
	ThinkSignature    string         // for StreamThinkSignature
	ToolCall          *ToolCallBlock // for StreamToolCall
	HostedItem        *HostedItem    // for StreamHostedItem
	Usage             *Usage         // for StreamUsage
	Error             error          // for StreamError
	StopReason        string         // for StreamDone: "stop", "length", "toolUse", "error", "aborted"
	RetryAttempt      int            // for StreamRetry: current retry attempt number
	RetryMax          int            // Deprecated: use RetryMaxAttempts.
	RetryMaxAttempts  int            // for StreamRetry: maximum retry attempts
	RetryAfterMS      int            // for StreamRetry: delay before the next attempt, in milliseconds
	RetryDetail       string         // for StreamRetry: sanitized single-line provider diagnostic for optional UI display; never affects retry behavior
	ProviderEventType string         // provider-native event type, sanitized
	ItemID            string         // protocol item id, when provider-neutral
	CallID            string         // protocol tool/function call id, when provider-neutral
	Metadata          map[string]any // sanitized, size-limited provider-neutral metadata
	Attachments       []Attachment   // sanitized generated files, citations, artifacts, tool results
}

// HostedItem is a provider-neutral lifecycle projection for native hosted
// tools. The canonical payload remains in the response archive; this event is
// only for live consumers that need added/done status without reimplementing a
// vendor codec.
type HostedItem struct {
	ID          string         `json:"id,omitempty"`
	Type        string         `json:"type,omitempty"`
	Status      string         `json:"status,omitempty"`
	OutputIndex int            `json:"outputIndex,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// StructuredOutputOptions describes cross-provider structured text output.
type StructuredOutputOptions struct {
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Format      string          `json:"format,omitempty"` // text, json_object, json_schema
}

// ToolChoice describes cross-provider tool choice controls.
type ToolChoice struct {
	Type string `json:"type,omitempty"` // auto, none, required, function
	Name string `json:"name,omitempty"` // function/custom tool name for explicit choices
}

// ResponseOptions carries protocol features that have provider-neutral
// semantics. Provider-specific runtime state remains in provider config.
type ResponseOptions struct {
	StructuredOutput *StructuredOutputOptions `json:"structuredOutput,omitempty"`
	ToolChoice       *ToolChoice              `json:"toolChoice,omitempty"`
	ParallelTools    *bool                    `json:"parallelTools,omitempty"`
	MaxToolCalls     *int                     `json:"maxToolCalls,omitempty"`
	// PreviousResponseID is used by providers that support remote response
	// lineage. It is optional so replay remains the default state strategy.
	PreviousResponseID string `json:"previousResponseId,omitempty"`
	// ReplayItems is a complete, ordered Responses input history. When set,
	// providers that support native item replay use it instead of rebuilding
	// the same history from role messages.
	ReplayItems []json.RawMessage `json:"-"`
	// SuppressConversation requests a local replay without the configured
	// remote conversation, used only after a provider reports that the remote
	// conversation is unavailable.
	SuppressConversation bool `json:"-"`
	// ResponseArchive receives a provider-neutral, sanitized representation of
	// a completed Responses turn. It is runtime-only and is never serialized.
	ResponseArchive func(ResponseArchive) `json:"-"`
}

// ResponseArchive is a protocol-neutral durable representation of a Responses
// turn. Providers retain their private wire codecs and only expose the fields
// required by session recovery and audit.
type ResponseArchive struct {
	ResponseID         string
	Status             string
	PreviousResponseID string
	ConversationID     string
	IncompleteReason   string
	StateMode          string
	Usage              *Usage
	Items              []ResponseArchiveItem
	Attachments        []Attachment
	UnknownEventTypes  []string
}

type ResponseArchiveItem struct {
	ID          string
	Type        string
	Status      string
	OutputIndex int
	Canonical   json.RawMessage
}

// ResponseStateModeProvider exposes the selected remote state behavior to the
// agent loop without leaking a provider's configuration implementation.
type ResponseStateModeProvider interface {
	ResponseStateMode() string
}

// ResponseStateFallbackProvider identifies remote lineage errors for which the
// agent may safely retry the current turn from its local replay archive.
type ResponseStateFallbackProvider interface {
	ResponseStateFallbackError(error) bool
}

// ResponseStateFailureClass describes a failed remote lineage request without
// exposing provider wire errors to session archives.
type ResponseStateFailureClass string

const (
	ResponseStateFailureExpired       ResponseStateFailureClass = "expired"
	ResponseStateFailurePermission    ResponseStateFailureClass = "permission"
	ResponseStateFailureRequestFailed ResponseStateFailureClass = "request_failed"
)

// ResponseStateFailureClassifier reports a stable classification suitable for
// recovery/audit decisions.
type ResponseStateFailureClassifier interface {
	ResponseStateFailureClass(error) ResponseStateFailureClass
}

// ChatParams contains all parameters for a chat request.
type ChatParams struct {
	Messages        []Message
	Tools           []ToolDefinition
	SystemPrompt    string
	ThinkingLevel   ThinkingLevel
	MaxTokens       int
	Temperature     *float64        // nil = use API default
	TopP            *float64        // nil = use API default
	ModelID         string          // which model to use
	Abort           <-chan struct{} // closed to abort the request
	ResponseOptions *ResponseOptions
}

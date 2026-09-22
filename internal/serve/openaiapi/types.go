package openaiapi

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
)

// --- OpenAI-compatible request types ---

// ChatCompletionRequest represents the OpenAI chat completions request.
type ChatCompletionRequest struct {
	Model       string           `json:"model,omitempty"`
	Messages    []RequestMessage `json:"messages"`
	Stream      bool             `json:"stream,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	TopP        *float64         `json:"top_p,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Background  bool             `json:"x_background,omitempty"`
}

// SessionToolOptions are per-session runtime tool toggles supplied by WebUI.
type SessionToolOptions struct {
	WebSearch  *bool `json:"webSearch,omitempty"`
	Browser    *bool `json:"browser,omitempty"`
	A2AMaster  *bool `json:"a2aMaster,omitempty"`
	Delegate   *bool `json:"delegate,omitempty"`
	MultiAgent *bool `json:"multiAgent,omitempty"`
	Workflows  *bool `json:"workflows,omitempty"`
}

// CapabilityFeature describes serve-level capability availability and defaults.
type CapabilityFeature struct {
	Available bool   `json:"available"`
	Default   bool   `json:"default"`
	Locked    bool   `json:"locked,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// CapabilityOverview is returned by GET /api/capabilities.
type CapabilityOverview struct {
	Modes    []string                     `json:"modes"`
	Features map[string]CapabilityFeature `json:"features"`
	Defaults SessionCapabilities          `json:"defaults"`
	// AttachmentDownload is provider-neutral and reflects an optional resolver
	// on the active provider. It is separate from Responses-specific details.
	AttachmentDownload bool                         `json:"attachmentDownload"`
	Responses          *ResponsesCapabilityOverview `json:"responses,omitempty"`
}

// ResponsesCapabilityOverview exposes provider-specific Responses support as
// a read-only report without changing the common Provider interface.
type ResponsesCapabilityOverview struct {
	ModelID                    string          `json:"modelId"`
	Provider                   string          `json:"provider,omitempty"`
	API                        string          `json:"api,omitempty"`
	SupportsResponses          bool            `json:"supportsResponses"`
	SupportsPreviousResponse   bool            `json:"supportsPreviousResponseId"`
	SupportsConversation       bool            `json:"supportsConversation"`
	SupportsBackground         bool            `json:"supportsBackground"`
	SupportsStructuredOutput   bool            `json:"supportsStructuredOutput"`
	SupportsServiceTier        bool            `json:"supportsServiceTier"`
	SupportsParallelTools      bool            `json:"supportsParallelToolCalls"`
	SupportsToolChoice         bool            `json:"supportsToolChoice"`
	SupportsAttachmentDownload bool            `json:"supportsAttachmentDownload"`
	HostedTools                map[string]bool `json:"hostedTools,omitempty"`
	HostedPolicies             map[string]bool `json:"hostedPolicies,omitempty"`
	SupportedInclude           []string        `json:"supportedInclude,omitempty"`
	SupportedEvents            []string        `json:"supportedEvents,omitempty"`
	SupportedItems             []string        `json:"supportedItems,omitempty"`
	AttachmentKinds            []string        `json:"attachmentKinds,omitempty"`
	SupportedAnnotations       []string        `json:"supportedAnnotations,omitempty"`
}

// SessionCapabilities are the effective runtime capabilities for a session.
type SessionCapabilities struct {
	ID              string `json:"id,omitempty"`
	WorkDir         string `json:"workDir,omitempty"`
	Active          bool   `json:"active"`
	Mode            string `json:"mode"`
	DisplayMode     string `json:"displayMode"`
	DelegateMode    bool   `json:"delegateMode"`
	Delegate        bool   `json:"delegate"`
	MultiAgent      bool   `json:"multiAgent"`
	Workflows       bool   `json:"workflows"`
	WebSearch       bool   `json:"webSearch"`
	Browser         bool   `json:"browser"`
	A2AMaster       bool   `json:"a2aMaster"`
	Model           string `json:"model,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	Persisted       bool   `json:"persisted"`
	RuntimeOnly     bool   `json:"runtimeOnly,omitempty"`
	PersistenceNote string `json:"persistenceNote,omitempty"`
}

// SessionCapabilityPatch updates mutable session runtime capabilities.
type SessionCapabilityPatch struct {
	Mode         *string `json:"mode,omitempty"`
	DisplayMode  *string `json:"displayMode,omitempty"`
	DelegateMode *bool   `json:"delegateMode,omitempty"`
	Delegate     *bool   `json:"delegate,omitempty"`
	MultiAgent   *bool   `json:"multiAgent,omitempty"`
	Workflows    *bool   `json:"workflows,omitempty"`
	WebSearch    *bool   `json:"webSearch,omitempty"`
	Browser      *bool   `json:"browser,omitempty"`
	A2AMaster    *bool   `json:"a2aMaster,omitempty"`
}

// SessionRuntimePatch is the structured WebUI runtime patch payload. Mode is
// kept separate from capabilities while capability toggles remain session-level
// user intent.
type SessionRuntimePatch struct {
	Mode         *string             `json:"mode,omitempty"`
	DisplayMode  *string             `json:"displayMode,omitempty"`
	Capabilities map[string]bool     `json:"capabilities,omitempty"`
	Tools        *SessionToolOptions `json:"tools,omitempty"`
}

// SessionRuntimeSnapshot is the structured WebUI view for runtime state.
type SessionRuntimeSnapshot struct {
	SessionID        string                                 `json:"sessionId"`
	Mode             string                                 `json:"mode"`
	DisplayMode      string                                 `json:"displayMode"`
	Model            string                                 `json:"model,omitempty"`
	ThinkingLevel    string                                 `json:"thinkingLevel,omitempty"`
	WorkDir          string                                 `json:"workDir,omitempty"`
	Capabilities     map[string]SessionCapabilityState      `json:"capabilities"`
	PendingApprovals []SessionApprovalRequest               `json:"pendingApprovals"`
	PendingQuestions []SessionQuestionRequest               `json:"pendingQuestions"`
	ActiveRun        *SessionActiveRun                      `json:"activeRun,omitempty"`
	Execution        *agentruntime.SessionExecutionSnapshot `json:"execution,omitempty"`
	ResponsesRun     *SessionResponsesRun                   `json:"responsesRun,omitempty"`
	ESM              *ESMSnapshot                           `json:"esm,omitempty"`
}

// SessionCapabilityState describes availability, desired enabled state and
// effective runtime state for one WebUI capability.
type SessionCapabilityState struct {
	Available      bool   `json:"available"`
	Enabled        bool   `json:"enabled"`
	Effective      bool   `json:"effective"`
	DisabledReason string `json:"disabledReason,omitempty"`
}

// SessionActiveRun describes the currently running session run, if any.
type SessionActiveRun struct {
	RunID     string    `json:"runId,omitempty"`
	Status    string    `json:"status"`
	Source    string    `json:"source,omitempty"`
	Model     string    `json:"model,omitempty"`
	Mode      string    `json:"mode,omitempty"`
	StartedAt time.Time `json:"startedAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// SessionResponsesRun is the local projection of a durable OpenAI Responses
// background run. The full provider-specific state remains behind the
// Responses run API.
type SessionResponsesRun struct {
	LocalRunID      string `json:"localRunId"`
	ResponseID      string `json:"responseId,omitempty"`
	State           string `json:"state"`
	CancelRequested bool   `json:"cancelRequested,omitempty"`
}

// SessionQuestionRequest is the WebUI question-center event shape.
type SessionQuestionRequest struct {
	QuestionID string   `json:"questionId"`
	SessionID  string   `json:"sessionId"`
	RunID      string   `json:"runId,omitempty"`
	Question   string   `json:"question"`
	Options    []string `json:"options,omitempty"`
	Context    string   `json:"context,omitempty"`
	Timestamp  string   `json:"timestamp,omitempty"`
}

// SessionQuestionResponse is a WebUI answer for one pending question.
type SessionQuestionResponse struct {
	Answer string `json:"answer"`
}

// SessionQuestionResolution is the server-confirmed question state.
type SessionQuestionResolution struct {
	QuestionID string `json:"questionId"`
	SessionID  string `json:"sessionId"`
	RunID      string `json:"runId,omitempty"`
	Answer     string `json:"answer,omitempty"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

type SessionApprovalRequest struct {
	ApprovalID string         `json:"approvalId"`
	ToolCallID string         `json:"toolCallId,omitempty"`
	SessionID  string         `json:"sessionId"`
	RunID      string         `json:"runId,omitempty"`
	Timestamp  string         `json:"timestamp,omitempty"`
	AgentID    string         `json:"agentId,omitempty"`
	Mode       string         `json:"mode,omitempty"`
	Risk       string         `json:"risk,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Tool       map[string]any `json:"tool,omitempty"`
	Context    map[string]any `json:"context,omitempty"`
	Actions    []string       `json:"actions,omitempty"`
}

// RequestMessage represents a message in the OpenAI request.
type RequestMessage struct {
	Role         string               `json:"role"`
	Content      string               `json:"content"`
	ContentParts []RequestContentPart `json:"-"`
	Name         string               `json:"name,omitempty"`
}

// RequestContentPart represents one OpenAI-compatible multimodal content part.
type RequestContentPart struct {
	Type       string             `json:"type"`
	Text       string             `json:"text,omitempty"`
	ImageURL   *RequestImageURL   `json:"image_url,omitempty"`
	Image      *RequestImageData  `json:"image,omitempty"`
	InputAudio *RequestInputAudio `json:"input_audio,omitempty"`
	VideoURL   *RequestVideoURL   `json:"video_url,omitempty"`
}

// RequestImageURL represents an OpenAI image_url content part.
type RequestImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// RequestImageData represents an internal image content part shape.
type RequestImageData struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
	Detail   string `json:"detail,omitempty"`
}

// RequestInputAudio represents an OpenAI-compatible input_audio content part.
// Data accepts raw base64 (OpenAI shape) or a data URL (Qwen/DashScope shape);
// remote URLs are not fetched.
type RequestInputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format,omitempty"`
}

// RequestVideoURL represents an OpenAI-compatible video_url content part. URL
// must be an inline base64 or data URL payload; remote URLs are not fetched.
type RequestVideoURL struct {
	URL string `json:"url"`
}

// UnmarshalJSON accepts both classic string content and OpenAI-style content arrays.
func (m *RequestMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Name    string          `json:"name,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	m.Name = raw.Name
	m.Content = ""
	m.ContentParts = nil
	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw.Content, &text); err == nil {
		m.Content = text
		return nil
	}
	var parts []RequestContentPart
	if err := json.Unmarshal(raw.Content, &parts); err != nil {
		return fmt.Errorf("content must be a string or content array")
	}
	m.ContentParts = parts
	for _, part := range parts {
		if part.Type == "text" && part.Text != "" {
			if m.Content != "" {
				m.Content += "\n"
			}
			m.Content += part.Text
		}
	}
	return nil
}

// --- OpenAI-compatible response types ---

// ChatCompletionResponse is the non-streaming response.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   *CompletionUsage       `json:"usage,omitempty"`
}

// ChatCompletionChoice is a single choice in the response.
type ChatCompletionChoice struct {
	Index        int              `json:"index"`
	Message      *ResponseMessage `json:"message,omitempty"`
	Delta        *ResponseMessage `json:"delta,omitempty"`
	FinishReason *string          `json:"finish_reason"`
}

// ResponseMessage is the assistant's response message.
type ResponseMessage struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// CompletionUsage tracks token counts.
type CompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// ToolCallSummary is an internal run summary. It is not serialized by the
// OpenAI-compatible endpoint.
type ToolCallSummary struct {
	Name   string         `json:"name"`
	Args   map[string]any `json:"args,omitempty"`
	Status string         `json:"status"`
}

// --- Streaming chunk types ---

// ChatCompletionChunk is the streaming chunk response.
type ChatCompletionChunk struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   *CompletionUsage       `json:"usage,omitempty"`
}

// --- SSE tool_status event (for sse_event mode) ---

// ToolStatusEvent is sent via SSE event: tool_status.
type ToolStatusEvent struct {
	SessionID  string         `json:"sessionId,omitempty"`
	RunID      string         `json:"runId,omitempty"`
	Timestamp  string         `json:"timestamp,omitempty"`
	Tool       string         `json:"tool"`
	ToolCallID string         `json:"toolCallId,omitempty"`
	AgentID    string         `json:"agentId,omitempty"`
	Status     string         `json:"status"` // "running", "completed", "failed"
	Args       map[string]any `json:"args,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	IsError    bool           `json:"isError,omitempty"`
	HasDetail  bool           `json:"hasDetail,omitempty"`
}

// TranscriptStreamEvent is a WebUI-oriented SSE event that mirrors the
// /api/sessions/{id}/messages entry shape while the response is still running.
type TranscriptStreamEvent struct {
	Type       string               `json:"type"` // "assistant_delta" or "message"
	XSessionID string               `json:"x_session_id,omitempty"`
	RunID      string               `json:"runId,omitempty"`
	Timestamp  string               `json:"timestamp,omitempty"`
	Message    *SessionMessageEntry `json:"message,omitempty"`
	HostedItem *HostedItemEvent     `json:"hostedItem,omitempty"`
}

// HostedItemEvent is the safe lifecycle projection of a native hosted tool.
// Canonical provider output remains in the response archive.
type HostedItemEvent struct {
	ID          string         `json:"id,omitempty"`
	Type        string         `json:"type,omitempty"`
	Status      string         `json:"status,omitempty"`
	OutputIndex int            `json:"outputIndex,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// SessionRunEventEntry is the Web/API view of a run lifecycle event.
type SessionRunEventEntry struct {
	Seq       int64          `json:"seq,omitempty"`
	ID        string         `json:"id"`
	SessionID string         `json:"sessionId"`
	RunID     string         `json:"runId"`
	EventType string         `json:"eventType"`
	Source    string         `json:"source,omitempty"`
	Status    string         `json:"status,omitempty"`
	Model     string         `json:"model,omitempty"`
	Mode      string         `json:"mode,omitempty"`
	Timestamp string         `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`
}

// SessionCapabilityEventEntry is the Web/API view of a capability transition.
type SessionCapabilityEventEntry struct {
	Seq        int64          `json:"seq,omitempty"`
	ID         string         `json:"id"`
	SessionID  string         `json:"sessionId"`
	RunID      string         `json:"runId,omitempty"`
	EventType  string         `json:"eventType"`
	Source     string         `json:"source,omitempty"`
	Actor      string         `json:"actor,omitempty"`
	Capability string         `json:"capability"`
	OldValue   string         `json:"oldValue"`
	NewValue   string         `json:"newValue"`
	Timestamp  string         `json:"timestamp"`
	Data       map[string]any `json:"data,omitempty"`
}

// --- Model list types ---

// ModelListResponse is the response for GET /v1/models.
type ModelListResponse struct {
	Object string      `json:"object"`
	Data   []ModelItem `json:"data"`
}

// ModelItem represents one model in the list.
type ModelItem struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	Object        string   `json:"object"`
	Created       int64    `json:"created"`
	OwnedBy       string   `json:"owned_by"`
	Provider      string   `json:"provider,omitempty"`
	Input         []string `json:"input,omitempty"`
	Reasoning     bool     `json:"reasoning"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
}

// ModelCatalogResponse is the response for GET /api/models/catalog. It lists
// every usable provider with its factory-resolved models — the same
// resolution the TUI uses when constructing a provider — so the WebUI model
// picker never re-derives the catalog from raw settings JSON.
type ModelCatalogResponse struct {
	Object          string        `json:"object"`
	DefaultProvider string        `json:"defaultProvider,omitempty"`
	DefaultModel    string        `json:"defaultModel,omitempty"`
	Providers       []string      `json:"providers"`
	Data            []ModelItem   `json:"data"`
	ModelDefaults   ModelDefaults `json:"modelDefaults"`
}

type ModelDefaults struct {
	Reasoning     bool     `json:"reasoning"`
	ContextWindow int      `json:"contextWindow"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Input         []string `json:"input"`
}

// --- Health ---

// HealthResponse is the response for GET /health.
type HealthResponse struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Sessions int    `json:"sessions"`
}

// --- Error response ---

// ErrorResponse is the standard OpenAI error format.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains error information.
type ErrorDetail struct {
	Message         string `json:"message"`
	Type            string `json:"type"`
	Code            string `json:"code,omitempty"`
	FailureClass    string `json:"failureClass,omitempty"`
	Phase           string `json:"phase,omitempty"`
	MessageKey      string `json:"messageKey,omitempty"`
	Detail          string `json:"detail,omitempty"`
	RetryMode       string `json:"retryMode,omitempty"`
	Retryable       bool   `json:"retryable,omitempty"`
	RetryAfterMS    int    `json:"retryAfterMs,omitempty"`
	Attempt         int    `json:"attempt,omitempty"`
	MaxAttempts     int    `json:"maxAttempts,omitempty"`
	SideEffectState string `json:"sideEffectState,omitempty"`
	PartialOutput   bool   `json:"partialOutput,omitempty"`
	RunID           string `json:"runId,omitempty"`
	IntentID        string `json:"intentId,omitempty"`
	RequestID       string `json:"requestId,omitempty"`
}

// --- Helpers ---

func newCompletionID() string {
	return fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
}

func newCommandCompletionID() string {
	return fmt.Sprintf("chatcmpl-cmd-%d", time.Now().UnixNano())
}

func stringPtr(s string) *string {
	return &s
}

func marshalJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

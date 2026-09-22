// Package agent defines the public Agent interface and related types.
// External Go developers can import this package to create custom Agent implementations
// or use the Builder to instantiate the built-in Agent.
//
// Import path: github.com/oschina/mothx/agent
package agent

import "context"

// AgentID uniquely identifies an agent instance.
type AgentID string

// Agent is the interface that all agent implementations must satisfy.
type Agent interface {
	// ID returns the unique identifier for this agent.
	ID() AgentID

	// ParentID returns the ID of the parent agent, or empty if top-level.
	ParentID() AgentID

	// Run processes a user message and streams events back.
	Run(ctx context.Context, userMsg string) <-chan Event

	// RunWithMessages processes with explicit message history.
	RunWithMessages(ctx context.Context, messages []Message) <-chan Event

	// Abort signals the agent to stop processing.
	Abort()

	// GetMessages returns a copy of the current message history.
	GetMessages() []Message

	// SetMessages replaces the message history.
	SetMessages(msgs []Message)

	// GetContext returns a copy of the current agent context.
	GetContext() *AgentContext

	// SetContext replaces the agent context.
	SetContext(ctx *AgentContext)

	// GetContextUsage returns the current context window usage, or nil if unavailable.
	GetContextUsage() *ContextUsage

	// LoadHistoryMessages loads historical messages into agent context.
	LoadHistoryMessages(messages []Message)

	// HandleApprovalResponse processes the user's approval response for a pending tool call.
	HandleApprovalResponse(approvalID string, approved bool)
}

// QuestionHandler is an optional extension of Agent that supports interactive questions.
// Only implemented by agents in TUI plan mode. Use type assertion to check support.
type QuestionHandler interface {
	Agent
	HandleQuestionResponse(questionID string, answer string)
}

// AgentConfigView is a read-only view of agent configuration for external inspection.
type AgentConfigView struct {
	ID       AgentID
	ParentID AgentID
	Mode     string
	ModelID  string
}

// ContextUsage reports how much of the context window is consumed.
type ContextUsage struct {
	Tokens        int // Deprecated alias for TotalTokens.
	TotalTokens   int // Full current input footprint.
	Input         int // Non-cache input tokens.
	CacheRead     int // Input tokens served from cache.
	CacheWrite    int // Input tokens written to cache.
	ContextWindow int
	Percent       *float64
}

// AgentContext holds the current agent conversation context.
type AgentContext struct {
	SystemPrompt string
	Messages     []Message
	Tools        []ToolDefinition
}

// Role identifies who produced a message.
type Role string

const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "toolResult"
	RoleSystem     Role = "system"
)

// Message represents a single message in the conversation.
type Message struct {
	Role           Role
	Content        string
	Contents       []ContentBlock
	Attachments    []Attachment
	IsError        bool
	SystemInjected bool
	ToolCallID     string
	ToolName       string
	ToolKind       string
	Usage          *Usage
}

// ContentBlock represents a typed block within a message.
type ContentBlock struct {
	Type         string // "text", "toolCall", "thinking", "image", "file", "audio", "video"
	Text         string
	ToolCall     *ToolCallBlock
	Thinking     string
	Signature    string
	Image        *ImageContent
	Audio        *AudioContent
	Video        *VideoContent
	File         *FileContent
	CacheControl *CacheControl
}

// FileContent identifies an existing provider file or an inline base64 file.
type FileContent struct {
	ID          string
	URL         string
	Data        string
	Filename    string
	MimeType    string
	Title       string
	Description string
	Size        *int
}

// ToolCallBlock represents a tool call requested by the LLM.
type ToolCallBlock struct {
	ID               string
	Name             string
	Kind             string
	Input            string
	Arguments        []byte
	InvalidArguments string
	ThoughtSignature string
}

// ImageContent represents an image in a content block.
type ImageContent struct {
	MimeType       string
	Data           string // base64-encoded
	Width          int
	Height         int
	Bytes          int
	OriginalWidth  int
	OriginalHeight int
	OriginalBytes  int
	Detail         string
	Scale          float64
	Cropped        bool
	CropX          int
	CropY          int
	CropWidth      int
	CropHeight     int
}

// AudioContent represents inline audio input in a content block. Audio input
// is user-turn only: assistant messages never carry audio blocks.
type AudioContent struct {
	MimeType string
	Format   string
	Data     string // base64-encoded
	URL      string
	Bytes    int
}

// VideoContent represents inline video input in a content block. Video input
// is user-turn only: assistant messages never carry video blocks.
type VideoContent struct {
	MimeType string
	Data     string // base64-encoded
	URL      string
	Bytes    int
}

// CacheControl represents cache control metadata on a content block.
type CacheControl struct {
	Type string // "ephemeral"
}

// ToolDefinition describes a tool available to the LLM.
type ToolDefinition struct {
	Name         string
	Description  string
	Parameters   []byte // JSON Schema
	Kind         string // function (default), custom, or hosted
	Format       []byte // custom tool text/grammar format
	Provider     string
	ProviderType string
	Model        string
}

// Usage tracks token consumption for a single LLM response.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
	TotalTokens  int
	Cost         CostBreakdown
}

// TotalInputTokens returns the full input footprint for the response, including
// cache reads and cache writes when the provider reports them separately. Use
// it as the prompt-cache hit-ratio denominator: providers differ in whether
// cached input is folded into InputTokens or reported apart, and this keeps the
// SDK, the CLI, and every front-end projection on one measurement basis.
func (u *Usage) TotalInputTokens() int {
	if u == nil {
		return 0
	}
	if u.TotalTokens > 0 {
		totalInput := u.TotalTokens - u.OutputTokens
		if totalInput > 0 {
			return totalInput
		}
	}
	return u.InputTokens + u.CacheRead + u.CacheWrite
}

// Attachment is a provider-neutral citation, file, image, or artifact emitted
// with a completed response.
type Attachment struct {
	Kind        string
	Name        string
	URL         string
	MediaType   string
	Metadata    map[string]any
	ProviderRef string
}

// CostBreakdown itemizes the cost of an LLM call.
type CostBreakdown struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Total      float64
}

// billableInputTokens returns the portion of input charged at the regular
// input rate. InputTokens returned by this SDK is normalized to non-cached
// input, but manually constructed values may still use a full prompt total.
func (u *Usage) billableInputTokens() int {
	input := u.InputTokens
	if totalInput := u.TotalTokens - u.OutputTokens; totalInput > 0 && totalInput == u.InputTokens {
		input -= u.CacheRead + u.CacheWrite
	}
	if input < 0 {
		return 0
	}
	return input
}

// CalculateCost computes cost based on model pricing.
func (u *Usage) CalculateCost(inputPrice, outputPrice, cacheReadPrice, cacheWritePrice float64) {
	u.Cost.Input = float64(u.billableInputTokens()) * inputPrice / 1_000_000
	u.Cost.Output = float64(u.OutputTokens) * outputPrice / 1_000_000
	u.Cost.CacheRead = float64(u.CacheRead) * cacheReadPrice / 1_000_000
	u.Cost.CacheWrite = float64(u.CacheWrite) * cacheWritePrice / 1_000_000
	u.Cost.Total = u.Cost.Input + u.Cost.Output + u.Cost.CacheRead + u.Cost.CacheWrite
}

// EventType identifies the type of agent event.
type EventType int

const (
	// Agent lifecycle events
	EventAgentStart EventType = iota
	EventAgentEnd

	// Turn lifecycle events (a turn = one assistant response + tool calls/results)
	EventTurnStart
	EventTurnEnd

	// Message lifecycle events
	EventMessageStart
	EventMessageUpdate
	EventMessageEnd

	// Streaming events
	EventTextDelta
	EventThinkDelta
	EventHostedItem

	// Tool execution events
	EventToolCall
	EventToolExecutionStart
	EventToolExecutionUpdate
	EventToolExecutionEnd
	EventToolResult
	EventToolApprovalRequest  // Request user approval for tool execution
	EventToolApprovalResponse // User response to approval request
	EventQuestionRequest      // Ask user a multiple-choice question
	EventQuestionResponse     // User response to question
	EventPlanUpdate           // Structured task plan update

	// Status events
	EventStatus
	EventDone
	EventError
	EventUsage

	// Compaction events
	EventCompactionStart
	EventCompactionEnd

	// Pressure and retry events
	EventContextPressure
	EventBudgetPressure
	EventRetry

	// EventRunFinished is the single canonical terminal event for a run. Exactly
	// one is emitted per run before legacy terminal events, carrying the TaskStatus outcome. Consumers should
	// prefer this over inferring success from EventDone/EventError/channel-close.
	EventRunFinished
)

// TaskStatus is the canonical terminal outcome of an agent run/task. It is
// carried by the EventRunFinished event and is the single source of truth that
// TUI, WebUI/Serve, and channel consumers use to classify a finished task.
type TaskStatus string

const (
	// TaskSuccess means the run completed normally and achieved its objective.
	TaskSuccess TaskStatus = "success"
	// TaskIncomplete means the run stopped before achieving its objective
	// (output/context limits, max iterations, stuck detection) without a hard error.
	TaskIncomplete TaskStatus = "incomplete"
	// TaskError means the run terminated due to an error.
	TaskError TaskStatus = "error"
	// TaskFailed is retained as a source-compatible alias for TaskError.
	// Deprecated: use TaskError.
	TaskFailed TaskStatus = TaskError
	// TaskCanceled means the run was canceled by the user, a timeout, or context cancellation.
	TaskCanceled TaskStatus = "canceled"
)

// IsTerminal reports whether the TaskStatus represents a finished run outcome.
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case TaskSuccess, TaskIncomplete, TaskFailed, TaskCanceled:
		return true
	}
	return false
}

// IsSuccessful reports whether the outcome is a successful completion.
func (s TaskStatus) IsSuccessful() bool { return s == TaskSuccess }

// Event represents an event from the agent to the consumer.
type Event struct {
	AgentID AgentID
	Type    EventType

	// Expert-team metadata is additive and populated on forwarded child-agent
	// events. It is a display snapshot so consumers can render a member without
	// resolving mutable expert-package content during replay.
	MemberID          string
	ExpertID          string
	MemberDisplayName string
	MemberEmoji       string
	MemberRole        string

	// Agent lifecycle
	Messages []Message

	// Turn lifecycle
	TurnMessage     Message
	TurnToolResults []Message

	// Message lifecycle
	Message Message

	// Stream events
	TextDelta  string
	ThinkDelta string
	HostedItem *HostedItem

	// Tool events
	ToolCall   *ToolCallBlock
	ToolCallID string
	ToolName   string
	ToolArgs   map[string]any
	ToolResult string
	ToolDiff   *FileDiff
	ToolError  error
	// ToolExecutionState is set to interrupted when idempotency recovery
	// refuses to repeat a tool whose prior process may have died mid-execution.
	ToolExecutionState string
	// ToolImages carries the image payloads embedded in a completed tool
	// result (EventToolExecutionEnd), for adapters that project them as image
	// content blocks. Data is base64-encoded. Additive: consumers that only
	// render ToolResult text can ignore it.
	ToolImages    []ToolImage
	PartialResult any

	// Plan events
	Plan *TaskPlan

	// Approval events
	ApprovalID     string
	ApprovalTool   string
	ApprovalArgs   map[string]any
	ApprovalResult bool

	// Question events
	QuestionID      string
	QuestionText    string
	QuestionOptions []string
	QuestionContext string
	QuestionAnswer  string

	// Status
	StatusMessage             string
	ResponseStateFailureClass string // expired, permission, request_failed
	// RetryStatus marks an EventStatus compatibility projection for the
	// following EventRetry. New adapters should consume EventRetry and ignore
	// this marked status to avoid rendering retry progress twice.
	RetryStatus bool

	// Retry information for automatic provider and turn recovery.
	RetryAttempt     int
	RetryMaxAttempts int
	RetryAfterMS     int
	RetryMaxTokens   int
	RetryReason      string
	RetryContinue    bool

	// Completion
	Done       bool
	StopReason string
	Error      error
	// Status is the canonical terminal outcome, set on EventRunFinished.
	Status TaskStatus

	// Usage
	Usage *Usage

	// Attachments emitted by the completed provider turn.
	Attachments []Attachment

	// Context usage
	ContextUsage *ContextUsage
}

// ToolImage is one image payload produced by a tool result. Data holds the
// base64-encoded image bytes and MimeType the image format, mirroring the
// provider image content the Agent Core already sends to vision models.
type ToolImage struct {
	MimeType string
	Data     string
}

// FileDiff describes a file change produced by a write-like tool.
type FileDiff struct {
	Path         string
	Added        int
	Deleted      int
	AddedLines   []int
	DeletedLines []int
	Unified      string
	OldText      *string
	NewText      string
	Truncated    bool
}

// TaskPlan describes a structured task plan emitted by the plan tool.
type TaskPlan struct {
	Title string
	Steps []PlanStep
	Note  string
}

// PlanStep describes one step in a task plan.
type PlanStep struct {
	Title  string
	Status string
}

// --- Helper constructors ---

// NewUserMessage creates a user message with plain text content.
func NewUserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

// NewAssistantMessage creates an assistant message with content blocks.
func NewAssistantMessage(contents []ContentBlock) Message {
	return Message{Role: RoleAssistant, Contents: contents}
}

// NewAssistantTextMessage creates an assistant message with plain text.
func NewAssistantTextMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: content}
}

// NewToolResultMessage creates a tool result message with plain text.
func NewToolResultMessage(toolCallID, toolName, content string, isError bool) Message {
	return Message{
		Role:       RoleToolResult,
		Content:    content,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		IsError:    isError,
	}
}

// NewToolResultMessageWithContents creates a tool result message with rich content blocks.
func NewToolResultMessageWithContents(toolCallID, toolName, text string, contents []ContentBlock, isError bool) Message {
	return Message{
		Role:       RoleToolResult,
		Content:    text,
		Contents:   contents,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		IsError:    isError,
	}
}

// NewSystemInjectedUserMessage creates a user message marked as system-injected
// (skipped by cache markers).
func NewSystemInjectedUserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content, SystemInjected: true}
}

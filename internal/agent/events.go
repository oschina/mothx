package agent

import (
	agentpkg "github.com/oschina/mothx/agent"
	ctxpkg "github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tools"
)

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
	EventRetry

	// Compaction events
	EventCompactionStart
	EventCompactionEnd

	// Pressure events
	EventContextPressure // Context usage exceeded threshold (one-shot)
	EventBudgetPressure  // Remaining iterations below threshold (one-shot)

	// EventRunFinished is the single canonical terminal event for a run. Exactly
	// one is emitted per run before the legacy EventDone/EventError and the
	// EventAgentEnd lifecycle event. It carries the TaskStatus outcome so consumers do not have to infer success from a mix of
	// EventDone/EventError events and channel-close signals.
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

// Event represents an event from the agent to the UI.
type Event struct {
	Type    EventType
	AgentID agentpkg.AgentID

	// Expert-team metadata (additive; empty when no expert team is bound).
	// MemberID and ExpertID identify the persona and bundle. The display fields
	// are immutable snapshots from the resolved member definition so replaying
	// an old event never needs to resolve potentially changed package content.
	MemberID          string
	ExpertID          string
	MemberDisplayName string
	MemberEmoji       string
	MemberRole        string

	// Agent lifecycle
	Messages []provider.Message

	// Turn lifecycle
	TurnMessage     provider.Message
	TurnToolResults []provider.Message

	// Message lifecycle
	Message provider.Message

	// Stream events
	TextDelta  string
	ThinkDelta string
	HostedItem *provider.HostedItem

	// Tool events
	ToolCall           *provider.ToolCallBlock
	ToolCallID         string
	ToolName           string
	ToolArgs           map[string]any
	ToolResult         string
	ToolDiff           *tools.FileDiff
	ToolError          error
	ToolExecutionState string
	// ToolImages carries the image payloads embedded in a completed tool
	// result (EventToolExecutionEnd) so adapters can project them as image
	// content blocks without re-parsing provider messages. Additive: text-only
	// consumers can ignore it.
	ToolImages    []ToolImage
	PartialResult any

	// Plan events
	Plan *tools.TaskPlan

	// Approval events
	ApprovalID     string         // Unique ID for approval request
	ApprovalTool   string         // Tool name requiring approval
	ApprovalArgs   map[string]any // Tool arguments
	ApprovalResult bool           // true = approved, false = denied

	// Question events
	QuestionID      string   // Unique ID for question request
	QuestionText    string   // The question to display
	QuestionOptions []string // Predefined options (last one is always "Custom input")
	QuestionContext string   // Optional context/explanation
	QuestionAnswer  string   // User's answer (set in response)

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
	// Assistant fields identify the final transcript entry staged for a
	// Runtime-owned terminal transaction. They are populated only on the
	// canonical EventRunFinished event.
	AssistantEntryID string
	AssistantMessage provider.Message
	// Status is the canonical terminal outcome, set on EventRunFinished.
	Status TaskStatus

	// Usage
	Usage *provider.Usage

	// Attachments are provider-neutral citations, files, images, and artifacts
	// produced by the completed turn.
	Attachments []provider.Attachment

	// Context usage
	ContextUsage *ctxpkg.ContextUsage

	// Pressure info (for EventContextPressure / EventBudgetPressure)
	PressureMessage string  // Human-readable warning message
	PressureType    string  // "context" or "budget"
	PressurePercent float64 // Usage percentage that triggered the event
}

// ToolImage is one image payload extracted from a rich tool result. Data holds
// the base64-encoded image bytes exactly as the tool produced them.
type ToolImage struct {
	MimeType string
	Data     string
}

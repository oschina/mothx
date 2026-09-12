package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	ctxpkg "github.com/startvibecoding/mothx/internal/context"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

// RunExecutor owns the lifecycle of a single agent execution.
// It consumes agent events, normalizes them, persists to SQLite,
// and publishes via EventBroker. It is independent of HTTP/SSE/WebSocket.
type RunExecutor struct {
	broker *EventBroker
	store  RunStore
	run    *session.SessionRun
	server *Server
	once   sync.Once
	done   chan struct{}
}

// RunStore is the persistence interface for run events.
type RunStore interface {
	SaveSessionRun(sessionDir string, run session.SessionRun) error
	UpdateSessionRunStatus(sessionDir, runID, status, message string, finishedAt *time.Time) error
	GetSessionRun(sessionDir, runID string) (*session.SessionRun, error)
	GetActiveSessionRun(sessionDir, sessionID string) (*session.SessionRun, error)
}

// NewRunExecutor creates a new RunExecutor for the given run.
func NewRunExecutor(srv *Server, broker *EventBroker, run *session.SessionRun) *RunExecutor {
	return &RunExecutor{
		broker: broker,
		server: srv,
		run:    run,
		done:   make(chan struct{}),
	}
}

// Execute consumes agent events from the channel and processes them.
// It runs in the calling goroutine (blocking) and closes the done channel
// when the agent finishes or errors.
// The caller is responsible for creating the agent and starting RunWithUserMessage.
func (e *RunExecutor) Execute(ctx context.Context, sess *APISession, a *agent.Agent, eventCh <-chan agent.Event, modelID, mode string, transcript bool) (*RunResult, error) {
	if e == nil {
		return nil, fmt.Errorf("run executor is nil")
	}
	result := &RunResult{
		RunID:     e.run.ID,
		SessionID: e.run.SessionID,
		Status:    "completed",
		ToolCalls: []ToolCallSummary{},
		ModelID:   modelID,
		StartTime: time.Now(),
	}
	var latestContextUsage *ctxpkg.ContextUsage
	defer func() {
		result.ContextUsage = latestContextUsage
		close(e.done)
	}()

	toolMode := ""
	toolDetail := ""
	if e.server != nil && e.server.cfg != nil {
		toolMode = e.server.cfg.ToolVisibility.Mode
		toolDetail = e.server.cfg.GetToolDetail()
	}

	pendingTools := make(map[string]*toolCallInfo)
	var totalUsage CompletionUsage

	for ev := range eventCh {
		select {
		case <-ctx.Done():
			result.Status = "canceled"
			result.Error = ctx.Err().Error()
			// Terminal events are sent unconditionally by the Agent loop, so a
			// buffer that filled up before the cancellation could still park the
			// aborted run on its next send and it could never finish its terminal
			// bookkeeping. Keep consuming the retired stream in the background.
			go func() {
				for range eventCh {
				}
			}()
			return result, nil
		default:
		}

		if ev.ContextUsage != nil {
			copy := *ev.ContextUsage
			latestContextUsage = &copy
		}
		// Error, retry, partial-output, and tool side-effect semantics are owned
		// by the shared Runtime. Serve only projects the returned contract.
		if execution := sess.executionRuntime(); execution != nil && (ev.Type != agent.EventRunFinished && ev.Type != agent.EventError || ev.AgentID == "") {
			observation, observeErr := execution.ObserveAgentEvent(ev)
			if observeErr != nil {
				result.Status = "failed"
				result.Error = "The run state could not be saved."
				result.ErrorInfo = &agentruntime.ErrorInfo{
					Code: "run_state_persistence_failed", Type: "server_error", FailureClass: agentruntime.FailurePersistence,
					Phase: agentruntime.PhasePersistence, MessageKey: "run.error.persistence", Message: result.Error,
					RetryMode: agentruntime.RetryUser, Retryable: true,
				}
				return result, nil
			}
			if observation.Error != nil {
				copy := *observation.Error
				result.ErrorInfo = &copy
			}
		}

		switch ev.Type {
		case agent.EventHostedItem:
			if ev.HostedItem != nil && e.server != nil {
				if e.run != nil {
					_ = e.server.recordSessionRunEvent(sess, e.run.ID, "hosted_item", ev.HostedItem.Status, e.run.Source, modelID, e.run.Mode, map[string]any{
						"hostedItem": safeHostedItemRunData(ev.HostedItem),
					})
				}
				evt := TranscriptStreamEvent{Type: "hosted_item", HostedItem: hostedItemEvent(ev.HostedItem)}
				if transcript {
					e.server.publishTranscriptEvent(sess.ID, evt)
				} else if e.broker != nil {
					e.broker.PublishTranscriptEvent(sess.ID, func() string {
						if e.run != nil {
							return e.run.ID
						}
						return ""
					}(), evt)
				}
			}
		case agent.EventStatus:
			if ev.ResponseStateFailureClass != "" && e.server != nil && e.run != nil {
				_ = e.server.recordSessionRunEvent(sess, e.run.ID, "responses_state_transition", "retrying", e.run.Source, modelID, e.run.Mode, map[string]any{
					"failureClass": ev.ResponseStateFailureClass,
				})
			}

		case agent.EventTextDelta:
			if e.server != nil {
				evt := assistantDeltaTranscriptEvent(ev.TextDelta, ev.AgentID, ev)
				if transcript {
					e.server.publishTranscriptEvent(sess.ID, evt)
				} else {
					// Always publish to the EventBroker for SSE subscribers.
					if broker := e.broker; broker != nil {
						runID := ""
						if e.run != nil {
							runID = e.run.ID
						}
						broker.PublishTranscriptEvent(sess.ID, runID, evt)
					}
				}
			}

		case agent.EventToolCall:
			name, callID := resolveToolEvent(ev)
			tc := &toolCallInfo{Name: name, Args: ev.ToolArgs, Status: "running"}
			if callID != "" {
				pendingTools[callID] = tc
			}
			result.ToolCalls = append(result.ToolCalls, ToolCallSummary{Name: name, Args: ev.ToolArgs, Status: "running"})
			if e.server != nil {
				e.server.publishToolEvent(sess.ID, ToolStatusEvent{
					Tool: name, ToolCallID: callID, AgentID: string(ev.AgentID),
					Status: "running", Args: ev.ToolArgs,
				})
			}

		case agent.EventToolExecutionEnd:
			status := "completed"
			if ev.ToolError != nil {
				status = "failed"
			}
			for i := len(result.ToolCalls) - 1; i >= 0; i-- {
				if result.ToolCalls[i].Name == ev.ToolName && result.ToolCalls[i].Status == "running" {
					result.ToolCalls[i].Status = status
					break
				}
			}
			tc := pendingTools[ev.ToolCallID]
			if tc == nil {
				tc = &toolCallInfo{Name: ev.ToolName, Args: ev.ToolArgs}
			}
			tc.Status = status
			tc.Result = ev.ToolResult
			tc.Diff = ev.ToolDiff
			tc.Error = ev.ToolError
			delete(pendingTools, ev.ToolCallID)
			name := ev.ToolName
			if name == "" {
				name = tc.Name
			}
			_ = toolMode
			_ = toolDetail
			if e.server != nil {
				e.server.publishToolEvent(sess.ID, ToolStatusEvent{
					Tool: name, ToolCallID: ev.ToolCallID, AgentID: string(ev.AgentID),
					Status: status, Args: tc.Args, Summary: toolStatusSummary(ev.ToolResult, ev.ToolError),
					IsError: ev.ToolError != nil, HasDetail: ev.ToolCallID != "",
				})
			}

		case agent.EventToolApprovalRequest:
			if e.server != nil {
				if execution := sess.executionRuntime(); execution != nil && e.run != nil {
					_ = execution.WaitForApproval(e.run.ID)
				}
				e.server.registerSessionApproval(sess, a, ev)
			}

		case agent.EventQuestionRequest:
			if e.server != nil && e.run != nil {
				e.server.registerSessionQuestion(sess, a, e.run.ID, ev)
			}

		case agent.EventUsage:
			if ev.Usage != nil {
				totalUsage.PromptTokens += ev.Usage.TotalInputTokens()
				totalUsage.CompletionTokens += ev.Usage.Output
				totalUsage.CacheReadTokens += ev.Usage.CacheRead
				totalUsage.CacheWriteTokens += ev.Usage.CacheWrite
				totalUsage.TotalTokens = totalUsage.PromptTokens + totalUsage.CompletionTokens
			}

		case agent.EventRetry:
			// ObserveAgentEvent has already persisted the canonical retrying event.

		case agent.EventRunFinished:
			if ev.AgentID != "" {
				continue // sub-agent terminal, not the main run
			}
			result.Usage = &totalUsage
			result.Attachments = append([]provider.Attachment(nil), ev.Attachments...)
			if len(ev.Attachments) > 0 && e.server != nil {
				evt := assistantAttachmentsTranscriptEvent(ev.Attachments, ev.AgentID)
				if transcript {
					e.server.publishTranscriptEvent(sess.ID, evt)
				} else if e.broker != nil {
					runID := ""
					if e.run != nil {
						runID = e.run.ID
					}
					e.broker.PublishTranscriptEvent(sess.ID, runID, evt)
				}
			}
			result.Status = runStatusForTaskStatus(ev.Status)
			if result.ErrorInfo != nil {
				result.Error = agentruntime.DisplayErrorMessage(*result.ErrorInfo)
			} else if ev.Error != nil {
				info := agentruntime.ClassifyError(ev.Error, agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseModel})
				result.ErrorInfo = &info
				result.Error = agentruntime.DisplayErrorMessage(info)
			} else if result.Status == "failed" {
				info := agentruntime.ClassifyError(nil, agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseTerminalization})
				result.ErrorInfo = &info
				result.Error = agentruntime.DisplayErrorMessage(info)
			}
			return result, nil

		case agent.EventDone:
			if ev.AgentID != "" {
				continue // sub-agent done, not the main run
			}
			result.Usage = &totalUsage
			result.Attachments = append([]provider.Attachment(nil), ev.Attachments...)
			if len(ev.Attachments) > 0 && e.server != nil {
				evt := assistantAttachmentsTranscriptEvent(ev.Attachments, ev.AgentID)
				if transcript {
					e.server.publishTranscriptEvent(sess.ID, evt)
				} else if e.broker != nil {
					runID := ""
					if e.run != nil {
						runID = e.run.ID
					}
					e.broker.PublishTranscriptEvent(sess.ID, runID, evt)
				}
			}
			result.Status = "completed"
			return result, nil

		case agent.EventError:
			if ev.AgentID != "" {
				continue // sub-agent error, not the main run
			}
			if ev.ResponseStateFailureClass != "" && e.server != nil && e.run != nil {
				_ = e.server.recordSessionRunEvent(sess, e.run.ID, "responses_state_transition", "failed", e.run.Source, modelID, e.run.Mode, map[string]any{
					"failureClass": ev.ResponseStateFailureClass,
				})
			}
			result.Usage = &totalUsage
			if result.ErrorInfo != nil {
				result.Error = agentruntime.DisplayErrorMessage(*result.ErrorInfo)
				result.Status = "failed"
			} else if ev.Error != nil {
				if errors.Is(ev.Error, context.Canceled) || errors.Is(ev.Error, context.DeadlineExceeded) {
					result.Status = "canceled"
				} else {
					result.Status = "failed"
				}
				info := agentruntime.ClassifyError(ev.Error, agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseModel})
				result.ErrorInfo = &info
				result.Error = agentruntime.DisplayErrorMessage(info)
			} else {
				// An error event without an error payload is a protocol violation,
				// never a successful completion.
				result.Status = "failed"
				info := agentruntime.ClassifyError(nil, agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseTerminalization})
				result.ErrorInfo = &info
				result.Error = agentruntime.DisplayErrorMessage(info)
			}
			return result, nil
		}
	}
	// Channel closed without any terminal event. This is a protocol failure and
	// must never be reported as a successful completion.
	result.Usage = &totalUsage
	result.Status = "failed"
	result.Error = "The run stopped before it could finish."
	result.ErrorInfo = &agentruntime.ErrorInfo{
		Code: "event_stream_interrupted", Type: "transport_error", FailureClass: agentruntime.FailureTransport,
		Phase: agentruntime.PhaseTransport, MessageKey: "run.error.streamInterrupted", Message: result.Error,
		RetryMode: agentruntime.RetryUser, Retryable: true,
	}
	finalizeExecutionRuntime(sess, e.run, result)
	return result, nil
}

func finalizeExecutionRuntime(sess *APISession, run *session.SessionRun, result *RunResult) {
	execution := sess.executionRuntime()
	if sess == nil || run == nil || result == nil || sess.isDurableRun(run.ID) || execution == nil {
		return
	}
	state := agentruntime.RunStateCompleted
	switch {
	case result.Status == "canceled" && strings.Contains(strings.ToLower(result.Error), "deadline"):
		state = agentruntime.RunStateTimedOut
	case result.Status == "canceled":
		state = agentruntime.RunStateCancelled
	case result.Status == "failed":
		state = agentruntime.RunStateFailed
	}
	_ = execution.FinishWithState(run.ID, state)
}

// runStatusForTaskStatus maps the canonical agent TaskStatus to the run status
// vocabulary persisted for session runs.
func runStatusForTaskStatus(status agent.TaskStatus) string {
	switch status {
	case agent.TaskSuccess:
		return "completed"
	case agent.TaskIncomplete:
		return "incomplete"
	case agent.TaskCanceled:
		return "canceled"
	default:
		return "failed"
	}
}

// Done returns a channel that is closed when the run finishes.
func (e *RunExecutor) Done() <-chan struct{} {
	if e == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return e.done
}

// Finalize is called exactly once to clean up the run after execution.
// It is idempotent via sync.Once.
func (e *RunExecutor) Finalize(sess *APISession, result *RunResult) {
	if e == nil {
		return
	}
	e.once.Do(func() {
		// Durable conversation turns stage the final assistant message during
		// Execute and commit it in the caller's FinishDurable path. Do not publish
		// a stream terminal event here: the WebUI handles `done` by reloading the
		// transcript, so emitting it before that commit would make the reload race
		// the database write and overwrite the live assistant text with an older
		// history snapshot. FinalizeRun publishes the terminal snapshot and `done`
		// after durable persistence (and remains the single terminal publisher).
		// Legacy/non-durable executions have no later FinishDurable owner, so keep
		// their historical projection behavior.
		if e.server == nil || sess == nil || e.run == nil || sess.isDurableRun(e.run.ID) {
			return
		}
		status := "failed"
		if result != nil {
			status = result.Status
		}
		e.server.publishSessionRuntime(sess)
		e.server.publishSessionStreamDone(sess.ID, e.run.ID, status)
	})
}

// RunResult captures the outcome of a single run execution.
type RunResult struct {
	RunID        string
	SessionID    string
	Status       string                  // "completed", "failed", "canceled"
	Error        string                  // non-empty if failed/canceled
	ErrorInfo    *agentruntime.ErrorInfo // structured safe failure, when non-successful
	Usage        *CompletionUsage        // final token usage
	ContextUsage *ctxpkg.ContextUsage    // final request-context footprint
	ToolCalls    []ToolCallSummary       // tool calls made during the run
	Attachments  []provider.Attachment   // citations, files, images, and artifacts
	ModelID      string
	StartTime    time.Time
}

// toolCallInfo is defined in tool_format.go.

// resolveToolEvent is defined in handler_chat.go.

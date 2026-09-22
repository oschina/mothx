package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/agent"
)

// AgentEventObservation is the adapter-neutral result of consuming an Agent
// Core event. The adapter may render it in its protocol, but must not infer
// retry safety or terminal error semantics from presentation text.
type AgentEventObservation struct {
	Retry *RetryInfo
	Error *ErrorInfo
}

type executionFacts struct {
	phase           RunPhase
	sideEffects     SideEffectState
	partialOutput   bool
	lastRetry       RetryInfo
	lastRetryActive bool
	lastError       ErrorInfo
}

// RecordFailure records a failure that happened before an Agent event stream
// existed, such as shared resource or Agent construction. It uses the same
// durable error contract as ObserveAgentEvent so adapters never need a second
// error classifier for preflight failures.
func (r *ExecutionRuntime) RecordFailure(err error, opts ErrorClassificationOptions) (ErrorInfo, error) {
	if r == nil {
		return ClassifyError(err, opts), nil
	}
	r.mu.Lock()
	facts := r.facts
	run := DurableRun{}
	if r.durable != nil {
		run = *r.durable
	}
	r.mu.Unlock()
	if opts.Phase == "" {
		opts.Phase = facts.phase
	}
	if opts.SideEffectState == "" {
		opts.SideEffectState = facts.sideEffects
	}
	if !opts.PartialOutput {
		opts.PartialOutput = facts.partialOutput
	}
	if opts.Attempt == 0 {
		opts.Attempt = facts.lastRetry.Attempt
	}
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = facts.lastRetry.MaxAttempts
	}
	if opts.RetryAfterMS == 0 {
		opts.RetryAfterMS = facts.lastRetry.RetryAfterMS
	}
	if opts.RunID == "" {
		opts.RunID = run.ID
	}
	if opts.IntentID == "" {
		opts.IntentID = run.IntentID
	}
	info := ClassifyError(err, opts)
	return r.RecordErrorInfo(info)
}

// RecordErrorInfo records a previously classified, safe failure. It supports
// remote/background paths that fail outside an Agent event stream while
// keeping durable error state and terminal-event facts Runtime-owned.
func (r *ExecutionRuntime) RecordErrorInfo(info ErrorInfo) (ErrorInfo, error) {
	if r == nil {
		return info, nil
	}
	r.mu.Lock()
	facts := r.facts
	run := DurableRun{}
	if r.durable != nil {
		run = *r.durable
	}
	info = enrichErrorInfo(info, facts, run)
	r.facts.lastError = info
	r.mu.Unlock()
	if persistErr := r.persistErrorInfo(run, info); persistErr != nil {
		return info, persistErr
	}
	return info, nil
}

// ObserveAgentEvent normalizes the Agent Core event stream into the shared
// execution contract. It tracks output and tool facts, persists retry progress
// for reconnecting adapters, and returns structured terminal errors. It does
// not terminalize the Run: that remains the caller's single FinishDurable path.
func (r *ExecutionRuntime) ObserveAgentEvent(ev agent.Event) (AgentEventObservation, error) {
	if r == nil {
		return AgentEventObservation{}, nil
	}
	if ev.Type == agent.EventRunFinished && ev.AssistantMessage.Role == "assistant" {
		// Agent events do not carry the Runtime Run ID as a separate field;
		// the active Run is authoritative for this staging operation. A
		// terminal event observed after shutdown is harmless and should not
		// turn a successful protocol projection into a persistence failure.
		if activeID, active := r.Active(); active {
			if err := r.SetAssistantMessage(activeID, ev.AssistantEntryID, ev.AssistantMessage); err != nil {
				return AgentEventObservation{}, err
			}
		}
	}

	r.mu.Lock()
	if r.facts.phase == "" {
		r.facts.phase = PhaseModel
	}
	switch ev.Type {
	case agent.EventTextDelta:
		if strings.TrimSpace(ev.TextDelta) != "" {
			r.facts.partialOutput = true
		}
	case agent.EventThinkDelta, agent.EventHostedItem:
		if strings.TrimSpace(ev.ThinkDelta) != "" || ev.HostedItem != nil {
			r.facts.partialOutput = true
		}
	case agent.EventContextPressure, agent.EventCompactionStart, agent.EventCompactionEnd:
		r.facts.phase = PhaseContext
	case agent.EventToolExecutionStart:
		r.facts.phase = PhaseTool
		r.facts.sideEffects = combineSideEffectState(r.facts.sideEffects, toolSideEffectState(ev.ToolName))
	case agent.EventToolExecutionEnd:
		r.facts.phase = PhaseTool
		effect := toolSideEffectState(ev.ToolName)
		if ev.ToolExecutionState == "interrupted" {
			effect = SideEffectUnknown
		}
		r.facts.sideEffects = combineSideEffectState(r.facts.sideEffects, effect)
	case agent.EventToolApprovalRequest:
		r.facts.phase = PhaseApproval
	case agent.EventStatus:
		if ev.ResponseStateFailureClass != "" {
			r.facts.phase = PhaseTransport
		}
	}
	facts := r.facts
	run := DurableRun{}
	if r.durable != nil {
		run = *r.durable
	}
	r.mu.Unlock()

	switch ev.Type {
	case agent.EventRetry:
		retry := retryInfoFromAgentEvent(ev, facts.phase)
		r.mu.Lock()
		r.facts.lastRetry = retry
		r.facts.lastRetryActive = true
		r.mu.Unlock()
		if err := r.persistRetryProgress(run, facts, retry); err != nil {
			return AgentEventObservation{}, err
		}
		return AgentEventObservation{Retry: &retry}, nil
	case agent.EventError:
		info := r.errorInfoForAgentFailure(ev.Error, facts, run)
		recorded, err := r.RecordErrorInfo(info)
		if err != nil {
			return AgentEventObservation{}, err
		}
		return AgentEventObservation{Error: &recorded}, nil
	case agent.EventRunFinished:
		if ev.Status.IsSuccessful() {
			if err := r.clearRetryProgress(run); err != nil {
				return AgentEventObservation{}, err
			}
			return AgentEventObservation{}, nil
		}
		info := r.errorInfoForTerminalEvent(ev, facts, run)
		recorded, err := r.RecordErrorInfo(info)
		if err != nil {
			return AgentEventObservation{}, err
		}
		if err := r.clearRetryProgress(run); err != nil {
			return AgentEventObservation{}, err
		}
		return AgentEventObservation{Error: &recorded}, nil
	}
	return AgentEventObservation{}, nil
}

func (r *ExecutionRuntime) errorInfoForAgentFailure(err error, facts executionFacts, run DurableRun) ErrorInfo {
	return ClassifyError(err, ErrorClassificationOptions{
		Phase:           facts.phase,
		Attempt:         facts.lastRetry.Attempt,
		MaxAttempts:     facts.lastRetry.MaxAttempts,
		RetryAfterMS:    facts.lastRetry.RetryAfterMS,
		SideEffectState: facts.sideEffects,
		PartialOutput:   facts.partialOutput,
		RunID:           run.ID,
		IntentID:        run.IntentID,
	})
}

func (r *ExecutionRuntime) errorInfoForTerminalEvent(ev agent.Event, facts executionFacts, run DurableRun) ErrorInfo {
	if ev.Error != nil {
		return r.errorInfoForAgentFailure(ev.Error, facts, run)
	}
	info := ErrorInfo{
		Phase:           facts.phase,
		Attempt:         facts.lastRetry.Attempt,
		MaxAttempts:     facts.lastRetry.MaxAttempts,
		RetryAfterMS:    facts.lastRetry.RetryAfterMS,
		SideEffectState: facts.sideEffects,
		PartialOutput:   facts.partialOutput,
		RunID:           run.ID,
		IntentID:        run.IntentID,
	}
	switch ev.Status {
	case agent.TaskCanceled:
		return applyErrorDefaults(info, "run_cancelled", "canceled", FailureCancelled, RetryUser, false, "run.error.cancelled")
	case agent.TaskIncomplete:
		return applyErrorDefaults(info, "run_incomplete", "incomplete_error", FailureIncomplete, retryModeForSafety(info), false, "run.error.incomplete")
	default:
		return ClassifyError(errors.New("agent run failed without error detail"), ErrorClassificationOptions{
			Phase:           facts.phase,
			Attempt:         facts.lastRetry.Attempt,
			MaxAttempts:     facts.lastRetry.MaxAttempts,
			RetryAfterMS:    facts.lastRetry.RetryAfterMS,
			SideEffectState: facts.sideEffects,
			PartialOutput:   facts.partialOutput,
			RunID:           run.ID,
			IntentID:        run.IntentID,
		})
	}
}

func enrichErrorInfo(info ErrorInfo, facts executionFacts, run DurableRun) ErrorInfo {
	if info.Phase == "" {
		info.Phase = facts.phase
	}
	if info.Phase == "" {
		info.Phase = PhaseTerminalization
	}
	if info.SideEffectState == "" {
		info.SideEffectState = facts.sideEffects
	}
	if info.SideEffectState == "" {
		info.SideEffectState = SideEffectNone
	}
	if facts.partialOutput {
		info.PartialOutput = true
	}
	if info.Attempt == 0 {
		info.Attempt = facts.lastRetry.Attempt
	}
	if info.MaxAttempts == 0 {
		info.MaxAttempts = facts.lastRetry.MaxAttempts
	}
	if info.RetryAfterMS == 0 {
		info.RetryAfterMS = facts.lastRetry.RetryAfterMS
	}
	if info.RunID == "" {
		info.RunID = run.ID
	}
	if info.IntentID == "" {
		info.IntentID = run.IntentID
	}
	return info
}

func terminalErrorInfoFor(state RunState, message string, facts executionFacts, run DurableRun) ErrorInfo {
	if facts.lastError.Code != "" {
		return enrichErrorInfo(facts.lastError, facts, run)
	}
	opts := ErrorClassificationOptions{
		Phase:           facts.phase,
		Attempt:         facts.lastRetry.Attempt,
		MaxAttempts:     facts.lastRetry.MaxAttempts,
		RetryAfterMS:    facts.lastRetry.RetryAfterMS,
		SideEffectState: facts.sideEffects,
		PartialOutput:   facts.partialOutput,
		RunID:           run.ID,
		IntentID:        run.IntentID,
	}
	switch state {
	case RunStateCancelled:
		return ClassifyError(context.Canceled, opts)
	case RunStateTimedOut:
		return ClassifyError(context.DeadlineExceeded, opts)
	case RunStateIncomplete:
		info := ErrorInfo{
			Phase: opts.Phase, Attempt: opts.Attempt, MaxAttempts: opts.MaxAttempts,
			RetryAfterMS: opts.RetryAfterMS, SideEffectState: opts.SideEffectState,
			PartialOutput: opts.PartialOutput, RunID: opts.RunID, IntentID: opts.IntentID,
		}
		return applyErrorDefaults(info, "run_incomplete", "incomplete_error", FailureIncomplete, retryModeForSafety(info), false, "run.error.incomplete")
	default:
		return ClassifyError(errors.New(message), opts)
	}
}

func (r *ExecutionRuntime) persistRetryProgress(run DurableRun, facts executionFacts, retry RetryInfo) error {
	store, ok := r.runStore().(DurableRunMetadataStore)
	if ok && store != nil && run.ID != "" {
		if err := store.UpdateProgress(run.ID, retry); err != nil {
			return fmt.Errorf("persist retry progress: %w", err)
		}
	}
	if run.ID == "" || run.SessionID == "" {
		return nil
	}
	data, err := json.Marshal(struct {
		State        string          `json:"state"`
		Attempt      int             `json:"attempt,omitempty"`
		MaxAttempts  int             `json:"maxAttempts,omitempty"`
		Phase        RunPhase        `json:"phase,omitempty"`
		ReasonCode   string          `json:"reasonCode,omitempty"`
		RetryAfterMS int             `json:"retryAfterMs,omitempty"`
		Continue     bool            `json:"continue,omitempty"`
		MessageKey   string          `json:"messageKey,omitempty"`
		Message      string          `json:"message,omitempty"`
		SideEffects  SideEffectState `json:"sideEffectState"`
		Partial      bool            `json:"partialOutput"`
	}{
		State: "retrying", Attempt: retry.Attempt, MaxAttempts: retry.MaxAttempts, Phase: retry.Phase,
		ReasonCode: retry.ReasonCode, RetryAfterMS: retry.RetryAfterMS, Continue: retry.Continue,
		MessageKey: retry.MessageKey, Message: retry.Message, SideEffects: facts.sideEffects, Partial: facts.partialOutput,
	})
	if err != nil {
		return fmt.Errorf("encode retry event: %w", err)
	}
	_, err = r.RecordEvent(RunEvent{
		SessionID: run.SessionID,
		RunID:     run.ID,
		EventType: "run_retrying",
		Source:    run.Source,
		Status:    string(RunStateRunning),
		Model:     run.Model,
		Mode:      run.Mode,
		Timestamp: time.Now(),
		Data:      data,
	})
	if err != nil {
		return fmt.Errorf("record retry event: %w", err)
	}
	return nil
}

func (r *ExecutionRuntime) persistErrorInfo(run DurableRun, info ErrorInfo) error {
	store, ok := r.runStore().(DurableRunMetadataStore)
	if !ok || store == nil || run.ID == "" {
		return nil
	}
	if err := store.UpdateErrorInfo(run.ID, info); err != nil {
		return fmt.Errorf("persist error info: %w", err)
	}
	return nil
}

func (r *ExecutionRuntime) clearRetryProgress(run DurableRun) error {
	store, ok := r.runStore().(DurableRunMetadataStore)
	if !ok || store == nil || run.ID == "" {
		return nil
	}
	if err := store.UpdateProgress(run.ID, RetryInfo{}); err != nil {
		return fmt.Errorf("clear retry progress: %w", err)
	}
	return nil
}

func retryInfoFromAgentEvent(ev agent.Event, phase RunPhase) RetryInfo {
	if phase == "" {
		phase = PhaseModel
	}
	info := RetryInfo{
		Attempt:      ev.RetryAttempt,
		MaxAttempts:  ev.RetryMaxAttempts,
		Phase:        phase,
		ReasonCode:   retryReasonCode(ev.RetryReason, ev.RetryContinue),
		RetryAfterMS: ev.RetryAfterMS,
		Continue:     ev.RetryContinue,
		// EventRetry.StatusMessage carries the provider diagnostic. Redact and
		// bound it like terminal error diagnostics before it enters durable
		// run records; adapters keep rendering the friendly MessageKey first.
		Message: diagnosticMessage(nil, ev.StatusMessage),
	}
	if info.Continue {
		info.MessageKey = "run.retry.continuing"
	} else {
		info.MessageKey = "run.retrying"
	}
	return info
}

func retryReasonCode(reason string, continuation bool) string {
	if continuation {
		return "continuation"
	}
	value := strings.ToLower(reason)
	switch {
	case strings.Contains(value, "timeout"):
		return "timeout"
	case strings.Contains(value, "empty"):
		return "empty_response"
	case strings.Contains(value, "token"), strings.Contains(value, "truncat"):
		return "output_limit"
	case strings.Contains(value, "context"):
		return "context"
	default:
		return "provider"
	}
}

func combineSideEffectState(current, next SideEffectState) SideEffectState {
	if current == SideEffectMutating || next == SideEffectMutating {
		return SideEffectMutating
	}
	if current == SideEffectUnknown || next == SideEffectUnknown {
		return SideEffectUnknown
	}
	if current == SideEffectReadOnly || next == SideEffectReadOnly {
		return SideEffectReadOnly
	}
	return SideEffectNone
}

func toolSideEffectState(name string) SideEffectState {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "read_file", "list", "list_dir", "glob", "grep", "search", "search_files", "find", "web_search":
		return SideEffectReadOnly
	default:
		// Shell, MCP, browser, and unrecognized tools are all conservatively
		// treated as unknown unless the shared tool contract gains an explicit
		// side-effect classification.
		return SideEffectUnknown
	}
}

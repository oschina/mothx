package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

const responsesBackgroundPollInterval = time.Second

var ErrResponsesRuntimeBusy = errors.New("Responses background session runtime is busy")

func (s *Server) responsesBackgroundEnabled() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	provider := s.provider
	manager := s.responsesRuns
	s.mu.RUnlock()
	backgroundProvider, ok := provider.(interface{ ResponsesBackgroundEnabled() bool })
	return manager != nil && ok && backgroundProvider.ResponsesBackgroundEnabled()
}

// executeResponsesBackgroundRun owns one remote Responses background task.
// It deliberately coordinates durable run state instead of invoking Agent.Run:
// a remote response_id can outlive this process, whereas an Agent loop cannot.
func (s *Server) executeResponsesBackgroundRun(sess *APISession, runID string, runtimeRelease func(), model *provider.Model, mode string, msg provider.Message, transcript bool) {
	s.executeResponsesBackgroundRunWithConfig(sess, runID, runtimeRelease, model, mode, msg, transcript, nil, nil, nil, nil)
}

// executeResponsesBackgroundRunWithConfig lets another serve entry point hand
// the already-resolved request configuration to the shared durable runtime.
func (s *Server) executeResponsesBackgroundRunWithConfig(sess *APISession, runID string, runtimeRelease func(), model *provider.Model, mode string, msg provider.Message, transcript bool, agentOpts *agentruntime.AgentBuildOptions, initialHistory []provider.Message, complete func(string, []provider.Attachment, error), progress func(string)) {
	terminalStatus := "failed"
	conversationTurnID := "turn-" + runID
	runSource := "responses_background"
	resolvedSource, resolvedMode, policyErr := s.canonicalRunIdentity(sess, runSource, mode)
	if policyErr == nil {
		runSource = resolvedSource
		mode = resolvedMode
	}
	defer runtimeRelease()
	defer sess.Unlock()
	defer func() {
		if complete == nil {
			return
		}
		var response string
		var attachments []provider.Attachment
		if messages := sess.Manager.GetMessages(); len(messages) > 0 {
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == "assistant" && (strings.TrimSpace(messageText(messages[i])) != "" || len(messages[i].Attachments) > 0) {
					response = messageText(messages[i])
					attachments = append([]provider.Attachment(nil), messages[i].Attachments...)
					break
				}
			}
		}
		if terminalStatus == "completed" || terminalStatus == "incomplete" {
			complete(response, attachments, nil)
		} else {
			complete(response, attachments, fmt.Errorf("background Responses run ended with status %s", terminalStatus))
		}
	}()

	durableLifecycle := sess.isDurableRun(runID)
	defer func() {
		if execution := sess.executionRuntime(); durableLifecycle && execution != nil {
			if err := execution.FinishDurableWithRetry(context.Background(), runID, webUIRunState(terminalStatus, ""), "", agentruntime.RunEvent{
				SessionID: sess.ID, RunID: runID, EventType: runEventTypeForStatus(terminalStatus),
				Source: runSource, Status: terminalStatus, Model: model.ID, Mode: mode, Timestamp: time.Now(),
			}); err != nil {
				// Cancellation may have terminalized the durable row concurrently.
				if _, active := execution.Active(); active {
					_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", runSource, model.ID, mode, map[string]any{"error": err.Error()})
				}
			}
		}
		s.FinalizeRun(sess, runID, terminalStatus, "")
	}()
	if policyErr != nil {
		_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", runSource, model.ID, mode, map[string]any{"error": policyErr.Error()})
		return
	}

	s.mu.RLock()
	manager := s.responsesRuns
	s.mu.RUnlock()
	if manager == nil {
		return
	}

	if agentOpts == nil {
		opts := s.buildAgentOptionsForSession(sess, model, mode)
		agentOpts = &opts
	}
	backgroundOpts := *agentOpts
	if backgroundOpts.IntentID != "" {
		conversationTurnID = "turn-" + backgroundOpts.IntentID
	}
	backgroundOpts.RunID = runID
	backgroundOpts.ConversationTurnID = conversationTurnID
	backgroundOpts.ConversationTurn = true
	backgroundOpts.RuntimeOwnsTurnEnd = true
	backgroundOpts.ProviderName = s.providerName
	backgroundOpts.Provider = s.provider
	backgroundOpts.Model = model
	backgroundAgent, err := sess.Runtime.BuildAgent(backgroundOpts)
	if err != nil {
		_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", model.ID, mode, map[string]any{"error": err.Error()})
		return
	}
	replayState := sess.Manager.GetReplayState()
	if len(replayState.Messages) > 0 {
		backgroundAgent.LoadHistoryState(replayState.Messages, replayState.EntryIDs)
	} else if len(initialHistory) > 0 {
		backgroundAgent.LoadHistoryMessages(initialHistory)
	}
	reusePersistedMessage := s.runRetriesPersistedMessage(runID, sess, replayState.Messages, msg)
	if !reusePersistedMessage && replayStateContainsRunUserEntry(replayState, runID) {
		// Durable admission atomically appended the run's admitted user entry
		// before this coordinator started, and the manager reload surfaced it
		// in the replay state. Reuse it as the continuation message instead
		// of appending a duplicate to the transcript and the provider request.
		reusePersistedMessage = true
	}
	var params provider.ChatParams
	if reusePersistedMessage {
		params, err = backgroundAgent.BuildBackgroundContinuationParams(runID)
	} else {
		params, err = backgroundAgent.BuildBackgroundChatParams(runID, msg)
	}
	if err != nil {
		_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", model.ID, mode, map[string]any{"error": err.Error()})
		return
	}

	// Keep the local transcript authoritative before any remote request exists.
	// Runs whose user entry was already persisted by durable admission or a
	// previous attempt skip this append to keep the transcript idempotent.
	if !reusePersistedMessage {
		if _, err := sess.Manager.AppendMessage(msg); err != nil {
			_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", model.ID, mode, map[string]any{"error": err.Error()})
			return
		}
	}

	requestCtx, requestCancel := context.WithTimeout(context.Background(), 30*time.Second)
	run, err := manager.Start(requestCtx, sess.ID, runID, params)
	requestCancel()
	if err != nil {
		_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", model.ID, mode, map[string]any{"error": err.Error()})
		return
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	if !sess.attachRunAgent(runID, backgroundAgent, func() {
		cancelRun()
		backgroundAgent.Abort()
	}) {
		return
	}
	if s.runManager != nil {
		s.attachResponsesBackgroundCancel(runID, sess.ID, run.LocalRunID, func() {
			cancelRun()
			backgroundAgent.Abort()
		})
	}
	if execution := sess.executionRuntime(); durableLifecycle && execution != nil {
		_ = execution.UpdateDurable(runID, agentruntime.RunStateRunning, "")
	} else {
		_ = agentruntime.UpdateDurableRun(s.settings.GetSessionDir(), runID, agentruntime.RunStateRunning, "")
	}
	_ = s.recordSessionRunEvent(sess, runID, "remote_started", "running", "responses_background", model.ID, mode, map[string]any{
		"responseRunId": run.LocalRunID,
		"responseId":    run.ResponseID,
		"state":         run.State,
	})
	s.publishSessionRuntime(sess)

	replayAttempted := false
	hostedDeadline := responsesHostedDeadline(s.provider)
	maxDeadline := time.Now().Add(s.backgroundRunMaxDuration())
	for {
		if !time.Now().Before(maxDeadline) {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = manager.Cancel(cancelCtx, sess.ID, run.LocalRunID)
			cancel()
			terminalStatus = "incomplete"
			_ = s.recordSessionRunEvent(sess, runID, "finished", terminalStatus, "responses_background", model.ID, mode, map[string]any{
				"responseRunId": run.LocalRunID, "responseId": run.ResponseID,
				"incompleteReason": "mothx_background_run_max_duration",
			})
			return
		}
		if !hostedDeadline.IsZero() && !time.Now().Before(hostedDeadline) {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = manager.Cancel(cancelCtx, sess.ID, run.LocalRunID)
			cancel()
			terminalStatus = "incomplete"
			_ = s.recordSessionRunEvent(sess, runID, "finished", terminalStatus, "responses_background", model.ID, mode, map[string]any{
				"responseRunId": run.LocalRunID, "responseId": run.ResponseID,
				"incompleteReason": "mothx_code_interpreter_timeout",
			})
			return
		}
		if isTerminalResponsesRunState(run.State) {
			if strings.EqualFold(strings.TrimSpace(run.State), "expired") && !replayAttempted {
				previousRunID := run.LocalRunID
				next, replayErr := s.startResponsesBackgroundReplay(context.Background(), sess.ID, runID, run.LocalTurnID, backgroundAgent)
				if replayErr == nil {
					replayAttempted = true
					run = next
					_ = s.recordSessionRunEvent(sess, runID, "remote_state_replay", "running", "responses_background", model.ID, mode, map[string]any{
						"responseRunId": next.LocalRunID, "previousResponseRunId": previousRunID,
						"reason": "remote Responses state expired",
					})
					continue
				}
			}
			calls, err := responsesBackgroundFunctionCallsForRun(s.settings.GetSessionDir(), sess.ID, run.LocalTurnID)
			if err != nil {
				_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", model.ID, mode, map[string]any{"error": err.Error()})
				return
			}
			if len(calls) > 0 {
				outputs, ok := s.executeResponsesBackgroundToolsWithProgress(runCtx, sess, backgroundAgent, runID, run.LocalTurnID, calls, false, progress)
				if !ok {
					return
				}
				continuationTurnID := runID + ":" + run.LocalRunID
				continueCtx, continueCancel := context.WithTimeout(context.Background(), 30*time.Second)
				next, continueErr := manager.Continue(continueCtx, sess.ID, continuationTurnID, run, outputs, params)
				continueCancel()
				if continueErr != nil {
					if backgroundAgent.ResponsesStateFallbackError(continueErr) {
						previousRunID := run.LocalRunID
						replayParams, replayErr := backgroundAgent.BuildBackgroundReplayParams(continuationTurnID)
						if replayErr == nil {
							replayCtx, replayCancel := context.WithTimeout(context.Background(), 30*time.Second)
							next, replayErr := manager.Start(replayCtx, sess.ID, continuationTurnID+":replay", replayParams)
							replayCancel()
							if replayErr == nil {
								run = next
								_ = s.recordSessionRunEvent(sess, runID, "remote_state_replay", "running", "responses_background", model.ID, mode, map[string]any{
									"responseRunId": next.LocalRunID, "previousResponseRunId": previousRunID,
									"reason": "remote Responses state unavailable",
								})
								continue
							}
						}
					}
					_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", model.ID, mode, map[string]any{"error": continueErr.Error(), "responseRunId": run.LocalRunID})
					return
				}
				run = next
				_ = s.recordSessionRunEvent(sess, runID, "remote_continuation", "running", "responses_background", model.ID, mode, map[string]any{"responseRunId": run.LocalRunID, "responseId": run.ResponseID})
				continue
			}
			terminalStatus = s.finalizeResponsesBackgroundResult(sess, runID, model.ID, mode, run, transcript)
			return
		}
		timer := time.NewTimer(responsesBackgroundPollInterval)
		select {
		case <-runCtx.Done():
			terminalStatus = "cancelled"
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
		currentRun := run
		pollCtx, pollCancel := context.WithTimeout(context.Background(), 30*time.Second)
		run, err = manager.Get(pollCtx, sess.ID, currentRun.LocalRunID)
		pollCancel()
		if err != nil {
			if !replayAttempted && backgroundAgent.ResponsesStateFallbackError(err) {
				next, replayErr := s.startResponsesBackgroundReplay(context.Background(), sess.ID, runID, currentRun.LocalTurnID, backgroundAgent)
				if replayErr == nil {
					replayAttempted = true
					previousRunID := currentRun.LocalRunID
					run = next
					_ = s.recordSessionRunEvent(sess, runID, "remote_state_replay", "running", "responses_background", model.ID, mode, map[string]any{
						"responseRunId": next.LocalRunID, "previousResponseRunId": previousRunID,
						"reason": "remote Responses state unavailable during background poll",
					})
					continue
				}
			}
			_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", model.ID, mode, map[string]any{"error": err.Error()})
			return
		}
		s.publishSessionRuntime(sess)
	}
}

func responsesHostedDeadline(active provider.Provider) time.Time {
	if active == nil {
		return time.Time{}
	}
	reporter, ok := active.(interface{ ResponsesHostedTimeout() time.Duration })
	if !ok {
		return time.Time{}
	}
	timeout := reporter.ResponsesHostedTimeout()
	if timeout <= 0 {
		return time.Time{}
	}
	return time.Now().Add(timeout)
}

// defaultBackgroundRunMaxDuration caps how long the coordinator polls a remote
// Responses run before declaring it incomplete. Without a cap a remote run
// that never reaches a terminal state would hold the session runtime lock
// forever, blocking channel /new and any other run for the session.
const defaultBackgroundRunMaxDuration = 6 * time.Hour

// backgroundRunMaxDuration returns the configured hard cap for durable
// background polling (api.backgroundRunMaxSeconds, default 6h).
func (s *Server) backgroundRunMaxDuration() time.Duration {
	if s == nil {
		return defaultBackgroundRunMaxDuration
	}
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg != nil && cfg.BackgroundRunMaxSecs > 0 {
		return time.Duration(cfg.BackgroundRunMaxSecs) * time.Second
	}
	return defaultBackgroundRunMaxDuration
}

// startResponsesBackgroundReplay creates one durable native-replay response
// after the remote state is explicitly known to be unavailable. It is shared
// by expiry and poll-error paths so availability behavior does not diverge by
// entry point. Callers enforce the one-replay limit for each local run.
func (s *Server) startResponsesBackgroundReplay(ctx context.Context, sessionID, runID, localTurnID string, backgroundAgent *agent.Agent) (*session.ResponseRun, error) {
	if s == nil || s.responsesRuns == nil || backgroundAgent == nil {
		return nil, fmt.Errorf("Responses background replay is unavailable")
	}
	params, err := backgroundAgent.BuildBackgroundReplayParams(localTurnID)
	if err != nil {
		return nil, err
	}
	replayID := runID + ":replay"
	if localTurnID != "" && localTurnID != runID {
		replayID = runID + ":" + localTurnID + ":replay"
	}
	if ctx == nil {
		ctx = context.Background()
	}
	replayCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.responsesRuns.Start(replayCtx, sessionID, replayID, params)
}

func (s *Server) executeResponsesBackgroundTools(ctx context.Context, sess *APISession, backgroundAgent *agent.Agent, runID, localTurnID string, calls []provider.ToolCallBlock) ([]provider.Message, bool) {
	return s.executeResponsesBackgroundToolsWithProgress(ctx, sess, backgroundAgent, runID, localTurnID, calls, false, nil)
}

func (s *Server) executeResponsesBackgroundToolsWithRecovery(ctx context.Context, sess *APISession, backgroundAgent *agent.Agent, runID, localTurnID string, calls []provider.ToolCallBlock, recoverReadOnly bool) ([]provider.Message, bool) {
	return s.executeResponsesBackgroundToolsWithProgress(ctx, sess, backgroundAgent, runID, localTurnID, calls, recoverReadOnly, nil)
}

func (s *Server) executeResponsesBackgroundToolsWithProgress(ctx context.Context, sess *APISession, backgroundAgent *agent.Agent, runID, localTurnID string, calls []provider.ToolCallBlock, recoverReadOnly bool, progress func(string)) ([]provider.Message, bool) {
	if backgroundAgent == nil {
		return nil, false
	}
	blocks := make([]provider.ContentBlock, 0, len(calls))
	for i := range calls {
		call := calls[i]
		blocks = append(blocks, provider.ContentBlock{Type: "toolCall", ToolCall: &call})
	}
	if _, err := sess.Manager.AppendMessage(provider.NewAssistantMessage(blocks)); err != nil {
		return nil, false
	}
	// Responses may return multiple function calls in one output. Execute them
	// concurrently so independent calls do not serialize remote latency, but
	// collect and persist outputs by original call order for deterministic
	// continuation input and transcript replay. The shared ToolLaunchOrder handle
	// keeps each call's reported start in that same declared order.
	type toolOutcome struct {
		output      *provider.Message
		interrupted bool
	}
	var progressMu sync.Mutex
	sendProgress := func(text string) {
		if progress == nil || strings.TrimSpace(text) == "" {
			return
		}
		progressMu.Lock()
		defer progressMu.Unlock()
		progress(text)
	}
	launchOrder := agent.NewToolLaunchOrder(len(calls))
	indexes := make([]int, len(calls))
	for i := range indexes {
		indexes[i] = i
	}
	outcomes := agent.BoundedParallel(backgroundAgent.MaxToolConcurrency(), indexes, func(index int) toolOutcome {
		call := calls[index]
		stream := backgroundAgent.ExecuteBackgroundToolCallOrdered(ctx, call, localTurnID, recoverReadOnly, launchOrder.Handle(index))
		var output *provider.Message
		interrupted := false
		for ev := range stream {
			s.publishResponsesBackgroundToolEvent(sess, backgroundAgent, runID, ev)
			if ev.Type == agent.EventToolExecutionStart {
				sendProgress(fmt.Sprintf("Tool %s running", ev.ToolName))
			}
			if ev.Type == agent.EventToolExecutionEnd {
				status := "completed"
				if ev.ToolExecutionState == "interrupted" {
					status = "interrupted"
				} else if ev.ToolError != nil {
					status = "failed"
				}
				summary := summarizeToolStatusResult(ev.ToolResult)
				if summary == "(empty result)" {
					summary = ""
				}
				line := fmt.Sprintf("Tool %s %s", ev.ToolName, status)
				if summary != "" {
					line += ": " + summary
				}
				sendProgress(line)
			}
			if ev.ToolExecutionState == "interrupted" {
				interrupted = true
			}
			if ev.Type == agent.EventToolResult {
				result := provider.NewToolResultMessage(ev.ToolCallID, ev.ToolName, ev.ToolResult, ev.ToolError != nil)
				result.ToolKind = call.Kind
				output = &result
			} else if ev.Type == agent.EventToolExecutionEnd && output == nil {
				result := provider.NewToolResultMessage(ev.ToolCallID, ev.ToolName, ev.ToolResult, ev.ToolError != nil)
				result.ToolKind = call.Kind
				output = &result
			}
		}
		return toolOutcome{output: output, interrupted: interrupted}
	})
	ordered := make([]*provider.Message, len(calls))
	allSucceeded := true
	for index, outcome := range outcomes {
		if outcome.output == nil {
			allSucceeded = false
			continue
		}
		if outcome.interrupted {
			allSucceeded = false
		}
		ordered[index] = outcome.output
	}
	if !allSucceeded {
		return nil, false
	}
	outputs := make([]provider.Message, 0, len(ordered))
	for _, output := range ordered {
		if output == nil {
			return nil, false
		}
		if _, err := sess.Manager.AppendMessage(*output); err != nil {
			return nil, false
		}
		outputs = append(outputs, *output)
	}
	return outputs, true
}

func (s *Server) publishResponsesBackgroundToolEvent(sess *APISession, backgroundAgent *agent.Agent, runID string, ev agent.Event) {
	if s == nil || sess == nil {
		return
	}
	switch ev.Type {
	case agent.EventToolApprovalRequest:
		s.registerSessionApproval(sess, backgroundAgent, ev)
	case agent.EventToolExecutionStart:
		s.publishToolEvent(sess.ID, ToolStatusEvent{Tool: ev.ToolName, ToolCallID: ev.ToolCallID, Status: "running", Args: ev.ToolArgs})
		s.persistResponsesBackgroundToolProgress(sess, runID, ev.ToolName, ev.ToolCallID, "running", "")
	case agent.EventToolExecutionEnd:
		status := "completed"
		if ev.ToolExecutionState == "interrupted" {
			status = "interrupted"
		} else if ev.ToolError != nil {
			status = "failed"
		}
		summary := summarizeToolStatusResult(ev.ToolResult)
		if status == "interrupted" && (summary == "" || summary == "(empty result)") {
			summary = "Execution interrupted; recovery requires explicit confirmation."
		}
		s.publishToolEvent(sess.ID, ToolStatusEvent{Tool: ev.ToolName, ToolCallID: ev.ToolCallID, Status: status, Args: ev.ToolArgs, Summary: summary, IsError: ev.ToolError != nil, HasDetail: ev.ToolCallID != ""})
		s.persistResponsesBackgroundToolProgress(sess, runID, ev.ToolName, ev.ToolCallID, status, summary)
	}
	if s.runManager != nil {
		s.runManager.Publish(runID, ev)
	}
}

func (s *Server) persistResponsesBackgroundToolProgress(sess *APISession, runID, toolName, toolCallID, status, summary string) {
	if s == nil || s.settings == nil || sess == nil || runID == "" {
		return
	}
	source := "responses_background"
	if run, err := agentruntime.GetDurableRun(context.Background(), s.settings.GetSessionDir(), runID); err == nil && run != nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(run.Source)), "channel:") {
		source = run.Source
	}
	_ = s.recordSessionRunEvent(sess, runID, "tool_progress", status, source, "", "", map[string]any{
		"tool": toolName, "toolCallId": toolCallID, "status": status, "summary": summary,
	})
}

func (s *Server) attachResponsesBackgroundCancel(runID, sessionID, localRunID string, localCancel context.CancelFunc) {
	if s == nil || s.runManager == nil || runID == "" || sessionID == "" || localRunID == "" {
		return
	}
	s.mu.RLock()
	manager := s.responsesRuns
	s.mu.RUnlock()
	if manager == nil {
		return
	}
	_ = s.runManager.Attach(runID, sessionID, func() {
		if localCancel != nil {
			localCancel()
		}
		cancelCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = manager.Cancel(cancelCtx, sessionID, localRunID)
	})
}

// recoverResponsesBackgroundRuns reattaches pending remote Responses tasks
// after server startup. It deliberately requires the local SessionRun and
// ResponseRun linkage to agree before it polls a remote response.
func (s *Server) recoverResponsesBackgroundRuns() error {
	if s == nil || s.runManager == nil || s.responsesRuns == nil || s.settings == nil {
		return nil
	}
	orphans, err := session.ListOrphanedSessionRuns(s.settings.GetSessionDir())
	if err != nil {
		return err
	}
	for _, localRun := range orphans {
		if localRun.Source != "responses_background" {
			continue
		}
		responseRuns, err := session.ListResponseRuns(s.settings.GetSessionDir(), localRun.SessionID, 100)
		if err != nil {
			return err
		}
		var responseRun *session.ResponseRun
		for i := range responseRuns {
			if responseRuns[i].LocalTurnID == localRun.ID || strings.HasPrefix(responseRuns[i].LocalTurnID, localRun.ID+":") {
				candidate := responseRuns[i]
				if responseRun == nil || candidate.UpdatedAt.After(responseRun.UpdatedAt) {
					responseRun = &candidate
				}
			}
		}
		if responseRun == nil {
			_, _ = agentruntime.RecoverOrphanedSessionRun(s.settings.GetSessionDir(), localRun.SessionID, func(session.SessionRun) agentruntime.RecoveryAction {
				return agentruntime.RecoveryFailLocal
			}, nil)
			continue
		}
		if _, err := s.reattachResponsesBackgroundRun(localRun, responseRun); err != nil && !errors.Is(err, ErrResponsesRuntimeBusy) {
			_, _ = agentruntime.RecoverOrphanedSessionRun(s.settings.GetSessionDir(), localRun.SessionID, func(session.SessionRun) agentruntime.RecoveryAction {
				return agentruntime.RecoveryFailLocal
			}, nil)
		}
	}
	return nil
}

// reattachResponsesBackgroundRun resumes exactly one durable background run.
// It is shared by startup recovery and the authenticated reconnect endpoint so
// both paths acquire the same runtime/session locks and construct the same
// tool/approval-aware monitor.
func (s *Server) reattachResponsesBackgroundRun(localRun session.SessionRun, responseRun *session.ResponseRun) (bool, error) {
	if s == nil || s.runManager == nil || s.responsesRuns == nil || s.settings == nil {
		return false, fmt.Errorf("Responses background runtime is unavailable")
	}
	if localRun.ID == "" || localRun.SessionID == "" || responseRun == nil || responseRun.SessionID != localRun.SessionID {
		return false, fmt.Errorf("invalid Responses background run linkage")
	}
	if isTerminalSessionRunState(localRun.Status) {
		return false, nil
	}
	sess, err := s.getOrCreateSession(localRun.SessionID, localRun.WorkDir)
	if err != nil || sess == nil {
		return false, fmt.Errorf("unable to restore session for Responses background run")
	}
	recoveryGuard, err := session.AcquireRecovery(s.settings.GetSessionDir(), sess.ID, localRun.ID)
	if err != nil {
		return false, ErrResponsesRuntimeBusy
	}
	runtimeRelease := recoveryGuard.Release
	if !sess.TryLock() {
		runtimeRelease()
		return false, ErrResponsesRuntimeBusy
	}
	if err := sess.Manager.Reload(); err != nil {
		sess.Unlock()
		runtimeRelease()
		return false, fmt.Errorf("reload session before Responses recovery: %w", err)
	}
	model := s.provider.GetModel(localRun.Model)
	if model == nil {
		model = s.model
	}
	if model == nil {
		sess.Unlock()
		runtimeRelease()
		return false, fmt.Errorf("model for Responses background run is unavailable")
	}
	fallbackSource := localRun.Source
	if fallbackSource == "" {
		fallbackSource = "responses_background"
	}
	runSource, mode, err := s.canonicalRunIdentity(sess, fallbackSource, localRun.Mode)
	if err != nil {
		sess.Unlock()
		runtimeRelease()
		return false, fmt.Errorf("resolve mode for Responses recovery: %w", err)
	}
	localRun.Source = runSource
	localRun.Mode = mode
	execution := sess.ensureExecution()
	execution.SetRunStore(agentruntime.RunStore{SessionDir: s.settings.GetSessionDir()})
	execution.SetEventSink(s.runtimeRunEventSink(sess))
	if sess.Runtime != nil {
		sess.Runtime.SetExecution(execution)
	}
	if _, err := execution.ReattachDurableRun(context.Background(), agentruntime.DurableRun{
		ID: localRun.ID, SessionID: localRun.SessionID, WorkDir: localRun.WorkDir,
		Source: localRun.Source, Model: localRun.Model, Mode: localRun.Mode,
		Status: localRun.Status, StartedAt: localRun.StartedAt, ConversationTurnID: responseRun.LocalTurnID, ConversationTurn: responseRun.LocalTurnID != "",
	}, webUIActiveRunState(localRun.Status), agentruntime.RunEvent{
		SessionID: localRun.SessionID, RunID: localRun.ID, EventType: "reattached",
		Source: localRun.Source, Status: localRun.Status, Model: localRun.Model,
		Mode: localRun.Mode, Timestamp: time.Now(),
	}); err != nil {
		sess.Unlock()
		runtimeRelease()
		return false, fmt.Errorf("reattach durable Responses run: %w", err)
	}
	sess.beginRunBookkeeping(localRun.ID)
	sess.markDurableRun(localRun.ID)
	if s.runManager != nil {
		_ = s.runManager.Register(localRun)
	}
	go s.monitorRecoveredResponsesBackgroundRun(sess, localRun, responseRun, model, runtimeRelease)
	return true, nil
}

func isTerminalSessionRunState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "completed", "incomplete", "expired", "failed", "cancelled", "canceled", "cancelling", "terminalizing":
		return true
	default:
		return false
	}
}

func (s *Server) monitorRecoveredResponsesBackgroundRun(sess *APISession, localRun session.SessionRun, responseRun *session.ResponseRun, model *provider.Model, runtimeRelease func()) {
	defer runtimeRelease()
	defer sess.Unlock()
	terminalStatus := "failed"
	defer func() {
		if execution := sess.executionRuntime(); execution != nil && sess.isDurableRun(localRun.ID) {
			if err := execution.FinishDurableWithRetry(context.Background(), localRun.ID, webUIRunState(terminalStatus, ""), "", agentruntime.RunEvent{
				SessionID: sess.ID, RunID: localRun.ID, EventType: runEventTypeForStatus(terminalStatus),
				Source: localRun.Source, Status: terminalStatus, Model: model.ID, Mode: localRun.Mode, Timestamp: time.Now(),
			}); err != nil {
				if _, active := execution.Active(); active {
					_ = s.recordSessionRunEvent(sess, localRun.ID, "failed", "failed", "responses_background", model.ID, localRun.Mode, map[string]any{"error": err.Error()})
				}
			}
		}
		s.FinalizeRun(sess, localRun.ID, terminalStatus, "")
	}()
	if responseRun == nil || model == nil {
		return
	}
	backgroundOpts := s.buildAgentOptionsForSession(sess, model, localRun.Mode)
	backgroundOpts.IntentID = localRun.IntentID
	backgroundOpts.RunID = localRun.ID
	backgroundOpts.ConversationTurnID = responseRun.LocalTurnID
	backgroundOpts.ConversationTurn = responseRun.LocalTurnID != ""
	backgroundOpts.RuntimeOwnsTurnEnd = backgroundOpts.ConversationTurn
	backgroundOpts.ApprovalDecisionLookup = func(toolCallID, toolName string, args map[string]any) (bool, bool) {
		return s.recoveredApprovalDecision(sess.ID, localRun.ID, toolCallID, toolName, args)
	}
	backgroundOpts.ProviderName = s.providerName
	backgroundOpts.Provider = s.provider
	backgroundOpts.Model = model
	backgroundAgent, err := sess.Runtime.BuildAgent(backgroundOpts)
	if err != nil {
		_ = s.recordSessionRunEvent(sess, localRun.ID, "failed", "failed", "responses_background", model.ID, localRun.Mode, map[string]any{"error": err.Error()})
		return
	}
	replayState := sess.Manager.GetReplayState()
	if len(replayState.Messages) > 0 {
		backgroundAgent.LoadHistoryState(replayState.Messages, replayState.EntryIDs)
	}
	params, err := backgroundAgent.BuildBackgroundContinuationParams(responseRun.LocalTurnID)
	if err != nil {
		_ = s.recordSessionRunEvent(sess, localRun.ID, "failed", "failed", "responses_background", model.ID, localRun.Mode, map[string]any{"error": err.Error()})
		return
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	if !sess.attachRunAgent(localRun.ID, backgroundAgent, func() {
		cancelRun()
		backgroundAgent.Abort()
	}) {
		return
	}
	s.attachResponsesBackgroundCancel(localRun.ID, sess.ID, responseRun.LocalRunID, func() {
		cancelRun()
		backgroundAgent.Abort()
	})
	replayAttempted := false
	hostedDeadline := responsesHostedDeadline(s.provider)
	maxDeadline := time.Now().Add(s.backgroundRunMaxDuration())
	for {
		if !time.Now().Before(maxDeadline) {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = s.responsesRuns.Cancel(cancelCtx, sess.ID, responseRun.LocalRunID)
			cancel()
			terminalStatus = "incomplete"
			_ = s.recordSessionRunEvent(sess, localRun.ID, "finished", terminalStatus, "responses_background", model.ID, localRun.Mode, map[string]any{
				"responseRunId": responseRun.LocalRunID, "responseId": responseRun.ResponseID,
				"incompleteReason": "mothx_background_run_max_duration",
			})
			return
		}
		if !hostedDeadline.IsZero() && !time.Now().Before(hostedDeadline) {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = s.responsesRuns.Cancel(cancelCtx, sess.ID, responseRun.LocalRunID)
			cancel()
			terminalStatus = "incomplete"
			_ = s.recordSessionRunEvent(sess, localRun.ID, "finished", terminalStatus, "responses_background", model.ID, localRun.Mode, map[string]any{
				"responseRunId": responseRun.LocalRunID, "responseId": responseRun.ResponseID,
				"incompleteReason": "mothx_code_interpreter_timeout",
			})
			return
		}
		pollCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		refreshed, err := s.responsesRuns.Get(pollCtx, sess.ID, responseRun.LocalRunID)
		cancel()
		if err != nil {
			if !replayAttempted && backgroundAgent.ResponsesStateFallbackError(err) {
				next, replayErr := s.startResponsesBackgroundReplay(context.Background(), sess.ID, localRun.ID, responseRun.LocalTurnID, backgroundAgent)
				if replayErr == nil {
					replayAttempted = true
					previousRunID := responseRun.LocalRunID
					responseRun = next
					_ = s.recordSessionRunEvent(sess, localRun.ID, "remote_state_replay", "running", "responses_background", model.ID, localRun.Mode, map[string]any{
						"responseRunId": next.LocalRunID, "previousResponseRunId": previousRunID,
						"reason": "remote Responses state unavailable during recovery poll",
					})
					continue
				}
			}
			_ = s.recordSessionRunEvent(sess, localRun.ID, "failed", "failed", "responses_background", model.ID, localRun.Mode, map[string]any{"error": err.Error()})
			return
		}
		if isTerminalResponsesRunState(refreshed.State) {
			if strings.EqualFold(strings.TrimSpace(refreshed.State), "expired") && !replayAttempted {
				previousRunID := refreshed.LocalRunID
				next, replayErr := s.startResponsesBackgroundReplay(context.Background(), sess.ID, localRun.ID, refreshed.LocalTurnID, backgroundAgent)
				if replayErr == nil {
					replayAttempted = true
					responseRun = next
					_ = s.recordSessionRunEvent(sess, localRun.ID, "remote_state_replay", "running", "responses_background", model.ID, localRun.Mode, map[string]any{
						"responseRunId": next.LocalRunID, "previousResponseRunId": previousRunID,
						"reason": "remote Responses state expired during recovery",
					})
					continue
				}
			}
			calls, callErr := responsesBackgroundFunctionCallsForRun(s.settings.GetSessionDir(), sess.ID, refreshed.LocalTurnID)
			if callErr != nil {
				_ = s.recordSessionRunEvent(sess, localRun.ID, "failed", "failed", "responses_background", model.ID, localRun.Mode, map[string]any{"error": callErr.Error()})
				return
			}
			if len(calls) > 0 {
				outputs, ok := s.executeResponsesBackgroundToolsWithProgress(runCtx, sess, backgroundAgent, localRun.ID, refreshed.LocalTurnID, calls, true, nil)
				if !ok {
					return
				}
				params.ResponseOptions = &provider.ResponseOptions{PreviousResponseID: refreshed.ResponseID}
				continuationID := localRun.ID + ":" + refreshed.LocalRunID
				continueCtx, continueCancel := context.WithTimeout(context.Background(), 30*time.Second)
				next, continueErr := s.responsesRuns.Continue(continueCtx, sess.ID, continuationID, refreshed, outputs, params)
				continueCancel()
				if continueErr != nil {
					if backgroundAgent.ResponsesStateFallbackError(continueErr) {
						replayParams, replayErr := backgroundAgent.BuildBackgroundReplayParams(continuationID)
						if replayErr == nil {
							replayCtx, replayCancel := context.WithTimeout(context.Background(), 30*time.Second)
							next, replayErr := s.responsesRuns.Start(replayCtx, sess.ID, continuationID+":replay", replayParams)
							replayCancel()
							if replayErr == nil {
								responseRun = next
								_ = s.recordSessionRunEvent(sess, localRun.ID, "remote_state_replay", "running", "responses_background", model.ID, localRun.Mode, map[string]any{
									"responseRunId": next.LocalRunID, "previousResponseRunId": refreshed.LocalRunID,
									"reason": "remote Responses state unavailable",
								})
								continue
							}
						}
					}
					_ = s.recordSessionRunEvent(sess, localRun.ID, "failed", "failed", "responses_background", model.ID, localRun.Mode, map[string]any{"error": continueErr.Error(), "responseRunId": refreshed.LocalRunID})
					return
				}
				responseRun = next
				_ = s.recordSessionRunEvent(sess, localRun.ID, "remote_continuation", "running", "responses_background", model.ID, localRun.Mode, map[string]any{"responseRunId": next.LocalRunID, "responseId": next.ResponseID})
				continue
			}
			terminalStatus = s.finalizeResponsesBackgroundResult(sess, localRun.ID, model.ID, localRun.Mode, refreshed, false)
			return
		}
		timer := time.NewTimer(responsesBackgroundPollInterval)
		select {
		case <-runCtx.Done():
			timer.Stop()
			_ = s.recordSessionRunEvent(sess, localRun.ID, "canceled", "cancelled", "responses_background", model.ID, localRun.Mode, map[string]any{"reason": "local run context cancelled"})
			return
		case <-timer.C:
		}
	}
}

func (s *Server) finalizeResponsesBackgroundResult(sess *APISession, runID, modelID, mode string, run *session.ResponseRun, transcript bool) string {
	if run == nil {
		return "failed"
	}
	state := strings.ToLower(strings.TrimSpace(run.State))
	if state != "completed" && state != "incomplete" {
		status := "failed"
		if state == "cancelled" || state == "canceled" {
			status = "cancelled"
		}
		_ = s.recordSessionRunEvent(sess, runID, runEventTypeForStatus(status), status, "responses_background", modelID, mode, map[string]any{
			"responseRunId": run.LocalRunID, "responseId": run.ResponseID, "state": run.State,
		})
		return status
	}

	items, err := session.ListResponseItems(s.settings.GetSessionDir(), sess.ID, run.LocalTurnID)
	if err != nil {
		_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", modelID, mode, map[string]any{"error": err.Error()})
		return "failed"
	}
	usage, attachments, err := responsesBackgroundDetails(s.settings.GetSessionDir(), sess.ID, run.LocalTurnID)
	if err != nil {
		_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", modelID, mode, map[string]any{"error": err.Error()})
		return "failed"
	}
	text, requiresLocalContinuation, err := responsesBackgroundText(items)
	if err != nil {
		_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", modelID, mode, map[string]any{"error": err.Error()})
		return "failed"
	}
	if requiresLocalContinuation {
		message := "background Responses response reached finalization with an unhandled local tool call"
		_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", modelID, mode, map[string]any{
			"responseRunId": run.LocalRunID, "responseId": run.ResponseID, "error": message,
		})
		return "failed"
	}
	localRun, _ := agentruntime.GetDurableRun(context.Background(), s.settings.GetSessionDir(), runID)
	channelRun := localRun != nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(localRun.Source)), "channel:")
	assistantEntryID := ""
	if text != "" || len(attachments) > 0 {
		contents := []provider.ContentBlock(nil)
		if text != "" {
			contents = []provider.ContentBlock{{Type: "text", Text: text}}
		}
		message := provider.NewAssistantMessage(contents)
		message.Attachments = append([]provider.Attachment(nil), attachments...)
		entryID, err := sess.Manager.AppendMessage(message)
		if err != nil {
			_ = s.recordSessionRunEvent(sess, runID, "failed", "failed", "responses_background", modelID, mode, map[string]any{"error": err.Error()})
			return "failed"
		}
		assistantEntryID = entryID
		if text != "" {
			event := agent.Event{Type: agent.EventTextDelta, TextDelta: text}
			if s.runManager != nil {
				s.runManager.Publish(runID, event)
			}
			if transcript {
				s.publishTranscriptEvent(sess.ID, assistantDeltaTranscriptEvent(text, ""))
			} else if broker := s.getEventBroker(); broker != nil {
				broker.PublishTranscriptEvent(sess.ID, runID, assistantDeltaTranscriptEvent(text, ""))
			}
		}
	}
	if len(attachments) > 0 {
		event := assistantAttachmentsTranscriptEvent(attachments, "")
		if transcript {
			s.publishTranscriptEvent(sess.ID, event)
		} else if broker := s.getEventBroker(); broker != nil {
			broker.PublishTranscriptEvent(sess.ID, runID, event)
		}
	}
	eventData := map[string]any{
		"responseRunId": run.LocalRunID, "responseId": run.ResponseID, "state": run.State,
	}
	if channelRun {
		eventData = agentruntime.DeliveryPendingData(run.LocalRunID, run.ResponseID, run.State, assistantEntryID, nil)
	}
	if usage != nil {
		eventData["usage"] = usage
	}
	if state == "incomplete" {
		if turn, turnErr := session.GetResponseTurn(s.settings.GetSessionDir(), sess.ID, run.LocalTurnID); turnErr == nil && turn != nil && turn.IncompleteReason != "" {
			eventData["incompleteReason"] = turn.IncompleteReason
		}
	}
	if len(attachments) > 0 {
		eventData["attachments"] = attachments
	}
	eventSource := "responses_background"
	if channelRun {
		eventSource = localRun.Source
	}
	localStatus := "completed"
	if state == "incomplete" {
		localStatus = "incomplete"
		eventData["incomplete"] = true
	}
	if channelRun {
		deliveryEvent := agentruntime.NewDeliveryPendingEvent(sess.ID, runID, eventSource, localStatus, modelID, mode, eventData)
		var deliveryData map[string]any
		if json.Unmarshal(deliveryEvent.Data, &deliveryData) == nil {
			eventData = deliveryData
		}
	}
	_ = s.recordSessionRunEvent(sess, runID, "finished", localStatus, eventSource, modelID, mode, eventData)
	return localStatus
}

func responsesBackgroundDetails(sessionDir, sessionID, localTurnID string) (*provider.Usage, []provider.Attachment, error) {
	turn, err := session.GetResponseTurn(sessionDir, sessionID, localTurnID)
	if err != nil || turn == nil || len(turn.ResponseSummary) == 0 {
		return nil, nil, err
	}
	var summary struct {
		Usage       *provider.Usage       `json:"usage"`
		Attachments []provider.Attachment `json:"attachments"`
	}
	if err := json.Unmarshal(turn.ResponseSummary, &summary); err != nil {
		return nil, nil, fmt.Errorf("decode Responses background summary: %w", err)
	}
	return summary.Usage, summary.Attachments, nil
}

func responsesBackgroundText(items []session.ResponseItemArchive) (string, bool, error) {
	var text strings.Builder
	for _, item := range items {
		var raw struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(item.SanitizedJSON, &raw); err != nil {
			return "", false, fmt.Errorf("decode archived Responses item %q: %w", item.ItemID, err)
		}
		switch raw.Type {
		case "function_call", "custom_tool_call":
			return "", true, nil
		case "computer_call", "computer_call_output":
			return "", false, fmt.Errorf("Responses computer use is not supported by this version")
		case "message":
			for _, part := range raw.Content {
				if part.Type == "output_text" || part.Type == "text" {
					text.WriteString(part.Text)
				}
			}
		}
	}
	return text.String(), false, nil
}

func responsesBackgroundFunctionCallsForRun(sessionDir, sessionID, localTurnID string) ([]provider.ToolCallBlock, error) {
	items, err := session.ListResponseItems(sessionDir, sessionID, localTurnID)
	if err != nil {
		return nil, err
	}
	var calls []provider.ToolCallBlock
	for _, item := range items {
		var raw struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Input     string `json:"input"`
		}
		if err := json.Unmarshal(item.SanitizedJSON, &raw); err != nil {
			return nil, fmt.Errorf("decode archived Responses item %q: %w", item.ItemID, err)
		}
		switch raw.Type {
		case "function_call":
			callID := raw.CallID
			if callID == "" {
				callID = raw.ID
			}
			if callID == "" || raw.Name == "" {
				return nil, fmt.Errorf("archived function call %q is missing call_id or name", item.ItemID)
			}
			calls = append(calls, provider.ToolCallBlock{ID: callID, Name: raw.Name, Arguments: json.RawMessage(raw.Arguments)})
		case "custom_tool_call":
			callID := raw.CallID
			if callID == "" {
				callID = raw.ID
			}
			if callID == "" || raw.Name == "" {
				return nil, fmt.Errorf("archived custom tool call %q is missing call_id or name", item.ItemID)
			}
			arguments, err := json.Marshal(map[string]string{"input": raw.Input})
			if err != nil {
				return nil, fmt.Errorf("encode archived custom tool call %q input: %w", item.ItemID, err)
			}
			calls = append(calls, provider.ToolCallBlock{ID: callID, Name: raw.Name, Kind: "custom", Input: raw.Input, Arguments: arguments})
		}
	}
	return calls, nil
}

// replayStateContainsRunUserEntry reports whether the shared transcript
// already carries the user entry that durable admission atomically appends
// for this run. The deterministic entry identity keeps the check idempotent
// across retries, recovery, and process restarts.
func replayStateContainsRunUserEntry(state session.ReplayState, runID string) bool {
	entryID := session.RunUserEntryID(runID)
	if entryID == "" {
		return false
	}
	for _, id := range state.EntryIDs {
		if id == entryID {
			return true
		}
	}
	return false
}

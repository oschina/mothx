package openaiapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/a2a"
	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/mcp"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/skills"
	"github.com/startvibecoding/mothx/internal/tools"
	"github.com/startvibecoding/mothx/internal/util"
	"github.com/startvibecoding/mothx/internal/workflow"
)

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10MB limit
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body", "invalid_request_error")
		return
	}
	defer r.Body.Close()

	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawFields); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "invalid_request_error")
		return
	}
	for field := range rawFields {
		if strings.HasPrefix(strings.ToLower(field), "x_") && strings.ToLower(field) != "x_background" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported extension field %q", field), "invalid_request_error")
			return
		}
	}
	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "invalid_request_error")
		return
	}

	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages array is required and must not be empty", "invalid_request_error")
		return
	}
	// Session state is kept internal and uses the configured default working
	// directory. x_background is the sole supported extension and is handled
	// above before the ordinary synchronous chat path.
	workDir := s.cfg.GetWorkDir()

	// Resolve model
	s.mu.RLock()
	currentModel := s.model
	currentProvider := s.provider
	s.mu.RUnlock()

	if req.Model != "" {
		if m := currentProvider.GetModel(req.Model); m != nil {
			currentModel = m
		} else {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("model %q not found — available: %s", req.Model, modelIDs(currentProvider.Models())), "invalid_request_error")
			return
		}
	}
	currentModel = cloneModel(currentModel)

	// Extract last user message
	lastUserMsg, systemMsgs, historyMsgs := parseMessages(req.Messages)
	if strings.TrimSpace(lastUserMsg.Content) == "" && len(lastUserMsg.ContentParts) == 0 {
		writeError(w, http.StatusBadRequest, "no user message found", "invalid_request_error")
		return
	}
	lastUserInput, lastUserIngresses, err := requestRunInput(lastUserMsg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	if req.Background {
		if req.Stream {
			writeError(w, http.StatusBadRequest, "x_background is only supported for non-streaming chat completions", "invalid_request_error")
			return
		}
		if !s.responsesBackgroundEnabled() {
			writeError(w, http.StatusNotImplemented, "x_background requires an available Responses background runtime", "capability_error")
			return
		}
		s.submitChatCompletionBackground(w, r, req, workDir, currentModel, lastUserInput, lastUserIngresses, systemMsgs, historyMsgs)
		return
	}
	// Get or create the server-owned default session.
	var sessionID string
	s.mu.RLock()
	sessionID = s.defaultSessionIDs[workDir]
	s.mu.RUnlock()
	var sess *APISession
	for {
		sess, err = s.getOrCreateSession(sessionID, workDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
			return
		}
		if sess == nil {
			writeError(w, http.StatusServiceUnavailable, "session pool is at capacity", "server_error")
			return
		}
		if s.pool.Pin(sess) {
			break
		}
	}
	defer s.pool.Unpin(sess)

	runtimeGuard, admissionErr := agentruntime.AcquireExecutionAdmission(r.Context(), s.settings.GetSessionDir(), sess.ID, agentruntime.ExecutionAdmissionOptions{})
	if admissionErr != nil {
		status, info := s.executionAdmissionError(sess.ID, admissionErr)
		writeErrorInfo(w, status, info)
		return
	}
	runtimeRelease := runtimeGuard.Release
	defer runtimeRelease()

	// The durable admission lease already serializes executions across
	// processes. A short-lived in-process session mutex is a reservation, not an
	// active Run; keep this non-blocking and expose the narrower state.
	if !sess.TryLock() {
		writeErrorInfo(w, http.StatusConflict, agentruntime.ErrorInfo{
			Code: "session_reserved", Type: "conflict_error", FailureClass: agentruntime.FailurePolicy,
			Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.sessionReserved",
			Message: "The session is reserved for another operation.", RetryMode: agentruntime.RetryUser,
			Retryable: true,
		})
		return
	}
	defer sess.Unlock()
	if err := sess.Manager.Reload(); err != nil {
		writeError(w, http.StatusInternalServerError, "reload session before run: "+err.Error(), "server_error")
		return
	}
	hadPersistedHistory := len(sess.Manager.GetReplayState().Messages) > 0
	if s.runSlots != nil {
		select {
		case s.runSlots <- struct{}{}:
			defer func() { <-s.runSlots }()
		default:
			writeError(w, http.StatusTooManyRequests, "maximum concurrent requests reached", "concurrency_limit_reached")
			return
		}
	}
	sess.Touch()
	runID := newRunID()
	runInput, err := sess.Runtime.AcceptInput(r.Context(), runID, lastUserInput.Text, lastUserIngresses)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	artifacts, err := sess.Runtime.BeginArtifactCollection(runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	defer artifacts.Close()
	lastUserMessage, err := sess.Runtime.BuildUserMessage(r.Context(), runInput)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	runStartedAt := time.Now()
	terminalStatus := "failed"
	terminalErrMsg := ""
	var mode string
	runSource := string(agentruntime.SourceWebUI)
	durableFinished := false
	defer func() {
		if execution := sess.executionRuntime(); !durableFinished && sess.isDurableRun(runID) && execution != nil {
			_ = execution.FinishDurableWithRetry(context.Background(), runID, webUIRunState(terminalStatus, terminalErrMsg), terminalErrMsg, agentruntime.RunEvent{SessionID: sess.ID, RunID: runID, EventType: runEventTypeForStatus(terminalStatus), Source: runSource, Status: terminalStatus, Model: currentModel.ID, Mode: mode, Timestamp: time.Now()})
		}
		s.FinalizeRun(sess, runID, terminalStatus, terminalErrMsg)
	}()
	resolution, resolvedMode, err := s.resolveSessionPolicy(sess, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	mode = resolvedMode
	if resolution.Source != agentruntime.SourceUnknown {
		runSource = string(resolution.Source)
	}
	// Canonical local Chat Run lifecycle is owned by ExecutionRuntime. The
	// RunManager only registers the in-memory event fan-out entry.
	runStatus := "running"
	chatRequestSnapshot, snapshotErr := json.Marshal(map[string]any{
		"model": req.Model, "stream": req.Stream, "input": runInput,
		"systemMessageCount": len(systemMsgs), "historyMessageCount": len(historyMsgs),
		"maxTokens": req.MaxTokens, "temperature": req.Temperature, "topP": req.TopP,
	})
	if snapshotErr != nil {
		writeSubmitError(w, http.StatusInternalServerError, snapshotErr, "run_request_snapshot_failed", "server_error", agentruntime.FailurePersistence, agentruntime.PhaseAdmission, "run.error.requestSnapshotFailed", "The run request could not be prepared.", agentruntime.RetryReconcile, true)
		return
	}
	chatPolicySnapshot, snapshotErr := marshalRunPolicySnapshot(s, sess, submitRunRequest{Message: lastUserMsg.Content, Model: req.Model, Transcript: req.Stream, WorkDir: workDir}, runSource, mode)
	if snapshotErr != nil {
		writeSubmitError(w, http.StatusInternalServerError, snapshotErr, "run_policy_snapshot_failed", "server_error", agentruntime.FailurePersistence, agentruntime.PhaseAdmission, "run.error.policySnapshotFailed", "The run policy could not be prepared.", agentruntime.RetryReconcile, true)
		return
	}
	chatIntent := agentruntime.ExecutionIntent{
		ID: newExecutionIntentID(), SessionID: sess.ID, Source: runSource, Model: currentModel.ID, Mode: mode,
		WorkDir: sess.WorkDir, RequestFingerprint: requestFingerprint(req), Request: chatRequestSnapshot,
		Policy: chatPolicySnapshot, CreatedAt: runStartedAt,
	}
	execution := sess.ensureExecution()
	execution.SetRunStore(agentruntime.RunStore{SessionDir: s.settings.GetSessionDir()})
	execution.SetEventSink(s.runtimeRunEventSink(sess))
	if _, err := execution.BeginIntentDurable(context.Background(), chatIntent, agentruntime.DurableRun{ID: runID, SessionID: sess.ID, IntentID: chatIntent.ID, WorkDir: sess.WorkDir, Source: runSource, Model: currentModel.ID, Mode: mode, Status: runStatus, StartedAt: runStartedAt, InputResourceIDs: runInput.ResourceIDs(), UserEntryID: session.RunUserEntryID(runID), UserMessage: &lastUserMessage, ConversationTurnID: "turn-" + runID, ConversationTurn: true}, agentruntime.RunEvent{SessionID: sess.ID, RunID: runID, EventType: "started", Source: runSource, Status: runStatus, Model: currentModel.ID, Mode: mode, Timestamp: runStartedAt, Data: rawEventData(map[string]any{"stream": req.Stream, "workDir": sess.WorkDir, "provider": s.providerName, "messageCount": len(req.Messages), "intentId": chatIntent.ID, "attempt": 1})}); err != nil {
		writeSubmitError(w, http.StatusInternalServerError, err, "run_persistence_failed", "server_error", agentruntime.FailurePersistence, agentruntime.PhasePersistence, "run.error.persistence", "The run could not be started.", agentruntime.RetryReconcile, true)
		return
	}
	if err := sess.Manager.Reload(); err != nil {
		writeSubmitError(w, http.StatusInternalServerError, err, "session_reload_failed", "server_error", agentruntime.FailurePersistence, agentruntime.PhasePersistence, "run.error.sessionReloadFailed", "The session could not be reloaded.", agentruntime.RetryReconcile, true)
		return
	}
	sess.markDurableRun(runID)
	sess.beginRunBookkeeping(runID)
	if s.runManager != nil {
		_ = s.runManager.Register(session.SessionRun{ID: runID, SessionID: sess.ID, IntentID: chatIntent.ID, Attempt: 1})
	}

	// Build extra context: system prompt handling
	extraContext := sess.ExtraContext
	if extraContext == "" {
		extraContext = s.extraContext
	}
	if s.cfg.SystemPromptMode == "append" && len(systemMsgs) > 0 {
		extraContext += "\n## Client Instructions\n" + strings.Join(systemMsgs, "\n") + "\n"
	}

	runtimeSettings := s.settingsForSession(sess)

	// Build request-specific agent inputs.
	thinkingLevel := provider.ThinkingLevel(s.cfg.DefaultThinkingLevel)
	if thinkingLevel == "" {
		thinkingLevel = provider.ThinkingLevel(s.settings.DefaultThinkingLevel)
	}

	maxTokens := req.MaxTokens
	if maxTokens < 0 {
		maxTokens = 0
	}
	if maxTokens == 0 && !(currentModel != nil && currentModel.MaxTokensSet && currentModel.MaxTokens == 0) {
		maxTokens = agent.ResolveMaxTokens(currentModel)
	}

	// Per-request temperature/top_p override (from OpenAI-compatible client)
	if req.Temperature != nil {
		currentModel.Temperature = config.NormalizeSamplingPtr(req.Temperature)
	}
	if req.TopP != nil {
		currentModel.TopP = config.NormalizeSamplingPtr(req.TopP)
	}

	// applySessionToolOptions calls syncSessionTools before this point. Tool
	// registration is therefore owned by the session runtime/capability layer,
	// not by mode selection or individual requests. The shared Runtime snapshots
	// the already-synchronized registry below.
	// Build the Agent through the shared SessionRuntime. Request-specific system
	// instructions and token limits remain per-run inputs; resource and sandbox
	// assembly stay Runtime-owned.
	a, err := sess.Runtime.BuildAgent(agentruntime.AgentBuildOptions{
		Provider: currentProvider, ProviderName: s.providerName, Model: currentModel,
		Settings: runtimeSettings, Allow: s.getAllow(), Mode: mode,
		ExtraContext: extraContext, ThinkingLevel: thinkingLevel,
		MaxTokens: maxTokens, MaxTokensSet: true,
		MultiAgent: sess.MultiAgent, DelegateMode: sess.DelegateMode, Workflows: sess.Workflows,
		GetSteeringMessages: s.esmSteeringMessages(sess.ID),
		IntentID:            chatIntent.ID, RunID: runID, ConversationTurnID: "turn-" + runID,
		ConversationTurn: true, RuntimeOwnsTurnEnd: true,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}

	// Apply force compact flag from /compact command
	if sess.ForceCompact {
		a.SetForceCompact()
		sess.ForceCompact = false
	}

	replayState := sess.Manager.GetReplayState()
	if !hadPersistedHistory && len(historyMsgs) > 0 {
		// Seed brand-new sessions from client-provided history.
		internalMsgs := convertHistoryMessages(historyMsgs)
		a.LoadHistoryMessages(internalMsgs)
	}
	if len(replayState.Messages) > 0 {
		a.LoadHistoryState(replayState.Messages, replayState.EntryIDs)
	}

	// Setup request timeout
	timeout := time.Duration(s.cfg.RequestTimeoutSecs) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if !sess.attachRunAgent(runID, a, cancel) {
		a.Abort()
		writeError(w, http.StatusConflict, "session run is being cancelled", "session_run_cancelling")
		return
	}
	if (sess.MultiAgent || sess.DelegateMode || sess.Workflows) && sess.AgentMgr != nil {
		sess.AgentMgr.Register(agent.NewAgentAdapter(a))
		defer func() {
			sess.AgentMgr.Finish(a.ID(), ctx.Err())
		}()
	}

	// Run agent
	rawEventCh := a.RunWithUserMessage(ctx, lastUserMessage)
	eventCh := rawEventCh
	if s.runManager != nil {
		_ = s.runManager.SetHook(runID, func(ev agent.Event) {
			if ev.Type == agent.EventError && ev.Error != nil {
				info := agentruntime.ClassifyError(ev.Error, agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseModel})
				_ = s.recordSessionRunEvent(sess, runID, "event_error", "failed", "agent", currentModel.ID, mode, map[string]any{"error": info, "errorInfo": info})
			}
		})
		var cancelSubscription func()
		eventCh, cancelSubscription, err = s.runManager.Subscribe(runID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
			return
		}
		defer cancelSubscription()
		if err := s.runManager.Start(runID, rawEventCh); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
			return
		}
	}

	// Use RunExecutor to process events and publish via EventBroker.
	// This replaces the old handleStreamingResponseWithAgent/handleNonStreamingResponseWithAgent
	// event loop with a unified executor that publishes to EventBroker.
	executor := NewRunExecutor(s, s.getEventBroker(), &session.SessionRun{
		ID: runID, SessionID: sess.ID, WorkDir: sess.WorkDir,
		Source: runSource, Model: currentModel.ID, Mode: mode,
		Status: "running", StartedAt: runStartedAt,
	})

	if req.Stream {
		usage, status, errMsg := s.handleStreamingViaBroker(w, r, sess, runID, currentModel.ID, executor, a, eventCh, false)
		terminalStatus = status
		terminalErrMsg = errMsg
		eventData := withContextUsageEventData(usageEventData(usage, errMsg), a.GetContextUsage())
		if execution := sess.executionRuntime(); sess.isDurableRun(runID) && execution != nil {
			usageJSON, _ := json.Marshal(usage)
			contextUsageJSON, _ := json.Marshal(a.GetContextUsage())
			_ = execution.RecordUsage(runID, usageJSON, contextUsageJSON)
			if err := execution.FinishDurableWithRetry(context.Background(), runID, webUIRunState(status, errMsg), errMsg, agentruntime.RunEvent{SessionID: sess.ID, RunID: runID, EventType: runEventTypeForStatus(status), Source: runSource, Status: status, Model: currentModel.ID, Mode: mode, Timestamp: time.Now(), Data: rawEventData(eventData)}); err == nil {
				durableFinished = true
			}
		} else {
			_ = s.recordSessionRunEvent(sess, runID, runEventTypeForStatus(status), status, "chat_completion", currentModel.ID, mode, eventData)
		}
	} else {
		usage, status, errMsg := s.handleNonStreamingViaBroker(w, sess, runID, currentModel.ID, executor, a, eventCh)
		terminalStatus = status
		terminalErrMsg = errMsg
		eventData := withContextUsageEventData(usageEventData(usage, errMsg), a.GetContextUsage())
		if execution := sess.executionRuntime(); sess.isDurableRun(runID) && execution != nil {
			usageJSON, _ := json.Marshal(usage)
			contextUsageJSON, _ := json.Marshal(a.GetContextUsage())
			_ = execution.RecordUsage(runID, usageJSON, contextUsageJSON)
			if err := execution.FinishDurableWithRetry(context.Background(), runID, webUIRunState(status, errMsg), errMsg, agentruntime.RunEvent{SessionID: sess.ID, RunID: runID, EventType: runEventTypeForStatus(status), Source: runSource, Status: status, Model: currentModel.ID, Mode: mode, Timestamp: time.Now(), Data: rawEventData(eventData)}); err == nil {
				durableFinished = true
			}
		} else {
			_ = s.recordSessionRunEvent(sess, runID, runEventTypeForStatus(status), status, "chat_completion", currentModel.ID, mode, eventData)
		}
	}
}

func cloneModel(model *provider.Model) *provider.Model {
	if model == nil {
		return nil
	}
	copy := *model
	copy.Input = append([]string(nil), model.Input...)
	if model.Compat != nil {
		compat := *model.Compat
		copy.Compat = &compat
	}
	return &copy
}

func safeRunResultMessage(result *RunResult) string {
	if result == nil {
		return "The run could not be completed."
	}
	if result.ErrorInfo != nil {
		if message := strings.TrimSpace(agentruntime.DisplayErrorMessage(*result.ErrorInfo)); message != "" {
			return message
		}
	}
	if result.Status == "canceled" || result.Status == "cancelled" {
		if strings.Contains(strings.ToLower(result.Error), "deadline") || strings.Contains(strings.ToLower(result.Error), "timeout") {
			return "The run timed out."
		}
		return "The run was cancelled."
	}
	info := agentruntime.ClassifyError(errors.New(result.Error), agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseModel, SideEffectState: agentruntime.SideEffectUnknown})
	return agentruntime.DisplayErrorMessage(info)
}

func safeAgentErrorMessage(err error) string {
	info := agentruntime.ClassifyError(err, agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseModel, SideEffectState: agentruntime.SideEffectUnknown})
	if message := strings.TrimSpace(agentruntime.DisplayErrorMessage(info)); message != "" {
		return message
	}
	return "The run could not be completed."
}

func sameWorkDir(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// handleStreamingViaBroker consumes agent events via RunExecutor and writes SSE
// by subscribing to the EventBroker. The RunExecutor processes events (publishing
// to EventBroker) while this function only converts BrokerEvent to SSE output.
func (s *Server) handleStreamingViaBroker(w http.ResponseWriter, r *http.Request, sess *APISession, runID, modelID string, executor *RunExecutor, runningAgent *agent.Agent, rawEventCh <-chan agent.Event, transcript bool) (CompletionUsage, string, string) {
	sessionID := sess.ID
	sse := NewSSEWriter(w, modelID, sessionID)
	sse.WriteRoleDelta()

	toolDetail := s.cfg.GetToolDetail()
	var totalUsage CompletionUsage
	pendingTools := make(map[string]*toolCallInfo)

	// Subscribe to the EventBroker to receive processed events.
	broker := s.getEventBroker()
	brokerEvents, brokerCancel := broker.Subscribe(sessionID)
	defer brokerCancel()

	// Run the executor in a goroutine so it processes events and publishes to the broker.
	execDone := make(chan *RunResult, 1)
	execErr := make(chan error, 1)
	go func() {
		result, err := executor.Execute(context.Background(), sess, runningAgent, rawEventCh, modelID, "", transcript)
		if err != nil {
			execErr <- err
		} else {
			execDone <- result
		}
	}()

	// Consume broker events and convert to SSE until the executor finishes.
	for {
		select {
		case result := <-execDone:
			// Executor finished; map the canonical run status to SSE output.
			executor.Finalize(sess, result)
			if result.Usage != nil {
				totalUsage = *result.Usage
			}
			switch result.Status {
			case "failed":
				errMsg := safeRunResultMessage(result)
				sse.WriteError(errMsg)
			case "canceled":
				sse.WriteDone(&totalUsage)
			default:
				finishReason := "stop"
				sse.WriteDoneReason(&totalUsage, finishReason)
			}
			return totalUsage, result.Status, result.Error

		case err := <-execErr:
			executor.Finalize(sess, nil)
			info := agentruntime.ClassifyError(err, agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseTransport, SideEffectState: agentruntime.SideEffectUnknown})
			message := agentruntime.DisplayErrorMessage(info)
			sse.WriteError(message)
			return totalUsage, "failed", message

		case ev, ok := <-brokerEvents:
			if !ok {
				// Broker channel closed, executor may have finished.
				continue
			}
			switch ev.Event {
			case "transcript":
				if tsEv, ok := ev.Data.(TranscriptStreamEvent); ok {
					if tsEv.Type == "hosted_item" && tsEv.HostedItem != nil {
						sse.WriteHostedItem(tsEv.HostedItem)
					} else if tsEv.Message != nil && tsEv.Message.AgentID == "" {
						sse.WriteContentDelta(tsEv.Message.Content)
					}
				}

			case "tool_event":
				if toolEv, ok := ev.Data.(ToolStatusEvent); ok {
					if toolEv.Status == "running" {
						pendingTools[toolEv.ToolCallID] = &toolCallInfo{Name: toolEv.Tool, Args: toolEv.Args, Status: "running"}
						sse.WriteContentDelta(formatToolRunning(toolEv.Tool, toolEv.Args))
						continue
					}
					tc := pendingTools[toolEv.ToolCallID]
					if tc == nil {
						tc = &toolCallInfo{Name: toolEv.Tool, Args: toolEv.Args}
					}
					tc.Status = toolEv.Status
					tc.Result = toolEv.Summary
					if toolEv.IsError {
						tc.Error = fmt.Errorf("tool failed")
					}
					delete(pendingTools, toolEv.ToolCallID)
					sse.WriteToolResult(tc, toolDetail)
				}

			case "approval_request":
				// Approval requests are handled by registerSessionApproval in the RunExecutor.
				// The SSE writer can emit an approval request event if needed.
				if sse != nil && ev.Data != nil {
					if reqData, ok := ev.Data.(map[string]any); ok {
						_ = reqData // approval requests are handled server-side
					}
				}

			case "run_event":
				// Run lifecycle events (started, finished, etc.) are for observers.

			case "done":
				// Stream done signal from the executor.
				// The actual termination is handled by the execDone/execErr channels.
			}
		}
	}
}

func (s *Server) handleStreamingResponse(w http.ResponseWriter, r *http.Request, eventCh <-chan agent.Event, modelID, sessionID string, transcript bool) (CompletionUsage, string, string) {
	return s.handleStreamingResponseWithAgent(w, r, eventCh, modelID, &APISession{ID: sessionID}, nil, transcript)
}

func (s *Server) handleStreamingResponseWithAgent(w http.ResponseWriter, r *http.Request, eventCh <-chan agent.Event, modelID string, sess *APISession, runningAgent *agent.Agent, transcript bool) (CompletionUsage, string, string) {
	sessionID := sess.ID
	sse := NewSSEWriter(w, modelID, sessionID)
	sse.WriteRoleDelta()

	toolMode := s.cfg.ToolVisibility.Mode
	toolDetail := s.cfg.GetToolDetail()
	var totalUsage CompletionUsage
	var xToolCalls []ToolCallSummary
	var attachments []provider.Attachment
	// Track in-flight tool calls by callID so we can attach result/diff on end.
	pendingTools := make(map[string]*toolCallInfo)

	for ev := range eventCh {
		switch ev.Type {
		case agent.EventHostedItem:
			if ev.HostedItem != nil {
				item := hostedItemEvent(ev.HostedItem)
				if transcript {
					s.writeTranscriptEvent(sse, sessionID, TranscriptStreamEvent{Type: "hosted_item", HostedItem: item})
				} else {
					sse.WriteHostedItem(item)
				}
			}
		case agent.EventTextDelta:
			if transcript {
				s.writeTranscriptEvent(sse, sessionID, assistantDeltaTranscriptEvent(ev.TextDelta, ev.AgentID, ev))
			}
			if ev.AgentID == "" {
				sse.WriteContentDelta(ev.TextDelta)
			}

		case agent.EventToolCall:
			name, callID := resolveToolEvent(ev)
			tc := &toolCallInfo{Name: name, Args: ev.ToolArgs, Status: "running"}
			if callID != "" {
				pendingTools[callID] = tc
			}
			xToolCalls = append(xToolCalls, ToolCallSummary{Name: name, Args: ev.ToolArgs, Status: "running"})
			s.publishToolEvent(sessionID, ToolStatusEvent{Tool: name, ToolCallID: callID, AgentID: string(ev.AgentID), Status: "running", Args: ev.ToolArgs})
			if transcript {
				s.writeTranscriptEvent(sse, sessionID, messageTranscriptEvent(transcriptToolCallEntry(name, callID, ev)))
			} else {
				switch toolMode {
				case "content":
					sse.WriteContentDelta(formatToolRunning(name, ev.ToolArgs))
				case "sse_event":
					sse.WriteToolStatusEvent(ToolStatusEvent{
						Tool:       name,
						ToolCallID: callID,
						AgentID:    string(ev.AgentID),
						Status:     "running",
						Args:       ev.ToolArgs,
					})
				}
			}

		case agent.EventToolExecutionEnd:
			status := "completed"
			if ev.ToolError != nil {
				status = "failed"
			}
			// Update xToolCalls status
			for i := len(xToolCalls) - 1; i >= 0; i-- {
				if xToolCalls[i].Name == ev.ToolName && xToolCalls[i].Status == "running" {
					xToolCalls[i].Status = status
					break
				}
			}
			// Build expanded output
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
			s.publishToolEvent(sessionID, ToolStatusEvent{
				Tool: name, ToolCallID: ev.ToolCallID, AgentID: string(ev.AgentID), Status: status,
				Args: tc.Args, Summary: toolStatusSummary(ev.ToolResult, ev.ToolError), IsError: ev.ToolError != nil, HasDetail: ev.ToolCallID != "",
			})

			if transcript {
				s.writeTranscriptEvent(sse, sessionID, messageTranscriptEvent(transcriptToolResultEntry(name, ev, status)))
			} else {
				switch toolMode {
				case "content":
					sse.WriteToolResult(tc, toolDetail)
				case "sse_event":
					sse.WriteToolStatusEvent(ToolStatusEvent{
						Tool:       name,
						ToolCallID: ev.ToolCallID,
						AgentID:    string(ev.AgentID),
						Status:     status,
						Args:       tc.Args,
						Summary:    toolStatusSummary(ev.ToolResult, ev.ToolError),
						IsError:    ev.ToolError != nil,
						HasDetail:  ev.ToolCallID != "",
					})
				}
			}

		case agent.EventToolApprovalRequest:
			if request := s.registerSessionApproval(sess, runningAgent, ev); request != nil {
				sse.WriteApprovalRequest(*request)
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
			if ev.RetryMaxTokens > 0 {
				message := fmt.Sprintf("Output limit reached; retrying with %d max tokens", ev.RetryMaxTokens)
				if transcript {
					s.writeTranscriptEvent(sse, sessionID, assistantDeltaTranscriptEvent("\n\n[Retry: "+message+"]", ""))
				}
				sse.WriteStatusEvent(message)
			}

		case agent.EventRunFinished:
			if ev.AgentID != "" {
				if transcript {
					s.writeTranscriptEvent(sse, sessionID, subAgentStatusTranscriptEvent(ev.AgentID, subAgentStatusForTaskStatus(ev.Status), errorString(ev.Error), ev))
				}
				continue
			}
			switch ev.Status {
			case agent.TaskFailed:
				errMsg := safeAgentErrorMessage(ev.Error)
				if transcript {
					s.writeTranscriptEvent(sse, sessionID, assistantDeltaTranscriptEvent("\n\n[Error: "+errMsg+"]", ""))
				}
				sse.WriteError(errMsg)
				return totalUsage, "failed", errMsg
			case agent.TaskCanceled:
				errMsg := safeAgentErrorMessage(ev.Error)
				sse.WriteDone(&totalUsage)
				return totalUsage, "canceled", errMsg
			case agent.TaskIncomplete, agent.TaskSuccess:
				attachments = append([]provider.Attachment(nil), ev.Attachments...)
				sse.WriteAttachments(attachments)
				finishReason := "stop"
				if isOutputTruncationStopReason(ev.StopReason) {
					finishReason = "length"
				}
				sse.WriteDoneReason(&totalUsage, finishReason)
				if ev.Status == agent.TaskIncomplete {
					return totalUsage, "incomplete", ""
				}
				return totalUsage, "completed", ""
			}

		case agent.EventDone:
			if ev.AgentID != "" {
				if transcript {
					s.writeTranscriptEvent(sse, sessionID, subAgentStatusTranscriptEvent(ev.AgentID, "done", "", ev))
				}
				continue
			}
			attachments = append([]provider.Attachment(nil), ev.Attachments...)
			sse.WriteAttachments(attachments)
			finishReason := "stop"
			if isOutputTruncationStopReason(ev.StopReason) {
				finishReason = "length"
			}
			sse.WriteDoneReason(&totalUsage, finishReason)
			return totalUsage, "completed", ""

		case agent.EventError:
			if ev.AgentID != "" {
				if transcript {
					s.writeTranscriptEvent(sse, sessionID, subAgentStatusTranscriptEvent(ev.AgentID, "error", safeAgentErrorMessage(ev.Error), ev))
				}
				continue
			}
			if ev.Error != nil && (errors.Is(ev.Error, context.Canceled) || errors.Is(ev.Error, context.DeadlineExceeded)) {
				sse.WriteDone(&totalUsage)
				return totalUsage, "canceled", safeAgentErrorMessage(ev.Error)
			}
			// An error event without an error payload is a protocol violation,
			// never a successful completion.
			errMsg := safeAgentErrorMessage(ev.Error)
			if transcript {
				s.writeTranscriptEvent(sse, sessionID, assistantDeltaTranscriptEvent("\n\n[Error: "+errMsg+"]", ""))
			}
			sse.WriteError(errMsg)
			return totalUsage, "failed", errMsg
		}
	}
	// Channel closed without a terminal event — protocol failure, never success.
	sse.WriteError("event stream closed without terminal result")
	return totalUsage, "failed", "event stream closed without terminal result"
}

func hostedItemEvent(item *provider.HostedItem) *HostedItemEvent {
	if item == nil {
		return nil
	}
	safe := safeHostedItemRunData(item)
	result := &HostedItemEvent{
		ID: safe["id"].(string), Type: safe["type"].(string), Status: safe["status"].(string),
		OutputIndex: safe["outputIndex"].(int),
	}
	if metadata, ok := safe["metadata"].(map[string]any); ok {
		result.Metadata = metadata
	}
	return result
}

func isOutputTruncationStopReason(reason string) bool {
	return strings.EqualFold(reason, "length") || strings.EqualFold(reason, "max_tokens") || strings.EqualFold(reason, "max_output_tokens") || strings.EqualFold(reason, "token_limit")
}

// subAgentStatusForTaskStatus maps the canonical TaskStatus to the sub-agent
// status string used in transcript events.
func subAgentStatusForTaskStatus(status agent.TaskStatus) string {
	switch status {
	case agent.TaskFailed:
		return "error"
	case agent.TaskIncomplete:
		return "incomplete"
	case agent.TaskCanceled:
		return "canceled"
	default:
		return "done"
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return safeAgentErrorMessage(err)
}

func assistantDeltaTranscriptEvent(text string, agentID agentpkg.AgentID, memberEvent ...agent.Event) TranscriptStreamEvent {
	entry := SessionMessageEntry{
		AgentID: string(agentID),
		Role:    "assistant",
		Content: text,
	}
	if len(memberEvent) > 0 {
		applyMemberEventMetadata(&entry, memberEvent[0])
	}
	return TranscriptStreamEvent{
		Type:    "assistant_delta",
		Message: &entry,
	}
}

func assistantAttachmentsTranscriptEvent(items []provider.Attachment, agentID agentpkg.AgentID) TranscriptStreamEvent {
	return TranscriptStreamEvent{
		Type: "attachments",
		Message: &SessionMessageEntry{
			AgentID:     string(agentID),
			Role:        "assistant",
			Attachments: append([]provider.Attachment(nil), items...),
		},
	}
}

func messageTranscriptEvent(entry SessionMessageEntry) TranscriptStreamEvent {
	return TranscriptStreamEvent{
		Type:    "message",
		Message: &entry,
	}
}

func subAgentStatusTranscriptEvent(agentID agentpkg.AgentID, status string, summary string, memberEvent ...agent.Event) TranscriptStreamEvent {
	entry := SessionMessageEntry{
		AgentID: string(agentID),
		Role:    "status",
		Content: status,
		Summary: summary,
		IsError: status == "error",
	}
	if len(memberEvent) > 0 {
		applyMemberEventMetadata(&entry, memberEvent[0])
	}
	return TranscriptStreamEvent{
		Type:    "subagent_status",
		Message: &entry,
	}
}

func applyMemberEventMetadata(entry *SessionMessageEntry, ev agent.Event) {
	if entry == nil || ev.MemberID == "" {
		return
	}
	entry.MemberID = ev.MemberID
	entry.ExpertID = ev.ExpertID
	entry.MemberDisplayName = ev.MemberDisplayName
	entry.MemberEmoji = ev.MemberEmoji
	entry.MemberRole = ev.MemberRole
}

func transcriptToolCallEntry(name, callID string, ev agent.Event) SessionMessageEntry {
	args := rawToolArgs(ev.ToolArgs)
	invalidArgs := ""
	if ev.ToolCall != nil {
		if ev.ToolCall.Name != "" {
			name = ev.ToolCall.Name
		}
		if ev.ToolCall.ID != "" {
			callID = ev.ToolCall.ID
		}
		if len(ev.ToolCall.Arguments) > 0 {
			args = validRawMessage(ev.ToolCall.Arguments)
		}
		invalidArgs = ev.ToolCall.InvalidArguments
	}
	entry := SessionMessageEntry{
		Role:        "toolCall",
		AgentID:     string(ev.AgentID),
		ToolCallID:  callID,
		ToolName:    name,
		Arguments:   args,
		InvalidArgs: invalidArgs,
		Plan:        planFromToolCall(name, args),
	}
	applyMemberEventMetadata(&entry, ev)
	return entry
}

func transcriptToolResultEntry(name string, ev agent.Event, status string) SessionMessageEntry {
	isError := status == "failed" || ev.ToolError != nil
	summary := summarizeToolStatusResult(ev.ToolResult)
	if isError {
		summary = safeToolErrorSummary(ev.ToolResult, ev.ToolError)
	}
	entry := SessionMessageEntry{
		Role:       "toolResult",
		AgentID:    string(ev.AgentID),
		ToolCallID: ev.ToolCallID,
		ToolName:   name,
		IsError:    isError,
		Summary:    summary,
		HasDetail:  ev.ToolCallID != "",
	}
	applyMemberEventMetadata(&entry, ev)
	return entry
}

func rawToolArgs(args map[string]any) json.RawMessage {
	if len(args) == 0 {
		return nil
	}
	data, err := json.Marshal(args)
	if err != nil || !json.Valid(data) {
		return nil
	}
	return data
}

// handleNonStreamingViaBroker runs the executor, waits for completion, and writes
// a single JSON response. No SSE streaming is needed.
func (s *Server) handleNonStreamingViaBroker(w http.ResponseWriter, sess *APISession, runID, modelID string, executor *RunExecutor, runningAgent *agent.Agent, rawEventCh <-chan agent.Event) (CompletionUsage, string, string) {
	sessionID := sess.ID
	var totalUsage CompletionUsage
	toolMode := s.cfg.ToolVisibility.Mode
	toolDetail := s.cfg.GetToolDetail()

	// Subscribe to the EventBroker to accumulate content and tool calls.
	broker := s.getEventBroker()
	brokerEvents, brokerCancel := broker.Subscribe(sessionID)
	defer brokerCancel()

	// Run the executor in a goroutine.
	execDone := make(chan *RunResult, 1)
	execErr := make(chan error, 1)
	go func() {
		result, err := executor.Execute(context.Background(), sess, runningAgent, rawEventCh, modelID, "", true)
		if err != nil {
			execErr <- err
		} else {
			execDone <- result
		}
	}()

	var sb strings.Builder
	var xToolCalls []ToolCallSummary
	pendingTools := make(map[string]*toolCallInfo)

	for {
		select {
		case result := <-execDone:
			executor.Finalize(sess, result)
			if result.Usage != nil {
				totalUsage = *result.Usage
			}
			switch result.Status {
			case "failed":
				msg := result.Error
				if msg == "" {
					msg = "run failed"
				}
				writeError(w, http.StatusInternalServerError, msg, "server_error")
				return totalUsage, result.Status, msg
			case "canceled":
				msg := result.Error
				if msg == "" {
					msg = "run canceled"
				}
				writeError(w, http.StatusConflict, msg, "request_canceled")
				return totalUsage, result.Status, msg
			case "incomplete":
				xToolCalls = result.ToolCalls
				finishReason := "length"
				resp := ChatCompletionResponse{
					ID: newCompletionID(), Object: "chat.completion", Created: time.Now().Unix(), Model: modelID,
					Choices: []ChatCompletionChoice{{Index: 0, Message: &ResponseMessage{Role: "assistant", Content: sb.String()}, FinishReason: &finishReason}},
					Usage:   &totalUsage,
				}
				writeJSON(w, http.StatusOK, resp)
				return totalUsage, result.Status, result.Error
			}
			xToolCalls = result.ToolCalls
			finishReason := "stop"
			resp := ChatCompletionResponse{
				ID:      newCompletionID(),
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   modelID,
				Choices: []ChatCompletionChoice{
					{
						Index:        0,
						Message:      &ResponseMessage{Role: "assistant", Content: sb.String()},
						FinishReason: &finishReason,
					},
				},
				Usage: &totalUsage,
			}
			writeJSON(w, http.StatusOK, resp)
			return totalUsage, result.Status, result.Error

		case err := <-execErr:
			executor.Finalize(sess, nil)
			errMsg := safeAgentErrorMessage(err)
			writeError(w, http.StatusInternalServerError, errMsg, "server_error")
			return totalUsage, "failed", errMsg

		case ev, ok := <-brokerEvents:
			if !ok {
				continue
			}
			switch ev.Event {
			case "transcript":
				if tsEv, ok := ev.Data.(TranscriptStreamEvent); ok {
					if tsEv.Message != nil && tsEv.Message.AgentID == "" {
						sb.WriteString(tsEv.Message.Content)
					}
				}
			case "tool_event":
				if toolEv, ok := ev.Data.(ToolStatusEvent); ok {
					if toolEv.Status == "running" {
						tc := &toolCallInfo{Name: toolEv.Tool, Args: toolEv.Args, Status: "running"}
						if toolEv.ToolCallID != "" {
							pendingTools[toolEv.ToolCallID] = tc
						}
						xToolCalls = append(xToolCalls, ToolCallSummary{Name: toolEv.Tool, Args: toolEv.Args, Status: "running"})
					} else {
						tc := pendingTools[toolEv.ToolCallID]
						if tc == nil {
							tc = &toolCallInfo{Name: toolEv.Tool, Args: toolEv.Args}
						}
						tc.Status = toolEv.Status
						delete(pendingTools, toolEv.ToolCallID)
						for i := len(xToolCalls) - 1; i >= 0; i-- {
							if xToolCalls[i].Name == toolEv.Tool && xToolCalls[i].Status == "running" {
								xToolCalls[i].Status = toolEv.Status
								break
							}
						}
						if toolMode == "content" && toolEv.AgentID == "" {
							sb.WriteString(formatToolResult(tc, toolDetail))
						}
					}
				}
			case "done":
				// Handled by execDone channel.
			}
		}
	}
}

func (s *Server) handleNonStreamingResponse(w http.ResponseWriter, eventCh <-chan agent.Event, modelID, sessionID string) (CompletionUsage, string, string) {
	return s.handleNonStreamingResponseWithAgent(w, eventCh, modelID, &APISession{ID: sessionID}, nil)
}

func (s *Server) handleNonStreamingResponseWithAgent(w http.ResponseWriter, eventCh <-chan agent.Event, modelID string, sess *APISession, runningAgent *agent.Agent) (CompletionUsage, string, string) {
	sessionID := sess.ID
	var sb strings.Builder
	var totalUsage CompletionUsage
	var xToolCalls []ToolCallSummary
	toolMode := s.cfg.ToolVisibility.Mode
	toolDetail := s.cfg.GetToolDetail()
	pendingTools := make(map[string]*toolCallInfo)
	sawTerminal := false

	for ev := range eventCh {
		switch ev.Type {
		case agent.EventTextDelta:
			if ev.AgentID == "" {
				sb.WriteString(ev.TextDelta)
			}

		case agent.EventToolCall:
			name, callID := resolveToolEvent(ev)
			tc := &toolCallInfo{Name: name, Args: ev.ToolArgs, Status: "running"}
			if callID != "" {
				pendingTools[callID] = tc
			}
			xToolCalls = append(xToolCalls, ToolCallSummary{Name: name, Args: ev.ToolArgs, Status: "running"})
			s.publishToolEvent(sessionID, ToolStatusEvent{Tool: name, ToolCallID: callID, AgentID: string(ev.AgentID), Status: "running", Args: ev.ToolArgs})

		case agent.EventToolExecutionEnd:
			status := "completed"
			if ev.ToolError != nil {
				status = "failed"
			}
			for i := len(xToolCalls) - 1; i >= 0; i-- {
				if xToolCalls[i].Name == ev.ToolName && xToolCalls[i].Status == "running" {
					xToolCalls[i].Status = status
					break
				}
			}
			// Build expanded output for content/none mode
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
			s.publishToolEvent(sessionID, ToolStatusEvent{
				Tool: name, ToolCallID: ev.ToolCallID, AgentID: string(ev.AgentID), Status: status,
				Args: tc.Args, Summary: toolStatusSummary(ev.ToolResult, ev.ToolError), IsError: ev.ToolError != nil, HasDetail: ev.ToolCallID != "",
			})

			if toolMode == "content" && ev.AgentID == "" {
				sb.WriteString(formatToolResult(tc, toolDetail))
			}

		case agent.EventToolApprovalRequest:
			s.registerSessionApproval(sess, runningAgent, ev)

		case agent.EventUsage:
			if ev.Usage != nil {
				totalUsage.PromptTokens += ev.Usage.TotalInputTokens()
				totalUsage.CompletionTokens += ev.Usage.Output
				totalUsage.CacheReadTokens += ev.Usage.CacheRead
				totalUsage.CacheWriteTokens += ev.Usage.CacheWrite
				totalUsage.TotalTokens = totalUsage.PromptTokens + totalUsage.CompletionTokens
			}

		case agent.EventRunFinished:
			if ev.AgentID != "" {
				continue
			}
			sawTerminal = true
			switch ev.Status {
			case agent.TaskFailed:
				msg := safeAgentErrorMessage(ev.Error)
				writeError(w, http.StatusInternalServerError, msg, "server_error")
				return totalUsage, "failed", msg
			case agent.TaskCanceled:
				msg := safeAgentErrorMessage(ev.Error)
				return totalUsage, "canceled", msg
			}
			// success/incomplete: keep consuming; the completion response is built
			// after the stream closes.

		case agent.EventDone:
			if ev.AgentID == "" {
				sawTerminal = true
			}

		case agent.EventError:
			if ev.AgentID != "" {
				continue
			}
			sawTerminal = true
			if ev.Error != nil {
				if errors.Is(ev.Error, context.Canceled) || errors.Is(ev.Error, context.DeadlineExceeded) {
					return totalUsage, "canceled", safeAgentErrorMessage(ev.Error)
				}
				errMsg := safeAgentErrorMessage(ev.Error)
				writeError(w, http.StatusInternalServerError, errMsg, "server_error")
				return totalUsage, "failed", errMsg
			}
			// An error event without an error payload is a protocol violation,
			// never a successful completion.
			writeError(w, http.StatusInternalServerError, "error event without error detail", "server_error")
			return totalUsage, "failed", "error event without error detail"
		}
	}

	if !sawTerminal {
		// Channel closed without a terminal event — protocol failure, never success.
		writeError(w, http.StatusInternalServerError, "event stream closed without terminal result", "server_error")
		return totalUsage, "failed", "event stream closed without terminal result"
	}

	finishReason := "stop"
	resp := ChatCompletionResponse{
		ID:      newCompletionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelID,
		Choices: []ChatCompletionChoice{
			{
				Index:        0,
				Message:      &ResponseMessage{Role: "assistant", Content: sb.String()},
				FinishReason: &finishReason,
			},
		},
		Usage: &totalUsage,
	}
	writeJSON(w, http.StatusOK, resp)
	return totalUsage, "completed", ""
}

func summarizeToolStatusResult(result string) string {
	text := strings.TrimSpace(result)
	if text == "" {
		return "(empty result)"
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	return util.TruncateWithSuffix(text, 140, "...")
}

func toolStatusSummary(result string, toolErr error) string {
	if toolErr != nil {
		return safeToolErrorSummary(result, toolErr)
	}
	return summarizeToolStatusResult(result)
}

func safeToolErrorSummary(result string, toolErr error) string {
	if toolErr == nil {
		toolErr = errors.New(strings.TrimSpace(result))
	}
	info := agentruntime.ClassifyError(toolErr, agentruntime.ErrorClassificationOptions{
		Phase: agentruntime.PhaseTool, SideEffectState: agentruntime.SideEffectUnknown,
	})
	return agentruntime.DisplayErrorMessage(info)
}

func (s *Server) writeCommandResponse(w http.ResponseWriter, result *CommandResult, modelID, sessionID, cmd string) {
	finishReason := "stop"
	resp := ChatCompletionResponse{
		ID:      newCommandCompletionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelID,
		Choices: []ChatCompletionChoice{
			{
				Index:        0,
				Message:      &ResponseMessage{Role: "assistant", Content: result.Message},
				FinishReason: &finishReason,
			},
		},
		Usage: &CompletionUsage{},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) writeCommandResponseStreaming(w http.ResponseWriter, result *CommandResult, modelID, sessionID, cmd string, transcript bool) {
	sse := NewSSEWriter(w, modelID, sessionID)
	sse.WriteRoleDelta()
	if transcript {
		s.writeTranscriptEvent(sse, sessionID, assistantDeltaTranscriptEvent(result.Message, ""))
	}
	sse.WriteContentDelta(result.Message)
	sse.WriteDone(&CompletionUsage{})
}

// AllocateSessionID returns a server-owned ID for a delayed WebUI session.
// Allocation does not persist a session; the first run still creates it. This
// keeps the WebUI's delayed-creation UX while making ID generation canonical.
func (s *Server) AllocateSessionID() (string, error) {
	if s == nil || s.settings == nil {
		return "", fmt.Errorf("session server is not ready")
	}
	s.sessionCreateMu.Lock()
	defer s.sessionCreateMu.Unlock()

	now := time.Now()
	s.mu.Lock()
	if s.allocatedSessionIDs == nil {
		s.allocatedSessionIDs = make(map[string]time.Time)
	}
	for id, allocatedAt := range s.allocatedSessionIDs {
		if now.Sub(allocatedAt) > 10*time.Minute {
			delete(s.allocatedSessionIDs, id)
		}
	}
	s.mu.Unlock()

	for attempt := 0; attempt < 16; attempt++ {
		id := session.GenerateID()
		s.mu.RLock()
		_, reserved := s.allocatedSessionIDs[id]
		s.mu.RUnlock()
		if reserved {
			continue
		}
		if _, err := session.OpenByIDExact(s.settings.GetSessionDir(), id); err == nil {
			continue
		} else {
			errText := strings.ToLower(err.Error())
			// OpenByIDExact reports a missing ID differently depending on
			// whether sessions.db exists: "not found" when the database is
			// absent, and "not registered in DB" when the row is absent.
			if strings.Contains(errText, "not found") || strings.Contains(errText, "not registered in db") {
				s.mu.Lock()
				s.allocatedSessionIDs[id] = now
				s.mu.Unlock()
				return id, nil
			}
			return "", fmt.Errorf("check session ID availability: %w", err)
		}
	}
	return "", fmt.Errorf("allocate unique session ID: %w", session.ErrSessionIDExists)
}

func (s *Server) claimAllocatedSessionID(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.allocatedSessionIDs == nil {
		return false
	}
	if _, ok := s.allocatedSessionIDs[id]; !ok {
		return false
	}
	delete(s.allocatedSessionIDs, id)
	return true
}

// getOrCreateSession returns an existing session or creates a new one.
func (s *Server) getOrCreateSession(sessionID, workDir string) (*APISession, error) {
	if sessionID != "" {
		if sess := s.pool.Get(sessionID); sess != nil {
			if workDir != "" && !sameWorkDir(sess.WorkDir, workDir) {
				return nil, fmt.Errorf("session %q belongs to a different working directory", sessionID)
			}
			if err := s.validatePersistedSessionWorkDir(sess.WorkDir); err != nil {
				return nil, err
			}
			return sess, nil
		}
	}

	// Serialize creation so concurrent requests don't create duplicate sessions
	// for the same explicit ID or work-directory default.
	s.sessionCreateMu.Lock()
	defer s.sessionCreateMu.Unlock()
	allocatedID := false

	if sessionID != "" {
		if sess := s.pool.Get(sessionID); sess != nil {
			if workDir != "" && !sameWorkDir(sess.WorkDir, workDir) {
				return nil, fmt.Errorf("session %q belongs to a different working directory", sessionID)
			}
			if err := s.validatePersistedSessionWorkDir(sess.WorkDir); err != nil {
				return nil, err
			}
			return sess, nil
		}
		allocatedID = s.claimAllocatedSessionID(sessionID)
		if !allocatedID {
			if sess, err := session.OpenByIDExact(s.settings.GetSessionDir(), sessionID); err == nil {
				sessWorkDir := workDir
				if sess.GetHeader() != nil && sess.GetHeader().Cwd != "" {
					sessWorkDir = sess.GetHeader().Cwd
				}
				if err := s.validatePersistedSessionWorkDir(sessWorkDir); err != nil {
					return nil, err
				}
				resources, err := s.buildSessionResources(sessWorkDir)
				if err != nil {
					return nil, err
				}
				gwSess := &APISession{
					Runtime:      resources.runtime,
					ID:           sessionID,
					WorkDir:      sessWorkDir,
					Manager:      sess,
					Registry:     resources.registry,
					SkillsMgr:    resources.skillsMgr,
					ExtraContext: resources.extraContext,
					RuleContent:  resources.ruleContent,
					DelegateMode: s.cfg.EnableDelegate,
					Workflows:    s.cfg.EnableWorkflows,
					WebSearch:    s.IsWebSearchAvailable(),
					Browser:      s.cfg.EnableBrowser,
					A2AMaster:    s.cfg.EnableA2AMaster,
					MultiAgent:   s.cfg.EnableSubAgents,
					LastUsed:     time.Now(),
				}
				if err := bindSessionRuntime(gwSess); err != nil {
					resources.runtime.Close()
					return nil, err
				}
				if err := s.applyStoredSessionCapabilities(gwSess); err != nil {
					return nil, err
				}
				if gwSess.MultiAgent || gwSess.DelegateMode || gwSess.Workflows {
					gwSess.AgentMgr = s.newAgentManagerForSession(gwSess)
				}
				s.registerCronTool(gwSess)
				if err := s.pool.Put(gwSess); err != nil {
					return nil, err
				}
				return gwSess, nil
			}
		}
	} else {
		s.mu.RLock()
		defaultID := s.defaultSessionIDs[workDir]
		s.mu.RUnlock()
		if defaultID != "" {
			if sess := s.pool.GetForWorkDir(workDir, defaultID); sess != nil {
				return sess, nil
			}
			if sess, err := session.OpenByIDExact(s.settings.GetSessionDir(), defaultID); err == nil {
				sessWorkDir := workDir
				if sess.GetHeader() != nil && sess.GetHeader().Cwd != "" {
					sessWorkDir = sess.GetHeader().Cwd
				}
				if err := s.validatePersistedSessionWorkDir(sessWorkDir); err != nil {
					return nil, err
				}
				resources, err := s.buildSessionResources(sessWorkDir)
				if err != nil {
					return nil, err
				}
				gwSess := &APISession{
					Runtime:      resources.runtime,
					ID:           defaultID,
					WorkDir:      sessWorkDir,
					Manager:      sess,
					Registry:     resources.registry,
					SandboxMgr:   resources.sandboxMgr,
					SkillsMgr:    resources.skillsMgr,
					ExtraContext: resources.extraContext,
					RuleContent:  resources.ruleContent,
					DelegateMode: s.cfg.EnableDelegate,
					Workflows:    s.cfg.EnableWorkflows,
					WebSearch:    s.IsWebSearchAvailable(),
					Browser:      s.cfg.EnableBrowser,
					A2AMaster:    s.cfg.EnableA2AMaster,
					MultiAgent:   s.cfg.EnableSubAgents,
					LastUsed:     time.Now(),
				}
				if err := bindSessionRuntime(gwSess); err != nil {
					resources.runtime.Close()
					return nil, err
				}
				if err := s.applyStoredSessionCapabilities(gwSess); err != nil {
					return nil, err
				}
				if gwSess.MultiAgent || gwSess.DelegateMode || gwSess.Workflows {
					gwSess.AgentMgr = s.newAgentManagerForSession(gwSess)
				}
				s.registerCronTool(gwSess)
				if err := s.pool.Put(gwSess); err != nil {
					return nil, err
				}
				return gwSess, nil
			}
			sessionID = defaultID
		}
	}

	// Create new session
	mgr, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{
		WorkDir: workDir, SessionDir: s.settings.GetSessionDir(), ID: sessionID,
	})
	if err != nil {
		if sessionID != "" {
			return nil, fmt.Errorf("initialize session %q: %w", sessionID, err)
		}
		return nil, fmt.Errorf("initialize session: %w", err)
	}

	id := sessionID
	if id == "" && mgr.GetHeader() != nil {
		id = mgr.GetHeader().ID
	}

	resources, err := s.buildSessionResources(workDir)
	if err != nil {
		return nil, err
	}

	sess := &APISession{
		Runtime:      resources.runtime,
		ID:           id,
		WorkDir:      workDir,
		Manager:      mgr,
		Registry:     resources.registry,
		SandboxMgr:   resources.sandboxMgr,
		MCPClients:   resources.mcpClients,
		Mode:         "",
		SkillsMgr:    resources.skillsMgr,
		ExtraContext: resources.extraContext,
		RuleContent:  resources.ruleContent,
		DelegateMode: s.cfg.EnableDelegate,
		Workflows:    s.cfg.EnableWorkflows,
		WebSearch:    s.IsWebSearchAvailable(),
		Browser:      s.cfg.EnableBrowser,
		A2AMaster:    s.cfg.EnableA2AMaster,
		MultiAgent:   s.cfg.EnableSubAgents,
		LastUsed:     time.Now(),
	}
	if err := bindSessionRuntime(sess); err != nil {
		resources.runtime.Close()
		return nil, err
	}
	if err := s.applyStoredSessionCapabilities(sess); err != nil {
		return nil, err
	}

	// The runtime resolves a team expert from the persisted session binding.
	// It forces the sub-agent capability even when the adapter-level toggle is
	// false, so tool/manager setup must go through the common predicate rather
	// than only the WebUI capability flags.
	if err := s.syncSessionTools(sess, false); err != nil {
		return nil, err
	}

	if err := s.pool.Put(sess); err != nil {
		return nil, err
	}

	// If this session was created for the standard endpoint's internal default,
	// remember it so subsequent requests for the same work directory reuse it.
	if sessionID == "" {
		s.mu.Lock()
		if s.defaultSessionIDs == nil {
			s.defaultSessionIDs = make(map[string]string)
		}
		if s.defaultSessionIDs[workDir] == "" {
			s.defaultSessionIDs[workDir] = sess.ID
		}
		s.mu.Unlock()
	}

	return sess, nil
}

// bindSessionRuntime attaches adapter session identity to its shared runtime.
// Resource construction intentionally happens before the persisted Manager is
// known in some recovery paths, so this binding is centralized here.
func bindSessionRuntime(sess *APISession) error {
	if sess == nil || sess.Runtime == nil {
		return nil
	}
	if err := sess.Runtime.BindSession(sess.Manager, agentruntime.SourceWebUI); err != nil {
		return err
	}
	if execution := sess.executionRuntime(); execution != nil {
		sess.Runtime.SetExecution(execution)
	}
	if sess.Decisions != nil {
		sess.Runtime.SetDecisions(sess.Decisions)
	}
	return nil
}

// validatePersistedSessionWorkDir applies the current policy when restoring a
// session. The configured default remains trusted even when overrides are
// disabled, preserving the documented default-workdir behavior.
func (s *Server) validatePersistedSessionWorkDir(workDir string) error {
	if sameWorkDir(workDir, s.cfg.GetWorkDir()) {
		return nil
	}
	return s.cfg.ValidateWorkDir(workDir)
}

type sessionResources struct {
	runtime      *agentruntime.SessionRuntime
	registry     *tools.Registry
	sandboxMgr   *sandbox.Manager
	mcpClients   []*mcp.Client
	skillsMgr    *skills.Manager
	extraContext string
	ruleContent  string
}

func (s *Server) buildSessionResources(workDir string) (*sessionResources, error) {
	// Serve supplies its effective sandbox level; the front-end-neutral builder
	// owns all shared context, skills, registry and MCP construction.
	level := sandbox.LevelNone
	if s.sandboxMgr != nil {
		level = s.sandboxMgr.GetActive().Level()
	}
	runtime, err := (agentruntime.Builder{Settings: s.settings, SandboxLevel: level}).Build(context.Background(), agentruntime.BuildOptions{
		Source:          agentruntime.SourceWebUI,
		WorkDir:         workDir,
		Workflows:       s.cfg.EnableWorkflows,
		Browser:         s.cfg.EnableBrowser,
		ArtifactEnabled: s.cfg.EnableArtifact,
		RegistryHooks: []agentruntime.RegistryHook{func(runtime *agentruntime.SessionRuntime) error {
			return s.registerA2AMasterTool(runtime.Registry)
		}},
	})
	if err != nil {
		return nil, err
	}
	return &sessionResources{
		runtime:      runtime,
		registry:     runtime.Registry,
		sandboxMgr:   runtime.SandboxMgr,
		mcpClients:   runtime.MCPClients,
		skillsMgr:    runtime.SkillsMgr,
		extraContext: runtime.ExtraContext,
		ruleContent:  runtime.RuleContent,
	}, nil
}

func (s *Server) applySessionToolOptions(sess *APISession, opts *SessionToolOptions, runID string) error {
	if sess == nil {
		return nil
	}
	before := capabilitySnapshotFromSession(sess)
	browserChanged := false
	workflowsChanged := false
	if opts != nil {
		applyBoolOption(&sess.WebSearch, opts.WebSearch)
		browserChanged = applyBoolOption(&sess.Browser, opts.Browser)
		applyBoolOption(&sess.A2AMaster, opts.A2AMaster)
		applyBoolOption(&sess.DelegateMode, opts.Delegate)
		applyBoolOption(&sess.MultiAgent, opts.MultiAgent)
		workflowsChanged = applyBoolOption(&sess.Workflows, opts.Workflows)
	}
	if err := s.syncSessionTools(sess, browserChanged || workflowsChanged); err != nil {
		return err
	}
	if opts != nil {
		return s.persistSessionCapabilitiesWithEvents(sess, before, "run_tools", "webui", runID, map[string]any{
			"source": "run_submit",
		})
	}
	return nil
}

func applyBoolOption(dst *bool, src *bool) bool {
	if src == nil || dst == nil || *dst == *src {
		return false
	}
	*dst = *src
	return true
}

func (s *Server) syncSessionTools(sess *APISession, refreshContext bool) error {
	if sess == nil || sess.Registry == nil {
		return nil
	}
	s.registerCronTool(sess)

	if refreshContext {
		if err := s.refreshSessionContext(sess); err != nil {
			return err
		}
	}

	if sess.Runtime != nil {
		sess.Runtime.SynchronizeCoreTools(sess.Browser)
	}

	if sess.A2AMaster {
		if err := s.registerA2ADispatchTool(sess.Registry); err != nil {
			return err
		}
	} else {
		sess.Registry.Remove("a2a_dispatch")
	}

	if agentruntime.SubAgentToolsEnabled(sess.Runtime, sess.MultiAgent) || sess.DelegateMode || sess.Workflows {
		if sess.AgentMgr == nil {
			sess.AgentMgr = s.newAgentManagerForSession(sess)
		}
	} else {
		sess.AgentMgr = nil
	}

	if agentruntime.SubAgentToolsEnabled(sess.Runtime, sess.MultiAgent) && sess.AgentMgr != nil {
		agent.RegisterSubAgentTools(sess.Registry, sess.AgentMgr)
	} else {
		removeSubAgentTools(sess.Registry)
	}

	if sess.DelegateMode && sess.AgentMgr != nil {
		agent.RegisterDelegateSubAgentTool(sess.Registry, sess.AgentMgr)
	} else {
		sess.Registry.Remove("delegate_subagent")
	}
	if sess.Workflows && sess.AgentMgr != nil {
		workflow.RegisterTools(sess.Registry, sess.AgentMgr, nil)
	} else {
		removeWorkflowTools(sess.Registry)
	}

	return nil
}

func (s *Server) registerCronTool(sess *APISession) {
	if sess == nil || sess.Registry == nil {
		return
	}
	if s == nil || s.cronStore == nil {
		sess.Registry.Remove("cron")
		return
	}
	sess.Registry.Register(cron.NewCronTool(cron.NewSessionScopedStoreWithWorkDir(s.cronStore, sess.ID, sess.WorkDir), s.cronScheduler))
}

func removeSubAgentTools(registry *tools.Registry) {
	if registry == nil {
		return
	}
	for _, name := range agent.SubAgentToolNames() {
		registry.Remove(name)
	}
}

func removeWorkflowTools(registry *tools.Registry) {
	if registry == nil {
		return
	}
	for _, name := range []string{"workflow_lint", "workflow_run", "workflow_status", "workflow_cancel"} {
		registry.Remove(name)
	}
}

func (s *Server) refreshSessionContext(sess *APISession) error {
	if sess == nil {
		return nil
	}
	if sess.Runtime == nil {
		// Compatibility for test fixtures and adapters not yet migrated to the
		// builder. All production OpenAI API sessions have Runtime set at open.
		sess.Runtime = &agentruntime.SessionRuntime{
			ID: sess.ID, Source: agentruntime.SourceWebUI, EntrySource: agentruntime.SourceWebUI,
			WorkDir: sess.WorkDir, Manager: sess.Manager, Registry: sess.Registry,
			SandboxMgr: sess.SandboxMgr, SkillsMgr: sess.SkillsMgr, MCPClients: sess.MCPClients,
			ExtraContext: sess.ExtraContext, RuleContent: sess.RuleContent,
			ArtifactEnabled: s.cfg != nil && s.cfg.EnableArtifact,
		}
		if sess.Manager != nil {
			if err := sess.Runtime.BindSession(sess.Manager, agentruntime.SourceWebUI); err != nil {
				return err
			}
		}
	}
	if err := sess.Runtime.RefreshResources(s.settings, agentruntime.RefreshOptions{
		Workflows: sess.Workflows, Browser: sess.Browser, ActiveSkills: sess.ActiveSkills,
	}); err != nil {
		return err
	}
	sess.SkillsMgr = sess.Runtime.SkillsMgr
	sess.ExtraContext = sess.Runtime.ExtraContext
	sess.RuleContent = sess.Runtime.RuleContent
	if sess.AgentMgr != nil {
		sess.AgentMgr = s.newAgentManagerForSession(sess)
		// Re-register sub-agent/delegate/workflow tools with the new manager so
		// tool instances reference the current AgentMgr. Without this, tools
		// created by syncSessionTools keep pointing at the old manager while the
		// parent agent is registered into the new one, causing "parent agent not
		// found" errors when sub-agents are spawned.
		if agentruntime.SubAgentToolsEnabled(sess.Runtime, sess.MultiAgent) && sess.AgentMgr != nil {
			agent.RegisterSubAgentTools(sess.Registry, sess.AgentMgr)
		}
		if sess.DelegateMode && sess.AgentMgr != nil {
			agent.RegisterDelegateSubAgentTool(sess.Registry, sess.AgentMgr)
		}
		if sess.Workflows && sess.AgentMgr != nil {
			workflow.RegisterTools(sess.Registry, sess.AgentMgr, nil)
		}
	}
	return nil
}

func (s *Server) settingsForSession(sess *APISession) *config.Settings {
	if s.settings == nil || sess == nil {
		return s.settings
	}
	runtimeSettings := *s.settings
	runtimeSettings.WebSearch.Enabled = config.BoolPtr(sess.WebSearch)
	return &runtimeSettings
}

func (s *Server) registerA2AMasterTool(registry *tools.Registry) error {
	if !s.cfg.EnableA2AMaster {
		return nil
	}
	return s.registerA2ADispatchTool(registry)
}

func (s *Server) registerA2ADispatchTool(registry *tools.Registry) error {
	a2aListPath := a2a.ProjectAgentListConfigPath()
	if _, err := os.Stat(a2aListPath); err != nil {
		a2aListPath = a2a.AgentListConfigPath()
	}
	a2aListCfg, err := a2a.LoadAgentList(a2aListPath)
	if err != nil {
		return fmt.Errorf("load a2a-list.json: %w", err)
	}
	a2aMgr := a2a.NewA2AManager(a2aListCfg)
	registry.Register(tools.NewA2ADispatchTool(&a2aDispatcherAdapter{mgr: a2aMgr}))
	return nil
}

type a2aDispatcherAdapter struct {
	mgr *a2a.A2AManager
}

func (a *a2aDispatcherAdapter) List() []tools.AgentEntry {
	entries := a.mgr.List()
	result := make([]tools.AgentEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, tools.AgentEntry{Name: e.Name, URL: e.URL})
	}
	return result
}

func (a *a2aDispatcherAdapter) Dispatch(ctx context.Context, name, message string) (string, error) {
	return a.mgr.Dispatch(ctx, name, message)
}

func (s *Server) clearSession(sess *APISession, workDir string) error {
	if sess == nil {
		return fmt.Errorf("no active session to clear")
	}
	sessionDir := s.settings.GetSessionDir()
	if sess.Manager == nil {
		return fmt.Errorf("current session is not initialized")
	}
	if sess.Manager.GetHeader() != nil && sess.Manager.GetHeader().Cwd != "" {
		workDir = sess.Manager.GetHeader().Cwd
	}
	if err := session.DeleteSession(sess.Manager.GetFile(), sessionDir); err != nil {
		return fmt.Errorf("delete current session: %w", err)
	}
	newMgr, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir, ID: sess.ID})
	if err != nil {
		return fmt.Errorf("create fresh session: %w", err)
	}
	sess.Manager = newMgr
	sess.WorkDir = workDir
	sess.Touch()
	sess.ForceCompact = false
	s.mu.Lock()
	if s.defaultSessionIDs == nil {
		s.defaultSessionIDs = make(map[string]string)
	}
	s.defaultSessionIDs[workDir] = sess.ID
	s.mu.Unlock()
	return nil
}

// parseMessages extracts the last user message, system messages, and history messages.
func parseMessages(msgs []RequestMessage) (lastUser RequestMessage, systemMsgs []string, history []RequestMessage) {
	for _, m := range msgs {
		switch m.Role {
		case "system":
			systemMsgs = append(systemMsgs, m.Content)
		}
	}

	// Find the last user message
	lastIdx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			lastIdx = i
			break
		}
	}
	if lastIdx < 0 {
		return RequestMessage{}, systemMsgs, nil
	}
	lastUser = msgs[lastIdx]

	// Everything before the last user message (excluding system) is history
	for i := 0; i < lastIdx; i++ {
		if msgs[i].Role != "system" {
			history = append(history, msgs[i])
		}
	}
	return lastUser, systemMsgs, history
}

// requestRunInput decodes the OpenAI-compatible transport envelope into
// Runtime-neutral text and authenticated one-shot byte streams. It never
// creates provider content; SessionRuntime.BuildUserMessage is the only
// conversion from these inputs to a provider.Message.
func requestRunInput(m RequestMessage) (agentruntime.RunInput, []agentruntime.InputIngress, error) {
	if len(m.ContentParts) == 0 {
		return agentruntime.RunInput{Text: m.Content}, nil, nil
	}
	text := strings.TrimSpace(m.Content)
	textParts := make([]string, 0, len(m.ContentParts))
	ingresses := make([]agentruntime.InputIngress, 0, len(m.ContentParts))
	for index, part := range m.ContentParts {
		switch part.Type {
		case "text":
			if strings.TrimSpace(part.Text) != "" {
				textParts = append(textParts, part.Text)
			}
		case "image_url":
			if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
				return agentruntime.RunInput{}, nil, fmt.Errorf("image_url content part is missing url")
			}
			mediaType, data, err := decodeRequestImageDataURL(part.ImageURL.URL)
			if err != nil {
				return agentruntime.RunInput{}, nil, err
			}
			ingresses = append(ingresses, requestImageIngress(index, mediaType, data))
		case "image":
			if part.Image == nil || part.Image.Data == "" || part.Image.MimeType == "" {
				return agentruntime.RunInput{}, nil, fmt.Errorf("image content part is missing data or mimeType")
			}
			if err := validateImagePayload(part.Image.MimeType, part.Image.Data); err != nil {
				return agentruntime.RunInput{}, nil, err
			}
			data, err := base64.StdEncoding.DecodeString(part.Image.Data)
			if err != nil {
				return agentruntime.RunInput{}, nil, fmt.Errorf("decode image content: %w", err)
			}
			ingresses = append(ingresses, requestImageIngress(index, part.Image.MimeType, data))
		default:
			return agentruntime.RunInput{}, nil, fmt.Errorf("unsupported content part type %q", part.Type)
		}
	}
	if text == "" {
		text = strings.Join(textParts, "\n")
	}
	return agentruntime.RunInput{Text: text}, ingresses, nil
}

func requestImageIngress(index int, mediaType string, data []byte) agentruntime.InputIngress {
	filename := fmt.Sprintf("image-%d", index+1)
	if strings.EqualFold(mediaType, "image/jpeg") {
		filename += ".jpg"
	} else if suffix := strings.TrimPrefix(strings.ToLower(mediaType), "image/"); suffix != "" {
		filename += "." + suffix
	}
	return agentruntime.InputIngress{
		Origin: "api:chat-completions", ItemIndex: index, Reference: "inline-image", Kind: agentruntime.AttachmentImage,
		FilenameHint: filename, MediaTypeHint: mediaType, SizeHint: int64(len(data)),
		Open: func(context.Context) (agentruntime.InputStream, error) {
			return agentruntime.InputStream{Reader: io.NopCloser(bytes.NewReader(data)), Filename: filename, MediaType: mediaType, ContentSize: int64(len(data))}, nil
		},
	}
}

func decodeRequestImageDataURL(dataURL string) (string, []byte, error) {
	const marker = ";base64,"
	if !strings.HasPrefix(dataURL, "data:image/") {
		return "", nil, fmt.Errorf("image_url must be a data:image URL")
	}
	idx := strings.Index(dataURL, marker)
	if idx < 0 {
		return "", nil, fmt.Errorf("image_url must contain base64 image data")
	}
	mediaType := dataURL[len("data:"):idx]
	encoded := dataURL[idx+len(marker):]
	if err := validateImagePayload(mediaType, encoded); err != nil {
		return "", nil, err
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("decode image_url data: %w", err)
	}
	return mediaType, data, nil
}

func validateImagePayload(mimeType, data string) error {
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return fmt.Errorf("unsupported image MIME type %q", mimeType)
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return fmt.Errorf("invalid base64 image data: %w", err)
	}
	return nil
}

// convertHistoryMessages converts OpenAI-format history to internal provider.Message.
func convertHistoryMessages(msgs []RequestMessage) []provider.Message {
	result := make([]provider.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			text := strings.TrimSpace(m.Content)
			if text == "" {
				parts := make([]string, 0, len(m.ContentParts))
				for _, part := range m.ContentParts {
					if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
						parts = append(parts, part.Text)
					}
				}
				text = strings.Join(parts, "\n")
			}
			if text != "" {
				result = append(result, provider.NewUserMessage(text))
			}
		case "assistant":
			result = append(result, provider.NewAssistantMessage([]provider.ContentBlock{
				{Type: "text", Text: m.Content},
			}))
		}
	}
	return result
}

// resolveToolEvent extracts tool name and call ID from an agent event,
// falling back to ToolCall fields when top-level fields are empty.
func resolveToolEvent(ev agent.Event) (name string, callID string) {
	name = ev.ToolName
	callID = ev.ToolCallID
	if ev.ToolCall != nil {
		if name == "" {
			name = ev.ToolCall.Name
		}
		if callID == "" {
			callID = ev.ToolCall.ID
		}
	}
	return name, callID
}

// modelIDs returns a comma-separated list of model IDs for error messages.
func modelIDs(models []*provider.Model) string {
	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return strings.Join(ids, ", ")
}

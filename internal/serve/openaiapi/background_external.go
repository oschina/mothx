package openaiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	serviceruntime "github.com/oschina/mothx/internal/serve/runtime"
	"github.com/oschina/mothx/internal/session"
)

// SubmitExternalResponsesBackground hands an external runtime message to the
// same durable coordinator used by WebUI. The caller does not retain a
// session/runtime lock while the background run executes.
func (s *Server) SubmitExternalResponsesBackground(req serviceruntime.BackgroundRequest) (string, error) {
	if s == nil || s.pool == nil || !s.responsesBackgroundEnabled() {
		return "", fmt.Errorf("Responses background runtime is unavailable")
	}
	if strings.TrimSpace(req.SessionID) == "" || (strings.TrimSpace(req.Input.Text) == "" && len(req.Input.Resources) == 0) {
		return "", fmt.Errorf("session ID and message are required")
	}
	if len(strings.TrimSpace(req.IdempotencyKey)) > 256 {
		return "", fmt.Errorf("Idempotency-Key is too long")
	}
	idempotencyScope := strings.TrimSpace(req.IdempotencyScope)
	if idempotencyScope == "" {
		idempotencyScope = "external"
	}
	requestFP := requestFingerprint(struct {
		Platform string                `json:"platform"`
		ModelID  string                `json:"model"`
		Mode     string                `json:"mode"`
		Input    agentruntime.RunInput `json:"input"`
	}{req.Platform, req.ModelID, req.Mode, req.Input})
	workDir := req.WorkDir
	if workDir == "" {
		workDir = s.cfg.GetWorkDir()
	}
	sess, err := s.getOrCreateSession(req.SessionID, workDir)
	if err != nil {
		return "", err
	}
	if sess == nil {
		return "", fmt.Errorf("session pool is at capacity")
	}
	if existing, err := findIdempotentRun(s.settings.GetSessionDir(), sess.ID, req.IdempotencyKey, requestFP, idempotencyScope); err != nil {
		return "", err
	} else if existing != nil {
		return existing.ID, nil
	}
	if !s.pool.Pin(sess) {
		return "", fmt.Errorf("session pool is at capacity")
	}
	unpin := true
	defer func() {
		if unpin {
			s.pool.Unpin(sess)
		}
	}()

	runtimeGuard, err := agentruntime.AcquireExecutionAdmission(req.Context, s.settings.GetSessionDir(), sess.ID, agentruntime.ExecutionAdmissionOptions{})
	if err != nil {
		return "", fmt.Errorf("session cannot start background run: %w", err)
	}
	runtimeRelease := runtimeGuard.Release
	if !sess.TryLock() {
		runtimeRelease()
		return "", fmt.Errorf("session already has an active run")
	}
	if err := sess.Manager.Reload(); err != nil {
		sess.Unlock()
		runtimeRelease()
		return "", fmt.Errorf("reload session before background run: %w", err)
	}
	// Repeat the idempotency lookup under the session/runtime admission locks.
	// The pre-lock lookup is only a fast path; this check closes the concurrent
	// duplicate-submit window while the durable submission table is pending.
	if existing, err := findIdempotentRun(s.settings.GetSessionDir(), sess.ID, req.IdempotencyKey, requestFP, idempotencyScope); err != nil {
		sess.Unlock()
		runtimeRelease()
		return "", err
	} else if existing != nil {
		sess.Unlock()
		runtimeRelease()
		return existing.ID, nil
	}

	s.mu.RLock()
	model := s.model
	currentProvider := s.provider
	s.mu.RUnlock()
	if currentProvider == nil || model == nil {
		sess.Unlock()
		runtimeRelease()
		return "", fmt.Errorf("provider and model are required")
	}
	if req.ModelID != "" && req.ModelID != "default" {
		if selected := currentProvider.GetModel(req.ModelID); selected != nil {
			model = selected
		}
	}
	model = cloneModel(model)
	if req.Temperature != nil {
		model.Temperature = req.Temperature
	}
	if req.TopP != nil {
		model.TopP = req.TopP
	}
	resolution, mode, err := s.resolveSessionPolicy(sess, strings.TrimSpace(req.Mode))
	if err != nil {
		sess.Unlock()
		runtimeRelease()
		return "", err
	}
	runSource := "channel:" + req.Platform
	if resolution.Source != agentruntime.SourceUnknown {
		runSource = string(resolution.Source)
	}
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		runID = newRunID()
	}
	message, err := sess.Runtime.BuildUserMessage(req.Context, req.Input)
	if err != nil {
		sess.Unlock()
		runtimeRelease()
		return "", fmt.Errorf("build Runtime user message: %w", err)
	}
	now := time.Now()
	requestSnapshot, snapshotErr := json.Marshal(map[string]any{
		"platform": req.Platform, "model": req.ModelID, "mode": req.Mode, "input": req.Input,
		"systemPrompt": req.SystemPrompt, "maxTokens": req.MaxTokens,
	})
	if snapshotErr != nil {
		sess.Unlock()
		runtimeRelease()
		return "", snapshotErr
	}
	policySnapshot, snapshotErr := marshalRunPolicySnapshot(s, sess, submitRunRequest{Message: req.Input.Text, Model: req.ModelID, Mode: mode, WorkDir: workDir}, runSource, mode)
	if snapshotErr != nil {
		sess.Unlock()
		runtimeRelease()
		return "", snapshotErr
	}
	intent := agentruntime.ExecutionIntent{ID: newExecutionIntentID(), SessionID: sess.ID, Source: runSource, Model: model.ID, Mode: mode, WorkDir: sess.WorkDir, RequestFingerprint: requestFP, Request: requestSnapshot, Policy: policySnapshot, CreatedAt: now}
	execution := sess.ensureExecution()
	execution.SetRunStore(agentruntime.RunStore{SessionDir: s.settings.GetSessionDir()})
	execution.SetEventSink(s.runtimeRunEventSink(sess))
	if sess.Runtime != nil {
		sess.Runtime.SetExecution(execution)
	}
	sess.beginRunBookkeeping(runID)
	if _, err := execution.BeginIntentDurable(context.Background(), intent, agentruntime.DurableRun{
		ID: runID, SessionID: sess.ID, IntentID: intent.ID, Attempt: 1, WorkDir: sess.WorkDir,
		Source: runSource, Model: model.ID, Mode: mode,
		InputResourceIDs: req.Input.ResourceIDs(), SubmissionKeyHash: idempotencyKeyFingerprint(req.IdempotencyKey),
		SubmissionScope: idempotencyScope, SubmissionFingerprint: requestFP,
		UserEntryID: session.RunUserEntryID(runID), UserMessage: &message,
		Status: "queued", StartedAt: now, ConversationTurnID: "turn-" + intent.ID, ConversationTurn: true,
	}, agentruntime.RunEvent{SessionID: sess.ID, RunID: runID, EventType: "started", Source: runSource, Status: "queued", Model: model.ID, Mode: mode, Timestamp: now, Data: rawEventData(map[string]any{
		"source": "channel", "idempotencyKeyHash": idempotencyKeyFingerprint(req.IdempotencyKey), "idempotencyScope": idempotencyScope, "requestFingerprint": requestFP, "intentId": intent.ID, "attempt": 1,
	})}); err != nil {
		sess.finishRun(runID)
		sess.Unlock()
		runtimeRelease()
		return "", err
	}
	// BeginIntentDurable atomically writes the turn boundary and the run's
	// user entry. Reload the shared manager so the background coordinator
	// sees the admitted user entry in its replay state and reuses it instead
	// of appending a duplicate.
	if err := sess.Manager.Reload(); err != nil {
		sess.finishRun(runID)
		sess.Unlock()
		runtimeRelease()
		return "", fmt.Errorf("reload session after background admission: %w", err)
	}
	sess.markDurableRun(runID)
	if s.runManager != nil {
		_ = s.runManager.Register(session.SessionRun{ID: runID, SessionID: sess.ID, IntentID: intent.ID, Attempt: 1})
	}

	agentOpts := s.buildAgentOptionsForSession(sess, model, mode)
	agentOpts.IntentID = intent.ID
	agentOpts.RunID = runID
	agentOpts.ConversationTurnID = "turn-" + intent.ID
	agentOpts.ConversationTurn = true
	agentOpts.RuntimeOwnsTurnEnd = true
	if strings.TrimSpace(req.SystemPrompt) != "" {
		agentOpts.ExtraContext += "\n## Client Instructions\n" + strings.TrimSpace(req.SystemPrompt)
	}
	if req.MaxTokens > 0 {
		agentOpts.MaxTokens = req.MaxTokens
		agentOpts.MaxTokensSet = true
	}
	artifacts, err := sess.Runtime.BeginArtifactCollection(runID)
	if err != nil {
		_ = execution.FinishDurableWithRetry(context.Background(), runID, agentruntime.RunStateFailed, err.Error(), agentruntime.RunEvent{
			SessionID: sess.ID, RunID: runID, EventType: "failed", Source: runSource, Status: "failed",
			Model: model.ID, Mode: mode, Timestamp: time.Now(),
		})
		sess.finishRun(runID)
		sess.Unlock()
		runtimeRelease()
		return "", fmt.Errorf("begin Runtime artifact collection: %w", err)
	}
	// Keep the pin until the coordinator has released the session/runtime locks.
	unpin = false
	release := func() {
		runtimeRelease()
		s.pool.Unpin(sess)
	}
	var onComplete func(string, []provider.Attachment, error)
	if req.Progress != nil {
		onComplete = func(response string, attachments []provider.Attachment, runErr error) {
			if runErr != nil {
				req.Progress("Responses background run failed: " + safeAgentErrorMessage(runErr))
				return
			}
			if summary := serviceruntime.FormatAttachmentSummary(attachments); summary != "" {
				if strings.TrimSpace(response) != "" {
					response += "\n\n"
				}
				response += summary
			}
			if strings.TrimSpace(response) != "" {
				req.Progress(response)
			}
		}
	}
	go func() {
		defer artifacts.Close()
		s.executeResponsesBackgroundRunWithConfig(sess, runID, release, model, mode, message, true, &agentOpts, req.InitialHistory, onComplete, req.Progress)
	}()
	return runID, nil
}

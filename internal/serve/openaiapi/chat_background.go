package openaiapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	serviceruntime "github.com/oschina/mothx/internal/serve/runtime"
)

func (s *Server) submitChatCompletionBackground(w http.ResponseWriter, r *http.Request, req ChatCompletionRequest, workDir string, model *provider.Model, inputSpec agentruntime.RunInput, ingresses []agentruntime.InputIngress, systemMsgs []string, history []RequestMessage) {
	s.mu.RLock()
	sessionID := s.defaultSessionIDs[workDir]
	s.mu.RUnlock()
	sess, err := s.getOrCreateSession(sessionID, workDir)
	if err != nil || sess == nil {
		if err == nil {
			writeError(w, http.StatusServiceUnavailable, "session pool is at capacity", "server_error")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	sessionID = sess.ID
	runID := newRunID()
	input, err := sess.Runtime.AcceptInput(r.Context(), runID, inputSpec.Text, ingresses)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	initialHistory := convertHistoryMessages(history)
	runID, err = s.SubmitExternalResponsesBackground(serviceruntime.BackgroundRequest{
		Context:        r.Context(),
		SessionID:      sessionID,
		WorkDir:        workDir,
		Platform:       "chat-completions",
		ModelID:        req.Model,
		Mode:           "",
		RunID:          runID,
		Input:          input,
		InitialHistory: initialHistory,
		SystemPrompt:   strings.Join(systemMsgs, "\n"),
		Temperature:    req.Temperature,
		TopP:           req.TopP,
		MaxTokens:      req.MaxTokens,
		IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")),
	})
	if err != nil {
		status := http.StatusInternalServerError
		errType := "server_error"
		if errors.Is(err, ErrIdempotencyKeyConflict) {
			status = http.StatusConflict
			errType = "idempotency_conflict"
		} else if strings.Contains(err.Error(), "active run") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error(), errType)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id": runID, "object": "chat.completion", "status": "queued",
		"sessionId": sessionID, "runId": runID,
	})
}

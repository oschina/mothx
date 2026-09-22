package openaiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/session"
)

// HandleRunAPI exposes durable run inspection, cancellation, and linked retry.
// Paths: GET /api/runs/{runID}, POST /api/runs/{runID}/cancel|retry
func (s *Server) HandleRunAPI(w http.ResponseWriter, r *http.Request) {
	// Parse path segments: /api/runs/<runID>[/cancel]
	path := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	path = strings.TrimSuffix(path, "/")
	segments := strings.Split(path, "/")
	if len(segments) < 1 || segments[0] == "" {
		writeErrorInfo(w, http.StatusBadRequest, agentruntime.ErrorInfo{
			Code: "run_id_required", Type: "invalid_request_error", FailureClass: agentruntime.FailureValidation,
			Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.idRequired", Message: "A run ID is required.",
		})
		return
	}
	runID := segments[0]
	isCancel := len(segments) == 2 && segments[1] == "cancel"
	isRetry := len(segments) == 2 && segments[1] == "retry"

	if r.Method == http.MethodGet {
		if isCancel || isRetry {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		run, err := s.GetRun(runID)
		if errors.Is(err, ErrSessionNotFound) {
			writeErrorInfo(w, http.StatusNotFound, runNotFoundError(runID))
			return
		}
		if err != nil {
			writeErrorInfo(w, http.StatusInternalServerError, runAPIStorageError("run_lookup_failed", "run.error.lookupFailed", "The run status could not be loaded.", runID))
			return
		}
		view, viewErr := runAPIResponse(s.settings.GetSessionDir(), run)
		if viewErr != nil {
			writeErrorInfo(w, http.StatusInternalServerError, runAPIStorageError("run_event_cursor_unavailable", "run.error.cursorUnavailable", "The run event position could not be loaded.", runID))
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	if r.Method == http.MethodPost && isCancel {
		run, lookupErr := s.GetRun(runID)
		if errors.Is(lookupErr, ErrSessionNotFound) {
			writeErrorInfo(w, http.StatusNotFound, runNotFoundError(runID))
			return
		}
		if lookupErr != nil || run == nil {
			writeErrorInfo(w, http.StatusInternalServerError, runAPIStorageError("run_lookup_failed", "run.error.lookupFailed", "The run status could not be loaded.", runID))
			return
		}
		result, stopErr := s.requestSessionStop(r.Context(), run.SessionID, runID)
		if stopErr != nil {
			writeErrorInfo(w, http.StatusInternalServerError, runAPIStorageError("run_cancellation_failed", "run.error.cancellationFailed", "The run could not be cancelled.", runID))
			return
		}
		switch result.Code {
		case agentruntime.SessionStopAccepted, agentruntime.SessionStopRemoteAccepted, agentruntime.SessionStopRecoveryStarted:
			// Continue with the canonical Run projection below.
		case agentruntime.SessionStopOwnedElsewhere:
			writeErrorInfo(w, http.StatusConflict, agentruntime.ErrorInfo{Code: string(result.Code), Type: "conflict_error", FailureClass: agentruntime.FailurePolicy, Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.sessionRunOwnedElsewhere", Message: "The run is executing in another process.", RetryMode: agentruntime.RetryUser, Retryable: true, RunID: runID})
			return
		case agentruntime.SessionStopTargetChanged:
			writeErrorInfo(w, http.StatusConflict, agentruntime.ErrorInfo{Code: string(result.Code), Type: "conflict_error", FailureClass: agentruntime.FailurePolicy, Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.targetChanged", Message: "The run is no longer the active run for this session.", RetryMode: agentruntime.RetryUser, Retryable: true, RunID: runID})
			return
		case agentruntime.SessionStopNoActiveRun:
			writeErrorInfo(w, http.StatusNotFound, runNotFoundError(runID))
			return
		case agentruntime.SessionStopReserved, agentruntime.SessionStopRemoteUnsupported:
			writeErrorInfo(w, http.StatusConflict, agentruntime.ErrorInfo{Code: string(result.Code), Type: "conflict_error", FailureClass: agentruntime.FailurePolicy, Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.cancellationRejected", Message: "The run cannot be cancelled from the current execution state.", RetryMode: agentruntime.RetryUser, Retryable: true, RunID: runID})
			return
		default:
			writeErrorInfo(w, http.StatusServiceUnavailable, runAPIStorageError("run_cancellation_unavailable", "run.error.cancellationFailed", "The run cancellation state is temporarily unavailable.", runID))
			return
		}
		run, err := s.GetRun(runID)
		if err != nil {
			if errors.Is(err, ErrSessionNotFound) {
				writeErrorInfo(w, http.StatusNotFound, runNotFoundError(runID))
				return
			}
			writeErrorInfo(w, http.StatusInternalServerError, runAPIStorageError("run_lookup_failed", "run.error.lookupFailed", "The run status could not be loaded.", runID))
			return
		}
		view, viewErr := runAPIResponse(s.settings.GetSessionDir(), run)
		if viewErr != nil {
			writeErrorInfo(w, http.StatusInternalServerError, runAPIStorageError("run_event_cursor_unavailable", "run.error.cursorUnavailable", "The run event position could not be loaded.", runID))
			return
		}
		writeJSON(w, http.StatusAccepted, view)
		return
	}
	if r.Method == http.MethodPost && isRetry {
		s.HandleRetryRun(w, r, runID)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

type runAPIView struct {
	ID           string                  `json:"id"`
	SessionID    string                  `json:"sessionId"`
	IntentID     string                  `json:"intentId,omitempty"`
	RetryOf      string                  `json:"retryOf,omitempty"`
	Attempt      int                     `json:"attempt"`
	WorkDir      string                  `json:"workDir,omitempty"`
	Source       string                  `json:"source,omitempty"`
	Model        string                  `json:"model,omitempty"`
	Mode         string                  `json:"mode,omitempty"`
	Status       string                  `json:"status"`
	StartedAt    string                  `json:"startedAt,omitempty"`
	UpdatedAt    string                  `json:"updatedAt,omitempty"`
	FinishedAt   string                  `json:"finishedAt,omitempty"`
	Error        string                  `json:"error,omitempty"`
	ErrorInfo    *agentruntime.ErrorInfo `json:"errorInfo,omitempty"`
	Progress     *agentruntime.RetryInfo `json:"progress,omitempty"`
	Usage        json.RawMessage         `json:"usage,omitempty"`
	ContextUsage json.RawMessage         `json:"contextUsage,omitempty"`
	LastEventSeq int64                   `json:"lastEventSeq,omitempty"`
}

func runAPIResponse(sessionDir string, run *session.SessionRun) (runAPIView, error) {
	if run == nil {
		return runAPIView{}, nil
	}
	view := runAPIView{
		ID: run.ID, SessionID: run.SessionID, IntentID: run.IntentID, RetryOf: run.RetryOf, Attempt: run.Attempt,
		WorkDir: run.WorkDir, Source: run.Source, Model: run.Model, Mode: run.Mode, Status: run.Status,
		StartedAt: run.StartedAt.Format(time.RFC3339Nano), UpdatedAt: run.UpdatedAt.Format(time.RFC3339Nano), Usage: run.Usage, ContextUsage: run.ContextUsage,
	}
	if view.Attempt <= 0 {
		view.Attempt = 1
	}
	if run.FinishedAt != nil {
		view.FinishedAt = run.FinishedAt.Format(time.RFC3339Nano)
	}
	var info agentruntime.ErrorInfo
	if len(run.ErrorInfo) > 0 && json.Unmarshal(run.ErrorInfo, &info) == nil && info.Code != "" {
		view.ErrorInfo = &info
		view.Error = agentruntime.DisplayErrorMessage(info)
	} else if run.Error != "" {
		info = retryErrorInfo(run)
		view.ErrorInfo = &info
		view.Error = agentruntime.DisplayErrorMessage(info)
	}
	var progress agentruntime.RetryInfo
	if len(run.Progress) > 0 && json.Unmarshal(run.Progress, &progress) == nil && progress.Attempt > 0 {
		view.Progress = &progress
	}
	seq, err := session.LatestSessionRunEventSeq(sessionDir, run.ID)
	if err != nil {
		return runAPIView{}, err
	}
	view.LastEventSeq = seq
	return view, nil
}

type retryRunRequest struct {
	ConfirmSideEffects bool `json:"confirmSideEffects"`
}

// HandleRetryRun creates one new linked attempt from a terminal durable Run.
// The stored intent is authoritative; clients cannot replace its prompt or
// execution policy through this endpoint.
func (s *Server) HandleRetryRun(w http.ResponseWriter, r *http.Request, runID string) {
	if s == nil || s.settings == nil || r == nil {
		writeErrorInfo(w, http.StatusServiceUnavailable, agentruntime.ErrorInfo{
			Code: "server_unavailable", Type: "server_error", FailureClass: agentruntime.FailurePersistence,
			Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.serverUnavailable", Message: "The run service is not ready.",
			RetryMode: agentruntime.RetryReconcile, Retryable: true,
		})
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeErrorInfo(w, http.StatusBadRequest, agentruntime.ErrorInfo{
			Code: "idempotency_key_required", Type: "invalid_request_error", FailureClass: agentruntime.FailureValidation,
			Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.idempotencyKeyRequired", Message: "An idempotency key is required to retry a run.",
		})
		return
	}
	var req retryRunRequest
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeErrorInfo(w, http.StatusBadRequest, agentruntime.ErrorInfo{
				Code: "invalid_retry_request", Type: "invalid_request_error", FailureClass: agentruntime.FailureValidation,
				Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.invalidRetryRequest", Message: "The retry request is invalid.",
			})
			return
		}
	}
	run, err := s.GetRun(runID)
	if errors.Is(err, ErrSessionNotFound) {
		writeErrorInfo(w, http.StatusNotFound, runNotFoundError(runID))
		return
	}
	if err != nil || run == nil {
		writeErrorInfo(w, http.StatusInternalServerError, runAPIStorageError("run_lookup_failed", "run.error.lookupFailed", "The run status could not be loaded.", runID))
		return
	}
	if !retryableRunStatus(run.Status) {
		writeErrorInfo(w, http.StatusConflict, agentruntime.ErrorInfo{
			Code: "run_not_retryable", Type: "conflict_error", FailureClass: agentruntime.FailurePolicy,
			Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.notRetryable", Message: "Only a finished unsuccessful run can be retried.",
			RunID: run.ID, IntentID: run.IntentID,
		})
		return
	}
	if run.IntentID == "" {
		writeErrorInfo(w, http.StatusConflict, agentruntime.ErrorInfo{
			Code: "retry_unavailable", Type: "conflict_error", FailureClass: agentruntime.FailurePersistence,
			Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.retryUnavailable", Message: "This older run does not have a recoverable request.",
			RunID: run.ID,
		})
		return
	}
	// Repeated retry commands must reconcile their scoped idempotency key before
	// the stale-attempt guard. Once the first retry exists, latest will point at
	// that newer Run; returning it is correct, while creating another attempt is
	// not.
	if existing, lookupErr := findIdempotentRun(s.settings.GetSessionDir(), run.SessionID, key, "", retryIdempotencyScope(run.IntentID, run.ID)); lookupErr != nil {
		if errors.Is(lookupErr, ErrIdempotencyRunMissing) {
			writeErrorInfo(w, http.StatusServiceUnavailable, agentruntime.ErrorInfo{
				Code: "submission_unknown", Type: "transport_error", FailureClass: agentruntime.FailureTransport,
				Phase: agentruntime.PhaseTransport, MessageKey: "run.error.submissionUnknown",
				Message: "The retry was accepted but its Run status is temporarily unavailable.", RetryMode: agentruntime.RetryReconcile, Retryable: true,
				RunID: run.ID, IntentID: run.IntentID,
			})
			return
		}
		if errors.Is(lookupErr, ErrIdempotencyKeyConflict) {
			writeErrorInfo(w, http.StatusConflict, agentruntime.ErrorInfo{
				Code: "idempotency_conflict", Type: "idempotency_conflict", FailureClass: agentruntime.FailurePolicy,
				Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.idempotencyConflict",
				Message: "The idempotency key conflicts with an existing retry.", RetryMode: agentruntime.RetryNone,
				RunID: run.ID, IntentID: run.IntentID,
			})
			return
		}
		writeErrorInfo(w, http.StatusInternalServerError, runAPIStorageError("run_retry_lookup_failed", "run.error.lookupFailed", "The retry history could not be loaded.", run.ID))
		return
	} else if existing != nil {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"sessionId": existing.SessionID, "runId": existing.ID, "status": existing.Status,
			"intentId": existing.IntentID, "attempt": existing.Attempt, "idempotent": true,
		})
		return
	}
	latest, latestErr := session.LatestSessionRunForIntent(s.settings.GetSessionDir(), run.SessionID, run.IntentID)
	if latestErr != nil {
		writeErrorInfo(w, http.StatusInternalServerError, runAPIStorageError("run_retry_lookup_failed", "run.error.lookupFailed", "The retry history could not be loaded.", run.ID))
		return
	}
	if latest != nil && latest.ID != run.ID {
		writeErrorInfo(w, http.StatusConflict, agentruntime.ErrorInfo{
			Code: "run_retry_stale", Type: "conflict_error", FailureClass: agentruntime.FailurePolicy,
			Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.retryStale",
			Message: "A newer attempt already exists for this request.", RetryMode: agentruntime.RetryUser,
			Retryable: retryableRunStatus(latest.Status), RunID: run.ID, IntentID: run.IntentID,
		})
		return
	}
	info := retryErrorInfo(run)
	if !info.Retryable {
		writeErrorInfo(w, http.StatusConflict, info)
		return
	}
	if info.RetryMode == agentruntime.RetryDecisionRequired && !req.ConfirmSideEffects {
		confirmation := info
		confirmation.Code = "retry_confirmation_required"
		confirmation.Type = "conflict_error"
		confirmation.MessageKey = "run.error.retryConfirmationRequired"
		confirmation.Message = "This run may have changed external state. Confirm before retrying."
		confirmation.Retryable = true
		writeErrorInfo(w, http.StatusConflict, confirmation)
		return
	}
	intentStore := agentruntime.RunStore{SessionDir: s.settings.GetSessionDir()}
	intent, err := intentStore.GetIntent(run.IntentID)
	if err != nil || intent == nil || intent.SessionID != run.SessionID {
		writeErrorInfo(w, http.StatusConflict, agentruntime.ErrorInfo{
			Code: "retry_unavailable", Type: "conflict_error", FailureClass: agentruntime.FailurePersistence,
			Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.retryUnavailable", Message: "The original request is no longer available for retry.",
			RunID: run.ID, IntentID: run.IntentID,
		})
		return
	}
	var stored submitRunRequest
	if err := json.Unmarshal(intent.Request, &stored); err != nil {
		writeErrorInfo(w, http.StatusConflict, agentruntime.ErrorInfo{
			Code: "retry_unavailable", Type: "conflict_error", FailureClass: agentruntime.FailurePersistence,
			Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.retryUnavailable", Message: "The original request could not be restored.",
			RunID: run.ID, IntentID: run.IntentID,
		})
		return
	}

	payload, err := json.Marshal(stored)
	if err != nil {
		writeErrorInfo(w, http.StatusInternalServerError, agentruntime.ErrorInfo{
			Code: "retry_prepare_failed", Type: "server_error", FailureClass: agentruntime.FailurePersistence,
			Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.retryPrepareFailed", Message: "The retry could not be prepared.",
			RetryMode: agentruntime.RetryReconcile, Retryable: true, RunID: run.ID, IntentID: run.IntentID,
		})
		return
	}
	// Re-enter the normal submit handler with a private retry context. This is
	// deliberately a linked admission, not a terminal-to-active transition.
	retryRequest := r.Clone(context.WithValue(r.Context(), retryRunContextKey{}, retryRunContext{
		Intent: *intent, RetryOf: run.ID, MinimumAttempt: max(run.Attempt+1, 2),
	}))
	urlCopy := *retryRequest.URL
	urlCopy.Path = "/api/sessions/" + run.SessionID + "/runs"
	retryRequest.URL = &urlCopy
	retryRequest.RequestURI = urlCopy.RequestURI()
	retryRequest.Method = http.MethodPost
	retryRequest.Body = io.NopCloser(bytes.NewReader(payload))
	retryRequest.ContentLength = int64(len(payload))
	retryRequest.Header = r.Header.Clone()
	retryRequest.Header.Set("Content-Type", "application/json")
	s.HandleSubmitRun(w, retryRequest)
}

func runNotFoundError(runID string) agentruntime.ErrorInfo {
	return agentruntime.ErrorInfo{
		Code: "run_not_found", Type: "not_found", FailureClass: agentruntime.FailureValidation,
		Phase: agentruntime.PhaseAdmission, MessageKey: "run.error.notFound", Message: "The requested run was not found.",
		RetryMode: agentruntime.RetryNone, RunID: runID,
	}
}

func runAPIStorageError(code, messageKey, message, runID string) agentruntime.ErrorInfo {
	return agentruntime.ErrorInfo{
		Code: code, Type: "server_error", FailureClass: agentruntime.FailurePersistence,
		Phase: agentruntime.PhasePersistence, MessageKey: messageKey, Message: message,
		RetryMode: agentruntime.RetryReconcile, Retryable: true, RunID: runID,
	}
}

func retryableRunStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "incomplete", "timed_out", "expired", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func retryErrorInfo(run *session.SessionRun) agentruntime.ErrorInfo {
	if run == nil {
		return agentruntime.ErrorInfo{Code: "retry_unavailable", Type: "conflict_error", RetryMode: agentruntime.RetryNone}
	}
	var info agentruntime.ErrorInfo
	if len(run.ErrorInfo) > 0 && json.Unmarshal(run.ErrorInfo, &info) == nil && info.Code != "" {
		if info.RunID == "" {
			info.RunID = run.ID
		}
		if info.IntentID == "" {
			info.IntentID = run.IntentID
		}
		return info
	}
	var fallbackErr error
	switch strings.ToLower(strings.TrimSpace(run.Status)) {
	case "cancelled", "canceled":
		fallbackErr = context.Canceled
	case "timed_out":
		fallbackErr = context.DeadlineExceeded
	default:
		fallbackErr = errors.New(run.Error)
	}
	info = agentruntime.ClassifyError(fallbackErr, agentruntime.ErrorClassificationOptions{
		Phase: agentruntime.PhaseModel, RunID: run.ID, IntentID: run.IntentID,
		SideEffectState: agentruntime.SideEffectUnknown,
	})
	if info.RetryMode == agentruntime.RetryAutomatic {
		info.RetryMode = agentruntime.RetryDecisionRequired
	}
	return info
}

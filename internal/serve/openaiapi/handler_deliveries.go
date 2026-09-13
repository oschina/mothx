package openaiapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/session"
)

// deliveryRequestBodyLimit caps the retry request body: the endpoint carries a
// single operation id, never a payload.
const deliveryRequestBodyLimit = 4 << 10

// HandleDeliveryFailuresAPI lists the durable delivery operations that need
// operator attention, most recently updated first. The Runtime owns the rows;
// this endpoint only projects them (the same facts ACP exposes to Desktop).
// GET /api/deliveries/failures?session_id={sessionID}&limit={n}
func (s *Server) HandleDeliveryFailuresAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s == nil || s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "server is not ready", "server_error")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "limit must be a non-negative integer", "invalid_request_error")
			return
		}
		limit = parsed
	}
	failures, err := session.ListDeliveryFailures(r.Context(), s.settings.GetSessionDir(), sessionID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	items := make([]map[string]any, 0, len(failures))
	for _, failure := range failures {
		items = append(items, deliveryFailureJSON(failure))
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": items, "count": len(items)})
}

// deliveryFailureJSON mirrors the ACP mothx/manage/deliveries/list projection so
// WebUI and Desktop render the same facts, including the retryable verdict that
// the shared Runtime predicate owns.
func deliveryFailureJSON(failure session.DeliveryFailure) map[string]any {
	return map[string]any{
		"operationId":   failure.OperationID,
		"intentId":      failure.IntentID,
		"sessionId":     failure.SessionID,
		"runId":         failure.RunID,
		"platform":      failure.Platform,
		"targetId":      failure.TargetID,
		"operationKind": failure.OperationKind,
		"status":        failure.Status,
		"failureCode":   failure.FailureCode,
		"attemptCount":  failure.AttemptCount,
		"updatedAt":     failure.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"retryable":     agentruntime.DeliveryFailureRetryable(failure.FailureCode),
	}
}

// HandleDeliveryRetryAPI reopens one failed transport-level delivery operation
// so the Runtime retries it inside a fresh retry window. The same refusal rules
// as the ACP operator entry apply: an unknown, non-failed, or permanently failed
// operation is reported instead of being clobbered back into retry_wait.
// POST /api/deliveries/retry {"operationId":"..."}
func (s *Server) HandleDeliveryRetryAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s == nil || s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "server is not ready", "server_error")
		return
	}
	var in struct {
		OperationID string `json:"operationId"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, deliveryRequestBodyLimit)).Decode(&in); err != nil || strings.TrimSpace(in.OperationID) == "" {
		writeError(w, http.StatusBadRequest, "operationId is required", "invalid_request_error")
		return
	}
	operationID := strings.TrimSpace(in.OperationID)
	sessionDir := s.settings.GetSessionDir()
	operation, err := session.GetDeliveryOperation(r.Context(), sessionDir, operationID)
	if err != nil {
		if errors.Is(err, session.ErrDeliveryOperationAbsent) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("delivery operation %s does not exist", operationID), "not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("delivery operation %s is not readable: %v", operationID, err), "server_error")
		return
	}
	if operation.Status != "failed" {
		writeError(w, http.StatusConflict, fmt.Sprintf("delivery operation %s is %s, only a failed operation can be retried", operationID, operation.Status), "invalid_request_error")
		return
	}
	if !agentruntime.DeliveryFailureRetryable(operation.FailureCode) {
		writeError(w, http.StatusConflict, fmt.Sprintf("delivery operation %s failed permanently (%s)", operationID, operation.FailureCode), "invalid_request_error")
		return
	}
	reopened, err := session.ReopenFailedDeliveryOperation(r.Context(), sessionDir, operationID, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operationId": operationID, "retried": reopened})
}

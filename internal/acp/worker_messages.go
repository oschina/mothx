package acp

import (
	"encoding/json"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
)

// workerMessagesParams is the read-only projection request for one ACP
// session's Router worker messages and pending questions. It exposes no
// mutation surface: answering a worker question remains a lead-only Runtime
// tool action (delegate_worker_mgr action=answer).
type workerMessagesParams struct {
	SessionID string `json:"sessionId"`
}

// workerMessagesResult is the folded worker message evidence plus the
// authoritative pending-question projection. Neither field carries a bridge
// token or a worker's MCP endpoint.
type workerMessagesResult struct {
	SessionID        string                            `json:"sessionId"`
	Messages         []agentruntime.WorkerMessage      `json:"messages"`
	PendingQuestions []agentruntime.WorkerQuestionView `json:"pendingQuestions"`
}

// handleWorkerMessages projects the Runtime-owned worker message log and the
// durable pending questions. Adapters only render the result; the Desktop does
// not read the JSONL file or the decision ledger directly.
func (s *server) handleWorkerMessages(req rpcRequest) {
	var in workerMessagesParams
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "sessionId is required", nil))
		return
	}
	sessionID := strings.TrimSpace(in.SessionID)
	rt := s.sessionRuntime(sessionID)
	if rt == nil || rt.runtime == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "unknown_session", "unknown session", nil))
		return
	}
	snapshot, err := rt.runtime.WorkerSnapshot()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "worker_messages_unavailable", err.Error(), nil))
		return
	}
	result := workerMessagesResult{
		SessionID:        sessionID,
		Messages:         snapshot.Messages,
		PendingQuestions: snapshot.PendingQuestions,
	}
	if result.Messages == nil {
		result.Messages = []agentruntime.WorkerMessage{}
	}
	if result.PendingQuestions == nil {
		result.PendingQuestions = []agentruntime.WorkerQuestionView{}
	}
	s.writeResponse(req.ID, result, nil)
}

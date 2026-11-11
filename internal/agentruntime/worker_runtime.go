package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
	"github.com/oschina/mothx/internal/config"
)

// workerBridge returns the process-injected bridge, if any.
func (r *SessionRuntime) workerBridge() *WorkerBridgeServer {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.WorkerBridge
}

// WorkerMessageLog returns the session's append-only worker message log,
// creating it on first use. It is Runtime-owned; adapters may read the folded
// projection but must not write it.
func (r *SessionRuntime) WorkerMessageLog() (*WorkerMessageLog, error) {
	if r == nil {
		return nil, fmt.Errorf("agent runtime is nil")
	}
	r.mu.RLock()
	existing := r.workerMessages
	manager := r.Manager
	r.mu.RUnlock()
	if existing != nil {
		return existing, nil
	}
	if manager == nil {
		return nil, fmt.Errorf("worker messages require a bound session directory")
	}
	sessionDir := manager.GetSessionDir()
	sessionID := r.sessionIDForWorkerBridge()
	if strings.TrimSpace(sessionDir) == "" {
		return nil, fmt.Errorf("worker messages require a session directory")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("worker messages require a session ID")
	}
	r.workerMessagesMu.Lock()
	defer r.workerMessagesMu.Unlock()
	// Do the file open (including an index rebuild) outside r.mu so a large log
	// cannot block every Runtime state read while it is being loaded.
	r.mu.RLock()
	existing = r.workerMessages
	closed := r.closed
	r.mu.RUnlock()
	if existing != nil {
		return existing, nil
	}
	if closed {
		return nil, fmt.Errorf("agent runtime is closed")
	}
	log, err := OpenWorkerMessageLog(sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.workerMessages != nil {
		existing = r.workerMessages
		r.mu.Unlock()
		_ = log.Close()
		return existing, nil
	}
	if r.closed {
		r.mu.Unlock()
		_ = log.Close()
		return nil, fmt.Errorf("agent runtime is closed")
	}
	r.workerMessages = log
	r.mu.Unlock()
	return log, nil
}

// WorkerMessages returns the folded worker message projection for this session.
// It is read-only: unlike WorkerMessageLog it never creates the JSONL file, so a
// UI projection for a session that never used the bridge has no side effect.
func (r *SessionRuntime) WorkerMessages() ([]WorkerMessage, error) {
	if r == nil {
		return nil, nil
	}
	r.mu.RLock()
	log := r.workerMessages
	manager := r.Manager
	r.mu.RUnlock()
	if log != nil {
		return log.List()
	}
	if manager == nil || strings.TrimSpace(manager.GetSessionDir()) == "" {
		return nil, nil
	}
	sessionID := r.sessionIDForWorkerBridge()
	if strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	return ReadWorkerMessages(manager.GetSessionDir(), sessionID)
}

// ReadWorkerMessages folds one existing session's worker message log without
// creating it. A missing file is an empty projection, and rows belonging to a
// different session are never projected.
func ReadWorkerMessages(sessionDir, sessionID string) ([]WorkerMessage, error) {
	path, err := WorkerMessageLogPath(sessionDir, sessionID)
	if err != nil {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	log := &WorkerMessageLog{path: path, sessionID: strings.TrimSpace(sessionID), dedupe: map[string]string{}}
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.foldLocked()
}

// WorkerQuestionView is the read-only adapter projection of one pending worker
// question. The authoritative state remains the durable DecisionRecord; a UI may
// render this but must not answer through it.
type WorkerQuestionView struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	RunID     string    `json:"runId,omitempty"`
	ChildID   string    `json:"childId,omitempty"`
	Question  string    `json:"question"`
	Options   []string  `json:"options,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

// WorkerSnapshot is the read-only worker projection shared by every adapter.
// Messages are the JSONL evidence log; PendingQuestions derive from the
// authoritative durable decision ledger. Neither field carries a token.
type WorkerSnapshot struct {
	Messages         []WorkerMessage      `json:"messages"`
	PendingQuestions []WorkerQuestionView `json:"pendingQuestions"`
}

// WorkerSnapshot returns the session's read-only worker message and pending
// question projection. It never mutates decision state.
func (r *SessionRuntime) WorkerSnapshot() (WorkerSnapshot, error) {
	messages, err := r.WorkerMessages()
	if err != nil {
		return WorkerSnapshot{}, err
	}
	questions, err := r.PendingWorkerQuestions()
	if err != nil {
		return WorkerSnapshot{}, err
	}
	return WorkerSnapshot{Messages: messages, PendingQuestions: questions}, nil
}

// PendingWorkerQuestions projects the still-pending worker questions from the
// durable decision ledger. Worker questions are identified by the Runtime-owned
// ID prefix, so a lead approval/question is never mistaken for a worker ask.
func (r *SessionRuntime) PendingWorkerQuestions() ([]WorkerQuestionView, error) {
	if r == nil {
		return nil, nil
	}
	r.mu.RLock()
	manager := r.Manager
	r.mu.RUnlock()
	if manager == nil {
		return nil, nil
	}
	sessionDir := manager.GetSessionDir()
	sessionID := r.sessionIDForWorkerBridge()
	if strings.TrimSpace(sessionDir) == "" || strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	records, err := LoadDecisionRecords(sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	pending := ReplayDecisions(records)
	views := make([]WorkerQuestionView, 0, len(pending))
	for _, record := range pending {
		if record.Kind != DecisionQuestion || !IsWorkerQuestionID(record.ID) {
			continue
		}
		view := WorkerQuestionView{
			ID: record.ID, SessionID: record.SessionID, RunID: record.RunID,
			CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
		}
		var payload struct {
			Question string   `json:"question"`
			Options  []string `json:"options"`
			ChildID  string   `json:"childId"`
		}
		if len(record.Payload) > 0 {
			_ = json.Unmarshal(record.Payload, &payload)
		}
		view.Question = payload.Question
		view.Options = payload.Options
		view.ChildID = payload.ChildID
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].CreatedAt.Equal(views[j].CreatedAt) {
			return views[i].ID < views[j].ID
		}
		return views[i].CreatedAt.Before(views[j].CreatedAt)
	})
	return views, nil
}

// registerWorkerBridge creates the per-child token scope and returns the
// session/new mcpServers payload plus a revoke function. A profile that is
// remote, has mcpBridge=off, or resolved without MCPHTTP is not injected; the
// prompt still runs.
func (r *SessionRuntime) registerWorkerBridge(childID, parentRunID string, profile config.ACPAgentProfile, contract SubagentTaskContract, caps workerdriver.WorkerCapabilities, workDir string) ([]any, func(), error) {
	bridge := r.workerBridge()
	if bridge == nil || !caps.MCPBridge() {
		return nil, nil, nil
	}
	if profile.IsRemote() || profile.EffectiveMCPBridge() == config.ACPMCPBridgeOff {
		return nil, nil, nil
	}
	log, err := r.WorkerMessageLog()
	if err != nil {
		return nil, nil, err
	}
	scope := &WorkerBridgeScope{
		SessionID: r.sessionIDForWorkerBridge(), ChildID: childID, ParentRunID: parentRunID,
		ProfileID: profile.ID, WorkDir: workDir,
		TaskPrompt: contract.RenderPrompt(workDir), Capabilities: caps, Messages: log,
		Notify: func(message WorkerMessage) {
			r.enqueueWorkerFinding(message)
		},
		Ask: func(ctx context.Context, question string, options []string, timeout time.Duration) (string, string) {
			return r.askWorkerQuestion(ctx, childID, parentRunID, question, options, timeout)
		},
	}
	token, servers, err := bridge.Register(scope)
	if err != nil {
		return nil, nil, err
	}
	return servers, func() { bridge.Revoke(token) }, nil
}

// markWorkerTurnActive re-enables a child's bridge scope for a new turn.
func (r *SessionRuntime) markWorkerTurnActive(childID string) {
	if bridge := r.workerBridge(); bridge != nil {
		bridge.SetChildTerminal(r.sessionIDForWorkerBridge(), childID, false)
	}
}

// markWorkerTurnTerminal rejects bridge calls after the child's terminal event.
func (r *SessionRuntime) markWorkerTurnTerminal(childID string) {
	if bridge := r.workerBridge(); bridge != nil {
		bridge.SetChildTerminal(r.sessionIDForWorkerBridge(), childID, true)
	}
}

func (r *SessionRuntime) sessionIDForWorkerBridge() string {
	r.mu.RLock()
	manager := r.Manager
	id := r.ID
	r.mu.RUnlock()
	if manager != nil {
		if header := manager.GetHeader(); header != nil && header.ID != "" {
			return header.ID
		}
	}
	return id
}

// enqueueWorkerFinding injects a worker finding into the session mailbox. It is
// the only worker message kind that enters lead context; notice/progress stay
// in the JSONL log. The mailbox wake debounce coalesces a burst.
func (r *SessionRuntime) enqueueWorkerFinding(message WorkerMessage) {
	if r == nil || r.Mailbox == nil {
		return
	}
	r.installWorkerDeliveryObserver()
	r.Mailbox.EnqueueCoalesced(agent.MemberCompletion{
		Kind:        agent.MemberItemFinding,
		MemberID:    message.ChildID,
		DisplayName: "ACP worker",
		Status:      "finding",
		Payload:     message.Body,
		External:    true,
		Wake:        true,
		MessageID:   message.ID,
	})
}

// installWorkerDeliveryObserver wires mailbox consumption to the JSONL evidence
// projection once per session. The mailbox never writes the log itself.
func (r *SessionRuntime) installWorkerDeliveryObserver() {
	r.mu.Lock()
	if r.workerDeliveryObserver || r.Mailbox == nil {
		r.mu.Unlock()
		return
	}
	r.workerDeliveryObserver = true
	mailbox := r.Mailbox
	r.mu.Unlock()
	mailbox.SetDeliveryObserver(func(items []agent.MemberCompletion) {
		for _, item := range items {
			if item.MessageID != "" {
				r.MarkWorkerMessageDelivered(item.MessageID, "")
			}
		}
	})
	// A finding replaced by a newer finding from the same worker never enters
	// lead context; close its evidence row instead of leaving it "created".
	mailbox.SetSupersededObserver(func(items []agent.MemberCompletion) {
		for _, item := range items {
			if item.MessageID != "" {
				r.markWorkerMessageSuperseded(item.MessageID, "coalesced")
			}
		}
	})
}

// MarkWorkerMessageDelivered records that a queued message was consumed by a
// lead Run. Callers own the Run identity; the log only records the projection.
func (r *SessionRuntime) MarkWorkerMessageDelivered(messageID, runID string) {
	log, err := r.WorkerMessageLog()
	if err != nil {
		return
	}
	_ = log.Mark(messageID, workerMessageOpDelivered, runID, "")
}

// markWorkerMessageSuperseded records that a queued finding was replaced by a
// newer finding before the lead consumed it. It never affects decision state.
func (r *SessionRuntime) markWorkerMessageSuperseded(messageID, reason string) {
	log, err := r.WorkerMessageLog()
	if err != nil {
		return
	}
	_ = log.Mark(messageID, workerMessageOpSuperseded, "", reason)
}

// WorkerMessageContext is a small read-only projection used by adapters that
// need bridge state without waking the session.
func (r *SessionRuntime) WorkerMessageContext(ctx context.Context) ([]WorkerMessage, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return r.WorkerMessages()
}

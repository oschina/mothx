package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/session"
)

// workerQuestionIDPrefix marks a decision as a worker ask. It is the Runtime's
// own identity namespace; adapters never construct or parse it.
const workerQuestionIDPrefix = "wq_"

// IsWorkerQuestionID reports whether a decision ID belongs to a worker ask.
func IsWorkerQuestionID(id string) bool {
	return strings.HasPrefix(strings.TrimSpace(id), workerQuestionIDPrefix)
}

// RuntimeWorkerQuestionResolver adapts the shared DecisionService to Agent
// Core's WorkerQuestionResolver. It is installed once during manager assembly,
// at the same layer as the change-set integrator, so internal/agent never
// imports agentruntime.
type RuntimeWorkerQuestionResolver struct {
	Runtime *SessionRuntime
}

func (r RuntimeWorkerQuestionResolver) ResolveWorkerQuestion(ctx context.Context, questionID, answer string) error {
	if r.Runtime == nil {
		return fmt.Errorf("runtime is not attached")
	}
	return r.Runtime.ResolveWorkerQuestion(ctx, questionID, answer)
}

// askWorkerQuestion creates one durable DecisionQuestion for a worker question,
// projects it into the lead mailbox, and blocks until the lead answers, the
// deadline elapses, or the worker turn is cancelled. It never ends the worker's
// ACP turn: the worker decides how to continue.
func (r *SessionRuntime) askWorkerQuestion(ctx context.Context, childID, parentRunID, question string, options []string, timeout time.Duration) (string, string) {
	if r == nil {
		return "", "unavailable"
	}
	decisions := r.currentDecisions()
	if decisions == nil {
		return "", "unavailable"
	}
	sessionID := r.sessionIDForWorkerBridge()
	if strings.TrimSpace(sessionID) == "" {
		return "", "unavailable"
	}
	if timeout <= 0 {
		timeout = DefaultWorkerAskTimeout
	}
	if timeout > MaxWorkerAskTimeout {
		timeout = MaxWorkerAskTimeout
	}
	request := DecisionRequest{
		ID:        workerQuestionIDPrefix + session.GenerateID(),
		RunID:     strings.TrimSpace(parentRunID),
		SessionID: sessionID,
		Kind:      DecisionQuestion,
	}
	if request.RunID == "" {
		// A worker can outlive the lead Run that spawned it. The decision still
		// needs a durable Run anchor for replay; use the session identity as a
		// stable pseudo-run when no lead Run is active.
		request.RunID = "worker:" + sessionID
	}
	answers := make(chan string, 1)
	if err := decisions.Register(request); err != nil {
		return "", "unavailable"
	}
	if err := decisions.Bind(request.ID, func(value string) error {
		select {
		case answers <- value:
		default:
		}
		return nil
	}); err != nil {
		decisions.Discard(request.ID)
		return "", "unavailable"
	}
	expiresAt := time.Now().Add(timeout)
	payload := map[string]any{"question": question, "options": append([]string(nil), options...), "childId": childID}
	_ = r.recordWorkerDecision(request, DecisionStatusPending, "", payload, expiresAt)
	if r.Mailbox != nil {
		r.Mailbox.Enqueue(agent.MemberCompletion{
			Kind: agent.MemberItemQuestion, MemberID: childID, DisplayName: "ACP worker",
			Status: agent.MemberStatusQuestion, Payload: question, QuestionID: request.ID,
			Options: append([]string(nil), options...), External: true, Wake: true,
		})
	}
	if log, err := r.WorkerMessageLog(); err == nil {
		if _, _, err := log.Create(WorkerMessage{
			SessionID: sessionID, ChildID: childID, ParentRunID: parentRunID,
			Kind: WorkerMessageQuestion, Body: question, DecisionID: request.ID,
		}); err == nil {
			// The JSONL row is the evidence projection; the DecisionRecord remains
			// the authority for pending/response state.
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case answer := <-answers:
		return answer, "answered"
	case <-timer.C:
		r.terminalizeWorkerQuestion(request, DecisionStatusTimedOut, "timeout")
		return "", "timeout"
	case <-ctx.Done():
		r.terminalizeWorkerQuestion(request, DecisionStatusCancelled, "cancelled")
		return "", "cancelled"
	}
}

// ResolveWorkerQuestion answers a pending worker question. Only the owning
// session may answer, and a repeated identical answer is idempotent through the
// DecisionService. The durable resolution event is committed before the pending
// entry is consumed.
func (r *SessionRuntime) ResolveWorkerQuestion(ctx context.Context, questionID, answer string) error {
	if r == nil {
		return fmt.Errorf("runtime is not attached")
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	questionID = strings.TrimSpace(questionID)
	if questionID == "" {
		return fmt.Errorf("question_id is required")
	}
	decisions := r.currentDecisions()
	if decisions == nil {
		return fmt.Errorf("worker question resolution is not available for this session")
	}
	owner := ""
	for _, pending := range decisions.Pending() {
		if pending.ID == questionID {
			owner = pending.SessionID
			break
		}
	}
	if owner != "" {
		if sessionID := r.sessionIDForWorkerBridge(); sessionID != "" && owner != sessionID {
			return fmt.Errorf("question %s does not belong to this session", questionID)
		}
	}
	_, err := decisions.ResolveWith(DecisionResolution{
		ID: questionID, Kind: DecisionQuestion, Status: DecisionStatusResolved, Value: answer,
	}, func(request DecisionRequest) error {
		return r.recordWorkerDecision(request, DecisionStatusResolved, answer,
			map[string]any{"answer": answer}, time.Time{})
	})
	if err != nil {
		return fmt.Errorf("resolve worker question: %w", err)
	}
	r.markWorkerQuestionAnswered(questionID, answer)
	return nil
}

func (r *SessionRuntime) terminalizeWorkerQuestion(request DecisionRequest, status, reason string) {
	decisions := r.currentDecisions()
	if decisions == nil {
		return
	}
	_, _ = decisions.ResolveWith(DecisionResolution{
		ID: request.ID, Kind: DecisionQuestion, Status: status, Value: reason,
	}, func(req DecisionRequest) error {
		return r.recordWorkerDecision(req, status, reason, map[string]any{"reason": reason}, time.Time{})
	})
	// Keep the JSONL evidence projection aligned with the authoritative
	// DecisionRecord: a timed-out or cancelled question must not stay
	// "created" forever in the read-only UI projection.
	op := workerMessageOpCancelled
	if status == DecisionStatusTimedOut {
		op = workerMessageOpExpired
	}
	r.markWorkerQuestionTerminal(request.ID, op, reason)
}

// markWorkerQuestionTerminal folds the durable question outcome into the
// JSONL projection. It never affects decision authority.
func (r *SessionRuntime) markWorkerQuestionTerminal(questionID, op, reason string) {
	log := r.workerMessageLogOrNil()
	if log == nil || strings.TrimSpace(questionID) == "" {
		return
	}
	messages, err := log.List()
	if err != nil {
		return
	}
	for _, message := range messages {
		if message.DecisionID == questionID {
			_ = log.Mark(message.ID, op, "", reason)
			return
		}
	}
}

func (r *SessionRuntime) recordWorkerDecision(request DecisionRequest, status, value string, payload any, expiresAt time.Time) error {
	sessionDir := ""
	r.mu.RLock()
	manager := r.Manager
	source := string(r.Source)
	mode := r.Mode
	r.mu.RUnlock()
	if manager != nil {
		sessionDir = manager.GetSessionDir()
	}
	if sessionDir == "" {
		return fmt.Errorf("worker decision requires a session directory")
	}
	_, err := RecordDecisionEvent(SessionRunEventSink{SessionDir: sessionDir}, DecisionTransition{
		Request: request, Status: status, Value: value, Payload: payload,
		ExpiresAt: expiresAt, Source: source, Mode: mode,
	})
	return err
}

// markWorkerQuestionAnswered records the projection fact that the lead answered
// a worker question. Failure is non-fatal: the DecisionRecord is authoritative.
func (r *SessionRuntime) markWorkerQuestionAnswered(questionID, answer string) {
	log := r.workerMessageLogOrNil()
	if log == nil {
		return
	}
	messages, err := log.List()
	if err != nil {
		return
	}
	for _, message := range messages {
		if message.DecisionID == questionID {
			_ = log.Mark(message.ID, workerMessageOpDelivered, "", "")
			return
		}
	}
}

func (r *SessionRuntime) currentDecisions() *DecisionService {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	decisions := r.Decisions
	r.mu.RUnlock()
	return decisions
}

func (r *SessionRuntime) workerMessageLogOrNil() *WorkerMessageLog {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workerMessages
}

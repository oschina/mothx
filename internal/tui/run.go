package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

// tuiRun adapts the Bubble Tea stream to the shared execution lifecycle. It
// intentionally does not own rendering or event translation.
type tuiRun struct {
	execution      *agentruntime.ExecutionRuntime
	decisions      *agentruntime.DecisionService
	sessionID      string
	id             string
	sessionDir     string
	releaseRuntime func()
	workDir        string
	model          string
	mode           string
	artifacts      *agentruntime.ArtifactCollector
}

func (r *tuiRun) registerDecision(id string, kind agentruntime.DecisionKind) error {
	if r == nil || r.decisions == nil {
		return nil
	}
	if err := r.decisions.Register(agentruntime.DecisionRequest{
		ID: id, RunID: r.id, SessionID: r.sessionID, Kind: kind,
	}); err != nil {
		return err
	}
	if err := r.persistDecision(id, kind, "pending", "", nil); err != nil {
		return err
	}
	return nil
}

func (r *tuiRun) persistDecision(id string, kind agentruntime.DecisionKind, status, value string, payload any) error {
	if r == nil || r.sessionDir == "" || r.sessionID == "" || r.id == "" {
		return nil
	}
	event, err := agentruntime.BuildDecisionEvent(agentruntime.DecisionTransition{
		Request: agentruntime.DecisionRequest{ID: id, RunID: r.id, SessionID: r.sessionID, Kind: kind},
		Status:  status, Value: value, Payload: payload, Source: "tui",
	})
	if err != nil {
		return err
	}
	_, err = r.execution.RecordEvent(event)
	return err
}
func (r *tuiRun) resolveDecision(id string, kind agentruntime.DecisionKind, value string) error {
	if r == nil || r.decisions == nil {
		return nil
	}
	_, err := r.decisions.ResolveWith(agentruntime.DecisionResolution{
		ID: id, Kind: kind, Status: "resolved", Value: value,
	}, func(_ agentruntime.DecisionRequest) error {
		return r.persistDecision(id, kind, "resolved", value, map[string]any{"value": value})
	})
	return err
}

func (r *tuiRun) clearDecisions(status string) {
	if r == nil || r.decisions == nil {
		return
	}
	for _, request := range r.decisions.ClearRunWithValue(r.id, "") {
		r.persistDecision(request.ID, request.Kind, status, "", map[string]any{"reason": "TUI run ended before the decision was resolved"})
	}
}

// decisionTerminalStatus maps the terminal RunState to the decision status
// recorded for decisions that were still pending when the run ended. Only an
// explicit cancellation cancels them; any other terminal outcome (completed,
// failed, incomplete, timed out) means they lapsed with the run, so recording
// "cancelled" would misrepresent them.
func decisionTerminalStatus(state agentruntime.RunState) string {
	switch state {
	case agentruntime.RunStateCancelled, agentruntime.RunStateCancelling:
		return "cancelled"
	default:
		return "timed_out"
	}
}
func (r *tuiRun) start(parent context.Context, a *agent.Agent, input agentruntime.RunInput, userMessage provider.Message) (<-chan agent.Event, error) {
	if parent == nil {
		parent = context.Background()
	}
	if r == nil || r.execution == nil || a == nil {
		return nil, fmt.Errorf("tui run is not ready")
	}
	var ctx context.Context
	var err error
	turnID := "turn-" + r.id
	intentID := ""
	if r.sessionID != "" {
		guard, err := agentruntime.AcquireExecutionAdmission(parent, r.sessionDir, r.sessionID, agentruntime.ExecutionAdmissionOptions{})
		if err != nil {
			return nil, fmt.Errorf("session %s cannot start execution: %w", r.sessionID, err)
		}
		r.releaseRuntime = guard.Release
		releaseOnError := func() {
			if r.releaseRuntime != nil {
				r.releaseRuntime()
				r.releaseRuntime = nil
			}
		}
		startedAt := time.Now()
		r.execution.SetRunStore(agentruntime.RunStore{SessionDir: r.sessionDir})
		requestSnapshot, snapshotErr := json.Marshal(map[string]any{"input": input, "model": r.model, "mode": r.mode, "workDir": r.workDir})
		if snapshotErr != nil {
			releaseOnError()
			return nil, snapshotErr
		}
		policySnapshot, snapshotErr := json.Marshal(map[string]any{"source": "tui", "mode": r.mode, "workDir": r.workDir, "approvalPolicy": "runtime", "questionPolicy": "runtime"})
		if snapshotErr != nil {
			releaseOnError()
			return nil, snapshotErr
		}
		digest := sha256.Sum256(requestSnapshot)
		intent := agentruntime.ExecutionIntent{ID: "intent_" + session.GenerateID(), SessionID: r.sessionID, Source: "tui", Model: r.model, Mode: r.mode, WorkDir: r.workDir, RequestFingerprint: fmt.Sprintf("sha256:%x", digest[:]), Request: requestSnapshot, Policy: policySnapshot, CreatedAt: startedAt}
		intentID = intent.ID
		turnID = "turn-" + intent.ID
		startData, _ := json.Marshal(map[string]any{"intentId": intent.ID, "attempt": 1})
		ctx, err = r.execution.BeginIntentDurable(parent, intent, agentruntime.DurableRun{
			ID: r.id, SessionID: r.sessionID, IntentID: intent.ID, Attempt: 1, WorkDir: r.workDir, Source: "tui",
			Model: r.model, Mode: r.mode, Status: "running", StartedAt: startedAt, InputResourceIDs: input.ResourceIDs(),
			UserEntryID: session.RunUserEntryID(r.id), UserMessage: &userMessage,
			ConversationTurnID: "turn-" + intent.ID, ConversationTurn: true,
		}, agentruntime.RunEvent{SessionID: r.sessionID, RunID: r.id, EventType: "started", Source: "tui", Status: "running", Model: r.model, Mode: r.mode, Timestamp: startedAt, Data: startData})
	} else {
		ctx, err = r.execution.Begin(parent, r.id)
	}
	if err != nil {
		if r.releaseRuntime != nil {
			r.releaseRuntime()
			r.releaseRuntime = nil
		}
		return nil, err
	}
	if r.sessionID != "" {
		a.SetConversationTurn(turnID, intentID, r.id)
	}
	r.execution.SetAgent(a)
	return a.RunWithUserMessage(ctx, userMessage), nil
}

func (r *tuiRun) waitForQuestion() error {
	if r == nil || r.execution == nil {
		return nil
	}
	return r.execution.WaitForQuestion(r.id)
}

func (r *tuiRun) waitForApproval() error {
	if r == nil || r.execution == nil {
		return nil
	}
	return r.execution.WaitForApproval(r.id)
}

func (r *tuiRun) resume() error {
	if r == nil || r.execution == nil {
		return nil
	}
	return r.execution.Resume(r.id)
}

func (r *tuiRun) cancel() bool {
	return r != nil && r.execution != nil && r.execution.Cancel()
}

func (r *tuiRun) finish(state agentruntime.RunState) {
	if r == nil || r.execution == nil {
		return
	}
	if r != nil {
		r.clearDecisions(decisionTerminalStatus(state))
	}
	if r.sessionID != "" {
		_ = r.execution.FinishDurableWithRetry(context.Background(), r.id, state, "", agentruntime.RunEvent{SessionID: r.sessionID, RunID: r.id, EventType: "finished", Source: "tui", Status: string(state), Model: r.model, Mode: r.mode, Timestamp: time.Now()})
	} else {
		_ = r.execution.FinishWithState(r.id, state)
	}
	if r.releaseRuntime != nil {
		r.releaseRuntime()
		r.releaseRuntime = nil
	}
	if r.artifacts != nil {
		r.artifacts.Close()
		r.artifacts = nil
	}
}

func recoverTUIOrphanedDecisions(sessionDir, sessionID string) error {
	if sessionDir == "" || sessionID == "" {
		return nil
	}
	run, err := agentruntime.GetActiveDurableRun(context.Background(), sessionDir, sessionID)
	if err != nil || run == nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(run.Source), "tui") {
		return nil
	}
	records, err := agentruntime.LoadRunDecisionRecords(sessionDir, sessionID, run.ID)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, record := range agentruntime.ExpiredDecisions(records, now) {
		if err := persistRecoveredTUIDecision(sessionDir, run, record, "timed_out"); err != nil {
			return err
		}
	}
	for _, record := range agentruntime.ReplayDecisionsAt(records, now) {
		if err := persistRecoveredTUIDecision(sessionDir, run, record, "cancelled"); err != nil {
			return err
		}
	}
	_, err = agentruntime.RecoverOrphanedRuns(sessionDir, func(candidate session.SessionRun) agentruntime.RecoveryAction {
		if candidate.ID == run.ID {
			return agentruntime.RecoveryFailLocal
		}
		return agentruntime.RecoveryKeepRemote
	}, nil)
	return err
}

func persistRecoveredTUIDecision(sessionDir string, run *session.SessionRun, pending agentruntime.DecisionRecord, status string) error {
	event, err := agentruntime.BuildDecisionEvent(agentruntime.DecisionTransition{
		Request: agentruntime.DecisionRequest{ID: pending.ID, SessionID: run.SessionID, RunID: run.ID, Kind: pending.Kind},
		Status:  status, Payload: map[string]any{"reason": "TUI execution stack was not recoverable"},
		Source: "tui", Model: run.Model, Mode: run.Mode,
	})
	if err != nil {
		return err
	}
	_, err = (agentruntime.SessionRunEventSink{SessionDir: sessionDir}).Record(event)
	return err
}

func newTUIRun(sessionIDs ...string) *tuiRun {
	sessionID, sessionDir := "", ""
	if len(sessionIDs) > 0 {
		sessionID = sessionIDs[0]
	}
	if len(sessionIDs) > 1 {
		sessionDir = sessionIDs[1]
	}
	run := &tuiRun{
		execution:  &agentruntime.ExecutionRuntime{},
		decisions:  &agentruntime.DecisionService{},
		sessionID:  sessionID,
		id:         "tui_" + session.GenerateID(),
		sessionDir: sessionDir,
	}
	run.execution.SetEventSink(agentruntime.SessionRunEventSink{SessionDir: sessionDir})
	return run
}

package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// tuiRun adapts the Bubble Tea stream to the shared execution lifecycle. It
// intentionally does not own rendering or event translation.
type tuiRun struct {
	execution      *agentruntime.ExecutionRuntime
	decisions      *agentruntime.DecisionService
	sessionID      string
	id             string
	sessionDir     string
	workDir        string
	model          string
	mode           string
	artifacts      *agentruntime.ArtifactCollector
	coordinator    *agentruntime.SubmissionCoordinator
	submission     *agentruntime.ExecutionHandle
	submissionDone chan error
	submissionOnce sync.Once
	admitted       bool
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
	var err error
	if r.sessionID != "" {
		if r.coordinator != nil && !r.admitted {
			startReady := make(chan struct {
				events <-chan agent.Event
				ctx    context.Context
				err    error
			}, 1)
			r.submissionDone = make(chan error, 1)
			r.submission, err = r.coordinator.Submit(
				parent,
				r.id,
				agentruntime.SubmitPolicy{QueueBehindLocal: true},
				func(admissionCtx context.Context) (func(), error) {
					guard, acquireErr := agentruntime.AcquireExecutionAdmission(admissionCtx, r.sessionDir, r.sessionID, agentruntime.ExecutionAdmissionOptions{})
					if acquireErr != nil {
						return nil, acquireErr
					}
					return guard.Release, nil
				},
				func(runCtx context.Context) error {
					events, startErr := r.startAdmitted(runCtx, a, input, userMessage)
					startReady <- struct {
						events <-chan agent.Event
						ctx    context.Context
						err    error
					}{events: events, ctx: runCtx, err: startErr}
					if startErr != nil {
						return startErr
					}
					select {
					case finishErr := <-r.submissionDone:
						return finishErr
					case <-runCtx.Done():
						return runCtx.Err()
					}
				},
			)
			if err != nil {
				return nil, fmt.Errorf("session %s cannot submit execution: %w", r.sessionID, err)
			}
			return r.awaitSubmissionStart(startReady)
		}
	}
	return r.startAdmitted(parent, a, input, userMessage)
}

// awaitSubmissionStart waits for the coordinator's runner handoff. The runner
// is never invoked when admission fails (external owner, closed coordinator, or
// recovery error), so Completed is the terminal signal for that case; waiting
// only on startReady would leak the caller forever.
func (r *tuiRun) awaitSubmissionStart(startReady <-chan struct {
	events <-chan agent.Event
	ctx    context.Context
	err    error
}) (<-chan agent.Event, error) {
	select {
	case started := <-startReady:
		if started.err != nil {
			return nil, started.err
		}
		return started.events, nil
	case completion := <-r.submission.Completed:
		// The runner sends startReady before it can complete, so a terminal
		// submission without a start is an admission/terminal failure. Prefer a
		// concurrently available start handoff to avoid a select race.
		select {
		case started := <-startReady:
			if started.err != nil {
				return nil, started.err
			}
			return started.events, nil
		default:
		}
		if completion.Error != nil {
			return nil, completion.Error
		}
		return nil, fmt.Errorf("submission %s completed before the run started", r.submission.SubmissionID)
	}
}

func (r *tuiRun) startAdmitted(parent context.Context, a *agent.Agent, input agentruntime.RunInput, userMessage provider.Message) (<-chan agent.Event, error) {
	var ctx context.Context
	var err error
	turnID := "turn-" + r.id
	intentID := ""
	if r.sessionID != "" {
		startedAt := time.Now()
		r.execution.SetRunStore(agentruntime.RunStore{SessionDir: r.sessionDir})
		requestSnapshot, snapshotErr := json.Marshal(map[string]any{"input": input, "model": r.model, "mode": r.mode, "workDir": r.workDir})
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		policySnapshot, snapshotErr := json.Marshal(map[string]any{"source": "tui", "mode": r.mode, "workDir": r.workDir, "approvalPolicy": "runtime", "questionPolicy": "runtime"})
		if snapshotErr != nil {
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
	r.clearDecisions(decisionTerminalStatus(state))
	if r.submissionDone != nil {
		var finishErr error
		if r.sessionID != "" {
			finishErr = r.execution.FinishDurableWithRetry(context.Background(), r.id, state, "", agentruntime.RunEvent{SessionID: r.sessionID, RunID: r.id, EventType: "finished", Source: "tui", Status: string(state), Model: r.model, Mode: r.mode, Timestamp: time.Now()})
		} else {
			finishErr = r.execution.FinishWithState(r.id, state)
		}
		r.submissionOnce.Do(func() { r.submissionDone <- finishErr })
		if r.artifacts != nil {
			r.artifacts.Close()
			r.artifacts = nil
		}
		return
	}
	if r.sessionID != "" {
		_ = r.execution.FinishDurableWithRetry(context.Background(), r.id, state, "", agentruntime.RunEvent{SessionID: r.sessionID, RunID: r.id, EventType: "finished", Source: "tui", Status: string(state), Model: r.model, Mode: r.mode, Timestamp: time.Now()})
	} else {
		_ = r.execution.FinishWithState(r.id, state)
	}
	if r.artifacts != nil {
		r.artifacts.Close()
		r.artifacts = nil
	}
}

func (r *tuiRun) completionCommand() tea.Cmd {
	if r == nil || r.submission == nil {
		return nil
	}
	if r.coordinator != nil {
		return nil
	}
	completion := r.submission.Completed
	return func() tea.Msg {
		result, ok := <-completion
		if !ok {
			return submissionCompletedMsg{completion: agentruntime.ExecutionCompletion{
				SubmissionID: r.submission.SubmissionID,
				RunID:        r.submission.RunID,
				Error:        agentruntime.ErrSubmissionClosed,
			}}
		}
		return submissionCompletedMsg{completion: result}
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

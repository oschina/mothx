package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/startvibecoding/mothx/internal/session"
)

type RecoveryAction string

const (
	RecoveryFailLocal  RecoveryAction = "fail_local"
	RecoveryKeepRemote RecoveryAction = "keep_remote"
)

type RunRecoveryPolicy func(session.SessionRun) RecoveryAction

type RunRecoveryResult struct {
	Failed  []session.SessionRun
	Kept    []session.SessionRun
	Skipped []session.SessionRun
}

const (
	recoveryWorkerLimit               = 8
	recoveryFailurePersistenceTimeout = 5 * time.Second
)

type recoveryAttemptResult struct {
	index  int
	run    session.SessionRun
	action RecoveryAction
	err    error
}

// DefaultRunRecoveryPolicy fails local Agent loops, which cannot survive
// process termination. Provider-native remote execution must be retained only
// by a caller that has resolved a canonical remote run record and capability;
// Run.Source alone is not evidence that a provider task still exists.
func DefaultRunRecoveryPolicy(run session.SessionRun) RecoveryAction {
	return RecoveryFailLocal
}

// RecoverOrphanedRuns applies one shared startup policy to all durable runs.
// beforeFail may persist adapter-compatible decision cleanup before the run is
// marked failed.
func RecoverOrphanedRuns(sessionDir string, policy RunRecoveryPolicy, beforeFail func(session.SessionRun) error) (RunRecoveryResult, error) {
	return recoverOrphanedRunsWithTrigger(context.Background(), sessionDir, "startup", DefaultRecoveryAttemptTimeout, policy, beforeFail)
}

func recoverOrphanedRunsWithTrigger(ctx context.Context, sessionDir, trigger string, attemptTimeout time.Duration, policy RunRecoveryPolicy, beforeFail func(session.SessionRun) error) (RunRecoveryResult, error) {
	var result RunRecoveryResult
	if ctx == nil {
		ctx = context.Background()
	}
	orphans, err := session.ListOrphanedSessionRunsContext(ctx, sessionDir)
	if err != nil {
		return result, fmt.Errorf("list orphaned runs: %w", err)
	}
	workerCount := recoveryWorkerLimit
	if len(orphans) < workerCount {
		workerCount = len(orphans)
	}
	type recoveryJob struct {
		index int
		run   session.SessionRun
	}
	jobs := make(chan recoveryJob)
	results := make(chan recoveryAttemptResult, len(orphans))
	var workers sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				run := job.run
				if err := ctx.Err(); err != nil {
					results <- recoveryAttemptResult{index: job.index, run: run, err: err}
					continue
				}
				reason := "server restarted while run was active"
				if trigger == "periodic" {
					reason = "execution owner lease expired while run was active"
				}
				attemptCtx := ctx
				cancel := func() {}
				if attemptTimeout > 0 {
					attemptCtx, cancel = context.WithTimeout(ctx, attemptTimeout)
				}
				action, err := recoverOrphanedRun(attemptCtx, sessionDir, run, policy, beforeFail, trigger, "owner_lost", reason, RunStateFailed)
				cancel()
				results <- recoveryAttemptResult{index: job.index, run: run, action: action, err: err}
			}
		}()
	}
	for index, run := range orphans {
		jobs <- recoveryJob{index: index, run: run}
	}
	close(jobs)
	workers.Wait()
	close(results)
	attempts := make([]recoveryAttemptResult, len(orphans))
	for attempt := range results {
		attempts[attempt.index] = attempt
	}
	var firstErr error
	for _, attempt := range attempts {
		if attempt.err != nil {
			if firstErr == nil {
				firstErr = attempt.err
			}
			continue
		}
		switch attempt.action {
		case RecoveryKeepRemote:
			result.Kept = append(result.Kept, attempt.run)
		case RecoveryFailLocal:
			result.Failed = append(result.Failed, attempt.run)
		default:
			result.Skipped = append(result.Skipped, attempt.run)
		}
	}
	if firstErr != nil {
		return result, firstErr
	}
	return result, nil
}

// RecoverOrphanedSessionRun reconciles the one active Run for a session before
// a new local execution is admitted. It acquires its own purpose=recovery lease;
// callers must not pre-acquire a generic Session lease. A valid owner is
// skipped, not terminalized. Remotely resumable Runs are retained only when the
// supplied policy has verified their durable provider state.
func RecoverOrphanedSessionRun(sessionDir, sessionID string, policy RunRecoveryPolicy, beforeFail func(session.SessionRun) error) (RunRecoveryResult, error) {
	return RecoverOrphanedSessionRunContext(context.Background(), sessionDir, sessionID, policy, beforeFail)
}

// RecoverOrphanedSessionRunContext is the context-bounded admission recovery
// path used by Runtime callers.
func RecoverOrphanedSessionRunContext(ctx context.Context, sessionDir, sessionID string, policy RunRecoveryPolicy, beforeFail func(session.SessionRun) error) (RunRecoveryResult, error) {
	var result RunRecoveryResult
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	run, err := session.GetActiveSessionRunContext(ctx, sessionDir, sessionID)
	if err != nil {
		return result, fmt.Errorf("get active session run: %w", err)
	}
	if run == nil {
		return result, nil
	}
	action, err := recoverOrphanedRun(ctx, sessionDir, *run, policy, beforeFail, "admission", "owner_lost", "run remained active when the session became available for a new local execution", RunStateFailed)
	if err != nil {
		return result, err
	}
	if action == RecoveryKeepRemote {
		result.Kept = append(result.Kept, *run)
	} else if action == RecoveryFailLocal {
		result.Failed = append(result.Failed, *run)
	} else {
		result.Skipped = append(result.Skipped, *run)
	}
	return result, nil
}

// StopOrphanedSessionRun performs the user-triggered form of orphan
// reconciliation. It uses the same recovery lease/fencing path as automatic
// recovery but records a cancelled terminal state and a distinct reason.
func StopOrphanedSessionRun(sessionDir, sessionID string, beforeTerminalize func(session.SessionRun) error) (RunRecoveryResult, error) {
	return StopOrphanedSessionRunContext(context.Background(), sessionDir, sessionID, beforeTerminalize)
}

// StopOrphanedSessionRunContext is the context-bounded user-triggered orphan
// convergence path.
func StopOrphanedSessionRunContext(ctx context.Context, sessionDir, sessionID string, beforeTerminalize func(session.SessionRun) error) (RunRecoveryResult, error) {
	return StopOrphanedSessionRunContextForRun(ctx, sessionDir, sessionID, "", beforeTerminalize)
}

// StopOrphanedSessionRunContextForRun is the target-scoped user-triggered
// orphan convergence path. An empty expectedRunID preserves the session-wide
// compatibility behavior; a non-empty value prevents a stale caller from
// terminalizing a newer Run admitted after its initial inspection.
func StopOrphanedSessionRunContextForRun(ctx context.Context, sessionDir, sessionID, expectedRunID string, beforeTerminalize func(session.SessionRun) error) (RunRecoveryResult, error) {
	var result RunRecoveryResult
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	run, err := session.GetActiveSessionRunContext(ctx, sessionDir, sessionID)
	if err != nil {
		return result, fmt.Errorf("get active session run: %w", err)
	}
	if run == nil {
		return result, nil
	}
	if expectedRunID != "" && run.ID != expectedRunID {
		return result, nil
	}
	action, err := recoverOrphanedRun(
		ctx, sessionDir, *run, nil, beforeTerminalize, "user_stop", "cancelled_by_user_after_owner_loss",
		"run cancelled by user after its execution owner was lost", RunStateCancelled,
	)
	if err != nil {
		return result, err
	}
	if action == RecoveryKeepRemote {
		result.Kept = append(result.Kept, *run)
	} else if action == RecoveryFailLocal {
		result.Failed = append(result.Failed, *run)
	} else {
		result.Skipped = append(result.Skipped, *run)
	}
	return result, nil
}

func recoverOrphanedRun(ctx context.Context, sessionDir string, run session.SessionRun, policy RunRecoveryPolicy, beforeFail func(session.SessionRun) error, trigger, reasonCode, reason string, terminalState RunState) (RecoveryAction, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	facts, err := session.ReadSessionExecutionFactsContext(ctx, sessionDir, run.SessionID)
	if err != nil {
		return "", fmt.Errorf("inspect orphaned run %s: %w", run.ID, err)
	}
	if !facts.SessionExists {
		return "", fmt.Errorf("recover orphaned run %s: %w", run.ID, session.ErrRuntimeSessionNotFound)
	}
	if len(facts.ActiveRuns) == 0 {
		return "", nil
	}
	if len(facts.ActiveRuns) != 1 || facts.ActiveRuns[0].ID != run.ID {
		return "", fmt.Errorf("recover orphaned run %s: %w", run.ID, session.ErrRuntimeLeaseRunMismatch)
	}
	if facts.Lease != nil && facts.Lease.Valid {
		// The lease may be external execution, an admission hand-off, or another
		// recovery worker. None can be overridden merely because this process has
		// no matching in-memory runtime.
		return "", nil
	}
	if (trigger == "startup" || trigger == "periodic") && facts.Recovery != nil && facts.Recovery.State == session.SessionRunRecoveryFailed &&
		facts.Recovery.NextRetryAt != nil && facts.DatabaseNow.Before(*facts.Recovery.NextRetryAt) {
		return "", nil
	}
	previousEpoch := int64(0)
	if facts.Lease != nil {
		previousEpoch = facts.Lease.Epoch
	}
	guard, err := session.AcquireRecoveryContext(ctx, sessionDir, run.SessionID, run.ID)
	if err != nil {
		if errors.Is(err, session.ErrRuntimeLeaseBusy) || errors.Is(err, session.ErrSessionRecoveryNotNeeded) {
			return "", nil
		}
		return "", fmt.Errorf("acquire recovery lease for run %s: %w", run.ID, err)
	}
	defer guard.Release()

	// Acquisition and terminalization are separate transactions. Re-read all
	// facts under the acquired epoch and require the exact recovery binding
	// before consulting policy or writing any terminal state.
	facts, err = session.ReadSessionExecutionFactsContext(ctx, sessionDir, run.SessionID)
	if err != nil {
		return "", fmt.Errorf("reinspect orphaned run %s: %w", run.ID, err)
	}
	binding := guard.Binding()
	if len(facts.ActiveRuns) == 0 {
		return "", nil
	}
	if len(facts.ActiveRuns) != 1 || facts.ActiveRuns[0].ID != run.ID || facts.Lease == nil || !facts.Lease.Valid ||
		facts.Lease.Purpose != session.RuntimeLeasePurposeRecovery || facts.Lease.RunID != run.ID ||
		facts.Lease.OwnerInstanceID != binding.OwnerInstanceID || facts.Lease.TokenHash != binding.TokenHash || facts.Lease.Epoch != binding.Epoch {
		return "", fmt.Errorf("recover orphaned run %s: %w", run.ID, session.ErrRuntimeLeaseRunMismatch)
	}
	run = facts.ActiveRuns[0]
	recovery, err := session.BeginSessionRunRecoveryContext(ctx, sessionDir, run.SessionID, run.ID, trigger, reasonCode, previousEpoch)
	if err != nil {
		return "", fmt.Errorf("record recovery attempt for run %s: %w", run.ID, err)
	}
	if err := ctx.Err(); err != nil {
		return "", failRunRecoveryAttemptContext(ctx, sessionDir, run, recovery.Attempt, err)
	}
	action := defaultRunRecoveryAction(facts)
	if policy != nil {
		action = policy(run)
	}
	if action == RecoveryKeepRemote {
		if err := session.MarkSessionRunRecoveryDetachedContext(ctx, sessionDir, run.SessionID, run.ID); err != nil {
			return "", fmt.Errorf("record detached remote run %s: %w", run.ID, err)
		}
		return RecoveryKeepRemote, nil
	}
	if beforeFail != nil {
		if err := beforeFail(run); err != nil {
			return "", failRunRecoveryAttemptContext(ctx, sessionDir, run, recovery.Attempt, err)
		}
		if err := ctx.Err(); err != nil {
			return "", failRunRecoveryAttemptContext(ctx, sessionDir, run, recovery.Attempt, err)
		}
	}
	decisionEvents, err := recoveryDecisionResolutionEvents(ctx, sessionDir, run, reasonCode, reason)
	if err != nil {
		return "", failRunRecoveryAttemptContext(ctx, sessionDir, run, recovery.Attempt, fmt.Errorf("resolve recovery decisions: %w", err))
	}
	data, err := json.Marshal(map[string]string{"reason": reason, "code": reasonCode})
	if err != nil {
		return "", failRunRecoveryAttemptContext(ctx, sessionDir, run, recovery.Attempt, fmt.Errorf("marshal recovery reason: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return "", failRunRecoveryAttemptContext(ctx, sessionDir, run, recovery.Attempt, err)
	}
	finishedAt := time.Now()
	run.Status = durableRunStatus(terminalState)
	run.Error = reason
	run.FinishedAt = &finishedAt
	terminalEvent := sessionRunEventFromRuntime(RunEvent{
		ID: "recovery_" + run.ID + "_" + string(terminalState), SessionID: run.SessionID, RunID: run.ID,
		EventType: "recovered", Source: "agentruntime", Status: string(terminalState), Model: run.Model, Mode: run.Mode,
		Timestamp: finishedAt, Data: data,
	})
	if err := session.ConvergeSessionRunRecoveryContext(ctx, sessionDir, run, terminalEvent, decisionEvents, string(terminalState), reason); err != nil {
		return "", failRunRecoveryAttemptContext(ctx, sessionDir, run, recovery.Attempt, fmt.Errorf("recover orphaned run %s: %w", run.ID, err))
	}
	return RecoveryFailLocal, nil
}

func recoveryDecisionResolutionEvents(ctx context.Context, sessionDir string, run session.SessionRun, reasonCode, reason string) ([]session.SessionRunEvent, error) {
	records, err := LoadRunDecisionRecordsContext(ctx, sessionDir, run.SessionID, run.ID)
	if err != nil {
		return nil, err
	}
	pending := ReplayDecisions(records)
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]session.SessionRunEvent, 0, len(ids))
	for _, id := range ids {
		requestRecord := pending[id]
		request := DecisionRequest{ID: requestRecord.ID, SessionID: run.SessionID, RunID: run.ID, Kind: requestRecord.Kind}
		value := ""
		eventType := "decision_resolved"
		switch requestRecord.Kind {
		case DecisionApproval:
			value = "deny_once"
			eventType = "approval_resolved"
		case DecisionQuestion:
			eventType = "question_resolved"
		}
		resolution := DecisionResolution{ID: id, Kind: requestRecord.Kind, Status: "cancelled", Value: value}
		record, err := NewDecisionResolutionRecord(request, resolution, map[string]string{"reason": reason, "code": reasonCode})
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(DecisionEventEnvelope(record))
		if err != nil {
			return nil, err
		}
		result = append(result, session.SessionRunEvent{
			ID: "recovery_decision_" + run.ID + "_" + id, SessionID: run.SessionID, RunID: run.ID,
			EventType: eventType, Source: "agentruntime", Status: "cancelled", Model: run.Model, Mode: run.Mode,
			Timestamp: time.Now(), Data: encoded,
		})
	}
	return result, nil
}

func defaultRunRecoveryAction(facts session.SessionExecutionFacts) RecoveryAction {
	if len(facts.ActiveRuns) != 1 || facts.RemoteRun == nil {
		return RecoveryFailLocal
	}
	run := facts.ActiveRuns[0]
	remote := facts.RemoteRun
	if run.Source != "responses_background" || remote.SessionID != run.SessionID || remote.ResponseID == "" || remote.Provider == "" || remote.API != "openai-responses" {
		return RecoveryFailLocal
	}
	return RecoveryKeepRemote
}

func failRunRecoveryAttempt(sessionDir string, run session.SessionRun, attempt int, cause error) error {
	return failRunRecoveryAttemptContext(context.Background(), sessionDir, run, attempt, cause)
}

func failRunRecoveryAttemptContext(ctx context.Context, sessionDir string, run session.SessionRun, attempt int, cause error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	delay := time.Second
	for i := 1; i < attempt && delay < time.Minute; i++ {
		delay *= 2
		if delay > time.Minute {
			delay = time.Minute
		}
	}
	// A timed-out attempt cannot use its already-cancelled context to record the
	// durable failure marker. Use a fresh, short bounded context so the next
	// coordinator observes recovery_failed instead of a stale recovering row.
	persistCtx, cancel := context.WithTimeout(context.Background(), recoveryFailurePersistenceTimeout)
	defer cancel()
	if err := session.MarkSessionRunRecoveryFailedContext(persistCtx, sessionDir, run.SessionID, run.ID, cause.Error(), time.Now().Add(delay)); err != nil {
		return errors.Join(cause, fmt.Errorf("record recovery failure: %w", err))
	}
	return cause
}

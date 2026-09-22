package agentruntime

import (
	"context"
	"errors"
	"fmt"

	"github.com/oschina/mothx/internal/session"
)

// SessionStopCode is the adapter-neutral outcome of a stop request. Adapters
// map it to their protocol status without reimplementing ownership decisions.
type SessionStopCode string

const (
	SessionStopAccepted          SessionStopCode = "stop_accepted"
	SessionStopRemoteAccepted    SessionStopCode = "remote_stop_accepted"
	SessionStopRecoveryStarted   SessionStopCode = "recovery_started"
	SessionStopOwnedElsewhere    SessionStopCode = "session_run_owned_elsewhere"
	SessionStopRemoteUnsupported SessionStopCode = "remote_stop_unsupported"
	SessionStopReserved          SessionStopCode = "session_reserved"
	SessionStopNoActiveRun       SessionStopCode = "no_active_run"
	SessionStopStateUnavailable  SessionStopCode = "session_execution_state_unavailable"
	SessionStopRecoveryFailed    SessionStopCode = "session_recovery_failed"
	SessionStopRemoteFailed      SessionStopCode = "remote_stop_failed"
	SessionStopTargetChanged     SessionStopCode = "session_run_target_changed"
)

// ErrRemoteStopUnsupported lets a provider control hook distinguish a missing
// cancel capability from an upstream failure.
var ErrRemoteStopUnsupported = errors.New("remote stop is unsupported")

// RemoteStopRequest contains canonical provider execution identity. It never
// contains lease credentials: the Runtime acquires and revalidates those
// internally before invoking the provider hook.
type RemoteStopRequest struct {
	SessionID   string `json:"sessionId"`
	RunID       string `json:"runId"`
	RemoteRunID string `json:"remoteRunId"`
	Provider    string `json:"provider"`
	State       string `json:"state"`
}

// SessionStopOptions supplies protocol/provider hooks while retaining all
// Run/lease ownership decisions in the shared Runtime.
type SessionStopOptions struct {
	// ExpectedRunID scopes a stop request to the Run selected by the caller.
	// Runtime revalidates it before every local, remote, or orphan transition so
	// a stale Run API request cannot cancel a newer Run in the same Session.
	ExpectedRunID           string
	RemoteCancel            func(context.Context, RemoteStopRequest) error
	BeforeOrphanTerminalize func(session.SessionRun) error
	// LegacyLocalCancel is a migration bridge for embedded adapters that still
	// have a process-local run but no durable session_runs row. Runtime invokes
	// it only after the canonical snapshot proves that no durable Run exists;
	// it must never be used to override a durable or externally-owned Run.
	LegacyLocalCancel func() bool
}

// SessionStopResult is returned for both accepted and rejected requests so an
// adapter can immediately project the latest canonical execution snapshot.
type SessionStopResult struct {
	Code      SessionStopCode          `json:"code"`
	Execution SessionExecutionSnapshot `json:"execution"`
}

// RequestSessionStop applies the canonical stop matrix. Snapshot data is an
// expectation only: local cancellation and recovery revalidate the exact
// Run/lease binding before making a durable change.
func RequestSessionStop(ctx context.Context, sessionDir, sessionID string, options SessionStopOptions) (SessionStopResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot, err := InspectSessionExecution(sessionDir, sessionID)
	if err != nil {
		return SessionStopResult{Code: SessionStopStateUnavailable, Execution: snapshot}, err
	}
	if expected := options.ExpectedRunID; expected != "" && (snapshot.ActiveRun == nil || snapshot.ActiveRun.ID != expected) {
		return SessionStopResult{Code: SessionStopTargetChanged, Execution: snapshot}, nil
	}
	switch snapshot.State {
	case SessionExecutionIdle:
		if result, handled := requestLegacyLocalStop(sessionDir, sessionID, snapshot, options.LegacyLocalCancel); handled {
			return result, nil
		}
		return SessionStopResult{Code: SessionStopNoActiveRun, Execution: snapshot}, nil
	case SessionExecutionReserved:
		// A pre-runtime embedded adapter may hold the old process-local lease
		// without having created a durable Run yet. It is still safe to invoke
		// the narrowly-scoped migration callback because the snapshot contains
		// no durable active Run. A reservation owned by another operation has no
		// callback and remains protected.
		if result, handled := requestLegacyLocalStop(sessionDir, sessionID, snapshot, options.LegacyLocalCancel); handled {
			return result, nil
		}
		return SessionStopResult{Code: SessionStopReserved, Execution: snapshot}, nil
	case SessionExecutionExternal:
		return SessionStopResult{Code: SessionStopOwnedElsewhere, Execution: snapshot}, nil
	case SessionExecutionInconsistent, SessionExecutionUnknown:
		return SessionStopResult{Code: SessionStopStateUnavailable, Execution: snapshot}, nil
	case SessionExecutionLocal:
		return requestLocalSessionStop(sessionDir, snapshot)
	case SessionExecutionDetached:
		return requestDetachedRemoteStop(ctx, sessionDir, snapshot, options.RemoteCancel)
	case SessionExecutionOrphaned, SessionExecutionRecoveryFailed:
		_, recoveryErr := StopOrphanedSessionRunContextForRun(ctx, sessionDir, sessionID, options.ExpectedRunID, options.BeforeOrphanTerminalize)
		latest, inspectErr := InspectSessionExecution(sessionDir, sessionID)
		if recoveryErr != nil {
			if inspectErr != nil {
				latest = snapshot
			}
			return SessionStopResult{Code: SessionStopRecoveryFailed, Execution: latest}, recoveryErr
		}
		if inspectErr != nil {
			return SessionStopResult{Code: SessionStopStateUnavailable, Execution: latest}, inspectErr
		}
		if latest.State == SessionExecutionIdle {
			return SessionStopResult{Code: SessionStopRecoveryStarted, Execution: latest}, nil
		}
		return passiveSessionStopResult(latest), nil
	default:
		return SessionStopResult{Code: SessionStopStateUnavailable, Execution: snapshot}, nil
	}
}

func requestLegacyLocalStop(sessionDir, sessionID string, snapshot SessionExecutionSnapshot, cancel func() bool) (SessionStopResult, bool) {
	if cancel == nil || !cancel() {
		return SessionStopResult{}, false
	}
	latest, inspectErr := InspectSessionExecution(sessionDir, sessionID)
	if inspectErr != nil {
		return SessionStopResult{Code: SessionStopAccepted, Execution: snapshot}, true
	}
	return SessionStopResult{Code: SessionStopAccepted, Execution: latest}, true
}

func requestLocalSessionStop(sessionDir string, expected SessionExecutionSnapshot) (SessionStopResult, error) {
	runtime, ok, err := localExecutionForStop(sessionDir, expected)
	if err != nil {
		return SessionStopResult{Code: SessionStopStateUnavailable, Execution: expected}, err
	}
	if !ok {
		latest, inspectErr := InspectSessionExecution(sessionDir, expected.SessionID)
		if inspectErr != nil {
			return SessionStopResult{Code: SessionStopStateUnavailable, Execution: latest}, inspectErr
		}
		return passiveSessionStopResult(latest), nil
	}
	accepted, err := runtime.CancelDurable("run cancellation requested by user")
	if err != nil {
		latest, inspectErr := InspectSessionExecution(sessionDir, expected.SessionID)
		if inspectErr == nil && latest.State != SessionExecutionLocal {
			return passiveSessionStopResult(latest), nil
		}
		if inspectErr == nil {
			expected = latest
		}
		return SessionStopResult{Code: SessionStopStateUnavailable, Execution: expected}, err
	}
	if !accepted {
		latest, inspectErr := InspectSessionExecution(sessionDir, expected.SessionID)
		if inspectErr != nil {
			return SessionStopResult{Code: SessionStopStateUnavailable, Execution: latest}, inspectErr
		}
		return passiveSessionStopResult(latest), nil
	}
	latest, inspectErr := InspectSessionExecution(sessionDir, expected.SessionID)
	if inspectErr != nil {
		if expected.ActiveRun != nil {
			expected.ActiveRun.Status = string(RunStateCancelling)
		}
		return SessionStopResult{Code: SessionStopAccepted, Execution: expected}, nil
	}
	return SessionStopResult{Code: SessionStopAccepted, Execution: latest}, nil
}

func localExecutionForStop(sessionDir string, expected SessionExecutionSnapshot) (*ExecutionRuntime, bool, error) {
	if expected.ActiveRun == nil || expected.State != SessionExecutionLocal {
		return nil, false, nil
	}
	facts, err := session.ReadSessionExecutionFacts(sessionDir, expected.SessionID)
	if err != nil {
		return nil, false, err
	}
	if len(facts.ActiveRuns) != 1 || facts.ActiveRuns[0].ID != expected.ActiveRun.ID || facts.Lease == nil || !facts.Lease.Valid ||
		facts.Lease.Purpose != session.RuntimeLeasePurposeExecution || facts.Lease.RunID != expected.ActiveRun.ID ||
		facts.Lease.Epoch != expected.LeaseEpoch || facts.Lease.OwnerInstanceID != expected.LeaseOwnerInstanceID ||
		facts.Lease.TokenHash != expected.LeaseTokenIdentity {
		return nil, false, nil
	}
	runtime, ok := registeredLocalExecution(facts.DatabaseIdentity, facts.ActiveRuns[0], *facts.Lease)
	return runtime, ok, nil
}

func requestDetachedRemoteStop(ctx context.Context, sessionDir string, expected SessionExecutionSnapshot, cancel func(context.Context, RemoteStopRequest) error) (result SessionStopResult, resultErr error) {
	if expected.ActiveRun == nil || expected.RemoteRunID == "" {
		return SessionStopResult{Code: SessionStopStateUnavailable, Execution: expected}, nil
	}
	if !expected.CanCancelRemote || cancel == nil {
		if isRemoteResponseTerminal(expected.RemoteState) {
			wakeRecoveryCoordinators(sessionDir)
			return SessionStopResult{Code: SessionStopRecoveryStarted, Execution: expected}, nil
		}
		return SessionStopResult{Code: SessionStopRemoteUnsupported, Execution: expected}, nil
	}
	guard, err := session.AcquireRecoveryContext(ctx, sessionDir, expected.SessionID, expected.ActiveRun.ID)
	if err != nil {
		latest, inspectErr := InspectSessionExecution(sessionDir, expected.SessionID)
		if inspectErr != nil {
			return SessionStopResult{Code: SessionStopStateUnavailable, Execution: latest}, inspectErr
		}
		return passiveSessionStopResult(latest), nil
	}
	released := false
	defer func() {
		if !released {
			guard.Release()
		}
	}()

	facts, err := session.ReadSessionExecutionFactsContext(ctx, sessionDir, expected.SessionID)
	if err != nil {
		return SessionStopResult{Code: SessionStopStateUnavailable, Execution: expected}, err
	}
	binding := guard.Binding()
	if len(facts.ActiveRuns) != 1 || facts.ActiveRuns[0].ID != expected.ActiveRun.ID || facts.Lease == nil || !facts.Lease.Valid ||
		facts.Lease.Purpose != session.RuntimeLeasePurposeRecovery || facts.Lease.RunID != expected.ActiveRun.ID ||
		facts.Lease.OwnerInstanceID != binding.OwnerInstanceID || facts.Lease.TokenHash != binding.TokenHash || facts.Lease.Epoch != binding.Epoch ||
		defaultRunRecoveryAction(facts) != RecoveryKeepRemote {
		guard.Release()
		released = true
		latest, inspectErr := InspectSessionExecution(sessionDir, expected.SessionID)
		if inspectErr != nil {
			return SessionStopResult{Code: SessionStopStateUnavailable, Execution: latest}, inspectErr
		}
		return passiveSessionStopResult(latest), nil
	}

	run := facts.ActiveRuns[0]
	recovery, err := session.BeginSessionRunRecoveryContext(ctx, sessionDir, run.SessionID, run.ID, "user_stop", "remote_run_cancelled_by_user", expected.LeaseEpoch)
	if err != nil {
		return SessionStopResult{Code: SessionStopStateUnavailable, Execution: expected}, err
	}
	remote := facts.RemoteRun
	err = cancel(ctx, RemoteStopRequest{
		SessionID: run.SessionID, RunID: run.ID, RemoteRunID: remote.LocalRunID,
		Provider: remote.Provider, State: remote.State,
	})
	if errors.Is(err, ErrRemoteStopUnsupported) {
		_ = session.MarkSessionRunRecoveryDetachedContext(ctx, sessionDir, run.SessionID, run.ID)
		return SessionStopResult{Code: SessionStopRemoteUnsupported, Execution: expected}, nil
	}
	if err != nil {
		failure := failRunRecoveryAttemptContext(ctx, sessionDir, run, recovery.Attempt, fmt.Errorf("cancel remote run: %w", err))
		return SessionStopResult{Code: SessionStopRemoteFailed, Execution: expected}, failure
	}
	if err := session.MarkSessionRunRecoveryDetachedContext(ctx, sessionDir, run.SessionID, run.ID); err != nil {
		return SessionStopResult{Code: SessionStopStateUnavailable, Execution: expected}, err
	}
	guard.Release()
	released = true
	wakeRecoveryCoordinators(sessionDir)
	latest, inspectErr := InspectSessionExecution(sessionDir, expected.SessionID)
	if inspectErr != nil {
		latest = expected
	}
	return SessionStopResult{Code: SessionStopRemoteAccepted, Execution: latest}, nil
}

func passiveSessionStopResult(snapshot SessionExecutionSnapshot) SessionStopResult {
	code := SessionStopStateUnavailable
	switch snapshot.State {
	case SessionExecutionIdle:
		code = SessionStopNoActiveRun
	case SessionExecutionReserved:
		code = SessionStopReserved
	case SessionExecutionExternal:
		code = SessionStopOwnedElsewhere
	case SessionExecutionDetached:
		code = SessionStopRemoteUnsupported
	case SessionExecutionOrphaned, SessionExecutionRecoveryFailed:
		code = SessionStopRecoveryFailed
	}
	return SessionStopResult{Code: code, Execution: snapshot}
}

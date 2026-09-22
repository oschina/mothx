package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oschina/mothx/internal/session"
)

var ErrDetachedRemoteExecution = errors.New("session has a recoverable detached remote execution")

// ExecutionAdmissionOptions controls how a frontend-neutral caller waits for
// ownership and how an orphan is reconciled before a new Run is admitted.
type ExecutionAdmissionOptions struct {
	Wait           bool
	PollInterval   time.Duration
	RecoveryPolicy RunRecoveryPolicy
	BeforeRecover  func(session.SessionRun) error
}

// AcquireExecutionAdmission obtains the explicit admission lease for a new
// Run. If a stale durable Run blocks admission, this operation reconciles it
// through the same lease-first Runtime recovery path and retries. A valid
// local or external owner is never displaced.
func AcquireExecutionAdmission(ctx context.Context, sessionDir, sessionID string, options ExecutionAdmissionOptions) (*session.RuntimeLeaseGuard, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pollInterval := options.PollInterval
	if pollInterval <= 0 {
		pollInterval = 50 * time.Millisecond
	}
	for {
		guard, err := session.AcquireExecutionAdmission(sessionDir, sessionID)
		if err == nil {
			return guard, nil
		}
		switch {
		case errors.Is(err, session.ErrSessionRecoveryRequired):
			result, recoveryErr := RecoverOrphanedSessionRunContext(ctx, sessionDir, sessionID, options.RecoveryPolicy, options.BeforeRecover)
			if recoveryErr != nil {
				return nil, recoveryErr
			}
			if len(result.Kept) > 0 {
				return nil, fmt.Errorf("%w: %s", ErrDetachedRemoteExecution, result.Kept[0].ID)
			}
			if len(result.Failed) > 0 {
				continue
			}
			if !options.Wait {
				return nil, session.ErrRuntimeLeaseBusy
			}
		case errors.Is(err, session.ErrRuntimeLeaseBusy):
			if !options.Wait {
				return nil, err
			}
		default:
			return nil, err
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// AcquireSessionMutation waits for (or immediately attempts) an explicit
// mutation lease. An orphaned local Run is reconciled through the same shared
// recovery path before the mutation is retried.
func AcquireSessionMutation(ctx context.Context, sessionDir, sessionID string, options ExecutionAdmissionOptions) (*session.RuntimeLeaseGuard, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pollInterval := options.PollInterval
	if pollInterval <= 0 {
		pollInterval = 50 * time.Millisecond
	}
	for {
		guard, err := session.AcquireMutation(sessionDir, sessionID)
		if err == nil {
			return guard, nil
		}
		switch {
		case errors.Is(err, session.ErrSessionRunActive):
			result, recoveryErr := RecoverOrphanedSessionRunContext(ctx, sessionDir, sessionID, options.RecoveryPolicy, options.BeforeRecover)
			if recoveryErr != nil {
				return nil, recoveryErr
			}
			if len(result.Kept) > 0 {
				return nil, fmt.Errorf("%w: %s", ErrDetachedRemoteExecution, result.Kept[0].ID)
			}
			if len(result.Failed) > 0 {
				continue
			}
			if !options.Wait {
				return nil, session.ErrRuntimeLeaseBusy
			}
		case errors.Is(err, session.ErrRuntimeLeaseBusy):
			if !options.Wait {
				return nil, err
			}
		default:
			return nil, err
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

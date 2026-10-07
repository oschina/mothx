package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
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
// local or external owner is never displaced. A busy lease still held by this
// process for an execution that already selected its terminal state is
// draining, not contended: admission waits for the Runtime-owned terminal
// persistence to release it (bounded by ctx) so a queued same-process
// successor keeps the session in this process instead of failing with a
// misleading cross-process busy error.
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
			if !options.Wait && !localExecutionDraining(sessionDir, sessionID) {
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
			if !options.Wait && !localExecutionDraining(sessionDir, sessionID) {
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

// AcquireSessionMutationUnlessHeld acquires the shared mutation lease unless
// this process already holds a live lease for the session (for example an
// in-run administrative patch): the held lease already fences the durable
// writes, and a second acquisition would trip the process-local lock with a
// false busy. A nil guard with a nil error means "proceed under the
// already-held lease"; the caller must release a non-nil guard.
func AcquireSessionMutationUnlessHeld(ctx context.Context, sessionDir, sessionID string, options ExecutionAdmissionOptions) (*session.RuntimeLeaseGuard, error) {
	if session.RuntimeLeaseHeldByCurrentProcess(sessionDir, sessionID) {
		return nil, nil
	}
	return AcquireSessionMutation(ctx, sessionDir, sessionID, options)
}

// MutationLeaseGroup owns mutation leases across several sessions for one
// coordinated mutation. Guard resolves the lease acquired for one session ID;
// Release relinquishes every lease in reverse acquisition order and is
// idempotent.
type MutationLeaseGroup struct {
	entries []mutationLeaseEntry
	once    sync.Once
}

type mutationLeaseEntry struct {
	sessionID string
	guard     *session.RuntimeLeaseGuard
}

func (g *MutationLeaseGroup) Guard(sessionID string) *session.RuntimeLeaseGuard {
	if g == nil {
		return nil
	}
	for _, entry := range g.entries {
		if entry.sessionID == sessionID {
			return entry.guard
		}
	}
	return nil
}

func (g *MutationLeaseGroup) Release() {
	if g == nil {
		return
	}
	g.once.Do(func() {
		for i := len(g.entries) - 1; i >= 0; i-- {
			g.entries[i].guard.Release()
		}
	})
}

// AcquireSessionMutationGroup acquires mutation leases for every given session
// in the same stable sorted order as session.AcquireMutations, so a
// cross-session operation cannot deadlock another caller taking the same set,
// while applying the shared orphan-run reconciliation per session. Earlier
// leases are released when a later acquisition fails.
func AcquireSessionMutationGroup(ctx context.Context, sessionDir string, sessionIDs []string, options ExecutionAdmissionOptions) (*MutationLeaseGroup, error) {
	ids := append([]string(nil), sessionIDs...)
	sort.Strings(ids)
	ordered := ids[:0]
	for _, id := range ids {
		if id == "" || (len(ordered) > 0 && ordered[len(ordered)-1] == id) {
			continue
		}
		ordered = append(ordered, id)
	}
	group := &MutationLeaseGroup{}
	for _, id := range ordered {
		guard, err := AcquireSessionMutation(ctx, sessionDir, id, options)
		if err != nil {
			group.Release()
			return nil, err
		}
		group.entries = append(group.entries, mutationLeaseEntry{sessionID: id, guard: guard})
	}
	return group, nil
}

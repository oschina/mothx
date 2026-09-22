package agentruntime

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/session"
)

// SessionExecutionState is the adapter-neutral ownership state of a Session.
type SessionExecutionState string

const (
	SessionExecutionIdle           SessionExecutionState = "idle"
	SessionExecutionReserved       SessionExecutionState = "reserved"
	SessionExecutionLocal          SessionExecutionState = "local"
	SessionExecutionExternal       SessionExecutionState = "external"
	SessionExecutionDetached       SessionExecutionState = "detached_remote"
	SessionExecutionOrphaned       SessionExecutionState = "orphaned"
	SessionExecutionRecoveryFailed SessionExecutionState = "recovery_failed"
	SessionExecutionInconsistent   SessionExecutionState = "inconsistent"
	SessionExecutionUnknown        SessionExecutionState = "unknown"
)

// SessionRunSummary is the canonical non-terminal Run projection carried by a
// Session execution snapshot.
type SessionRunSummary struct {
	ID        string    `json:"runId"`
	Status    string    `json:"status"`
	Source    string    `json:"source,omitempty"`
	Model     string    `json:"model,omitempty"`
	Mode      string    `json:"mode,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SessionExecutionSnapshot is the single Runtime-owned interpretation of the
// durable Run, SQLite lease, and current process execution registration.
type SessionExecutionSnapshot struct {
	SessionID            string                `json:"sessionId"`
	SessionExists        bool                  `json:"sessionExists"`
	ActiveRun            *SessionRunSummary    `json:"activeRun,omitempty"`
	State                SessionExecutionState `json:"state"`
	Phase                string                `json:"phase,omitempty"`
	Running              bool                  `json:"running"`
	Busy                 bool                  `json:"busy"`
	CanSubmit            bool                  `json:"canSubmit"`
	CanCancelLocal       bool                  `json:"canCancelLocal"`
	CanCancelRemote      bool                  `json:"canCancelRemote"`
	LeasePurpose         string                `json:"leasePurpose,omitempty"`
	LeaseEpoch           int64                 `json:"leaseEpoch,omitempty"`
	LeaseExpiresAt       *time.Time            `json:"leaseExpiresAt,omitempty"`
	LeaseOwnerInstanceID string                `json:"ownerInstanceId,omitempty"`
	LeaseOwnerPID        int                   `json:"ownerPid,omitempty"`
	LeaseTokenIdentity   string                `json:"leaseTokenIdentity,omitempty"`
	LinkageState         string                `json:"linkageState"`
	RecoveryAction       string                `json:"recoveryAction"`
	RecoveryAttempt      int                   `json:"recoveryAttempt,omitempty"`
	RecoveryLastError    string                `json:"recoveryLastError,omitempty"`
	RecoveryNextAt       *time.Time            `json:"recoveryNextAt,omitempty"`
	DisplayOwnerScope    string                `json:"ownerScope"`
	RemoteRunID          string                `json:"remoteRunId,omitempty"`
	RemoteProvider       string                `json:"remoteProvider,omitempty"`
	RemoteState          string                `json:"remoteState,omitempty"`
}

type durableExecutionOwnershipStore interface {
	ExecutionBinding(sessionID, runID string) (session.RuntimeLeaseBinding, bool, error)
	PrepareExistingExecution(sessionID, runID string) error
}

// durableExecutionLeaseRetentionStore lets the Runtime retain the exact
// execution lease while an adapter's admission guard is being released. It is
// optional so legacy embedded stores without a durable lease remain usable.
type durableExecutionLeaseRetentionStore interface {
	RetainExecutionLease(sessionID, runID string) (session.RuntimeLeaseBinding, func(), bool, error)
}

type localExecutionRegistration struct {
	binding session.RuntimeLeaseBinding
	runtime *ExecutionRuntime
}

var localExecutionRegistry = struct {
	sync.RWMutex
	entries map[string]localExecutionRegistration
}{entries: make(map[string]localExecutionRegistration)}

func executionRegistrationKey(binding session.RuntimeLeaseBinding) string {
	return binding.DatabaseIdentity + "\x00" + binding.SessionID + "\x00" + binding.RunID + "\x00" + strconv.FormatInt(binding.Epoch, 10)
}

func (r *ExecutionRuntime) registerLocalExecution(run DurableRun) error {
	if r == nil {
		return fmt.Errorf("execution runtime is nil")
	}
	store, ok := r.runStore().(durableExecutionOwnershipStore)
	if !ok || store == nil {
		return nil
	}
	binding, present, err := store.ExecutionBinding(run.SessionID, run.ID)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if binding.Purpose != session.RuntimeLeasePurposeExecution || binding.SessionID != run.SessionID || binding.RunID != run.ID {
		return session.ErrRuntimeLeaseRunMismatch
	}
	var releaseLease func()
	if retentionStore, retentionOK := store.(durableExecutionLeaseRetentionStore); retentionOK {
		retainedBinding, release, retained, retainErr := retentionStore.RetainExecutionLease(run.SessionID, run.ID)
		if retainErr != nil {
			return retainErr
		}
		if retained {
			binding = retainedBinding
			releaseLease = release
		}
	}
	bindingCopy := binding
	r.mu.Lock()
	if !r.activeLocked(run.ID) {
		r.mu.Unlock()
		if releaseLease != nil {
			releaseLease()
		}
		return fmt.Errorf("execution is not active: %s", run.ID)
	}
	r.leaseBinding = &bindingCopy
	r.leaseRelease = releaseLease
	r.mu.Unlock()

	key := executionRegistrationKey(binding)
	localExecutionRegistry.Lock()
	if existing, exists := localExecutionRegistry.entries[key]; exists && existing.runtime != r {
		localExecutionRegistry.Unlock()
		r.mu.Lock()
		if r.leaseBinding != nil && executionRegistrationKey(*r.leaseBinding) == key {
			r.leaseBinding = nil
			releaseLease = r.leaseRelease
			r.leaseRelease = nil
		}
		r.mu.Unlock()
		if releaseLease != nil {
			releaseLease()
		}
		return fmt.Errorf("local execution binding already registered: %s", run.ID)
	}
	localExecutionRegistry.entries[key] = localExecutionRegistration{binding: binding, runtime: r}
	localExecutionRegistry.Unlock()
	return nil
}

func (r *ExecutionRuntime) unregisterLocalExecution() {
	if r == nil {
		return
	}
	r.mu.Lock()
	binding := r.leaseBinding
	releaseLease := r.leaseRelease
	r.leaseBinding = nil
	r.leaseRelease = nil
	r.mu.Unlock()
	if binding == nil {
		if releaseLease != nil {
			releaseLease()
		}
		return
	}
	key := executionRegistrationKey(*binding)
	localExecutionRegistry.Lock()
	if current, ok := localExecutionRegistry.entries[key]; ok && current.runtime == r {
		delete(localExecutionRegistry.entries, key)
	}
	localExecutionRegistry.Unlock()
	if releaseLease != nil {
		releaseLease()
	}
}

func registeredLocalExecution(databaseIdentity string, run session.SessionRun, lease session.RuntimeLeaseSnapshot) (*ExecutionRuntime, bool) {
	binding := session.RuntimeLeaseBinding{
		DatabaseIdentity: databaseIdentity,
		SessionID:        run.SessionID,
		RunID:            run.ID,
		OwnerInstanceID:  lease.OwnerInstanceID,
		TokenHash:        lease.TokenHash,
		Epoch:            lease.Epoch,
		Purpose:          lease.Purpose,
	}
	localExecutionRegistry.RLock()
	entry, ok := localExecutionRegistry.entries[executionRegistrationKey(binding)]
	localExecutionRegistry.RUnlock()
	if !ok || entry.runtime == nil || entry.binding.OwnerInstanceID != binding.OwnerInstanceID || entry.binding.TokenHash != binding.TokenHash || entry.binding.Purpose != binding.Purpose {
		return nil, false
	}
	registeredRunID, active := entry.runtime.Active()
	return entry.runtime, active && registeredRunID == run.ID
}

func sameLeaseIdentity(binding session.RuntimeLeaseBinding, lease session.RuntimeLeaseSnapshot) bool {
	return binding.SessionID == lease.SessionID && binding.OwnerInstanceID == lease.OwnerInstanceID && binding.TokenHash == lease.TokenHash && binding.Epoch == lease.Epoch
}

// InspectSessionExecution resolves the canonical execution state without
// trusting adapter-local maps. Database read failures return an unknown
// snapshot together with the error so callers remain conservatively busy.
func InspectSessionExecution(sessionDir, sessionID string) (SessionExecutionSnapshot, error) {
	snapshot := SessionExecutionSnapshot{
		SessionID:         sessionID,
		State:             SessionExecutionUnknown,
		Busy:              true,
		LinkageState:      "none",
		RecoveryAction:    "none",
		DisplayOwnerScope: "unknown",
	}
	facts, err := session.ReadSessionExecutionFacts(sessionDir, sessionID)
	if err != nil {
		return snapshot, err
	}
	snapshot.SessionExists = facts.SessionExists
	if !facts.SessionExists {
		snapshot.Phase = "missing"
		return snapshot, nil
	}
	if facts.Lease != nil {
		lease := facts.Lease
		snapshot.LeasePurpose = string(lease.Purpose)
		snapshot.LeaseEpoch = lease.Epoch
		expiresAt := lease.ExpiresAt
		snapshot.LeaseExpiresAt = &expiresAt
		snapshot.LeaseOwnerInstanceID = lease.OwnerInstanceID
		snapshot.LeaseOwnerPID = lease.OwnerPID
		snapshot.LeaseTokenIdentity = lease.TokenHash
	}
	if len(facts.ActiveRuns) > 1 {
		snapshot.State = SessionExecutionInconsistent
		snapshot.LinkageState = "mismatched"
		return snapshot, nil
	}
	if len(facts.ActiveRuns) == 0 {
		if facts.Lease != nil && facts.Lease.Valid {
			snapshot.State = SessionExecutionReserved
			snapshot.Phase = leasePhase(facts.Lease.Purpose, false)
			snapshot.DisplayOwnerScope = leaseOwnerScope(sessionDir, *facts.Lease)
			return snapshot, nil
		}
		snapshot.State = SessionExecutionIdle
		snapshot.Busy = false
		snapshot.CanSubmit = true
		snapshot.DisplayOwnerScope = "none"
		return snapshot, nil
	}

	run := facts.ActiveRuns[0]
	snapshot.ActiveRun = &SessionRunSummary{
		ID: run.ID, Status: run.Status, Source: run.Source, Model: run.Model, Mode: run.Mode,
		StartedAt: run.StartedAt, UpdatedAt: run.UpdatedAt,
	}
	if facts.Recovery != nil {
		snapshot.RecoveryAttempt = facts.Recovery.Attempt
		snapshot.RecoveryLastError = facts.Recovery.LastError
		if facts.Recovery.NextRetryAt != nil {
			nextRetryAt := *facts.Recovery.NextRetryAt
			snapshot.RecoveryNextAt = &nextRetryAt
		}
	}
	lease := facts.Lease
	if lease == nil || !lease.Valid {
		if defaultRunRecoveryAction(facts) == RecoveryKeepRemote {
			remoteTerminal := isRemoteResponseTerminal(facts.RemoteRun.State)
			snapshot.State = SessionExecutionDetached
			snapshot.Phase = "executing"
			snapshot.Running = !remoteTerminal
			snapshot.CanCancelRemote = !remoteTerminal && !facts.RemoteRun.CancelRequested
			snapshot.RecoveryAction = "reattach_remote"
			snapshot.DisplayOwnerScope = "remote"
			snapshot.RemoteRunID = facts.RemoteRun.LocalRunID
			snapshot.RemoteProvider = facts.RemoteRun.Provider
			snapshot.RemoteState = facts.RemoteRun.State
			if remoteTerminal {
				snapshot.Phase = "recovering"
				snapshot.RecoveryAction = "finalize_remote"
			}
			return snapshot, nil
		}
		if facts.Recovery != nil && facts.Recovery.State == session.SessionRunRecoveryFailed {
			snapshot.State = SessionExecutionRecoveryFailed
			snapshot.Phase = "recovering"
			snapshot.RecoveryAction = "retry_recovery"
			snapshot.DisplayOwnerScope = "none"
			wakeRecoveryCoordinators(sessionDir)
			return snapshot, nil
		}
		snapshot.State = SessionExecutionOrphaned
		snapshot.Phase = "recovering"
		snapshot.RecoveryAction = "recover_orphan"
		snapshot.DisplayOwnerScope = "none"
		wakeRecoveryCoordinators(sessionDir)
		return snapshot, nil
	}
	snapshot.DisplayOwnerScope = leaseOwnerScope(sessionDir, *lease)
	switch {
	case lease.RunID == run.ID:
		snapshot.LinkageState = "bound"
	case lease.RunID == "" && lease.Purpose == session.RuntimeLeasePurposeLegacyRun:
		snapshot.LinkageState = "legacy_unbound"
	default:
		snapshot.LinkageState = "mismatched"
	}

	switch lease.Purpose {
	case session.RuntimeLeasePurposeRecovery:
		if lease.RunID != run.ID {
			snapshot.State = SessionExecutionInconsistent
			return snapshot, nil
		}
		snapshot.State = SessionExecutionReserved
		snapshot.Phase = "recovering"
		snapshot.RecoveryAction = "retry_recovery"
		return snapshot, nil
	case session.RuntimeLeasePurposeAdmission, session.RuntimeLeasePurposeMutation, session.RuntimeLeasePurposeFork:
		snapshot.State = SessionExecutionInconsistent
		snapshot.Phase = leasePhase(lease.Purpose, true)
		return snapshot, nil
	case session.RuntimeLeasePurposeExecution:
		if lease.RunID != run.ID {
			snapshot.State = SessionExecutionInconsistent
			return snapshot, nil
		}
	case session.RuntimeLeasePurposeLegacyRun:
		if lease.RunID != "" && lease.RunID != run.ID {
			snapshot.State = SessionExecutionInconsistent
			return snapshot, nil
		}
	default:
		snapshot.State = SessionExecutionInconsistent
		return snapshot, nil
	}

	snapshot.Phase = "executing"
	if _, local := registeredLocalExecution(facts.DatabaseIdentity, run, *lease); local {
		snapshot.State = SessionExecutionLocal
		snapshot.Running = true
		snapshot.CanCancelLocal = true
		snapshot.DisplayOwnerScope = "local"
		return snapshot, nil
	}
	if localBinding, ok := session.CurrentRuntimeLeaseBinding(sessionDir, sessionID); ok && sameLeaseIdentity(localBinding, *lease) && lease.Purpose == session.RuntimeLeasePurposeExecution {
		snapshot.State = SessionExecutionInconsistent
		snapshot.DisplayOwnerScope = "local"
		return snapshot, nil
	}
	snapshot.State = SessionExecutionExternal
	snapshot.Running = true
	return snapshot, nil
}

func isRemoteResponseTerminal(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "completed", "failed", "incomplete", "cancelled", "canceled", "expired":
		return true
	default:
		return false
	}
}

func leasePhase(purpose session.RuntimeLeasePurpose, activeRun bool) string {
	switch purpose {
	case session.RuntimeLeasePurposeAdmission:
		return "admitting"
	case session.RuntimeLeasePurposeExecution, session.RuntimeLeasePurposeLegacyRun:
		if activeRun {
			return "executing"
		}
		return "releasing"
	case session.RuntimeLeasePurposeRecovery:
		return "recovering"
	default:
		return "reserved"
	}
}

func leaseOwnerScope(sessionDir string, lease session.RuntimeLeaseSnapshot) string {
	if binding, ok := session.CurrentRuntimeLeaseBinding(sessionDir, lease.SessionID); ok && sameLeaseIdentity(binding, lease) {
		return "local"
	}
	return "external"
}

package agentruntime

import (
	"github.com/oschina/mothx/internal/session"
)

const (
	// snapshotSourceDatabase marks a projection derived from an authoritative
	// SQLite read of the execution facts.
	snapshotSourceDatabase = "db"
	// snapshotSourceCache marks a projection derived from the synchronized,
	// process-wide ownership view (which is itself derived from SQLite leases).
	snapshotSourceCache = "cache"
	// snapshotSourceLocal marks a projection derived from process-local execution
	// registration for a session without a shared session root.
	snapshotSourceLocal = "local"
)

func newSessionExecutionSnapshot(sessionID string) SessionExecutionSnapshot {
	return SessionExecutionSnapshot{
		SessionID:         sessionID,
		State:             SessionExecutionUnknown,
		Busy:              true,
		LinkageState:      "none",
		RecoveryAction:    "none",
		DisplayOwnerScope: "unknown",
	}
}

func sessionExecutionRunID(facts session.SessionExecutionFacts) string {
	if len(facts.ActiveRuns) == 1 {
		return facts.ActiveRuns[0].ID
	}
	return ""
}

func sessionExecutionRunStatus(facts session.SessionExecutionFacts) string {
	if len(facts.ActiveRuns) == 1 {
		return facts.ActiveRuns[0].Status
	}
	return ""
}

// inspectSessionExecutionFromOwnershipCache projects a session's execution state
// from the process-wide synchronized ownership view when it is fresh and
// complete enough to project without a SQLite read.
//
// It deliberately declines (falling through to the authoritative SQLite path):
//   - our own lease, because the local path adds process-local execution state;
//   - anything but a remote execution lease that carries a Run id and status,
//     because the projector maps an execution lease whose Run id does not match
//     the durable Run row to "inconsistent" - a distinction the cache cannot
//     make without the row.
func inspectSessionExecutionFromOwnershipCache(sessionDir, sessionID, identity string) (SessionExecutionSnapshot, bool) {
	lookup := session.LookupRuntimeOwnership(identity, sessionID)
	if !lookup.Fresh {
		return SessionExecutionSnapshot{}, false
	}
	entry := lookup.Entry
	if entry.OwnerInstanceID == "" || entry.OwnerInstanceID == session.RuntimeOwnerInstanceID() {
		return SessionExecutionSnapshot{}, false
	}
	if entry.Purpose != string(session.RuntimeLeasePurposeExecution) || entry.RunID == "" || entry.RunStatus == "" {
		return SessionExecutionSnapshot{}, false
	}
	facts := session.SessionExecutionFacts{
		DatabaseIdentity: identity,
		SessionID:        sessionID,
		SessionExists:    true,
		Lease: &session.RuntimeLeaseSnapshot{
			SessionID:       sessionID,
			OwnerInstanceID: entry.OwnerInstanceID,
			OwnerPID:        entry.OwnerPID,
			Epoch:           entry.Epoch,
			RunID:           entry.RunID,
			Purpose:         session.RuntimeLeasePurpose(entry.Purpose),
			State:           "active",
			ExpiresAt:       entry.ExpiresAt,
			Valid:           true,
		},
		ActiveRuns: []session.SessionRun{{
			ID:        entry.RunID,
			SessionID: sessionID,
			Status:    entry.RunStatus,
			Source:    entry.Purpose,
		}},
	}
	snapshot, _ := projectSessionExecutionFromFacts(sessionDir, sessionID, facts)
	snapshot.Source = snapshotSourceCache
	return snapshot, true
}

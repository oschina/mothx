package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

// RuntimeLeasePurpose describes why a process owns the Session-wide lease.
// Legacy rows use RuntimeLeasePurposeLegacyRun until all callers have migrated
// to an explicit purpose.
type RuntimeLeasePurpose string

const (
	RuntimeLeasePurposeLegacyRun RuntimeLeasePurpose = "run"
	RuntimeLeasePurposeAdmission RuntimeLeasePurpose = "admission"
	RuntimeLeasePurposeExecution RuntimeLeasePurpose = "execution"
	RuntimeLeasePurposeRecovery  RuntimeLeasePurpose = "recovery"
	RuntimeLeasePurposeMutation  RuntimeLeasePurpose = "mutation"
	RuntimeLeasePurposeFork      RuntimeLeasePurpose = "fork"
	// RuntimeLeasePurposeLegacy is the compatibility reservation taken by calls
	// that reach the Session Manager without a Runtime execution lease. It is a
	// short reservation like mutation/fork: it never binds a Run and never
	// grants execution authority. See AcquireContentWriteReservation.
	RuntimeLeasePurposeLegacy RuntimeLeasePurpose = "legacy"
)

// RuntimeLeaseSnapshot is an immutable database view of one Session lease.
// TokenHash is an internal identity component used only for matching a
// Runtime-owned binding; callers must never accept it back as authorization.
type RuntimeLeaseSnapshot struct {
	SessionID       string
	OwnerInstanceID string
	OwnerPID        int
	OwnerKind       string
	TokenHash       string
	Epoch           int64
	RunID           string
	Purpose         RuntimeLeasePurpose
	State           string
	AcquiredAt      time.Time
	HeartbeatAt     time.Time
	ExpiresAt       time.Time
	UpdatedAt       time.Time
	Valid           bool
}

// SessionExecutionFacts contains the durable Run and lease rows observed from
// one SQLite read transaction and one SQLite clock sample. ActiveRuns normally
// contains at most one row; retaining the slice lets Runtime surface corrupted
// or legacy databases with multiple active rows as inconsistent.
type SessionExecutionFacts struct {
	DatabaseIdentity string
	SessionID        string
	SessionExists    bool
	DatabaseNow      time.Time
	ActiveRuns       []SessionRun
	Lease            *RuntimeLeaseSnapshot
	Recovery         *SessionRunRecovery
	RemoteRun        *ResponseRun
}

// ReadSessionExecutionFacts reads the durable execution facts for one Session
// from a single transaction. It deliberately does not consult process-local
// runtime state; internal/agentruntime combines this immutable view with its
// own registered execution bindings.
func ReadSessionExecutionFacts(sessionDir, sessionID string) (SessionExecutionFacts, error) {
	return ReadSessionExecutionFactsContext(context.Background(), sessionDir, sessionID)
}

// ReadSessionExecutionFactsContext is the cancellable form used by bounded
// recovery attempts. The transaction and every query in the snapshot share
// the caller's deadline. It is the batch-of-one form of
// ReadSessionExecutionFactsBatch so both paths cannot drift.
func ReadSessionExecutionFactsContext(ctx context.Context, sessionDir, sessionID string) (SessionExecutionFacts, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return SessionExecutionFacts{DatabaseIdentity: runtimeDatabaseIdentity(sessionDir), SessionID: sessionID}, fmt.Errorf("session ID is required")
	}
	all, err := ReadSessionExecutionFactsBatch(ctx, sessionDir, []string{sessionID})
	if err != nil {
		return all[sessionID], err
	}
	return all[sessionID], nil
}

// ReadSessionExecutionFactsBatch reads the durable execution facts for several
// Sessions from one read transaction and one SQLite clock sample. It is the
// list/page path: existence, non-terminal runs, and leases are read in three
// batch queries instead of three queries per session, while recovery and linked
// remote rows are still read only for the sessions that have exactly one active
// run.
//
// Every requested ID present in the result map carries the same projection as
// ReadSessionExecutionFacts. Empty IDs are ignored and duplicates collapse to
// one fact entry. A session that does not exist is reported with
// SessionExists=false rather than as an error.
func ReadSessionExecutionFactsBatch(ctx context.Context, sessionDir string, sessionIDs []string) (map[string]SessionExecutionFacts, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	identity := runtimeDatabaseIdentity(sessionDir)
	factsByID := make(map[string]SessionExecutionFacts, len(sessionIDs))
	ids := make([]string, 0, len(sessionIDs))
	for _, raw := range sessionIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, exists := factsByID[id]; exists {
			continue
		}
		factsByID[id] = SessionExecutionFacts{DatabaseIdentity: identity, SessionID: id}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return factsByID, nil
	}

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return factsByID, err
	}
	tx, err := db.BeginTx(ctx, &dao.TxOptions{ReadOnly: true})
	if err != nil {
		return factsByID, err
	}
	defer func() { _ = tx.Rollback() }()

	now, err := sqliteNowContext(tx, ctx)
	if err != nil {
		return factsByID, fmt.Errorf("read SQLite execution clock: %w", err)
	}
	databaseNow := time.Unix(now, 0).UTC()
	for id, facts := range factsByID {
		facts.DatabaseNow = databaseNow
		factsByID[id] = facts
	}

	existingIDs, err := dao.NewSessionDAO(nil).ExistingIDs(ctx, tx, ids)
	if err != nil {
		return factsByID, fmt.Errorf("inspect execution sessions: %w", err)
	}
	for _, id := range existingIDs {
		facts, ok := factsByID[id]
		if !ok {
			continue
		}
		facts.SessionExists = true
		factsByID[id] = facts
	}

	records, err := dao.NewRunDAO(nil).ActiveBySessions(ctx, tx, ids, NonTerminalSessionRunStatuses())
	if err != nil {
		return factsByID, fmt.Errorf("read active session runs: %w", err)
	}
	for i := range records {
		facts, ok := factsByID[records[i].SessionID]
		if !ok {
			continue
		}
		facts.ActiveRuns = append(facts.ActiveRuns, sessionRunFromRecord(&records[i]))
		factsByID[records[i].SessionID] = facts
	}

	// Recovery and linked response rows are only meaningful while exactly one
	// non-terminal run exists; keep the single-session read's rule and helpers.
	for id, facts := range factsByID {
		if len(facts.ActiveRuns) != 1 {
			continue
		}
		activeRunID := facts.ActiveRuns[0].ID
		recovery, recoveryErr := readSessionRunRecoveryTxContext(ctx, tx, activeRunID)
		if recoveryErr != nil && recoveryErr != dao.ErrNoRows {
			return factsByID, fmt.Errorf("read session run recovery: %w", recoveryErr)
		}
		if recoveryErr == nil {
			facts.Recovery = recovery
		}
		remote, remoteErr := readLinkedResponseRunTxContext(ctx, tx, id, activeRunID)
		if remoteErr != nil && remoteErr != dao.ErrNoRows {
			return factsByID, fmt.Errorf("read linked remote run: %w", remoteErr)
		}
		if remoteErr == nil {
			facts.RemoteRun = remote
		}
		factsByID[id] = facts
	}

	leaseRecords, err := dao.NewRuntimeLeaseDAO(nil).FindMany(ctx, tx, ids)
	if err != nil {
		return factsByID, fmt.Errorf("read session runtime leases: %w", err)
	}
	for id, record := range leaseRecords {
		facts, ok := factsByID[id]
		if !ok {
			continue
		}
		lease := RuntimeLeaseSnapshot{SessionID: record.SessionID, OwnerInstanceID: record.OwnerID, OwnerPID: record.OwnerPID, OwnerKind: record.OwnerKind, TokenHash: record.TokenHash, Epoch: record.Epoch, RunID: record.RunID, Purpose: RuntimeLeasePurpose(record.Purpose), State: record.State,
			AcquiredAt: time.Unix(record.AcquiredAt, 0).UTC(), HeartbeatAt: time.Unix(record.HeartbeatAt, 0).UTC(), ExpiresAt: time.Unix(record.ExpiresAt, 0).UTC(), UpdatedAt: time.Unix(record.UpdatedAt, 0).UTC(), Valid: record.State == "active" && record.ExpiresAt > now}
		facts.Lease = &lease
		factsByID[id] = facts
	}

	if err := tx.Commit(); err != nil {
		return factsByID, err
	}
	return factsByID, nil
}

func readLinkedResponseRunTx(tx *dao.Tx, sessionID, runID string) (*ResponseRun, error) {
	return readLinkedResponseRunTxContext(context.Background(), tx, sessionID, runID)
}

func readLinkedResponseRunTxContext(ctx context.Context, tx *dao.Tx, sessionID, runID string) (*ResponseRun, error) {
	record, err := dao.NewResponseDAO(nil).LinkedRun(ctx, tx, sessionID, runID)
	if err != nil {
		return nil, err
	}
	run := ResponseRun{ID: record.ID, SessionID: record.SessionID, LocalRunID: record.LocalRunID, LocalTurnID: record.LocalTurnID, Provider: record.Provider, API: record.API, State: record.State, CancelRequested: record.CancelRequested, CreatedAt: parseSessionTimestamp(record.CreatedAt), UpdatedAt: parseSessionTimestamp(record.UpdatedAt)}
	if record.ResponseID != nil {
		run.ResponseID = *record.ResponseID
	}
	if record.MessageID != nil {
		run.MessageID = record.MessageID
	}
	if record.PollingURL != nil {
		run.PollingURL = *record.PollingURL
	}
	run.LastEventSequence = record.LastEventSequence
	return &run, nil
}

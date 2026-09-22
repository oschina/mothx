package session

import (
	"context"
	"fmt"
	"github.com/oschina/mothx/internal/dao"
	"strings"
	"time"
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
// the caller's deadline.
func ReadSessionExecutionFactsContext(ctx context.Context, sessionDir, sessionID string) (SessionExecutionFacts, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	facts := SessionExecutionFacts{DatabaseIdentity: runtimeDatabaseIdentity(sessionDir), SessionID: sessionID}
	if sessionID == "" {
		return facts, fmt.Errorf("session ID is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return facts, err
	}
	tx, err := db.BeginTx(ctx, &dao.TxOptions{ReadOnly: true})
	if err != nil {
		return facts, err
	}
	defer func() { _ = tx.Rollback() }()

	now, err := sqliteNowContext(tx, ctx)
	if err != nil {
		return facts, fmt.Errorf("read SQLite execution clock: %w", err)
	}
	facts.DatabaseNow = time.Unix(now, 0).UTC()
	facts.SessionExists, err = dao.NewRuntimeLeaseDAO(nil).SessionExists(ctx, tx, sessionID)
	if err != nil {
		return facts, fmt.Errorf("inspect execution session: %w", err)
	}

	records, err := dao.NewRunDAO(nil).OrphanedFrom(ctx, tx, NonTerminalSessionRunStatuses())
	if err != nil {
		return facts, fmt.Errorf("read active session runs: %w", err)
	}
	for _, record := range records {
		if record.SessionID == sessionID {
			facts.ActiveRuns = append(facts.ActiveRuns, sessionRunFromRecord(&record))
		}
	}
	if len(facts.ActiveRuns) == 1 {
		activeRunID := facts.ActiveRuns[0].ID
		recovery, recoveryErr := readSessionRunRecoveryTxContext(ctx, tx, activeRunID)
		if recoveryErr != nil && recoveryErr != dao.ErrNoRows {
			return facts, fmt.Errorf("read session run recovery: %w", recoveryErr)
		}
		if recoveryErr == nil {
			facts.Recovery = recovery
		}
		remote, remoteErr := readLinkedResponseRunTxContext(ctx, tx, sessionID, activeRunID)
		if remoteErr != nil && remoteErr != dao.ErrNoRows {
			return facts, fmt.Errorf("read linked remote run: %w", remoteErr)
		}
		if remoteErr == nil {
			facts.RemoteRun = remote
		}
	}

	record, err := dao.NewRuntimeLeaseDAO(nil).Find(ctx, tx, sessionID)
	if err != nil && err != dao.ErrNoRows {
		return facts, fmt.Errorf("read session runtime lease: %w", err)
	}
	if err == nil {
		lease := RuntimeLeaseSnapshot{SessionID: record.SessionID, OwnerInstanceID: record.OwnerID, OwnerPID: record.OwnerPID, OwnerKind: record.OwnerKind, TokenHash: record.TokenHash, Epoch: record.Epoch, RunID: record.RunID, Purpose: RuntimeLeasePurpose(record.Purpose), State: record.State,
			AcquiredAt: time.Unix(record.AcquiredAt, 0).UTC(), HeartbeatAt: time.Unix(record.HeartbeatAt, 0).UTC(), ExpiresAt: time.Unix(record.ExpiresAt, 0).UTC(), UpdatedAt: time.Unix(record.UpdatedAt, 0).UTC(), Valid: record.State == "active" && record.ExpiresAt > now}
		facts.Lease = &lease
	}

	if err := tx.Commit(); err != nil {
		return facts, err
	}
	return facts, nil
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

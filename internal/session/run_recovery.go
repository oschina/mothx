package session

import (
	"context"
	"fmt"
	"github.com/oschina/mothx/internal/dao"
	"strings"
	"time"
)

type SessionRunRecoveryState string

const (
	SessionRunRecoveryRunning  SessionRunRecoveryState = "recovering"
	SessionRunRecoveryFailed   SessionRunRecoveryState = "failed"
	SessionRunRecoveryComplete SessionRunRecoveryState = "completed"
	SessionRunRecoveryDetached SessionRunRecoveryState = "detached_remote"
)

// SessionRunRecovery is the durable diagnostic and retry state for orphan
// reconciliation. It never grants ownership; the recovery lease remains the
// sole authority for changing a Run.
type SessionRunRecovery struct {
	RunID              string
	SessionID          string
	State              SessionRunRecoveryState
	TriggerSource      string
	ReasonCode         string
	Attempt            int
	PreviousLeaseEpoch int64
	LastError          string
	NextRetryAt        *time.Time
	StartedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        *time.Time
}

// BeginSessionRunRecovery records an attempt while verifying the exact
// purpose=recovery lease and target Run in the same transaction.
func BeginSessionRunRecovery(sessionDir, sessionID, runID, triggerSource, reasonCode string, previousLeaseEpoch int64) (*SessionRunRecovery, error) {
	return BeginSessionRunRecoveryContext(context.Background(), sessionDir, sessionID, runID, triggerSource, reasonCode, previousLeaseEpoch)
}

// BeginSessionRunRecoveryContext is the cancellable form used by the bounded
// Runtime recovery coordinator.
func BeginSessionRunRecoveryContext(ctx context.Context, sessionDir, sessionID, runID, triggerSource, reasonCode string, previousLeaseEpoch int64) (*SessionRunRecovery, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("session recovery identity is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := validateRuntimeLeaseBindingTxContext(ctx, tx, sessionDir, sessionID, runID, RuntimeLeasePurposeRecovery); err != nil {
		return nil, err
	}
	now, err := sqliteNowContext(tx, ctx)
	if err != nil {
		return nil, err
	}
	if err := dao.NewRecoveryDAO(nil).Upsert(ctx, tx, &dao.RecoveryRecord{RunID: runID, SessionID: sessionID, State: string(SessionRunRecoveryRunning), TriggerSource: triggerSource, ReasonCode: reasonCode, Attempt: 1, PreviousLeaseEpoch: previousLeaseEpoch, StartedAt: now, UpdatedAt: now}); err != nil {
		return nil, err
	}
	recovery, err := readSessionRunRecoveryTxContext(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return recovery, nil
}

// MarkSessionRunRecoveryFailed persists a retryable failure under the same
// fenced recovery owner. A zero nextRetryAt means retry as soon as a Runtime
// coordinator observes the row again.
func MarkSessionRunRecoveryFailed(sessionDir, sessionID, runID, message string, nextRetryAt time.Time) error {
	return MarkSessionRunRecoveryFailedContext(context.Background(), sessionDir, sessionID, runID, message, nextRetryAt)
}

func MarkSessionRunRecoveryFailedContext(ctx context.Context, sessionDir, sessionID, runID, message string, nextRetryAt time.Time) error {
	return updateSessionRunRecoveryContext(ctx, sessionDir, sessionID, runID, SessionRunRecoveryFailed, message, nextRetryAt)
}

// MarkSessionRunRecoveryDetached records that a canonical remote record was
// retained. The response record, not this marker, remains the evidence used to
// decide whether the provider execution is still recoverable.
func MarkSessionRunRecoveryDetached(sessionDir, sessionID, runID string) error {
	return MarkSessionRunRecoveryDetachedContext(context.Background(), sessionDir, sessionID, runID)
}

func MarkSessionRunRecoveryDetachedContext(ctx context.Context, sessionDir, sessionID, runID string) error {
	return updateSessionRunRecoveryContext(ctx, sessionDir, sessionID, runID, SessionRunRecoveryDetached, "", time.Time{})
}

// MarkSessionRunRecoveryComplete records successful fenced convergence.
func MarkSessionRunRecoveryComplete(sessionDir, sessionID, runID string) error {
	return MarkSessionRunRecoveryCompleteContext(context.Background(), sessionDir, sessionID, runID)
}

func MarkSessionRunRecoveryCompleteContext(ctx context.Context, sessionDir, sessionID, runID string) error {
	return updateSessionRunRecoveryContext(ctx, sessionDir, sessionID, runID, SessionRunRecoveryComplete, "", time.Time{})
}

// ConvergeSessionRunRecovery atomically records pending Decision resolutions,
// closes every open ConversationTurn owned by the Run, writes the terminal Run
// event and state, and completes the recovery record. The exact local
// purpose=recovery lease is revalidated inside the transaction so a stale
// recovery worker cannot commit after another owner takes over.
func ConvergeSessionRunRecovery(sessionDir string, run SessionRun, terminalEvent SessionRunEvent, decisionEvents []SessionRunEvent, turnStatus, stopReason string) error {
	return ConvergeSessionRunRecoveryContext(context.Background(), sessionDir, run, terminalEvent, decisionEvents, turnStatus, stopReason)
}

// ConvergeSessionRunRecoveryContext is the cancellable form used by the
// bounded Runtime recovery coordinator.
func ConvergeSessionRunRecoveryContext(ctx context.Context, sessionDir string, run SessionRun, terminalEvent SessionRunEvent, decisionEvents []SessionRunEvent, turnStatus, stopReason string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.SessionID) == "" || strings.TrimSpace(run.Status) == "" {
		return fmt.Errorf("recovered run identity and terminal status are required")
	}
	if IsNonTerminalSessionRunStatus(run.Status) {
		return fmt.Errorf("recovered run status must be terminal: %s", run.Status)
	}
	if terminalEvent.ID == "" || terminalEvent.EventType == "" {
		return fmt.Errorf("recovery terminal event identity and type are required")
	}
	terminalEvent.SessionID = run.SessionID
	terminalEvent.RunID = run.ID
	if terminalEvent.Status == "" {
		terminalEvent.Status = run.Status
	}
	if terminalEvent.Timestamp.IsZero() {
		terminalEvent.Timestamp = time.Now()
	}
	if run.FinishedAt == nil {
		finishedAt := terminalEvent.Timestamp
		run.FinishedAt = &finishedAt
	}
	if turnStatus == "" {
		turnStatus = run.Status
	}
	turnStatus = normalizeTurnStatus(turnStatus)

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := validateRuntimeLeaseBindingTxContext(ctx, tx, sessionDir, run.SessionID, run.ID, RuntimeLeasePurposeRecovery); err != nil {
		return err
	}

	allowed := allowedRunPredecessors(run.Status)
	finished := run.FinishedAt.Format(time.RFC3339Nano)
	changed, err := dao.NewRunDAO(nil).UpdateStatus(ctx, tx, run.ID, run.Status, terminalEvent.Timestamp.Format(time.RFC3339Nano), &finished, run.Error, allowed)
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("recovery target run is no longer active: %s", run.ID)
	}

	insertEvent := func(event SessionRunEvent) error {
		if event.ID == "" || event.EventType == "" {
			return fmt.Errorf("recovery event identity and type are required")
		}
		if event.SessionID == "" {
			event.SessionID = run.SessionID
		}
		if event.RunID == "" {
			event.RunID = run.ID
		}
		if event.SessionID != run.SessionID || event.RunID != run.ID {
			return fmt.Errorf("recovery event identity does not match run")
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = terminalEvent.Timestamp
		}
		return dao.NewRunDAO(nil).InsertEvent(ctx, tx, &dao.SessionRunEventRecord{ID: event.ID, SessionID: event.SessionID, RunID: event.RunID, EventType: event.EventType, Source: event.Source, Status: event.Status, Model: event.Model, Mode: event.Mode, Timestamp: event.Timestamp.Format(time.RFC3339Nano), Data: string(normalizedRunJSON(event.Data))})
	}
	for _, event := range decisionEvents {
		if err := insertEvent(event); err != nil {
			return err
		}
	}
	if err := insertEvent(terminalEvent); err != nil {
		return err
	}

	type openTurn struct {
		id, intentID, runID string
	}
	rows, err := dao.NewRecoveryDAO(nil).ListOpenTurns(ctx, tx, run.SessionID)
	if err != nil {
		return err
	}
	var openTurns []openTurn
	for _, row := range rows {
		turn := openTurn{id: row.ID, intentID: row.IntentID, runID: row.RunID}
		if turn.runID == run.ID || (turn.runID == "" && run.IntentID != "" && turn.intentID == run.IntentID) {
			openTurns = append(openTurns, turn)
		}
	}
	for _, turn := range openTurns {
		parentID, err := currentLeafTxContext(ctx, tx, run.SessionID)
		if err != nil {
			return err
		}
		entry := TurnEndEntry{
			EntryBase: EntryBase{Type: EntryTurnEnd, ID: GenerateID(), ParentID: stringPtr(parentID), Timestamp: terminalEvent.Timestamp},
			TurnID:    turn.id, IntentID: turn.intentID, RunID: run.ID, Status: turnStatus, StopReason: stopReason,
		}
		endSeq, err := appendTurnEntryTxContext(ctx, tx, run.SessionID, entry, parentID)
		if err != nil {
			return err
		}
		if err := dao.NewConversationTurnDAO(nil).Close(ctx, tx, run.SessionID, turn.id, turnStatus, endSeq,
			terminalEvent.Timestamp.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}

	completed := terminalEvent.Timestamp.Unix()
	if err := dao.NewRecoveryDAO(nil).Update(ctx, tx, run.ID, run.SessionID, string(SessionRunRecoveryComplete), "", nil, &completed, &completed); err != nil {
		return fmt.Errorf("session recovery record not found: %s", run.ID)
	}
	return tx.Commit()
}

func updateSessionRunRecovery(sessionDir, sessionID, runID string, state SessionRunRecoveryState, message string, nextRetryAt time.Time) error {
	return updateSessionRunRecoveryContext(context.Background(), sessionDir, sessionID, runID, state, message, nextRetryAt)
}

func updateSessionRunRecoveryContext(ctx context.Context, sessionDir, sessionID, runID string, state SessionRunRecoveryState, message string, nextRetryAt time.Time) error {
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := validateRuntimeLeaseBindingTxContext(ctx, tx, sessionDir, sessionID, runID, RuntimeLeasePurposeRecovery); err != nil {
		return err
	}
	now, err := sqliteNowContext(tx, ctx)
	if err != nil {
		return err
	}
	var next *int64
	if !nextRetryAt.IsZero() {
		value := nextRetryAt.Unix()
		next = &value
	}
	var completed *int64
	if state == SessionRunRecoveryComplete {
		completed = &now
	}
	if err := dao.NewRecoveryDAO(nil).Update(ctx, tx, runID, sessionID, string(state), message, next, &now, completed); err != nil {
		return err
	}
	return tx.Commit()
}

// GetSessionRunRecovery returns the last durable recovery disposition for a
// Run. A missing row is represented by (nil, nil).
func GetSessionRunRecovery(sessionDir, runID string) (*SessionRunRecovery, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("run ID is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	recovery, err := readSessionRunRecoveryTx(db.Bun(), runID)
	if err == dao.ErrNoRows {
		return nil, nil
	}
	return recovery, err
}

func readSessionRunRecoveryTx(executor dao.Executor, runID string) (*SessionRunRecovery, error) {
	record, err := dao.NewRecoveryDAO(nil).Find(context.Background(), executor, runID)
	if err != nil {
		return nil, err
	}
	return recoveryFromRecord(record), nil
}

func readSessionRunRecoveryTxContext(ctx context.Context, executor dao.Executor, runID string) (*SessionRunRecovery, error) {
	record, err := dao.NewRecoveryDAO(nil).Find(ctx, executor, runID)
	if err != nil {
		return nil, err
	}
	return recoveryFromRecord(record), nil
}

func recoveryFromRecord(record *dao.RecoveryRecord) *SessionRunRecovery {
	if record == nil {
		return nil
	}
	recovery := &SessionRunRecovery{RunID: record.RunID, SessionID: record.SessionID, State: SessionRunRecoveryState(record.State), TriggerSource: record.TriggerSource, ReasonCode: record.ReasonCode, Attempt: record.Attempt, PreviousLeaseEpoch: record.PreviousLeaseEpoch, LastError: record.LastError, StartedAt: time.Unix(record.StartedAt, 0).UTC(), UpdatedAt: time.Unix(record.UpdatedAt, 0).UTC()}
	if record.NextRetryAt != nil {
		value := time.Unix(*record.NextRetryAt, 0).UTC()
		recovery.NextRetryAt = &value
	}
	if record.CompletedAt != nil {
		value := time.Unix(*record.CompletedAt, 0).UTC()
		recovery.CompletedAt = &value
	}
	return recovery
}

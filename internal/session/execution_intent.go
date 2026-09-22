package session

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/oschina/mothx/internal/dao"
	"time"
)

// ExecutionIntent is the durable, adapter-neutral record of an accepted user
// request. Request and policy snapshots are opaque to session storage; the
// shared Runtime owns their interpretation.
type ExecutionIntent struct {
	ID                 string
	SessionID          string
	Source             string
	Model              string
	Mode               string
	WorkDir            string
	RequestFingerprint string
	Request            json.RawMessage
	Policy             json.RawMessage
	CreatedAt          time.Time
}

func SaveExecutionIntent(sessionDir string, intent ExecutionIntent) error {
	if intent.ID == "" || intent.SessionID == "" {
		return fmt.Errorf("execution intent ID and session ID are required")
	}
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = time.Now()
	}
	intent.Request = normalizedRunJSON(intent.Request)
	intent.Policy = normalizedRunJSON(intent.Policy)
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRuntimeLeaseTx(tx, sessionDir, intent.SessionID); err != nil {
		return err
	}
	err = dao.NewRunDAO(db.Bun()).InsertIntent(context.Background(), tx, &dao.ExecutionIntentRecord{
		ID: intent.ID, SessionID: intent.SessionID, Source: intent.Source, Model: intent.Model, Mode: intent.Mode,
		WorkDir: intent.WorkDir, RequestFingerprint: intent.RequestFingerprint, RequestJSON: string(intent.Request),
		PolicyJSON: string(intent.Policy), CreatedAt: intent.CreatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// CreateExecutionIntentAndSessionRun atomically admits an immutable execution
// intent with its first or linked Run. Runtime-owned callers use this instead
// of writing the two records independently, so a reconnect can always resolve
// a durable Run back to the request that created it.
func CreateExecutionIntentAndSessionRun(sessionDir string, intent ExecutionIntent, run SessionRun) error {
	_, err := CreateExecutionIntentAndSessionRunEvent(sessionDir, intent, run, SessionRunEvent{})
	return err
}

// CreateExecutionIntentAndSessionRunEvent atomically admits an immutable
// intent, its Run row, and (when supplied) the canonical started event. This
// prevents a process loss between the intent/run write and event publication
// from creating an accepted execution with no replay anchor.
func CreateExecutionIntentAndSessionRunEvent(sessionDir string, intent ExecutionIntent, run SessionRun, event SessionRunEvent) (string, error) {
	return createExecutionIntentAndSessionRunEvent(sessionDir, intent, run, event, nil)
}

// CreateExecutionIntentAndSessionRunEventWithTurn atomically admits an
// immutable intent, its Run/event, and the conversation turn boundary.
func CreateExecutionIntentAndSessionRunEventWithTurn(sessionDir string, intent ExecutionIntent, run SessionRun, event SessionRunEvent, turn ConversationTurn) (string, error) {
	return createExecutionIntentAndSessionRunEvent(sessionDir, intent, run, event, &turn)
}

func createExecutionIntentAndSessionRunEvent(sessionDir string, intent ExecutionIntent, run SessionRun, event SessionRunEvent, turn *ConversationTurn) (string, error) {
	if intent.ID == "" || intent.SessionID == "" {
		return "", fmt.Errorf("execution intent ID and session ID are required")
	}
	if run.ID == "" || run.SessionID == "" || run.Status == "" {
		return "", fmt.Errorf("session run ID, session ID, and status are required")
	}
	if intent.SessionID != run.SessionID {
		return "", fmt.Errorf("execution intent and session run must belong to the same session")
	}
	if run.IntentID == "" {
		run.IntentID = intent.ID
	}
	if run.IntentID != intent.ID {
		return "", fmt.Errorf("session run intent ID does not match execution intent")
	}
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = time.Now()
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = intent.CreatedAt
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = run.StartedAt
	}
	if run.Attempt <= 0 {
		run.Attempt = 1
	}
	intent.Request = normalizedRunJSON(intent.Request)
	intent.Policy = normalizedRunJSON(intent.Policy)
	run.ErrorInfo = normalizedRunJSON(run.ErrorInfo)
	run.Progress = normalizedRunJSON(run.Progress)
	if len(run.Usage) == 0 {
		run.Usage = json.RawMessage(`{}`)
	}
	if len(run.ContextUsage) == 0 {
		run.ContextUsage = json.RawMessage(`{}`)
	}

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return "", err
	}
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRuntimeLeaseTx(tx, sessionDir, run.SessionID); err != nil {
		return "", err
	}
	if err := dao.NewRunDAO(db.Bun()).InsertIntent(context.Background(), tx, &dao.ExecutionIntentRecord{
		ID: intent.ID, SessionID: intent.SessionID, Source: intent.Source, Model: intent.Model, Mode: intent.Mode,
		WorkDir: intent.WorkDir, RequestFingerprint: intent.RequestFingerprint, RequestJSON: string(intent.Request),
		PolicyJSON: string(intent.Policy), CreatedAt: intent.CreatedAt.Format(time.RFC3339Nano),
	}); err != nil {
		return "", fmt.Errorf("create execution intent: %w", err)
	}
	var finished any
	if run.FinishedAt != nil {
		finished = run.FinishedAt.Format(time.RFC3339Nano)
	}
	var finishedAt *string
	if value, ok := finished.(string); ok {
		finishedAt = &value
	}
	if err := dao.NewRunDAO(db.Bun()).InsertRun(context.Background(), tx, sessionRunRecord(&run, finishedAt)); err != nil {
		return "", fmt.Errorf("create session run for execution intent: %w", err)
	}
	if event.EventType != "" {
		if event.ID == "" {
			event.ID = GenerateID()
		}
		if event.SessionID == "" {
			event.SessionID = run.SessionID
		}
		if event.RunID == "" {
			event.RunID = run.ID
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = run.StartedAt
		}
		if event.Status == "" {
			event.Status = run.Status
		}
		if event.Source == "" {
			event.Source = run.Source
		}
		if event.Model == "" {
			event.Model = run.Model
		}
		if event.Mode == "" {
			event.Mode = run.Mode
		}
		if err := dao.NewRunDAO(db.Bun()).InsertEvent(context.Background(), tx, &dao.SessionRunEventRecord{
			ID: event.ID, SessionID: event.SessionID, RunID: event.RunID, EventType: event.EventType,
			Source: event.Source, Status: event.Status, Model: event.Model, Mode: event.Mode,
			Timestamp: event.Timestamp.Format(time.RFC3339Nano), Data: string(normalizedRunJSON(event.Data)),
		}); err != nil {
			return "", fmt.Errorf("create session run started event: %w", err)
		}
	}
	if turn != nil {
		if turn.SessionID != run.SessionID || (turn.RunID != "" && turn.RunID != run.ID) {
			return "", fmt.Errorf("conversation turn identity does not match run")
		}
		if turn.RunID == "" {
			turn.RunID = run.ID
		}
		if turn.IntentID == "" {
			turn.IntentID = intent.ID
		}
		if err := startConversationTurnTx(tx, *turn); err != nil {
			return "", fmt.Errorf("create conversation turn: %w", err)
		}
	}
	if err := appendRunUserMessageTx(tx, run); err != nil {
		return "", fmt.Errorf("create session user entry: %w", err)
	}
	if err := bindInputResourcesToRunTx(tx, run.SessionID, run.ID, run.IntentID, run.InputResourceIDs); err != nil {
		return "", fmt.Errorf("bind input resources to session run: %w", err)
	}
	if err := reserveRuntimeSubmissionTx(tx, run); err != nil {
		return "", fmt.Errorf("reserve runtime submission: %w", err)
	}
	boundLease, err := bindRuntimeLeaseToRunTx(tx, sessionDir, run.SessionID, run.ID)
	if err != nil {
		return "", fmt.Errorf("bind execution lease to session run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	markRuntimeLeaseBound(boundLease, run.ID)
	return event.ID, nil
}

func GetExecutionIntent(sessionDir, intentID string) (*ExecutionIntent, error) {
	if intentID == "" {
		return nil, fmt.Errorf("execution intent ID is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	record, err := dao.NewRunDAO(db.Bun()).FindIntent(context.Background(), intentID)
	if err == dao.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	intent := ExecutionIntent{ID: record.ID, SessionID: record.SessionID, Source: record.Source, Model: record.Model,
		Mode: record.Mode, WorkDir: record.WorkDir, RequestFingerprint: record.RequestFingerprint,
		Request: json.RawMessage(record.RequestJSON), Policy: json.RawMessage(record.PolicyJSON), CreatedAt: parseSessionTimestamp(record.CreatedAt)}
	return &intent, nil
}

func sessionRunRecord(run *SessionRun, finishedAt *string) *dao.SessionRunRecord {
	if run == nil {
		return nil
	}
	return &dao.SessionRunRecord{ID: run.ID, SessionID: run.SessionID, IntentID: run.IntentID, RetryOf: run.RetryOf,
		Attempt: run.Attempt, WorkDir: run.WorkDir, Source: run.Source, Model: run.Model, Mode: run.Mode, Status: run.Status,
		StartedAt: run.StartedAt.Format(time.RFC3339Nano), UpdatedAt: run.UpdatedAt.Format(time.RFC3339Nano), FinishedAt: finishedAt,
		Error: run.Error, ErrorInfoJSON: string(run.ErrorInfo), ProgressJSON: string(run.Progress), UsageJSON: string(run.Usage), ContextUsageJSON: string(run.ContextUsage)}
}

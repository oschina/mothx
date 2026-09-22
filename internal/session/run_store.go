package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

// nonTerminalSessionRunStatusList is the single source of truth for durable
// Run statuses that keep a Session busy. Every other form of this set (SQL
// literals, membership checks, partial unique indexes) derives from it.
var nonTerminalSessionRunStatusList = []string{"created", "queued", "running", "waiting_for_approval", "waiting_for_question", "cancelling", "terminalizing"}

// terminalSessionRunStatusList is the canonical terminal Run status set.
// "expired" is included for parity with fork/reopen/recovery handling even
// though the terminalizer does not currently write it.
var terminalSessionRunStatusList = []string{"completed", "incomplete", "expired", "failed", "cancelled", "canceled", "timed_out"}

// NonTerminalSessionRunStatuses returns the canonical durable statuses that
// keep a Session busy. Callers receive a copy so the shared definition cannot
// be mutated outside this package.
func NonTerminalSessionRunStatuses() []string {
	return append([]string(nil), nonTerminalSessionRunStatusList...)
}

// IsNonTerminalSessionRunStatus reports whether a durable Run still requires
// execution, cancellation, or terminal persistence work.
func IsNonTerminalSessionRunStatus(status string) bool {
	for _, candidate := range nonTerminalSessionRunStatusList {
		if candidate == status {
			return true
		}
	}
	return false
}

// TerminalSessionRunStatuses returns the canonical terminal Run statuses.
// Callers receive a copy so the shared definition cannot be mutated.
func TerminalSessionRunStatuses() []string {
	return append([]string(nil), terminalSessionRunStatusList...)
}

// IsTerminalSessionRunStatus reports whether a durable Run status is terminal.
func IsTerminalSessionRunStatus(status string) bool {
	for _, candidate := range terminalSessionRunStatusList {
		if candidate == status {
			return true
		}
	}
	return false
}

// nonTerminalSessionRunStatusSQL renders the canonical non-terminal status set
// as a SQL IN(...) literal so DDL partial unique indexes stay derived from
// the same source instead of drifting.
func nonTerminalSessionRunStatusSQL() string {
	quoted := make([]string, len(nonTerminalSessionRunStatusList))
	for i, status := range nonTerminalSessionRunStatusList {
		quoted[i] = "'" + status + "'"
	}
	return strings.Join(quoted, ", ")
}

// SessionRun is the durable lifecycle record for one agent execution.
type SessionRun struct {
	ID           string
	SessionID    string
	IntentID     string
	RetryOf      string
	Attempt      int
	WorkDir      string
	Source       string
	Model        string
	Mode         string
	Status       string
	StartedAt    time.Time
	UpdatedAt    time.Time
	FinishedAt   *time.Time
	Error        string
	ErrorInfo    json.RawMessage
	Progress     json.RawMessage
	Usage        json.RawMessage
	ContextUsage json.RawMessage
	// InputResourceIDs are Runtime-prepared resources admitted with this Run.
	// They are bound by the same transaction as the intent/run/start event.
	InputResourceIDs []string
	// Submission fields are admission-only digests. They are reserved in the
	// same transaction as the intent, Run, turn, resources, and start event.
	SubmissionKeyHash     string
	SubmissionScope       string
	SubmissionFingerprint string
	// UserMessage is the canonical user entry admitted with a conversation
	// Run. It is admission-only; transcript replay remains the source of truth.
	UserEntryID string
	UserMessage *provider.Message
	// AssistantMessage is the final assistant entry held by the Runtime until
	// terminalization. It is intentionally transient and is committed together
	// with the terminal Run/turn and delivery plan.
	AssistantEntryID string
	AssistantMessage *provider.Message
	// DeliveryPlan is terminal-only. The terminal transaction creates its
	// outbox rows together with the Run, turn, and terminal event transition.
	DeliveryPlan *DeliveryPlan
}

func SaveSessionRun(sessionDir string, run SessionRun) error {
	if run.ID == "" || run.SessionID == "" {
		return fmt.Errorf("session run ID and session ID are required")
	}
	if run.Status == "" {
		return fmt.Errorf("session run status is required")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = run.StartedAt
	}
	if len(run.Usage) == 0 {
		run.Usage = json.RawMessage(`{}`)
	}
	if len(run.ContextUsage) == 0 {
		run.ContextUsage = json.RawMessage(`{}`)
	}
	if run.Attempt <= 0 {
		run.Attempt = 1
	}
	run.ErrorInfo = normalizedRunJSON(run.ErrorInfo)
	run.Progress = normalizedRunJSON(run.Progress)
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	var finished any
	if run.FinishedAt != nil {
		finished = run.FinishedAt.Format(time.RFC3339Nano)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateRuntimeLeaseTx(tx, sessionDir, run.SessionID); err != nil {
		return err
	}
	var finishedAt *string
	if value, ok := finished.(string); ok {
		finishedAt = &value
	}
	err = dao.NewRunDAO(nil).UpsertRun(context.Background(), tx, sessionRunRecord(&run, finishedAt))
	if err != nil {
		return err
	}
	if err := appendRunUserMessageTx(tx, run); err != nil {
		return err
	}
	if err := bindInputResourcesToRunTx(tx, run.SessionID, run.ID, run.IntentID, run.InputResourceIDs); err != nil {
		return err
	}
	if err := reserveRuntimeSubmissionTx(tx, run); err != nil {
		return err
	}
	var boundLease *runtimeLease
	if IsNonTerminalSessionRunStatus(run.Status) {
		boundLease, err = bindRuntimeLeaseToRunTx(tx, sessionDir, run.SessionID, run.ID)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	markRuntimeLeaseBound(boundLease, run.ID)
	return nil
}

// CreateSessionRun inserts one canonical run row. Unlike SaveSessionRun, this
// method never overwrites an existing identity; Runtime-owned lifecycle code
// must treat duplicate run IDs as an admission error.
func CreateSessionRun(sessionDir string, run SessionRun) error {
	if run.ID == "" || run.SessionID == "" {
		return fmt.Errorf("session run ID and session ID are required")
	}
	if run.Status == "" {
		return fmt.Errorf("session run status is required")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = run.StartedAt
	}
	if len(run.Usage) == 0 {
		run.Usage = json.RawMessage(`{}`)
	}
	if len(run.ContextUsage) == 0 {
		run.ContextUsage = json.RawMessage(`{}`)
	}
	if run.Attempt <= 0 {
		run.Attempt = 1
	}
	run.ErrorInfo = normalizedRunJSON(run.ErrorInfo)
	run.Progress = normalizedRunJSON(run.Progress)
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	var finished any
	if run.FinishedAt != nil {
		finished = run.FinishedAt.Format(time.RFC3339Nano)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateRuntimeLeaseTx(tx, sessionDir, run.SessionID); err != nil {
		return err
	}
	var finishedAt *string
	if value, ok := finished.(string); ok {
		finishedAt = &value
	}
	err = dao.NewRunDAO(nil).InsertRun(context.Background(), tx, sessionRunRecord(&run, finishedAt))
	if err != nil {
		return err
	}
	if err := appendRunUserMessageTx(tx, run); err != nil {
		return err
	}
	if err := bindInputResourcesToRunTx(tx, run.SessionID, run.ID, run.IntentID, run.InputResourceIDs); err != nil {
		return err
	}
	if err := reserveRuntimeSubmissionTx(tx, run); err != nil {
		return err
	}
	var boundLease *runtimeLease
	if IsNonTerminalSessionRunStatus(run.Status) {
		boundLease, err = bindRuntimeLeaseToRunTx(tx, sessionDir, run.SessionID, run.ID)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	markRuntimeLeaseBound(boundLease, run.ID)
	return nil
}

// CreateSessionRunAndEvent atomically inserts a new canonical Run and its
// first event. Retry attempts use this path so a process loss cannot leave a
// durable attempt without a replay anchor.
func CreateSessionRunAndEvent(sessionDir string, run SessionRun, event SessionRunEvent) (string, error) {
	return createSessionRunAndEvent(sessionDir, run, event, nil)
}

// CreateSessionRunAndEventWithTurn atomically admits a Run, its first event,
// and a conversation turn boundary when the Run produces transcript output.
func CreateSessionRunAndEventWithTurn(sessionDir string, run SessionRun, event SessionRunEvent, turn ConversationTurn) (string, error) {
	return createSessionRunAndEvent(sessionDir, run, event, &turn)
}

// FinishSessionRunAndConversationTurn atomically closes a conversation turn,
// its Run row, and the terminal Run event. A missing or already-closed turn is
// tolerated for recovery/idempotent retries because an Agent may already have
// emitted the boundary before Runtime terminalization.
func FinishSessionRunAndConversationTurn(sessionDir string, run SessionRun, event SessionRunEvent, turnID, turnStatus, stopReason string) (string, error) {
	if run.ID == "" || run.SessionID == "" || run.Status == "" {
		return "", fmt.Errorf("session run identity and terminal status are required")
	}
	if event.EventType == "" {
		return "", fmt.Errorf("session run event type is required")
	}
	if event.ID == "" {
		event.ID = RunTerminalEventID(run.ID, event.EventType)
		if event.ID == "" {
			event.ID = GenerateID()
		}
	}
	event.SessionID, event.RunID = run.SessionID, run.ID
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.Status == "" {
		event.Status = run.Status
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
	allowed := allowedRunPredecessors(run.Status)
	finished := ""
	if run.FinishedAt != nil {
		finished = run.FinishedAt.Format(time.RFC3339Nano)
	}
	var finishedPtr *string
	if finished != "" {
		finishedPtr = &finished
	}
	changed, err := dao.NewRunDAO(nil).UpdateStatus(context.Background(), tx, run.ID, run.Status, time.Now().Format(time.RFC3339Nano), finishedPtr, run.Error, allowed)
	if err != nil {
		return "", err
	}
	if changed == 0 {
		record, err := dao.NewRunDAO(nil).FindRun(context.Background(), tx, run.ID)
		if err != nil {
			return "", err
		}
		if record.Status != run.Status {
			return "", fmt.Errorf("invalid session run transition %q -> %q", record.Status, run.Status)
		}
	}
	if err := appendRunAssistantMessageTx(tx, run); err != nil {
		return "", err
	}
	if err := dao.NewRunDAO(nil).InsertEvent(context.Background(), tx, &dao.SessionRunEventRecord{ID: event.ID, SessionID: event.SessionID,
		RunID: event.RunID, EventType: event.EventType, Source: event.Source, Status: event.Status, Model: event.Model, Mode: event.Mode,
		Timestamp: event.Timestamp.Format(time.RFC3339Nano), Data: string(normalizedRunJSON(event.Data))}); err != nil {
		return "", err
	}
	if turnID != "" {
		state, err := dao.NewConversationTurnDAO(nil).State(context.Background(), tx, run.SessionID, turnID)
		if err == nil && state.Status == "open" {
			parentID, err := currentLeafTx(tx, run.SessionID)
			if err != nil {
				return "", err
			}
			entry := TurnEndEntry{EntryBase: EntryBase{Type: EntryTurnEnd, ID: GenerateID(), ParentID: stringPtr(parentID), Timestamp: event.Timestamp}, TurnID: turnID, IntentID: state.IntentID, RunID: state.RunID, Status: turnStatus, StopReason: stopReason}
			endSeq, err := appendTurnEntryTx(tx, run.SessionID, entry, parentID)
			if err != nil {
				return "", err
			}
			if err := dao.NewConversationTurnDAO(nil).Close(context.Background(), tx, run.SessionID, turnID, turnStatus, endSeq, event.Timestamp.Format(time.RFC3339Nano)); err != nil {
				return "", err
			}
		} else if err != nil && err != dao.ErrNoRows {
			return "", err
		}
	}
	if run.DeliveryPlan != nil {
		plan := *run.DeliveryPlan
		if plan.Intent.SessionID == "" {
			plan.Intent.SessionID = run.SessionID
		}
		if plan.Intent.RunID == "" {
			plan.Intent.RunID = run.ID
		}
		if plan.Intent.SessionID != run.SessionID || plan.Intent.RunID != run.ID {
			return "", fmt.Errorf("delivery plan identity does not match terminal Run")
		}
		if err := createDeliveryPlanTx(context.Background(), tx, plan); err != nil {
			return "", fmt.Errorf("create terminal delivery plan: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return event.ID, nil
}

func createSessionRunAndEvent(sessionDir string, run SessionRun, event SessionRunEvent, turn *ConversationTurn) (string, error) {
	if run.ID == "" || run.SessionID == "" {
		return "", fmt.Errorf("session run ID and session ID are required")
	}
	if run.Status == "" {
		return "", fmt.Errorf("session run status is required")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = run.StartedAt
	}
	if len(run.Usage) == 0 {
		run.Usage = json.RawMessage(`{}`)
	}
	if len(run.ContextUsage) == 0 {
		run.ContextUsage = json.RawMessage(`{}`)
	}
	if run.Attempt <= 0 {
		run.Attempt = 1
	}
	run.ErrorInfo = normalizedRunJSON(run.ErrorInfo)
	run.Progress = normalizedRunJSON(run.Progress)
	if event.EventType == "" {
		return "", fmt.Errorf("session run event type is required")
	}
	if event.ID == "" {
		event.ID = GenerateID()
	}
	if event.SessionID == "" {
		event.SessionID = run.SessionID
	}
	if event.RunID == "" {
		event.RunID = run.ID
	}
	if event.SessionID != run.SessionID || event.RunID != run.ID {
		return "", fmt.Errorf("session run event identity does not match run")
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
	var finishedAt *string
	if run.FinishedAt != nil {
		value := run.FinishedAt.Format(time.RFC3339Nano)
		finishedAt = &value
	}
	if err := dao.NewRunDAO(nil).InsertRun(context.Background(), tx, sessionRunRecord(&run, finishedAt)); err != nil {
		return "", err
	}
	if err := dao.NewRunDAO(nil).InsertEvent(context.Background(), tx, &dao.SessionRunEventRecord{ID: event.ID, SessionID: event.SessionID,
		RunID: event.RunID, EventType: event.EventType, Source: event.Source, Status: event.Status, Model: event.Model, Mode: event.Mode,
		Timestamp: event.Timestamp.Format(time.RFC3339Nano), Data: string(normalizedRunJSON(event.Data))}); err != nil {
		return "", err
	}
	if turn != nil {
		if turn.SessionID != run.SessionID || (turn.RunID != "" && turn.RunID != run.ID) {
			return "", fmt.Errorf("conversation turn identity does not match run")
		}
		if turn.RunID == "" {
			turn.RunID = run.ID
		}
		if turn.IntentID == "" {
			turn.IntentID = run.IntentID
		}
		if err := startConversationTurnTx(tx, *turn); err != nil {
			return "", err
		}
	}
	if err := appendRunUserMessageTx(tx, run); err != nil {
		return "", err
	}
	if err := bindInputResourcesToRunTx(tx, run.SessionID, run.ID, run.IntentID, run.InputResourceIDs); err != nil {
		return "", err
	}
	if err := reserveRuntimeSubmissionTx(tx, run); err != nil {
		return "", err
	}
	boundLease, err := bindRuntimeLeaseToRunTx(tx, sessionDir, run.SessionID, run.ID)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	markRuntimeLeaseBound(boundLease, run.ID)
	return event.ID, nil
}

func scanSessionRun(scanner interface{ Scan(...any) error }) (*SessionRun, error) {
	var run SessionRun
	var started, updated, errorInfo, progress, usage, contextUsage string
	var finished dao.NullString
	if err := scanner.Scan(&run.ID, &run.SessionID, &run.IntentID, &run.RetryOf, &run.Attempt, &run.WorkDir, &run.Source, &run.Model, &run.Mode, &run.Status, &started, &updated, &finished, &run.Error, &errorInfo, &progress, &usage, &contextUsage); err != nil {
		return nil, err
	}
	run.StartedAt = parseSessionTimestamp(started)
	run.UpdatedAt = parseSessionTimestamp(updated)
	if finished.Valid && finished.String != "" {
		value := parseSessionTimestamp(finished.String)
		run.FinishedAt = &value
	}
	run.Usage = json.RawMessage(usage)
	run.ErrorInfo = json.RawMessage(errorInfo)
	run.Progress = json.RawMessage(progress)
	run.ContextUsage = json.RawMessage(contextUsage)
	return &run, nil
}

func sessionRunFromRecord(record *dao.SessionRunRecord) SessionRun {
	if record == nil {
		return SessionRun{}
	}
	run := SessionRun{ID: record.ID, SessionID: record.SessionID, IntentID: record.IntentID, RetryOf: record.RetryOf,
		Attempt: record.Attempt, WorkDir: record.WorkDir, Source: record.Source, Model: record.Model, Mode: record.Mode,
		Status: record.Status, StartedAt: parseSessionTimestamp(record.StartedAt), UpdatedAt: parseSessionTimestamp(record.UpdatedAt),
		Error: record.Error, ErrorInfo: json.RawMessage(record.ErrorInfoJSON), Progress: json.RawMessage(record.ProgressJSON),
		Usage: json.RawMessage(record.UsageJSON), ContextUsage: json.RawMessage(record.ContextUsageJSON)}
	if record.FinishedAt != nil && *record.FinishedAt != "" {
		finished := parseSessionTimestamp(*record.FinishedAt)
		run.FinishedAt = &finished
	}
	return run
}

func GetSessionRun(sessionDir, runID string) (*SessionRun, error) {
	return GetSessionRunContext(context.Background(), sessionDir, runID)
}

func GetSessionRunContext(ctx context.Context, sessionDir, runID string) (*SessionRun, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == "" {
		return nil, fmt.Errorf("run ID is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	record, err := dao.NewRunDAO(db.Bun()).FindRun(ctx, db.Bun(), runID)
	if err == dao.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	runs := []SessionRun{sessionRunFromRecord(record)}
	if err := loadInputResourceIDs(ctx, db, runs); err != nil {
		return nil, err
	}
	return &runs[0], nil
}

func GetActiveSessionRun(sessionDir, sessionID string) (*SessionRun, error) {
	return GetActiveSessionRunContext(context.Background(), sessionDir, sessionID)
}

func GetActiveSessionRunContext(ctx context.Context, sessionDir, sessionID string) (*SessionRun, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return nil, nil
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	record, err := dao.NewRunDAO(db.Bun()).ActiveRun(ctx, sessionID, NonTerminalSessionRunStatuses())
	if err == dao.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return GetSessionRunContext(ctx, sessionDir, record.ID)
}

func ListSessionRuns(sessionDir, sessionID string, limit int) ([]SessionRun, error) {
	if sessionID == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewRunDAO(db.Bun()).ListRuns(context.Background(), sessionID, limit)
	if err != nil {
		return nil, err
	}
	var result []SessionRun
	for _, record := range records {
		result = append(result, sessionRunFromRecord(&record))
	}
	// rows are exhausted (the single pooled connection is released at EOF), so
	// issuing per-run queries now cannot self-deadlock the MaxOpenConns(1) pool.
	if err := loadInputResourceIDs(context.Background(), db, result); err != nil {
		return nil, err
	}
	return result, nil
}

// ListLatestSessionRuns returns the most recent durable Run of each given
// session as a read-only projection keyed by session ID. Sessions without any
// Run are absent from the result. Adapters use it to project sidebar run
// status (for example ACP session/list lastRun); Run lifecycle writes remain
// owned by the Runtime and all SQL stays in the DAO.
func ListLatestSessionRuns(ctx context.Context, sessionDir string, sessionIDs []string) (map[string]SessionRun, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := make(map[string]SessionRun)
	if len(sessionIDs) == 0 {
		return result, nil
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return result, err
	}
	records, err := dao.NewRunDAO(db.Bun()).LatestRunBySessions(ctx, sessionIDs)
	if err != nil {
		return nil, err
	}
	for sessionID, record := range records {
		row := record
		result[sessionID] = sessionRunFromRecord(&row)
	}
	return result, nil
}

func loadInputResourceIDs(ctx context.Context, db *dao.Database, runs []SessionRun) error {
	if len(runs) == 0 {
		return nil
	}
	sessionID := runs[0].SessionID
	byRun, err := dao.NewRunDAO(db.Bun()).InputResourceIDs(ctx, sessionID)
	if err != nil {
		return err
	}
	for i := range runs {
		runs[i].InputResourceIDs = byRun[runs[i].ID]
	}
	return nil
}

// NextSessionRunAttempt returns the next ordered user-visible attempt for an
// ExecutionIntent. Callers must hold their Runtime admission lock while using
// the returned value and creating the Run, so two retry commands cannot select
// the same attempt number.
func NextSessionRunAttempt(sessionDir, sessionID, intentID string) (int, error) {
	if sessionID == "" || intentID == "" {
		return 0, fmt.Errorf("session ID and execution intent ID are required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return 0, err
	}
	attempt, err := dao.NewRunDAO(db.Bun()).NextAttempt(context.Background(), sessionID, intentID)
	if err != nil {
		return 0, err
	}
	if attempt < 2 {
		attempt = 2
	}
	return attempt, nil
}

// LatestSessionRunForIntent returns the highest-attempt Run in an immutable
// intent chain. Retry admission uses it to prevent two callers from retrying
// an older terminal attempt after a newer attempt already exists.
func LatestSessionRunForIntent(sessionDir, sessionID, intentID string) (*SessionRun, error) {
	if sessionID == "" || intentID == "" {
		return nil, fmt.Errorf("session ID and execution intent ID are required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	record, err := dao.NewRunDAO(db.Bun()).LatestForIntent(context.Background(), sessionID, intentID)
	if err == dao.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return GetSessionRun(sessionDir, record.ID)
}

func UpdateSessionRunStatus(sessionDir, runID, status, message string, finishedAt *time.Time) error {
	if runID == "" || status == "" {
		return fmt.Errorf("run ID and status are required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	var finished any
	finishedValue := ""
	if finishedAt != nil {
		finished = finishedAt.Format(time.RFC3339Nano)
		finishedValue = finished.(string)
	}
	allowed := allowedRunPredecessors(status)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sessionID, err := dao.NewRunDAO(nil).SessionID(context.Background(), tx, runID)
	if err != nil {
		return err
	}
	if err := validateRuntimeLeaseTx(tx, sessionDir, sessionID); err != nil {
		return err
	}
	var finishedPtr *string
	if finishedValue != "" {
		finishedPtr = &finishedValue
	}
	changed, err := dao.NewRunDAO(nil).UpdateStatus(context.Background(), tx, runID, status, time.Now().Format(time.RFC3339Nano), finishedPtr, message, allowed)
	if err != nil {
		return err
	}
	if changed == 0 {
		record, getErr := dao.NewRunDAO(nil).FindRun(context.Background(), tx, runID)
		if getErr == dao.ErrNoRows {
			return dao.ErrNoRows
		}
		if getErr != nil {
			return getErr
		}
		current := sessionRunFromRecord(record)
		if current.Status == status {
			return nil
		}
		return fmt.Errorf("invalid session run transition %q -> %q", current.Status, status)
	}
	return tx.Commit()
}

// AnnotateSessionRunError records a terminal error reason on a run row that
// reached a terminal status without one (for example a background run
// abandoned after interrupted tool execution). It never changes the run
// status and is a no-op when the run already carries an error, so an earlier
// finalizer stays authoritative. It reports whether the annotation was
// applied.
func AnnotateSessionRunError(sessionDir, runID, message string) (bool, error) {
	if runID == "" {
		return false, fmt.Errorf("run ID is required")
	}
	if strings.TrimSpace(message) == "" {
		return false, nil
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return false, err
	}
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	sessionID, err := dao.NewRunDAO(nil).SessionID(context.Background(), tx, runID)
	if err != nil {
		if err == dao.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if err := validateRuntimeLeaseTx(tx, sessionDir, sessionID); err != nil {
		return false, err
	}
	changed, err := dao.NewRunDAO(nil).UpdateErrorIfEmpty(context.Background(), tx, runID, message, time.Now().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	if changed == 0 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateSessionRunErrorInfo stores the structured terminal/recovery error
// independently of the compatibility Error summary column.
func UpdateSessionRunErrorInfo(sessionDir, runID string, info json.RawMessage) error {
	if runID == "" {
		return fmt.Errorf("run ID is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sessionID, err := dao.NewRunDAO(nil).SessionID(context.Background(), tx, runID)
	if err != nil {
		return err
	}
	if err := validateRuntimeLeaseTx(tx, sessionDir, sessionID); err != nil {
		return err
	}
	if err := dao.NewRunDAO(nil).UpdateJSON(context.Background(), tx, runID, "error_info_json", string(normalizedRunJSON(info)), time.Now().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateSessionRunProgress persists the latest non-terminal retry/recovery
// projection. Terminal callers should clear it with an empty object.
func UpdateSessionRunProgress(sessionDir, runID string, progress json.RawMessage) error {
	if runID == "" {
		return fmt.Errorf("run ID is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sessionID, err := dao.NewRunDAO(nil).SessionID(context.Background(), tx, runID)
	if err != nil {
		return err
	}
	if err := validateRuntimeLeaseTx(tx, sessionDir, sessionID); err != nil {
		return err
	}
	if err := dao.NewRunDAO(nil).UpdateJSON(context.Background(), tx, runID, "progress_json", string(normalizedRunJSON(progress)), time.Now().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateSessionRunUsage persists token and context-window usage independently
// from terminalization so reconnects can inspect partial or recovered runs.
func UpdateSessionRunUsage(sessionDir, runID string, usage, contextUsage json.RawMessage) error {
	if runID == "" {
		return fmt.Errorf("run ID is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sessionID, err := dao.NewRunDAO(nil).SessionID(context.Background(), tx, runID)
	if err != nil {
		return err
	}
	if err := validateRuntimeLeaseTx(tx, sessionDir, sessionID); err != nil {
		return err
	}
	if err := dao.NewRunDAO(nil).UpdateJSON(context.Background(), tx, runID, "usage_json", string(normalizedRunJSON(usage)), time.Now().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err := dao.NewRunDAO(nil).UpdateJSON(context.Background(), tx, runID, "context_usage_json", string(normalizedRunJSON(contextUsage)), time.Now().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func normalizedRunJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || !json.Valid(value) {
		return json.RawMessage(`{}`)
	}
	return value
}

func allowedRunPredecessors(status string) []string {
	switch status {
	case "created":
		return []string{"created"}
	case "queued":
		return []string{"created", "queued"}
	case "running":
		return []string{"created", "queued", "running", "waiting_for_approval", "waiting_for_question"}
	case "waiting_for_approval", "waiting_for_question":
		return []string{"running", status}
	case "cancelling":
		return []string{"created", "queued", "running", "waiting_for_approval", "waiting_for_question", "cancelling"}
	case "terminalizing":
		return []string{"created", "queued", "running", "waiting_for_approval", "waiting_for_question", "cancelling", "terminalizing"}
	}
	if IsTerminalSessionRunStatus(status) {
		return []string{"created", "queued", "running", "waiting_for_approval", "waiting_for_question", "cancelling", "terminalizing", status}
	}
	return []string{status}
}

// ListOrphanedSessionRuns returns all runs that are in a non-terminal state.
// This is used during server startup to recover runs that were active when
// the previous server instance stopped.
func ListOrphanedSessionRuns(sessionDir string) ([]SessionRun, error) {
	return ListOrphanedSessionRunsContext(context.Background(), sessionDir)
}

func ListOrphanedSessionRunsContext(ctx context.Context, sessionDir string) ([]SessionRun, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewRunDAO(db.Bun()).Orphaned(ctx, NonTerminalSessionRunStatuses())
	if err != nil {
		return nil, err
	}
	var result []SessionRun
	for _, record := range records {
		result = append(result, sessionRunFromRecord(&record))
	}
	return result, nil
}

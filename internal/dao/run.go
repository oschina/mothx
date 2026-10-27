package dao

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"
)

type ExecutionIntentRecord struct {
	bun.BaseModel      `bun:"table:session_execution_intents"`
	ID                 string `bun:"id,pk"`
	SessionID          string `bun:"session_id"`
	Source             string `bun:"source"`
	Model              string `bun:"model"`
	Mode               string `bun:"mode"`
	WorkDir            string `bun:"work_dir"`
	RequestFingerprint string `bun:"request_fingerprint"`
	RequestJSON        string `bun:"request_json"`
	PolicyJSON         string `bun:"policy_json"`
	CreatedAt          string `bun:"created_at"`
}

type SessionRunRecord struct {
	bun.BaseModel    `bun:"table:session_runs"`
	ID               string  `bun:"id,pk"`
	SessionID        string  `bun:"session_id"`
	IntentID         string  `bun:"intent_id"`
	RetryOf          string  `bun:"retry_of"`
	Attempt          int     `bun:"attempt"`
	WorkDir          string  `bun:"work_dir"`
	Source           string  `bun:"source"`
	Model            string  `bun:"model"`
	Mode             string  `bun:"mode"`
	Status           string  `bun:"status"`
	StartedAt        string  `bun:"started_at"`
	UpdatedAt        string  `bun:"updated_at"`
	FinishedAt       *string `bun:"finished_at,nullzero"`
	Error            string  `bun:"error"`
	ErrorInfoJSON    string  `bun:"error_info_json"`
	ProgressJSON     string  `bun:"progress_json"`
	UsageJSON        string  `bun:"usage_json"`
	ContextUsageJSON string  `bun:"context_usage_json"`
}

type SessionRunEventRecord struct {
	bun.BaseModel `bun:"table:session_run_events"`
	Seq           int64  `bun:"seq"`
	ID            string `bun:"id,pk"`
	SessionID     string `bun:"session_id"`
	RunID         string `bun:"run_id"`
	EventType     string `bun:"event_type"`
	Source        string `bun:"source"`
	Status        string `bun:"status"`
	Model         string `bun:"model"`
	Mode          string `bun:"mode"`
	Timestamp     string `bun:"timestamp"`
	Data          string `bun:"data"`
}

type RunDAO struct{ db *bun.DB }

func NewRunDAO(db *bun.DB) *RunDAO { return &RunDAO{db: db} }

func (d *RunDAO) InsertIntent(ctx context.Context, executor bun.IDB, record *ExecutionIntentRecord) error {
	_, err := executor.NewInsert().Model(record).Exec(ctx)
	return err
}

func (d *RunDAO) FindIntent(ctx context.Context, intentID string) (*ExecutionIntentRecord, error) {
	record := new(ExecutionIntentRecord)
	err := d.db.NewSelect().Model(record).Where("id = ?", intentID).Limit(1).Scan(ctx)
	return record, err
}

func (d *RunDAO) InsertRun(ctx context.Context, executor bun.IDB, record *SessionRunRecord) error {
	_, err := executor.NewInsert().Model(record).Exec(ctx)
	return err
}

func (d *RunDAO) UpsertRun(ctx context.Context, executor bun.IDB, record *SessionRunRecord) error {
	_, err := executor.NewInsert().Model(record).On("CONFLICT(id) DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at, finished_at = excluded.finished_at, error = excluded.error, error_info_json = excluded.error_info_json, progress_json = excluded.progress_json, usage_json = excluded.usage_json, context_usage_json = excluded.context_usage_json").Exec(ctx)
	return err
}

func (d *RunDAO) InsertEvent(ctx context.Context, executor bun.IDB, record *SessionRunEventRecord) error {
	_, err := executor.NewInsert().Model(record).ExcludeColumn("seq").On("CONFLICT(id) DO NOTHING").Exec(ctx)
	return err
}

func (d *RunDAO) FindRun(ctx context.Context, executor bun.IDB, runID string) (*SessionRunRecord, error) {
	record := new(SessionRunRecord)
	err := executor.NewSelect().Model(record).Where("id = ?", runID).Limit(1).Scan(ctx)
	return record, err
}

func (d *RunDAO) ListRuns(ctx context.Context, sessionID string, limit int) ([]SessionRunRecord, error) {
	var records []SessionRunRecord
	err := d.db.NewSelect().Model(&records).Where("session_id = ?", sessionID).OrderExpr("started_at DESC").Limit(limit).Scan(ctx)
	return records, err
}

// LatestRunBySessions returns the most recent run row for each of the given
// sessions in one query, keyed by session ID. "Most recent" is the run with the
// greatest started_at, ties broken by the greatest rowid so the selection is
// deterministic. It backs read-only sidebar projections (ACP session/list
// lastRun); lifecycle writes stay owned by ExecutionRuntime/RunStore.
func (d *RunDAO) LatestRunBySessions(ctx context.Context, sessionIDs []string) (map[string]SessionRunRecord, error) {
	result := make(map[string]SessionRunRecord)
	if len(sessionIDs) == 0 {
		return result, nil
	}
	var records []SessionRunRecord
	// Rank the rows of the requested sessions and keep the newest per session.
	// The window ordering (started_at DESC, rowid DESC) is stable even when two
	// runs share a timestamp. The outer column list mirrors SessionRunRecord
	// and drops the ranking helper column.
	err := d.db.NewRaw(`SELECT id, session_id, intent_id, retry_of, attempt, work_dir, source, model, mode, status,
		started_at, updated_at, finished_at, error, error_info_json, progress_json, usage_json, context_usage_json
		FROM (SELECT *, ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY started_at DESC, rowid DESC) AS run_rank
		FROM session_runs WHERE session_id IN (?)) AS ranked
		WHERE run_rank = 1 ORDER BY session_id ASC`, bun.In(sessionIDs)).Scan(ctx, &records)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		result[record.SessionID] = record
	}
	return result, nil
}

func (d *RunDAO) ActiveRun(ctx context.Context, sessionID string, statuses []string) (*SessionRunRecord, error) {
	record := new(SessionRunRecord)
	err := d.db.NewSelect().Model(record).Where("session_id = ?", sessionID).Where("status IN (?)", bun.In(statuses)).OrderExpr("started_at DESC").Limit(1).Scan(ctx)
	return record, err
}

func (d *RunDAO) Orphaned(ctx context.Context, statuses []string) ([]SessionRunRecord, error) {
	return d.OrphanedFrom(ctx, d.db, statuses)
}

func (d *RunDAO) OrphanedFrom(ctx context.Context, executor bun.IDB, statuses []string) ([]SessionRunRecord, error) {
	var records []SessionRunRecord
	err := executor.NewSelect().Model(&records).Where("status IN (?)", bun.In(statuses)).OrderExpr("started_at ASC").Scan(ctx)
	return records, err
}

// ActiveBySessions returns every non-terminal run belonging to the requested
// sessions in one query. Ordering matches OrphanedFrom (started_at ASC) so a
// batch projection of ActiveRuns keeps the single-session read's ordering.
func (d *RunDAO) ActiveBySessions(ctx context.Context, executor bun.IDB, sessionIDs, statuses []string) ([]SessionRunRecord, error) {
	if len(sessionIDs) == 0 || len(statuses) == 0 {
		return nil, nil
	}
	var records []SessionRunRecord
	err := executor.NewSelect().Model(&records).
		Where("session_id IN (?)", bun.In(sessionIDs)).
		Where("status IN (?)", bun.In(statuses)).
		OrderExpr("started_at ASC").Scan(ctx)
	return records, err
}

func (d *RunDAO) InputResourceIDs(ctx context.Context, sessionID string) (map[string][]string, error) {
	var rows []struct {
		RunID string `bun:"run_id"`
		ID    string `bun:"id"`
	}
	err := d.db.NewSelect().Table("input_resources").Column("run_id", "id").Where("session_id = ?", sessionID).OrderExpr("created_at ASC, id ASC").Scan(ctx, &rows)
	result := make(map[string][]string)
	for _, row := range rows {
		if row.RunID != "" && row.ID != "" {
			result[row.RunID] = append(result[row.RunID], row.ID)
		}
	}
	return result, err
}

func (d *RunDAO) NextAttempt(ctx context.Context, sessionID, intentID string) (int, error) {
	var attempt int
	err := d.db.NewSelect().Table("session_runs").ColumnExpr("COALESCE(MAX(attempt), 0) + 1").Where("session_id = ? AND intent_id = ?", sessionID, intentID).Scan(ctx, &attempt)
	return attempt, err
}

func (d *RunDAO) LatestForIntent(ctx context.Context, sessionID, intentID string) (*SessionRunRecord, error) {
	record := new(SessionRunRecord)
	err := d.db.NewSelect().Model(record).Where("session_id = ? AND intent_id = ?", sessionID, intentID).OrderExpr("attempt DESC, started_at DESC").Limit(1).Scan(ctx)
	return record, err
}

func (d *RunDAO) SessionID(ctx context.Context, executor bun.IDB, runID string) (string, error) {
	var sessionID string
	err := executor.NewSelect().Table("session_runs").Column("session_id").Where("id = ?", runID).Limit(1).Scan(ctx, &sessionID)
	return sessionID, err
}

func (d *RunDAO) UpdateStatus(ctx context.Context, executor bun.IDB, runID, status, updatedAt string, finishedAt *string, message string, predecessors []string) (int64, error) {
	query := executor.NewUpdate().Model((*SessionRunRecord)(nil)).Set("status = ?", status).Set("updated_at = ?", updatedAt).Set("finished_at = ?", finishedAt).Set("error = ?", message).Where("id = ?", runID)
	if len(predecessors) > 0 {
		query.Where("status IN (?)", bun.In(predecessors))
	}
	result, err := query.Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *RunDAO) UpdateJSON(ctx context.Context, executor bun.IDB, runID, column, value, updatedAt string) error {
	_, err := executor.NewUpdate().Model((*SessionRunRecord)(nil)).Set(column+" = ?", value).Set("updated_at = ?", updatedAt).Where("id = ?", runID).Exec(ctx)
	return err
}

// UpdateErrorIfEmpty sets the error message only while the stored error is
// still empty, preserving any reason an earlier finalizer already recorded.
// It never touches the run status and reports how many rows changed.
func (d *RunDAO) UpdateErrorIfEmpty(ctx context.Context, executor bun.IDB, runID, message, updatedAt string) (int64, error) {
	result, err := executor.NewUpdate().Model((*SessionRunRecord)(nil)).
		Set("error = ?", message).
		Set("updated_at = ?", updatedAt).
		Where("id = ?", runID).
		Where("(error IS NULL OR error = '')").
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func IsNoRowsRun(err error) bool { return err == sql.ErrNoRows }

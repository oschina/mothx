package dao

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"
)

type RecoveryRecord struct {
	bun.BaseModel      `bun:"table:session_run_recoveries"`
	RunID              string `bun:"run_id,pk"`
	SessionID          string `bun:"session_id"`
	State              string `bun:"state"`
	TriggerSource      string `bun:"trigger_source"`
	ReasonCode         string `bun:"reason_code"`
	Attempt            int    `bun:"attempt"`
	PreviousLeaseEpoch int64  `bun:"previous_lease_epoch"`
	LastError          string `bun:"last_error"`
	NextRetryAt        *int64 `bun:"next_retry_at,nullzero"`
	StartedAt          int64  `bun:"started_at"`
	UpdatedAt          int64  `bun:"updated_at"`
	CompletedAt        *int64 `bun:"completed_at,nullzero"`
}

type RecoveryDAO struct{ db *bun.DB }
type OpenTurnRecord struct{ ID, IntentID, RunID string }

func NewRecoveryDAO(db *bun.DB) *RecoveryDAO { return &RecoveryDAO{db: db} }

func (d *RecoveryDAO) Upsert(ctx context.Context, executor bun.IDB, record *RecoveryRecord) error {
	_, err := executor.NewInsert().Model(record).On("CONFLICT(run_id) DO UPDATE SET session_id = excluded.session_id, state = excluded.state, trigger_source = excluded.trigger_source, reason_code = excluded.reason_code, attempt = attempt + 1, previous_lease_epoch = excluded.previous_lease_epoch, last_error = '', next_retry_at = NULL, started_at = excluded.started_at, updated_at = excluded.updated_at, completed_at = NULL").Exec(ctx)
	return err
}

func (d *RecoveryDAO) Find(ctx context.Context, executor bun.IDB, runID string) (*RecoveryRecord, error) {
	record := new(RecoveryRecord)
	err := executor.NewSelect().Model(record).Where("run_id = ?", runID).Limit(1).Scan(ctx)
	return record, err
}

func (d *RecoveryDAO) Update(ctx context.Context, executor bun.IDB, runID, sessionID, state, lastError string, nextRetryAt, updatedAt, completedAt *int64) error {
	query := executor.NewUpdate().Model((*RecoveryRecord)(nil)).Set("state = ?", state).Set("last_error = ?", lastError).Set("next_retry_at = ?", nextRetryAt).Set("updated_at = ?", updatedAt).Set("completed_at = ?", completedAt).Where("run_id = ? AND session_id = ?", runID, sessionID)
	result, err := query.Exec(ctx)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (d *RecoveryDAO) ListOpenTurns(ctx context.Context, executor bun.IDB, sessionID string) ([]OpenTurnRecord, error) {
	var rows []struct {
		ID       string `bun:"id"`
		IntentID string `bun:"intent_id"`
		RunID    string `bun:"run_id"`
	}
	err := executor.NewSelect().TableExpr("conversation_turns AS t").ColumnExpr("t.id, t.intent_id, COALESCE((SELECT json_extract(e.data, '$.runId') FROM entries e WHERE e.session_id = t.session_id AND e.type = 'turn_start' AND json_extract(e.data, '$.turnId') = t.id ORDER BY e.seq DESC LIMIT 1), (SELECT p.run_id FROM session_event_projections p WHERE p.session_id = t.session_id AND p.type = 'turn_start' AND p.turn_id = t.id ORDER BY p.seq DESC LIMIT 1), '') AS run_id").Where("t.session_id = ? AND t.status = ?", sessionID, "open").OrderExpr("t.start_seq").Scan(ctx, &rows)
	result := make([]OpenTurnRecord, len(rows))
	for i, row := range rows {
		result[i] = OpenTurnRecord{row.ID, row.IntentID, row.RunID}
	}
	return result, err
}

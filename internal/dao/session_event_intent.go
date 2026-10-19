package dao

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"
)

// SessionEventIntentRecord is the control-plane half of a session event batch.
// The event bytes live in the session EventStore; this row is the fenced
// authority that decides whether those bytes are canonical.
type SessionEventIntentRecord struct {
	bun.BaseModel   `bun:"table:session_event_intents"`
	ID              string `bun:"id,pk"`
	SessionID       string `bun:"session_id"`
	RunID           string `bun:"run_id"`
	TurnID          string `bun:"turn_id"`
	SeqFrom         int64  `bun:"seq_from"`
	SeqTo           int64  `bun:"seq_to"`
	Digest          string `bun:"digest"`
	State           string `bun:"state"`
	OwnerInstanceID string `bun:"owner_instance_id"`
	LeaseEpoch      int64  `bun:"lease_epoch"`
	LeaseTokenHash  string `bun:"lease_token_hash"`
	CreatedAt       int64  `bun:"created_at"`
	UpdatedAt       int64  `bun:"updated_at"`
	LastError       string `bun:"last_error"`
}

type SessionEventIntentDAO struct{ db *bun.DB }

func NewSessionEventIntentDAO(db *bun.DB) *SessionEventIntentDAO {
	return &SessionEventIntentDAO{db: db}
}

func (d *SessionEventIntentDAO) Find(ctx context.Context, executor bun.IDB, id string) (*SessionEventIntentRecord, error) {
	record := new(SessionEventIntentRecord)
	err := executor.NewSelect().Model(record).Where("id = ?", id).Limit(1).Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, err
	}
	return record, err
}

func (d *SessionEventIntentDAO) Insert(ctx context.Context, executor bun.IDB, record *SessionEventIntentRecord) error {
	_, err := executor.NewInsert().Model(record).Exec(ctx)
	return err
}

// InsertFenced creates an intent only while the same owner/epoch/token is
// active in the SQLite lease table. EventStore bytes alone never authorize a
// control-plane intent.
func (d *SessionEventIntentDAO) InsertFenced(ctx context.Context, executor bun.IDB, record *SessionEventIntentRecord) (int64, error) {
	result, err := executor.NewRaw(`INSERT INTO session_event_intents
		(id, session_id, run_id, turn_id, seq_from, seq_to, digest, state,
		 owner_instance_id, lease_epoch, lease_token_hash, created_at, updated_at, last_error)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE EXISTS (
			SELECT 1 FROM session_runtime_leases
			WHERE session_id = ? AND owner_instance_id = ? AND epoch = ?
			  AND lease_token_hash = ? AND state = 'active'
		)`, record.ID, record.SessionID, record.RunID, record.TurnID, record.SeqFrom, record.SeqTo,
		record.Digest, record.State, record.OwnerInstanceID, record.LeaseEpoch, record.LeaseTokenHash,
		record.CreatedAt, record.UpdatedAt, record.LastError, record.SessionID, record.OwnerInstanceID,
		record.LeaseEpoch, record.LeaseTokenHash).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// MarkState is fenced by the exact owner, epoch, and token hash. A zero row
// count means the caller no longer has authority or the intent already moved.
func (d *SessionEventIntentDAO) MarkState(ctx context.Context, executor bun.IDB, id, sessionID, owner string, epoch int64, tokenHash, fromState, toState, lastError string, now int64) (int64, error) {
	result, err := executor.NewUpdate().Model((*SessionEventIntentRecord)(nil)).
		Set("state = ?", toState).
		Set("last_error = ?", lastError).
		Set("updated_at = ?", now).
		Where("id = ? AND session_id = ? AND owner_instance_id = ? AND lease_epoch = ? AND lease_token_hash = ? AND state = ?", id, sessionID, owner, epoch, tokenHash, fromState).
		Where("EXISTS (SELECT 1 FROM session_runtime_leases WHERE session_id = ? AND owner_instance_id = ? AND epoch = ? AND lease_token_hash = ? AND state = 'active')", sessionID, owner, epoch, tokenHash).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Adopt transfers an unresolved intent to a fenced recovery lease. Recovery
// is the only path allowed to replace the original writer identity; a normal
// execution owner must never overwrite another owner's intent.
func (d *SessionEventIntentDAO) Adopt(ctx context.Context, executor bun.IDB, id, sessionID, runID, owner string, epoch int64, tokenHash string, now int64) (int64, error) {
	result, err := executor.NewUpdate().Model((*SessionEventIntentRecord)(nil)).
		Set("owner_instance_id = ?", owner).
		Set("lease_epoch = ?", epoch).
		Set("lease_token_hash = ?", tokenHash).
		Set("updated_at = ?", now).
		Where("id = ? AND session_id = ? AND run_id = ? AND state IN (?)", id, sessionID, runID, bun.In([]string{"prepared", "appended"})).
		Where("EXISTS (SELECT 1 FROM session_runtime_leases WHERE session_id = ? AND owner_instance_id = ? AND epoch = ? AND lease_token_hash = ? AND state = 'active')", sessionID, owner, epoch, tokenHash).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *SessionEventIntentDAO) ListPending(ctx context.Context, executor bun.IDB, sessionID string) ([]SessionEventIntentRecord, error) {
	return d.list(ctx, executor, sessionID, []string{"prepared", "appended", "recovery_required"})
}

func (d *SessionEventIntentDAO) List(ctx context.Context, executor bun.IDB, sessionID string) ([]SessionEventIntentRecord, error) {
	return d.list(ctx, executor, sessionID, nil)
}

// CommittedStateCounts returns how many of the session's event intents are
// committed and how many are in any other state. It is the O(1) control-plane
// half of the append write gate: with a single fenced writer and an unchanged
// physical revision, "no non-committed intents and committed count equals the
// physical batch count" is exactly the healthy reconciliation state.
func (d *SessionEventIntentDAO) CommittedStateCounts(ctx context.Context, executor bun.IDB, sessionID string) (committed, nonCommitted int, err error) {
	committed, err = executor.NewSelect().Model((*SessionEventIntentRecord)(nil)).
		Where("session_id = ?", sessionID).
		Where("state = ?", "committed").
		Count(ctx)
	if err != nil {
		return 0, 0, err
	}
	total, err := executor.NewSelect().Model((*SessionEventIntentRecord)(nil)).
		Where("session_id = ?", sessionID).
		Count(ctx)
	if err != nil {
		return 0, 0, err
	}
	return committed, total - committed, nil
}

func (d *SessionEventIntentDAO) list(ctx context.Context, executor bun.IDB, sessionID string, states []string) ([]SessionEventIntentRecord, error) {
	var records []SessionEventIntentRecord
	query := executor.NewSelect().Model(&records).Where("session_id = ?", sessionID).OrderExpr("seq_from ASC")
	if len(states) > 0 {
		query = query.Where("state IN (?)", bun.In(states))
	}
	err := query.Scan(ctx)
	return records, err
}

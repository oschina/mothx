package dao

import (
	"context"

	"github.com/uptrace/bun"
)

// SessionEventProjectionRecord is a rebuildable SQLite index for EventStore
// content. It deliberately contains no message payload; the EventStore owns
// canonical content bytes.
type SessionEventProjectionRecord struct {
	bun.BaseModel `bun:"table:session_event_projections"`
	ID            string  `bun:"id,pk"`
	SessionID     string  `bun:"session_id"`
	Seq           int64   `bun:"seq"`
	Type          string  `bun:"type"`
	ParentID      *string `bun:"parent_id,nullzero"`
	RunID         string  `bun:"run_id"`
	TurnID        string  `bun:"turn_id"`
	Timestamp     string  `bun:"timestamp"`
	BatchID       string  `bun:"batch_id"`
}

type SessionEventProjectionDAO struct{ db *bun.DB }

func NewSessionEventProjectionDAO(db *bun.DB) *SessionEventProjectionDAO {
	return &SessionEventProjectionDAO{db: db}
}

func (d *SessionEventProjectionDAO) InsertBatch(ctx context.Context, executor bun.IDB, records []SessionEventProjectionRecord) error {
	if len(records) == 0 {
		return nil
	}
	_, err := executor.NewInsert().Model(&records).Exec(ctx)
	return err
}

func (d *SessionEventProjectionDAO) DeleteBatch(ctx context.Context, executor bun.IDB, batchID string) error {
	_, err := executor.NewDelete().Model((*SessionEventProjectionRecord)(nil)).Where("batch_id = ?", batchID).Exec(ctx)
	return err
}

func (d *SessionEventProjectionDAO) DeleteSession(ctx context.Context, executor bun.IDB, sessionID string) error {
	_, err := executor.NewDelete().Model((*SessionEventProjectionRecord)(nil)).Where("session_id = ?", sessionID).Exec(ctx)
	return err
}

// AnyIDPresent reports whether any of ids is already recorded for the session.
// It is the O(1) existence probe behind the append path's idempotency check: a
// retry only matters when at least one incoming event ID is already committed,
// so the caller reads the full committed transcript only when this is true.
func (d *SessionEventProjectionDAO) AnyIDPresent(ctx context.Context, executor bun.IDB, sessionID string, ids []string) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	return executor.NewSelect().Model((*SessionEventProjectionRecord)(nil)).
		Column("id").
		Where("session_id = ?", sessionID).
		Where("id IN (?)", bun.In(ids)).
		Limit(1).
		Exists(ctx)
}

func (d *SessionEventProjectionDAO) Seq(ctx context.Context, executor bun.IDB, sessionID, id string) (int64, error) {
	var seq int64
	err := executor.NewSelect().Table("session_event_projections").Column("seq").Where("session_id = ? AND id = ?", sessionID, id).Limit(1).Scan(ctx, &seq)
	return seq, err
}

// BoundarySeqsByTurn returns each turn marker's EventStore sequence as recorded
// in the projection columns, keyed by turn ID and marker kind
// ("turn_start"/"turn_end").
//
// The production repair derives the same mapping from the canonical event
// payloads instead (an older build left these projection columns empty for
// migrated markers), so this reader is deliberately a second, independent view
// of the fact. It exists for contract tests that must confirm the derived
// identity and the stored projection agree; do not wire it into the repair path
// or the cross-check becomes circular.
func (d *SessionEventProjectionDAO) BoundarySeqsByTurn(ctx context.Context, executor bun.IDB, sessionID string) ([]TurnMarkerSeq, error) {
	var rows []TurnMarkerSeq
	err := executor.NewSelect().Table("session_event_projections").
		ColumnExpr("turn_id, type, seq").
		Where("session_id = ? AND type IN ('turn_start', 'turn_end')", sessionID).
		OrderExpr("seq ASC").
		Scan(ctx, &rows)
	return rows, err
}

// TurnMarkerSeq is one turn boundary marker of the EventStore projection.
type TurnMarkerSeq struct {
	TurnID string `bun:"turn_id"`
	Type   string `bun:"type"`
	Seq    int64  `bun:"seq"`
}

// DeleteLegacyEntries clears the obsolete payload-bearing SQLite transcript
// only after the matching EventStore batch is committed and indexed.
func (d *SessionEventProjectionDAO) DeleteLegacyEntries(ctx context.Context, executor bun.IDB, sessionID string) error {
	_, err := executor.NewDelete().Table("entries").Where("session_id = ? AND type <> ?", sessionID, "session").Exec(ctx)
	return err
}

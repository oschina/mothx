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

// HasLegacyEntries reports whether the session still carries a payload-bearing
// SQLite duplicate of content the EventStore already owns. Convergence probes
// with this read-only existence check before deciding whether anything needs
// retiring: on an already-converged session the answer is no, so the bind path
// performs no write at all.
func (d *SessionEventProjectionDAO) HasLegacyEntries(ctx context.Context, executor bun.IDB, sessionID string) (bool, error) {
	return executor.NewSelect().Table("entries").
		ColumnExpr("1").
		Where("session_id = ? AND type <> ?", sessionID, "session").
		Limit(1).
		Exists(ctx)
}

// UncoveredLegacyEntryCount reports how many legacy payload rows are not
// represented by a committed EventStore projection.
//
// Entry identity survives the migration export, so "this row has no projection"
// is exactly "this row was written after the session flipped to the EventStore"
// (or by a build that still writes the legacy transcript). Zero is the only
// state in which retiring the duplicate cannot lose content; a positive count is
// the split-store condition that must be repaired by adopting the delta, never
// by deleting it.
func (d *SessionEventProjectionDAO) UncoveredLegacyEntryCount(ctx context.Context, executor bun.IDB, sessionID string) (int64, error) {
	var count int64
	err := executor.NewSelect().Table("entries").
		ColumnExpr("COUNT(*)").
		Where("session_id = ?", sessionID).
		Where("type <> ?", "session").
		Where("NOT EXISTS (SELECT 1 FROM session_event_projections p WHERE p.session_id = entries.session_id AND p.id = entries.id)").
		Scan(ctx, &count)
	return count, err
}

// UncoveredLegacyEntryIDs returns the IDs of the legacy payload rows no
// committed projection covers, ordered by legacy sequence. It is the delta an
// adoption has to append to the event log; entry IDs survive the export, so the
// caller can keep chaining by identity instead of comparing sequence spaces.
func (d *SessionEventProjectionDAO) UncoveredLegacyEntryIDs(ctx context.Context, executor bun.IDB, sessionID string) ([]string, error) {
	var ids []string
	err := executor.NewSelect().Table("entries").
		Column("id").
		Where("session_id = ?", sessionID).
		Where("type <> ?", "session").
		Where("NOT EXISTS (SELECT 1 FROM session_event_projections p WHERE p.session_id = entries.session_id AND p.id = entries.id)").
		OrderExpr("seq ASC").
		Scan(ctx, &ids)
	return ids, err
}

// SplitStoreSessionRecord is one session whose EventStore is canonical while its
// legacy transcript still holds rows the event log does not cover.
type SplitStoreSessionRecord struct {
	SessionID string `bun:"session_id"`
	CWD       string `bun:"cwd"`
	Uncovered int64  `bun:"uncovered"`
}

// SplitContentStoreSessions lists every split-store session in the database.
// It is a read-only diagnostic for `mothx doctor`: the repair works on the
// returned IDs through the session layer, never through this query.
func (d *SessionEventProjectionDAO) SplitContentStoreSessions(ctx context.Context, executor bun.IDB) ([]SplitStoreSessionRecord, error) {
	var rows []SplitStoreSessionRecord
	// The base table must appear exactly once: bun appends TableExpr to Table, so
	// declaring both produces `FROM entries, entries AS e` and a cross join.
	err := executor.NewSelect().
		ColumnExpr("e.session_id AS session_id").
		ColumnExpr("COALESCE(s.cwd, '') AS cwd").
		ColumnExpr("COUNT(*) AS uncovered").
		TableExpr("entries AS e").
		Join("JOIN sessions AS s ON s.id = e.session_id AND s.content_store = ?", "event_store").
		Where("e.type <> ?", "session").
		Where("NOT EXISTS (SELECT 1 FROM session_event_projections p WHERE p.session_id = e.session_id AND p.id = e.id)").
		GroupExpr("e.session_id, s.cwd").
		OrderExpr("uncovered DESC, e.session_id ASC").
		Scan(ctx, &rows)
	return rows, err
}

// DeleteCoveredLegacyEntries retires only the legacy payload rows the EventStore
// already owns, and returns how many it removed.
//
// The coverage test lives in the DELETE itself rather than in a prior probe: a
// writer that still appends the legacy transcript (an older build sharing the
// session root) can insert a row between any probe and this statement, and that
// row must survive. Rows written after the flip are always uncovered, so this
// statement can never delete content the event log does not have.
func (d *SessionEventProjectionDAO) DeleteCoveredLegacyEntries(ctx context.Context, executor bun.IDB, sessionID string) (int64, error) {
	result, err := executor.NewDelete().Table("entries").
		Where("session_id = ?", sessionID).
		Where("type <> ?", "session").
		Where("EXISTS (SELECT 1 FROM session_event_projections p WHERE p.session_id = entries.session_id AND p.id = entries.id)").
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// DeleteLegacyEntries clears every payload-bearing SQLite transcript row of the
// session regardless of whether the event log covers it.
//
// Production paths must use DeleteCoveredLegacyEntries: this unconditional
// variant exists only so contract tests can reproduce the destructive cleanup an
// older build performed, which is exactly the state the split-store repair has
// to survive.
func (d *SessionEventProjectionDAO) DeleteLegacyEntries(ctx context.Context, executor bun.IDB, sessionID string) error {
	_, err := executor.NewDelete().Table("entries").Where("session_id = ? AND type <> ?", sessionID, "session").Exec(ctx)
	return err
}

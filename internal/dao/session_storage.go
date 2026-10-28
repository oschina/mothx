package dao

import (
	"context"

	"github.com/uptrace/bun"
)

// SessionStorageCounts is the row inventory reported by session-storage
// maintenance. Transcript counts are reported, never modified.
type SessionStorageCounts struct {
	Entries     int64 `bun:"entries"`
	SubEntries  int64 `bun:"sub_entries"`
	ToolRecords int64 `bun:"tool_records"`
}

// SessionStorageStats is SQLite's page/free-list view used to decide whether a
// VACUUM would actually reclaim space.
type SessionStorageStats struct {
	FreelistPages int64 `bun:"freelist_pages"`
	PageCount     int64 `bun:"page_count"`
	PageSize      int64 `bun:"page_size"`
}

// SessionStorageDAO owns the SQL for host session-storage maintenance. It is
// deliberately limited to derived idempotency records: transcript tables are
// only counted, never deleted.
type SessionStorageDAO struct{ db *bun.DB }

func NewSessionStorageDAO(db *bun.DB) *SessionStorageDAO {
	return &SessionStorageDAO{db: db}
}

// PruneCompletedToolRecords removes terminal tool-execution idempotency records
// older than cutoff. In-flight states are excluded by construction so a
// retry/recovery can never lose the record that fences a side effect.
func (d *SessionStorageDAO) PruneCompletedToolRecords(ctx context.Context, executor bun.IDB, cutoff string) (int64, error) {
	result, err := executor.NewDelete().Table("tool_execution_records").
		Where("execution_state IN (?)", bun.In([]string{"completed", "abandoned"})).
		Where("datetime(COALESCE(completed_at, created_at)) < datetime(?)", cutoff).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *SessionStorageDAO) Counts(ctx context.Context, executor bun.IDB) (SessionStorageCounts, error) {
	var counts SessionStorageCounts
	err := executor.NewRaw(`SELECT
		(SELECT COUNT(*) FROM entries) AS entries,
		(SELECT COUNT(*) FROM sub_entries) AS sub_entries,
		(SELECT COUNT(*) FROM tool_execution_records) AS tool_records`).Scan(ctx, &counts)
	return counts, err
}

func (d *SessionStorageDAO) Stats(ctx context.Context, executor bun.IDB) (SessionStorageStats, error) {
	var stats SessionStorageStats
	err := executor.NewRaw(`SELECT
		(SELECT freelist_count FROM pragma_freelist_count) AS freelist_pages,
		(SELECT page_count FROM pragma_page_count) AS page_count,
		(SELECT page_size FROM pragma_page_size) AS page_size`).Scan(ctx, &stats)
	return stats, err
}

// Optimize lets SQLite refresh its query-planner statistics. It is cheap and
// safe to run during every maintenance pass.
func (d *SessionStorageDAO) Optimize(ctx context.Context, executor bun.IDB) error {
	_, err := executor.NewRaw(`PRAGMA optimize`).Exec(ctx)
	return err
}

// Vacuum rewrites the database file to release free pages. Callers must opt in:
// it holds the SQLite writer for the duration of the rewrite.
func (d *SessionStorageDAO) Vacuum(ctx context.Context, executor bun.IDB) error {
	_, err := executor.NewRaw(`VACUUM`).Exec(ctx)
	return err
}

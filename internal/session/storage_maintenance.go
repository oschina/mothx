package session

import (
	"context"
	"fmt"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

// SessionStoragePolicy is the resolved intent for Runtime-owned session-storage
// maintenance. Transcript rows (`entries` / `sub_entries`) are never deleted by
// this policy; it exists to bound derived idempotency records and optionally
// reclaim free pages.
type SessionStoragePolicy struct {
	// ToolExecutionRetention is how long a completed or abandoned
	// tool-execution idempotency record is kept. Zero keeps every record.
	ToolExecutionRetention time.Duration
	// Vacuum requests a VACUUM after pruning when free pages exist.
	Vacuum bool
}

// SessionStorageResult reports what a maintenance pass found and did.
type SessionStorageResult struct {
	PrunedToolRecords int64
	Entries           int64
	SubEntries        int64
	ToolRecords       int64
	FreelistPages     int64
	PageCount         int64
	PageSize          int64
	Vacuumed          bool
}

// InspectSessionStorage reports the session database's transcript, tool-record,
// and page inventory without writing. It is the read-only counterpart used by
// `mothx maintenance status` and the vacuum dry run.
func InspectSessionStorage(ctx context.Context, sessionDir string) (SessionStorageResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionDir == "" {
		return SessionStorageResult{}, fmt.Errorf("session directory is required")
	}
	db, ok, err := openExistingSessionDBReadOnly(sessionDir)
	if err != nil {
		return SessionStorageResult{}, err
	}
	if !ok {
		return SessionStorageResult{}, nil
	}
	defer db.Close()
	storeDAO := dao.NewSessionStorageDAO(db.Bun())
	counts, err := storeDAO.Counts(ctx, db.Bun())
	if err != nil {
		return SessionStorageResult{}, err
	}
	stats, err := storeDAO.Stats(ctx, db.Bun())
	if err != nil {
		return SessionStorageResult{}, err
	}
	return SessionStorageResult{
		Entries: counts.Entries, SubEntries: counts.SubEntries, ToolRecords: counts.ToolRecords,
		FreelistPages: stats.FreelistPages, PageCount: stats.PageCount, PageSize: stats.PageSize,
	}, nil
}

// MaintainSessionStorage runs one bounded maintenance pass over the shared
// session database.
//
// It deliberately preserves every transcript row: messages, compaction entries,
// content overrides, sub-agent entries, and their EventStore mirrors are never
// deleted. Only completed/abandoned tool-execution idempotency records older
// than the configured retention may be pruned; in-flight states are always kept
// so an uncertain side effect stays fenced. VACUUM is opt-in because rewrites
// hold the SQLite writer for the duration of the pass.
func MaintainSessionStorage(ctx context.Context, sessionDir string, policy SessionStoragePolicy, now time.Time) (SessionStorageResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionDir == "" {
		return SessionStorageResult{}, fmt.Errorf("session directory is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return SessionStorageResult{}, err
	}
	storeDAO := dao.NewSessionStorageDAO(db.Bun())
	result := SessionStorageResult{}

	if policy.ToolExecutionRetention > 0 {
		cutoff := now.UTC().Add(-policy.ToolExecutionRetention).Format(time.RFC3339Nano)
		if err := WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
			pruned, err := storeDAO.PruneCompletedToolRecords(ctx, tx, cutoff)
			if err != nil {
				return err
			}
			result.PrunedToolRecords = pruned
			return nil
		}); err != nil {
			return result, fmt.Errorf("prune completed tool-execution records: %w", err)
		}
	}

	if err := storeDAO.Optimize(ctx, db.Bun()); err != nil {
		return result, fmt.Errorf("optimize session database: %w", err)
	}
	counts, err := storeDAO.Counts(ctx, db.Bun())
	if err != nil {
		return result, err
	}
	result.Entries, result.SubEntries, result.ToolRecords = counts.Entries, counts.SubEntries, counts.ToolRecords

	stats, err := storeDAO.Stats(ctx, db.Bun())
	if err != nil {
		return result, err
	}
	result.FreelistPages, result.PageCount, result.PageSize = stats.FreelistPages, stats.PageCount, stats.PageSize

	if policy.Vacuum && stats.FreelistPages > 0 {
		if err := storeDAO.Vacuum(ctx, db.Bun()); err != nil {
			return result, fmt.Errorf("vacuum session database: %w", err)
		}
		result.Vacuumed = true
		if stats, err = storeDAO.Stats(ctx, db.Bun()); err != nil {
			return result, err
		}
		result.FreelistPages, result.PageCount, result.PageSize = stats.FreelistPages, stats.PageCount, stats.PageSize
	}
	return result, nil
}

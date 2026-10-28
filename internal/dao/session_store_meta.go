package dao

import (
	"context"
	"errors"

	"github.com/uptrace/bun"
)

// SessionStoreSearchWriteVersionKey names the durable global write counter that
// invalidates derived, query-level search caches. Database triggers bump it on
// every write that can change a session's searchable projection, including
// writes committed by another process, so a cached result can never outlive
// the rows it was computed from.
const SessionStoreSearchWriteVersionKey = "search_write_version"

// SessionStoreMetaDAO owns the small key/value table used for store-wide
// derived-state metadata. It is deliberately not a place for canonical session
// facts.
type SessionStoreMetaDAO struct{ db *bun.DB }

func NewSessionStoreMetaDAO(db *bun.DB) *SessionStoreMetaDAO {
	return &SessionStoreMetaDAO{db: db}
}

// SearchWriteVersion reads the current global search write version. The second
// return value reports whether the row exists; a missing row means the cache
// must be treated as unavailable rather than as version zero.
func (d *SessionStoreMetaDAO) SearchWriteVersion(ctx context.Context, executor bun.IDB) (int64, bool, error) {
	if executor == nil {
		executor = d.db
	}
	var row struct {
		Value int64 `bun:"value"`
	}
	err := executor.NewSelect().Table("session_store_meta").
		Column("value").
		Where("key = ?", SessionStoreSearchWriteVersionKey).
		Limit(1).
		Scan(ctx, &row)
	if errors.Is(err, ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return row.Value, true, nil
}

package dao

import (
	"context"

	"github.com/uptrace/bun"
)

// SessionDisplayProjectionRecord is the small SQLite projection a session list
// row needs: first-user preview, latest title, and binding entries. It is
// rebuildable from the EventStore and never canonical content.
type SessionDisplayProjectionRecord struct {
	bun.BaseModel `bun:"table:session_display_projection"`
	SessionID     string `bun:"session_id,pk"`
	Preview       string `bun:"preview"`
	PreviewSeen   bool   `bun:"preview_seen"`
	Title         string `bun:"title"`
	BindingsJSON  string `bun:"bindings_json"`
	UpdatedAt     string `bun:"updated_at"`
}

type SessionDisplayProjectionDAO struct{ db *bun.DB }

func NewSessionDisplayProjectionDAO(db *bun.DB) *SessionDisplayProjectionDAO {
	return &SessionDisplayProjectionDAO{db: db}
}

func (d *SessionDisplayProjectionDAO) FindMany(ctx context.Context, executor bun.IDB, sessionIDs []string) (map[string]SessionDisplayProjectionRecord, error) {
	result := make(map[string]SessionDisplayProjectionRecord, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return result, nil
	}
	var records []SessionDisplayProjectionRecord
	if err := executor.NewSelect().Model(&records).
		Where("session_id IN (?)", bun.In(sessionIDs)).
		Scan(ctx); err != nil {
		return nil, err
	}
	for _, record := range records {
		result[record.SessionID] = record
	}
	return result, nil
}

func (d *SessionDisplayProjectionDAO) Upsert(ctx context.Context, executor bun.IDB, record *SessionDisplayProjectionRecord) error {
	_, err := executor.NewInsert().Model(record).
		On("CONFLICT(session_id) DO UPDATE SET preview = excluded.preview, preview_seen = excluded.preview_seen, title = excluded.title, bindings_json = excluded.bindings_json, updated_at = excluded.updated_at").
		Exec(ctx)
	return err
}

func (d *SessionDisplayProjectionDAO) DeleteSession(ctx context.Context, executor bun.IDB, sessionID string) error {
	_, err := executor.NewDelete().Model((*SessionDisplayProjectionRecord)(nil)).Where("session_id = ?", sessionID).Exec(ctx)
	return err
}

// UnprojectedEventStoreSessionIDs returns EventStore sessions with messages
// that do not have a display projection yet, newest first. Legacy sessions are
// excluded: their list rows already come from indexed SQLite queries.
func (d *SessionDisplayProjectionDAO) UnprojectedEventStoreSessionIDs(ctx context.Context, executor bun.IDB, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	var rows []struct {
		ID string `bun:"id"`
	}
	err := executor.NewRaw(`SELECT s.id
		FROM sessions AS s
		WHERE s.content_store = 'event_store'
		AND EXISTS (SELECT 1 FROM session_event_projections p WHERE p.session_id = s.id AND p.type = 'message')
		AND NOT EXISTS (SELECT 1 FROM session_display_projection d WHERE d.session_id = s.id)
		ORDER BY s.timestamp DESC
		LIMIT ?`, limit).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

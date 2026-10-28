package dao

import (
	"context"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// SessionSearchIndexRow is one searchable text fragment of a session. The
// fragment is stored verbatim; matching semantics (compaction and content
// overrides) are decided by the canonical replay, not by this index.
type SessionSearchIndexRow struct {
	EntryID string
	Text    string
}

// SessionSearchIndexDAO owns the derived full-text accelerator for session
// content search. The FTS table is rebuildable from committed transcript
// content and is never authoritative: it only narrows which sessions need a
// canonical replay.
type SessionSearchIndexDAO struct{ db *bun.DB }

func NewSessionSearchIndexDAO(db *bun.DB) *SessionSearchIndexDAO {
	return &SessionSearchIndexDAO{db: db}
}

// Insert appends text fragments for one session and marks the session as
// indexed. Callers pass the same transaction that persists the entry so the
// index and the canonical row cannot diverge.
func (d *SessionSearchIndexDAO) Insert(ctx context.Context, executor bun.IDB, sessionID string, rows []SessionSearchIndexRow) error {
	if sessionID == "" {
		return nil
	}
	for _, row := range rows {
		if row.Text == "" {
			continue
		}
		if _, err := executor.NewRaw(
			`INSERT INTO session_content_fts(session_id, entry_id, text) VALUES (?, ?, ?)`,
			sessionID, row.EntryID, row.Text,
		).Exec(ctx); err != nil {
			return err
		}
	}
	return d.MarkIndexed(ctx, executor, sessionID)
}

// MarkIndexed records that the session's persisted content has been indexed.
// A session without indexable text still gets a state row: it can never match
// on content, so skipping its replay is correct.
func (d *SessionSearchIndexDAO) MarkIndexed(ctx context.Context, executor bun.IDB, sessionID string) error {
	_, err := executor.NewRaw(
		`INSERT INTO session_content_index_state(session_id, updated_at) VALUES (?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET updated_at = excluded.updated_at`,
		sessionID, time.Now().UTC().Format(time.RFC3339Nano),
	).Exec(ctx)
	return err
}

// IndexedSessionIDs returns the subset of sessionIDs whose content index is
// complete enough to trust for candidate narrowing. Sessions absent from the
// result must keep the canonical replay path.
func (d *SessionSearchIndexDAO) IndexedSessionIDs(ctx context.Context, executor bun.IDB, sessionIDs []string) (map[string]struct{}, error) {
	indexed := make(map[string]struct{}, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return indexed, nil
	}
	var rows []struct {
		SessionID string `bun:"session_id"`
	}
	if err := executor.NewSelect().Table("session_content_index_state").
		Column("session_id").
		Where("session_id IN (?)", bun.In(sessionIDs)).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		indexed[row.SessionID] = struct{}{}
	}
	return indexed, nil
}

// MatchSessionIDs returns every session whose indexed text contains query.
// FTS5's trigram tokenizer matches arbitrary substrings (case-insensitively),
// which preserves the previous strings.Contains semantics for queries of at
// least three characters. Shorter queries must not use this method.
func (d *SessionSearchIndexDAO) MatchSessionIDs(ctx context.Context, executor bun.IDB, query string) ([]string, error) {
	var rows []struct {
		SessionID string `bun:"session_id"`
	}
	if err := executor.NewRaw(
		`SELECT DISTINCT session_id FROM session_content_fts WHERE session_content_fts MATCH ?`,
		SessionSearchFTSPhrase(query),
	).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.SessionID)
	}
	return ids, nil
}

// UnindexedSessionIDs returns root sessions with persisted messages that have
// no search-index state row yet, newest first. Sessions without a message (and
// sub-agent sessions, whose content lives in sub_entries) are not listed: the
// list projection and search never match them on content.
func (d *SessionSearchIndexDAO) UnindexedSessionIDs(ctx context.Context, executor bun.IDB, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	var rows []struct {
		ID string `bun:"id"`
	}
	err := executor.NewRaw(`SELECT s.id
		FROM sessions AS s
		WHERE (EXISTS (SELECT 1 FROM entries e WHERE e.session_id = s.id AND e.type = 'message')
			OR EXISTS (SELECT 1 FROM session_event_projections p WHERE p.session_id = s.id AND p.type = 'message'))
		AND NOT EXISTS (SELECT 1 FROM session_content_index_state st WHERE st.session_id = s.id)
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

// DeleteSession removes and forgets one session's derived index. It is called
// from the session cascade so deleted sessions cannot keep matching searches.
func (d *SessionSearchIndexDAO) DeleteSession(ctx context.Context, executor bun.IDB, sessionID string) error {
	if _, err := executor.NewRaw(`DELETE FROM session_content_fts WHERE session_id = ?`, sessionID).Exec(ctx); err != nil {
		return err
	}
	_, err := executor.NewRaw(`DELETE FROM session_content_index_state WHERE session_id = ?`, sessionID).Exec(ctx)
	return err
}

// SessionSearchFTSPhrase turns a raw user substring into an FTS5 string
// literal. Quoting is mandatory: unquoted punctuation would otherwise be
// parsed as FTS operators instead of literal match text.
func SessionSearchFTSPhrase(query string) string {
	return `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
}

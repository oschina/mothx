package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/oschina/mothx/internal/dao"
)

// sessionSearchQueryIndexable reports whether the trigram FTS index can answer
// this query. FTS5 trigram matches substrings of at least three unicode
// characters; shorter or whitespace-only queries keep the canonical replay
// path so their semantics never change.
func sessionSearchQueryIndexable(query string) bool {
	if strings.TrimSpace(query) == "" {
		return false
	}
	return utf8.RuneCountInString(query) >= 3
}

// sessionSearchIndexRowsForEntry extracts exactly the text that canonical
// replay can expose for an entry. Entries that replay ignores (turn markers,
// directory grants, labels) contribute nothing. Overrides are indexed with
// their replacement text; the original message row keeps its own text, and the
// replay decides which one is effective.
func sessionSearchIndexRowsForEntry(entry interface{}) []dao.SessionSearchIndexRow {
	id, _, _, _ := getEntryMetadata(entry)
	if id == "" {
		return nil
	}
	var texts []string
	switch e := entry.(type) {
	case MessageEntry:
		texts = messageSearchTexts(e.Message)
	case *MessageEntry:
		texts = messageSearchTexts(e.Message)
	case CompactionEntry:
		if strings.TrimSpace(e.Summary) != "" {
			texts = []string{e.Summary}
		}
	case *CompactionEntry:
		if e != nil && strings.TrimSpace(e.Summary) != "" {
			texts = []string{e.Summary}
		}
	case ContentOverrideEntry:
		texts = messageSearchTexts(e.Message)
	case *ContentOverrideEntry:
		if e != nil {
			texts = messageSearchTexts(e.Message)
		}
	case SessionInfoEntry:
		if strings.TrimSpace(e.Name) != "" {
			texts = []string{e.Name}
		}
	case *SessionInfoEntry:
		if e != nil && strings.TrimSpace(e.Name) != "" {
			texts = []string{e.Name}
		}
	}
	rows := make([]dao.SessionSearchIndexRow, 0, len(texts))
	for _, text := range texts {
		if text == "" {
			continue
		}
		rows = append(rows, dao.SessionSearchIndexRow{EntryID: id, Text: text})
	}
	return rows
}

// collectSessionSearchIndexRows flattens the searchable text of entries already
// loaded by a Manager. It is used only for the one-time backfill of sessions
// created before the index existed.
func collectSessionSearchIndexRows(entries []interface{}) []dao.SessionSearchIndexRow {
	rows := make([]dao.SessionSearchIndexRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, sessionSearchIndexRowsForEntry(entry)...)
	}
	return rows
}

// indexSessionSearchEntriesTx writes the derived index in the transaction that
// persists the entries, so a committed transcript and its index cannot diverge.
func indexSessionSearchEntriesTx(ctx context.Context, tx *dao.Tx, sessionID string, entries []interface{}) error {
	rows := collectSessionSearchIndexRows(entries)
	if len(rows) == 0 {
		return nil
	}
	return dao.NewSessionSearchIndexDAO(nil).Insert(ctx, tx, sessionID, rows)
}

// ensureSearchIndex backfills sessions that predate the derived search index.
// It is best-effort: any failure leaves the session on the canonical replay
// path, so search stays correct (just slower) until a later attach retries.
//
// The state row is the authority for "indexed". Checking it before opening a
// write transaction keeps every later attach write-free, matching the open-path
// cost contract; the check is repeated inside the transaction so two managers
// opening the same old session cannot both insert the same rows.
func (m *Manager) ensureSearchIndex(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.subAgent || m.header == nil {
		m.mu.Unlock()
		return nil
	}
	sessionID := m.header.ID
	sessionDir := m.sessionDirOrDerive()
	entries := append([]interface{}(nil), m.entries...)
	m.mu.Unlock()

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	indexDAO := dao.NewSessionSearchIndexDAO(db.Bun())
	indexed, err := indexDAO.IndexedSessionIDs(ctx, db.Bun(), []string{sessionID})
	if err != nil {
		return err
	}
	if _, ok := indexed[sessionID]; ok {
		return nil
	}
	// Only materialize the text copy for sessions that still need indexing;
	// attach is otherwise write-free even for very long transcripts.
	rows := collectSessionSearchIndexRows(entries)
	if len(rows) == 0 {
		return nil
	}
	return WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		indexDAO := dao.NewSessionSearchIndexDAO(nil)
		indexed, err := indexDAO.IndexedSessionIDs(ctx, tx, []string{sessionID})
		if err != nil {
			return err
		}
		if _, ok := indexed[sessionID]; ok {
			return nil
		}
		return indexDAO.Insert(ctx, tx, sessionID, rows)
	})
}

// DefaultSearchIndexBackfillBatch bounds how many transcript sessions one
// maintenance pass indexes. Each session is written in its own transaction, so
// the bound keeps a very large history from turning one run into a long series
// of writer-lock acquisitions; remaining sessions are picked up next run.
const DefaultSearchIndexBackfillBatch = 512

// SearchIndexBackfillResult reports one bounded backfill pass.
type SearchIndexBackfillResult struct {
	Sessions int
	Rows     int
	Failed   int
	More     bool
}

// BackfillSessionSearchIndex indexes sessions created before the derived FTS
// table existed. It only reads canonical content and writes the derived index;
// transcript rows are never modified. Per-session failures are counted and
// skipped so one damaged EventStore cannot block the rest; the next pass retries
// them, and search keeps using the canonical replay fallback meanwhile.
func BackfillSessionSearchIndex(ctx context.Context, sessionDir string, limit int) (SearchIndexBackfillResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionDir == "" {
		return SearchIndexBackfillResult{}, fmt.Errorf("session directory is required")
	}
	if limit <= 0 {
		limit = DefaultSearchIndexBackfillBatch
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return SearchIndexBackfillResult{}, err
	}
	sessionDAO := dao.NewSessionDAO(db.Bun())
	ids, err := dao.NewSessionSearchIndexDAO(db.Bun()).UnindexedSessionIDs(ctx, db.Bun(), limit)
	if err != nil {
		return SearchIndexBackfillResult{}, err
	}
	result := SearchIndexBackfillResult{More: len(ids) == limit}
	for _, id := range ids {
		record, err := sessionDAO.FindListByID(ctx, id)
		if err != nil || record == nil {
			result.Failed++
			continue
		}
		entries, err := loadSearchIndexEntries(ctx, sessionDir, id, record.ContentStore, sessionDAO)
		if err != nil || len(entries) == 0 {
			// An empty read on a session selected for having messages means the
			// content store moved underneath us or the store cannot be read.
			// Leave it unindexed for a later pass instead of marking an incomplete
			// index as complete.
			result.Failed++
			continue
		}
		rows := collectSessionSearchIndexRows(entries)
		written, err := writeSearchIndexRows(ctx, sessionDir, id, rows)
		if err != nil {
			result.Failed++
			continue
		}
		if written {
			result.Sessions++
			result.Rows += len(rows)
		}
	}
	return result, nil
}

func loadSearchIndexEntries(ctx context.Context, sessionDir, sessionID, contentStore string, sessionDAO *dao.SessionDAO) ([]interface{}, error) {
	if contentStore == "event_store" {
		batches, err := ReadCommittedEventBatches(ctx, sessionDir, sessionID)
		if err != nil {
			return nil, err
		}
		entries := make([]interface{}, 0, len(batches))
		for _, batch := range batches {
			for _, record := range batch.Events {
				entry, err := DecodeEventRecord(record)
				if err != nil {
					return nil, err
				}
				if _, ok := entry.(Header); ok {
					continue
				}
				entries = append(entries, entry)
			}
		}
		return entries, nil
	}
	records, err := sessionDAO.Entries(ctx, "entries", sessionID)
	if err != nil {
		return nil, err
	}
	entries := make([]interface{}, 0, len(records))
	for _, record := range records {
		if entry := decodeSearchIndexEntry(record.Type, record.Data); entry != nil {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func decodeSearchIndexEntry(entryType, data string) interface{} {
	if EntryType(entryType) == EntrySessionInfo {
		var entry SessionInfoEntry
		if json.Unmarshal([]byte(data), &entry) == nil {
			return entry
		}
		return nil
	}
	entry, _ := decodeLegacySearchEntry(entryType, data)
	return entry
}

// writeSearchIndexRows re-checks the state row inside the write transaction so a
// concurrent attach or append that already indexed the session wins without
// duplicating rows.
func writeSearchIndexRows(ctx context.Context, sessionDir, sessionID string, rows []dao.SessionSearchIndexRow) (bool, error) {
	written := false
	err := WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		indexDAO := dao.NewSessionSearchIndexDAO(nil)
		indexed, err := indexDAO.IndexedSessionIDs(ctx, tx, []string{sessionID})
		if err != nil {
			return err
		}
		if _, ok := indexed[sessionID]; ok {
			return nil
		}
		if len(rows) == 0 {
			if err := indexDAO.MarkIndexed(ctx, tx, sessionID); err != nil {
				return err
			}
			written = true
			return nil
		}
		if err := indexDAO.Insert(ctx, tx, sessionID, rows); err != nil {
			return err
		}
		written = true
		return nil
	})
	return written, err
}

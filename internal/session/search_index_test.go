package session

import (
	"context"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

func searchIndexRowCounts(t *testing.T, sessionDir, sessionID string) (rows, state int) {
	t.Helper()
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatalf("open root database: %v", err)
	}
	if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM session_content_fts WHERE session_id = ?`, sessionID).Scan(&rows); err != nil {
		t.Fatalf("count fts rows: %v", err)
	}
	if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM session_content_index_state WHERE session_id = ?`, sessionID).Scan(&state); err != nil {
		t.Fatalf("count index state: %v", err)
	}
	return rows, state
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestSessionContentIndexTracksAppends pins the dual-write contract for both
// content stores: an appended message (and a title) is queryable immediately,
// without opening or replaying the session.
func TestSessionContentIndexTracksAppends(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	legacy := newSearchTestSession(t, sessionDir, "/tmp/index-legacy", "index-legacy")
	if _, err := legacy.AppendSessionInfo("Indexed Title Marker"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendMessage(provider.NewUserMessage("MothX 桌面检索系统 alpha")); err != nil {
		t.Fatal(err)
	}

	event := convergedEventStoreSessionForTest(t, sessionDir, "index-event")
	if _, err := event.AppendSessionInfo("Event Indexed Title"); err != nil {
		t.Fatal(err)
	}
	if _, err := event.AppendMessage(provider.NewUserMessage("event store index body")); err != nil {
		t.Fatal(err)
	}

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatalf("open root database: %v", err)
	}
	indexDAO := dao.NewSessionSearchIndexDAO(db.Bun())
	indexed, err := indexDAO.IndexedSessionIDs(context.Background(), db.Bun(), []string{"index-legacy", "index-event"})
	if err != nil {
		t.Fatalf("read indexed sessions: %v", err)
	}
	if len(indexed) != 2 {
		t.Fatalf("indexed sessions = %d, want both content stores", len(indexed))
	}

	cases := []struct {
		query string
		want  string
	}{
		{"OTHX", "index-legacy"},             // case-insensitive substring inside a token
		{"桌面检", "index-legacy"},              // CJK substring (three runes)
		{"indexed title", "index-legacy"},    // title text
		{"event store index", "index-event"}, // EventStore content
	}
	for _, tc := range cases {
		ids, err := indexDAO.MatchSessionIDs(context.Background(), db.Bun(), tc.query)
		if err != nil {
			t.Fatalf("match %q: %v", tc.query, err)
		}
		if !containsID(ids, tc.want) {
			t.Fatalf("match %q did not return %q: %v", tc.query, tc.want, ids)
		}
	}
}

// TestSessionContentIndexBackfillsUnindexedSession covers the backfill path:
// sessions created before the index keep the canonical replay fallback, and the
// first attach fills the index exactly once.
func TestSessionContentIndexBackfillsUnindexedSession(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := newSearchTestSession(t, sessionDir, "/tmp/index-backfill", "index-backfill")
	if _, err := manager.AppendMessage(provider.NewUserMessage("backfill-only-marker")); err != nil {
		t.Fatal(err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatalf("open root database: %v", err)
	}
	if _, err := db.Bun().Exec(`DELETE FROM session_content_fts WHERE session_id = ?`, "index-backfill"); err != nil {
		t.Fatalf("clear fts rows: %v", err)
	}
	if _, err := db.Bun().Exec(`DELETE FROM session_content_index_state WHERE session_id = ?`, "index-backfill"); err != nil {
		t.Fatalf("clear index state: %v", err)
	}

	// Without an index the read-only search must still find the message.
	details, total, err := SearchAllDetailed(sessionDir, "backfill-only-marker", 0, 0)
	if err != nil {
		t.Fatalf("search unindexed session: %v", err)
	}
	if total != 1 || len(details) != 1 || details[0].ID != "index-backfill" {
		t.Fatalf("unindexed search = total %d details %#v", total, details)
	}
	// Search is read-only: it must not have populated the index.
	if rows, state := searchIndexRowCounts(t, sessionDir, "index-backfill"); rows != 0 || state != 0 {
		t.Fatalf("search wrote the derived index: rows=%d state=%d", rows, state)
	}

	if err := manager.ensureSearchIndex(context.Background()); err != nil {
		t.Fatalf("backfill search index: %v", err)
	}
	rows, state := searchIndexRowCounts(t, sessionDir, "index-backfill")
	if rows == 0 || state != 1 {
		t.Fatalf("backfill rows=%d state=%d, want indexed content", rows, state)
	}

	// A second attach must not duplicate rows.
	if err := manager.ensureSearchIndex(context.Background()); err != nil {
		t.Fatalf("repeat backfill: %v", err)
	}
	if again, _ := searchIndexRowCounts(t, sessionDir, "index-backfill"); again != rows {
		t.Fatalf("repeated backfill rows = %d, want %d", again, rows)
	}
}

// TestSearchNarrowsContentReplayWithIndex proves the index is actually used as
// a candidate filter rather than an extra copy of the old full scan. Short
// queries and unindexed sessions still replay, which is the correctness
// fallback the design relies on.
func TestSearchNarrowsContentReplayWithIndex(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	const sessions = 6
	for i := 0; i < sessions; i++ {
		manager := newSearchTestSession(t, sessionDir, "/tmp/narrow-cwd", "narrow-session-"+string(rune('a'+i)))
		if _, err := manager.AppendMessage(provider.NewUserMessage("unique-marker-" + string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
	}

	before := SessionSearchContentReplayCount()
	details, total, err := SearchAllDetailed(sessionDir, "unique-marker-c", 0, 0)
	if err != nil {
		t.Fatalf("indexed search: %v", err)
	}
	if total != 1 || len(details) != 1 || details[0].ID != "narrow-session-c" {
		t.Fatalf("indexed search = total %d details %#v", total, details)
	}
	if replays := SessionSearchContentReplayCount() - before; replays != 1 {
		t.Fatalf("indexed search replayed %d sessions, want only the candidate", replays)
	}

	// A two-rune query cannot use the trigram index and must fall back to
	// replaying every content session without losing matches.
	before = SessionSearchContentReplayCount()
	if _, total, err = SearchAllDetailed(sessionDir, "ma", 0, 0); err != nil {
		t.Fatalf("short-query search: %v", err)
	}
	if total != sessions {
		t.Fatalf("short-query total = %d, want %d", total, sessions)
	}
	if replays := SessionSearchContentReplayCount() - before; replays != sessions {
		t.Fatalf("short-query replayed %d sessions, want %d", replays, sessions)
	}

	// An unindexed session must still be found through the replay fallback.
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatalf("open root database: %v", err)
	}
	if _, err := db.Bun().Exec(`DELETE FROM session_content_fts WHERE session_id = ?`, "narrow-session-a"); err != nil {
		t.Fatalf("clear fts rows: %v", err)
	}
	if _, err := db.Bun().Exec(`DELETE FROM session_content_index_state WHERE session_id = ?`, "narrow-session-a"); err != nil {
		t.Fatalf("clear index state: %v", err)
	}
	before = SessionSearchContentReplayCount()
	details, total, err = SearchAllDetailed(sessionDir, "unique-marker-a", 0, 0)
	if err != nil {
		t.Fatalf("unindexed search: %v", err)
	}
	if total != 1 || len(details) != 1 || details[0].ID != "narrow-session-a" {
		t.Fatalf("unindexed search = total %d details %#v", total, details)
	}
	if replays := SessionSearchContentReplayCount() - before; replays != 1 {
		t.Fatalf("unindexed search replayed %d sessions, want one fallback", replays)
	}
}

// TestDeleteSessionRemovesContentIndex keeps the derived table from retaining
// rows for sessions that no longer exist.
func TestDeleteSessionRemovesContentIndex(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := newSearchTestSession(t, sessionDir, "/tmp/index-delete", "index-delete")
	if _, err := manager.AppendMessage(provider.NewUserMessage("deleted-index-marker")); err != nil {
		t.Fatal(err)
	}
	if rows, state := searchIndexRowCounts(t, sessionDir, "index-delete"); rows == 0 || state != 1 {
		t.Fatalf("index before delete = rows %d state %d", rows, state)
	}
	if err := DeleteSession(manager.GetFile(), sessionDir); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if rows, state := searchIndexRowCounts(t, sessionDir, "index-delete"); rows != 0 || state != 0 {
		t.Fatalf("index after delete = rows %d state %d, want cleared", rows, state)
	}
}

// TestForkCopiesContentIndex covers the one non-append transcript copy path:
// the legacy fork inserts copied rows directly, so it must seed the derived
// index for the child in the same transaction.
func TestForkCopiesContentIndex(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("fork-index-source"); err != nil {
		t.Fatal(err)
	}
	turnID := "fork-index-turn"
	if err := StartConversationTurn(sessionDir, ConversationTurn{ID: turnID, SessionID: "fork-index-source", IntentID: "fork-index-intent", RunID: "fork-index-run"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("forked-index-marker")); err != nil {
		t.Fatal(err)
	}
	if err := EndConversationTurn(sessionDir, "fork-index-source", turnID, "completed", "stop", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}

	forked, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "fork-index-source", RequestID: "fork-index-request"})
	if err != nil {
		t.Fatalf("fork session: %v", err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatalf("open root database: %v", err)
	}
	indexDAO := dao.NewSessionSearchIndexDAO(db.Bun())
	indexed, err := indexDAO.IndexedSessionIDs(context.Background(), db.Bun(), []string{forked.SessionID})
	if err != nil {
		t.Fatalf("read child index state: %v", err)
	}
	if _, ok := indexed[forked.SessionID]; !ok {
		t.Fatal("forked child has no content index")
	}
	ids, err := indexDAO.MatchSessionIDs(context.Background(), db.Bun(), "forked-index-marker")
	if err != nil {
		t.Fatalf("match child content: %v", err)
	}
	if !containsID(ids, forked.SessionID) {
		t.Fatalf("forked child is missing from content candidates: %v", ids)
	}
}

// TestBackfillSessionSearchIndexIndexesLegacyAndEventStoreSessions covers the
// bounded history backfill: sessions created before the index existed are read
// from their canonical store once, indexed, and skipped on later passes.
func TestBackfillSessionSearchIndexIndexesLegacyAndEventStoreSessions(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	legacy := newSearchTestSession(t, sessionDir, "/tmp/backfill-legacy", "backfill-legacy")
	if _, err := legacy.AppendMessage(provider.NewUserMessage("legacy-backfill-marker")); err != nil {
		t.Fatal(err)
	}
	event := convergedEventStoreSessionForTest(t, sessionDir, "backfill-event")
	if _, err := event.AppendMessage(provider.NewUserMessage("event-backfill-marker")); err != nil {
		t.Fatal(err)
	}
	indexed := newSearchTestSession(t, sessionDir, "/tmp/backfill-indexed", "backfill-indexed")
	if _, err := indexed.AppendMessage(provider.NewUserMessage("already-indexed-marker")); err != nil {
		t.Fatal(err)
	}
	// A title without a message is not a session the list/search projects, so it
	// must not be selected for backfill.
	titled := newSearchTestSession(t, sessionDir, "/tmp/backfill-title", "backfill-title")
	if _, err := titled.AppendSessionInfo("title without a message"); err != nil {
		t.Fatal(err)
	}

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatalf("open root database: %v", err)
	}
	for _, id := range []string{"backfill-legacy", "backfill-event", "backfill-title"} {
		if _, err := db.Bun().Exec(`DELETE FROM session_content_fts WHERE session_id = ?`, id); err != nil {
			t.Fatalf("clear fts rows for %s: %v", id, err)
		}
		if _, err := db.Bun().Exec(`DELETE FROM session_content_index_state WHERE session_id = ?`, id); err != nil {
			t.Fatalf("clear index state for %s: %v", id, err)
		}
	}

	result, err := BackfillSessionSearchIndex(context.Background(), sessionDir, 10)
	if err != nil {
		t.Fatalf("backfill session search index: %v", err)
	}
	if result.Sessions != 2 || result.Failed != 0 {
		t.Fatalf("backfill result = %#v, want the two unindexed transcript sessions", result)
	}
	indexDAO := dao.NewSessionSearchIndexDAO(db.Bun())
	indexedIDs, err := indexDAO.IndexedSessionIDs(context.Background(), db.Bun(), []string{"backfill-legacy", "backfill-event", "backfill-indexed", "backfill-title"})
	if err != nil {
		t.Fatalf("read index state: %v", err)
	}
	for _, id := range []string{"backfill-legacy", "backfill-event", "backfill-indexed"} {
		if _, ok := indexedIDs[id]; !ok {
			t.Fatalf("session %s was not indexed", id)
		}
	}
	if _, ok := indexedIDs["backfill-title"]; ok {
		t.Fatal("a title-only session was selected for text backfill")
	}
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"legacy-backfill-marker", "backfill-legacy"},
		{"event-backfill-marker", "backfill-event"},
	} {
		details, total, err := SearchAllDetailed(sessionDir, tc.query, 0, 0)
		if err != nil {
			t.Fatalf("search %q after backfill: %v", tc.query, err)
		}
		if total != 1 || len(details) != 1 || details[0].ID != tc.want {
			t.Fatalf("search %q = total %d details %#v", tc.query, total, details)
		}
	}

	again, err := BackfillSessionSearchIndex(context.Background(), sessionDir, 10)
	if err != nil {
		t.Fatalf("repeat backfill: %v", err)
	}
	if again.Sessions != 0 || again.Failed != 0 {
		t.Fatalf("repeat backfill = %#v, want every session already indexed", again)
	}
}

// TestBackfillSessionSearchIndexSkipsUnreadableEventStore keeps one damaged
// session from blocking the pass; it stays on the replay fallback and is
// retried later.
func TestBackfillSessionSearchIndexSkipsUnreadableEventStore(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	broken := convergedEventStoreSessionForTest(t, sessionDir, "backfill-broken")
	if _, err := broken.AppendMessage(provider.NewUserMessage("broken-backfill-marker")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveEventStore(sessionDir, "backfill-broken"); err != nil {
		t.Fatal(err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatalf("open root database: %v", err)
	}
	if _, err := db.Bun().Exec(`DELETE FROM session_content_fts WHERE session_id = ?`, "backfill-broken"); err != nil {
		t.Fatalf("clear fts rows: %v", err)
	}
	if _, err := db.Bun().Exec(`DELETE FROM session_content_index_state WHERE session_id = ?`, "backfill-broken"); err != nil {
		t.Fatalf("clear index state: %v", err)
	}

	result, err := BackfillSessionSearchIndex(context.Background(), sessionDir, 10)
	if err != nil {
		t.Fatalf("backfill with a broken store: %v", err)
	}
	if result.Sessions != 0 || result.Failed == 0 {
		t.Fatalf("backfill result = %#v, want the broken session skipped", result)
	}
	indexedIDs, err := dao.NewSessionSearchIndexDAO(db.Bun()).IndexedSessionIDs(context.Background(), db.Bun(), []string{"backfill-broken"})
	if err != nil {
		t.Fatalf("read index state: %v", err)
	}
	if _, ok := indexedIDs["backfill-broken"]; ok {
		t.Fatal("an unreadable store was marked as indexed")
	}
}

// TestSearchProjectionCacheReusesUnchangedEventStore proves repeated searches
// over the same EventStore bytes do not replay the session again, while an
// append changes the physical fingerprint and forces a fresh projection.
func TestSearchProjectionCacheReusesUnchangedEventStore(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := convergedEventStoreSessionForTest(t, sessionDir, "search-projection-cache")
	if _, err := manager.AppendSessionTitle("Cache Title", "manual"); err != nil {
		t.Fatal(err)
	}

	before := SessionSearchProjectionLoadCount()
	details, total, err := SearchAllDetailed(sessionDir, "message-2", 0, 0)
	if err != nil {
		t.Fatalf("first search: %v", err)
	}
	if total != 1 || len(details) != 1 || details[0].ID != "search-projection-cache" {
		t.Fatalf("first search = total %d details %#v", total, details)
	}
	if loads := SessionSearchProjectionLoadCount() - before; loads != 1 {
		t.Fatalf("first search loaded %d projections, want 1", loads)
	}

	before = SessionSearchProjectionLoadCount()
	if _, total, err = SearchAllDetailed(sessionDir, "message-2", 0, 0); err != nil {
		t.Fatalf("cached search: %v", err)
	}
	if total != 1 {
		t.Fatalf("cached search total = %d, want 1", total)
	}
	if loads := SessionSearchProjectionLoadCount() - before; loads != 0 {
		t.Fatalf("cached search loaded %d projections, want a cache hit", loads)
	}

	if _, err := manager.AppendMessage(provider.NewUserMessage("cache-refresh-marker")); err != nil {
		t.Fatal(err)
	}
	before = SessionSearchProjectionLoadCount()
	details, total, err = SearchAllDetailed(sessionDir, "cache-refresh-marker", 0, 0)
	if err != nil {
		t.Fatalf("search after append: %v", err)
	}
	if total != 1 || len(details) != 1 || details[0].ID != "search-projection-cache" {
		t.Fatalf("search after append = total %d details %#v", total, details)
	}
	if loads := SessionSearchProjectionLoadCount() - before; loads != 1 {
		t.Fatalf("append did not invalidate the projection cache: loads=%d", loads)
	}
}

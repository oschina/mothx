package session

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

func cacheDelta(before uint64, read func() uint64) uint64 { return read() - before }

// TestSearchResultCacheServesRepeatedQueries proves a repeated page does no
// scan at all: the second call is a cache hit even though the underlying
// EventStore projection cache would also have avoided a replay.
func TestSearchResultCacheServesRepeatedQueries(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := newSearchTestSession(t, sessionDir, "/tmp/result-cache", "result-cache-session")
	if _, err := manager.AppendMessage(provider.NewUserMessage("result cache marker")); err != nil {
		t.Fatal(err)
	}

	beforeHits := SessionSearchResultCacheHits()
	beforeMisses := SessionSearchResultCacheMisses()
	first, firstTotal, err := SearchAllDetailed(sessionDir, "result cache marker", 10, 0)
	if err != nil {
		t.Fatalf("first search: %v", err)
	}
	if firstTotal != 1 || len(first) != 1 || first[0].ID != "result-cache-session" {
		t.Fatalf("first search = total %d details %#v", firstTotal, first)
	}
	if got := cacheDelta(beforeHits, SessionSearchResultCacheHits); got != 0 {
		t.Fatalf("first search recorded %d cache hits, want 0", got)
	}
	if got := cacheDelta(beforeMisses, SessionSearchResultCacheMisses); got != 1 {
		t.Fatalf("first search recorded %d cache misses, want 1", got)
	}

	beforeHits = SessionSearchResultCacheHits()
	beforeMisses = SessionSearchResultCacheMisses()
	beforeReplays := SessionSearchContentReplayCount()
	second, secondTotal, err := SearchAllDetailed(sessionDir, "result cache marker", 10, 0)
	if err != nil {
		t.Fatalf("second search: %v", err)
	}
	if got := cacheDelta(beforeReplays, SessionSearchContentReplayCount); got != 0 {
		t.Fatalf("cached search replayed %d session contents, want 0", got)
	}
	if got := cacheDelta(beforeHits, SessionSearchResultCacheHits); got != 1 {
		t.Fatalf("second search recorded %d cache hits, want 1", got)
	}
	if got := cacheDelta(beforeMisses, SessionSearchResultCacheMisses); got != 0 {
		t.Fatalf("second search recorded %d cache misses, want 0", got)
	}
	if secondTotal != firstTotal || !reflect.DeepEqual(second, first) {
		t.Fatalf("cached result differs from first search: total %d -> %d\ndetails %#v -> %#v", firstTotal, secondTotal, first, second)
	}
}

// TestSearchResultCacheCachesEmptyPages pins the common "no match" case: it is
// expensive to recompute (every session is replayed) and must be cached too.
func TestSearchResultCacheCachesEmptyPages(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := newSearchTestSession(t, sessionDir, "/tmp/result-cache-empty", "result-cache-empty")
	if _, err := manager.AppendMessage(provider.NewUserMessage("unrelated body")); err != nil {
		t.Fatal(err)
	}

	if details, total, err := SearchAllDetailed(sessionDir, "definitely-no-match", 10, 0); err != nil || total != 0 || len(details) != 0 {
		t.Fatalf("first empty search = %#v total=%d err=%v", details, total, err)
	}
	beforeHits := SessionSearchResultCacheHits()
	if _, total, err := SearchAllDetailed(sessionDir, "definitely-no-match", 10, 0); err != nil || total != 0 {
		t.Fatalf("second empty search = total %d err %v", total, err)
	}
	if got := cacheDelta(beforeHits, SessionSearchResultCacheHits); got != 1 {
		t.Fatalf("repeated empty search recorded %d cache hits, want 1", got)
	}
}

// TestSearchResultCacheInvalidatesOnEntryWrite proves the global write version
// replaces any need for time-based expiry: appending content makes the next
// identical query observe the new match.
func TestSearchResultCacheInvalidatesOnEntryWrite(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	first := newSearchTestSession(t, sessionDir, "/tmp/result-cache-entry", "result-cache-entry-a")
	if _, err := first.AppendMessage(provider.NewUserMessage("entry-invalidation-marker")); err != nil {
		t.Fatal(err)
	}
	if details, total, err := SearchAllDetailed(sessionDir, "entry-invalidation-marker", 10, 0); err != nil || total != 1 || len(details) != 1 {
		t.Fatalf("first search = %#v total=%d err=%v", details, total, err)
	}

	second := newSearchTestSession(t, sessionDir, "/tmp/result-cache-entry-b", "result-cache-entry-b")
	if _, err := second.AppendMessage(provider.NewUserMessage("entry-invalidation-marker")); err != nil {
		t.Fatal(err)
	}

	beforeHits := SessionSearchResultCacheHits()
	details, total, err := SearchAllDetailed(sessionDir, "entry-invalidation-marker", 10, 0)
	if err != nil {
		t.Fatalf("search after append: %v", err)
	}
	if got := cacheDelta(beforeHits, SessionSearchResultCacheHits); got != 0 {
		t.Fatalf("search after append recorded %d cache hits, want a miss", got)
	}
	if total != 2 || len(details) != 2 {
		t.Fatalf("search after append = total %d details %#v, want both sessions", total, details)
	}
}

// TestSearchResultCacheInvalidatesOnSessionUpdate covers the sessions-row
// trigger: a cwd that becomes searchable (or stops being searchable) must not
// be masked by an empty cached page.
func TestSearchResultCacheInvalidatesOnSessionUpdate(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := newSearchTestSession(t, sessionDir, "/tmp/result-cache-cwd", "result-cache-cwd")
	if _, err := manager.AppendMessage(provider.NewUserMessage("cwd body")); err != nil {
		t.Fatal(err)
	}
	if _, total, err := SearchAllDetailed(sessionDir, "cwd-invalidation-marker", 10, 0); err != nil || total != 0 {
		t.Fatalf("first search = total %d err %v, want 0", total, err)
	}

	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionDAO(nil).UpdateSessionCWD(context.Background(), tx, "sessions", "result-cache-cwd", "/tmp/cwd-invalidation-marker")
	}); err != nil {
		t.Fatalf("update cwd: %v", err)
	}

	beforeHits := SessionSearchResultCacheHits()
	details, total, err := SearchAllDetailed(sessionDir, "cwd-invalidation-marker", 10, 0)
	if err != nil {
		t.Fatalf("search after cwd update: %v", err)
	}
	if got := cacheDelta(beforeHits, SessionSearchResultCacheHits); got != 0 {
		t.Fatalf("search after cwd update recorded %d cache hits, want a miss", got)
	}
	if total != 1 || len(details) != 1 || details[0].ID != "result-cache-cwd" {
		t.Fatalf("search after cwd update = total %d details %#v", total, details)
	}
}

// TestSearchResultCacheInvalidatesOnTitleWrite covers session_info entries:
// titles are part of cheap matching and are appended to the entries table.
func TestSearchResultCacheInvalidatesOnTitleWrite(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := newSearchTestSession(t, sessionDir, "/tmp/result-cache-title", "result-cache-title")
	if _, err := manager.AppendMessage(provider.NewUserMessage("title body")); err != nil {
		t.Fatal(err)
	}
	if _, total, err := SearchAllDetailed(sessionDir, "title-invalidation-marker", 10, 0); err != nil || total != 0 {
		t.Fatalf("first search = total %d err %v, want 0", total, err)
	}
	if _, err := manager.AppendSessionTitle("title-invalidation-marker", "manual"); err != nil {
		t.Fatal(err)
	}
	details, total, err := SearchAllDetailed(sessionDir, "title-invalidation-marker", 10, 0)
	if err != nil {
		t.Fatalf("search after title write: %v", err)
	}
	if total != 1 || len(details) != 1 || details[0].Name != "title-invalidation-marker" {
		t.Fatalf("search after title write = total %d details %#v", total, details)
	}
}

// TestSearchResultCacheInvalidatesOnEventStoreAppend covers
// session_event_projections, which is the only SQLite row an EventStore content
// append writes.
func TestSearchResultCacheInvalidatesOnEventStoreAppend(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := convergedEventStoreSessionForTest(t, sessionDir, "result-cache-event")
	if _, total, err := SearchAllDetailed(sessionDir, "event-invalidation-marker", 10, 0); err != nil || total != 0 {
		t.Fatalf("first search = total %d err %v, want 0", total, err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("event-invalidation-marker")); err != nil {
		t.Fatal(err)
	}
	details, total, err := SearchAllDetailed(sessionDir, "event-invalidation-marker", 10, 0)
	if err != nil {
		t.Fatalf("search after EventStore append: %v", err)
	}
	if total != 1 || len(details) != 1 || details[0].ID != "result-cache-event" {
		t.Fatalf("search after EventStore append = total %d details %#v", total, details)
	}
}

// TestSearchResultCacheInvalidatesOnProjectScope covers session_metadata: the
// same query with the same project filter must reflect a newly grouped session.
func TestSearchResultCacheInvalidatesOnProjectScope(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	const now = "2026-01-01T00:00:00Z"
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		_, err := tx.ExecContext(context.Background(), `INSERT INTO projects (id, name, created_at, updated_at) VALUES ('result-cache-project', 'Cache', ?, ?)`, now, now)
		return err
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	manager := newSearchTestSession(t, sessionDir, "/tmp/result-cache-project", "result-cache-project")
	if _, err := manager.AppendMessage(provider.NewUserMessage("project-cache-marker")); err != nil {
		t.Fatal(err)
	}
	filter := dao.SessionListFilter{MessagesOnly: true, ProjectID: "result-cache-project"}
	if _, total, err := searchAllDetailed(sessionDir, "project-cache-marker", 0, 0, filter); err != nil || total != 0 {
		t.Fatalf("first scoped search = total %d err %v, want 0", total, err)
	}
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		_, err := tx.ExecContext(context.Background(), `INSERT INTO session_metadata (session_id, project_id, updated_at) VALUES ('result-cache-project', 'result-cache-project', ?)`, now)
		return err
	}); err != nil {
		t.Fatalf("group session: %v", err)
	}
	details, total, err := searchAllDetailed(sessionDir, "project-cache-marker", 0, 0, filter)
	if err != nil {
		t.Fatalf("scoped search after grouping: %v", err)
	}
	if total != 1 || len(details) != 1 {
		t.Fatalf("scoped search after grouping = total %d details %#v", total, details)
	}
}

// TestSearchResultCacheEvictsBeyondBounds pins the memory ceiling: the cache is
// a bounded projection, not an unbounded history of every query the process has
// seen.
func TestSearchResultCacheEvictsBeyondBounds(t *testing.T) {
	cache := &sessionSearchResultCache{entries: map[string]sessionSearchResultCacheEntry{}}
	for i := 0; i < maxSearchResultCacheEntries+10; i++ {
		cache.put(strconv.Itoa(i), int64(i), []SessionDetail{{ID: "session"}}, 1)
	}
	if len(cache.entries) > maxSearchResultCacheEntries {
		t.Fatalf("cache kept %d entries, want at most %d", len(cache.entries), maxSearchResultCacheEntries)
	}
	if cache.bytes > maxSearchResultCacheBytes {
		t.Fatalf("cache kept %d bytes, want at most %d", cache.bytes, maxSearchResultCacheBytes)
	}
	if _, ok := cache.get("0", 0); ok {
		t.Fatal("oldest entry was not evicted")
	}
	if _, ok := cache.get(strconv.Itoa(maxSearchResultCacheEntries+9), int64(maxSearchResultCacheEntries+9)); !ok {
		t.Fatal("newest entry was not retained")
	}
}

// TestSearchResultCacheKeysDistinguishFilterScopes pins the parts of the key
// that are semantically different but serialize to the same empty string:
// nil CWDs (no restriction) versus an empty non-nil set (no visible session).
func TestSearchResultCacheKeysDistinguishFilterScopes(t *testing.T) {
	base := dao.SessionListFilter{MessagesOnly: true}
	unrestricted := sessionSearchResultCacheKey("dir", "query", 10, 0, base)
	emptyScope := sessionSearchResultCacheKey("dir", "query", 10, 0, dao.SessionListFilter{MessagesOnly: true, CWDs: []string{}})
	if unrestricted == emptyScope {
		t.Fatal("nil and empty CWD scopes share a cache key")
	}
	scoped := sessionSearchResultCacheKey("dir", "query", 10, 0, dao.SessionListFilter{MessagesOnly: true, CWDs: []string{"/tmp/a", "/tmp/b"}})
	reordered := sessionSearchResultCacheKey("dir", "query", 10, 0, dao.SessionListFilter{MessagesOnly: true, CWDs: []string{"/tmp/b", "/tmp/a"}})
	if scoped != reordered {
		t.Fatal("equivalent CWD scopes produced different cache keys")
	}
	if scoped == unrestricted {
		t.Fatal("a scoped query shares the unrestricted key")
	}
}

// TestSearchResultCacheKeysPerPage keeps pagination honest: the same query at
// different offsets must not return another page's cached row.
func TestSearchResultCacheKeysPerPage(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	for _, id := range []string{"result-cache-page-a", "result-cache-page-b", "result-cache-page-c"} {
		manager := newSearchTestSession(t, sessionDir, "/tmp/result-cache-page", id)
		if _, err := manager.AppendMessage(provider.NewUserMessage("page-cache-marker")); err != nil {
			t.Fatal(err)
		}
		// Distinct timestamps keep the list order deterministic.
		time.Sleep(time.Millisecond)
	}
	first, total, err := SearchAllDetailed(sessionDir, "page-cache-marker", 1, 0)
	if err != nil || total != 3 || len(first) != 1 {
		t.Fatalf("first page = %#v total=%d err=%v", first, total, err)
	}
	second, total, err := SearchAllDetailed(sessionDir, "page-cache-marker", 1, 1)
	if err != nil || total != 3 || len(second) != 1 {
		t.Fatalf("second page = %#v total=%d err=%v", second, total, err)
	}
	if first[0].ID == second[0].ID {
		t.Fatalf("different offsets returned the same cached row %q", first[0].ID)
	}
}

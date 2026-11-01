package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

// TestStaleLegacyHandleAdoptsEventStoreAfterFlip is the P0 regression for the
// false "session was modified by another process" report.
//
// A Manager that loaded the legacy transcript before another Manager migrated
// the session must not keep appending to `entries` (whose duplicate the
// migration retires) and must never surface a lost-CAS conflict. It resolves the
// persisted content store, adopts the committed log, and appends to the
// EventStore — exactly once, with no duplicate content.
func TestStaleLegacyHandleAdoptsEventStoreAfterFlip(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "stale-legacy-handle"
	const workDir = "/tmp/event-stale-legacy-handle"

	creator := New(workDir, sessionDir)
	if err := creator.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := creator.AppendMessage(provider.NewUserMessage("legacy question")); err != nil {
		t.Fatal(err)
	}

	// The stale handle: it opened while the session was still on the legacy store.
	stale, err := Open(creator.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	if stale.eventStoreModeForTest() {
		t.Fatal("fixture opened the stale handle on the EventStore")
	}
	if got := stale.GetMessages(); len(got) != 1 {
		t.Fatalf("stale handle replay = %d messages, want the legacy one", len(got))
	}

	// Another Manager migrates the session (export + flip + fenced retire).
	adopter, err := Open(creator.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := adopter.MigrateToEventStoreOutcome(context.Background()); err != nil {
		t.Fatal(err)
	} else if outcome != MigrationAdopted {
		t.Fatalf("migration outcome = %q, want %q", outcome, MigrationAdopted)
	}
	// Retirement waits out the post-flip grace window; age it, then let the attach
	// path perform the fenced cleanup.
	elapseContentStoreRetireGraceForTest(t, sessionDir, sessionID)
	if _, err := adopter.MigrateToEventStoreOutcome(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != 0 {
		t.Fatalf("migration left %d legacy payload rows, want the covered duplicate retired", count)
	}

	// The stale handle appends anyway: no ErrSessionModified, no re-rooted turn.
	answer := provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "fresh answer"}})
	if _, err := stale.AppendMessage(answer); err != nil {
		t.Fatalf("stale handle append after migration = %v, want it to adopt the EventStore", err)
	}
	if !stale.eventStoreModeForTest() {
		t.Fatal("stale handle did not adopt the EventStore after the migration")
	}

	// Everything must be replayable from the canonical log exactly once.
	reader, err := OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := reader.GetMessages(); !equalReplay(got, []string{"legacy question", "fresh answer"}) {
		t.Fatalf("canonical replay after adoption = %#v, want the legacy question and the fresh answer", textsOf(got))
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != 0 {
		t.Fatalf("adoption reintroduced %d legacy payload rows", count)
	}
}

// TestAppendMessagesAdoptsEventStoreAfterFlip covers the batched write path used
// for tool results: a stale legacy handle must self-heal the whole batch instead
// of failing the iteration with a conflict.
func TestAppendMessagesAdoptsEventStoreAfterFlip(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "stale-batch-adopts"
	const workDir = "/tmp/event-stale-batch-adopts"

	creator := New(workDir, sessionDir)
	if err := creator.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := creator.AppendMessage(provider.NewUserMessage("start")); err != nil {
		t.Fatal(err)
	}
	stale, err := Open(creator.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	adopter, err := Open(creator.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adopter.MigrateToEventStoreOutcome(context.Background()); err != nil {
		t.Fatal(err)
	}

	batch := []provider.Message{
		provider.NewToolResultMessage("call-1", "bash", "one", false),
		provider.NewToolResultMessage("call-2", "bash", "two", false),
	}
	ids, err := stale.AppendMessages(batch)
	if err != nil {
		t.Fatalf("stale batch append after migration = %v, want adoption", err)
	}
	if len(ids) != len(batch) {
		t.Fatalf("batch returned %d ids for %d messages", len(ids), len(batch))
	}
	reader, err := OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := reader.GetMessages(); !equalReplay(got, []string{"start", "one", "two"}) {
		t.Fatalf("canonical replay after batched adoption = %#v", textsOf(got))
	}
}

// TestAppendReportsRetiredLegacyTranscript pins the mixed-build failure mode: an
// older build (or a manual delete) retires the legacy payload while the session
// still selects the legacy store. The append must report the typed transition
// error, not the misleading "another process modified the session" conflict, and
// must never silently re-root the transcript.
func TestAppendReportsRetiredLegacyTranscript(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "legacy-transcript-retired"
	manager := New("/tmp/event-legacy-retired", sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("before")); err != nil {
		t.Fatal(err)
	}
	// Simulate the destructive cleanup an older build performed: payload rows
	// removed while content_store still says sqlite.
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionEventProjectionDAO(nil).DeleteLegacyEntries(context.Background(), tx, sessionID)
	}); err != nil {
		t.Fatal(err)
	}

	_, err := manager.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "after"}}))
	if err == nil {
		t.Fatal("append to a retired legacy transcript succeeded")
	}
	if !errors.Is(err, ErrSessionContentStoreChanged) {
		t.Fatalf("append error = %v, want ErrSessionContentStoreChanged", err)
	}
	if errors.Is(err, ErrSessionModified) {
		t.Fatal("retired transcript was reported as a concurrent-writer conflict")
	}
}

// TestOpenPreservesSplitLegacyRowsAndRetireRefuses pins the mixed-build safety
// rule: rows a legacy writer appended after the flip are content the event log
// does not own. No open may delete them, and the fenced retire must refuse
// instead of trimming around them.
func TestOpenPreservesSplitLegacyRowsAndRetireRefuses(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "split-store-preserved"
	const workDir = "/tmp/event-split-store"

	creator := New(workDir, sessionDir)
	if err := creator.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := creator.AppendMessage(provider.NewUserMessage("migrated")); err != nil {
		t.Fatal(err)
	}
	if _, err := creator.MigrateToEventStoreOutcome(context.Background()); err != nil {
		t.Fatal(err)
	}
	elapseContentStoreRetireGraceForTest(t, sessionDir, sessionID)

	// Re-stage the covered duplicate (crash window) and one post-flip row an older
	// build would have written: it has no committed projection, so it is content.
	forceLegacyPayloadDuplicateForTest(t, sessionDir, sessionID)
	leaf := creator.GetLeafID()
	postFlip := MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: "post-flip-legacy-entry", ParentID: leaf, Timestamp: time.Now()},
		Message:   provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "newer than the log"}}),
	}
	insertLegacyRowForTest(t, sessionDir, sessionID, postFlip)
	before := legacyTranscriptPayloadCount(t, sessionDir, sessionID)
	if before < 2 {
		t.Fatalf("fixture holds %d legacy payload rows, want the covered duplicate plus the post-flip row", before)
	}
	splitBefore := SessionStoreSplitDetections()

	opened, err := Open(creator.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != before {
		t.Fatalf("open changed the legacy payload row count: %d -> %d", before, count)
	}
	if got := SessionStoreSplitDetections(); got <= splitBefore {
		t.Fatal("open did not record the split-store condition")
	}

	// The attach path is allowed to act, but it must refuse to trim: deleting the
	// covered rows would leave the post-flip row chained onto a deleted parent.
	if _, err := opened.MigrateToEventStoreOutcome(context.Background()); err != nil {
		t.Fatalf("attach on a split store returned %v, want a tolerated split", err)
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != before {
		t.Fatalf("attach deleted rows on a split store: %d -> %d", before, count)
	}
	if _, err := retireLegacyPayloadRows(context.Background(), sessionDir, sessionID, true); !errors.Is(err, ErrLegacyPayloadSplit) {
		t.Fatalf("retire on a split store = %v, want ErrLegacyPayloadSplit", err)
	}
}

// TestDeleteCoveredLegacyEntriesSparesUncoveredRows pins the statement-level
// guarantee the fenced retire depends on: coverage is evaluated inside the
// DELETE, so a row inserted after the probe survives.
func TestDeleteCoveredLegacyEntriesSparesUncoveredRows(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "covered-delete-filter"
	convergedEventStoreSessionForTest(t, sessionDir, sessionID)
	before := eventProjectionCountForTest(t, sessionDir, sessionID)
	if before == 0 {
		t.Fatal("fixture has no committed projections")
	}
	forceLegacyPayloadDuplicateForTest(t, sessionDir, sessionID)
	insertLegacyRowForTest(t, sessionDir, sessionID, MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: "uncovered-row", Timestamp: time.Now()},
		Message:   provider.NewUserMessage("not in the log"),
	})
	total := legacyTranscriptPayloadCount(t, sessionDir, sessionID)

	removed := int64(0)
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		n, err := dao.NewSessionEventProjectionDAO(nil).DeleteCoveredLegacyEntries(context.Background(), tx, sessionID)
		removed = n
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if removed != int64(before) {
		t.Fatalf("deleted %d covered rows, want exactly the %d covered duplicates", removed, before)
	}
	remaining := legacyTranscriptPayloadCount(t, sessionDir, sessionID)
	if remaining != total-int(before) {
		t.Fatalf("remaining legacy rows = %d, want only the uncovered one(s)", remaining)
	}
}

// insertLegacyRowForTest writes one payload row straight into the legacy
// transcript, reproducing what a build that predates the EventStore writes. It
// bypasses the Manager on purpose: the Manager now routes every append through
// the persisted content store, which is exactly what an older build does not do.
// It returns the legacy `entries.seq` the row was assigned.
func insertLegacyRowForTest(t *testing.T, sessionDir, sessionID string, entry interface{}) int64 {
	t.Helper()
	data, err := marshalSessionEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	id, typeStr, parentID, ts := getEntryMetadata(entry)
	var parent interface{}
	if parentID != nil {
		parent = *parentID
	}
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionDAO(nil).InsertEntry(context.Background(), tx, "entries", sessionID, id, typeStr, parent, ts.Format(time.RFC3339Nano), string(data))
	}); err != nil {
		t.Fatal(err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	if err := db.Bun().NewSelect().Table("entries").Column("seq").Where("session_id = ? AND id = ?", sessionID, id).Limit(1).Scan(context.Background(), &seq); err != nil {
		t.Fatal(err)
	}
	return seq
}

func eventProjectionCountForTest(t *testing.T, sessionDir, sessionID string) int {
	t.Helper()
	count, err := eventProjectionCount(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

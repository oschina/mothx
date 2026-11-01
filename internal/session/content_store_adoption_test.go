package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

// flippedSessionWithDeltaForTest builds a session that has already been migrated
// to the EventStore and then stages the legacy transcript an older build would
// keep writing: a fresh chain whose first row has no parent, because the old
// handle's own chain was retired by the migration.
func flippedSessionWithDeltaForTest(t *testing.T, sessionDir, sessionID, workDir string, staged int) *Manager {
	t.Helper()
	creator := New(workDir, sessionDir)
	if err := creator.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := creator.AppendMessage(provider.NewUserMessage("canonical question")); err != nil {
		t.Fatal(err)
	}
	if _, err := creator.MigrateToEventStoreOutcome(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The fixture must start from a cleanly converged session, so age the flip
	// window and let the attach path retire the covered duplicate.
	elapseContentStoreRetireGraceForTest(t, sessionDir, sessionID)
	if _, err := creator.MigrateToEventStoreOutcome(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != 0 {
		t.Fatalf("fixture did not retire the covered duplicate: %d row(s)", count)
	}
	var parent *string
	for i := 0; i < staged; i++ {
		entry := MessageEntry{
			EntryBase: EntryBase{Type: EntryMessage, ID: legacyDeltaID(sessionID, i), ParentID: parent, Timestamp: time.Now()},
			Message:   provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: legacyDeltaText(i)}}),
		}
		insertLegacyRowForTest(t, sessionDir, sessionID, entry)
		id := entry.ID
		parent = &id
	}
	return creator
}

// legacyDeltaID keeps the staged rows unique across sessions: `entries.id` is a
// database-wide unique key, so two fixtures in one database may not share IDs.
func legacyDeltaID(sessionID string, i int) string {
	return sessionID + "-legacy-delta-" + string(rune('a'+i))
}
func legacyDeltaText(i int) string { return "delta " + string(rune('a'+i)) }

// TestAdoptLegacyContentReconcilesSplitStore is the split-store repair: the rows
// only the legacy transcript still holds are appended to the canonical log (in
// order, keeping their identity) and the duplicate is retired afterwards.
func TestAdoptLegacyContentReconcilesSplitStore(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "adopt-split-store"
	const workDir = "/tmp/event-adopt-split"
	creator := flippedSessionWithDeltaForTest(t, sessionDir, sessionID, workDir, 3)
	staged := legacyTranscriptPayloadCount(t, sessionDir, sessionID)
	if staged != 3 {
		t.Fatalf("staged legacy rows = %d, want 3", staged)
	}

	result, err := creator.AdoptLegacyContent(context.Background())
	if err != nil {
		t.Fatalf("adopt split store: %v", err)
	}
	if result.Deferred {
		t.Fatal("adoption deferred without another owner")
	}
	if result.AdoptedEntries != staged {
		t.Fatalf("adopted %d rows, want %d", result.AdoptedEntries, staged)
	}
	if result.RetiredEntries != int64(staged) {
		t.Fatalf("retired %d rows, want %d", result.RetiredEntries, staged)
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != 0 {
		t.Fatalf("legacy transcript still holds %d rows after adoption", count)
	}
	reader, err := OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"canonical question", legacyDeltaText(0), legacyDeltaText(1), legacyDeltaText(2)}
	if got := reader.GetMessages(); !equalReplay(got, want) {
		t.Fatalf("canonical replay after adoption = %#v, want %#v", textsOf(got), want)
	}
	if split := reader.eventStoreModeForTest(); !split {
		t.Fatal("reader did not stay on the EventStore after adoption")
	}
	if sessions, err := ListSplitContentStoreSessions(sessionDir); err != nil {
		t.Fatal(err)
	} else if len(sessions) != 0 {
		t.Fatalf("split-store listing after adoption = %#v, want none", sessions)
	}

	// Idempotent: a second call has nothing to adopt and changes no content.
	again, err := creator.AdoptLegacyContent(context.Background())
	if err != nil {
		t.Fatalf("second adoption: %v", err)
	}
	if again.AdoptedEntries != 0 || again.RetiredEntries != 0 {
		t.Fatalf("second adoption = %+v, want a no-op", again)
	}
}

// TestAdoptLegacyContentRejectsMissingAncestor pins the fail-closed rule: an
// ancestor that exists in neither the event log nor the delta means the old
// cleanup already destroyed history, so the repair refuses and keeps the rows.
func TestAdoptLegacyContentRejectsMissingAncestor(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "adopt-broken-parent"
	creator := flippedSessionWithDeltaForTest(t, sessionDir, sessionID, "/tmp/event-adopt-broken", 1)
	orphanParent := "deleted-ancestor"
	insertLegacyRowForTest(t, sessionDir, sessionID, MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: "orphan-delta", ParentID: &orphanParent, Timestamp: time.Now()},
		Message:   provider.NewUserMessage("orphaned"),
	})
	before := legacyTranscriptPayloadCount(t, sessionDir, sessionID)

	_, err := creator.AdoptLegacyContent(context.Background())
	if !errors.Is(err, ErrSessionStoreBroken) {
		t.Fatalf("adoption error = %v, want ErrSessionStoreBroken", err)
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != before {
		t.Fatalf("refused adoption changed %d legacy rows into %d", before, count)
	}
}

// TestAdoptLegacyContentDefersWhileAnotherOwnerRuns keeps the repair from
// contending with a live owner: adoption reports the deferral and changes
// nothing, and a later idle call can finish the work.
func TestAdoptLegacyContentDefersWhileAnotherOwnerRuns(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "adopt-deferred-run"
	creator := flippedSessionWithDeltaForTest(t, sessionDir, sessionID, "/tmp/event-adopt-deferred", 2)
	before := legacyTranscriptPayloadCount(t, sessionDir, sessionID)
	// The migration path releases a reservation tombstone. A plain admission in a
	// test has no process-local lease to bind it to, so clear the tombstone first;
	// production admission always re-acquires the lease before binding.
	clearRuntimeLeaseForTest(t, sessionDir, sessionID)
	now := time.Now()
	if err := SaveSessionRun(sessionDir, SessionRun{
		ID: "run-elsewhere", SessionID: sessionID, Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := creator.AdoptLegacyContent(context.Background())
	if err != nil {
		t.Fatalf("adoption while another owner runs = %v, want a deferral", err)
	}
	if !result.Deferred || result.AdoptedEntries != 0 {
		t.Fatalf("adoption result = %+v, want a deferral with no changes", result)
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != before {
		t.Fatalf("deferred adoption changed legacy rows: %d -> %d", before, count)
	}
	reader, err := OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := reader.GetMessages(); !equalReplay(got, []string{"canonical question"}) {
		t.Fatalf("canonical replay after deferral = %#v", textsOf(got))
	}
}

// TestAdoptLegacyContentRepairsTurnBounds proves the adopted turn markers also
// retarget their control-plane bounds: the repair matches the marker payload's
// own turn identity, so it never confuses a legacy sequence with an event one.
func TestAdoptLegacyContentRepairsTurnBounds(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "adopt-turn-bounds"
	creator := New("/tmp/event-adopt-turn", sessionDir)
	if err := creator.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := creator.AppendMessage(provider.NewUserMessage("canonical")); err != nil {
		t.Fatal(err)
	}
	if _, err := creator.MigrateToEventStoreOutcome(context.Background()); err != nil {
		t.Fatal(err)
	}

	turn := ConversationTurn{ID: "turn-late", SessionID: sessionID, IntentID: "intent-late", RunID: "run-late", Kind: "conversation", Status: "open", StartedAt: time.Now()}
	startSeq := insertLegacyRowForTest(t, sessionDir, sessionID, TurnStartEntry{
		EntryBase: EntryBase{Type: EntryTurnStart, ID: "late-turn-start", Timestamp: turn.StartedAt},
		TurnID:    turn.ID, IntentID: turn.IntentID, RunID: turn.RunID,
	})
	messageSeq := insertLegacyRowForTest(t, sessionDir, sessionID, MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: "late-message", ParentID: ptrString("late-turn-start"), Timestamp: time.Now()},
		Message:   provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "late answer"}}),
	})
	endSeq := insertLegacyRowForTest(t, sessionDir, sessionID, TurnEndEntry{
		EntryBase: EntryBase{Type: EntryTurnEnd, ID: "late-turn-end", ParentID: ptrString("late-message"), Timestamp: time.Now()},
		TurnID:    turn.ID, IntentID: turn.IntentID, RunID: turn.RunID, Status: "completed",
	})
	if startSeq == 0 || messageSeq == 0 || endSeq == 0 {
		t.Fatalf("staged legacy sequences = %d/%d/%d, want non-zero", startSeq, messageSeq, endSeq)
	}
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewConversationTurnDAO(nil).Insert(context.Background(), tx, &dao.ConversationTurnRecord{
			ID: turn.ID, SessionID: sessionID, IntentID: turn.IntentID, Kind: turn.Kind,
			Status: "completed", StartSeq: startSeq, EndSeq: &endSeq, StartedAt: turn.StartedAt.Format(time.RFC3339Nano),
		})
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := creator.AdoptLegacyContent(context.Background()); err != nil {
		t.Fatalf("adopt turn delta: %v", err)
	}

	wantStart, err := projectionSeqForTest(sessionDir, sessionID, "late-turn-start")
	if err != nil {
		t.Fatal(err)
	}
	wantEnd, err := projectionSeqForTest(sessionDir, sessionID, "late-turn-end")
	if err != nil {
		t.Fatal(err)
	}
	start, end, err := conversationTurnBoundsForID(t, sessionDir, sessionID, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if start != wantStart || end != wantEnd {
		t.Fatalf("turn bounds after adoption = %d/%d, want the event-log marker seqs %d/%d", start, end, wantStart, wantEnd)
	}
	if start == startSeq || end == endSeq {
		t.Fatal("turn bounds still point at the legacy sequence space")
	}
}

// clearRuntimeLeaseForTest drops the session's lease row so a test can stage an
// admission without a process-local lease (the shape `SaveSessionRun` expects).
func clearRuntimeLeaseForTest(t *testing.T, sessionDir, sessionID string) {
	t.Helper()
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().Exec("DELETE FROM session_runtime_leases WHERE session_id = ?", sessionID); err != nil {
		t.Fatal(err)
	}
}

func projectionSeqForTest(sessionDir, sessionID, entryID string) (int64, error) {
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return 0, err
	}
	return dao.NewSessionEventProjectionDAO(nil).Seq(context.Background(), db.Bun(), sessionID, entryID)
}

type turnBoundsRow struct {
	Start int64  `bun:"start_seq"`
	End   *int64 `bun:"end_seq"`
}

func conversationTurnBoundsForID(t *testing.T, sessionDir, sessionID, turnID string) (int64, int64, error) {
	t.Helper()
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return 0, 0, err
	}
	var row turnBoundsRow
	if err := db.Bun().NewSelect().Table("conversation_turns").
		Column("start_seq", "end_seq").
		Where("session_id = ? AND id = ?", sessionID, turnID).
		Scan(context.Background(), &row); err != nil {
		return 0, 0, err
	}
	if row.End == nil {
		return row.Start, 0, nil
	}
	return row.Start, *row.End, nil
}

// TestListSplitContentStoreSessionsReportsExactCounts guards the diagnostic query
// against a cross join: the listing must agree with the per-session probe, or the
// doctor total would overstate the delta by the size of the whole entries table.
func TestListSplitContentStoreSessionsReportsExactCounts(t *testing.T) {
	sessionDir := t.TempDir()
	creator := flippedSessionWithDeltaForTest(t, sessionDir, "split-listing-a", "/tmp/event-split-listing-a", 3)
	elapseContentStoreRetireGraceForTest(t, sessionDir, "split-listing-a")
	_ = creator
	flippedSessionWithDeltaForTest(t, sessionDir, "split-listing-b", "/tmp/event-split-listing-b", 1)

	sessions, err := ListSplitContentStoreSessions(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("split sessions = %#v, want two", sessions)
	}
	want := map[string]int64{"split-listing-a": 3, "split-listing-b": 1}
	for _, split := range sessions {
		if split.UncoveredRows != want[split.SessionID] {
			t.Fatalf("session %s uncovered rows = %d, want %d", split.SessionID, split.UncoveredRows, want[split.SessionID])
		}
	}
}

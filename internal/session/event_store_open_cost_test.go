package session

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/provider"
)

// convergedEventStoreSessionForTest builds an event-store session with committed
// content and no legacy payload rows: initialize, flip the content store, then
// append through the EventStore while the Manager is already in event-store mode.
// The returned Manager is the one that created the session; tests reopen the
// handle path to measure what an open actually costs.
func convergedEventStoreSessionForTest(t *testing.T, sessionDir, sessionID string) *Manager {
	t.Helper()
	manager := New("/tmp/event-open-cost", sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatalf("init session: %v", err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatalf("flip session onto the EventStore: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := manager.AppendMessage(provider.NewUserMessage(fmt.Sprintf("message-%d", i))); err != nil {
			t.Fatalf("append message %d: %v", i, err)
		}
	}
	return manager
}

// TestOpenConvergedEventStoreSessionScansOnce pins the open path to a single
// physical EventStore scan. Before the observation was threaded through
// convergence and the turn-boundary repair, one open inspected the whole log
// three times: once to replay, once to converge, and once more to repair
// `conversation_turns`. The assertion is deterministic (the process-local
// FullScans counter) and never measures wall time.
func TestOpenConvergedEventStoreSessionScansOnce(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "open-scan-once"
	creator := convergedEventStoreSessionForTest(t, sessionDir, sessionID)
	if err := CloseDatabases(); err != nil {
		t.Fatalf("close databases: %v", err)
	}

	before := EventStoreWriteStats().FullScans
	opened, err := Open(creator.GetFile())
	if err != nil {
		t.Fatalf("open converged session: %v", err)
	}
	scanned := EventStoreWriteStats().FullScans - before
	if scanned > 1 {
		t.Fatalf("opening a converged session performed %d whole-log scans, want at most 1", scanned)
	}
	if !opened.eventStoreModeForTest() {
		t.Fatal("opened manager did not load through the EventStore")
	}
	if got := opened.GetMessages(); !equalReplay(got, []string{"message-0", "message-1", "message-2"}) {
		t.Fatalf("opened replay = %#v, want the three committed messages", textsOf(got))
	}

	// A second open has no cross-open scan cache by design: a new Manager must
	// observe the current log rather than inherit another open's verdict. The
	// point of the assertion is that the per-open cost stays bounded at one scan
	// instead of growing with the number of convergence steps.
	beforeSecond := EventStoreWriteStats().FullScans
	second, err := Open(creator.GetFile())
	if err != nil {
		t.Fatalf("reopen converged session: %v", err)
	}
	secondScan := EventStoreWriteStats().FullScans - beforeSecond
	if secondScan > 1 {
		t.Fatalf("reopening a converged session performed %d whole-log scans, want at most 1", secondScan)
	}
	if total := scanned + secondScan; total > 2 {
		t.Fatalf("two opens performed %d whole-log scans, want at most 2", total)
	}
	if got := second.GetMessages(); !equalReplay(got, []string{"message-0", "message-1", "message-2"}) {
		t.Fatalf("second open replay = %#v, want the three committed messages", textsOf(got))
	}
	if err := CloseDatabases(); err != nil {
		t.Fatalf("close databases: %v", err)
	}
}

// TestMigrateToEventStoreReinspectsAfterPhysicalChange pins the boundary of the
// open-convergence reuse: "converged" is an in-memory verdict about the bytes the
// open observed, so it stops being reusable as soon as the physical log changes.
//
// The first half is the unchanged case: a freshly opened converged session binds
// with zero whole-log scans and zero write transactions. The second half appends
// an orphan batch straight to the EventStore, with no SQLite intent to account
// for it. The next MigrateToEventStore must notice the layout changed, inspect
// the store again (FullScans delta >= 1), and fail closed instead of silently
// reusing the earlier convergence.
func TestMigrateToEventStoreReinspectsAfterPhysicalChange(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "migrate-reinspect-after-append"
	creator := convergedEventStoreSessionForTest(t, sessionDir, sessionID)
	if err := CloseDatabases(); err != nil {
		t.Fatalf("close databases: %v", err)
	}

	opened, err := Open(creator.GetFile())
	if err != nil {
		t.Fatalf("open converged session: %v", err)
	}

	// Unchanged disk: the open's convergence is still the current observation.
	beforeWrites := writeRootTransactionCount()
	beforeScans := EventStoreWriteStats().FullScans
	if err := opened.MigrateToEventStore(context.Background()); err != nil {
		t.Fatalf("bind unchanged converged session: %v", err)
	}
	if writes := writeRootTransactionCount() - beforeWrites; writes != 0 {
		t.Fatalf("binding an unchanged converged session opened %d root write transactions, want 0", writes)
	}
	if scans := EventStoreWriteStats().FullScans - beforeScans; scans != 0 {
		t.Fatalf("binding an unchanged converged session performed %d whole-log scans, want 0", scans)
	}

	// Orphan bytes with no SQLite intent: the canonical store is no longer
	// replayable and the recorded convergence fingerprint no longer matches.
	store, err := NewEventStore(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("open event store: %v", err)
	}
	if _, err := store.Append("orphan-batch", sessionID, "", "", []EventRecord{{
		EventID: "orphan-event", SessionID: sessionID, Kind: "message", Payload: json.RawMessage(`{}`),
	}}); err != nil {
		t.Fatalf("append orphan batch: %v", err)
	}

	beforeReinspect := EventStoreWriteStats().FullScans
	if err := opened.MigrateToEventStore(context.Background()); err == nil {
		t.Fatal("MigrateToEventStore silently reused a stale convergence after the physical log changed, want an error")
	}
	if scans := EventStoreWriteStats().FullScans - beforeReinspect; scans < 1 {
		t.Fatalf("MigrateToEventStore performed %d whole-log scans after the log changed, want at least 1 re-inspection", scans)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatalf("close databases: %v", err)
	}
}

// TestOpenConvergedEventStoreSessionSkipsLegacyCleanupWrite pins the write half
// of the open cost: with no legacy payload duplicate left, convergence must not
// open the single writer lock just to run a DELETE that can never match.
//
// The counter is the package's WriteRootDatabase transaction boundary, not a
// timer: opening a converged session must perform zero such transactions.
func TestOpenConvergedEventStoreSessionSkipsLegacyCleanupWrite(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "open-no-legacy-write"
	creator := convergedEventStoreSessionForTest(t, sessionDir, sessionID)
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != 0 {
		t.Fatalf("fixture holds %d legacy payload rows, want a converged session", count)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatalf("close databases: %v", err)
	}

	before := writeRootTransactionCount()
	opened, err := Open(creator.GetFile())
	if err != nil {
		t.Fatalf("open converged session: %v", err)
	}
	if writes := writeRootTransactionCount() - before; writes != 0 {
		t.Fatalf("opening a converged session opened %d root write transactions, want 0", writes)
	}
	if got := opened.GetMessages(); !equalReplay(got, []string{"message-0", "message-1", "message-2"}) {
		t.Fatalf("opened replay = %#v, want the three committed messages", textsOf(got))
	}

	// Binding the same manager must stay write-free too: MigrateToEventStore
	// reuses the convergence the open already performed instead of converging
	// (and scanning) a second time.
	beforeBind := writeRootTransactionCount()
	beforeScans := EventStoreWriteStats().FullScans
	if err := opened.MigrateToEventStore(context.Background()); err != nil {
		t.Fatalf("bind converged session: %v", err)
	}
	if writes := writeRootTransactionCount() - beforeBind; writes != 0 {
		t.Fatalf("binding a converged session opened %d root write transactions, want 0", writes)
	}
	if scans := EventStoreWriteStats().FullScans - beforeScans; scans != 0 {
		t.Fatalf("binding a converged session performed %d whole-log scans, want 0", scans)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatalf("close databases: %v", err)
	}
}

// TestOpenRemovesLegacyDuplicateAndRepairsTurnBoundsInOneScan is the preservation
// half: a flipped session is not necessarily a converged one. The crash window
// between the content-store flip and cleanup leaves legacy payload rows behind,
// and a session flipped by an older build keeps `conversation_turns` bounds that
// name deleted legacy sequences. Opening such a session must still retire the
// duplicate and translate the bounds, while paying only the single scan the open
// already performed.
func TestOpenRemovesLegacyDuplicateAndRepairsTurnBoundsInOneScan(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "open-unconverged-heal"
	const workDir = "/tmp/event-open-unconverged"

	// A completed legacy transcript with one closed turn, exported for real.
	manager := New(workDir, sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	if err := StartConversationTurn(sessionDir, ConversationTurn{
		ID: "turn-1", SessionID: sessionID, IntentID: "intent-1", RunID: "run-1",
		Kind: "conversation", Status: "open", StartedAt: startedAt,
	}); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	// The standalone helper wrote its turn_start row straight to the database,
	// so the Manager's leaf must be resynchronised before the next append.
	if err := manager.Reload(); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	assistant := provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "world"}})
	if _, err := manager.AppendMessage(assistant); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if err := EndConversationTurn(sessionDir, sessionID, "turn-1", "completed", "end_turn", startedAt.Add(time.Second)); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()
	legacyBounds := conversationTurnBoundsForTest(t, sessionDir, sessionID)

	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventBounds := markerBoundsForTest(t, sessionDir, sessionID)
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}

	// Stage the two un-converged hazards: the legacy payload duplicate and the
	// pre-remap control rows.
	forceLegacyPayloadDuplicateForTest(t, sessionDir, sessionID)
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count == 0 {
		t.Fatal("setup did not leave a legacy payload duplicate to converge away")
	}
	rootDB := mustOpenRootDB(t, sessionDir)
	if _, err := rootDB.Bun().Exec("UPDATE conversation_turns SET start_seq = ?, end_seq = ? WHERE session_id = ? AND id = ?",
		legacyBounds.start, legacyBounds.end, sessionID, "turn-1"); err != nil {
		t.Fatal(err)
	}
	if staged := conversationTurnBoundsForTest(t, sessionDir, sessionID); staged != legacyBounds {
		t.Fatalf("staged bounds = %+v, want the legacy numbers %+v", staged, legacyBounds)
	}

	before := EventStoreWriteStats().FullScans
	if _, err := Open(manager.GetFile()); err != nil {
		t.Fatalf("open un-converged session: %v", err)
	}
	if scanned := EventStoreWriteStats().FullScans - before; scanned > 1 {
		t.Fatalf("healing an un-converged session performed %d whole-log scans, want at most 1", scanned)
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != 0 {
		t.Fatalf("open left %d legacy payload rows, want the duplicate removed", count)
	}
	if bounds := conversationTurnBoundsForTest(t, sessionDir, sessionID); bounds != eventBounds {
		t.Fatalf("turn bounds after open = %+v, want the event-log markers %+v", bounds, eventBounds)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
}

// TestConvergeEventStoreStandaloneInspectsOnce covers the public entry point
// directly: ConvergeEventStore must observe the log once and reuse that
// observation for the control-plane repair, exactly like an open does.
func TestConvergeEventStoreStandaloneInspectsOnce(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "converge-standalone-once"
	convergedEventStoreSessionForTest(t, sessionDir, sessionID)
	if err := CloseDatabases(); err != nil {
		t.Fatalf("close databases: %v", err)
	}

	manager, err := OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	before := EventStoreWriteStats().FullScans
	if err := manager.ConvergeEventStore(context.Background()); err != nil {
		t.Fatalf("converge event store: %v", err)
	}
	if scanned := EventStoreWriteStats().FullScans - before; scanned > 1 {
		t.Fatalf("standalone convergence performed %d whole-log scans, want at most 1", scanned)
	}
	if got := manager.GetMessages(); !equalReplay(got, []string{"message-0", "message-1", "message-2"}) {
		t.Fatalf("converged replay = %#v, want the three committed messages", textsOf(got))
	}
	if err := CloseDatabases(); err != nil {
		t.Fatalf("close databases: %v", err)
	}
}

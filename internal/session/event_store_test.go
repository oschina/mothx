package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

func TestEventStoreAppendsChecksummedBatch(t *testing.T) {
	store, err := NewEventStore(t.TempDir(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.Append("batch-1", "session-1", "run-1", "turn-1", []EventRecord{
		{EventID: "event-1", Kind: "user_message", Payload: json.RawMessage(`{"text":"hello"}`)},
		{EventID: "event-2", Kind: "assistant_message", Payload: json.RawMessage(`{"text":"world"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if batch.SeqFrom != eventStoreSeqBase || batch.SeqTo != eventStoreSeqBase+1 || batch.Digest == "" {
		t.Fatalf("batch = %#v", batch)
	}
	batches, err := store.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || len(batches[0].Events) != 2 {
		t.Fatalf("scanned batches = %#v", batches)
	}
	if batches[0].Events[0].EventID != "event-1" || batches[0].Events[1].SessionID != "session-1" {
		t.Fatalf("scanned events = %#v", batches[0].Events)
	}
}

func TestEventStoreAppendCacheKeepsSequenceAcrossStoreInstances(t *testing.T) {
	before := EventStoreWriteStats()
	root := t.TempDir()
	first, err := NewEventStore(root, "session-cache")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Append("batch-cache-1", "session-cache", "", "", []EventRecord{{EventID: "cache-event-1", Kind: "message", Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	second, err := NewEventStore(root, "session-cache")
	if err != nil {
		t.Fatal(err)
	}
	batch, err := second.Append("batch-cache-2", "session-cache", "", "", []EventRecord{{EventID: "cache-event-2", Kind: "message", Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if batch.SeqFrom != eventStoreSeqBase+1 || batch.SeqTo != eventStoreSeqBase+1 {
		t.Fatalf("cached append sequence = %d-%d, want %d-%d", batch.SeqFrom, batch.SeqTo, eventStoreSeqBase+1, eventStoreSeqBase+1)
	}
	after := EventStoreWriteStats()
	if after.AppendBatches-before.AppendBatches != 2 || after.AppendEvents-before.AppendEvents != 2 || after.AppendBytes <= before.AppendBytes || after.CacheHits <= before.CacheHits {
		t.Fatalf("append stats before=%#v after=%#v, want two batches/events, bytes, and cache hit", before, after)
	}
}

func TestEventStoreRejectsDuplicateEventIDAcrossBatches(t *testing.T) {
	store, err := NewEventStore(t.TempDir(), "session-duplicate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append("batch-duplicate-1", "session-duplicate", "", "", []EventRecord{{EventID: "event-duplicate", Kind: "message", Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append("batch-duplicate-2", "session-duplicate", "", "", []EventRecord{{EventID: "event-duplicate", Kind: "message", Payload: json.RawMessage(`{}`)}}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate event append error = %v, want duplicate rejection", err)
	}
}

func eventStoreActivePath(t *testing.T, store *EventStore) string {
	t.Helper()
	layout, err := store.scanLayout()
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.paths) == 0 {
		t.Fatal("event store has no active segment")
	}
	return layout.paths[len(layout.paths)-1]
}

func TestEventStoreScanRejectsPhysicalDuplicateEventID(t *testing.T) {
	root := t.TempDir()
	store, err := NewEventStore(root, "session-physical-duplicate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append("batch-physical-1", "session-physical-duplicate", "", "", []EventRecord{{EventID: "duplicate-a", Kind: "message", Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append("batch-physical-2", "session-physical-duplicate", "", "", []EventRecord{{EventID: "duplicate-b", Kind: "message", Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	path := eventStoreActivePath(t, store)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.Replace(string(contents), "duplicate-b", "duplicate-a", 1))
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Scan(); err == nil || !strings.Contains(err.Error(), "duplicate event ID") {
		t.Fatalf("physical duplicate scan error = %v, want duplicate rejection", err)
	}
}

// TestEventStoreInspectDoesNotMutateFilesystem pins the read-only contract that
// session load and every other reconciliation path depend on. Inspection runs
// without the fenced writer lease, so it may not write anything at all — in
// particular the rebuildable manifest. Regressing this turns a plain "look at
// the store" call into a filesystem writer, which races an unrelated teardown
// (observed as a manifest.json appearing inside a t.TempDir() after cleanup had
// already started, failing the TUI run tests).
func TestEventStoreInspectDoesNotMutateFilesystem(t *testing.T) {
	root := t.TempDir()
	store, err := NewEventStore(root, "session-inspect-readonly")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append("batch-1", "session-inspect-readonly", "", "", []EventRecord{{EventID: "event-1", Kind: "message", Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	path := eventStoreActivePath(t, store)
	// A real crash tail: begin line plus an unterminated event line. This is the
	// only shape that makes "did Inspect write?" observable, because the
	// repairing scan truncates it.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"kind":"batch_begin","batchId":"crashed","seqFrom":2,"seqTo":2,"count":1}` + "\n" + `{"kind":"event","batchId":"crashed","seq":2,"index":0,"count":1,"eventId":"event-crashed","sessionId":"session-inspect-readonly","eventKind":"message","payload":{}`); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Append legitimately publishes the manifest; removal below isolates the
	// Inspect call so that a re-created manifest cannot be mistaken for one the
	// append path left behind.
	manifestPath := filepath.Join(store.Path(), eventStoreManifest)
	if _, statErr := os.Stat(manifestPath); statErr != nil {
		t.Fatalf("append did not publish the manifest: %v", statErr)
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}

	// Read-only inspection cannot repair, so the tail stays an explicit error
	// rather than a silently skipped record.
	if _, err := store.Inspect(); err == nil || !strings.Contains(err.Error(), "incomplete middle record") {
		t.Fatalf("Inspect error = %v, want incomplete tail rejection", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("Inspect rewrote a segment it was only reading")
	}
	infoAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !infoAfter.ModTime().Equal(infoBefore.ModTime()) || infoAfter.Size() != infoBefore.Size() {
		t.Fatalf("Inspect rewrote the segment: %d@%s -> %d@%s", infoBefore.Size(), infoBefore.ModTime(), infoAfter.Size(), infoAfter.ModTime())
	}
	if _, statErr := os.Lstat(manifestPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Inspect materialised the rebuildable manifest: statErr=%v", statErr)
	}

	// The repairing scan still owns the fix, so read-only is a narrowing of who
	// may repair rather than a loss of the repair.
	repaired, err := store.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired) != 1 || repaired[0].BatchID != "batch-1" {
		t.Fatalf("scan batches after inspect = %#v", repaired)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "crashed") {
		t.Fatal("Scan no longer repairs the incomplete tail after Inspect ran")
	}
	if _, statErr := os.Stat(manifestPath); statErr != nil {
		t.Fatalf("Scan did not rebuild the manifest: %v", statErr)
	}
}

func TestEventStoreRepairsOnlyIncompleteTail(t *testing.T) {
	root := t.TempDir()
	store, err := NewEventStore(root, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append("batch-1", "session-1", "", "", []EventRecord{{EventID: "event-1", Kind: "message", Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	path := eventStoreActivePath(t, store)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"kind":"batch_begin","batchId":"crashed","seqFrom":1,"seqTo":1,"count":1}` + "\n" + `{"kind":"event","batchId":"crashed","seq":1,"index":0,"count":1,"eventId":"event-2","sessionId":"session-1","eventKind":"message","payload":{}}`); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	batches, err := store.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || batches[0].SeqTo != eventStoreSeqBase {
		t.Fatalf("batches after tail repair = %#v", batches)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "crashed") {
		t.Fatal("incomplete tail was not removed")
	}
}

func TestEventStoreRejectsMiddleCorruption(t *testing.T) {
	root := t.TempDir()
	store, err := NewEventStore(root, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append("batch-1", "session-1", "", "", []EventRecord{{EventID: "event-1", Kind: "message", Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	path := eventStoreActivePath(t, store)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents[0] = '{'
	contents[1] = 'x'
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Scan(); err == nil || !strings.Contains(err.Error(), "event store corruption") {
		t.Fatalf("Scan error = %v, want corruption", err)
	}
}

// TestEventStoreRotatesSegmentsAtTheSizeCeiling proves the physical layout stays
// replayable across segment boundaries: sequences remain global, sealed segments
// are never rewritten, and the manifest reflects the active segment list.
func TestEventStoreRotatesSegmentsAtTheSizeCeiling(t *testing.T) {
	root := t.TempDir()
	eventStoreRollBytes.Store(1024)
	t.Cleanup(func() { eventStoreRollBytes.Store(0) })
	store, err := NewEventStore(root, "session-rotate")
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"text":"` + strings.Repeat("x", 512) + `"}`)
	for i, batchID := range []string{"rotate-1", "rotate-2", "rotate-3"} {
		if _, err := store.Append(batchID, "session-rotate", "", "", []EventRecord{{
			EventID: fmt.Sprintf("rotate-event-%d", i), Kind: "message", Payload: payload,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	layout, err := store.scanLayout()
	if err != nil {
		t.Fatal(err)
	}
	if layout.legacy {
		t.Fatal("rotated session must not use the legacy single-file layout")
	}
	if len(layout.paths) < 2 {
		t.Fatalf("segment count = %d, want rotation", len(layout.paths))
	}
	sealed := layout.paths[:len(layout.paths)-1]
	sealedBefore := make([]string, len(sealed))
	for i, path := range sealed {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		sealedBefore[i] = fmt.Sprintf("%s:%d:%s", filepath.Base(path), info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano))
	}
	batches, err := store.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 3 {
		t.Fatalf("batches = %d, want 3", len(batches))
	}
	for i, batch := range batches {
		want := int64(i) + eventStoreSeqBase
		if batch.SeqFrom != want || batch.SeqTo != want {
			t.Fatalf("batch %d sequence = %d-%d, want %d-%d", i, batch.SeqFrom, batch.SeqTo, want, want)
		}
	}
	for i, path := range sealed {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := fmt.Sprintf("%s:%d:%s", filepath.Base(path), info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano)); got != sealedBefore[i] {
			t.Fatalf("sealed segment was rewritten: %s -> %s", sealedBefore[i], got)
		}
	}
	manifestPath := filepath.Join(store.Path(), eventStoreManifest)
	encoded, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest EventManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.NextSeq != eventStoreSeqBase+3 || len(manifest.Active) != len(layout.paths) {
		t.Fatalf("manifest = %#v, want %d active segments and next seq %d", manifest, len(layout.paths), eventStoreSeqBase+3)
	}
	for i, active := range manifest.Active {
		want, relErr := filepath.Rel(store.Path(), filepath.Join(store.Path(), filepath.FromSlash(active)))
		if relErr != nil {
			t.Fatal(relErr)
		}
		if filepath.ToSlash(want) != active || !strings.HasPrefix(active, eventStoreSegmentsDir+"/") {
			t.Fatalf("manifest active[%d] = %q, want relative segment path (%q)", i, active, want)
		}
	}
	// A missing manifest is rebuildable: it is never canonical state.
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(manifestPath); statErr != nil {
		t.Fatalf("manifest was not rebuilt after scan: %v", statErr)
	}
}

func TestInspectEventStoreFailsClosedForOrphanBatch(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-store", sessionDir)
	if err := manager.InitWithID("session-1"); err != nil {
		t.Fatal(err)
	}
	store, err := NewEventStore(sessionDir, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append("orphan-batch", "session-1", "", "", []EventRecord{{EventID: "event-1", Kind: "message", Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectEventStore(context.Background(), sessionDir, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != EventStoreBlocked {
		t.Fatalf("state = %q, want %q (%s)", inspection.State, EventStoreBlocked, inspection.Reason)
	}
}

// TestEventStoreCorruptionIsSessionScoped proves the blast radius of a damaged
// content store stays inside one session directory: another session's Run,
// lease, decision and listing surfaces must remain usable.
func TestEventStoreCorruptionIsSessionScoped(t *testing.T) {
	sessionDir := t.TempDir()
	broken := New("/tmp/event-isolation", sessionDir)
	if err := broken.InitWithID("session-broken"); err != nil {
		t.Fatal(err)
	}
	if err := broken.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "session-broken")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broken.AppendMessage(provider.NewUserMessage("broken")); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()

	healthy := New("/tmp/event-isolation", sessionDir)
	if err := healthy.InitWithID("session-healthy"); err != nil {
		t.Fatal(err)
	}
	if err := healthy.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	healthyGuard, err := AcquireMutation(sessionDir, "session-healthy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := healthy.AppendMessage(provider.NewUserMessage("healthy")); err != nil {
		healthyGuard.Release()
		t.Fatal(err)
	}
	healthyGuard.Release()

	brokenStore, err := NewEventStore(sessionDir, "session-broken")
	if err != nil {
		t.Fatal(err)
	}
	layout, err := brokenStore.scanLayout()
	if err != nil {
		t.Fatal(err)
	}
	path := layout.paths[len(layout.paths)-1]
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents[0] = '{'
	contents[1] = 'x'
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	ForgetEventStoreCaches(sessionDir)

	inspection, err := InspectEventStore(context.Background(), sessionDir, "session-broken")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != EventStoreCorrupt {
		t.Fatalf("damaged session state = %q, want %q (%s)", inspection.State, EventStoreCorrupt, inspection.Reason)
	}
	healthyInspection, err := InspectEventStore(context.Background(), sessionDir, "session-healthy")
	if err != nil {
		t.Fatal(err)
	}
	if healthyInspection.State != EventStoreHealthy {
		t.Fatalf("neighbouring session state = %q, want %q (%s)", healthyInspection.State, EventStoreHealthy, healthyInspection.Reason)
	}
	messages := healthy.GetMessages()
	if len(messages) != 1 || messages[0].Content != "healthy" {
		t.Fatalf("neighbouring session replay = %#v", messages)
	}
	projected, err := ReadCommittedEventBatches(context.Background(), sessionDir, "session-healthy")
	if err != nil || len(projected) == 0 {
		t.Fatalf("neighbouring committed batches = %d, err = %v", len(projected), err)
	}
	listed, err := ListAll(sessionDir)
	if err != nil || len(listed) != 2 {
		t.Fatalf("session listing with one damaged store = %d sessions, err = %v", len(listed), err)
	}
}

// TestEventIntentCommitRefusedAfterLeaseTakeover proves an old owner cannot
// canonicalize bytes once a competing process bumped the lease epoch, even
// though its event bytes are already durable on disk.
func TestEventIntentCommitRefusedAfterLeaseTakeover(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-takeover", sessionDir)
	if err := manager.InitWithID("session-takeover"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "session-takeover")
	if err != nil {
		t.Fatal(err)
	}
	binding := guard.Binding()
	event, err := EventRecordFromEntry(MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "takeover-event", Timestamp: time.Now().UTC()}, Message: provider.NewUserMessage("takeover")}, "session-takeover", "", "")
	if err != nil {
		guard.Release()
		t.Fatal(err)
	}
	digest, err := DigestEventBatch([]EventRecord{event})
	if err != nil {
		guard.Release()
		t.Fatal(err)
	}
	spec := EventIntentSpec{ID: "takeover-batch", SessionID: "session-takeover", SeqFrom: eventStoreSeqBase, SeqTo: eventStoreSeqBase, Digest: digest, OwnerInstanceID: binding.OwnerInstanceID, LeaseEpoch: binding.Epoch, LeaseTokenHash: binding.TokenHash}
	if _, err := AppendPreparedEventBatch(context.Background(), sessionDir, spec, []EventRecord{event}); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	// The old owner voluntarily releases its guard, then a competing process
	// takes the tombstone row over through the same fenced CAS the runtime
	// uses: owner identity, token and epoch all advance together. The intent
	// keeps the *old* identity, which is exactly the displaced-owner case.
	guard.Release()
	replacement, err := AcquireMutation(sessionDir, "session-takeover")
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Release()
	rebound := replacement.Binding()
	if rebound.Epoch <= binding.Epoch || rebound.OwnerInstanceID == "" {
		t.Fatalf("replacement lease binding = %#v, want a bumped epoch over %#v", rebound, binding)
	}
	if err := CommitEventIntent(context.Background(), sessionDir, spec, nil); !errors.Is(err, ErrEventIntentFenced) {
		t.Fatalf("commit after takeover error = %v, want ErrEventIntentFenced", err)
	}
	committed, err := ReadCommittedEventBatches(context.Background(), sessionDir, "session-takeover")
	if err != nil {
		t.Fatal(err)
	}
	for _, batch := range committed {
		if batch.BatchID == spec.ID {
			t.Fatal("taken-over owner committed its batch")
		}
	}
}

// TestRebuildEventProjectionAfterSegmentRotation proves the rebuildable SQLite
// index can be regenerated from committed segments regardless of how the
// physical layout rolled.
func TestRebuildEventProjectionAfterSegmentRotation(t *testing.T) {
	sessionDir := t.TempDir()
	eventStoreRollBytes.Store(1024)
	t.Cleanup(func() { eventStoreRollBytes.Store(0) })
	manager := New("/tmp/event-rebuild", sessionDir)
	if err := manager.InitWithID("session-rebuild"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "session-rebuild")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	payload := strings.Repeat("y", 600)
	for i := 0; i < 4; i++ {
		if _, err := manager.AppendMessage(provider.NewUserMessage(fmt.Sprintf("%s%d", payload, i))); err != nil {
			t.Fatal(err)
		}
	}
	store, err := NewEventStore(sessionDir, "session-rebuild")
	if err != nil {
		t.Fatal(err)
	}
	layout, err := store.scanLayout()
	if err != nil {
		t.Fatal(err)
	}
	if layout.legacy || len(layout.paths) < 2 {
		t.Fatalf("layout = %#v, want rotated segments", layout)
	}
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var before int
	if err := database.Bun().QueryRow("SELECT COUNT(*) FROM session_event_projections WHERE session_id = ?", "session-rebuild").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 4 {
		t.Fatalf("projections before rebuild = %d, want 4", before)
	}
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionEventProjectionDAO(nil).DeleteSession(context.Background(), tx, "session-rebuild")
	}); err != nil {
		t.Fatal(err)
	}
	if err := RebuildEventProjection(context.Background(), sessionDir, "session-rebuild", guard); err != nil {
		t.Fatal(err)
	}
	database, err = OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var after int
	if err := database.Bun().QueryRow("SELECT COUNT(*) FROM session_event_projections WHERE session_id = ?", "session-rebuild").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 4 {
		t.Fatalf("projections after rebuild = %d, want 4", after)
	}
	messages := manager.GetMessages()
	if len(messages) != 4 {
		t.Fatalf("replay after rebuild = %d messages", len(messages))
	}
}

func TestInspectEventStoreAcceptsCommittedBatch(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-store", sessionDir)
	if err := manager.InitWithID("session-1"); err != nil {
		t.Fatal(err)
	}
	store, err := NewEventStore(sessionDir, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.Append("batch-1", "session-1", "run-1", "turn-1", []EventRecord{{EventID: "event-1", Kind: "message", Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	intent := &dao.SessionEventIntentRecord{ID: batch.BatchID, SessionID: "session-1", RunID: "run-1", TurnID: "turn-1", SeqFrom: batch.SeqFrom, SeqTo: batch.SeqTo, Digest: batch.Digest, State: EventIntentCommitted, CreatedAt: now, UpdatedAt: now}
	if err := dao.NewSessionEventIntentDAO(database.Bun()).Insert(context.Background(), database.Bun(), intent); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectEventStore(context.Background(), sessionDir, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != EventStoreHealthy {
		t.Fatalf("state = %q, want %q (%s)", inspection.State, EventStoreHealthy, inspection.Reason)
	}
}

func TestEventIntentProtocolCommitsOnlyWithFencedIdentity(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-store", sessionDir)
	if err := manager.InitWithID("session-1"); err != nil {
		t.Fatal(err)
	}
	spec := EventIntentSpec{
		ID: "batch-1", SessionID: "session-1", RunID: "run-1", TurnID: "turn-1",
		SeqFrom: eventStoreSeqBase, SeqTo: eventStoreSeqBase, OwnerInstanceID: "owner-1", LeaseEpoch: 7, LeaseTokenHash: "token-1",
	}
	now := time.Now().Unix()
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewRuntimeLeaseDAO(nil).Insert(context.Background(), tx, &dao.RuntimeLeaseRecord{
			SessionID: "session-1", OwnerID: spec.OwnerInstanceID, TokenHash: spec.LeaseTokenHash,
			Epoch: spec.LeaseEpoch, RunID: spec.RunID, Purpose: string(RuntimeLeasePurposeExecution),
			State: "active", AcquiredAt: now, HeartbeatAt: now, ExpiresAt: now + 60, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	event, err := EventRecordFromEntry(MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "event-1", Timestamp: time.Now().UTC()}, Message: provider.NewUserMessage("hello")}, "session-1", spec.RunID, spec.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AppendPreparedEventBatch(context.Background(), sessionDir, spec, []EventRecord{event}); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectEventStore(context.Background(), sessionDir, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != EventStoreReconciling {
		t.Fatalf("state before commit = %q, want %q", inspection.State, EventStoreReconciling)
	}
	committed, err := ReadCommittedEventBatches(context.Background(), sessionDir, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 0 {
		t.Fatalf("committed batches before commit = %d, want 0", len(committed))
	}
	wrong := spec
	wrong.LeaseEpoch++
	if err := CommitEventIntent(context.Background(), sessionDir, wrong, nil); !errors.Is(err, ErrEventIntentFenced) {
		t.Fatalf("wrong fenced identity error = %v, want ErrEventIntentFenced", err)
	}
	if err := CommitEventIntent(context.Background(), sessionDir, spec, nil); err != nil {
		t.Fatal(err)
	}
	inspection, err = InspectEventStore(context.Background(), sessionDir, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != EventStoreHealthy {
		t.Fatalf("state after commit = %q, want %q (%s)", inspection.State, EventStoreHealthy, inspection.Reason)
	}
	committed, err = ReadCommittedEventBatches(context.Background(), sessionDir, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 1 || len(committed[0].Events) != 1 {
		t.Fatalf("committed batches after commit = %#v", committed)
	}
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var projectionCount int
	if err := database.Bun().QueryRow("SELECT COUNT(*) FROM session_event_projections WHERE session_id = ?", "session-1").Scan(&projectionCount); err != nil {
		t.Fatal(err)
	}
	if projectionCount != 1 {
		t.Fatalf("event projections = %d, want 1", projectionCount)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(manager.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	messages := reopened.GetMessages()
	if len(messages) != 1 || messages[0].Content != "hello" {
		t.Fatalf("event-backed replay = %#v, want one hello message", messages)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
}

func TestEventStoreRejectsNewAppendWhileIntentNeedsReconciliation(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-reconciling", sessionDir)
	if err := manager.InitWithID("event-reconciling-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "event-reconciling-session")
	if err != nil {
		t.Fatal(err)
	}
	binding := guard.Binding()
	event, err := EventRecordFromEntry(MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "pending-event", Timestamp: time.Now().UTC()}, Message: provider.NewUserMessage("pending")}, "event-reconciling-session", "", "")
	if err != nil {
		guard.Release()
		t.Fatal(err)
	}
	digest, err := DigestEventBatch([]EventRecord{event})
	if err != nil {
		guard.Release()
		t.Fatal(err)
	}
	spec := EventIntentSpec{ID: "pending-batch", SessionID: "event-reconciling-session", SeqFrom: eventStoreSeqBase, SeqTo: eventStoreSeqBase, Digest: digest, OwnerInstanceID: binding.OwnerInstanceID, LeaseEpoch: binding.Epoch, LeaseTokenHash: binding.TokenHash}
	if _, err := AppendPreparedEventBatch(context.Background(), sessionDir, spec, []EventRecord{event}); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("must not append")); err == nil || !strings.Contains(err.Error(), "reconciling") {
		guard.Release()
		t.Fatalf("append during reconciliation error = %v, want blocked", err)
	}
	guard.Release()
}

func TestEventStoreReconciliationBlocksMissingAppendedBytes(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-missing", sessionDir)
	if err := manager.InitWithID("event-missing-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "event-missing-session")
	if err != nil {
		t.Fatal(err)
	}
	binding := guard.Binding()
	event, err := EventRecordFromEntry(MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "missing-event", Timestamp: time.Now().UTC()}, Message: provider.NewUserMessage("missing")}, "event-missing-session", "run-missing", "turn-missing")
	if err != nil {
		guard.Release()
		t.Fatal(err)
	}
	digest, err := DigestEventBatch([]EventRecord{event})
	if err != nil {
		guard.Release()
		t.Fatal(err)
	}
	spec := EventIntentSpec{ID: "missing-batch", SessionID: "event-missing-session", RunID: "run-missing", TurnID: "turn-missing", SeqFrom: eventStoreSeqBase, SeqTo: eventStoreSeqBase, Digest: digest, OwnerInstanceID: binding.OwnerInstanceID, LeaseEpoch: binding.Epoch, LeaseTokenHash: binding.TokenHash}
	if _, err := AppendPreparedEventBatch(context.Background(), sessionDir, spec, []EventRecord{event}); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	missingStore, err := NewEventStore(sessionDir, "event-missing-session")
	if err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if err := os.RemoveAll(missingStore.Path()); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	inspection, err := InspectEventStore(context.Background(), sessionDir, "event-missing-session")
	if err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if inspection.State != EventStoreInconsistent {
		guard.Release()
		t.Fatalf("missing appended batch state = %q, want inconsistent", inspection.State)
	}
	guard.Release()
}

func TestRecoveryMarksMissingAppendedIntentRequiresRecovery(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-recovery-missing", sessionDir)
	if err := manager.InitWithID("event-recovery-missing-session"); err != nil {
		t.Fatal(err)
	}
	if err := SaveSessionRun(sessionDir, SessionRun{ID: "event-recovery-missing-run", SessionID: "event-recovery-missing-session", Status: "running", StartedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireRecovery(sessionDir, "event-recovery-missing-session", "event-recovery-missing-run")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	binding := guard.Binding()
	store, err := NewEventStore(sessionDir, "event-recovery-missing-session")
	if err != nil {
		t.Fatal(err)
	}
	event, err := EventRecordFromEntry(MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "recovery-missing-event", Timestamp: time.Now().UTC()}, Message: provider.NewUserMessage("missing")}, "event-recovery-missing-session", "event-recovery-missing-run", "")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := DigestEventBatch([]EventRecord{event})
	if err != nil {
		t.Fatal(err)
	}
	spec := EventIntentSpec{ID: "recovery-missing-batch", SessionID: "event-recovery-missing-session", RunID: "event-recovery-missing-run", SeqFrom: eventStoreSeqBase, SeqTo: eventStoreSeqBase, Digest: digest, OwnerInstanceID: binding.OwnerInstanceID, LeaseEpoch: binding.Epoch, LeaseTokenHash: binding.TokenHash}
	if _, err := AppendPreparedEventBatch(context.Background(), sessionDir, spec, []EventRecord{event}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(store.Path()); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileEventIntents(context.Background(), sessionDir, spec.SessionID, spec.RunID, guard, nil); err == nil || !strings.Contains(err.Error(), "missing appended bytes") {
		t.Fatalf("recovery missing bytes error = %v, want missing appended bytes", err)
	}
	inspection, err := InspectEventStore(context.Background(), sessionDir, spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != EventStoreBlocked || len(inspection.Intents) != 1 || inspection.Intents[0].State != EventIntentRecoveryRequired {
		t.Fatalf("recovery intent state = %q intents=%#v, want blocked/recovery_required", inspection.State, inspection.Intents)
	}
	_ = manager
}

func TestRecoveryAbortsPreparedIntentWithoutEventBytes(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-recovery-prepared", sessionDir)
	if err := manager.InitWithID("event-recovery-prepared-session"); err != nil {
		t.Fatal(err)
	}
	if err := SaveSessionRun(sessionDir, SessionRun{ID: "event-recovery-prepared-run", SessionID: "event-recovery-prepared-session", Status: "running", StartedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireRecovery(sessionDir, "event-recovery-prepared-session", "event-recovery-prepared-run")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	binding := guard.Binding()
	spec := EventIntentSpec{ID: "prepared-missing-batch", SessionID: "event-recovery-prepared-session", RunID: "event-recovery-prepared-run", SeqFrom: eventStoreSeqBase, SeqTo: eventStoreSeqBase, Digest: "sha256:missing", OwnerInstanceID: binding.OwnerInstanceID, LeaseEpoch: binding.Epoch, LeaseTokenHash: binding.TokenHash}
	if err := PrepareEventIntent(context.Background(), sessionDir, spec); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileEventIntents(context.Background(), sessionDir, spec.SessionID, spec.RunID, guard, nil); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectEventStore(context.Background(), sessionDir, spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != EventStoreHealthy || len(inspection.Intents) != 1 || inspection.Intents[0].State != EventIntentAborted {
		t.Fatalf("prepared recovery state = %q intents=%#v, want healthy/aborted", inspection.State, inspection.Intents)
	}
	_ = manager
}

func TestReconcileEventIntentUsesRecoveryLease(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-recovery", sessionDir)
	if err := manager.InitWithID("session-recovery"); err != nil {
		t.Fatal(err)
	}
	if err := SaveSessionRun(sessionDir, SessionRun{ID: "run-recovery", SessionID: "session-recovery", Status: "running", StartedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireRecovery(sessionDir, "session-recovery", "run-recovery")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	binding := guard.Binding()
	event, err := EventRecordFromEntry(MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "event-recovery", Timestamp: time.Now().UTC()}, Message: provider.NewUserMessage("recovery")}, "session-recovery", "run-recovery", "")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := DigestEventBatch([]EventRecord{event})
	if err != nil {
		t.Fatal(err)
	}
	spec := EventIntentSpec{ID: "batch-recovery", SessionID: "session-recovery", RunID: "run-recovery", SeqFrom: eventStoreSeqBase, SeqTo: eventStoreSeqBase, Digest: digest, OwnerInstanceID: binding.OwnerInstanceID, LeaseEpoch: binding.Epoch, LeaseTokenHash: binding.TokenHash}
	if err := PrepareEventIntent(context.Background(), sessionDir, spec); err != nil {
		t.Fatal(err)
	}
	store, err := NewEventStore(sessionDir, "session-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(spec.ID, spec.SessionID, spec.RunID, spec.TurnID, []EventRecord{event}); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileEventIntents(context.Background(), sessionDir, spec.SessionID, spec.RunID, guard, nil); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectEventStore(context.Background(), sessionDir, spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != EventStoreHealthy {
		t.Fatalf("reconciled state = %q, want %q (%s)", inspection.State, EventStoreHealthy, inspection.Reason)
	}
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionEventProjectionDAO(nil).DeleteSession(context.Background(), tx, spec.SessionID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := RebuildEventProjection(context.Background(), sessionDir, spec.SessionID, guard); err != nil {
		t.Fatal(err)
	}
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var projectionCount int
	if err := database.Bun().QueryRow("SELECT COUNT(*) FROM session_event_projections WHERE session_id = ?", spec.SessionID).Scan(&projectionCount); err != nil {
		t.Fatal(err)
	}
	if projectionCount != 1 {
		t.Fatalf("rebuilt projections = %d, want 1", projectionCount)
	}
}

func TestManagerAppendEventStoreEntriesUsesCanonicalReplay(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-manager", sessionDir)
	if err := manager.InitWithID("event-manager-session"); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "event-manager-session")
	if err != nil {
		t.Fatal(err)
	}
	first := MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "manager-message-1", Timestamp: time.Now().UTC()}, Message: provider.NewUserMessage("first")}
	parent := first.ID
	second := MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "manager-message-2", ParentID: &parent, Timestamp: time.Now().UTC()}, Message: provider.NewUserMessage("second")}
	ids, err := manager.AppendEventStoreEntries(context.Background(), "", "", []interface{}{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != first.ID || ids[1] != second.ID {
		t.Fatalf("event entry IDs = %#v", ids)
	}
	guard.Release()
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(manager.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	messages := reopened.GetMessages()
	if len(messages) != 2 || messages[0].Content != "first" || messages[1].Content != "second" {
		t.Fatalf("event-backed manager replay = %#v", messages)
	}
	details, err := ListForDirDetailed("/tmp/event-manager", sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0].MessageCount != 2 || details[0].Preview != "first" {
		t.Fatalf("event-backed session details = %#v", details)
	}
	matches, err := ListAll(sessionDir, WithSearch("first"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("event-backed search matches = %#v", matches)
	}
	count, err := CountWithMessages(sessionDir, WithSearch("second"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("event-backed search count = %d, want 1", count)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
}

func TestForkEventStoreSessionCopiesCanonicalEvents(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-fork", sessionDir)
	if err := manager.InitWithID("event-fork-source"); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireMutation(sessionDir, "event-fork-source")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	user := MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "fork-user", Timestamp: now.Add(time.Second)}, Message: provider.NewUserMessage("fork request")}
	assistant := MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "fork-assistant", Timestamp: now.Add(2 * time.Second)}, Message: provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "fork answer"}})}
	if _, err := manager.AppendEventStoreEntries(context.Background(), "", "", []interface{}{user, assistant}); err != nil {
		lease.Release()
		t.Fatal(err)
	}
	if err := SaveSessionRun(sessionDir, SessionRun{ID: "fork-run", SessionID: "event-fork-source", Status: "completed", StartedAt: now, UpdatedAt: now.Add(3 * time.Second), FinishedAt: timePtr(now.Add(3 * time.Second))}); err != nil {
		lease.Release()
		t.Fatal(err)
	}
	lease.Release()
	result, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "event-fork-source", RequestID: "event-fork-request"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := OpenByIDExact(sessionDir, result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	messages := child.GetMessages()
	if len(messages) != 2 || messages[0].Content != "fork request" || len(messages[1].Contents) != 1 || messages[1].Contents[0].Text != "fork answer" {
		t.Fatalf("forked EventStore messages = %#v", messages)
	}
	title, source, err := LatestSessionTitle(sessionDir, result.SessionID)
	if err != nil || title != "Session (1)" || source != "auto" {
		t.Fatalf("forked EventStore title = (%q, %q, %v)", title, source, err)
	}
	second, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "event-fork-source", RequestID: "event-fork-request-2"})
	if err != nil {
		t.Fatal(err)
	}
	title, _, err = LatestSessionTitle(sessionDir, second.SessionID)
	if err != nil || title != "Session (2)" {
		t.Fatalf("second EventStore fork title = (%q, %v), want Session (2)", title, err)
	}
}

func TestMigrateLegacySessionToEventStore(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-migrate", sessionDir)
	if err := manager.InitWithID("event-migrate-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("legacy message")); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(manager.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	if messages := reopened.GetMessages(); len(messages) != 1 || messages[0].Content != "legacy message" {
		t.Fatalf("migrated replay = %#v", messages)
	}
	inspection, err := InspectEventStore(context.Background(), sessionDir, "event-migrate-session")
	if err != nil || inspection.State != EventStoreHealthy {
		t.Fatalf("migrated inspection = %#v, err=%v", inspection, err)
	}
}

func TestMigrateEmptySessionRetainsEventStoreModeAfterReload(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-empty", sessionDir)
	if err := manager.InitWithID("event-empty-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(manager.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "event-empty-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.AppendMessage(provider.NewUserMessage("first EventStore message")); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var legacy int
	if err := database.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ? AND type <> 'session'", "event-empty-session").Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != 0 {
		t.Fatalf("empty migrated session wrote %d legacy entries", legacy)
	}
}

func TestEventStoreConversationTurnBoundariesStayInEventStore(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-turn", sessionDir)
	if err := manager.InitWithID("event-turn-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "event-turn-session")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.StartConversationTurn("turn-event-store", "intent-event-store", ""); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("turn input")); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if err := manager.EndConversationTurn("turn-event-store", "completed", "end_turn"); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()
	turns, err := ListConversationTurns(sessionDir, "event-turn-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Status != "completed" || turns[0].EndSeq == nil {
		t.Fatalf("EventStore conversation turns = %#v", turns)
	}
	legacyDB, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var legacyPayloads int
	if err := legacyDB.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ? AND type <> 'session'", "event-turn-session").Scan(&legacyPayloads); err != nil {
		t.Fatal(err)
	}
	if legacyPayloads != 0 {
		t.Fatalf("SQLite legacy transcript rows = %d, want 0", legacyPayloads)
	}
}

func TestOpenedEventStoreSessionUsesHandleDirectoryForTurnWrites(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-turn-handle", sessionDir)
	if err := manager.InitWithID("event-turn-handle-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(manager.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	if reopened.GetSessionDir() != sessionDir {
		t.Fatalf("reopened EventStore session dir = %q, want %q", reopened.GetSessionDir(), sessionDir)
	}
	guard, err := AcquireMutation(sessionDir, "event-turn-handle-session")
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.StartConversationTurn("event-turn-handle", "intent", ""); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if err := reopened.EndConversationTurn("event-turn-handle", "completed", "stop"); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()
	turns, err := ListConversationTurns(sessionDir, "event-turn-handle-session")
	if err != nil || len(turns) != 1 || turns[0].Status != "completed" {
		t.Fatalf("handle-open EventStore turns = %#v, err=%v", turns, err)
	}
}

func TestEventStoreRunTerminalPersistsAssistantAndTurnBoundary(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-terminal", sessionDir)
	if err := manager.InitWithID("event-terminal-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireExecutionAdmission(sessionDir, "event-terminal-session")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := CreateSessionRunAndEventWithTurn(sessionDir, SessionRun{ID: "event-terminal-run", SessionID: "event-terminal-session", Status: "running", StartedAt: now, UpdatedAt: now}, SessionRunEvent{ID: "event-terminal-start", SessionID: "event-terminal-session", RunID: "event-terminal-run", EventType: "started", Timestamp: now}, ConversationTurn{ID: "event-terminal-turn", SessionID: "event-terminal-session", IntentID: "event-terminal-intent", RunID: "event-terminal-run", StartedAt: now}); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	assistant := provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "terminal answer"}})
	finishedAt := now.Add(time.Second)
	assistant.Timestamp = finishedAt
	run := SessionRun{ID: "event-terminal-run", SessionID: "event-terminal-session", Status: "completed", StartedAt: now, UpdatedAt: finishedAt, FinishedAt: &finishedAt, AssistantEntryID: RunAssistantEntryID("event-terminal-run"), AssistantMessage: &assistant}
	event := SessionRunEvent{ID: RunTerminalEventID(run.ID, "finished"), SessionID: run.SessionID, RunID: run.ID, EventType: "finished", Status: "completed", Timestamp: finishedAt}
	if _, err := FinishSessionRunAndConversationTurn(sessionDir, run, event, "event-terminal-turn", "completed", "end_turn"); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(manager.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	messages := reopened.GetMessages()
	if len(messages) != 1 || len(messages[0].Contents) != 1 || messages[0].Contents[0].Text != "terminal answer" {
		t.Fatalf("terminal EventStore replay = %#v", messages)
	}
	turns, err := ListConversationTurns(sessionDir, run.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Status != "completed" || turns[0].EndSeq == nil {
		t.Fatalf("terminal turn = %#v", turns)
	}
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var legacyMessages int
	if err := database.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ? AND type = 'message'", run.SessionID).Scan(&legacyMessages); err != nil {
		t.Fatal(err)
	}
	if legacyMessages != 0 {
		t.Fatalf("admission payload rows left in SQLite = %d, want 0", legacyMessages)
	}
	var headerRows int
	if err := database.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ? AND type = 'session'", run.SessionID).Scan(&headerRows); err != nil {
		t.Fatal(err)
	}
	if headerRows != 1 {
		t.Fatalf("session header rows = %d, want 1", headerRows)
	}
}

func TestEventStoreRunAdmissionMovesUserEntryOutOfSQLite(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-admission", sessionDir)
	if err := manager.InitWithID("event-admission-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireExecutionAdmission(sessionDir, "event-admission-session")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	userMessage := provider.NewUserMessage("admitted user")
	userMessage.Timestamp = now
	run := SessionRun{ID: "event-admission-run", SessionID: "event-admission-session", Status: "running", StartedAt: now, UpdatedAt: now, UserEntryID: RunUserEntryID("event-admission-run"), UserMessage: &userMessage}
	if _, err := CreateSessionRunAndEventWithTurn(sessionDir, run, SessionRunEvent{ID: "event-admission-start", EventType: "started", Timestamp: now}, ConversationTurn{ID: "event-admission-turn", SessionID: run.SessionID, IntentID: "event-admission-intent", RunID: run.ID, StartedAt: now}); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()
	reopened, err := Open(manager.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	if messages := reopened.GetMessages(); len(messages) != 1 || messages[0].Content != "admitted user" {
		t.Fatalf("admission EventStore replay = %#v", messages)
	}
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var legacyPayloads int
	if err := database.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ? AND type <> 'session'", run.SessionID).Scan(&legacyPayloads); err != nil {
		t.Fatal(err)
	}
	if legacyPayloads != 0 {
		t.Fatalf("admission legacy payload rows = %d, want 0", legacyPayloads)
	}
}

func TestEventStoreMessagePagingUsesCanonicalEvents(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-paging", sessionDir)
	if err := manager.InitWithID("event-paging-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "event-paging-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessages([]provider.Message{provider.NewUserMessage("one"), provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "two"}}), provider.NewUserMessage("three")}); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()
	all, err := ListSessionMessagesWithSeq(sessionDir, "event-paging-session")
	if err != nil || len(all) != 3 {
		t.Fatalf("EventStore sequenced messages = %#v, err=%v", all, err)
	}
	after, err := ListSessionMessagesAfter(sessionDir, "event-paging-session", all[0].Seq, 10)
	if err != nil || len(after) != 2 || after[0].Message.Contents[0].Text != "two" {
		t.Fatalf("EventStore messages after = %#v, err=%v", after, err)
	}
	latest, err := ListSessionMessagesLatest(sessionDir, "event-paging-session", 2)
	if err != nil || len(latest) != 2 || latest[1].Message.Content != "three" {
		t.Fatalf("EventStore latest messages = %#v, err=%v", latest, err)
	}
	before, err := ListSessionMessagesBefore(sessionDir, "event-paging-session", all[2].Seq, 5)
	if err != nil || len(before) != 2 || before[0].Message.Content != "one" {
		t.Fatalf("EventStore messages before = %#v, err=%v", before, err)
	}
}

func TestEventStoreLargeBatchSplitsIntoBoundedBatches(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-bounded", sessionDir)
	if err := manager.InitWithID("event-bounded-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "event-bounded-session")
	if err != nil {
		t.Fatal(err)
	}
	total := maxEventStoreEntriesPerBatch*2 + 3
	entries := make([]interface{}, total)
	var parent *string
	for i := range entries {
		id := fmt.Sprintf("bounded-%03d", i)
		entries[i] = MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: id, ParentID: parent, Timestamp: time.Now().UTC()}, Message: provider.NewUserMessage(id)}
		parentID := id
		parent = &parentID
	}
	ids, err := manager.AppendEventStoreEntries(context.Background(), "", "", entries)
	guard.Release()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != total {
		t.Fatalf("bounded append IDs = %d, want %d", len(ids), total)
	}
	batches, err := ReadCommittedEventBatches(context.Background(), sessionDir, "event-bounded-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 3 || len(batches[0].Events) != maxEventStoreEntriesPerBatch || len(batches[1].Events) != maxEventStoreEntriesPerBatch || len(batches[2].Events) != 3 {
		t.Fatalf("bounded batches = %#v", batches)
	}
}

func TestLatestSessionTitleReadsEventStore(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-title", sessionDir)
	if err := manager.InitWithID("event-title-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "event-title-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendSessionTitle("EventStore title", "auto"); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()
	name, source, err := LatestSessionTitle(sessionDir, "event-title-session")
	if err != nil || name != "EventStore title" || source != "auto" {
		t.Fatalf("EventStore latest title = (%q, %q, %v)", name, source, err)
	}
}

func TestEventStoreSessionMetadataAndCompactionDoNotWriteLegacyEntries(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-metadata", sessionDir)
	if err := manager.InitWithID("event-metadata-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "event-metadata-session")
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := manager.AppendMessage(provider.NewUserMessage("original"))
	if err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := manager.AppendModelChange("provider", "model"); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := manager.AppendModeChange("yolo"); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := manager.AppendThinkingLevelChange("high"); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := manager.AppendAdditionalDirectories([]string{"/tmp/extra"}); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := manager.AppendContentOverride(messageID, provider.NewUserMessage("replacement"), "test", ""); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := manager.AppendCompaction("summary", "", 42); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var legacy int
	if err := database.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ? AND type <> 'session'", "event-metadata-session").Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != 0 {
		t.Fatalf("metadata event session wrote %d legacy entries", legacy)
	}
	if messages := manager.GetMessages(); len(messages) != 1 || messages[0].Content != "summary" {
		t.Fatalf("EventStore compaction replay = %#v", messages)
	}
}

func TestSubAgentSessionCannotUseRootEventStore(t *testing.T) {
	manager := NewSubAgent("/tmp/sub-agent-event", t.TempDir())
	if err := manager.InitWithID("sub-agent-event-store"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err == nil || !strings.Contains(err.Error(), "sub_entries") {
		t.Fatalf("sub-agent migration error = %v, want sub_entries compatibility error", err)
	}
	if _, err := manager.AppendEventStoreEntries(context.Background(), "", "", []interface{}{MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "sub-event"}, Message: provider.NewUserMessage("no")}}); err == nil || !strings.Contains(err.Error(), "sub-agent") {
		t.Fatalf("sub-agent EventStore append error = %v, want rejection", err)
	}
}

// legacyTranscriptPayloadCount counts SQLite transcript rows that still carry a
// payload for one session. The header row is written by the session-registration
// path and is not part of the transcript copy removed by the EventStore
// migration, so it is excluded exactly like DAO DeleteLegacyEntries does.
func legacyTranscriptPayloadCount(t *testing.T, sessionDir, sessionID string) int {
	t.Helper()
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.Bun().QueryRow(`SELECT COUNT(*) FROM entries WHERE session_id = ? AND type <> 'session'`, sessionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// eventProjectionCount reports how many rebuildable SQLite projection rows one
// session currently has. Projections are derived state: they may be wiped and
// regenerated from the committed segments at any time.
func eventProjectionCount(sessionDir, sessionID string) (int, error) {
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		return 0, err
	}
	var count int
	if err := database.Bun().QueryRow(`SELECT COUNT(*) FROM session_event_projections WHERE session_id = ?`, sessionID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// TestEventStoreArtifactReplayNeedsNoLegacyTranscriptCopy proves the acceptance
// criterion that attachment/artifact replay is rebuilt purely from committed
// events plus the Runtime-owned attachment store: the assistant message keeps
// only the generated attachment reference, the migrated session keeps no SQLite
// transcript payload at all, and reopening the session reconstructs both the
// artifact metadata and the provider content blocks from the segments.
func TestEventStoreArtifactReplayNeedsNoLegacyTranscriptCopy(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-artifacts", sessionDir)
	if err := manager.InitWithID("event-artifact-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := legacyTranscriptPayloadCount(t, sessionDir, "event-artifact-session"); got != 0 {
		t.Fatalf("legacy SQLite transcript payloads after enabling the EventStore = %d, want 0", got)
	}
	guard, err := AcquireMutation(sessionDir, "event-artifact-session")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()

	now := time.Now().UTC()
	user := MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: "artifact-user", Timestamp: now},
		Message: provider.Message{
			Role:      "user",
			Content:   "publish the report",
			Timestamp: now,
			Contents: []provider.ContentBlock{{
				Type: "file",
				File: &provider.FileContent{ID: "att-input", Filename: "notes.txt", MimeType: "text/plain"},
			}},
		},
	}
	assistant := MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: "artifact-assistant", ParentID: stringPtr(user.ID), Timestamp: now.Add(time.Second)},
		Message: provider.Message{
			Role:      "assistant",
			Timestamp: now.Add(time.Second),
			Contents: []provider.ContentBlock{
				{Type: "text", Text: "report published"},
				{Type: "image", Image: &provider.ImageContent{Data: "aW1hZ2U=", MimeType: "image/png", Bytes: 5}},
			},
			Attachments: []provider.Attachment{{
				Kind:      "artifact",
				Name:      "report.txt",
				MediaType: "text/plain",
				Metadata:  map[string]any{"attachmentId": "att-report", "bytes": float64(16)},
			}},
		},
	}
	if _, err := manager.AppendEventStoreEntries(context.Background(), "run-artifact", "turn-artifact", []interface{}{user, assistant}); err != nil {
		t.Fatal(err)
	}
	if got := legacyTranscriptPayloadCount(t, sessionDir, "event-artifact-session"); got != 0 {
		t.Fatalf("legacy SQLite transcript payloads after EventStore append = %d, want 0", got)
	}
	guard.Release()

	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(manager.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	messages := reopened.GetMessages()
	if len(messages) != 2 {
		t.Fatalf("replayed messages = %#v", messages)
	}
	if len(messages[0].Contents) != 1 || messages[0].Contents[0].File == nil ||
		messages[0].Contents[0].File.ID != "att-input" || messages[0].Contents[0].File.Filename != "notes.txt" {
		t.Fatalf("replayed input attachment block = %#v", messages[0].Contents)
	}
	if len(messages[1].Contents) != 2 || messages[1].Contents[0].Text != "report published" {
		t.Fatalf("replayed assistant blocks = %#v", messages[1].Contents)
	}
	image := messages[1].Contents[1].Image
	if image == nil || image.MimeType != "image/png" || image.Data != "aW1hZ2U=" || image.Bytes != 5 {
		t.Fatalf("replayed inline image block = %#v, want the base64 payload preserved", image)
	}
	if len(messages[1].Attachments) != 1 {
		t.Fatalf("replayed generated artifacts = %#v", messages[1].Attachments)
	}
	artifact := messages[1].Attachments[0]
	if artifact.Kind != "artifact" || artifact.Name != "report.txt" || artifact.MediaType != "text/plain" ||
		artifact.Metadata["attachmentId"] != "att-report" || artifact.Metadata["bytes"] != float64(16) {
		t.Fatalf("replayed generated artifact = %#v", artifact)
	}
	inspection, err := InspectEventStore(context.Background(), sessionDir, "event-artifact-session")
	if err != nil || inspection.State != EventStoreHealthy {
		t.Fatalf("artifact session inspection = %#v, err=%v", inspection, err)
	}
}

// TestEventStoreDecisionReplayNeedsNoTranscriptCopy proves the acceptance
// criterion for decision replay on both fork paths. The decision ledger lives in
// the control-plane run events, so it survives a full SQLite projection rebuild;
// and the EventStore fork branch must apply the same pending-decision gate as
// the legacy branch instead of copying a transcript that still waits for an
// approval. Neither check reads the transcript payload table.
func TestEventStoreDecisionReplayNeedsNoTranscriptCopy(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-decisions", sessionDir)
	if err := manager.InitWithID("event-decision-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	admission, err := AcquireExecutionAdmission(sessionDir, "event-decision-session")
	if err != nil {
		t.Fatal(err)
	}
	defer admission.Release()
	now := time.Now().UTC()
	if _, err := CreateSessionRunAndEventWithTurn(sessionDir, SessionRun{
		ID: "run-decision", SessionID: "event-decision-session", Status: "running",
		Source: "test", StartedAt: now, UpdatedAt: now,
	}, SessionRunEvent{
		SessionID: "event-decision-session", RunID: "run-decision",
		EventType: "started", Source: "test", Timestamp: now,
	}, ConversationTurn{ID: "turn-decision", SessionID: "event-decision-session", IntentID: "intent-decision", RunID: "run-decision", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	user := MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: "decision-user", Timestamp: now},
		Message:   provider.NewUserMessage("delete the branch?"),
	}
	if _, err := manager.AppendEventStoreEntries(context.Background(), "run-decision", "turn-decision", []interface{}{user}); err != nil {
		t.Fatal(err)
	}

	request := eventDecision{ID: "approval-1", RunID: "run-decision", SessionID: "event-decision-session", Kind: "approval"}
	deadline := now.Add(time.Minute)
	recordDecision := func(status, value string, expiresAt *time.Time, at time.Time) {
		t.Helper()
		data, err := request.envelope(status, value, expiresAt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := SaveSessionRunEvent(sessionDir, SessionRunEvent{
			SessionID: request.SessionID, RunID: request.RunID,
			EventType: DecisionEventType(status), Source: "test", Status: status,
			Timestamp: at, Data: data,
		}); err != nil {
			t.Fatal(err)
		}
	}
	recordDecision("pending", "", &deadline, now.Add(time.Second))
	if err := UpdateSessionRunStatus(sessionDir, "run-decision", "waiting_for_approval", "", nil); err != nil {
		t.Fatal(err)
	}
	assistant := MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: "decision-assistant", ParentID: &user.ID, Timestamp: now.Add(time.Second)},
		Message: provider.NewAssistantMessage([]provider.ContentBlock{{
			Type: "toolCall",
			ToolCall: &provider.ToolCallBlock{
				ID: "call-1", Name: "shell", Arguments: json.RawMessage(`{"command":"git branch -D x"}`),
			},
		}}),
	}
	if _, err := manager.AppendEventStoreEntries(context.Background(), "run-decision", "turn-decision", []interface{}{assistant}); err != nil {
		t.Fatal(err)
	}
	admission.Release()

	// A pending decision must block a fork through the EventStore branch, not
	// only through the legacy SQLite branch.
	if _, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: request.SessionID, RequestID: "pending-blocks-fork"}); !errors.Is(err, ErrForkSessionActive) {
		t.Fatalf("pending decision fork error = %v, want active", err)
	}

	ledgerBefore, err := readEventDecisionLedger(sessionDir, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	projectionsBefore, err := eventProjectionCount(sessionDir, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledgerBefore) != 1 || projectionsBefore != 3 {
		t.Fatalf("pre-rebuild state = (%d decisions, %d projections), want (1, 3)", len(ledgerBefore), projectionsBefore)
	}

	// Wipe the rebuildable projection and regenerate it from the committed
	// segments only. The decision ledger is control-plane truth and must be
	// untouched, and no legacy transcript payload may reappear.
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionEventProjectionDAO(nil).DeleteSession(context.Background(), tx, request.SessionID)
	}); err != nil {
		t.Fatal(err)
	}
	// A projection rebuild needs a lease that tolerates the still-active Run;
	// this is the recovery owner, not an idle-session mutation.
	recovery, err := AcquireRecovery(sessionDir, request.SessionID, "run-decision")
	if err != nil {
		t.Fatal(err)
	}
	recoveryReleased := false
	releaseRecovery := func() {
		if !recoveryReleased {
			recoveryReleased = true
			recovery.Release()
		}
	}
	t.Cleanup(releaseRecovery)
	if err := RebuildEventProjection(context.Background(), sessionDir, request.SessionID, recovery); err != nil {
		t.Fatal(err)
	}

	ledgerAfter, err := readEventDecisionLedger(sessionDir, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledgerAfter) != len(ledgerBefore) {
		t.Fatalf("decision ledger shrank through rebuild: before=%d after=%d", len(ledgerBefore), len(ledgerAfter))
	}
	for i := range ledgerBefore {
		if ledgerBefore[i] != ledgerAfter[i] {
			t.Fatalf("decision ledger diverged at %d: before=%#v after=%#v", i, ledgerBefore[i], ledgerAfter[i])
		}
	}
	if got, err := eventProjectionCount(sessionDir, request.SessionID); err != nil {
		t.Fatal(err)
	} else if got != projectionsBefore {
		t.Fatalf("projections after rebuild = %d, want %d", got, projectionsBefore)
	}
	if got := legacyTranscriptPayloadCount(t, sessionDir, request.SessionID); got != 0 {
		t.Fatalf("rebuild resurrected %d legacy transcript payloads, want 0", got)
	}
	reloaded, err := OpenByIDExact(sessionDir, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	// The assistant turn carries an emitted tool call, so rebuilding the
	// projection must restore the side-effect intent rather than a text blob.
	if messages := reloaded.GetMessages(); len(messages) != 2 ||
		messages[0].Content != "delete the branch?" ||
		len(messages[1].Contents) != 1 || messages[1].Contents[0].Type != "toolCall" ||
		messages[1].Contents[0].ToolCall == nil ||
		messages[1].Contents[0].ToolCall.ID != "call-1" ||
		messages[1].Contents[0].ToolCall.Name != "shell" {
		t.Fatalf("transcript after projection rebuild = %#v", messages)
	}
	if replayed := replayEventDecisions(ledgerAfter, now.Add(30*time.Second)); len(replayed) != 1 {
		t.Fatalf("pending decision was lost through rebuild: %#v", replayed)
	}
	if _, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: request.SessionID, RequestID: "pending-still-blocks"}); !errors.Is(err, ErrForkSessionActive) {
		t.Fatalf("fork after rebuild error = %v, want active", err)
	}

	// Resolving the decision terminalizes the run and must not revive afterwards.
	recordDecision("resolved", "allow", nil, now.Add(2*time.Second))
	finishedAt := now.Add(2 * time.Second)
	if _, err := FinishSessionRunAndConversationTurn(sessionDir, SessionRun{
		ID: "run-decision", SessionID: request.SessionID, Status: "completed",
		StartedAt: now, UpdatedAt: finishedAt, FinishedAt: &finishedAt,
	}, SessionRunEvent{
		SessionID: request.SessionID, RunID: "run-decision",
		EventType: "finished", Status: "completed", Timestamp: finishedAt,
	}, "turn-decision", "completed", "end_turn"); err != nil {
		t.Fatal(err)
	}
	releaseRecovery()
	ledgerResolved, err := readEventDecisionLedger(sessionDir, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed := replayEventDecisions(ledgerResolved, now.Add(30*time.Second)); len(replayed) != 0 {
		t.Fatalf("resolved decision was revived: %#v", replayed)
	}
	child, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: request.SessionID, RequestID: "resolved-allows-fork"})
	if err != nil {
		t.Fatalf("fork after resolved decision replay: %v", err)
	}
	childManager, err := OpenByIDExact(sessionDir, child.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if messages := childManager.GetMessages(); len(messages) != 2 || messages[0].Content != "delete the branch?" {
		t.Fatalf("forked EventStore messages = %#v", messages)
	}
	if contents := childManager.GetMessages()[1].Contents; len(contents) != 1 ||
		contents[0].Type != "toolCall" || contents[0].ToolCall == nil ||
		contents[0].ToolCall.ID != "call-1" || contents[0].ToolCall.Name != "shell" {
		t.Fatalf("forked session lost the emitted tool call: %#v", contents)
	}
	// Forking must not clone the source decision ledger: the child starts with no
	// pending approval of its own.
	childLedger, err := readEventDecisionLedger(sessionDir, child.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(childLedger) != 0 {
		t.Fatalf("forked session inherited %d decision rows, want 0", len(childLedger))
	}
	if got := legacyTranscriptPayloadCount(t, sessionDir, child.SessionID); got != 0 {
		t.Fatalf("forked session carries %d legacy transcript payloads, want 0", got)
	}
}

func timePtr(value time.Time) *time.Time { return &value }

// TestEventStoreForkBlockedByIdlePendingDecision pins the parity gate that
// AcquireFork alone cannot provide: a pending approval whose Run row is already
// terminal (a restarted process, or an expired lease reconciled by recovery)
// must still refuse an EventStore fork, exactly as the legacy transcript branch
// does. Without the explicit ActiveRunCount/OpenTurnCount/pendingDecisionsTx
// gates in forkEventStoreSession this fork would succeed and clone a session
// mid-approval.
func TestEventStoreForkBlockedByIdlePendingDecision(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/event-idle-decision", sessionDir)
	if err := manager.InitWithID("idle-decision-session"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	admission, err := AcquireExecutionAdmission(sessionDir, "idle-decision-session")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := CreateSessionRunAndEventWithTurn(sessionDir, SessionRun{
		ID: "run-idle", SessionID: "idle-decision-session", Status: "running",
		Source: "test", StartedAt: now, UpdatedAt: now,
	}, SessionRunEvent{
		SessionID: "idle-decision-session", RunID: "run-idle",
		EventType: "started", Source: "test", Timestamp: now,
	}, ConversationTurn{ID: "turn-idle", SessionID: "idle-decision-session", IntentID: "intent-idle", RunID: "run-idle", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	user := MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: "idle-user", Timestamp: now},
		Message:   provider.NewUserMessage("delete the branch?"),
	}
	if _, err := manager.AppendEventStoreEntries(context.Background(), "run-idle", "turn-idle", []interface{}{user}); err != nil {
		t.Fatal(err)
	}
	// The decision ledger lives on the control plane only, never in the
	// EventStore, so the fork gate has to read session_run_events directly.
	request := eventDecision{ID: "approval-idle", RunID: "run-idle", SessionID: "idle-decision-session", Kind: "approval"}
	deadline := now.Add(time.Hour)
	data, err := request.envelope("pending", "", &deadline)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SaveSessionRunEvent(sessionDir, SessionRunEvent{
		SessionID: request.SessionID, RunID: request.RunID,
		EventType: DecisionEventType("pending"), Source: "test", Status: "pending",
		Timestamp: now.Add(time.Second), Data: data,
	}); err != nil {
		t.Fatal(err)
	}
	// The Run goes idle while the decision stays pending: this is the restarted
	// process / reconciled lease state that AcquireFork alone cannot detect.
	if err := UpdateSessionRunStatus(sessionDir, "run-idle", "waiting_for_approval", "", nil); err != nil {
		t.Fatal(err)
	}
	admission.Release()

	// Terminalize the Run and close the turn, then drop every legacy payload, so
	// the only thing left that can block the fork is the pending decision.
	closedAt := now.Add(2 * time.Second)
	// The Run is still non-terminal, so only the recovery owner may drive it to
	// its terminal state while the decision remains pending.
	recovery, err := AcquireRecovery(sessionDir, request.SessionID, "run-idle")
	if err != nil {
		t.Fatal(err)
	}
	recoveryReleased := false
	releaseRecovery := func() {
		if !recoveryReleased {
			recoveryReleased = true
			recovery.Release()
		}
	}
	t.Cleanup(releaseRecovery)
	if _, err := FinishSessionRunAndConversationTurn(sessionDir, SessionRun{
		ID: "run-idle", SessionID: request.SessionID, Status: "completed",
		StartedAt: now, UpdatedAt: closedAt, FinishedAt: &closedAt,
	}, SessionRunEvent{
		SessionID: request.SessionID, RunID: "run-idle",
		EventType: "finished", Status: "completed", Timestamp: closedAt,
	}, "turn-idle", "completed", "end_turn"); err != nil {
		t.Fatal(err)
	}
	releaseRecovery()
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionEventProjectionDAO(nil).DeleteLegacyEntries(context.Background(), tx, request.SessionID)
	}); err != nil {
		t.Fatal(err)
	}

	mutation, err := AcquireMutation(sessionDir, request.SessionID)
	if err != nil {
		t.Fatalf("session with a terminal Run should admit a mutation lease: %v", err)
	}
	mutation.Release()

	if _, err := ForkSession(context.Background(), sessionDir, ForkOptions{
		SourceSessionID: request.SessionID, RequestID: "idle-pending-blocks-fork",
	}); !errors.Is(err, ErrForkSessionActive) {
		t.Fatalf("idle pending decision fork error = %v, want ErrForkSessionActive", err)
	}

	// Resolving the decision releases the gate; the fork then succeeds and the
	// child inherits the transcript without any pending approval of its own.
	resolved, err := request.envelope("resolved", "allow", nil)
	if err != nil {
		t.Fatal(err)
	}
	resolveLease, err := AcquireMutation(sessionDir, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SaveSessionRunEvent(sessionDir, SessionRunEvent{
		SessionID: request.SessionID, RunID: request.RunID,
		EventType: DecisionEventType("resolved"), Source: "test", Status: "resolved",
		Timestamp: closedAt.Add(time.Second), Data: resolved,
	}); err != nil {
		resolveLease.Release()
		t.Fatal(err)
	}
	resolveLease.Release()
	child, err := ForkSession(context.Background(), sessionDir, ForkOptions{
		SourceSessionID: request.SessionID, RequestID: "idle-resolved-allows-fork",
	})
	if err != nil {
		t.Fatalf("fork after resolving the idle decision: %v", err)
	}
	childLedger, err := readEventDecisionLedger(sessionDir, child.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(childLedger) != 0 {
		t.Fatalf("forked session inherited %d decision rows, want 0", len(childLedger))
	}
	if got := legacyTranscriptPayloadCount(t, sessionDir, child.SessionID); got != 0 {
		t.Fatalf("forked session carries %d legacy transcript payloads, want 0", got)
	}
	childManager, err := OpenByIDExact(sessionDir, child.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if messages := childManager.GetMessages(); len(messages) != 1 || messages[0].Content != "delete the branch?" {
		t.Fatalf("forked transcript = %#v", messages)
	}
}

// eventDecision mirrors the Runtime decision envelope (agentruntime.DecisionRecord
// inside a {"decision": ..., "payload": ...} wrapper). The session package cannot
// import agentruntime, so this is the shape cross-checked by the Runtime-side
// replay test; drifting field names break both directions loudly.
type eventDecision struct {
	ID        string
	RunID     string
	SessionID string
	Kind      string
}

func (d eventDecision) envelope(status, value string, expiresAt *time.Time) ([]byte, error) {
	record := map[string]any{
		"id": d.ID, "sessionId": d.SessionID, "runId": d.RunID,
		"kind": d.Kind, "status": status,
	}
	if value != "" {
		record["value"] = value
	}
	if expiresAt != nil && !expiresAt.IsZero() {
		record["expiresAt"] = *expiresAt
	}
	return json.Marshal(map[string]any{"decision": record, "payload": map[string]any{"command": "git branch -D x"}})
}

type eventDecisionRecord struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Value     string    `json:"value"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func readEventDecisionLedger(sessionDir, sessionID string) ([]eventDecisionRecord, error) {
	events, err := ListSessionRunEventsContext(context.Background(), sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	records := make([]eventDecisionRecord, 0, len(events))
	for _, ev := range events {
		if !IsDecisionEventType(ev.EventType) {
			continue
		}
		var envelope struct {
			Decision eventDecisionRecord `json:"decision"`
		}
		if err := json.Unmarshal(ev.Data, &envelope); err != nil {
			return nil, err
		}
		if envelope.Decision.ID == "" {
			continue
		}
		records = append(records, envelope.Decision)
	}
	return records, nil
}

// replayEventDecisions repeats the Runtime replay rule locally so the session
// side of the ledger stays self-describing after a projection rebuild.
func replayEventDecisions(records []eventDecisionRecord, now time.Time) map[string]eventDecisionRecord {
	pending := make(map[string]eventDecisionRecord)
	for _, record := range records {
		switch record.Status {
		case "pending", "requested":
			if !record.ExpiresAt.IsZero() && !now.Before(record.ExpiresAt) {
				delete(pending, record.ID)
				continue
			}
			pending[record.ID] = record
		default:
			delete(pending, record.ID)
		}
	}
	return pending
}

// TestMessageCursorParityAcrossContentStores pins the cross-entry replay-cursor
// contract. A legacy SQLite session and an EventStore session holding the same
// transcript must behave identically, so TUI, CLI, WebUI/API, ACP, and Channel
// can share one paging implementation.
//
// The invariant is relational, not literal: legacy `entries.seq` is a
// database-global AUTOINCREMENT shared by every session, so two stores can
// never promise the same integers. What both must guarantee is that a cursor is
// always positive (so it never collides with the "no cursor yet" zero sentinel),
// strictly increasing in transcript order, and selects the same window relative
// to its own transcript.
func TestMessageCursorParityAcrossContentStores(t *testing.T) {
	transcript := []provider.Message{
		provider.NewUserMessage("one"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "two"}}),
		provider.NewUserMessage("three"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "four"}}),
	}
	wantTexts := []string{"one", "two", "three", "four"}

	legacyDir := t.TempDir()
	legacy := New("/tmp/cursor-parity-legacy", legacyDir)
	if err := legacy.InitWithID("cursor-parity-legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendMessages(transcript); err != nil {
		t.Fatal(err)
	}

	storeDir := t.TempDir()
	stored := New("/tmp/cursor-parity-store", storeDir)
	if err := stored.InitWithID("cursor-parity-store"); err != nil {
		t.Fatal(err)
	}
	if err := stored.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(storeDir, "cursor-parity-store")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stored.AppendMessages(transcript); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()

	type session struct {
		label string
		dir   string
		id    string
	}
	sessions := []session{
		{"legacy", legacyDir, "cursor-parity-legacy"},
		{"event-store", storeDir, "cursor-parity-store"},
	}

	for _, s := range sessions {
		full, err := ListSessionMessagesWithSeq(s.dir, s.id)
		if err != nil {
			t.Fatal(err)
		}
		if len(full) != len(wantTexts) {
			t.Fatalf("%s: replay length = %d, want %d", s.label, len(full), len(wantTexts))
		}
		var texts []string
		for i, item := range full {
			texts = append(texts, sequencedMessageText(item.Message))
			// A non-positive cursor is indistinguishable from "no cursor yet",
			// which silently re-delivers or drops the first message.
			if item.Seq <= 0 {
				t.Fatalf("%s: cursor[%d] = %d, want > 0", s.label, i, item.Seq)
			}
			if i > 0 && item.Seq <= full[i-1].Seq {
				t.Fatalf("%s: cursors are not increasing at [%d]: %d after %d", s.label, i, item.Seq, full[i-1].Seq)
			}
		}
		if !equalStringSlices(texts, wantTexts) {
			t.Fatalf("%s: replay = %v, want %v", s.label, texts, wantTexts)
		}

		// Every cursor position must select the same relative window on both
		// stores. Cursors are per-store, so compare against the expected text
		// suffix/prefix rather than across stores.
		for i, cursor := range full {
			after, err := ListSessionMessagesAfter(s.dir, s.id, cursor.Seq, 10)
			if err != nil {
				t.Fatal(err)
			}
			if got := sequencedTexts(after); !equalStringSlices(got, wantTexts[i+1:]) {
				t.Fatalf("%s: after cursor[%d]=%d -> %v, want %v", s.label, i, cursor.Seq, got, wantTexts[i+1:])
			}
			before, err := ListSessionMessagesBefore(s.dir, s.id, cursor.Seq, 10)
			if err != nil {
				t.Fatal(err)
			}
			if got := sequencedTexts(before); !equalStringSlices(got, wantTexts[:i]) {
				t.Fatalf("%s: before cursor[%d]=%d -> %v, want %v", s.label, i, cursor.Seq, got, wantTexts[:i])
			}
		}

		// The zero sentinel means "from the beginning" and must admit the first
		// message rather than being read as a real position.
		head, err := ListSessionMessagesAfter(s.dir, s.id, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if got := sequencedTexts(head); !equalStringSlices(got, wantTexts) {
			t.Fatalf("%s: zero cursor replay -> %v, want %v", s.label, got, wantTexts)
		}

		// A past-the-end cursor returns nothing and terminates a paged walk.
		last := full[len(full)-1].Seq
		tail, err := ListSessionMessagesAfter(s.dir, s.id, last+7, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(tail) != 0 {
			t.Fatalf("%s: past-the-end cursor returned %v", s.label, sequencedTexts(tail))
		}

		// A forward walk at a page size smaller than the transcript must visit
		// each message exactly once. This is what breaks when a store's first
		// cursor collides with the zero sentinel: the walk re-reads message 0
		// forever instead of advancing.
		var walked []string
		var cursor int64
		for pages := 0; ; pages++ {
			if pages > len(wantTexts)+2 {
				t.Fatalf("%s: paged walk did not terminate", s.label)
			}
			page, err := ListSessionMessagesAfter(s.dir, s.id, cursor, 3)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range page {
				walked = append(walked, sequencedMessageText(item.Message))
				if item.Seq > cursor {
					cursor = item.Seq
				}
			}
			if len(page) < 3 {
				break
			}
		}
		if !equalStringSlices(walked, wantTexts) {
			t.Fatalf("%s: paged walk = %v, want %v", s.label, walked, wantTexts)
		}

		latest, err := ListSessionMessagesLatest(s.dir, s.id, 2)
		if err != nil {
			t.Fatal(err)
		}
		if got := sequencedTexts(latest); !equalStringSlices(got, []string{"three", "four"}) {
			t.Fatalf("%s: latest 2 = %v, want [three four]", s.label, got)
		}
	}
}

func sequencedTexts(messages []SequencedMessage) []string {
	texts := make([]string, 0, len(messages))
	for _, item := range messages {
		texts = append(texts, sequencedMessageText(item.Message))
	}
	return texts
}

func sequencedMessageText(m provider.Message) string {
	if m.Content != "" {
		return m.Content
	}
	for _, block := range m.Contents {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMigrateSessionRemapsTurnBoundaries proves that adopting the EventStore
// retargets `conversation_turns` onto the new sequence space instead of leaving
// the bounds pointing at deleted legacy `entries.seq` rows.
//
// The turn markers are written by the standalone helpers straight to the
// database, so the export cannot rely on the Manager's in-memory view; and a
// dangling bound makes fork resolution silently pick the wrong boundary. The
// assertions therefore check both the remapped interval and that a fork taken
// after migration still resolves against it.
func TestMigrateSessionRemapsTurnBoundaries(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "remap-turns-session"
	manager := New("/tmp/event-remap", sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	if _, err := manager.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := StartConversationTurn(sessionDir, ConversationTurn{ID: "turn-1", SessionID: sessionID, IntentID: "intent-1", RunID: "run-1", Kind: "conversation", Status: "open", StartedAt: startedAt}); err != nil {
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

	before, err := ListConversationTurns(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 {
		t.Fatalf("turns before migration = %d, want 1", len(before))
	}

	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}

	after, err := ListConversationTurns(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("turns after migration = %d, want 1", len(after))
	}
	messages, err := ListSessionMessagesWithSeq(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages after migration = %d, want 2", len(messages))
	}
	// Exported order is: user message (1), turn_start (2), assistant (3),
	// turn_end (4). A bound that still names a legacy seq lands outside this
	// space entirely.
	if after[0].StartSeq != 2 || after[0].EndSeq == nil || *after[0].EndSeq != 4 {
		t.Fatalf("remapped bounds = %d..%v, want 2..4", after[0].StartSeq, derefSeq(after[0].EndSeq))
	}
	if after[0].Status != "completed" {
		t.Fatalf("remapped turn status = %q, want completed", after[0].Status)
	}

	// The whole point of the remap: fork resolution must still find the
	// completed turn in the new space.
	result, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: sessionID, RequestID: "remap-turns-fork"})
	if err != nil {
		t.Fatalf("fork after migration: %v", err)
	}
	if result.ForkKind != ForkKindSession || result.BoundarySeq != 4 {
		t.Fatalf("fork result = %#v, want session kind at boundary 4", result)
	}
	child, err := OpenByID("/tmp/event-remap", sessionDir, result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := child.GetMessages(); len(got) != 2 || got[0].Content != "hello" {
		t.Fatalf("forked child replay = %#v", got)
	}
	childTurns, err := ListConversationTurns(sessionDir, result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(childTurns) != 1 {
		t.Fatalf("forked child turns = %d, want 1", len(childTurns))
	}
	if childTurns[0].EndSeq == nil || childTurns[0].StartSeq >= *childTurns[0].EndSeq {
		t.Fatalf("forked child turn bounds = %d..%v, want a closed interval", childTurns[0].StartSeq, derefSeq(childTurns[0].EndSeq))
	}
}

func derefSeq(value *int64) int64 {
	if value == nil {
		return -1
	}
	return *value
}

// TestConvergeFlippedSessionRepairsPreRemapTurnBoundaries covers the population
// flipped by a build that predates the migration boundary remap: the markers were
// exported into the EventStore and the legacy payload rows were deleted, yet the
// `conversation_turns` bounds still name the old database sequences, so they
// address nothing at all.
//
// Convergence re-runs on every bind, so repairing there heals those sessions
// without a new schema version. The assertion is again behavioural: a fork taken
// after the repair must resolve at the turn's real end marker rather than drop
// the turn out of the child transcript.
func TestConvergeFlippedSessionRepairsPreRemapTurnBoundaries(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "preremap-turns-session"
	manager := New("/tmp/event-preremap", sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	if _, err := manager.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := StartConversationTurn(sessionDir, ConversationTurn{ID: "turn-1", SessionID: sessionID, IntentID: "intent-1", RunID: "run-1", Kind: "conversation", Status: "open", StartedAt: startedAt}); err != nil {
		guard.Release()
		t.Fatal(err)
	}
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

	// Read the legacy bounds while the payload rows still exist: those are the
	// numbers a pre-remap flip would have left behind next to the deleted rows.
	preDB, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	preTurns, err := dao.NewConversationTurnDAO(preDB.Bun()).List(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preTurns) != 1 || preTurns[0].EndSeq == nil {
		t.Fatalf("turns before migration = %#v, want one closed turn", preTurns)
	}
	staleStart, staleEnd := preTurns[0].StartSeq, *preTurns[0].EndSeq

	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}

	// Roll the control row back to the legacy numbers, exactly as a build without
	// the boundary remap left the population this repair exists for.
	rootDB, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rootDB.Bun().Exec("UPDATE conversation_turns SET start_seq = ?, end_seq = ? WHERE session_id = ? AND id = ?", staleStart, staleEnd, sessionID, "turn-1"); err != nil {
		t.Fatal(err)
	}
	markerSeqs, err := dao.NewSessionEventProjectionDAO(rootDB.Bun()).BoundarySeqsByTurn(context.Background(), rootDB.Bun(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(markerSeqs) != 2 {
		t.Fatalf("turn markers in projection = %v, want two", markerSeqs)
	}
	legacyBounds := []int64{markerSeqs[0].Seq, markerSeqs[1].Seq}

	// A fresh Manager binding the already-flipped session only converges; it never
	// re-exports, so this is precisely the path that must heal the bounds.
	reopened, err := OpenByID("/tmp/event-preremap", sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}

	turns, err := ListConversationTurns(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns after convergence = %d, want 1", len(turns))
	}
	if turns[0].StartSeq == staleStart || turns[0].EndSeq == nil || *turns[0].EndSeq == staleEnd {
		t.Fatalf("bounds still stale after convergence: %d..%v", turns[0].StartSeq, derefSeq(turns[0].EndSeq))
	}
	if turns[0].StartSeq != legacyBounds[0] || *turns[0].EndSeq != legacyBounds[1] {
		t.Fatalf("repaired bounds = %d..%v, want %d..%d", turns[0].StartSeq, derefSeq(turns[0].EndSeq), legacyBounds[0], legacyBounds[1])
	}
	if turns[0].Status != "completed" {
		t.Fatalf("repaired turn status = %q, want completed", turns[0].Status)
	}

	result, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: sessionID, RequestID: "preremap-turns-fork"})
	if err != nil {
		t.Fatalf("fork after repair: %v", err)
	}
	if result.ForkKind != ForkKindSession || result.BoundarySeq != legacyBounds[1] {
		t.Fatalf("fork result = %#v, want session kind at boundary %d", result, legacyBounds[1])
	}
	child, err := OpenByID("/tmp/event-preremap", sessionDir, result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := child.GetMessages(); len(got) != 2 || got[0].Content != "hello" {
		t.Fatalf("forked child replay = %#v", got)
	}
}

// TestAppendEventStoreEntriesAllowsExternalActiveRunWithoutLocalLease pins the
// availability contract that the content-write reservation exists to protect.
//
// A durable Run owned by another process (or by a path in this process that did
// not take the session lease) must not freeze this session's transcript: only a
// fenced takeover decides ownership, so wall-clock state and someone else's open
// Run are not evidence that this writer may not append. `AcquireMutation` would
// refuse with ErrSessionRunActive here and stall the turn, which is why the append
// path uses the short-lived content-write reservation instead. The reservation
// still serialises cross-process writers, so the assertion is that the append
// succeeds and lands exactly once rather than that locking vanished.
func TestAppendEventStoreEntriesAllowsExternalRunWithoutLocalLease(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "append-external-run"
	manager := New("/tmp/event-external-run", sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := SaveSessionRun(sessionDir, SessionRun{
		ID: "run-external", SessionID: sessionID, Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// The mutation guard the legacy path would use is genuinely refused, so the
	// append below exercising the reservation path proves it is a different rule.
	if _, err := AcquireMutation(sessionDir, sessionID); !errors.Is(err, ErrSessionRunActive) {
		t.Fatalf("AcquireMutation with an external active run = %v, want ErrSessionRunActive", err)
	}

	entry := MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "append-external-msg", Timestamp: now.UTC()}, Message: provider.NewUserMessage("hello")}
	ids, err := manager.AppendEventStoreEntries(context.Background(), "", "", []interface{}{entry})
	if err != nil {
		t.Fatalf("append while another Run is active: %v", err)
	}
	if len(ids) != 1 || ids[0] != "append-external-msg" {
		t.Fatalf("appended IDs = %v, want [append-external-msg]", ids)
	}
	inspection, err := InspectEventStore(context.Background(), sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != EventStoreHealthy || len(inspection.Batches) != 1 {
		t.Fatalf("inspection = %+v, want one healthy committed batch", inspection)
	}
	batches, err := ReadCommittedEventBatches(context.Background(), sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, batch := range batches {
		total += len(batch.Events)
	}
	if total != 1 {
		t.Fatalf("committed events = %d, want exactly 1 (no duplicate from the reservation retry)", total)
	}
}

// TestMigrateToEventStoreDoesNotContendWithHeldLease covers the shape the Runtime
// bind actually arrives in: the durable Run owns the session lease and the bind
// migrates the same session inside that window.
//
// Migration must therefore never reserve an idle session. It converges facts that
// are already committed with no lease at all, and fences the single irreversible
// step (the content-store flip) against the exact owner/epoch/token this process
// already holds. If migration demanded idleness, binding a session would deadlock
// against the very Run it exists to serve.
func TestMigrateToEventStoreDoesNotContendWithHeldLease(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "migrate-held-lease"
	manager := New("/tmp/event-held-lease", sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := SaveSessionRun(sessionDir, SessionRun{
		ID: "run-held", SessionID: sessionID, Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireRecovery(sessionDir, sessionID, "run-held")
	if err != nil {
		t.Fatalf("acquire recovery lease: %v", err)
	}
	defer guard.Release()

	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatalf("migrate while this process holds the Run lease: %v", err)
	}
	flipped, err := sessionUsesEventStore(context.Background(), mustOpenRootDB(t, sessionDir), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !flipped {
		t.Fatal("session content store was not flipped to the EventStore under a held lease")
	}
	// The lease survives the migration: the Run this process is serving is
	// untouched, and its terminal transaction can still commit afterwards.
	if binding, ok := CurrentRuntimeLeaseBinding(sessionDir, sessionID); !ok || binding.RunID != "run-held" {
		t.Fatalf("lease after migration = %+v/%v, want run-held still bound", binding, ok)
	}
	messages, err := ListSessionMessagesWithSeq(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Message.Content != "hello" {
		t.Fatalf("replay after leased migration = %#v", messages)
	}
}

// TestAppendEventStoreEntriesAfterReleasedReservation proves the reservation is
// short-lived rather than an acquired-and-forgotten lock: a second append after
// the first released its reservation must succeed and extend the same log.
func TestAppendEventStoreEntriesAfterReleasedReservation(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "append-released-reservation"
	manager := New("/tmp/event-released-reservation", sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	first := MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "reservation-first", Timestamp: base}, Message: provider.NewUserMessage("one")}
	if _, err := manager.AppendEventStoreEntries(context.Background(), "", "", []interface{}{first}); err != nil {
		t.Fatal(err)
	}
	second := MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "reservation-second", ParentID: ptrString("reservation-first"), Timestamp: base.Add(time.Second)}, Message: provider.NewUserMessage("two")}
	if _, err := manager.AppendEventStoreEntries(context.Background(), "", "", []interface{}{second}); err != nil {
		t.Fatalf("append after the first reservation was released: %v", err)
	}
	batches, err := ReadCommittedEventBatches(context.Background(), sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	var lastSeq int64
	for _, batch := range batches {
		events += len(batch.Events)
		lastSeq = batch.SeqTo
	}
	if events != 2 || lastSeq != eventStoreSeqBase+1 {
		t.Fatalf("committed events = %d at last seq %d, want 2 events ending at %d", events, lastSeq, eventStoreSeqBase+1)
	}
}

func mustOpenRootDB(t *testing.T, sessionDir string) *dao.Database {
	t.Helper()
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func ptrString(value string) *string { return &value }

// TestOpenDuringPreparedIntentWindowReplaysCommittedHistory pins the read/write
// split of the `reconciling` state.
//
// `AppendPreparedEventBatch` inserts the SQLite intent row and then appends the
// bytes; the two cannot share an atomic commit, which is exactly why the intent
// protocol exists. A reader that opens the session inside that window therefore
// observes a `prepared` intent — the store is transiently `reconciling`. The
// proposal prescribes what such a reader does ("继续展示已有已提交历史，但不接受
// 新的 side-effecting Run"): replay the already-committed history and exclude the
// in-doubt batch, while every write gate keeps refusing. Load used to collapse
// both halves into one error, so racing a live Run turned a sibling's normal
// in-flight batch into a hard `session event store is reconciling` failure for
// unrelated readers (the intermittent `TestSubmitRun*` flakes).
//
// The window is entered honestly rather than simulated: a real fenced execution
// lease plus a real committed batch are produced by the production append path,
// and only the second batch is stopped between `PrepareEventIntent` and
// `store.Append` by preparing it directly. The assertions after the commit prove
// the relaxation is not a lossy read — once the sibling settles, its events
// become visible to a fresh open.
func TestOpenDuringPreparedIntentWindowReplaysCommittedHistory(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "reconciling-read"
	ctx := context.Background()

	manager := New("/tmp/event-reconciling-read", sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(ctx); err != nil {
		t.Fatal(err)
	}

	// A live Run holding an execution lease is the production shape that creates
	// the window: the lease is what authorises a control-plane intent, because
	// without it InsertFenced writes zero rows and no `prepared` row ever forms.
	admission, err := AcquireExecutionAdmission(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer admission.Release()
	now := time.Now().UTC()
	if _, err := CreateSessionRunAndEventWithTurn(sessionDir, SessionRun{
		ID: "run-inflight", SessionID: sessionID, Status: "running", StartedAt: now, UpdatedAt: now,
	}, SessionRunEvent{
		ID: "run-inflight-start", SessionID: sessionID, RunID: "run-inflight", EventType: "started", Timestamp: now,
	}, ConversationTurn{
		ID: "turn-inflight", SessionID: sessionID, IntentID: "intent-inflight", RunID: "run-inflight", Kind: "conversation", Status: "open", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	binding, ok := CurrentRuntimeLeaseBinding(sessionDir, sessionID)
	if !ok || binding.Purpose != RuntimeLeasePurposeExecution {
		t.Fatalf("lease after admission = %+v (ok=%v), want an execution lease", binding, ok)
	}

	committedEntry := MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "committed-msg", Timestamp: now}, Message: provider.NewUserMessage("committed")}
	if _, err := manager.AppendEventStoreEntries(ctx, "run-inflight", "turn-inflight", []interface{}{committedEntry}); err != nil {
		t.Fatal(err)
	}

	// Enter the window honestly: reproduce exactly what AppendPreparedEventBatch
	// does between its two non-atomic steps, stopping after the SQLite intent row
	// and before the bytes exist.
	inflight := MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "inflight-msg", ParentID: ptrString("committed-msg"), Timestamp: now.Add(time.Second)}, Message: provider.NewUserMessage("in flight")}
	record, err := EventRecordFromEntry(inflight, sessionID, "run-inflight", "turn-inflight")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := DigestEventBatch([]EventRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewEventStore(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	seqFrom, err := store.NextSequence()
	if err != nil {
		t.Fatal(err)
	}
	spec := EventIntentSpec{
		ID: "batch-inflight", SessionID: sessionID, RunID: "run-inflight", TurnID: "turn-inflight",
		SeqFrom: seqFrom, SeqTo: seqFrom, Digest: digest,
		OwnerInstanceID: binding.OwnerInstanceID, LeaseEpoch: binding.Epoch, LeaseTokenHash: binding.TokenHash,
	}
	if err := PrepareEventIntent(ctx, sessionDir, spec); err != nil {
		t.Fatal(err)
	}

	inspection, err := InspectEventStore(ctx, sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != EventStoreReconciling {
		t.Fatalf("state inside the window = %q, want %q (%s)", inspection.State, EventStoreReconciling, inspection.Reason)
	}

	// The reader: opening the session must succeed and show only committed bytes.
	reader, err := OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("open during the prepared window: %v", err)
	}
	messages := reader.GetMessages()
	if len(messages) != 1 || messages[0].Content != "committed" {
		t.Fatalf("replayed messages = %#v, want only the committed entry", messages)
	}

	// Reads stay honest, but the write fence must still refuse the same store:
	// appending over in-doubt bytes would let two batches claim one sequence.
	blocked := MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: "blocked-msg", ParentID: ptrString("inflight-msg"), Timestamp: now.Add(2 * time.Second)}, Message: provider.NewUserMessage("blocked")}
	if _, err := manager.AppendEventStoreEntries(ctx, "run-inflight", "turn-inflight", []interface{}{blocked}); !errors.Is(err, ErrEventStoreNotHealthy) {
		t.Fatalf("append during the prepared window = %v, want %v", err, ErrEventStoreNotHealthy)
	}

	// Settle the sibling batch and prove the relaxed read lost nothing.
	if _, err := store.Append(spec.ID, spec.SessionID, spec.RunID, spec.TurnID, []EventRecord{record}); err != nil {
		t.Fatal(err)
	}
	if err := markEventIntent(ctx, sessionDir, spec, EventIntentPrepared, EventIntentAppended, ""); err != nil {
		t.Fatal(err)
	}
	if err := CommitEventIntent(ctx, sessionDir, spec, nil); err != nil {
		t.Fatal(err)
	}
	settled, err := OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	messages = settled.GetMessages()
	if len(messages) != 2 || messages[1].Content != "in flight" {
		t.Fatalf("messages after the batch settled = %#v, want both entries", messages)
	}
}

// TestMigrateSessionRefusesUnsupportedEntryType pins the fail-closed half of the
// migration contract.
//
// The legacy schema declares `custom`/`custom_message`, but neither has a
// decoded structure, a provider projection, nor a declared reference rewrite
// policy (fork refuses the same pair). Exporting such a session must therefore
// error out rather than drop the row: a silent omission would shorten the
// transcript, shift every later EventStore sequence, and leave the turn bounds
// translated against a chain that no longer contains the entry. Refusing keeps
// the legacy payload authoritative and recoverable instead.
func TestMigrateSessionRefusesUnsupportedEntryType(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "unsupported-entry-session"
	manager := New("/tmp/event-unsupported", sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatal(err)
	}

	rootDB, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"type":       string(EntryCustom),
		"id":         "custom-1",
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
		"customType": "widget",
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := rootDB.Bun().Exec("INSERT INTO entries (session_id, id, type, parent_id, timestamp, data) VALUES (?, ?, ?, NULL, ?, ?)", sessionID, "custom-1", string(EntryCustom), timestamp, string(payload)); err != nil {
		t.Fatal(err)
	}

	if err := manager.MigrateToEventStore(context.Background()); err == nil {
		t.Fatal("migration of a session holding an unsupported entry type succeeded, want a refusal")
	} else if !strings.Contains(err.Error(), string(EntryCustom)) {
		t.Fatalf("migration error = %v, want it to name the unsupported entry type", err)
	}

	// Refusing must be non-destructive: the legacy transcript stays intact so a
	// later build that learns the type can still migrate this session.
	rows, err := dao.NewSessionDAO(rootDB.Bun()).TranscriptRows(context.Background(), "entries", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("legacy transcript rows after refused migration = %d, want the original transcript intact", len(rows))
	}
	foundCustom := false
	for _, row := range rows {
		if row.ID == "custom-1" {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Fatal("the unsupported entry was removed from the legacy transcript by a refused migration")
	}
	flipped, err := sessionUsesEventStore(context.Background(), rootDB, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if flipped {
		t.Fatal("content store was flipped to the EventStore despite a refused export")
	}
}

// TestOpenConvergesFlippedSessionWithoutBind pins the convergence-on-open
// invariant. Convergence is a property of the session, not of whichever entry
// point happens to call SessionRuntime.BindSession: serve/chat, the background
// run coordinator, channels, ESM, ACP and the TUI's own resume path all obtain a
// Manager through load() and never bind. Before this hook existed, each of those
// paths replayed the legacy SQLite duplicate of a transcript the EventStore
// already owned, and a session flipped by an older build kept holding
// `conversation_turns` bounds that named deleted legacy sequences forever.
//
// Every assertion below is made on a plain open with no Runtime and no
// MigrateToEventStore call in sight, so nothing but the open itself can satisfy
// it. Two populations are covered: a session whose legacy payload duplicate
// outlived the flip, and one flipped by a build predating the boundary remap
// whose control rows still name deleted legacy sequences.
func TestOpenConvergesFlippedSessionWithoutBind(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "converge-on-open-session"
	const workDir = "/tmp/event-converge-on-open"

	// A completed legacy transcript with one closed turn, built exactly as an
	// un-migrated session holds it.
	manager := New(workDir, sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	if _, err := manager.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := StartConversationTurn(sessionDir, ConversationTurn{ID: "turn-1", SessionID: sessionID, IntentID: "intent-1", RunID: "run-1", Kind: "conversation", Status: "open", StartedAt: startedAt}); err != nil {
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
	if legacyBounds.start == 0 || legacyBounds.end == 0 {
		t.Fatalf("legacy turn bounds = %+v, want a closed interval", legacyBounds)
	}

	// Export for real, so the session genuinely selects the EventStore and the
	// markers land in the new sequence space.
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventBounds := markerBoundsForTest(t, sessionDir, sessionID)
	if eventBounds.start == 0 || eventBounds.end <= eventBounds.start {
		t.Fatalf("event-log marker bounds = %+v, want a closed interval above zero", eventBounds)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}

	// Reinstate the payload copy the post-commit cleanup removed. This is the
	// crash window between the content-store flip and convergence, and more
	// importantly the state every unbound entry point used to inherit for good.
	forceLegacyPayloadDuplicateForTest(t, sessionDir, sessionID)
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count == 0 {
		t.Fatal("setup did not leave a legacy payload duplicate to converge away")
	}
	// Roll the control row back to the legacy numbers, exactly as a build without
	// the boundary remap left the population this repair exists for. The payload
	// duplicate above is already gone in that population, so `entries` is cleared
	// again after staging to keep the two hazards independent.
	rootDB := mustOpenRootDB(t, sessionDir)
	if _, err := rootDB.Bun().Exec("UPDATE conversation_turns SET start_seq = ?, end_seq = ? WHERE session_id = ? AND id = ?",
		legacyBounds.start, legacyBounds.end, sessionID, "turn-1"); err != nil {
		t.Fatal(err)
	}
	staged := conversationTurnBoundsForTest(t, sessionDir, sessionID)
	if staged != legacyBounds {
		t.Fatalf("staged bounds = %+v, want the legacy numbers %+v", staged, legacyBounds)
	}

	// Opening alone must retire the duplicate and replay the canonical log. No
	// MigrateToEventStore follows, so the hook inside load() is the only thing
	// that can make these assertions true.
	reopened, err := Open(manager.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.eventStoreModeForTest() {
		t.Fatal("opened manager did not load through the EventStore")
	}
	if got := reopened.GetMessages(); !equalReplay(got, []string{"hello", "world"}) {
		t.Fatalf("opened manager replay has %d messages, want [hello world]: %#v", len(got), textsOf(got))
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != 0 {
		t.Fatalf("opened session still holds %d legacy payload rows, want the duplicate removed by convergence", count)
	}
	turns := conversationTurnBoundsForTest(t, sessionDir, sessionID)
	if turns != eventBounds {
		t.Fatalf("turn bounds after open = %+v, want the event-log markers %+v", turns, eventBounds)
	}
	if turns == legacyBounds {
		t.Fatal("turn bounds were never translated out of the legacy sequence space")
	}

	// Convergence must be a fixed point: reopening an already-converged session
	// neither rewrites the control plane nor loses content.
	again, err := Open(manager.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	if got := again.GetMessages(); !equalReplay(got, []string{"hello", "world"}) {
		t.Fatalf("second open replay has %d messages, want [hello world]: %#v", len(got), textsOf(got))
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != 0 {
		t.Fatalf("second open left %d legacy payload rows", count)
	}
	if stable := conversationTurnBoundsForTest(t, sessionDir, sessionID); stable != turns {
		t.Fatalf("turn bounds moved across an idempotent open: %+v -> %+v", turns, stable)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
}

// forceLegacyPayloadDuplicateForTest reinstates the SQLite transcript copy of a
// session whose content the EventStore already owns. It replays the committed
// batches back into `entries`, so the fixture reproduces the crash window
// honestly instead of asking the export path to leave a duplicate behind.
func forceLegacyPayloadDuplicateForTest(t *testing.T, sessionDir, sessionID string) {
	t.Helper()
	batches, err := ReadCommittedEventBatches(context.Background(), sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	db := mustOpenRootDB(t, sessionDir)
	for _, batch := range batches {
		for _, record := range batch.Events {
			entry, err := DecodeEventRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			id, typeStr, parentID, ts := getEntryMetadata(entry)
			if id == "" || typeStr == string(EntrySession) {
				continue
			}
			data, err := marshalSessionEntry(entry)
			if err != nil {
				t.Fatal(err)
			}
			var parent any
			if parentID != nil {
				parent = *parentID
			}
			if _, err := db.Bun().Exec("INSERT INTO entries (session_id, id, type, parent_id, timestamp, data) VALUES (?, ?, ?, ?, ?, ?)",
				sessionID, id, typeStr, parent, ts.Format(time.RFC3339Nano), string(data)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// turnBounds names the two positions a `conversation_turns` row claims bound one
// logical turn. The control row and the event log are read through the same type
// so a comparison cannot pass silently on a zero value.
type turnBounds struct {
	start int64
	end   int64
}

func conversationTurnBoundsForTest(t *testing.T, sessionDir, sessionID string) turnBounds {
	t.Helper()
	turns, err := ListConversationTurns(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].EndSeq == nil {
		t.Fatalf("turns of session %s = %#v, want one closed turn", sessionID, turns)
	}
	return turnBounds{start: turns[0].StartSeq, end: *turns[0].EndSeq}
}

func markerBoundsForTest(t *testing.T, sessionDir, sessionID string) turnBounds {
	t.Helper()
	db := mustOpenRootDB(t, sessionDir)
	markers, err := dao.NewSessionEventProjectionDAO(db.Bun()).BoundarySeqsByTurn(context.Background(), db.Bun(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 2 {
		t.Fatalf("turn markers of session %s = %v, want two", sessionID, markers)
	}
	return turnBounds{start: markers[0].Seq, end: markers[1].Seq}
}

// TestReloadConvergesFlippedSessionWithoutDeadlock covers the two hazards that
// live in the same call stack. Manager.Reload() owns m.mu, so it must reload
// through the locked body: routing it back through load() would re-lock a
// non-reentrant mutex and hang the process instead of returning. And because a
// retained Manager is exactly how serve entry points observe another path's
// write, the reload must also converge onto the EventStore rather than keep
// serving the stale in-memory legacy transcript.
func TestReloadConvergesFlippedSessionWithoutDeadlock(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "reload-converge-session"
	const workDir = "/tmp/event-reload-converge"

	manager := New(workDir, sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	if manager.eventStoreModeForTest() {
		t.Fatal("a legacy session loaded before the flip already reports the EventStore")
	}

	other, err := OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Reload must return (no self-deadlock) and must stop serving the duplicate.
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if !manager.eventStoreModeForTest() {
		t.Fatal("reload did not adopt the EventStore as the canonical content source")
	}
	if got := manager.GetMessages(); !equalReplay(got, []string{"hello"}) {
		t.Fatalf("reloaded replay = %#v, want [hello]", got)
	}
	if count := legacyTranscriptPayloadCount(t, sessionDir, sessionID); count != 0 {
		t.Fatalf("reloaded session still holds %d legacy payload rows", count)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
}

// eventStoreModeForTest reads this Manager's in-memory content-source decision.
// The durable column is a separate fact: an export flips both, but convergence
// is what makes a *retained* Manager agree with them, so these tests assert on
// the Manager rather than only on the database row.
func (m *Manager) eventStoreModeForTest() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.eventStoreMode
}

// equalReplay compares a replayed transcript against the texts it must contain.
// Assistant blocks live in Contents, so comparing only Content would pass on an
// empty transcript and fail on a faithful one.
func equalReplay(messages []provider.Message, want []string) bool {
	if len(messages) != len(want) {
		return false
	}
	for i, message := range messages {
		text := message.Content
		if text == "" && len(message.Contents) == 1 {
			text = message.Contents[0].Text
		}
		if text != want[i] {
			return false
		}
	}
	return true
}

// textsOf renders a replayed transcript as bare text so a failure names the
// duplication or loss that happened instead of dumping every Message field.
func textsOf(messages []provider.Message) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		text := message.Content
		if text == "" && len(message.Contents) == 1 {
			text = message.Contents[0].Text
		}
		out = append(out, text)
	}
	return out
}

// TestEventStoreManifestLeavesNoTempFile pins the cleanup of the rebuildable
// manifest publish. Every append and every repairing scan republishes
// `manifest.json` through a temp file in the same private directory; an ignored
// unlink left `.manifest-*.tmp` behind, and because those directories live under
// a session's EventStore they outlive nothing else in the process — a test's
// t.TempDir() teardown failed with "directory not empty" whenever a request had
// already returned but its late write had not. Success must rename (so no temp
// survives), and a refused publish must still remove what it created.
func TestEventStoreManifestLeavesNoTempFile(t *testing.T) {
	root := t.TempDir()
	store, err := NewEventStore(root, "session-manifest-temp")
	if err != nil {
		t.Fatal(err)
	}
	assertNoManifestTemp := func(stage string) {
		t.Helper()
		entries, err := os.ReadDir(store.Path())
		if err != nil {
			t.Fatalf("%s: read event store directory: %v", stage, err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".manifest-") {
				t.Fatalf("%s: leaked manifest temp %s", stage, entry.Name())
			}
		}
	}

	if _, err := store.Append("batch-manifest-1", "session-manifest-temp", "", "", []EventRecord{{EventID: "event-manifest-1", Kind: "message", Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	assertNoManifestTemp("append")
	manifestPath := filepath.Join(store.Path(), eventStoreManifest)
	if _, statErr := os.Stat(manifestPath); statErr != nil {
		t.Fatalf("append did not publish the manifest: %v", statErr)
	}

	// A scan repairs the tail and republishes the manifest on the same path.
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	assertNoManifestTemp("scan")
	if _, statErr := os.Stat(manifestPath); statErr != nil {
		t.Fatalf("scan dropped the published manifest: %v", statErr)
	}

	// The failure path must clean up too: with the destination unpublishable the
	// temp file is removed rather than abandoned.
	layout, err := store.scanLayout()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	// A directory occupying the manifest name makes the rename fail deterministically,
	// which is the publish-refusal case the ignored unlink used to abandon a temp in.
	if err := os.MkdirAll(filepath.Join(manifestPath, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.writeManifest(layout, 2); err == nil || !strings.Contains(err.Error(), "publish event store manifest") {
		t.Fatalf("writeManifest error = %v, want publish failure", err)
	}
	assertNoManifestTemp("failed publish")
}

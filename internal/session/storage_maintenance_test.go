package session

import (
	"context"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

func claimToolRecordForStorageTest(t *testing.T, db *dao.Database, sessionID, key, state string, created time.Time, completed *time.Time) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin tool record: %v", err)
	}
	defer tx.Rollback()
	var completedAt *string
	if completed != nil {
		formatted := completed.UTC().Format(time.RFC3339Nano)
		completedAt = &formatted
	}
	if _, _, err := dao.NewResponseDAO(db.Bun()).ClaimTool(context.Background(), tx, &dao.ToolExecutionRecord{
		SessionID: sessionID, LocalTurnID: "turn-storage", ExecutionKey: key,
		Provider: "openai", API: "responses", ToolKind: "function", ToolName: "read_file",
		ArgsHash: "hash", ExecutionState: state,
		CreatedAt: created.UTC().Format(time.RFC3339Nano), CompletedAt: completedAt,
	}); err != nil {
		t.Fatalf("claim tool record %s: %v", key, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tool record %s: %v", key, err)
	}
}

// TestMaintainSessionStoragePreservesTranscriptAndPrunesTerminalTools pins the
// retention contract: transcript rows are never removed, and only terminal
// tool-execution records older than the configured cutoff are pruned. In-flight
// states stay because they fence an uncertain side effect.
func TestMaintainSessionStoragePreservesTranscriptAndPrunesTerminalTools(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("storage-maintenance"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("retained-message-marker")); err != nil {
		t.Fatal(err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-60 * 24 * time.Hour)
	oldCompleted := old.Add(time.Minute)
	freshCompleted := now.Add(-time.Hour)
	claimToolRecordForStorageTest(t, db, "storage-maintenance", "old-completed", "completed", old, &oldCompleted)
	claimToolRecordForStorageTest(t, db, "storage-maintenance", "old-abandoned", "abandoned", old, &oldCompleted)
	claimToolRecordForStorageTest(t, db, "storage-maintenance", "old-running", "running", old, nil)
	claimToolRecordForStorageTest(t, db, "storage-maintenance", "old-interrupted", "interrupted", old, nil)
	claimToolRecordForStorageTest(t, db, "storage-maintenance", "old-retry", "retry_requested", old, nil)
	claimToolRecordForStorageTest(t, db, "storage-maintenance", "fresh-completed", "completed", now, &freshCompleted)

	result, err := MaintainSessionStorage(context.Background(), sessionDir, SessionStoragePolicy{
		ToolExecutionRetention: 30 * 24 * time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("maintain session storage: %v", err)
	}
	if result.PrunedToolRecords != 2 {
		t.Fatalf("pruned tool records = %d, want only the aged terminal pair", result.PrunedToolRecords)
	}
	if result.Entries != 2 {
		t.Fatalf("transcript entries = %d, want the header and message preserved", result.Entries)
	}
	var retained int
	if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM entries WHERE data LIKE ?`, "%retained-message-marker%").Scan(&retained); err != nil {
		t.Fatalf("inspect retained message: %v", err)
	}
	if retained != 1 {
		t.Fatalf("retained message rows = %d, want the transcript untouched", retained)
	}
	if result.SubEntries != 0 {
		t.Fatalf("sub-agent entries = %d, want none created", result.SubEntries)
	}
	if result.ToolRecords != 4 {
		t.Fatalf("remaining tool records = %d, want in-flight + fresh rows kept", result.ToolRecords)
	}
	if result.Vacuumed {
		t.Fatal("maintenance vacuumed without an explicit opt-in")
	}

	// A second pass is a no-op and still never touches the message.
	again, err := MaintainSessionStorage(context.Background(), sessionDir, SessionStoragePolicy{
		ToolExecutionRetention: 30 * 24 * time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("repeat maintain session storage: %v", err)
	}
	if again.PrunedToolRecords != 0 || again.Entries != 2 {
		t.Fatalf("repeat maintenance = %#v, want no further pruning and both preserved entries", again)
	}
}

// TestMaintainSessionStorageKeepsAllRecordsByDefault pins the product choice:
// retention 0 means every transcript and tool-execution record stays, even when
// the maintenance job runs.
func TestMaintainSessionStorageKeepsAllRecordsByDefault(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("storage-keep-all"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("keep-everything")); err != nil {
		t.Fatal(err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-365 * 24 * time.Hour)
	completed := old.Add(time.Minute)
	claimToolRecordForStorageTest(t, db, "storage-keep-all", "ancient-completed", "completed", old, &completed)

	result, err := MaintainSessionStorage(context.Background(), sessionDir, SessionStoragePolicy{}, now)
	if err != nil {
		t.Fatalf("maintain session storage: %v", err)
	}
	if result.PrunedToolRecords != 0 {
		t.Fatalf("pruned %d records with retention 0, want every record kept", result.PrunedToolRecords)
	}
	if result.Entries != 2 || result.ToolRecords != 1 {
		t.Fatalf("counts = %#v, want the transcript and the tool record preserved", result)
	}
}

// TestMaintainSessionStorageVacuumsOnlyWhenOptedIn covers the space-reclamation
// path: pruning a large terminal record leaves free pages, and VACUUM is only
// performed when the policy explicitly asks for it.
func TestMaintainSessionStorageVacuumsOnlyWhenOptedIn(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("storage-vacuum"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("vacuum-retained-message")); err != nil {
		t.Fatal(err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-60 * 24 * time.Hour)
	completed := old.Add(time.Minute)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin large tool record: %v", err)
	}
	completedAt := completed.UTC().Format(time.RFC3339Nano)
	if _, _, err := dao.NewResponseDAO(db.Bun()).ClaimTool(context.Background(), tx, &dao.ToolExecutionRecord{
		SessionID: "storage-vacuum", LocalTurnID: "turn-vacuum", ExecutionKey: "large-old-completed",
		Provider: "openai", API: "responses", ToolKind: "function", ToolName: "read_file",
		ArgsHash: "hash", ExecutionState: "completed",
		ResultSummaryJSON: make([]byte, 2<<20),
		CreatedAt:         old.UTC().Format(time.RFC3339Nano), CompletedAt: &completedAt,
	}); err != nil {
		t.Fatalf("claim large tool record: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit large tool record: %v", err)
	}

	result, err := MaintainSessionStorage(context.Background(), sessionDir, SessionStoragePolicy{
		ToolExecutionRetention: 30 * 24 * time.Hour,
		Vacuum:                 true,
	}, now)
	if err != nil {
		t.Fatalf("maintain with vacuum: %v", err)
	}
	if result.PrunedToolRecords != 1 {
		t.Fatalf("pruned tool records = %d, want the aged large record", result.PrunedToolRecords)
	}
	if !result.Vacuumed {
		t.Fatal("VACUUM was requested and free pages existed, but it did not run")
	}
	if result.Entries != 2 {
		t.Fatalf("transcript entries = %d, want the message preserved across VACUUM", result.Entries)
	}
}

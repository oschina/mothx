package session

import (
	"context"
	"testing"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

func emptyEventStoreSessionForTest(t *testing.T, sessionDir, sessionID string) *Manager {
	t.Helper()
	manager := New(t.TempDir(), sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatalf("init session: %v", err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatalf("flip empty session onto the EventStore: %v", err)
	}
	return manager
}

// TestDisplayProjectionServesListRowsWithoutEventStore pins the point of the
// SQLite projection: once it exists, preview/title/bindings reads do not need
// the EventStore bytes at all.
func TestDisplayProjectionServesListRowsWithoutEventStore(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	const sessionID = "display-projection"
	manager := emptyEventStoreSessionForTest(t, sessionDir, sessionID)
	if _, err := manager.AppendSessionTitle("Projection Title", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendModelChange("openai", "gpt-projection"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendModeChange("plan"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendThinkingLevelChange("high"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("projection preview marker")); err != nil {
		t.Fatal(err)
	}

	projections, err := loadDisplayProjections(context.Background(), sessionDir, []string{sessionID})
	if err != nil {
		t.Fatalf("load display projection: %v", err)
	}
	state, ok := projections[sessionID]
	if !ok {
		t.Fatal("display projection row is missing")
	}
	if state.Preview != "projection preview marker" || !state.PreviewSeen {
		t.Fatalf("projection preview = %#v", state)
	}
	if state.Title != "Projection Title" {
		t.Fatalf("projection title = %q", state.Title)
	}
	if !state.Bindings.HasModelChange || state.Bindings.ModelChange.ModelID != "gpt-projection" ||
		!state.Bindings.HasModeChange || state.Bindings.ModeChange.Mode != "plan" ||
		!state.Bindings.HasThinkingLevelChange || state.Bindings.ThinkingLevelChange.ThinkingLevel != "high" {
		t.Fatalf("projection bindings = %#v", state.Bindings)
	}

	// Removing the canonical bytes must not remove what a list row can still
	// project from SQLite.
	if err := RemoveEventStore(sessionDir, sessionID); err != nil {
		t.Fatal(err)
	}
	details := []SessionDetail{{ID: sessionID, MessageCount: 1}}
	FillSessionDetailPreviews(sessionDir, details)
	if details[0].Preview != "projection preview marker" || details[0].Name != "Projection Title" {
		t.Fatalf("list detail without EventStore = %#v, want the persisted projection", details[0])
	}
	bindings, err := LatestSessionBindings(sessionDir, []string{sessionID})
	if err != nil {
		t.Fatalf("read bindings: %v", err)
	}
	if !bindings[sessionID].HasModeChange || bindings[sessionID].ModeChange.Mode != "plan" {
		t.Fatalf("bindings without EventStore = %#v", bindings[sessionID])
	}
}

// TestDisplayProjectionTracksLaterAppends ensures the projection advances with
// committed batches once it exists, instead of freezing at the backfill value.
func TestDisplayProjectionTracksLaterAppends(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	const sessionID = "display-projection-updates"
	manager := emptyEventStoreSessionForTest(t, sessionDir, sessionID)
	if _, err := manager.AppendMessage(provider.NewUserMessage("first preview")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendSessionTitle("First Title", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendModeChange("agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendSessionTitle("Second Title", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendModeChange("yolo"); err != nil {
		t.Fatal(err)
	}
	// A later user message must not replace the first preview.
	if _, err := manager.AppendMessage(provider.NewUserMessage("later preview must not win")); err != nil {
		t.Fatal(err)
	}

	projections, err := loadDisplayProjections(context.Background(), sessionDir, []string{sessionID})
	if err != nil {
		t.Fatal(err)
	}
	state := projections[sessionID]
	if state.Preview != "first preview" {
		t.Fatalf("preview = %q, want the first user message", state.Preview)
	}
	if state.Title != "Second Title" {
		t.Fatalf("title = %q, want the latest title", state.Title)
	}
	if state.Bindings.ModeChange.Mode != "yolo" {
		t.Fatalf("mode = %q, want the latest mode", state.Bindings.ModeChange.Mode)
	}
}

// TestBackfillSessionDisplayProjections fills only missing EventStore rows and
// leaves unreadable stores for a later pass.
func TestBackfillSessionDisplayProjections(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	const sessionID = "display-backfill"
	manager := emptyEventStoreSessionForTest(t, sessionDir, sessionID)
	if _, err := manager.AppendMessage(provider.NewUserMessage("backfill preview")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendSessionTitle("Backfill Title", "manual"); err != nil {
		t.Fatal(err)
	}
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionDisplayProjectionDAO(nil).DeleteSession(context.Background(), tx, sessionID)
	}); err != nil {
		t.Fatal(err)
	}

	result, err := BackfillSessionDisplayProjections(context.Background(), sessionDir, 10)
	if err != nil {
		t.Fatalf("backfill display projections: %v", err)
	}
	if result.Sessions != 1 || result.Failed != 0 || result.Skipped != 0 {
		t.Fatalf("backfill result = %#v", result)
	}
	projections, err := loadDisplayProjections(context.Background(), sessionDir, []string{sessionID})
	if err != nil {
		t.Fatal(err)
	}
	if projections[sessionID].Preview != "backfill preview" || projections[sessionID].Title != "Backfill Title" {
		t.Fatalf("backfilled projection = %#v", projections[sessionID])
	}

	again, err := BackfillSessionDisplayProjections(context.Background(), sessionDir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if again.Sessions != 0 || again.Failed != 0 {
		t.Fatalf("repeat backfill = %#v, want nothing left to project", again)
	}
}

// TestDeleteSessionRemovesDisplayProjection keeps deleted sessions out of the
// derived table.
func TestDeleteSessionRemovesDisplayProjection(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	const sessionID = "display-delete"
	manager := convergedEventStoreSessionForTest(t, sessionDir, sessionID)
	if _, err := manager.AppendMessage(provider.NewUserMessage("delete projection")); err != nil {
		t.Fatal(err)
	}
	if projections, err := loadDisplayProjections(context.Background(), sessionDir, []string{sessionID}); err != nil || len(projections) == 0 {
		t.Fatalf("projection before delete = %#v err=%v", projections, err)
	}
	if err := DeleteSession(manager.GetFile(), sessionDir); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if projections, err := loadDisplayProjections(context.Background(), sessionDir, []string{sessionID}); err != nil || len(projections) != 0 {
		t.Fatalf("projection after delete = %#v err=%v", projections, err)
	}
}

// TestDisplayProjectionFromForkedEventStoreSession covers the event-store fork
// path, which creates a new session without the usual attach-time migration.
func TestDisplayProjectionFromForkedEventStoreSession(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	const sourceID = "display-fork-source"
	manager := emptyEventStoreSessionForTest(t, sessionDir, sourceID)
	turnID := "display-fork-turn"
	if err := manager.StartConversationTurn(turnID, "display-fork-intent", "display-fork-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("fork preview")); err != nil {
		t.Fatal(err)
	}
	if err := manager.EndConversationTurn(turnID, "completed", "stop"); err != nil {
		t.Fatal(err)
	}

	forked, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: sourceID, RequestID: "display-fork-request"})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	projections, err := loadDisplayProjections(context.Background(), sessionDir, []string{forked.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	state, ok := projections[forked.SessionID]
	if !ok {
		t.Fatal("forked session has no display projection")
	}
	if state.Preview != "fork preview" {
		t.Fatalf("forked preview = %q", state.Preview)
	}
}

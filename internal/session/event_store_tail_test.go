package session

import (
	"context"
	"errors"
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

func tailEventStoreSessionForTest(t *testing.T, sessionDir, sessionID string, messages int) *Manager {
	t.Helper()
	manager := New(t.TempDir(), sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < messages; i++ {
		if _, err := manager.AppendMessage(provider.NewUserMessage("message-" + string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := manager.AppendSessionTitle("Tail title", "manual"); err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestReadEventStoreTailPageReturnsOnlyTheRequestedWindow(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })
	const sessionID = "tail-page"
	manager := tailEventStoreSessionForTest(t, sessionDir, sessionID, 6)
	if _, err := manager.AppendModelChange("tail-provider", "tail-model"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendModeChange("plan"); err != nil {
		t.Fatal(err)
	}

	if !TailReplaySupported(sessionDir, sessionID) {
		t.Fatal("expected tail replay to be supported")
	}

	tail, err := ReadEventStoreTailPage(sessionDir, sessionID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if tail.Total != 6 || tail.Start != 4 || len(tail.Messages) != 2 {
		t.Fatalf("tail page = start %d total %d messages %d, want 4/6/2", tail.Start, tail.Total, len(tail.Messages))
	}
	if tail.Messages[0].Content != "message-e" || tail.Messages[1].Content != "message-f" {
		t.Fatalf("tail messages = %#v", []string{tail.Messages[0].Content, tail.Messages[1].Content})
	}

	middle, err := ReadEventStoreTailPage(sessionDir, sessionID, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if middle.Start != 2 || len(middle.Messages) != 2 || middle.Messages[0].Content != "message-c" || middle.Messages[1].Content != "message-d" {
		t.Fatalf("middle page = %#v", middle)
	}

	first, err := ReadEventStoreTailPage(sessionDir, sessionID, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if first.Start != 0 || len(first.Messages) != 2 || first.Messages[0].Content != "message-a" || first.Messages[1].Content != "message-b" {
		t.Fatalf("first page = %#v", first)
	}
}

func TestOpenByIDExactTailIsPartialUntilReload(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })
	const sessionID = "tail-open"
	manager := tailEventStoreSessionForTest(t, sessionDir, sessionID, 3)
	if _, err := manager.AppendModelChange("tail-provider", "tail-model"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendModeChange("plan"); err != nil {
		t.Fatal(err)
	}

	opened, err := OpenByIDExactTail(sessionDir, sessionID, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !opened.Partial() {
		t.Fatal("tail open must be partial")
	}
	if header := opened.GetHeader(); header == nil || header.ID != sessionID {
		t.Fatalf("tail header = %+v", header)
	}
	if model, ok := opened.GetLatestModelChange(); !ok || model.ModelID != "tail-model" {
		t.Fatalf("tail model binding = %+v, ok=%v", model, ok)
	}
	if mode, ok := opened.GetLatestModeChange(); !ok || mode.Mode != "plan" {
		t.Fatalf("tail mode binding = %+v, ok=%v", mode, ok)
	}
	if err := opened.Reload(); err != nil {
		t.Fatal(err)
	}
	if opened.Partial() {
		t.Fatal("Reload must clear the partial flag")
	}
	if got := len(opened.GetMessages()); got != 3 {
		t.Fatalf("reloaded messages = %d, want 3", got)
	}
}

func TestTailReplayRejectsCompactedSession(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })
	const sessionID = "tail-compacted"
	manager := tailEventStoreSessionForTest(t, sessionDir, sessionID, 3)
	if _, err := manager.AppendCompaction("summary", "", 0); err != nil {
		t.Fatal(err)
	}
	if TailReplaySupported(sessionDir, sessionID) {
		t.Fatal("compacted session must not use tail replay")
	}
	if _, err := ReadEventStoreTailPage(sessionDir, sessionID, 0, 2); !errors.Is(err, ErrTailReplayUnsupported) {
		t.Fatalf("tail replay error = %v, want ErrTailReplayUnsupported", err)
	}
}

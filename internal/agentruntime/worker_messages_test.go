package agentruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func mustWorkerMessageLogPath(t *testing.T, sessionDir, sessionID string) string {
	t.Helper()
	path, err := WorkerMessageLogPath(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("worker message log path: %v", err)
	}
	return path
}

// Two sessions sharing one sessions root must never share a worker message
// file, dedupe index, or compaction target. Reusing the same child ID and
// dedupe key in both sessions must still produce two independent findings.
func TestWorkerMessageLogsAreIsolatedBySession(t *testing.T) {
	sessionDir := t.TempDir()
	first, err := OpenWorkerMessageLog(sessionDir, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := OpenWorkerMessageLog(sessionDir, "session-b")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	if first.Path() == second.Path() {
		t.Fatalf("two sessions shared one worker message log: %s", first.Path())
	}
	if filepath.Base(filepath.Dir(first.Path())) != "session-a" ||
		filepath.Base(filepath.Dir(second.Path())) != "session-b" {
		t.Fatalf("worker message log is not session-scoped: %s / %s", first.Path(), second.Path())
	}

	firstMessage, _, err := first.Create(WorkerMessage{
		SessionID: "session-a", ChildID: "agent-1", Kind: WorkerMessageFinding,
		Body: "first session finding", DedupeKey: "same-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondMessage, duplicate, err := second.Create(WorkerMessage{
		SessionID: "session-b", ChildID: "agent-1", Kind: WorkerMessageFinding,
		Body: "second session finding", DedupeKey: "same-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate {
		t.Fatal("a different session's dedupe key suppressed this finding")
	}
	if firstMessage.ID == secondMessage.ID {
		t.Fatalf("cross-session messages reused one ID: %s", firstMessage.ID)
	}

	firstMessages, err := first.List()
	if err != nil {
		t.Fatal(err)
	}
	secondMessages, err := second.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(firstMessages) != 1 || firstMessages[0].Body != "first session finding" {
		t.Fatalf("session-a projection leaked or lost rows: %#v", firstMessages)
	}
	if len(secondMessages) != 1 || secondMessages[0].Body != "second session finding" {
		t.Fatalf("session-b projection leaked or lost rows: %#v", secondMessages)
	}

	// Reads are filtered by the requested session as well as by path.
	readA, err := ReadWorkerMessages(sessionDir, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(readA) != 1 || readA[0].SessionID != "session-a" {
		t.Fatalf("read projection for session-a = %#v", readA)
	}
}

// A copied or tampered file cannot smuggle another session's finding into the
// projection: every read filters on SessionID.
func TestWorkerMessageReadFiltersForeignSessionRows(t *testing.T) {
	sessionDir := t.TempDir()
	log, err := OpenWorkerMessageLog(sessionDir, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := log.Create(WorkerMessage{
		SessionID: "session-a", ChildID: "agent-a", Kind: WorkerMessageFinding, Body: "owned",
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	// Append a row crafted as another session directly to the file.
	file, err := os.OpenFile(mustWorkerMessageLogPath(t, sessionDir, "session-a"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"op":"created","id":"wm_foreign","sessionId":"session-b","childId":"agent-b","kind":"finding","body":"foreign","at":"2026-01-01T00:00:00Z"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	messages, err := ReadWorkerMessages(sessionDir, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Body != "owned" {
		t.Fatalf("foreign session row entered projection: %#v", messages)
	}
}

func TestWorkerMessageLogRejectsForeignSessionCreate(t *testing.T) {
	log, err := OpenWorkerMessageLog(t.TempDir(), "session-a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	if _, _, err := log.Create(WorkerMessage{SessionID: "session-b", ChildID: "agent-1", Kind: WorkerMessageFinding, Body: "x"}); err == nil {
		t.Fatal("a log accepted a message for another session")
	}
}

// Concurrent openers of the same session log share one instance so they cannot
// run independent dedupe indexes or compactors against the same file.
func TestWorkerMessageLogConcurrentOpenersShareOneCompactor(t *testing.T) {
	sessionDir := t.TempDir()
	first, err := OpenWorkerMessageLog(sessionDir, "session-shared")
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenWorkerMessageLog(sessionDir, "session-shared")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("two openers of one session received independent worker message logs")
	}
	if _, _, err := first.Create(WorkerMessage{
		SessionID: "session-shared", ChildID: "agent-1", Kind: WorkerMessageFinding, Body: "shared",
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := second.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("shared log projection = %#v", messages)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// The second reference keeps the log usable.
	if _, _, err := second.Create(WorkerMessage{
		SessionID: "session-shared", ChildID: "agent-2", Kind: WorkerMessageNotice, Body: "still open",
	}); err != nil {
		t.Fatalf("first Close closed a log still held by another opener: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveWorkerMessagesOnlyDeletesOneSession(t *testing.T) {
	sessionDir := t.TempDir()
	for _, sessionID := range []string{"session-a", "session-b"} {
		log, err := OpenWorkerMessageLog(sessionDir, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := log.Create(WorkerMessage{
			SessionID: sessionID, ChildID: "agent-1", Kind: WorkerMessageFinding, Body: sessionID,
		}); err != nil {
			t.Fatal(err)
		}
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveWorkerMessages(sessionDir, "session-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mustWorkerMessageLogPath(t, sessionDir, "session-a")); !os.IsNotExist(err) {
		t.Fatalf("session-a worker log survived deletion: %v", err)
	}
	messages, err := ReadWorkerMessages(sessionDir, "session-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].SessionID != "session-b" {
		t.Fatalf("deleting session-a damaged session-b: %#v", messages)
	}
}

func TestWorkerMessageLogPathRejectsEscapingSessionID(t *testing.T) {
	for _, sessionID := range []string{"", ".", "..", "../escape", "a/b"} {
		if _, err := WorkerMessageLogPath(t.TempDir(), sessionID); err == nil {
			t.Fatalf("session ID %q was accepted as a path component", sessionID)
		}
	}
}

// Session deletion must cascade the Runtime-private worker evidence log while
// leaving other sessions untouched.
func TestDeleteSessionRemovesWorkerMessages(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	_, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir, ID: "session-delete-worker"})
	if err != nil {
		t.Fatal(err)
	}

	log, err := OpenWorkerMessageLog(sessionDir, "session-delete-worker")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := log.Create(WorkerMessage{
		SessionID: "session-delete-worker", ChildID: "agent-1", Kind: WorkerMessageFinding, Body: "temporary evidence",
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	path := mustWorkerMessageLogPath(t, sessionDir, "session-delete-worker")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected worker evidence before deletion: %v", err)
	}
	if err := DeleteSession(sessionDir, "session-delete-worker"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("worker evidence survived session deletion: %v", err)
	}
}

// ACP multi-delete holds one shared mutation lease and calls
// DeleteSessionWithMutation per target. That path must cascade worker evidence
// exactly like the single-session DeleteSession path.
func TestDeleteSessionWithMutationRemovesWorkerMessages(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	for _, id := range []string{"session-multi-delete", "session-multi-keep"} {
		if _, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir, ID: id}); err != nil {
			t.Fatal(err)
		}
		log, err := OpenWorkerMessageLog(sessionDir, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := log.Create(WorkerMessage{
			SessionID: id, ChildID: "agent-1", Kind: WorkerMessageFinding, Body: "evidence for " + id,
		}); err != nil {
			t.Fatal(err)
		}
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
	}

	deletedPath := mustWorkerMessageLogPath(t, sessionDir, "session-multi-delete")
	keptPath := mustWorkerMessageLogPath(t, sessionDir, "session-multi-keep")
	guard, err := AcquireSessionMutation(t.Context(), sessionDir, "session-multi-delete", ExecutionAdmissionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	deleted := false
	defer func() {
		if !deleted {
			guard.Release()
		}
	}()
	if err := DeleteSessionWithMutation(sessionDir, "session-multi-delete", guard); err != nil {
		t.Fatal(err)
	}
	deleted = true
	guard.Release()

	if _, err := os.Stat(deletedPath); !os.IsNotExist(err) {
		t.Fatalf("worker evidence survived DeleteSessionWithMutation: %v", err)
	}
	if _, err := os.Stat(keptPath); err != nil {
		t.Fatalf("deleting one session removed another session's worker evidence: %v", err)
	}
	messages, err := ReadWorkerMessages(sessionDir, "session-multi-keep")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].SessionID != "session-multi-keep" {
		t.Fatalf("kept session projection = %#v", messages)
	}
}

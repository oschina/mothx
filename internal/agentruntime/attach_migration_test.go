package agentruntime

import (
	"context"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

// attachedContentStore reads the persisted content-store selection plus the
// number of legacy payload rows still held by SQLite for a session.
func attachedContentStore(t *testing.T, sessionDir, sessionID string) (string, int) {
	t.Helper()
	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		t.Fatalf("open root db: %v", err)
	}
	var payloadRows int
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ? AND type <> 'session'", sessionID).Scan(&payloadRows); err != nil {
		t.Fatalf("count legacy payload rows: %v", err)
	}
	header, err := dao.NewSessionDAO(db.Bun()).Header(context.Background(), "sessions", sessionID)
	if err != nil {
		t.Fatalf("read session header: %v", err)
	}
	return header.ContentStore, payloadRows
}

func attachMigratedSession(t *testing.T, workDir, sessionDir string, manager *session.Manager) *SessionRuntime {
	t.Helper()
	registry := tools.NewRegistry(workDir, sandbox.NewNoneSandbox())
	attached, err := AttachSessionResources(AttachedResources{
		Source: SourceACP, WorkDir: workDir, Manager: manager, Registry: registry,
	})
	if err != nil {
		t.Fatalf("AttachSessionResources: %v", err)
	}
	return attached
}

// TestAttachSessionResourcesMigratesLegacyTranscriptToEventStore pins the
// unified migration entry point: an adapter that attaches resources directly
// (ACP/Desktop, channels, and knowledge runs) must export an existing legacy
// session exactly like SessionRuntime.BindSession does, so content ownership
// cannot diverge by entry point.
func TestAttachSessionResourcesMigratesLegacyTranscriptToEventStore(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("legacy before attach")); err != nil {
		t.Fatal(err)
	}

	attachMigratedSession(t, workDir, sessionDir, manager)

	store, payloadRows := attachedContentStore(t, sessionDir, manager.GetHeader().ID)
	if store != "event_store" {
		t.Fatalf("content store after attach = %q, want event_store", store)
	}
	if payloadRows != 0 {
		t.Fatalf("legacy payload rows after attach = %d, want 0", payloadRows)
	}
	if messages := manager.GetMessages(); len(messages) != 1 || messages[0].Content != "legacy before attach" {
		t.Fatalf("migrated replay after attach = %#v", messages)
	}
}

// TestAttachSessionResourcesFlipsEmptySessionToEventStore proves a brand-new
// session opened through an attach-only adapter adopts the EventStore just like
// one bound by TUI/CLI/WebUI, instead of silently starting on the legacy store.
func TestAttachSessionResourcesFlipsEmptySessionToEventStore(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}

	attachMigratedSession(t, workDir, sessionDir, manager)

	store, payloadRows := attachedContentStore(t, sessionDir, manager.GetHeader().ID)
	if store != "event_store" {
		t.Fatalf("content store after attach = %q, want event_store", store)
	}
	if payloadRows != 0 {
		t.Fatalf("legacy payload rows after attach = %d, want 0", payloadRows)
	}
}

// TestBuilderBuildMigratesLegacyTranscriptToEventStore covers the third attach
// path: a Builder handed a persisted Manager (TUI/CLI shared runtimes) must run
// the same migration as BindSession and AttachSessionResources.
func TestBuilderBuildMigratesLegacyTranscriptToEventStore(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("legacy before build")); err != nil {
		t.Fatal(err)
	}

	runtime, err := testBuilder().Build(context.Background(), BuildOptions{WorkDir: workDir, Manager: manager})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer runtime.Close()

	store, payloadRows := attachedContentStore(t, sessionDir, manager.GetHeader().ID)
	if store != "event_store" {
		t.Fatalf("content store after build = %q, want event_store", store)
	}
	if payloadRows != 0 {
		t.Fatalf("legacy payload rows after build = %d, want 0", payloadRows)
	}
	if messages := manager.GetMessages(); len(messages) != 1 || messages[0].Content != "legacy before build" {
		t.Fatalf("migrated replay after build = %#v", messages)
	}
}

// TestAttachSessionResourcesDefersMigrationForActiveRun proves the unified entry
// never blocks an adapter from attaching to a session another owner is actively
// running: the migration is deferred and the self-consistent legacy store keeps
// serving until a later attach finds the session idle.
func TestAttachSessionResourcesDefersMigrationForActiveRun(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	sessionID := manager.GetHeader().ID
	if _, err := manager.AppendMessage(provider.NewUserMessage("legacy busy transcript")); err != nil {
		t.Fatal(err)
	}
	// An orphaned active run row (no live lease held by this process) makes the
	// session unavailable for export, exactly like a peer process mid-run.
	if err := (RunStore{SessionDir: sessionDir}).Create(DurableRun{
		ID: "attach-defer-run", SessionID: sessionID, Source: "tui", Status: string(RunStateRunning), StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed active run: %v", err)
	}

	attachMigratedSession(t, workDir, sessionDir, manager)

	store, payloadRows := attachedContentStore(t, sessionDir, sessionID)
	if store != "sqlite" {
		t.Fatalf("content store after deferred attach = %q, want sqlite", store)
	}
	if payloadRows != 1 {
		t.Fatalf("legacy payload rows after deferred attach = %d, want 1", payloadRows)
	}
	if messages := manager.GetMessages(); len(messages) != 1 || messages[0].Content != "legacy busy transcript" {
		t.Fatalf("deferred replay = %#v", messages)
	}
}

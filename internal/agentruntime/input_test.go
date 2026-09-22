package agentruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

func TestInputMaterializerWritesProjectResourceAndManifest(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	materializer, err := NewInputMaterializer(root, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatalf("NewInputMaterializer: %v", err)
	}
	record, err := materializer.Prepare(t.Context(), mgr.GetHeader().ID, "run-1", InputIngress{
		Origin: "test", EventID: "message-1", ItemIndex: 0, Reference: "secret-reference",
		Kind: AttachmentFile, FilenameHint: "notes.txt", MediaTypeHint: "text/plain",
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(strings.NewReader("hello input"))}, nil
		},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasPrefix(record.RelativePath, ".mothx/tmp/inputs/") || filepath.IsAbs(record.RelativePath) {
		t.Fatalf("RelativePath = %q", record.RelativePath)
	}
	data, err := os.ReadFile(filepath.Join(workDir, filepath.FromSlash(record.RelativePath)))
	if err != nil || string(data) != "hello input" {
		t.Fatalf("materialized content = %q, %v", data, err)
	}
	var metadata string
	if err := session.QueryRootDatabase(root, func(db *dao.Database) error {
		return db.Bun().QueryRow(`SELECT metadata FROM input_resources WHERE id = ?`, record.ID).Scan(&metadata)
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(metadata, "secret-reference") {
		t.Fatalf("transport reference persisted: %q", metadata)
	}
	runtime := &SessionRuntime{ID: mgr.GetHeader().ID, WorkDir: workDir, Inputs: materializer}
	msg, err := runtime.BuildUserMessage(t.Context(), InputSubmission{Text: "inspect this", Resources: []PreparedInput{record.Prepared()}})
	if err != nil {
		t.Fatalf("BuildUserMessage: %v", err)
	}
	if !strings.Contains(msg.Content, record.RelativePath) || !strings.Contains(msg.Content, "Use read") {
		t.Fatalf("message manifest = %q", msg.Content)
	}
	if len(msg.Contents) != 0 || strings.Contains(msg.Content, "read_attachment") {
		t.Fatalf("message retained legacy content: %#v", msg)
	}
}

func TestInputMaterializerCanonicalizesImageWithoutDirectProviderContent(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	materializer, err := NewInputMaterializer(root, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatal(err)
	}
	png := onePixelPNG(t)
	record, err := materializer.Prepare(t.Context(), mgr.GetHeader().ID, "run-image", InputIngress{
		Origin: "test", Kind: AttachmentImage, FilenameHint: "screen.bin", MediaTypeHint: "application/octet-stream",
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(bytes.NewReader(png))}, nil
		},
	})
	if err != nil {
		t.Fatalf("Prepare image: %v", err)
	}
	if record.MediaType != "image/png" || filepath.Ext(record.Filename) != ".png" {
		t.Fatalf("canonical image record = %#v", record)
	}
	runtime := &SessionRuntime{ID: mgr.GetHeader().ID, WorkDir: workDir, Inputs: materializer}
	msg, err := runtime.BuildUserMessage(t.Context(), InputSubmission{Resources: []PreparedInput{record.Prepared()}})
	if err != nil {
		t.Fatalf("BuildUserMessage: %v", err)
	}
	if len(msg.Contents) != 0 || !strings.Contains(msg.Content, record.RelativePath) {
		t.Fatalf("image input was not path-only: %#v", msg)
	}
	// Input acceptance is provider-neutral. A text-only model is evaluated only
	// if a later tool actually returns rich image content.
	_ = &provider.Model{ID: "text-only", Input: []string{"text"}}
}

func TestInputMaterializerDetectsExtensionlessWebP(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	mater, err := NewInputMaterializer(root, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	record, err := mater.Prepare(t.Context(), mgr.GetHeader().ID, "run-webp", InputIngress{
		Origin: "test", EventID: "webp-event", Kind: AttachmentImage, FilenameHint: "clipboard",
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(bytes.NewReader(data))}, nil
		},
	})
	if err != nil {
		t.Fatalf("Prepare extensionless WebP: %v", err)
	}
	if record.MediaType != "image/webp" || filepath.Ext(record.Filename) != ".webp" {
		t.Fatalf("extensionless WebP record = %#v", record)
	}
}

func TestInputMaterializerDeduplicatesConcurrentEventItem(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	materializer, err := NewInputMaterializer(root, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatal(err)
	}
	ingress := InputIngress{
		Origin: "channel:wechat", EventID: "event-42", ItemIndex: 1,
		Kind: AttachmentFile, FilenameHint: "same.txt",
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(strings.NewReader("same bytes"))}, nil
		},
	}
	var wg sync.WaitGroup
	records := make([]InputResource, 2)
	errs := make([]error, 2)
	for i := range records {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			records[index], errs[index] = materializer.Prepare(context.Background(), mgr.GetHeader().ID, "run-1", ingress)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Prepare: %v", err)
		}
	}
	if records[0].ID == "" || records[0].ID != records[1].ID {
		t.Fatalf("deduplicated records = %#v", records)
	}
	var count int
	if err := session.QueryRootDatabase(root, func(db *dao.Database) error {
		return db.Bun().QueryRow(`SELECT COUNT(*) FROM input_resources WHERE session_id = ?`, mgr.GetHeader().ID).Scan(&count)
	}); err != nil || count != 1 {
		t.Fatalf("resource count = %d, %v", count, err)
	}
}

func TestInputMaterializerUsesStableHMACForReferenceFallback(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	mater, err := NewInputMaterializer(root, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatal(err)
	}
	ingress := InputIngress{
		Origin: "wechat", Reference: "opaque-reference", Kind: AttachmentFile, ItemIndex: 3,
		FilenameHint: "voice.amr", MediaTypeHint: "audio/amr",
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(strings.NewReader("voice"))}, nil
		},
	}
	first, err := mater.Prepare(t.Context(), mgr.GetHeader().ID, "", ingress)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mater.Prepare(t.Context(), mgr.GetHeader().ID, "", ingress)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.ItemKey == "" || first.EventID != "" {
		t.Fatalf("reference fallback records = %#v and %#v", first, second)
	}
	if strings.Contains(first.ItemKey, ingress.Reference) {
		t.Fatalf("item key leaked reference: %q", first.ItemKey)
	}
	other, err := NewInputMaterializer(root, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatal(err)
	}
	third, err := other.Prepare(t.Context(), mgr.GetHeader().ID, "", ingress)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID != first.ID {
		t.Fatalf("cross-materializer ID = %q, want %q", third.ID, first.ID)
	}
}

func TestInputResourceLifecycleEventsAndDraftCleanup(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	mater, err := NewInputMaterializer(root, workDir, InputPolicy{
		MaxImageBytes: 1 << 20, MaxFileBytes: 1 << 20, MaxImagePixels: 100,
		DraftMaxAge: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := mater.Prepare(t.Context(), mgr.GetHeader().ID, "", InputIngress{
		Origin: "tui", EventID: "paste-event", Kind: AttachmentFile, FilenameHint: "draft.txt",
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(strings.NewReader("draft"))}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := session.ListInputResourceEvents(t.Context(), root, mgr.GetHeader().ID)
	if err != nil || len(events) != 1 || events[0].EventType != "input_resource_prepared" {
		t.Fatalf("prepared events = %#v, %v", events, err)
	}
	path := filepath.Join(workDir, filepath.FromSlash(record.RelativePath))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("draft path missing: %v", err)
	}
	if err := mater.Discard(t.Context(), mgr.GetHeader().ID, record.ID); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("discarded path error = %v", err)
	}
	events, err = session.ListInputResourceEvents(t.Context(), root, mgr.GetHeader().ID)
	if err != nil || len(events) != 2 || events[1].EventType != "input_resource_deleted" {
		t.Fatalf("discard events = %#v, %v", events, err)
	}

	old, err := mater.Prepare(t.Context(), mgr.GetHeader().ID, "", InputIngress{
		Origin: "webui", EventID: "old-draft", Kind: AttachmentFile, FilenameHint: "old.txt",
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(strings.NewReader("old"))}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.WriteRootDatabase(t.Context(), root, func(tx *dao.Tx) error {
		_, err := tx.Exec(`UPDATE input_resources SET created_at = ? WHERE id = ?`, time.Now().UTC().Add(-2*time.Hour).Format(time.RFC3339Nano), old.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := mater.Cleanup(t.Context(), mgr.GetHeader().ID, time.Now().UTC())
	if err != nil || removed != 1 {
		t.Fatalf("Cleanup = %d, %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(workDir, filepath.FromSlash(old.RelativePath))); !os.IsNotExist(err) {
		t.Fatalf("cleaned path error = %v", err)
	}
}

func TestInputResourcesBindAtomicallyWithRunAdmission(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	mater, err := NewInputMaterializer(root, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatal(err)
	}
	record, err := mater.Prepare(t.Context(), mgr.GetHeader().ID, "run-before-admission", InputIngress{
		Origin: "test", EventID: "admission-event", Kind: AttachmentFile, FilenameHint: "input.txt",
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(strings.NewReader("admission input"))}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.RunID != "" || record.Status != "prepared" {
		t.Fatalf("prepared resource prematurely owned: %#v", record)
	}
	started := time.Now()
	intent := ExecutionIntent{ID: "intent-input-admission", SessionID: mgr.GetHeader().ID, Source: "test", CreatedAt: started}
	userMessage := provider.NewUserMessage("inspect the admitted input")
	if _, err := session.CreateExecutionIntentAndSessionRunEventWithTurn(root, intent, session.SessionRun{
		ID: "run-input-admission", SessionID: intent.SessionID, IntentID: intent.ID, Source: "test", Status: "running", StartedAt: started,
		InputResourceIDs: []string{record.ID}, UserEntryID: session.RunUserEntryID("run-input-admission"), UserMessage: &userMessage,
	}, session.SessionRunEvent{SessionID: intent.SessionID, RunID: "run-input-admission", EventType: "started", Source: "test", Status: "running", Timestamp: started}, session.ConversationTurn{
		ID: "turn-input-admission", SessionID: intent.SessionID, IntentID: intent.ID, RunID: "run-input-admission", Attempt: 1, StartedAt: started,
	}); err != nil {
		t.Fatalf("atomic input admission: %v", err)
	}
	var runID, status, turnEntryID, userEntryID, userParentID string
	if err := session.QueryRootDatabase(root, func(db *dao.Database) error {
		if err := db.Bun().QueryRow(`SELECT run_id, status FROM input_resources WHERE id = ?`, record.ID).Scan(&runID, &status); err != nil {
			return err
		}
		if err := db.Bun().QueryRow(`SELECT id FROM entries WHERE session_id = ? AND type = 'turn_start' ORDER BY seq DESC LIMIT 1`, intent.SessionID).Scan(&turnEntryID); err != nil {
			return err
		}
		return db.Bun().QueryRow(`SELECT id, parent_id FROM entries WHERE session_id = ? AND type = 'message' ORDER BY seq DESC LIMIT 1`, intent.SessionID).Scan(&userEntryID, &userParentID)
	}); err != nil {
		t.Fatal(err)
	}
	if runID != "run-input-admission" || status != "attached" {
		t.Fatalf("bound resource = run=%q status=%q", runID, status)
	}
	if userEntryID != session.RunUserEntryID("run-input-admission") || userParentID != turnEntryID {
		t.Fatalf("atomic transcript order = turn %q, user %q parent %q", turnEntryID, userEntryID, userParentID)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	replay := mgr.GetReplayState()
	if len(replay.Messages) != 1 || len(replay.EntryIDs) != 1 || replay.EntryIDs[0] != userEntryID || replay.Messages[0].Content != userMessage.Content {
		t.Fatalf("atomic user replay = %#v / %#v", replay.Messages, replay.EntryIDs)
	}
	loaded, err := session.GetSessionRun(root, "run-input-admission")
	if err != nil || loaded == nil || len(loaded.InputResourceIDs) != 1 || loaded.InputResourceIDs[0] != record.ID {
		t.Fatalf("replayed resource ownership = %#v, err=%v", loaded, err)
	}
	finished := time.Now()
	if err := session.SaveSessionRun(root, session.SessionRun{
		ID: "run-input-admission", SessionID: intent.SessionID, IntentID: intent.ID, Source: "test", Status: "completed",
		StartedAt: started, FinishedAt: &finished,
	}); err != nil {
		t.Fatalf("finish successful admission fixture: %v", err)
	}
	if err := session.EndConversationTurn(root, intent.SessionID, "turn-input-admission", "completed", "end_turn", finished); err != nil {
		t.Fatalf("close successful admission turn: %v", err)
	}

	failedIntent := ExecutionIntent{ID: "intent-input-rollback", SessionID: intent.SessionID, Source: "test", CreatedAt: started}
	failedUserMessage := provider.NewUserMessage("must roll back")
	_, err = session.CreateExecutionIntentAndSessionRunEventWithTurn(root, failedIntent, session.SessionRun{
		ID: "run-input-rollback", SessionID: intent.SessionID, IntentID: failedIntent.ID, Source: "test", Status: "running", StartedAt: started,
		InputResourceIDs: []string{"missing-resource"}, UserEntryID: session.RunUserEntryID("run-input-rollback"), UserMessage: &failedUserMessage,
	}, session.SessionRunEvent{SessionID: intent.SessionID, RunID: "run-input-rollback", EventType: "started", Source: "test", Status: "running", Timestamp: started}, session.ConversationTurn{
		ID: "turn-input-rollback", SessionID: intent.SessionID, IntentID: failedIntent.ID, RunID: "run-input-rollback", Attempt: 1, StartedAt: started,
	})
	if err == nil || !strings.Contains(err.Error(), "does not belong to session") {
		t.Fatalf("missing resource admission error = %v", err)
	}
	var intentCount, runCount, eventCount, entryCount, turnCount int
	if err := session.QueryRootDatabase(root, func(db *dao.Database) error {
		if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM session_execution_intents WHERE id = ?`, failedIntent.ID).Scan(&intentCount); err != nil {
			return err
		}
		if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM session_runs WHERE id = ?`, "run-input-rollback").Scan(&runCount); err != nil {
			return err
		}
		if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM session_run_events WHERE run_id = ?`, "run-input-rollback").Scan(&eventCount); err != nil {
			return err
		}
		if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM entries WHERE session_id = ? AND id = ?`, intent.SessionID, session.RunUserEntryID("run-input-rollback")).Scan(&entryCount); err != nil {
			return err
		}
		return db.Bun().QueryRow(`SELECT COUNT(*) FROM conversation_turns WHERE id = ?`, "turn-input-rollback").Scan(&turnCount)
	}); err != nil {
		t.Fatal(err)
	}
	if intentCount != 0 || runCount != 0 || eventCount != 0 || entryCount != 0 || turnCount != 0 {
		t.Fatalf("failed admission left durable rows: intents=%d runs=%d events=%d entries=%d turns=%d", intentCount, runCount, eventCount, entryCount, turnCount)
	}
}

func TestInputMaterializerRejectsOversizedAndInvalidImage(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	materializer, err := NewInputMaterializer(root, workDir, InputPolicy{MaxImageBytes: 32, MaxFileBytes: 4, MaxImagePixels: 4})
	if err != nil {
		t.Fatal(err)
	}
	_, err = materializer.Prepare(t.Context(), mgr.GetHeader().ID, "run-1", InputIngress{
		Origin: "test", Kind: AttachmentFile, FilenameHint: "large.bin",
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(strings.NewReader("12345"))}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("oversized error = %v", err)
	}
	_, err = materializer.Prepare(t.Context(), mgr.GetHeader().ID, "run-2", InputIngress{
		Origin: "test", Kind: AttachmentImage, FilenameHint: "broken.png",
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(strings.NewReader("not an image"))}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "detected media type") {
		t.Fatalf("invalid image error = %v", err)
	}
	var count int
	if err := session.QueryRootDatabase(root, func(db *dao.Database) error {
		return db.Bun().QueryRow(`SELECT COUNT(*) FROM input_resources`).Scan(&count)
	}); err != nil || count != 0 {
		t.Fatalf("persisted rejected resources = %d, %v", count, err)
	}
}

func TestAcceptProviderAttachmentMaterializesGeneratedArtifact(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	service, err := NewAttachmentService(root, DefaultAttachmentPolicy())
	if err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{ID: mgr.GetHeader().ID, WorkDir: workDir, Manager: mgr, Attachments: service}
	record, err := runtime.AcceptProviderAttachment(t.Context(), "run-output", attachmentResolverProvider{}, provider.Attachment{
		Kind: "file", Name: "report.txt", MediaType: "text/plain", ProviderRef: "file_output_1",
	})
	if err != nil {
		t.Fatalf("AcceptProviderAttachment: %v", err)
	}
	if record.Status != "generated" || record.Origin != "provider:test" || strings.HasPrefix(record.StorageKey, ".mothx/") {
		t.Fatalf("generated artifact = %#v", record)
	}
	if !strings.HasPrefix(record.StorageKey, "artifacts/") {
		t.Fatalf("artifact storage key = %q, want private artifact store", record.StorageKey)
	}
	_, reader, err := service.Open(t.Context(), mgr.GetHeader().ID, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "generated report" {
		t.Fatalf("artifact content = %q, %v", data, err)
	}
}

func TestArtifactOpenRejectsPrivateStoreTampering(t *testing.T) {
	root, _, mgr := inputTestSession(t)
	service, err := NewAttachmentService(root, DefaultAttachmentPolicy())
	if err != nil {
		t.Fatal(err)
	}
	record := publishTestArtifact(t, service, mgr.GetHeader().ID, "run-1", "result.txt", "original")
	path := filepath.Join(root, filepath.FromSlash(record.StorageKey))
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Open(t.Context(), mgr.GetHeader().ID, record.ID); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("tampered artifact error = %v", err)
	}
}

func TestArtifactCleanupExpiresPrivateContent(t *testing.T) {
	root, _, mgr := inputTestSession(t)
	service, err := NewAttachmentService(root, AttachmentPolicy{MaxImageBytes: 1 << 20, MaxFileBytes: 1 << 20, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	record := publishTestArtifact(t, service, mgr.GetHeader().ID, "run-1", "expired.txt", "expired")
	if err := session.WriteRootDatabase(t.Context(), root, func(tx *dao.Tx) error {
		_, err := tx.Exec(`UPDATE session_attachments SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), record.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	count, err := service.CleanupExpired(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("CleanupExpired = %d, %v", count, err)
	}
	if _, _, err := service.Open(t.Context(), mgr.GetHeader().ID, record.ID); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("Open expired artifact error = %v", err)
	}
}

func TestPublishArtifactCopiesWorkDirectoryFileIntoRuntimeStorage(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	if err := os.WriteFile(filepath.Join(workDir, "report.txt"), []byte("first version"), 0600); err != nil {
		t.Fatal(err)
	}
	service, err := NewAttachmentService(root, DefaultAttachmentPolicy())
	if err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{ID: mgr.GetHeader().ID, WorkDir: workDir, Attachments: service, Registry: tools.NewRegistry(workDir, nil), ArtifactEnabled: true}
	collector, err := runtime.BeginArtifactCollection("run-output")
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := runtime.Registry.Get("publish_artifact")
	if !ok {
		t.Fatal("publish_artifact was not registered")
	}
	if _, err := tool.Execute(t.Context(), map[string]any{"path": "report.txt"}); err != nil {
		t.Fatal(err)
	}
	items := collector.Artifacts()
	if len(items) != 1 || items[0].Status != "generated" {
		t.Fatalf("artifacts = %#v", items)
	}
	if err := os.WriteFile(filepath.Join(workDir, "report.txt"), []byte("mutated"), 0600); err != nil {
		t.Fatal(err)
	}
	_, reader, err := service.Open(t.Context(), mgr.GetHeader().ID, items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(data) != "first version" {
		t.Fatalf("published content = %q, read=%v close=%v", data, readErr, closeErr)
	}
	collector.Close()
	if _, ok := runtime.Registry.Get("publish_artifact"); ok {
		t.Fatal("publish_artifact remained registered")
	}
}

type attachmentResolverProvider struct{}

func (attachmentResolverProvider) Name() string                    { return "test" }
func (attachmentResolverProvider) API() string                     { return "test" }
func (attachmentResolverProvider) Models() []*provider.Model       { return nil }
func (attachmentResolverProvider) GetModel(string) *provider.Model { return nil }
func (attachmentResolverProvider) Chat(context.Context, provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch
}
func (attachmentResolverProvider) ResolveAttachment(context.Context, string) (provider.AttachmentContent, error) {
	return provider.AttachmentContent{Data: []byte("generated report"), Filename: "report.txt", MediaType: "text/plain"}, nil
}

func inputTestSession(t *testing.T) (string, string, *session.Manager) {
	t.Helper()
	root := t.TempDir()
	workDir := t.TempDir()
	mgr := session.New(workDir, root)
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })
	return root, workDir, mgr
}

func publishTestArtifact(t *testing.T, service *AttachmentService, sessionID, runID, filename, content string) SessionAttachment {
	t.Helper()
	record, err := service.acceptArtifact(t.Context(), sessionID, runID, artifactIngress{
		Origin: "test", Kind: AttachmentFile, Filename: filename, MediaType: "text/plain", SizeHint: int64(len(content)),
		Open: func(context.Context) (artifactStream, error) {
			return artifactStream{Reader: io.NopCloser(strings.NewReader(content))}, nil
		},
	})
	if err != nil {
		t.Fatalf("publish test artifact: %v", err)
	}
	if err := service.SetStatus(t.Context(), sessionID, record.ID, "generated"); err != nil {
		t.Fatalf("mark generated: %v", err)
	}
	record.Status = "generated"
	return record
}

func onePixelPNG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl8P6sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// wavPayload returns a minimal RIFF/WAVE header that content sniffing detects
// as audio/wave.
func wavPayload() []byte { return []byte("RIFF\x24\x00\x00\x00WAVEfmt ") }

// mp4Payload returns a minimal MP4 signature for content sniffing.
func mp4Payload() []byte { return []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00") }

func prepareTestMedia(t *testing.T, materializer *InputMaterializer, sessionID, runID, origin string, kind AttachmentKind, filename, hint string, payload []byte) InputResource {
	t.Helper()
	record, err := materializer.Prepare(t.Context(), sessionID, runID, InputIngress{
		Origin: origin, ItemIndex: 0, Kind: kind, FilenameHint: filename, MediaTypeHint: hint,
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(bytes.NewReader(payload)), MediaType: hint}, nil
		},
	})
	if err != nil {
		t.Fatalf("Prepare %s: %v", kind, err)
	}
	return record
}

func TestBuildUserMessageInlinesAudioVideoForCapableModel(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	materializer, err := NewInputMaterializer(root, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatal(err)
	}
	sessionID := mgr.GetHeader().ID
	audio := prepareTestMedia(t, materializer, sessionID, "run-media", "cli", AttachmentAudio, "clip.bin", "audio/wav", wavPayload())
	video := prepareTestMedia(t, materializer, sessionID, "run-media", "cli", AttachmentVideo, "scene.bin", "video/mp4", mp4Payload())
	runtime := &SessionRuntime{
		ID: sessionID, WorkDir: workDir, Inputs: materializer,
		Model: &provider.Model{ID: "omni", Input: []string{"text", "image", "audio", "video"}},
	}
	msg, err := runtime.BuildUserMessage(t.Context(), InputSubmission{Text: "analyze", Resources: []PreparedInput{audio.Prepared(), video.Prepared()}})
	if err != nil {
		t.Fatalf("BuildUserMessage: %v", err)
	}
	if len(msg.Contents) != 3 || msg.Contents[0].Type != "text" || msg.Contents[1].Type != "audio" || msg.Contents[2].Type != "video" {
		t.Fatalf("message contents = %#v, want text+audio+video blocks", msg.Contents)
	}
	audioBlock := msg.Contents[1].Audio
	if audioBlock == nil || audioBlock.MimeType != "audio/wave" || audioBlock.Format != "wav" {
		t.Fatalf("audio block = %#v", audioBlock)
	}
	decoded, err := base64.StdEncoding.DecodeString(audioBlock.Data)
	if err != nil || !bytes.Equal(decoded, wavPayload()) {
		t.Fatalf("audio payload = %q, %v", decoded, err)
	}
	videoBlock := msg.Contents[2].Video
	if videoBlock == nil || videoBlock.MimeType != "video/mp4" {
		t.Fatalf("video block = %#v", videoBlock)
	}
	if !strings.Contains(msg.Content, "delivered: inline audio content attached") ||
		!strings.Contains(msg.Content, "delivered: inline video content attached") ||
		!strings.Contains(msg.Content, audio.RelativePath) {
		t.Fatalf("manifest = %q", msg.Content)
	}
}

func TestBuildUserMessageKeepsMediaPathOnlyForIncapableModel(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	materializer, err := NewInputMaterializer(root, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatal(err)
	}
	sessionID := mgr.GetHeader().ID
	audio := prepareTestMedia(t, materializer, sessionID, "run-media", "webui", AttachmentAudio, "clip.bin", "audio/wav", wavPayload())
	for _, model := range []*provider.Model{{ID: "text-only", Input: []string{"text"}}, nil} {
		runtime := &SessionRuntime{ID: sessionID, WorkDir: workDir, Inputs: materializer, Model: model}
		msg, err := runtime.BuildUserMessage(t.Context(), InputSubmission{Resources: []PreparedInput{audio.Prepared()}})
		if err != nil {
			t.Fatalf("BuildUserMessage: %v", err)
		}
		if len(msg.Contents) != 0 {
			t.Fatalf("message contents = %#v, want path-only manifest", msg.Contents)
		}
		if !strings.Contains(msg.Content, "the selected model does not support audio input") || !strings.Contains(msg.Content, audio.RelativePath) {
			t.Fatalf("manifest = %q", msg.Content)
		}
	}
}

func TestBuildUserMessageSkipsInlineMediaOverLimit(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	policy := DefaultInputPolicy()
	policy.MaxInlineMediaBytes = 4
	materializer, err := NewInputMaterializer(root, workDir, policy)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := mgr.GetHeader().ID
	audio := prepareTestMedia(t, materializer, sessionID, "run-media", "cli", AttachmentAudio, "clip.bin", "audio/wav", wavPayload())
	runtime := &SessionRuntime{
		ID: sessionID, WorkDir: workDir, Inputs: materializer,
		Model: &provider.Model{ID: "omni", Input: []string{"text", "audio"}},
	}
	msg, err := runtime.BuildUserMessage(t.Context(), InputSubmission{Resources: []PreparedInput{audio.Prepared()}})
	if err != nil {
		t.Fatalf("BuildUserMessage: %v", err)
	}
	if len(msg.Contents) != 0 {
		t.Fatalf("message contents = %#v, want path-only manifest above the inline limit", msg.Contents)
	}
	if !strings.Contains(msg.Content, "above the 4-byte inline limit") {
		t.Fatalf("manifest = %q", msg.Content)
	}
}

func TestInputKindNormalizationAndMediaValidation(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	materializer, err := NewInputMaterializer(root, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatal(err)
	}
	sessionID := mgr.GetHeader().ID

	// A generic file input carrying an audio payload is normalized to the
	// canonical media kind so every adapter shares one intake rule.
	record := prepareTestMedia(t, materializer, sessionID, "run-kind", "webui", AttachmentFile, "voice-note.bin", "", wavPayload())
	if record.Kind != AttachmentAudio || record.MediaType != "audio/wave" {
		t.Fatalf("normalized record = %#v, want audio kind", record)
	}

	// Explicitly declared media kinds must carry a matching media type.
	_, err = materializer.Prepare(t.Context(), sessionID, "run-kind", InputIngress{
		Origin: "acp", Kind: AttachmentAudio, FilenameHint: "notes.txt",
		Open: func(context.Context) (InputStream, error) {
			return InputStream{Reader: io.NopCloser(strings.NewReader("plain text"))}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "audio input has detected media type") {
		t.Fatalf("Prepare mismatched audio = %v, want media type rejection", err)
	}
}

// TestInputMediaContractAcrossEntryPoints proves the five user-facing entries
// (TUI, CLI, WebUI/API, ACP, Channel) converge on one canonical Runtime
// outcome: adapters differ only in origin metadata and transport decoding,
// while resource records and provider media blocks stay identical.
func TestInputMediaContractAcrossEntryPoints(t *testing.T) {
	entries := []struct{ name, origin string }{
		{"tui", "tui"},
		{"cli", "cli"},
		{"webui-api", "api:chat-completions"},
		{"acp", "acp"},
		{"channel", "channel:wechat"},
	}
	type outcome struct {
		kind      AttachmentKind
		mediaType string
		bytes     int64
		blockType string
		format    string
		data      string
	}
	var want *outcome
	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			root, workDir, mgr := inputTestSession(t)
			materializer, err := NewInputMaterializer(root, workDir, DefaultInputPolicy())
			if err != nil {
				t.Fatal(err)
			}
			sessionID := mgr.GetHeader().ID
			record := prepareTestMedia(t, materializer, sessionID, "run-contract", entry.origin, AttachmentAudio, "clip.bin", "audio/wav", wavPayload())
			runtime := &SessionRuntime{
				ID: sessionID, WorkDir: workDir, Inputs: materializer,
				Model: &provider.Model{ID: "omni", Input: []string{"text", "audio"}},
			}
			msg, err := runtime.BuildUserMessage(t.Context(), InputSubmission{Text: "transcribe", Resources: []PreparedInput{record.Prepared()}})
			if err != nil {
				t.Fatalf("BuildUserMessage: %v", err)
			}
			if len(msg.Contents) != 2 || msg.Contents[1].Type != "audio" || msg.Contents[1].Audio == nil {
				t.Fatalf("message contents = %#v, want text+audio blocks", msg.Contents)
			}
			block := msg.Contents[1].Audio
			got := outcome{
				kind: record.Kind, mediaType: record.MediaType, bytes: record.Bytes,
				blockType: msg.Contents[1].Type, format: block.Format, data: block.Data,
			}
			if want == nil {
				want = &got
				return
			}
			if got != *want {
				t.Fatalf("entry outcome = %#v, want %#v", got, *want)
			}
		})
	}
}

package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/oschina/mothx/internal/session"
)

func TestResolveSubagentAttachmentsUsesRuntimeOwnedInput(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("acp-attachment"); err != nil {
		t.Fatal(err)
	}
	inputs, err := NewInputMaterializer(sessionDir, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{ID: manager.GetHeader().ID, WorkDir: workDir, Inputs: inputs}
	record := prepareTestMedia(t, inputs, runtime.ID, "run-attachment", "test",
		AttachmentFile, "notes.txt", "text/plain", []byte("Runtime-owned notes"))

	attachments, err := (ACPChildBackend{Runtime: runtime}).resolveSubagentAttachments(context.Background(), []string{record.ID})
	if err != nil {
		t.Fatalf("resolve attachments: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments = %#v", attachments)
	}
	got := attachments[0]
	if got.ID != record.ID || got.Filename != "notes.txt" || got.MediaType != "text/plain; charset=utf-8" ||
		string(got.Data) != "Runtime-owned notes" || got.RelativePath != record.RelativePath {
		t.Fatalf("resolved attachment = %#v", got)
	}
}

func TestInputMaterializerReadRejectsSymlinkedResourceDirectory(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("acp-attachment-symlink"); err != nil {
		t.Fatal(err)
	}
	inputs, err := NewInputMaterializer(sessionDir, workDir, DefaultInputPolicy())
	if err != nil {
		t.Fatal(err)
	}
	record := prepareTestMedia(t, inputs, manager.GetHeader().ID, "run-attachment", "test",
		AttachmentFile, "notes.txt", "text/plain", []byte("Runtime-owned notes"))

	resourceDir := filepath.Dir(filepath.Join(workDir, filepath.FromSlash(record.RelativePath)))
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, record.Filename), []byte("Runtime-owned notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(resourceDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, resourceDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := inputs.Read(context.Background(), manager.GetHeader().ID, record.ID); err == nil {
		t.Fatal("Read accepted an input resource redirected outside the Runtime input root")
	}
}

package session

import (
	"context"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

func TestListGeneratedArtifactsFiltersStatusAndSessionInCreationOrder(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("session-artifacts"); err != nil {
		t.Fatal(err)
	}
	other := New(t.TempDir(), sessionDir)
	if err := other.InitWithID("session-artifacts-other"); err != nil {
		t.Fatal(err)
	}
	sessionID := mgr.GetHeader().ID
	now := time.Now().UTC()
	expires := now.Add(time.Hour).Format(time.RFC3339Nano)
	records := []*dao.AttachmentRecord{
		{
			ID: "att-later", SessionID: sessionID, RunID: "run-2", Origin: "tool:publish_artifact",
			Kind: "file", Filename: "later.txt", MediaType: "text/plain", Bytes: 5, SHA256: "sum-later",
			StorageKey: "artifacts/att-later/content", Status: "generated",
			CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano), ExpiresAt: expires, Metadata: "{}",
		},
		{
			ID: "att-first", SessionID: sessionID, RunID: "run-1", Origin: "tool:publish_artifact",
			Kind: "image", Filename: "first.png", MediaType: "image/png", Bytes: 3, SHA256: "sum-first",
			StorageKey: "artifacts/att-first/content", Status: "generated",
			CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: expires, Metadata: "{}",
		},
		{
			ID: "att-input", SessionID: sessionID, RunID: "run-1", Origin: "acp",
			Kind: "file", Filename: "input.txt", MediaType: "text/plain", Bytes: 2, SHA256: "sum-input",
			StorageKey: "artifacts/att-input/content", Status: "accepted",
			CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: expires, Metadata: "{}",
		},
		{
			ID: "att-foreign", SessionID: other.GetHeader().ID, RunID: "run-9", Origin: "tool:publish_artifact",
			Kind: "file", Filename: "foreign.txt", MediaType: "text/plain", Bytes: 4, SHA256: "sum-foreign",
			StorageKey: "artifacts/att-foreign/content", Status: "generated",
			CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: expires, Metadata: "{}",
		},
	}
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		for _, record := range records {
			if err := dao.NewAttachmentDAO(nil).Insert(context.Background(), tx, record); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed attachments: %v", err)
	}

	artifacts, err := ListGeneratedArtifacts(context.Background(), sessionDir, sessionID)
	if err != nil {
		t.Fatalf("ListGeneratedArtifacts: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifacts = %#v, want only the two generated rows of this session", artifacts)
	}
	if artifacts[0].ID != "att-first" || artifacts[1].ID != "att-later" {
		t.Fatalf("artifact order = %s, %s, want durable creation order", artifacts[0].ID, artifacts[1].ID)
	}
	first := artifacts[0]
	if first.SessionID != sessionID || first.RunID != "run-1" || first.Kind != "image" || first.Filename != "first.png" ||
		first.MediaType != "image/png" || first.Bytes != 3 || first.Status != "generated" || first.Origin != "tool:publish_artifact" {
		t.Fatalf("artifact projection = %#v", first)
	}
	if first.CreatedAt.IsZero() {
		t.Fatal("artifact created timestamp was not parsed")
	}

	empty, err := ListGeneratedArtifacts(context.Background(), sessionDir, "")
	if err != nil || empty != nil {
		t.Fatalf("empty session listing = %#v, %v", empty, err)
	}
	missing, err := ListGeneratedArtifacts(context.Background(), sessionDir, "session-without-artifacts")
	if err != nil || len(missing) != 0 {
		t.Fatalf("unknown session listing = %#v, %v", missing, err)
	}
}

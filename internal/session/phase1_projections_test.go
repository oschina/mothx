package session

import (
	"context"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

func TestListLatestSessionRunsProjectsNewestRunPerSession(t *testing.T) {
	sessionDir := t.TempDir()
	first := New(t.TempDir(), sessionDir)
	if err := first.InitWithID("session-latest-a"); err != nil {
		t.Fatal(err)
	}
	second := New(t.TempDir(), sessionDir)
	if err := second.InitWithID("session-latest-b"); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-2 * time.Hour).UTC()
	newer := time.Now().Add(-time.Hour).UTC()
	if err := CreateSessionRun(sessionDir, SessionRun{ID: "run-a-old", SessionID: "session-latest-a", Status: "completed", StartedAt: older, FinishedAt: &older}); err != nil {
		t.Fatal(err)
	}
	if err := CreateSessionRun(sessionDir, SessionRun{ID: "run-a-new", SessionID: "session-latest-a", Status: "failed", StartedAt: newer}); err != nil {
		t.Fatal(err)
	}
	if err := CreateSessionRun(sessionDir, SessionRun{ID: "run-b", SessionID: "session-latest-b", Status: "running", StartedAt: newer}); err != nil {
		t.Fatal(err)
	}

	latest, err := ListLatestSessionRuns(context.Background(), sessionDir, []string{"session-latest-a", "session-latest-b", "session-missing"})
	if err != nil {
		t.Fatalf("ListLatestSessionRuns: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("latest = %#v, want two sessions", latest)
	}
	if latest["session-latest-a"].ID != "run-a-new" || latest["session-latest-a"].Status != "failed" {
		t.Fatalf("session-a projection = %#v, want the newest run", latest["session-latest-a"])
	}
	if latest["session-latest-b"].ID != "run-b" || latest["session-latest-b"].Status != "running" {
		t.Fatalf("session-b projection = %#v, want run-b", latest["session-latest-b"])
	}
	if _, ok := latest["session-missing"]; ok {
		t.Fatal("sessions without runs must be absent")
	}

	empty, err := ListLatestSessionRuns(context.Background(), sessionDir, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty input = %#v, %v, want an empty projection", empty, err)
	}

	// A session directory without any database degrades to an empty result.
	missingDir, err := ListLatestSessionRuns(context.Background(), t.TempDir(), []string{"session-latest-a"})
	if err != nil || len(missingDir) != 0 {
		t.Fatalf("missing database = %#v, %v, want an empty projection", missingDir, err)
	}
}

func TestListSessionMetadataBatchAndProjectCounts(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("session-meta-a"); err != nil {
		t.Fatal(err)
	}
	other := New(t.TempDir(), sessionDir)
	if err := other.InitWithID("session-meta-b"); err != nil {
		t.Fatal(err)
	}
	project, err := CreateProject(sessionDir, "Phase1")
	if err != nil {
		t.Fatal(err)
	}
	if err := SetSessionMetadata(sessionDir, "session-meta-a", SessionMetadata{ProjectID: project.ID, Pinned: true}); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionMetadata(sessionDir, "session-meta-b", SessionMetadata{Pinned: true}); err != nil {
		t.Fatal(err)
	}

	metadata, err := ListSessionMetadata(sessionDir, []string{"session-meta-a", "session-meta-b", "session-missing"})
	if err != nil {
		t.Fatalf("ListSessionMetadata: %v", err)
	}
	if len(metadata) != 2 {
		t.Fatalf("metadata = %#v, want two sessions", metadata)
	}
	if metadata["session-meta-a"].ProjectID != project.ID || !metadata["session-meta-a"].Pinned {
		t.Fatalf("session-a metadata = %#v", metadata["session-meta-a"])
	}
	if metadata["session-meta-a"].UpdatedAt.IsZero() {
		t.Fatal("metadata UpdatedAt was not projected")
	}
	if _, ok := metadata["session-missing"]; ok {
		t.Fatal("sessions without metadata rows must be absent")
	}

	counts, err := ProjectSessionCounts(sessionDir)
	if err != nil {
		t.Fatalf("ProjectSessionCounts: %v", err)
	}
	if counts[project.ID] != 1 {
		t.Fatalf("project counts = %#v, want one session", counts)
	}

	// GetSessionMetadata now also projects the persisted revision time.
	single, err := GetSessionMetadata(sessionDir, "session-meta-a")
	if err != nil {
		t.Fatal(err)
	}
	if single.UpdatedAt.IsZero() || single.ProjectID != project.ID || !single.Pinned {
		t.Fatalf("single metadata = %#v", single)
	}
}

func TestDeleteProjectClearsSessionAssignments(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("session-project-delete"); err != nil {
		t.Fatal(err)
	}
	project, err := CreateProject(sessionDir, "Temporary")
	if err != nil {
		t.Fatal(err)
	}
	if err := SetSessionMetadata(sessionDir, "session-project-delete", SessionMetadata{ProjectID: project.ID, Pinned: true}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteProject(sessionDir, project.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	projects, err := ListProjects(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("projects after delete = %#v, want empty", projects)
	}
	metadata, err := GetSessionMetadata(sessionDir, "session-project-delete")
	if err != nil {
		t.Fatal(err)
	}
	// The declared ON DELETE SET NULL semantics must hold even without SQLite
	// foreign-key enforcement: the assignment is cleared, the pin survives.
	if metadata.ProjectID != "" {
		t.Fatalf("metadata after project delete = %#v, want a cleared project assignment", metadata)
	}
	if !metadata.Pinned {
		t.Fatalf("metadata after project delete = %#v, want the pin preserved", metadata)
	}
}

func TestSetSessionMetadataRejectsMissingReferences(t *testing.T) {
	sessionDir := t.TempDir()
	project, err := CreateProject(sessionDir, "Existing")
	if err != nil {
		t.Fatal(err)
	}
	if err := SetSessionMetadata(sessionDir, "missing-session", SessionMetadata{ProjectID: project.ID}); err == nil || err.Error() != "session not found" {
		t.Fatalf("missing session error = %v, want session not found", err)
	}
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("metadata-reference-check"); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionMetadata(sessionDir, "metadata-reference-check", SessionMetadata{ProjectID: "missing-project"}); err == nil || err.Error() != "project not found" {
		t.Fatalf("missing project error = %v, want project not found", err)
	}
}

func TestListSessionAttachmentsOptionalStatusFilter(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("session-attachments-list"); err != nil {
		t.Fatal(err)
	}
	sessionID := mgr.GetHeader().ID
	now := time.Now().UTC()
	expires := now.Add(time.Hour).Format(time.RFC3339Nano)
	records := []*dao.AttachmentRecord{
		{
			ID: "att-input", SessionID: sessionID, RunID: "run-1", Origin: "acp",
			Kind: "image", Filename: "input.png", MediaType: "image/png", Bytes: 7, SHA256: "sum-input",
			StorageKey: "artifacts/att-input/content", Status: "accepted",
			CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: expires, Metadata: "{}",
		},
		{
			ID: "att-generated", SessionID: sessionID, RunID: "run-1", Origin: "tool:publish_artifact",
			Kind: "file", Filename: "out.txt", MediaType: "text/plain", Bytes: 9, SHA256: "sum-generated",
			StorageKey: "artifacts/att-generated/content", Status: "generated",
			CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano), ExpiresAt: expires, Metadata: "{}",
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

	all, err := ListSessionAttachments(context.Background(), sessionDir, sessionID, "")
	if err != nil {
		t.Fatalf("ListSessionAttachments: %v", err)
	}
	if len(all) != 2 || all[0].ID != "att-input" || all[1].ID != "att-generated" {
		t.Fatalf("all attachments = %#v, want creation order", all)
	}
	if all[1].RunID != "run-1" || all[1].Bytes != 9 || all[1].CreatedAt.IsZero() {
		t.Fatalf("attachment projection = %#v, want complete metadata fields", all[1])
	}
	generated, err := ListSessionAttachments(context.Background(), sessionDir, sessionID, "generated")
	if err != nil {
		t.Fatal(err)
	}
	if len(generated) != 1 || generated[0].ID != "att-generated" {
		t.Fatalf("generated attachments = %#v", generated)
	}
	accepted, err := ListSessionAttachments(context.Background(), sessionDir, sessionID, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted) != 1 || accepted[0].ID != "att-input" {
		t.Fatalf("accepted attachments = %#v", accepted)
	}
	foreign, err := ListSessionAttachments(context.Background(), sessionDir, "session-unknown", "")
	if err != nil || len(foreign) != 0 {
		t.Fatalf("unknown session attachments = %#v, %v, want empty", foreign, err)
	}
}

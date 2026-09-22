package dao_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/session"
)

func TestRunDAOLatestRunBySessionsPicksNewestPerSession(t *testing.T) {
	root := t.TempDir()
	database, err := session.OpenBunDatabase(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })

	runDAO := dao.NewRunDAO(database.Bun())
	ctx := context.Background()
	seed := []*dao.SessionRunRecord{
		{ID: "run-a1", SessionID: "session-a", Status: "completed", StartedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:01Z"},
		{ID: "run-a2", SessionID: "session-a", Status: "failed", StartedAt: "2026-01-02T00:00:00Z", UpdatedAt: "2026-01-02T00:00:01Z"},
		{ID: "run-b1", SessionID: "session-b", Status: "running", StartedAt: "2026-01-03T00:00:00Z", UpdatedAt: "2026-01-03T00:00:00Z"},
		{ID: "run-c1", SessionID: "session-c", Status: "completed", StartedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z"},
	}
	for _, record := range seed {
		if err := runDAO.InsertRun(ctx, database.Bun(), record); err != nil {
			t.Fatal(err)
		}
	}

	latest, err := runDAO.LatestRunBySessions(ctx, []string{"session-a", "session-b", "session-missing"})
	if err != nil {
		t.Fatalf("LatestRunBySessions: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("latest = %#v, want exactly session-a and session-b", latest)
	}
	if latest["session-a"].ID != "run-a2" || latest["session-a"].Status != "failed" {
		t.Fatalf("session-a latest = %#v, want the newest run run-a2", latest["session-a"])
	}
	if latest["session-b"].ID != "run-b1" {
		t.Fatalf("session-b latest = %#v, want run-b1", latest["session-b"])
	}
	if _, ok := latest["session-missing"]; ok {
		t.Fatal("sessions without runs must be absent from the projection")
	}

	empty, err := runDAO.LatestRunBySessions(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty input = %#v, %v, want an empty projection", empty, err)
	}
}

func TestProjectDAOMetadataBatchCountsAndClear(t *testing.T) {
	root := t.TempDir()
	database, err := session.OpenBunDatabase(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })

	projectDAO := dao.NewProjectDAO(database.Bun())
	ctx := context.Background()
	if err := projectDAO.Insert(ctx, &dao.ProjectRecord{ID: "project-1", Name: "One", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	projectID := "project-1"
	if err := projectDAO.UpsertMetadata(ctx, &dao.SessionMetadataRecord{SessionID: "session-a", ProjectID: &projectID, Pinned: 1, UpdatedAt: "2026-01-01T00:00:01Z"}); err != nil {
		t.Fatal(err)
	}
	if err := projectDAO.UpsertMetadata(ctx, &dao.SessionMetadataRecord{SessionID: "session-b", ProjectID: &projectID, Pinned: 0, UpdatedAt: "2026-01-01T00:00:02Z"}); err != nil {
		t.Fatal(err)
	}
	if err := projectDAO.UpsertMetadata(ctx, &dao.SessionMetadataRecord{SessionID: "session-c", Pinned: 1, UpdatedAt: "2026-01-01T00:00:03Z"}); err != nil {
		t.Fatal(err)
	}

	records, err := projectDAO.MetadataForSessions(ctx, []string{"session-a", "session-c", "session-missing"})
	if err != nil {
		t.Fatalf("MetadataForSessions: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("metadata records = %#v, want session-a and session-c only", records)
	}
	if records[0].SessionID != "session-a" || records[1].SessionID != "session-c" {
		t.Fatalf("metadata order = %s, %s, want stable session order", records[0].SessionID, records[1].SessionID)
	}
	if records[0].ProjectID == nil || *records[0].ProjectID != "project-1" || records[0].Pinned != 1 {
		t.Fatalf("session-a metadata = %#v", records[0])
	}

	counts, err := projectDAO.SessionCountsByProject(ctx)
	if err != nil {
		t.Fatalf("SessionCountsByProject: %v", err)
	}
	if counts["project-1"] != 2 {
		t.Fatalf("project counts = %#v, want two sessions for project-1", counts)
	}

	if err := projectDAO.ClearMetadataProject(ctx, "project-1"); err != nil {
		t.Fatalf("ClearMetadataProject: %v", err)
	}
	counts, err = projectDAO.SessionCountsByProject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Fatalf("counts after clear = %#v, want empty", counts)
	}
	record, err := projectDAO.Metadata(ctx, "session-a")
	if err != nil || record == nil || record.ProjectID != nil || record.Pinned != 1 {
		t.Fatalf("session-a after clear = %#v, %v, want detached project with pin preserved", record, err)
	}
}

func TestAttachmentDAOListBySessionOptionalStatus(t *testing.T) {
	root := t.TempDir()
	database, err := session.OpenBunDatabase(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })

	attachmentDAO := dao.NewAttachmentDAO(database.Bun())
	ctx := context.Background()
	records := []*dao.AttachmentRecord{
		{ID: "att-2", SessionID: "session-a", RunID: "run-1", Kind: "file", Filename: "second.txt", MediaType: "text/plain", Bytes: 2, Status: "generated", CreatedAt: "2026-01-01T00:00:02Z"},
		{ID: "att-1", SessionID: "session-a", RunID: "run-1", Kind: "image", Filename: "first.png", MediaType: "image/png", Bytes: 1, Status: "accepted", CreatedAt: "2026-01-01T00:00:01Z"},
		{ID: "att-foreign", SessionID: "session-b", RunID: "run-2", Kind: "file", Filename: "foreign.txt", MediaType: "text/plain", Bytes: 3, Status: "generated", CreatedAt: "2026-01-01T00:00:03Z"},
	}
	for _, record := range records {
		if err := attachmentDAO.Insert(ctx, database.Bun(), record); err != nil {
			t.Fatal(err)
		}
	}

	all, err := attachmentDAO.ListBySession(ctx, "session-a", "")
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(all) != 2 || all[0].ID != "att-1" || all[1].ID != "att-2" {
		t.Fatalf("all rows = %#v, want durable creation order of session-a only", all)
	}
	generated, err := attachmentDAO.ListBySession(ctx, "session-a", "generated")
	if err != nil {
		t.Fatal(err)
	}
	if len(generated) != 1 || generated[0].ID != "att-2" {
		t.Fatalf("generated rows = %#v, want att-2 only", generated)
	}
	accepted, err := attachmentDAO.ListBySession(ctx, "session-a", "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted) != 1 || accepted[0].ID != "att-1" {
		t.Fatalf("accepted rows = %#v, want att-1 only", accepted)
	}
	missing, err := attachmentDAO.ListBySession(ctx, "session-missing", "")
	if err != nil || len(missing) != 0 {
		t.Fatalf("unknown session rows = %#v, %v, want empty", missing, err)
	}
}

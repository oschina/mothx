package dao_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/session"
)

func TestCronDAOCRUDAndClaim(t *testing.T) {
	root := t.TempDir()
	database, err := session.OpenBunDatabase(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })

	cronDAO := dao.NewCronDAO(database.Bun())
	record := &dao.CronJobRecord{ID: "cron-test", Name: "test", Prompt: "run", Enabled: true, CreatedAt: "2026-01-01T00:00:00Z"}
	if err := cronDAO.Create(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	loaded, err := cronDAO.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != record.Name || !loaded.Enabled {
		t.Fatalf("loaded record = %#v", loaded)
	}

	claimed, err := cronDAO.ClaimDue(context.Background(), record.ID, "2026-01-02T00:00:00Z", "2025-12-31T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("expected an unstarted enabled job to be claimed")
	}

	record.Name = "updated"
	if err := cronDAO.Update(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if err := cronDAO.Delete(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cronDAO.Get(context.Background(), record.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get after delete error = %v, want sql.ErrNoRows", err)
	}
}

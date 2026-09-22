package dao_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/session"
)

// TestAttachmentDAOListStorageReferencesReturnsEveryRow proves the reference set
// the private-store reconciliation is built from covers every session and every
// lifecycle status: an expired or generated row still claims its content until
// the row itself is gone, because the reconciliation must never see a
// referenced object as an orphan.
func TestAttachmentDAOListStorageReferencesReturnsEveryRow(t *testing.T) {
	db, err := session.OpenRootDB(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })
	ctx := context.Background()
	now := time.Now().UTC()

	rows := []*dao.AttachmentRecord{
		{ID: "0000000000000001", SessionID: "session-a", Kind: "file", StorageKey: "artifacts/0000000000000001/content", Status: "accepted",
			CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano)},
		{ID: "0000000000000002", SessionID: "session-b", Kind: "image", StorageKey: "artifacts/0000000000000002/content", Status: "expired",
			CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339Nano)},
	}
	for _, row := range rows {
		if err := dao.NewAttachmentDAO(nil).Insert(ctx, db.Bun(), row); err != nil {
			t.Fatal(err)
		}
	}

	references, err := dao.NewAttachmentDAO(db.Bun()).ListStorageReferences(ctx, db.Bun())
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != len(rows) {
		t.Fatalf("references = %#v, want both rows regardless of session or status", references)
	}
	byID := map[string]string{}
	for _, reference := range references {
		byID[reference.ID] = reference.StorageKey
	}
	for _, row := range rows {
		if byID[row.ID] != row.StorageKey {
			t.Fatalf("reference for %s = %q, want %q", row.ID, byID[row.ID], row.StorageKey)
		}
	}
}

package openaiapi

import (
	"context"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/session"
)

func insertExportSessionRow(t *testing.T, sessionDir, id, cwd, parent string, created time.Time) {
	t.Helper()
	if err := session.WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionDAO(nil).InsertSession(context.Background(), tx, "sessions", id, cwd, created.Format(time.RFC3339Nano), parent, 1, "", "", 0, 0, "", "")
	}); err != nil {
		t.Fatalf("insert session %q: %v", id, err)
	}
}

func TestExportSessionIDsUsesParentProjectionWithoutOpeningEachSession(t *testing.T) {
	sessionDir := t.TempDir()
	defer session.CloseDatabases()
	root := session.New("/tmp/export-root", sessionDir)
	if err := root.InitWithID("export-root"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	insertExportSessionRow(t, sessionDir, "export-child", "/tmp/export-child", "export-root", now.Add(time.Second))
	insertExportSessionRow(t, sessionDir, "export-grandchild", "/tmp/export-grandchild", "export-child", now.Add(2*time.Second))
	insertExportSessionRow(t, sessionDir, "export-unrelated", "/tmp/export-unrelated", "", now.Add(3*time.Second))

	// The exporter must reach for the lightweight id/parent_session projection.
	// Counting the seam also proves it never falls back to a full detail/preview
	// listing: in that case the hook would not be called at all.
	parentCalls := 0
	previous := listSessionParents
	listSessionParents = func(sessionDir string) ([]session.SessionParent, error) {
		parentCalls++
		return previous(sessionDir)
	}
	defer func() { listSessionParents = previous }()

	srv := &Server{settings: &config.Settings{SessionDir: sessionDir}}
	beforeOpens := session.OpenByIDExactCallCount()
	ids, err := srv.exportSessionIDs("export-root", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"export-root", "export-child", "export-grandchild"}
	if len(ids) != len(want) {
		t.Fatalf("export ids = %#v, want %#v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("export ids = %#v, want %#v", ids, want)
		}
	}
	if parentCalls != 1 {
		t.Fatalf("exportSessionIDs called the parent projection %d times, want 1", parentCalls)
	}
	if opens := session.OpenByIDExactCallCount() - beforeOpens; opens != 1 {
		t.Fatalf("exportSessionIDs opened %d sessions, want only the root existence check", opens)
	}
}

func TestExportSessionIDsWithoutDescendantsReturnsRoot(t *testing.T) {
	sessionDir := t.TempDir()
	defer session.CloseDatabases()
	root := session.New("/tmp/export-only-root", sessionDir)
	if err := root.InitWithID("export-only-root"); err != nil {
		t.Fatal(err)
	}
	srv := &Server{settings: &config.Settings{SessionDir: sessionDir}}
	ids, err := srv.exportSessionIDs("export-only-root", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "export-only-root" {
		t.Fatalf("export ids = %#v", ids)
	}
}

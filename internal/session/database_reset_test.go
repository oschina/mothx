package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/startvibecoding/mothx/internal/dao"
	"github.com/startvibecoding/mothx/internal/provider"
)

func TestResetDatabaseCreatesFreshDatabaseWhenAbsent(t *testing.T) {
	sessionDir := t.TempDir()

	backup, err := ResetDatabase(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDatabases() })
	if backup != "" {
		t.Fatalf("backup = %q, want empty when no database existed", backup)
	}
	if _, err := os.Stat(RootDatabasePath(sessionDir)); err != nil {
		t.Fatalf("fresh database not created: %v", err)
	}
	sessions, err := ListAll(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("fresh database lists %d sessions, want 0", len(sessions))
	}
}

func TestResetDatabaseMovesDatabaseAndPreservesPreviousSessions(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()

	manager := New(workDir, sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	sessionID := manager.GetHeader().ID
	if _, err := manager.AppendMessage(provider.Message{Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDatabases() })

	// A leftover sidecar must travel with the database, not stay behind where
	// the freshly created database could pick it up.
	if err := os.WriteFile(RootDatabasePath(sessionDir)+"-wal", []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}

	backup, err := ResetDatabase(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("expected a backup path")
	}
	if filepath.Dir(backup) != sessionDir {
		t.Fatalf("backup %q is not next to the session database", backup)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup database missing: %v", err)
	}
	if _, err := os.Stat(backup + "-wal"); err != nil {
		t.Fatalf("sidecar was not moved with the database: %v", err)
	}
	if _, err := os.Stat(RootDatabasePath(sessionDir) + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("stale sidecar left next to the fresh database: %v", err)
	}

	// The fresh database is empty...
	sessions, err := ListAll(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("fresh database lists %d sessions, want 0", len(sessions))
	}

	// ...while the backup still holds the previous session.
	previous, err := OpenBunDatabase(backup)
	if err != nil {
		t.Fatal(err)
	}
	records, err := dao.NewSessionDAO(previous.Bun()).List(context.Background(), dao.SessionListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != sessionID {
		t.Fatalf("backup sessions = %#v, want the previous session %q", records, sessionID)
	}
}

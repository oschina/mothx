package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/dao"
	"github.com/startvibecoding/mothx/internal/provider"
)

func TestResetDatabaseCreatesFreshDatabaseWhenAbsent(t *testing.T) {
	sessionDir := t.TempDir()

	report, err := ResetDatabase(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDatabases() })
	if report.DatabaseBackup != "" {
		t.Fatalf("database backup = %q, want empty when no database existed", report.DatabaseBackup)
	}
	if len(report.Archived) != 0 {
		t.Fatalf("archived = %#v, want nothing archived", report.Archived)
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

	report, err := ResetDatabase(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.DatabaseBackup == "" {
		t.Fatal("expected a database backup path")
	}
	if filepath.Dir(report.DatabaseBackup) != sessionDir {
		t.Fatalf("backup %q is not next to the session database", report.DatabaseBackup)
	}
	if _, err := os.Stat(report.DatabaseBackup); err != nil {
		t.Fatalf("backup database missing: %v", err)
	}
	if _, err := os.Stat(report.DatabaseBackup + "-wal"); err != nil {
		t.Fatalf("sidecar was not moved with the database: %v", err)
	}
	if _, err := os.Stat(RootDatabasePath(sessionDir) + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("stale sidecar left next to the fresh database: %v", err)
	}
	if len(report.Archived) != 2 || report.Archived[0].To != report.DatabaseBackup {
		t.Fatalf("archived = %#v, want the database first and then its sidecar", report.Archived)
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
	previous, err := OpenBunDatabase(report.DatabaseBackup)
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

// TestResetDatabaseArchivesOrphanedSidecars pins the case a hand-deleted or
// half-finished reset leaves behind: sidecars with no sessions.db beside them.
// The fresh database must not inherit them, and the operator must be told they
// were moved rather than quietly swallowed by SQLite.
func TestResetDatabaseArchivesOrphanedSidecars(t *testing.T) {
	sessionDir := t.TempDir()
	dbPath := RootDatabasePath(sessionDir)
	for _, suffix := range databaseSidecarSuffixes {
		if err := os.WriteFile(dbPath+suffix, []byte("leftover"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	report, err := ResetDatabase(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDatabases() })
	if report.DatabaseBackup != "" {
		t.Fatalf("database backup = %q, want none: no database existed to archive", report.DatabaseBackup)
	}
	if len(report.Archived) != len(databaseSidecarSuffixes) {
		t.Fatalf("archived = %#v, want every orphaned sidecar moved", report.Archived)
	}
	archivedBySource := map[string]string{}
	for _, archived := range report.Archived {
		archivedBySource[archived.From] = archived.To
	}
	for _, suffix := range databaseSidecarSuffixes {
		if _, err := os.Stat(dbPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("%s survived the reset: %v", dbPath+suffix, err)
		}
		destination, ok := archivedBySource[dbPath+suffix]
		if !ok {
			t.Fatalf("%s was not reported as archived: %#v", dbPath+suffix, report.Archived)
		}
		if _, err := os.Stat(destination); err != nil {
			t.Fatalf("archived sidecar missing at %s: %v", destination, err)
		}
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("fresh database not created: %v", err)
	}
}

// TestMoveDatabaseFilesRollsBackWithTheMainFileFirst drives the rollback branch
// directly: the move is interrupted by an occupied destination, and the restore
// order must never leave a sidecar whose database is missing.
func TestMoveDatabaseFilesRollsBackWithTheMainFileFirst(t *testing.T) {
	sessionDir := t.TempDir()
	dbPath := RootDatabasePath(sessionDir)
	sources := []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
	for _, source := range sources {
		if err := os.WriteFile(source, []byte(filepath.Base(source)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	backupPath := filepath.Join(sessionDir, "sessions.db.pure-test.bak")
	// The -shm destination is an existing directory, so its rename fails on every
	// platform while the first two moves have already succeeded.
	if err := os.Mkdir(backupPath+"-shm", 0700); err != nil {
		t.Fatal(err)
	}

	restored, err := moveDatabaseFiles(dbPath, sources, backupPath)
	if err == nil {
		t.Fatal("expected the move to fail on the occupied sidecar destination")
	}
	if !strings.Contains(err.Error(), dbPath+"-shm") {
		t.Fatalf("error = %v, want the file that could not move", err)
	}
	for _, source := range []string{dbPath, dbPath + "-wal"} {
		if _, statErr := os.Stat(source); statErr != nil {
			t.Fatalf("%s was not restored after the failed move: %v", source, statErr)
		}
	}
	if len(restored) != 2 || restored[0].To != dbPath {
		t.Fatalf("restored = %#v, want the main database restored first", restored)
	}
	if _, statErr := os.Stat(backupPath); !os.IsNotExist(statErr) {
		t.Fatalf("archived database survived the rollback: %v", statErr)
	}
	if _, statErr := os.Stat(backupPath + "-wal"); !os.IsNotExist(statErr) {
		t.Fatalf("archived sidecar survived the rollback: %v", statErr)
	}
}

// TestResetBackupPathAvoidsOccupiedNames keeps the archive naming from selecting
// a name whose sidecar derivatives would collide mid-move.
func TestResetBackupPathAvoidsOccupiedNames(t *testing.T) {
	sessionDir := t.TempDir()
	dbPath := RootDatabasePath(sessionDir)
	stamp := time.Now().UTC().Format("20060102T150405Z")
	occupied := dbPath + ".pure-" + stamp + ".bak"
	if err := os.WriteFile(occupied, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(occupied+"-wal", []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	candidate, err := resetBackupPath(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if candidate == occupied {
		t.Fatal("resetBackupPath returned the occupied name")
	}
	if _, err := os.Stat(candidate); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate %q is not free: %v", candidate, err)
	}
	if !strings.HasPrefix(filepath.Base(candidate), filepath.Base(dbPath)+".pure-") || !strings.HasSuffix(candidate, ".bak") {
		t.Fatalf("candidate %q is not a sibling archive of %s", candidate, dbPath)
	}
}

// TestResetDatabaseReportsWhatItLeftBehind makes the orphan risk explicit: the
// reset moves only sessions.db, so everything else in the directory has to be
// named, including the sizes that stay on disk unreferenced.
func TestResetDatabaseReportsWhatItLeftBehind(t *testing.T) {
	sessionDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sessionDir, "artifacts", "attachment-1"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "artifacts", "attachment-1", "content"), make([]byte, 4096), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "notes.txt"), []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}

	report, err := ResetDatabase(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDatabases() })

	byName := map[string]LeftBehindEntry{}
	for _, entry := range report.LeftBehind {
		byName[entry.Name] = entry
	}
	if len(byName) != 2 {
		t.Fatalf("left behind = %#v, want the artifacts directory and notes.txt", report.LeftBehind)
	}
	artifacts, ok := byName["artifacts"]
	if !ok || !artifacts.Directory || artifacts.Bytes != 4096 {
		t.Fatalf("artifacts entry = %#v, want a directory holding 4096 bytes", artifacts)
	}
	notes, ok := byName["notes.txt"]
	if !ok || notes.Directory || notes.Bytes != int64(len("keep me")) {
		t.Fatalf("notes.txt entry = %#v, want a 7 byte file", notes)
	}
	// The fresh database and its sidecars are part of the reset, not orphans.
	for _, entry := range report.LeftBehind {
		if strings.HasPrefix(entry.Name, filepath.Base(RootDatabasePath(sessionDir))) {
			t.Fatalf("reset reported its own database files as left behind: %#v", entry)
		}
	}
}

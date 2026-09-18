package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/dao"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

func TestPureCommandArchivesAndRecreatesSessionDatabase(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := filepath.Join(t.TempDir(), "sessions")

	manager := session.New(workDir, sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.Message{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := session.CloseDatabases(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })

	// Artifact storage lives beside the database but is not part of it: the
	// command has to say so instead of implying the directory is now empty.
	if err := os.MkdirAll(filepath.Join(sessionDir, "artifacts", "attachment-1"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "artifacts", "attachment-1", "content"), make([]byte, 2048), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := newPureCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--session-dir", sessionDir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Moved previous sessions database to:") {
		t.Fatalf("stdout = %q, want the archive notice", output)
	}
	if !strings.Contains(output, session.RootDatabasePath(sessionDir)) {
		t.Fatalf("stdout = %q, want the fresh database path", output)
	}
	if !strings.Contains(output, "artifacts") || !strings.Contains(output, "2.0 KB") {
		t.Fatalf("stdout = %q, want the un-archived resource directory and its size", output)
	}
	// The reset must not claim that everything it left behind is unreferenced:
	// channel roots and knowledge-base files are authoritative outside sessions.db.
	if !strings.Contains(output, "Left in place by this reset") {
		t.Fatalf("stdout = %q, want the neutral left-behind heading", output)
	}
	if !strings.Contains(output, "Unreferenced attachment storage is reclaimed automatically") {
		t.Fatalf("stdout = %q, want the attachment-specific reclamation note", output)
	}

	sessions, err := session.ListAll(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("fresh database lists %d sessions, want 0", len(sessions))
	}
	backups, err := filepath.Glob(session.RootDatabasePath(sessionDir) + ".pure-*.bak")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("archived databases = %v, want exactly one", backups)
	}
}

func TestPureSessionDirPrefersExplicitFlag(t *testing.T) {
	dir, err := pureSessionDir("~/explicit-sessions")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(dir, "~") {
		t.Fatalf("explicit session dir %q was not home-expanded", dir)
	}
}

func TestPureCommandReportsHowToRecoverFromAMoveFailure(t *testing.T) {
	// A regular file where the session directory should be makes the reset
	// fail, exercising the guidance appended to the error (the Windows case is
	// an open handle blocking the move instead).
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := newPureCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	// The path itself is unreadable for the lease preflight; --force reaches the
	// reset failure branch this test is intended to cover.
	cmd.SetArgs([]string{"--session-dir", filepath.Join(blocker, "sessions"), "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected the reset to fail")
	}
	if !strings.Contains(err.Error(), "stop every other mothx process") {
		t.Fatalf("error = %v, want the recovery hint", err)
	}
	if strings.Contains(err.Error(), "still recoverable") {
		t.Fatalf("error = %v, want no archive claim when nothing moved", err)
	}
}

// TestPureCommandReportsArchivedFilesWhenTheResetFails covers the branch where
// the database has already left the directory and only the fresh one could not
// be created. Reporting "stop every other process" alone would read as a plain
// failure and hide that the data is safe in the archive.
func TestPureCommandReportsArchivedFilesWhenTheResetFails(t *testing.T) {
	sessionDir := t.TempDir()
	archived := filepath.Join(sessionDir, "sessions.db.pure-test.bak")
	stubReset(t, session.ResetReport{
		DatabaseBackup: archived,
		Archived:       []session.ArchivedFile{{From: filepath.Join(sessionDir, "sessions.db"), To: archived}},
	}, errors.New("create fresh sessions database: unable to open database file"))

	cmd := newPureCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--session-dir", sessionDir})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected the reset to fail")
	}
	if !strings.Contains(err.Error(), archived) {
		t.Fatalf("error = %v, want the archive path the database was moved to", err)
	}
	if !strings.Contains(err.Error(), "still recoverable") {
		t.Fatalf("error = %v, want an explicit note that nothing was lost", err)
	}
	if !strings.Contains(err.Error(), "unable to open database file") {
		t.Fatalf("error = %v, want the underlying cause", err)
	}
}

// TestPureCommandRefusesWhileASessionRunIsActive proves the preflight turns the
// "stop every other process" instruction into an enforced refusal, and that
// --force is the documented escape hatch.
func TestPureCommandRefusesWhileASessionRunIsActive(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("busy-session"); err != nil {
		t.Fatal(err)
	}
	insertActiveLease(t, sessionDir, "busy-session", time.Now().Add(time.Minute).Unix())

	cmd := newPureCommand()
	var stderr bytes.Buffer
	cmd.SetOut(io.Discard)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--session-dir", sessionDir})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected the active lease to block the reset")
	}
	if !strings.Contains(err.Error(), "busy-session") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %v, want the holder and the escape hatch", err)
	}
	if _, statErr := os.Stat(session.RootDatabasePath(sessionDir)); statErr != nil {
		t.Fatalf("a refused reset must leave the database in place: %v", statErr)
	}
	if !strings.Contains(stderr.String(), "refusing to reset") {
		t.Fatalf("stderr = %q, want the refusal", stderr.String())
	}

	forced := newPureCommand()
	forced.SetOut(io.Discard)
	forced.SetErr(io.Discard)
	forced.SetArgs([]string{"--session-dir", sessionDir, "--force"})
	if err := forced.Execute(); err != nil {
		t.Fatalf("reset with --force: %v", err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })
}

// TestPureCommandRefusesAnExpiredButUnreleasedLease preserves long-running
// ownership through a transient heartbeat outage. Expiry alone does not fence
// the owner out, so the reset must require the same explicit --force escape
// hatch as for a freshly renewed lease.
func TestPureCommandRefusesAnExpiredButUnreleasedLease(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("stalled-session"); err != nil {
		t.Fatal(err)
	}
	insertActiveLease(t, sessionDir, "stalled-session", time.Now().Add(-time.Minute).Unix())

	cmd := newPureCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--session-dir", sessionDir})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("an expired but unreleased lease must block the reset")
	}
	if !strings.Contains(err.Error(), "stalled-session") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %v, want the held lease and forced-reset guidance", err)
	}
	if _, statErr := os.Stat(session.RootDatabasePath(sessionDir)); statErr != nil {
		t.Fatalf("a refused reset must leave the database in place: %v", statErr)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })
}

// TestPureCommandRefusesWhenTheLeaseCheckCannotReadTheDatabase keeps an
// inconclusive preflight from silently archiving a database another process may
// still be writing. --force remains the explicit recovery path for a damaged
// database.
func TestPureCommandRefusesWhenTheLeaseCheckCannotReadTheDatabase(t *testing.T) {
	sessionDir := t.TempDir()
	if err := os.WriteFile(session.RootDatabasePath(sessionDir), []byte("not a sqlite file"), 0600); err != nil {
		t.Fatal(err)
	}
	stubReset(t, session.ResetReport{}, nil)

	cmd := newPureCommand()
	var stderr bytes.Buffer
	cmd.SetOut(io.Discard)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--session-dir", sessionDir})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("an unreadable lease table must refuse without --force")
	}
	if !strings.Contains(err.Error(), "--force") || !strings.Contains(stderr.String(), "refusing to reset") {
		t.Fatalf("error = %v, stderr = %q, want the forced-recovery guidance", err, stderr.String())
	}

	forced := newPureCommand()
	forced.SetOut(io.Discard)
	forced.SetErr(io.Discard)
	forced.SetArgs([]string{"--session-dir", sessionDir, "--force"})
	if err := forced.Execute(); err != nil {
		t.Fatalf("--force must permit recovery from an unreadable database: %v", err)
	}
}

// stubReset replaces the reset entry point for one test, restoring it on cleanup.
func stubReset(t *testing.T, report session.ResetReport, result error) {
	t.Helper()
	original := resetSessionsDatabase
	resetSessionsDatabase = func(string) (session.ResetReport, error) { return report, result }
	t.Cleanup(func() { resetSessionsDatabase = original })
}

// TestPureCommandPrunesUnreferencedAttachmentStorage covers the one reclaim
// path an operator can trigger on demand: an artifact directory no row claims
// and that is already past the retention floor is removed, while a directory
// inside the window is explicitly kept and reported.
func TestPureCommandPrunesUnreferencedAttachmentStorage(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	if err := session.CloseDatabases(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })

	policy := agentruntime.DefaultAttachmentPolicy()
	now := time.Now().UTC()
	floor := agentruntime.ArtifactReclaimFloor(policy, now)
	aged := writePureArtifactDirectory(t, sessionDir, "0123456789abcdef", floor.Add(-time.Hour), "stale bytes")
	recent := writePureArtifactDirectory(t, sessionDir, "1123456789abcdef", now.Add(-time.Hour), "in flight")

	cmd := newPureCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--session-dir", sessionDir, "--prune-unreferenced"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Reclaimed 1 unreferenced attachment directory") {
		t.Fatalf("stdout = %q, want exactly one reclaimed directory", output)
	}
	if !strings.Contains(output, "Kept 1 unreferenced attachment directory") {
		t.Fatalf("stdout = %q, want the too-young directory reported", output)
	}
	if _, err := os.Stat(aged); !os.IsNotExist(err) {
		t.Fatalf("aged orphan survived --prune-unreferenced: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("a directory inside the retention window was removed: %v", err)
	}
}

// writePureArtifactDirectory creates attachment-looking storage aged to the
// given modification time.
func writePureArtifactDirectory(t *testing.T, sessionDir, id string, modified time.Time, content string) string {
	t.Helper()
	dir := filepath.Join(sessionDir, "artifacts", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "content")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, modified, modified); err != nil {
		t.Fatal(err)
	}
	return path
}

// insertActiveLease writes a live lease row the way a running peer would, so the
// preflight has something real to find without spawning a second process.
func insertActiveLease(t *testing.T, sessionDir, sessionID string, expiresAt int64) {
	t.Helper()
	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	err = dao.NewRuntimeLeaseDAO(db.Bun()).Insert(context.Background(), db.Bun(), &dao.RuntimeLeaseRecord{
		SessionID: sessionID, OwnerID: "peer-owner", OwnerPID: 1, OwnerKind: "process",
		TokenHash: "peer-token", Epoch: 1, RunID: "run_active", Purpose: "execution", State: "active",
		AcquiredAt: now, HeartbeatAt: now, ExpiresAt: expiresAt, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.CloseDatabases(); err != nil {
		t.Fatal(err)
	}
}

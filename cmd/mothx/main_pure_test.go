package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

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

	cmd := newPureCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--session-dir", sessionDir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Moved previous sessions database to:") {
		t.Fatalf("stdout = %q, want the archive notice", stdout.String())
	}
	if !strings.Contains(stdout.String(), session.RootDatabasePath(sessionDir)) {
		t.Fatalf("stdout = %q, want the fresh database path", stdout.String())
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

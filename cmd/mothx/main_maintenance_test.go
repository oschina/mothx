package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// TestMaintenanceCommandsPreserveTranscript covers the one-shot maintenance
// entry points: status reports the transcript inventory, the vacuum dry run does
// not claim to have rewritten anything, and a real pass leaves messages intact.
func TestMaintenanceCommandsPreserveTranscript(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "sessions")
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("maintenance-cli"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("maintenance-cli-marker")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })

	run := func(args ...string) string {
		t.Helper()
		cmd := newMaintenanceCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(append(args, "--session-dir", sessionDir))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("maintenance %v: %v", args, err)
		}
		return out.String()
	}

	status := run("status")
	for _, want := range []string{"Transcript:", "sub-agent entries (never deleted)", "Tool records:", "Database size:"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status output = %q, want %q", status, want)
		}
	}

	dryRun := run("vacuum", "--dry-run")
	if !strings.Contains(dryRun, "Dry run: sessions.db was not rewritten.") {
		t.Fatalf("dry-run output = %q, want the no-write notice", dryRun)
	}

	vacuum := run("vacuum")
	if !strings.Contains(vacuum, "Transcript preserved") && !strings.Contains(vacuum, "No free pages") {
		t.Fatalf("vacuum output = %q, want a completed or no-op report", vacuum)
	}

	reopened, err := session.OpenByIDExact(sessionDir, "maintenance-cli")
	if err != nil {
		t.Fatalf("reopen session after vacuum: %v", err)
	}
	if messages := reopened.GetMessages(); len(messages) != 1 || messages[0].Content != "maintenance-cli-marker" {
		t.Fatalf("transcript after vacuum = %#v, want the message preserved", messages)
	}
}

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// `mothx --list-sessions` exists because a conversation started from another
// entry point (Desktop, Serve, a channel) is otherwise impossible to find from
// the CLI. The listing must cross working directories and must order by last
// activity so the conversation a person just had is first.
func TestListRecentSessionsCrossesWorkingDirectories(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.SessionDir = filepath.Join(configDir, "sessions")
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}

	older := session.New(filepath.Join(t.TempDir(), "desktop-project"), settings.SessionDir)
	if err := older.InitWithID("desktop-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := older.AppendMessage(provider.NewUserMessage("paper draft")); err != nil {
		t.Fatal(err)
	}
	newer := session.New(filepath.Join(t.TempDir(), "cli-project"), settings.SessionDir)
	if err := newer.InitWithID("cli-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := newer.AppendMessage(provider.NewUserMessage("build the tool")); err != nil {
		t.Fatal(err)
	}
	// Make the first session the most recently used one.
	if _, err := older.AppendMessage(provider.NewUserMessage("continue the draft")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := listRecentSessions(&out, 0, ""); err != nil {
		t.Fatalf("list recent sessions: %v", err)
	}
	listing := out.String()
	desktopIndex := strings.Index(listing, "desktop-session")
	cliIndex := strings.Index(listing, "cli-session")
	if desktopIndex < 0 || cliIndex < 0 {
		t.Fatalf("listing must contain both working directories:\n%s", listing)
	}
	if desktopIndex > cliIndex {
		t.Fatalf("listing must put the last used session first:\n%s", listing)
	}
	for _, want := range []string{"SESSION ID", "LAST USED", "MSGS", "WORKDIR", "paper draft", "mothx --continue", "mothx --session"} {
		if !strings.Contains(listing, want) {
			t.Fatalf("listing is missing %q:\n%s", want, listing)
		}
	}
}

func TestListRecentSessionsCanBeNarrowedToOneWorkingDirectory(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.SessionDir = filepath.Join(configDir, "sessions")
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}

	workDir := filepath.Join(t.TempDir(), "CrystalPlasticity")
	wanted := session.New(workDir, settings.SessionDir)
	if err := wanted.InitWithID("wanted-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := wanted.AppendMessage(provider.NewUserMessage("keep me")); err != nil {
		t.Fatal(err)
	}
	other := session.New(filepath.Join(t.TempDir(), "elsewhere"), settings.SessionDir)
	if err := other.InitWithID("other-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.AppendMessage(provider.NewUserMessage("drop me")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := listRecentSessions(&out, 0, workDir); err != nil {
		t.Fatalf("list recent sessions: %v", err)
	}
	listing := out.String()
	if !strings.Contains(listing, "wanted-session") || strings.Contains(listing, "other-session") {
		t.Fatalf("cwd filter must keep only the requested directory:\n%s", listing)
	}
}

func TestListRecentSessionsReportsAnEmptyCatalog(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.SessionDir = filepath.Join(configDir, "sessions")
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := listRecentSessions(&out, 0, ""); err != nil {
		t.Fatalf("list recent sessions: %v", err)
	}
	if !strings.Contains(out.String(), "No sessions with messages yet.") {
		t.Fatalf("empty catalog listing = %q", out.String())
	}
}

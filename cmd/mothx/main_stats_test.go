package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

func TestStatsCLIPrintsUsageSummary(t *testing.T) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.EnsureCurrentSchema(raw); err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	_, err = raw.Exec(
		"INSERT INTO request_stats (timestamp, session_id, provider, protocol, model, input_tokens, output_tokens, total_tokens, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		ts, "sess1", "openai", "openai-chat", "gpt-4.1", 1000, 250, 1250, 1800,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := newStatsCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--cli", "--db", dbPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute stats --cli: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"VibeCoding Stats",
		"Requests:",
		"Total tokens:",
		"By Provider",
		"openai (openai-chat)",
		"By Model",
		"gpt-4.1",
		"Recent Requests",
		"1.8s",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestOpenStatsDBUsesConfiguredSessionDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	sessionDir := filepath.Join(tmpDir, "custom-sessions")
	t.Setenv("MOTHX_DIR", configDir)

	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.GlobalSettingsPath(), []byte(`{"sessionDir":`+strconv.Quote(sessionDir)+`}`), 0600); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(sessionDir, "sessions.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.EnsureCurrentSchema(raw); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := openStatsDB(&statsFlags{})
	if err != nil {
		t.Fatalf("openStatsDB: %v", err)
	}
	defer db.Close()
}

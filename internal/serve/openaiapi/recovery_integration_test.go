package openaiapi

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

func TestServerRestartRecoversLocalRunAndPersistsRecoveryEvent(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("restart-session"); err != nil {
		t.Fatal(err)
	}
	first := NewRunManager(sessionDir)
	run := session.SessionRun{
		ID: "restart-local-run", SessionID: "restart-session", WorkDir: t.TempDir(),
		Source: "webui", Model: "test-model", Mode: "agent", Status: "running",
		StartedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := first.Create(run); err != nil {
		t.Fatal(err)
	}

	// A new manager represents the next server process and has no in-memory run.
	second := NewRunManager(sessionDir)
	if err := second.RecoverOrphanedRunsExcept(nil); err != nil {
		t.Fatal(err)
	}
	recovered, err := second.Get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil || recovered.Status != "failed" || recovered.Error == "" {
		t.Fatalf("recovered run = %#v", recovered)
	}
	events, err := session.ListSessionRunEvents(sessionDir, run.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "recovered" || events[0].Status != "failed" {
		t.Fatalf("recovery events = %#v", events)
	}
}

func TestServerRestartPreservesResponsesBackgroundRun(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("restart-responses-session"); err != nil {
		t.Fatal(err)
	}
	first := NewRunManager(sessionDir)
	run := session.SessionRun{
		ID: "restart-responses-run", SessionID: "restart-responses-session", WorkDir: t.TempDir(),
		Source: "responses_background", Status: "running", StartedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := first.Create(run); err != nil {
		t.Fatal(err)
	}
	second := NewRunManager(sessionDir)
	if err := second.RecoverOrphanedRunsExcept(func(run session.SessionRun) bool {
		return run.Source == "responses_background"
	}); err != nil {
		t.Fatal(err)
	}
	preserved, err := second.Get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preserved == nil || preserved.Status != "running" {
		t.Fatalf("preserved run = %#v", preserved)
	}
	events, err := session.ListSessionRunEvents(sessionDir, run.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("unexpected recovery events for preserved run: %#v", events)
	}
}

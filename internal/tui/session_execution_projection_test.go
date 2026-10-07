package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tui/i18n"
)

func TestSessionExecutionBadgeProjectsCanonicalStates(t *testing.T) {
	a := &App{translator: i18n.New(i18n.LanguageEN)}
	cases := []struct {
		state agentruntime.SessionExecutionState
		want  string
	}{
		{agentruntime.SessionExecutionIdle, ""},
		{agentruntime.SessionExecutionUnknown, ""},
		{agentruntime.SessionExecutionInconsistent, ""},
		{agentruntime.SessionExecutionLocal, "▶ running"},
		{agentruntime.SessionExecutionExternal, "▶ running elsewhere"},
		{agentruntime.SessionExecutionOrphaned, "⚠ interrupted"},
		{agentruntime.SessionExecutionDetached, "☁ remote"},
		{agentruntime.SessionExecutionReserved, "… reserved"},
		{agentruntime.SessionExecutionRecoveryFailed, "⚠ recovery failed"},
	}
	for _, tc := range cases {
		if got := a.sessionExecutionBadge(agentruntime.SessionExecutionSnapshot{State: tc.state}); got != tc.want {
			t.Fatalf("badge(%q) = %q, want %q", tc.state, got, tc.want)
		}
	}
	// The zero snapshot (unreadable/absent entry) renders no badge.
	if got := a.sessionExecutionBadge(agentruntime.SessionExecutionSnapshot{}); got != "" {
		t.Fatalf("zero snapshot badge = %q, want empty", got)
	}
}

func TestNotifySessionExecutionStateSurfacesCrossProcessOwnership(t *testing.T) {
	sessionDir := t.TempDir()
	orphan := session.New(t.TempDir(), sessionDir)
	if err := orphan.InitWithID("tui-orphan"); err != nil {
		t.Fatal(err)
	}
	idle := session.New(t.TempDir(), sessionDir)
	if err := idle.InitWithID("tui-idle"); err != nil {
		t.Fatal(err)
	}
	if err := (agentruntime.RunStore{SessionDir: sessionDir}).Create(agentruntime.DurableRun{
		ID: "tui-orphan-run", SessionID: "tui-orphan", Source: "tui", Status: "running", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	a := &App{settings: &config.Settings{SessionDir: sessionDir}, translator: i18n.New(i18n.LanguageEN)}
	a.notifySessionExecutionState("tui-idle")
	if len(a.messages) != 0 {
		t.Fatalf("idle session must stay silent: %#v", a.messages)
	}
	a.notifySessionExecutionState("tui-orphan")
	if len(a.messages) != 1 || !strings.Contains(a.messages[0], "interrupted run was detected") {
		t.Fatalf("orphan notification = %#v", a.messages)
	}
	// Unreadable/missing sessions must not fail or emit a projection.
	a.notifySessionExecutionState("tui-missing")
	if len(a.messages) != 1 {
		t.Fatalf("missing session emitted %#v", a.messages[1:])
	}
}

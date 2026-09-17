package tui

import (
	"testing"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/session"
)

func TestActivateSessionKeepsCurrentSessionWhenBindingFails(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	current := session.New(workDir, sessionDir)
	if err := current.Init(); err != nil {
		t.Fatal(err)
	}
	next := session.New(workDir, sessionDir)
	if err := next.Init(); err != nil {
		t.Fatal(err)
	}

	runtime := &agentruntime.SessionRuntime{Source: agentruntime.SourceTUI}
	runtime.Close()
	app := &App{session: current, cwd: current.GetHeader().Cwd, runtime: runtime}
	if err := app.activateSession(next); err == nil {
		t.Fatal("activateSession unexpectedly succeeded with a closed Runtime")
	}
	if app.session != current {
		t.Fatalf("active session changed after failed binding: got %p, want %p", app.session, current)
	}
	if app.cwd != current.GetHeader().Cwd {
		t.Fatalf("active cwd changed after failed binding: got %q, want %q", app.cwd, current.GetHeader().Cwd)
	}
}

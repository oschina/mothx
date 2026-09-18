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

// TestActivateSessionCommitsBothAdapterAndRuntimeIdentityOnSuccess is the other
// half of the ordering contract: once binding succeeds, the adapter directory
// mirrors the Runtime's authoritative WorkDir exactly, so neither can render or
// resolve policy for a session the other is not attached to.
func TestActivateSessionCommitsBothAdapterAndRuntimeIdentityOnSuccess(t *testing.T) {
	sessionDir := t.TempDir()
	sess := session.New(t.TempDir(), sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatal(err)
	}

	runtime := &agentruntime.SessionRuntime{Source: agentruntime.SourceTUI}
	app := &App{session: nil, cwd: "/previous/directory", runtime: runtime}
	if err := app.activateSession(sess); err != nil {
		t.Fatal(err)
	}
	if app.session != sess {
		t.Fatalf("active session = %p, want %p", app.session, sess)
	}
	if app.cwd != sess.GetHeader().Cwd {
		t.Fatalf("active cwd = %q, want the bound session directory %q", app.cwd, sess.GetHeader().Cwd)
	}
	if runtime.WorkDir != app.cwd || runtime.ID != sess.GetHeader().ID {
		t.Fatalf("runtime identity = (%q,%q), want it aligned with the adapter", runtime.ID, runtime.WorkDir)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })
}

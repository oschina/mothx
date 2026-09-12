package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

func newAbortTestApp(t *testing.T) (*App, *session.Manager, string, string) {
	t.Helper()
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := sess.GetHeader().ID
	app := NewApp(nil, &provider.Model{ID: "test"}, config.DefaultSettings(), sess, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	return app, sess, sessionID, sessionDir
}

func beginDurableTestRun(t *testing.T, app *App, sessionID, sessionDir string) *tuiRun {
	t.Helper()
	run := newTUIRun(sessionID, sessionDir)
	run.execution.SetRunStore(agentruntime.RunStore{SessionDir: sessionDir})
	startedAt := time.Now()
	intent := agentruntime.ExecutionIntent{ID: "intent-quit-" + sessionID, SessionID: sessionID, Source: "tui", CreatedAt: startedAt}
	if _, err := run.execution.BeginIntentDurable(context.Background(), intent, agentruntime.DurableRun{
		ID: run.id, SessionID: sessionID, IntentID: intent.ID, Source: "tui", Status: "running", StartedAt: startedAt,
		ConversationTurn: true, ConversationTurnID: "turn-" + intent.ID,
	}, agentruntime.RunEvent{SessionID: sessionID, RunID: run.id, EventType: "started", Source: "tui", Status: "running", Timestamp: startedAt}); err != nil {
		t.Fatalf("begin durable run: %v", err)
	}
	app.run = run
	app.isThinking = true
	return run
}

func durableRunStatus(t *testing.T, sessionDir, sessionID, runID string) string {
	t.Helper()
	runs, err := session.ListSessionRuns(sessionDir, sessionID, 20)
	if err != nil {
		t.Fatalf("list session runs: %v", err)
	}
	for _, run := range runs {
		if run.ID == runID {
			return run.Status
		}
	}
	t.Fatalf("run %s not found in session %s", runID, sessionID)
	return ""
}

// TestAbortResumesQueuedPrompts guards the queue hand-off: prompts submitted
// while a run was active used to be stranded after an abort because the aborted
// run's terminal event is retired and only that event restarted the queue.
func TestAbortResumesQueuedPrompts(t *testing.T) {
	app, _, _, _ := newAbortTestApp(t)
	app.queuedPrompts = []queuedPrompt{{text: "queued follow-up"}}

	cmd := app.abortPendingRequest("test abort")
	if cmd == nil {
		t.Fatal("abortPendingRequest returned no command")
	}
	if len(app.queuedPrompts) != 1 {
		t.Fatalf("queued prompts = %d, want the prompt to stay queued until it starts", len(app.queuedPrompts))
	}

	queue := []tea.Cmd{cmd}
	seen := false
	for ticks := 0; len(queue) > 0 && ticks < 50; ticks++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case queuedPromptStartMsg:
			seen = true
		default:
			if _, follow := app.Update(msg); follow != nil {
				queue = append(queue, follow)
			}
		}
	}
	if !seen {
		t.Fatal("abort did not resume the queued prompt")
	}
}

// TestAgentDoneWithoutTerminalEventFinalizesRun guards the stream-closed
// fallback: leaving a.run set would swallow every later submission into the
// queue and keep the session's execution lease alive.
func TestAgentDoneWithoutTerminalEventFinalizesRun(t *testing.T) {
	app, _, sessionID, sessionDir := newAbortTestApp(t)
	run := beginDurableTestRun(t, app, sessionID, sessionDir)
	app.eventCh = nil

	_, _ = app.Update(agentDoneMsg{eventCh: nil})

	if app.run != nil {
		t.Fatal("agentDoneMsg left the run attached")
	}
	if app.isThinking {
		t.Fatal("agentDoneMsg left the spinner active")
	}
	if status := durableRunStatus(t, sessionDir, sessionID, run.id); status != "failed" {
		t.Fatalf("durable run status = %q, want failed", status)
	}
}

// TestFinalizeForQuitTerminalizesRun guards the quit path: without a terminal
// write the run is later recovered as failed instead of the cancellation the
// user asked for.
func TestFinalizeForQuitTerminalizesRun(t *testing.T) {
	app, _, sessionID, sessionDir := newAbortTestApp(t)
	run := beginDurableTestRun(t, app, sessionID, sessionDir)

	app.finalizeForQuit()

	if app.run != nil {
		t.Fatal("finalizeForQuit left the run attached")
	}
	if status := durableRunStatus(t, sessionDir, sessionID, run.id); status != "cancelled" {
		t.Fatalf("durable run status = %q, want cancelled", status)
	}
}

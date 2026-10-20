package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tools"
)

// A first submission must become visible before the slow session bootstrap
// completes: the input is acknowledged, the waiting state is active, and the
// synchronous Bubble Tea update returns without creating the session.
func TestFirstPromptAcknowledgesBeforeSessionCreation(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	mock := provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model", Name: "Mock"}}, nil)
	app := NewApp(mock, mock.Models()[0], settings, nil, tools.NewRegistry(workDir, nil), "", "", "", nil, "yolo", false, false, nil, nil, nil)
	app.cwd = workDir

	cmd := app.processInput("hello new session")
	if cmd == nil {
		t.Fatal("processInput returned nil for the first prompt")
	}
	if app.session != nil {
		t.Fatal("session was created synchronously on the update loop")
	}
	if !app.preparingSession || !app.isThinking {
		t.Fatalf("waiting state = preparingSession=%v isThinking=%v, want both true", app.preparingSession, app.isThinking)
	}
	if len(app.messages) == 0 || !strings.Contains(stripANSI(app.messages[len(app.messages)-1]), "hello new session") {
		t.Fatalf("user prompt was not acknowledged immediately: %#v", app.messages)
	}

	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("first prompt command = %#v, want a non-empty batch", cmd())
	}
	msg, ok := batch[0]().(tuiSessionReadyMsg)
	if !ok {
		t.Fatalf("session creation result = %#v, want tuiSessionReadyMsg", batch[0]())
	}
	if msg.err != nil {
		t.Fatalf("create session: %v", msg.err)
	}

	_, startCmd := app.Update(msg)
	if app.session == nil || app.session.GetHeader() == nil {
		t.Fatal("session was not activated after the async bootstrap")
	}
	if app.run == nil {
		t.Fatal("prompt run was not prepared after the async bootstrap")
	}
	if app.agent == nil {
		t.Fatal("agent was not built after the async bootstrap")
	}
	if app.preparingSession || !app.isThinking {
		t.Fatalf("waiting state after bootstrap = preparingSession=%v isThinking=%v, want false/true", app.preparingSession, app.isThinking)
	}
	if startCmd == nil {
		t.Fatal("async bootstrap did not return the stream start command")
	}
	streamStart, ok := startCmd().(agentStreamStartMsg)
	if !ok {
		t.Fatalf("stream start = %#v, want agentStreamStartMsg", startCmd())
	}
	if streamStart.err != nil {
		t.Fatalf("start run: %v", streamStart.err)
	}
	if streamStart.input != "" {
		t.Fatalf("stream start input = %q, want empty because the prompt was already echoed", streamStart.input)
	}
}

func TestFirstPromptQueuesLaterSubmissionsWhileSessionIsPreparing(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	mock := provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model", Name: "Mock"}}, nil)
	app := NewApp(mock, mock.Models()[0], settings, nil, tools.NewRegistry(workDir, nil), "", "", "", nil, "yolo", false, false, nil, nil, nil)
	app.cwd = workDir

	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("first prompt did not return a command")
	}
	if cmd := app.processInput("second"); cmd != nil {
		t.Fatal("second prompt started a parallel submission while the session was preparing")
	}
	if len(app.queuedPrompts) != 1 || app.queuedPrompts[0].text != "second" {
		t.Fatalf("queued prompts = %#v, want the second submission", app.queuedPrompts)
	}
}

func TestAbortFirstPromptInvalidatesLateSessionCreation(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	mock := provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model", Name: "Mock"}}, nil)
	app := NewApp(mock, mock.Models()[0], settings, nil, tools.NewRegistry(workDir, nil), "", "", "", nil, "yolo", false, false, nil, nil, nil)
	app.cwd = workDir

	cmd := app.processInput("cancel me")
	if cmd == nil {
		t.Fatal("first prompt did not return a command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatal("first prompt command is not a batch")
	}
	late := batch[0]()

	app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if app.preparingSession || app.isThinking {
		t.Fatalf("abort left waiting state = preparingSession=%v isThinking=%v", app.preparingSession, app.isThinking)
	}

	app.Update(late)
	if app.session != nil {
		t.Fatal("a late session creation result re-activated an aborted submission")
	}
	if app.run != nil {
		t.Fatal("aborted submission started a run")
	}
}

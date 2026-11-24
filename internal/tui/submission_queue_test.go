package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
	"github.com/oschina/mothx/internal/tui/i18n"
)

func newSubmissionQueueTestApp(t *testing.T) (*App, string, string, chan tea.Msg) {
	t.Helper()
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	mock := provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model", Name: "Mock"}}, nil)
	app := NewApp(mock, mock.Models()[0], settings, sess, tools.NewRegistry(workDir, nil), "", "", "", nil, "yolo", false, false, nil, nil, nil)
	app.cwd = workDir
	app.program = tea.NewProgram(app)
	msgs := make(chan tea.Msg, 64)
	app.sendHook = func(msg tea.Msg) { msgs <- msg }
	return app, workDir, sessionDir, msgs
}

func waitForSubmissionCompletions(t *testing.T, app *App, msgs <-chan tea.Msg, want int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for got := 0; got < want; {
		select {
		case msg := <-msgs:
			if msg == nil {
				continue
			}
			if _, ok := msg.(tuiSubmissionAdmittedMsg); ok {
				// Admission is only projected after the legacy path is gone; a
				// pending admission must be abandoned through its submission ctx.
				continue
			}
			app.Update(msg)
			if _, ok := msg.(tuiSubmissionCompletedMsg); ok {
				got++
			}
		case <-deadline:
			t.Fatalf("received %d/%d submission completions", got, want)
		}
	}
}

// Two rapid prompts must both be registered with the shared coordinator before
// the first run starts. The adapter projection may be empty of a.run while it
// waits, but it must never start a second local submission path.
func TestRuntimeSubmissionsRegisterSynchronouslyAndQueue(t *testing.T) {
	app, _, _, msgs := newSubmissionQueueTestApp(t)

	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("first prompt returned no waiting command")
	}
	if app.run != nil {
		t.Fatal("first prompt installed a.run before admission")
	}
	if len(app.pendingSubmissions) != 1 {
		t.Fatalf("pending submissions = %d, want 1", len(app.pendingSubmissions))
	}
	if len(app.queuedPrompts) != 0 {
		t.Fatalf("runtime submission leaked into the local queue: %#v", app.queuedPrompts)
	}

	if cmd := app.processInput("second"); cmd != nil {
		t.Fatal("second prompt started a parallel local submission")
	}
	if len(app.pendingSubmissions) != 2 {
		t.Fatalf("pending submissions = %d, want 2", len(app.pendingSubmissions))
	}
	if app.pendingSubmissions[0].text != "first" || app.pendingSubmissions[1].text != "second" {
		t.Fatalf("submission order = %q, %q", app.pendingSubmissions[0].text, app.pendingSubmissions[1].text)
	}

	if cmd := app.processInput("/help"); cmd != nil {
		t.Fatal("a slash command while busy started work")
	}
	if len(app.pendingSubmissions) != 2 {
		t.Fatalf("slash command was queued as a prompt: %d submissions", len(app.pendingSubmissions))
	}
	if last := app.messages[len(app.messages)-1]; !strings.Contains(stripANSI(last), app.translator.Text(i18n.MsgCommandsNotQueued)) {
		t.Fatalf("slash command rejection = %q", stripANSI(last))
	}

	app.cancelPendingSubmissions()
	waitForSubmissionCompletions(t, app, msgs, 2)
	if app.hasPendingSubmissions() {
		t.Fatal("cancelled submissions stayed in the projection")
	}
}

// Prompts staged while the first session is being created must move into the
// shared coordinator once the bootstrap completes. They must not remain in the
// TUI-local queue, where no Agent terminal event would ever drain them.
func TestSessionPreparationFlushesStagedPromptsIntoCoordinator(t *testing.T) {
	app, workDir, sessionDir, msgs := newSubmissionQueueTestApp(t)
	app.session = nil

	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("first prompt did not start session preparation")
	}
	if !app.preparingSession {
		t.Fatal("session preparation did not enter the waiting state")
	}
	if cmd := app.processInput("second"); cmd != nil {
		t.Fatal("second prompt started work while the session was preparing")
	}
	if len(app.queuedPrompts) != 1 || app.queuedPrompts[0].text != "second" {
		t.Fatalf("staged prompts = %#v, want [second]", app.queuedPrompts)
	}

	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init prepared session: %v", err)
	}
	app.Update(tuiSessionReadyMsg{seq: app.sessionPrepSeq, sess: sess, input: "first"})
	if app.preparingSession {
		t.Fatal("session preparation stayed active after the bootstrap result")
	}
	if len(app.queuedPrompts) != 0 {
		t.Fatalf("staged prompts remained after flush: %#v", app.queuedPrompts)
	}
	if len(app.pendingSubmissions) != 2 {
		t.Fatalf("coordinator submissions = %d, want 2", len(app.pendingSubmissions))
	}
	if app.pendingSubmissions[0].text != "first" || app.pendingSubmissions[1].text != "second" {
		t.Fatalf("submission order = %q, %q", app.pendingSubmissions[0].text, app.pendingSubmissions[1].text)
	}

	app.cancelPendingSubmissions()
	waitForSubmissionCompletions(t, app, msgs, 2)
}

// A first prompt's staged resources belong to that prompt, not to a second
// prompt typed while the session is still being created.
func TestSessionPreparationKeepsFirstPromptResources(t *testing.T) {
	app, _, _, _ := newSubmissionQueueTestApp(t)
	app.session = nil
	first := []agentruntime.PreparedInput{{ResourceID: "resource-first"}}
	app.pendingInputResources = first
	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("first prompt did not start session preparation")
	}
	if len(app.preparingInputResources) != len(first) {
		t.Fatalf("preparing resources = %d, want %d", len(app.preparingInputResources), len(first))
	}
	if len(app.pendingInputResources) != 0 {
		t.Fatalf("pending resources were not captured: %d", len(app.pendingInputResources))
	}
	if cmd := app.processInput("second"); cmd != nil {
		t.Fatal("second prompt started work while preparing")
	}
	if len(app.queuedPrompts) != 1 || len(app.queuedPrompts[0].resources) != 0 {
		t.Fatalf("second prompt stole the first prompt's resources: %#v", app.queuedPrompts)
	}
	app.preparingInputResources = nil
	app.queuedPrompts = nil
}

// Three prompts submitted in order must execute in FIFO order through the
// shared coordinator, and each must echo its own user line when it starts.
func TestQueuedPromptsRunInOrderThroughCoordinator(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	responses := []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "ok"},
		{Type: provider.StreamDone, StopReason: "end_turn"},
	}
	mock := provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model", Name: "Mock"}}, responses)
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	settings.DefaultThinkingLevel = "off"
	app := NewApp(mock, mock.Models()[0], settings, sess, tools.NewRegistry(workDir, nil), "", "", "", nil, "yolo", false, false, nil, nil, nil)
	app.cwd = workDir
	app.program = tea.NewProgram(app)
	msgs := make(chan tea.Msg, 64)
	app.sendHook = func(msg tea.Msg) { msgs <- msg }

	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("first prompt returned no waiting command")
	}
	if cmd := app.processInput("second"); cmd != nil {
		t.Fatal("second prompt started a parallel local submission")
	}
	if cmd := app.processInput("third"); cmd != nil {
		t.Fatal("third prompt started a parallel local submission")
	}
	if got := len(app.pendingSubmissions); got != 3 {
		t.Fatalf("pending submissions = %d, want 3", got)
	}

	deadline := time.After(10 * time.Second)
	for index := 0; index < 3; index++ {
		var admitted tuiSubmissionAdmittedMsg
		for admitted.submission == nil {
			select {
			case raw := <-msgs:
				if msg, ok := raw.(tuiSubmissionAdmittedMsg); ok {
					admitted = msg
				}
			case <-deadline:
				t.Fatalf("submission %d was not admitted", index)
			}
		}
		_, startCmd := app.Update(admitted)
		if startCmd == nil {
			t.Fatalf("submission %d produced no stream start command", index)
		}
		start, ok := startCmd().(agentStreamStartMsg)
		if !ok || start.err != nil || start.eventCh == nil {
			t.Fatalf("submission %d stream start = %#v err=%v", index, start, start.err)
		}
		app.Update(start)
		finished := false
		for !finished {
			select {
			case event, open := <-start.eventCh:
				if !open {
					app.Update(agentDoneMsg{eventCh: start.eventCh})
					finished = true
					continue
				}
				app.Update(agentEventMsg{event: event, eventCh: start.eventCh})
				if event.Type == agent.EventRunFinished {
					finished = true
				}
			case <-deadline:
				t.Fatalf("submission %d did not finish", index)
			}
		}
		if app.run != nil {
			t.Fatalf("submission %d left a.run installed", index)
		}
	}
	if got := mock.GetCallCount(); got != 3 {
		t.Fatalf("provider calls = %d, want 3", got)
	}
	if app.hasPendingSubmissions() {
		t.Fatalf("submissions remained after execution: %d", len(app.pendingSubmissions))
	}
	for _, want := range []string{"first", "second", "third"} {
		found := false
		for _, message := range app.messages {
			if strings.Contains(stripANSI(message), want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing transcript line for %q", want)
		}
	}
}

// A single prompt on an idle session is admitted at once. Its projection row
// exists until the admitted hand-off is processed, but it is starting rather
// than waiting behind a run, so it must never render as "queued".
func TestIdleSinglePromptDoesNotRenderQueueIndicator(t *testing.T) {
	app, _, _, msgs := newSubmissionQueueTestApp(t)

	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("first prompt returned no waiting command")
	}
	if len(app.pendingSubmissions) != 1 {
		t.Fatalf("pending submissions = %d, want the head-of-line projection", len(app.pendingSubmissions))
	}
	if got := stripANSI(app.renderQueuedPrompts(80)); got != "" {
		t.Fatalf("idle prompt rendered the queue indicator: %q", got)
	}

	// Only input submitted behind it is really queued, and still renders.
	if cmd := app.processInput("second"); cmd != nil {
		t.Fatal("second prompt started a parallel local submission")
	}
	wantHeader := stripANSI(app.translator.Text(i18n.MsgQueuedPromptsHeader, 1))
	got := stripANSI(app.renderQueuedPrompts(80))
	if !strings.Contains(got, wantHeader) || !strings.Contains(got, "1. second") {
		t.Fatalf("prompt queued behind the active run was not rendered: %q", got)
	}
	if strings.Contains(got, "first") {
		t.Fatalf("head-of-line prompt listed as queued: %q", got)
	}

	app.cancelPendingSubmissions()
	waitForSubmissionCompletions(t, app, msgs, 2)
}

// The first prompt of a new session takes the same admitted hand-off. Prompts
// staged during the bootstrap really queue behind it; the first prompt itself
// is never listed.
func TestSessionBootstrapListsOnlyStagedPromptsAsQueued(t *testing.T) {
	app, workDir, sessionDir, msgs := newSubmissionQueueTestApp(t)
	app.session = nil

	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("first prompt did not start session preparation")
	}
	if cmd := app.processInput("second"); cmd != nil {
		t.Fatal("second prompt started work while the session was preparing")
	}
	wantHeader := stripANSI(app.translator.Text(i18n.MsgQueuedPromptsHeader, 1))
	if got := stripANSI(app.renderQueuedPrompts(80)); !strings.Contains(got, wantHeader) || strings.Contains(got, "first") {
		t.Fatalf("staged prompt projection = %q, want only the prompt staged during bootstrap", got)
	}

	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init prepared session: %v", err)
	}
	app.Update(tuiSessionReadyMsg{seq: app.sessionPrepSeq, sess: sess, input: "first"})
	got := stripANSI(app.renderQueuedPrompts(80))
	if !strings.Contains(got, wantHeader) || !strings.Contains(got, "1. second") {
		t.Fatalf("prompt staged behind the first submission was not rendered: %q", got)
	}
	if strings.Contains(got, "first") {
		t.Fatalf("first prompt listed as queued after activation: %q", got)
	}

	app.cancelPendingSubmissions()
	waitForSubmissionCompletions(t, app, msgs, 2)
}

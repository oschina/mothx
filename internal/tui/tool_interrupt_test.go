package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
)

func newInterruptTestApp(t *testing.T) *App {
	t.Helper()
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "yolo", false, false, nil, nil, nil)
	a.program = tea.NewProgram(a)
	a.messages = []string{"assistant start"}
	a.printedMessageIdx = map[int]bool{0: true}
	return a
}

func startRunningBashTool(a *App) {
	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolArgs:   map[string]any{"command": "sleep 100"},
	})
}

func queuedPrints(a *App) string {
	a.printMu.Lock()
	defer a.printMu.Unlock()
	return stripANSI(strings.Join(a.printQueue, "\n"))
}

// An Esc cancellation while a tool is still executing must terminalize the row.
// Otherwise renderLiveTranscriptContent keeps it pinned in the managed viewport
// forever instead of committing it to terminal scrollback.
func TestAbortTerminalizesRunningToolRow(t *testing.T) {
	a := newInterruptTestApp(t)
	a.isThinking = true
	startRunningBashTool(a)

	if live := stripANSI(a.renderLiveTranscriptContent()); !strings.Contains(live, "sleep 100 (running)") {
		t.Fatalf("running tool missing before abort: %q", live)
	}

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEsc))

	if live := stripANSI(a.renderLiveTranscriptContent()); strings.Contains(live, "sleep 100") {
		t.Fatalf("aborted tool row stayed in the live viewport: %q", live)
	}
	printed := queuedPrints(a)
	if !strings.Contains(printed, "sleep 100") {
		t.Fatalf("aborted tool row was never printed to scrollback: %q", printed)
	}
	if strings.Contains(printed, "(running)") {
		t.Fatalf("aborted tool row printed in running state: %q", printed)
	}
	if got, want := strings.Count(printed, "sleep 100"), 1; got != want {
		t.Fatalf("aborted tool row printed %d times, want %d: %q", got, want, printed)
	}
}

// A run that terminalizes as canceled without the TUI aborting it (for example a
// Runtime lease loss) must not leave a running row pinned either.
func TestCanceledRunTerminalizesRunningToolRow(t *testing.T) {
	a := newInterruptTestApp(t)
	startRunningBashTool(a)

	a.handleAgentEvent(agent.Event{Type: agent.EventRunFinished, Status: agent.TaskCanceled})

	if live := stripANSI(a.renderLiveTranscriptContent()); strings.Contains(live, "sleep 100") {
		t.Fatalf("canceled tool row stayed in the live viewport: %q", live)
	}
	printed := queuedPrints(a)
	if !strings.Contains(printed, "sleep 100") || strings.Contains(printed, "(running)") {
		t.Fatalf("canceled run did not commit a terminal tool row: %q", printed)
	}
}

// The interrupted row must not resurface in the managed viewport once the next
// prompt starts streaming.
func TestAbortedToolRowDoesNotLingerIntoNextRun(t *testing.T) {
	a := newInterruptTestApp(t)
	a.isThinking = true
	startRunningBashTool(a)

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEsc))

	a.handleAgentEvent(agent.Event{Type: agent.EventTurnStart})
	a.handleAgentEvent(agent.Event{Type: agent.EventTextDelta, TextDelta: "second attempt"})

	live := stripANSI(a.renderLiveTranscriptContent())
	if strings.Contains(live, "sleep 100") {
		t.Fatalf("interrupted tool row resurfaced in the next run's live viewport: %q", live)
	}
	if !strings.Contains(live, "second attempt") {
		t.Fatalf("next run streaming content missing from live viewport: %q", live)
	}
}

// Non-bash tools report the terminal state in the coalesced row as well.
func TestInterruptedToolRowRendersCanceledState(t *testing.T) {
	a := newInterruptTestApp(t)
	a.isThinking = true
	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "tool-read",
		ToolName:   "read",
		ToolArgs:   map[string]any{"path": "/tmp/file.go"},
	})

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEsc))

	printed := queuedPrints(a)
	if !strings.Contains(printed, "🔧 [read] /tmp/file.go canceled") {
		t.Fatalf("interrupted read row = %q, want canceled state", printed)
	}
}

// A completed tool result must still coalesce into the single terminal row: the
// interrupt path must not print or duplicate a row that already finished.
func TestToolResultAfterRunningRowIsNotDuplicated(t *testing.T) {
	a := newInterruptTestApp(t)
	startRunningBashTool(a)

	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolResult,
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolResult: "done",
	})

	printed := queuedPrints(a)
	if got, want := strings.Count(printed, "sleep 100"), 1; got != want {
		t.Fatalf("completed tool row printed %d times, want %d: %q", got, want, printed)
	}
	if strings.Contains(printed, "(running)") {
		t.Fatalf("completed tool row printed in running state: %q", printed)
	}
}

// A late result from the aborted run must not open a second row for a tool call
// whose row was already committed as canceled.
func TestLateToolResultAfterAbortIsIgnored(t *testing.T) {
	a := newInterruptTestApp(t)
	a.isThinking = true
	startRunningBashTool(a)

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEsc))
	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolResult: "context canceled",
		ToolError:  errors.New("context canceled"),
	})

	if got := len(a.toolResults); got != 1 {
		t.Fatalf("toolResults = %d rows, want 1: %#v", got, a.toolResults)
	}
	if got, want := strings.Count(queuedPrints(a), "sleep 100"), 1; got != want {
		t.Fatalf("tool row printed %d times, want %d: %q", got, want, queuedPrints(a))
	}
}

// Full event display commits the same single-line canceled row for bash.
func TestInterruptedBashRowStaysSingleLineInFullMode(t *testing.T) {
	a := newInterruptTestApp(t)
	a.compactMode = false
	a.isThinking = true
	startRunningBashTool(a)

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEsc))

	printed := queuedPrints(a)
	if !strings.Contains(printed, "🔧 [bash] sleep 100 (canceled)") || strings.Contains(printed, "...") {
		t.Fatalf("full mode interrupted bash row = %q", printed)
	}
}

// A tool call that is executed again under the same id after an interrupt must
// still complete its new running row instead of being treated as a straggler.
func TestRestartedToolCallAfterInterruptStillCompletes(t *testing.T) {
	a := newInterruptTestApp(t)
	a.isThinking = true
	startRunningBashTool(a)
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEsc))

	startRunningBashTool(a)
	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolResult,
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolResult: "restart done",
	})

	if got := len(a.toolResults); got != 2 {
		t.Fatalf("toolResults = %d rows, want 2: %#v", got, a.toolResults)
	}
	if a.toolResults[1].status != toolResultStatusCompleted || a.toolResults[1].summary == "" {
		t.Fatalf("restarted tool row = %#v, want completed result", a.toolResults[1])
	}
	printed := queuedPrints(a)
	if !strings.Contains(printed, "🔧 [bash] sleep 100 (succeeded)") {
		t.Fatalf("restarted tool result missing from scrollback: %q", printed)
	}
}

// Starting a new run must not render underneath a running row left behind by an
// earlier one: its terminal result can no longer reach this transcript.
func TestNewRunStartFinalizesStaleRunningToolRow(t *testing.T) {
	a := newInterruptTestApp(t)
	startRunningBashTool(a)

	a.Update(agentStreamStartMsg{eventCh: make(chan agent.Event, 1), input: "next prompt"})

	if a.toolResults[0].status != toolResultStatusInterrupted {
		t.Fatalf("stale tool row status = %q, want interrupted", a.toolResults[0].status)
	}
	printed := queuedPrints(a)
	if !strings.Contains(printed, "sleep 100 (canceled)") {
		t.Fatalf("stale tool row was not committed: %q", printed)
	}
	if strings.Index(printed, "sleep 100") > strings.Index(printed, "next prompt") {
		t.Fatalf("stale tool row must be committed before the new prompt: %q", printed)
	}
	if live := stripANSI(a.renderLiveTranscriptContent()); strings.Contains(live, "sleep 100") {
		t.Fatalf("stale tool row resurfaced in the live viewport: %q", live)
	}
}

// Aborting a stream must commit what already streamed, so the partial answer
// stays in terminal scrollback instead of vanishing when the next run starts.
func TestAbortCommitsStreamedAssistantText(t *testing.T) {
	a := newInterruptTestApp(t)
	a.isThinking = true
	a.handleAgentEvent(agent.Event{Type: agent.EventTurnStart})
	a.handleAgentEvent(agent.Event{Type: agent.EventTextDelta, TextDelta: "partial answer"})

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEsc))

	printed := queuedPrints(a)
	if !strings.Contains(printed, "partial answer") {
		t.Fatalf("streamed text was not committed on abort: %q", printed)
	}
	if live := stripANSI(a.renderLiveTranscriptContent()); strings.TrimSpace(live) != "" {
		t.Fatalf("live viewport not cleared after abort: %q", live)
	}

	a.handleAgentEvent(agent.Event{Type: agent.EventTurnStart})
	a.handleAgentEvent(agent.Event{Type: agent.EventTextDelta, TextDelta: "second attempt"})
	if live := stripANSI(a.renderLiveTranscriptContent()); !strings.Contains(live, "second attempt") {
		t.Fatalf("next run streaming content missing: %q", live)
	}
}

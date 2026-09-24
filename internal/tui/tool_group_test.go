package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/agent"
)

func startRunningTool(a *App, id, name string, args map[string]any) {
	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: id,
		ToolName:   name,
		ToolArgs:   args,
	})
}

func finishTool(a *App, id, name, result string) {
	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolResult,
		ToolCallID: id,
		ToolName:   name,
		ToolResult: result,
	})
}

// Parallel running tool calls collapse into one tree group with a count title.
func TestParallelRunningToolsRenderAsTreeGroup(t *testing.T) {
	a := newInterruptTestApp(t)
	startRunningTool(a, "tool-1", "bash", map[string]any{"command": "sleep 100"})
	startRunningTool(a, "tool-2", "read", map[string]any{"path": "/tmp/file.go"})
	startRunningTool(a, "tool-3", "grep", map[string]any{"pattern": "needle"})

	live := stripANSI(a.renderLiveTranscriptContent())
	if !strings.Contains(live, "Running: 3 tools") {
		t.Fatalf("group title missing count: %q", live)
	}
	if !strings.Contains(live, "├─ ") || !strings.Contains(live, "└─ ") {
		t.Fatalf("tree branches missing: %q", live)
	}
	if got := strings.Count(live, "🔧"); got != 4 {
		t.Fatalf("tool rows = %d, want title + 3 branches: %q", got, live)
	}
}

// A single running tool keeps its standalone row instead of a one-item group.
func TestSingleRunningToolIsNotGrouped(t *testing.T) {
	a := newInterruptTestApp(t)
	startRunningTool(a, "tool-1", "bash", map[string]any{"command": "sleep 100"})

	live := stripANSI(a.renderLiveTranscriptContent())
	if strings.Contains(live, "Running:") || strings.ContainsAny(live, "├└") {
		t.Fatalf("single running tool was grouped: %q", live)
	}
	if !strings.Contains(live, "sleep 100 (running)") {
		t.Fatalf("single running tool row missing: %q", live)
	}
}

// A partially finished batch stays a single tree group in the viewport until
// the last call terminalizes; it must not split into independent rows.
func TestPartiallyFinishedGroupStaysGrouped(t *testing.T) {
	a := newInterruptTestApp(t)
	startRunningTool(a, "tool-1", "bash", map[string]any{"command": "sleep 100"})
	startRunningTool(a, "tool-2", "bash", map[string]any{"command": "sleep 200"})

	finishTool(a, "tool-1", "bash", "first done")

	live := stripANSI(a.renderLiveTranscriptContent())
	if !strings.Contains(live, "Running: 2 tools") {
		t.Fatalf("group title lost while a sibling still runs: %q", live)
	}
	if !strings.Contains(live, "sleep 100 (succeeded)") {
		t.Fatalf("completed child missing from the group: %q", live)
	}
	if !strings.Contains(live, "sleep 200 (running)") {
		t.Fatalf("running child missing from the group: %q", live)
	}
	if printed := queuedPrints(a); strings.TrimSpace(printed) != "" {
		t.Fatalf("partially finished group was committed early: %q", printed)
	}
}

// Once every call terminalizes the batch is committed to scrollback as one tree
// block that keeps its shape, instead of separate per-call rows.
func TestFinishedGroupCommitsAsTreeBlock(t *testing.T) {
	a := newInterruptTestApp(t)
	startRunningTool(a, "tool-1", "bash", map[string]any{"command": "sleep 100"})
	startRunningTool(a, "tool-2", "read", map[string]any{"path": "/tmp/file.go"})
	startRunningTool(a, "tool-3", "grep", map[string]any{"pattern": "needle"})

	finishTool(a, "tool-1", "bash", "first")
	finishTool(a, "tool-2", "read", "second")
	finishTool(a, "tool-3", "grep", "third")

	printed := queuedPrints(a)
	if got := strings.Count(printed, "Done: 3 tools"); got != 1 {
		t.Fatalf("done title occurrences = %d, want 1: %q", got, printed)
	}
	if !strings.Contains(printed, "├─ ") || !strings.Contains(printed, "└─ ") {
		t.Fatalf("committed block lost its tree shape: %q", printed)
	}
	if strings.Contains(printed, "Running:") {
		t.Fatalf("committed block kept the running title: %q", printed)
	}
	// The three calls commit together, so exactly one scrollback entry exists.
	if got := strings.Count(printed, "🔧"); got != 3 {
		t.Fatalf("committed tool rows = %d, want 3 branches: %q", got, printed)
	}
	if live := stripANSI(a.renderLiveTranscriptContent()); strings.Contains(live, "🔧") {
		t.Fatalf("committed group lingered in the live viewport: %q", live)
	}
}

// An interrupted batch still commits as one tree block rather than as separate
// canceled rows.
func TestInterruptedGroupCommitsAsTreeBlock(t *testing.T) {
	a := newInterruptTestApp(t)
	a.isThinking = true
	startRunningTool(a, "tool-1", "bash", map[string]any{"command": "sleep 100"})
	startRunningTool(a, "tool-2", "read", map[string]any{"path": "/tmp/file.go"})

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEsc))

	printed := queuedPrints(a)
	if got := strings.Count(printed, "Done: 2 tools"); got != 1 {
		t.Fatalf("done title occurrences = %d, want 1: %q", got, printed)
	}
	if !strings.Contains(printed, "canceled") {
		t.Fatalf("interrupted group missing canceled children: %q", printed)
	}
	if live := stripANSI(a.renderLiveTranscriptContent()); strings.Contains(live, "🔧") {
		t.Fatalf("interrupted group lingered in the live viewport: %q", live)
	}
}

package esm

import (
	"strings"

	agentpkg "github.com/oschina/mothx/agent"
)

// EvidenceTracker accumulates tool-call evidence for one ESM role run. TUI and
// WebUI adapters must share this tracker so the "tool-backed evidence" checks
// in ApplyWorkerResult/ApplyReviewResult cannot diverge between adapters.
type EvidenceTracker struct {
	toolCalls int
	toolNames map[string]int
	toolError map[string]bool
	seen      map[string]struct{}
}

// NewEvidenceTracker returns an empty tracker for one role run.
func NewEvidenceTracker() *EvidenceTracker {
	return &EvidenceTracker{
		toolNames: make(map[string]int),
		toolError: make(map[string]bool),
		seen:      make(map[string]struct{}),
	}
}

// Observe records one agent event's tool evidence. Tool calls are counted once
// per unique tool-call ID; events without an ID are counted as they arrive.
func (t *EvidenceTracker) Observe(ev agentpkg.Event) {
	if t == nil {
		return
	}
	switch ev.Type {
	case agentpkg.EventToolCall, agentpkg.EventToolExecutionStart:
		id := ev.ToolCallID
		if id == "" && ev.ToolCall != nil {
			id = ev.ToolCall.ID
		}
		if id == "" {
			t.toolCalls++
		} else if _, ok := t.seen[id]; !ok {
			t.seen[id] = struct{}{}
			t.toolCalls++
		}
		name := ev.ToolName
		if name == "" && ev.ToolCall != nil {
			name = ev.ToolCall.Name
		}
		if name != "" {
			t.toolNames[name]++
		}
	case agentpkg.EventToolExecutionEnd, agentpkg.EventToolResult:
		if ev.ToolError != nil && ev.ToolCallID != "" {
			t.toolError[ev.ToolCallID] = true
		}
	}
}

// Summary returns the accumulated evidence for a RoleResult.
func (t *EvidenceTracker) Summary() (int, map[string]int, map[string]bool) {
	if t == nil {
		return 0, map[string]int{}, map[string]bool{}
	}
	return t.toolCalls, t.toolNames, t.toolError
}

// FinalAssistantResponse returns the final assistant text of a run. It prefers
// Content and falls back to concatenated text blocks, so both adapters parse
// the same structured ESM report from the same canonical extraction.
func FinalAssistantResponse(messages []agentpkg.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != agentpkg.RoleAssistant {
			continue
		}
		if strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
		var b strings.Builder
		for _, block := range messages[i].Contents {
			if block.Type == "text" && block.Text != "" {
				b.WriteString(block.Text)
			}
		}
		return b.String()
	}
	return ""
}

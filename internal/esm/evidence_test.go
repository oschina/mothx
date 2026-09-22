package esm

import (
	"errors"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
)

func TestFinalAssistantResponsePrefersContentAndFallsBackToBlocks(t *testing.T) {
	messages := []agentpkg.Message{
		{Role: agentpkg.RoleAssistant, Content: "first answer"},
		{Role: agentpkg.RoleAssistant, Contents: []agentpkg.ContentBlock{
			{Type: "thinking", Thinking: "ignore"},
			{Type: "text", Text: "block "},
			{Type: "text", Text: "answer"},
		}},
	}
	if got := FinalAssistantResponse(messages); got != "block answer" {
		t.Fatalf("FinalAssistantResponse = %q, want block fallback", got)
	}

	messages = append(messages, agentpkg.Message{Role: agentpkg.RoleAssistant, Content: "plain content"})
	if got := FinalAssistantResponse(messages); got != "plain content" {
		t.Fatalf("FinalAssistantResponse = %q, want content", got)
	}

	if got := FinalAssistantResponse([]agentpkg.Message{{Role: "user", Content: "hi"}}); got != "" {
		t.Fatalf("FinalAssistantResponse = %q, want empty", got)
	}
}

func TestEvidenceTrackerCountsUniqueToolCallsAndErrors(t *testing.T) {
	tracker := NewEvidenceTracker()
	tracker.Observe(agentpkg.Event{Type: agentpkg.EventToolExecutionStart, ToolCallID: "call-1", ToolName: "read"})
	tracker.Observe(agentpkg.Event{Type: agentpkg.EventToolCall, ToolCallID: "call-1", ToolName: "read"})
	tracker.Observe(agentpkg.Event{Type: agentpkg.EventToolExecutionStart, ToolName: "bash"})
	tracker.Observe(agentpkg.Event{Type: agentpkg.EventToolExecutionEnd, ToolCallID: "call-1", ToolError: errors.New("denied")})
	tracker.Observe(agentpkg.Event{Type: agentpkg.EventToolResult, ToolCallID: "call-2", ToolError: errors.New("failed")})

	calls, names, errs := tracker.Summary()
	if calls != 2 {
		t.Fatalf("tool calls = %d, want 2 (deduped by ID + one without ID)", calls)
	}
	if names["read"] != 2 || names["bash"] != 1 {
		t.Fatalf("tool names = %#v", names)
	}
	if !errs["call-1"] || !errs["call-2"] || len(errs) != 2 {
		t.Fatalf("tool errors = %#v", errs)
	}
}

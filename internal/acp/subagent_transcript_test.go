package acp

import (
	"encoding/json"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agent"
)

func newSubagentFixture(t *testing.T) (*server, *syncedBuffer, *agent.AgentManager) {
	t.Helper()
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	manager := agent.NewAgentManager(&agent.AgentFactory{})
	if _, err := manager.Create(agent.AgentOptions{ID: "main"}); err != nil {
		t.Fatalf("create main agent: %v", err)
	}
	if _, err := manager.Create(agent.AgentOptions{
		ID: "child-1", ParentID: "main", MemberID: "engineer", ExpertID: "software-company",
		MemberDisplayName: "工程师", MemberEmoji: "🛠️", MemberRole: "member",
	}); err != nil {
		t.Fatalf("create child agent: %v", err)
	}
	s.agentMgr = manager
	s.sessions["session-1"] = &sessionRuntime{id: "session-1", agentMgr: manager}
	s.observeSubagentEvent("session-1", agentpkg.Event{
		AgentID: agentpkg.AgentID("child-1"), Type: agentpkg.EventTextDelta,
		MemberID: "engineer", ExpertID: "software-company",
		MemberDisplayName: "工程师", MemberEmoji: "🛠️", MemberRole: "member",
	})
	return s, output, manager
}

func decodeSubagentResponse(t *testing.T, output string) map[string]any {
	t.Helper()
	messages := parseACPMessages(t, output)
	if len(messages) != 1 {
		t.Fatalf("messages = %#v, want one response", messages)
	}
	result, _ := messages[0]["result"].(map[string]any)
	if result == nil {
		t.Fatalf("response = %#v, want a result", messages[0])
	}
	return result
}

func TestACPSubagentListProjectsManagedChildren(t *testing.T) {
	s, output, manager := newSubagentFixture(t)
	output.Reset()
	manager.MarkRunning(agentpkg.AgentID("child-1"))
	child, _ := manager.Get(agentpkg.AgentID("child-1"))
	child.SetMessages([]agentpkg.Message{
		{Role: agentpkg.RoleUser, Content: "inspect"},
		{Role: agentpkg.RoleAssistant, Contents: []agentpkg.ContentBlock{{Type: "text", Text: "done"}}},
	})

	params, _ := json.Marshal(map[string]any{"sessionId": "session-1"})
	s.handleSubagentList(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/subagent/list", Params: params})
	result := decodeSubagentResponse(t, output.String())
	agents, _ := result["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("agents = %#v, want one child", result["agents"])
	}
	info, _ := agents[0].(map[string]any)
	if info["agentId"] != "child-1" || info["parentAgentId"] != "main" {
		t.Fatalf("agent identity = %#v", info)
	}
	if info["status"] != "running" || info["active"] != true || info["messageCount"] != float64(2) {
		t.Fatalf("agent status = %#v, want running/active/2", info)
	}
	if info["memberDisplayName"] != "工程师" || info["memberEmoji"] != "🛠️" || info["memberRole"] != "member" {
		t.Fatalf("member metadata = %#v", info)
	}

	output.Reset()
	manager.MarkDone(agentpkg.AgentID("child-1"), "finished")
	s.handleSubagentList(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "mothx/subagent/list", Params: params})
	result = decodeSubagentResponse(t, output.String())
	agents, _ = result["agents"].([]any)
	info, _ = agents[0].(map[string]any)
	if info["status"] != "completed" || info["lastResponse"] != "finished" {
		t.Fatalf("terminal status = %#v", info)
	}
}

func TestACPSubagentMessagesProjectChildTranscriptAsSessionUpdates(t *testing.T) {
	s, output, manager := newSubagentFixture(t)
	output.Reset()
	child, _ := manager.Get(agentpkg.AgentID("child-1"))
	child.SetMessages([]agentpkg.Message{
		{Role: agentpkg.RoleUser, Content: "inspect the file"},
		{Role: agentpkg.RoleAssistant, Contents: []agentpkg.ContentBlock{
			{Type: "thinking", Thinking: "checking"},
			{Type: "text", Text: "reading"},
			{Type: "toolCall", ToolCall: &agentpkg.ToolCallBlock{ID: "call-1", Name: "read", Arguments: []byte(`{"path":"main.go"}`)}},
		}},
		{Role: agentpkg.RoleToolResult, ToolCallID: "call-1", ToolName: "read", Content: "package main"},
	})

	params, _ := json.Marshal(map[string]any{"sessionId": "session-1", "agentId": "child-1"})
	s.handleSubagentMessages(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/subagent/messages", Params: params})
	result := decodeSubagentResponse(t, output.String())
	updates, _ := result["updates"].([]any)
	kinds := make([]string, 0, len(updates))
	for _, raw := range updates {
		update, _ := raw.(map[string]any)
		kinds = append(kinds, update["sessionUpdate"].(string))
	}
	want := []string{"user_message_chunk", "agent_thought_chunk", "agent_message_chunk", "tool_call", "tool_call_update"}
	if len(kinds) != len(want) {
		t.Fatalf("updates = %#v, want %#v", kinds, want)
	}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("updates = %#v, want %#v", kinds, want)
		}
	}
}

func TestACPSubagentMessagesRejectsUnknownSessionChild(t *testing.T) {
	s := newPhase1FixtureServer(&syncedBuffer{})
	output := s.w.(*syncedBuffer)
	params, _ := json.Marshal(map[string]any{"sessionId": "session-1", "agentId": "missing"})
	s.handleSubagentMessages(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/subagent/messages", Params: params})
	messages := parseACPMessages(t, output.String())
	assertACPRPCErrorCode(t, messages[0], "subagent_not_found")
}

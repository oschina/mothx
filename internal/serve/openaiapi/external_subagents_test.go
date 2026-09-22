package openaiapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/esm"
	"golang.org/x/net/websocket"
)

// External child terminal events are projections from channel-owned managers.
// They may update the child history but are never an ESM continuation trigger:
// only explicit user ESM mutations start the coordinator.
func TestExternalMemberTerminalDoesNotStartESMContinuation(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	const sessionID = "external-member-terminal-no-esm"
	if _, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := srv.esmStore().Create(context.Background(), sessionID, "complete the delivery without an auto-start"); err != nil {
		t.Fatalf("create ESM objective: %v", err)
	}

	srv.PublishExternalSubAgentEvent(sessionID, agent.Event{
		Type: agent.EventRunFinished, AgentID: "member-terminal", MemberID: "software-engineer",
		ExpertID: "software-company", MemberDisplayName: "工程师", Status: agent.TaskSuccess,
	})

	if srv.esmCoordinator != nil || srv.esmCoordinatorRunning(sessionID) {
		t.Fatal("member terminal event started an ESM coordinator without a user ESM mutation")
	}
	obj, err := srv.esmStore().Get(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("get ESM objective: %v", err)
	}
	if obj.Status != esm.StatusActive {
		t.Fatalf("member terminal changed ESM objective status to %q", obj.Status)
	}
	children, err := srv.GetSessionSubAgents(sessionID)
	if err != nil {
		t.Fatalf("get projected child state: %v", err)
	}
	if len(children) != 1 || children[0].Status != "done" || children[0].MemberID != "software-engineer" {
		t.Fatalf("child terminal projection = %#v", children)
	}
}

func TestExternalSubAgentEventsExposeHistoryAndPublishLiveUpdates(t *testing.T) {
	srv := &Server{eventBroker: NewEventBroker(), pool: NewSessionPool(0, 0)}
	events, cancel := srv.eventBroker.Subscribe("wechat-session")
	defer cancel()

	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{
		Type: agent.EventTextDelta, AgentID: "child-1", TextDelta: "working",
		MemberID: "engineer", ExpertID: "software-company", MemberDisplayName: "工程师", MemberEmoji: "🛠️", MemberRole: "member",
	})
	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{
		Type: agent.EventToolCall, AgentID: "child-1", ToolCallID: "call-1", ToolName: "grep", ToolArgs: map[string]any{"pattern": "TODO"},
	})
	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{
		Type: agent.EventToolExecutionEnd, AgentID: "child-1", ToolCallID: "call-1", ToolName: "grep", ToolResult: "found",
	})
	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{
		Type: agent.EventDone, AgentID: "child-1",
	})

	agents, err := srv.GetSessionSubAgents("wechat-session")
	if err != nil {
		t.Fatalf("GetSessionSubAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "child-1" {
		t.Fatalf("agents = %#v", agents)
	}
	if agents[0].Status != "done" || agents[0].Active || agents[0].MessageCount != 4 {
		t.Fatalf("agent status = %#v", agents[0])
	}
	if agents[0].MemberID != "engineer" || agents[0].ExpertID != "software-company" || agents[0].MemberDisplayName != "工程师" || agents[0].MemberEmoji != "🛠️" || agents[0].MemberRole != "member" {
		t.Fatalf("agent member metadata = %#v", agents[0])
	}

	messages, err := srv.GetSessionSubAgentMessages("wechat-session", "child-1")
	if err != nil {
		t.Fatalf("GetSessionSubAgentMessages: %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Role != "assistant" || messages[0].Content != "working" {
		t.Fatalf("first message = %#v", messages[0])
	}
	if messages[1].Role != "toolCall" || messages[1].ToolName != "grep" {
		t.Fatalf("tool call = %#v", messages[1])
	}
	if messages[2].Role != "toolResult" || messages[2].ToolName != "grep" {
		t.Fatalf("tool result = %#v", messages[2])
	}
	if messages[3].Role != "status" || messages[3].Content != "done" {
		t.Fatalf("completion = %#v", messages[3])
	}

	var gotTranscript, gotTool, gotDone bool
	for i := 0; i < 4; i++ {
		ev := <-events
		switch ev.Event {
		case "transcript":
			gotTranscript = true
			if item, ok := ev.Data.(TranscriptStreamEvent); ok && item.Type == "subagent_status" {
				gotDone = true
			}
		case "tool_event":
			gotTool = true
		}
	}
	if !gotTranscript || !gotTool || !gotDone {
		t.Fatalf("live events transcript=%v tool=%v done=%v", gotTranscript, gotTool, gotDone)
	}

	if _, err := srv.GetSessionSubAgentMessages("wechat-session", "unknown"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unknown agent error = %v, want ErrSessionNotFound", err)
	}
}

func TestExternalSubAgentEventsReachWebSocketClient(t *testing.T) {
	srv := NewExternalSubAgentServer()
	defer srv.pool.Stop()
	wsServer := httptest.NewServer(srv.RunWebSocketHandler())
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	ws, err := websocket.Dial(wsURL, "", wsServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer ws.Close()

	if err := websocket.JSON.Send(ws, map[string]any{"type": "hello", "clientId": "external-subagent-test"}); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	var ready map[string]any
	if err := websocket.JSON.Receive(ws, &ready); err != nil {
		t.Fatalf("receive ready: %v", err)
	}
	if ready["type"] != "ready" {
		t.Fatalf("ready message = %#v", ready)
	}
	if err := websocket.JSON.Send(ws, map[string]any{
		"type":          "subscribe",
		"subscriptions": []map[string]any{{"sessionId": "wechat-session"}},
	}); err != nil {
		t.Fatalf("send subscribe: %v", err)
	}
	var subscribed map[string]any
	if err := websocket.JSON.Receive(ws, &subscribed); err != nil {
		t.Fatalf("receive subscribed: %v", err)
	}
	if subscribed["type"] != "subscribed" || subscribed["sessionId"] != "wechat-session" {
		t.Fatalf("subscribed message = %#v", subscribed)
	}

	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{Type: agent.EventTextDelta, AgentID: "child-1", TextDelta: "live"})
	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{Type: agent.EventDone, AgentID: "child-1"})

	var gotText, gotDone bool
	for !(gotText && gotDone) {
		var event struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Event     string `json:"event"`
			Data      struct {
				Type    string `json:"type"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"data"`
		}
		if err := ws.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("set websocket deadline: %v", err)
		}
		if err := websocket.JSON.Receive(ws, &event); err != nil {
			t.Fatalf("receive live event: %v (text=%v done=%v)", err, gotText, gotDone)
		}
		if event.Type != "session_event" || event.SessionID != "wechat-session" || event.Event != "transcript" {
			continue
		}
		if event.Data.Type == "assistant_delta" && event.Data.Message.Content == "live" {
			gotText = true
		}
		if event.Data.Type == "subagent_status" && event.Data.Message.Content == "done" {
			gotDone = true
		}
	}
}

func TestExternalSubAgentTerminalEventDeduplicated(t *testing.T) {
	srv := &Server{eventBroker: NewEventBroker(), pool: NewSessionPool(0, 0)}

	// The parent event stream and the AgentManager status listener can both
	// deliver the same terminal event; the sink must record it only once.
	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{Type: agent.EventTextDelta, AgentID: "child-1", TextDelta: "work"})
	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{Type: agent.EventDone, AgentID: "child-1"})
	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{Type: agent.EventDone, AgentID: "child-1"})
	// Anything after the terminal state is a duplicate or out-of-order straggler.
	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{Type: agent.EventTextDelta, AgentID: "child-1", TextDelta: "late"})

	agents, err := srv.GetSessionSubAgents("wechat-session")
	if err != nil {
		t.Fatalf("GetSessionSubAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].Status != "done" || agents[0].Active {
		t.Fatalf("agents = %#v", agents)
	}
	if agents[0].MessageCount != 2 {
		t.Fatalf("message count = %d, want 2 (text + done)", agents[0].MessageCount)
	}
	messages, err := srv.GetSessionSubAgentMessages("wechat-session", "child-1")
	if err != nil {
		t.Fatalf("GetSessionSubAgentMessages: %v", err)
	}
	if len(messages) != 2 || messages[1].Role != "status" || messages[1].Content != "done" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestExternalSubAgentTerminalFallbackRecoversQueuedAssistantText(t *testing.T) {
	srv := &Server{eventBroker: NewEventBroker(), pool: NewSessionPool(0, 0)}
	events, cancel := srv.eventBroker.Subscribe("wechat-session")
	defer cancel()

	// The AgentManager terminal listener can overtake text already queued on the
	// parent stream. Its status snapshot carries the complete persisted result so
	// the projection can fill the missing suffix before publishing the terminal.
	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{
		Type: agent.EventTextDelta, AgentID: "child-1", TextDelta: "child ",
	})
	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{
		Type: agent.EventRunFinished, AgentID: "child-1", Status: agent.TaskSuccess, StatusMessage: "child result",
	})
	srv.PublishExternalSubAgentEvent("wechat-session", agent.Event{
		Type: agent.EventTextDelta, AgentID: "child-1", TextDelta: "result",
	})

	messages, err := srv.GetSessionSubAgentMessages("wechat-session", "child-1")
	if err != nil {
		t.Fatalf("GetSessionSubAgentMessages: %v", err)
	}
	if len(messages) != 2 || messages[0].Role != "assistant" || messages[0].Content != "child result" || messages[1].Role != "status" || messages[1].Content != "done" {
		t.Fatalf("messages = %#v", messages)
	}

	var projected []TranscriptStreamEvent
	for i := 0; i < 3; i++ {
		ev := <-events
		if item, ok := ev.Data.(TranscriptStreamEvent); ok {
			projected = append(projected, item)
		}
	}
	if len(projected) != 3 || projected[0].Type != "assistant_delta" || projected[0].Message.Content != "child " || projected[1].Type != "assistant_delta" || projected[1].Message.Content != "result" || projected[2].Type != "subagent_status" {
		t.Fatalf("projected events = %#v", projected)
	}
}

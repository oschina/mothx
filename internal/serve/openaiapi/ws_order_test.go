package openaiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/provider"
	"golang.org/x/net/websocket"
)

// scriptedToolProvider emits a deterministic sequence: text delta, a read
// tool call, then on the second call a final text delta. Used to verify the
// WebSocket event order matches the agent emission order.
type scriptedToolProvider struct {
	mu     sync.Mutex
	calls  int
	models []*provider.Model
	script func(call int, ch chan provider.StreamEvent)
}

func newScriptedToolProvider() *scriptedToolProvider {
	p := &scriptedToolProvider{
		models: []*provider.Model{{ID: "m1", Name: "Model 1", ContextWindow: 32768, MaxTokens: 2048}},
	}
	p.script = func(call int, ch chan provider.StreamEvent) {
		if call == 1 {
			ch <- provider.StreamEvent{Type: provider.StreamStart}
			ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "第一部分。"}
			args, _ := json.Marshal(map[string]any{"path": "note.txt"})
			ch <- provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{ID: "c1", Name: "read", Arguments: args}}
			ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "tool_calls"}
			return
		}
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "总结。"}
		ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "stop"}
	}
	return p
}

func (p *scriptedToolProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		p.script(call, ch)
	}()
	return ch
}

func (p *scriptedToolProvider) Name() string              { return "scripted" }
func (p *scriptedToolProvider) API() string               { return "openai-chat" }
func (p *scriptedToolProvider) Models() []*provider.Model { return p.models }
func (p *scriptedToolProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func (p *scriptedToolProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestSubmitRunWebSocketEventOrder(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg.DefaultMode = "yolo"
	p := newScriptedToolProvider()
	srv.provider = p
	srv.model = p.models[0]
	srv.eventBroker = NewEventBroker()

	// A file for the read tool.
	workDir := srv.cfg.GetWorkDir()
	if err := writeTestFile(workDir, "note.txt", "hello"); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	sessionID := "ws-order-session"

	// WebSocket client first — subscribe before submitting, like the WebUI.
	wsSrv := httptest.NewServer(srv.RunWebSocketHandler())
	defer wsSrv.Close()
	wsURL := "ws" + strings.TrimPrefix(wsSrv.URL, "http")
	ws, err := websocket.Dial(wsURL, "", "http://localhost/")
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer ws.Close()

	subscribe := map[string]any{
		"type": "subscribe",
		"subscriptions": []map[string]any{{
			"sessionId": sessionID,
			"cursor":    map[string]any{"entrySeq": 0, "runSeq": 0, "capabilitySeq": 0},
		}},
	}
	if err := websocket.JSON.Send(ws, subscribe); err != nil {
		t.Fatalf("send subscribe: %v", err)
	}
	// Give the server a moment to register the subscription before the run starts.
	time.Sleep(100 * time.Millisecond)

	// Submit the run.
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/runs", strings.NewReader(`{"message":"hi","transcript":true}`))
	w := httptest.NewRecorder()
	srv.HandleSubmitRun(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}

	// Collect frames until done.
	type frame struct {
		event   string
		summary string
	}
	var frames []frame
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var ev runWebSocketEvent
		if err := websocket.JSON.Receive(ws, &ev); err != nil {
			t.Fatalf("receive: %v (frames so far: %v)", err, frames)
		}
		if ev.Type != "session_event" {
			continue
		}
		summary := ""
		switch ev.Event {
		case "transcript":
			if m, ok := ev.Data.(map[string]any); ok {
				summary = fmt.Sprintf("%v/%v", m["type"], transcriptContent(m))
			}
		case "tool_event":
			if m, ok := ev.Data.(map[string]any); ok {
				summary = fmt.Sprintf("%v:%v", m["tool"], m["status"])
			}
		}
		frames = append(frames, frame{event: ev.Event, summary: summary})
		if ev.Event == "done" {
			break
		}
	}

	var order []string
	for _, f := range frames {
		if f.event == "transcript" || f.event == "tool_event" {
			order = append(order, fmt.Sprintf("%s(%s)", f.event, f.summary))
		}
	}
	t.Logf("frame order: %v", order)

	// Verify relative order: first text delta, then tool running, then tool
	// completed, then final text delta.
	idx := func(match string) int {
		for i, s := range order {
			if strings.Contains(s, match) {
				return i
			}
		}
		return -1
	}
	intro := idx("第一部分")
	toolRunning := idx("read:running")
	toolCompleted := idx("read:completed")
	final := idx("总结")
	if intro < 0 || toolRunning < 0 || toolCompleted < 0 || final < 0 {
		t.Fatalf("missing frames: intro=%d running=%d completed=%d final=%d order=%v", intro, toolRunning, toolCompleted, final, order)
	}
	if !(intro < toolRunning && toolRunning < toolCompleted && toolCompleted < final) {
		t.Fatalf("frames out of order: intro=%d running=%d completed=%d final=%d order=%v", intro, toolRunning, toolCompleted, final, order)
	}
}

func TestSubmitRunPersistsFinalAssistantMessage(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	completed := make(chan string, 1)
	srv.SetRunCompleteObserver(func(_, runID, status, _ string) {
		if status == "completed" {
			completed <- runID
		}
	})
	srv.cfg.DefaultMode = "yolo"
	p := newScriptedToolProvider()
	srv.provider = p
	srv.model = p.models[0]
	workDir := srv.cfg.GetWorkDir()
	if err := writeTestFile(workDir, "note.txt", "hello"); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	const sessionID = "persist-assistant-session"
	w := submitRun(t, srv, sessionID, `{"message":"hi","transcript":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}
	var accepted struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil || accepted.RunID == "" {
		t.Fatalf("decode submit response: %v body=%s", err, w.Body.String())
	}
	select {
	case runID := <-completed:
		if runID != accepted.RunID {
			t.Fatalf("completion observer run = %q, want %q", runID, accepted.RunID)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for completed run %q (provider calls=%d)", accepted.RunID, p.callCount())
	}
	runs, err := srv.GetSessionRunEvents(sessionID)
	if err != nil {
		t.Fatalf("get session run events: %v", err)
	}
	for _, event := range runs {
		if event.RunID == accepted.RunID && event.EventType == "finished" && event.Status == "completed" {
			messages, messageErr := srv.GetSessionMessages(sessionID)
			if messageErr != nil {
				t.Fatalf("get session messages: %v", messageErr)
			}
			for _, message := range messages {
				if message.Role == "assistant" && strings.Contains(message.Content, "总结") {
					return
				}
			}
			t.Fatalf("run finished but final assistant message is missing: messages=%#v", messages)
		}
	}
	messages, _ := srv.GetSessionMessages(sessionID)
	runs, _ = srv.GetSessionRunEvents(sessionID)
	t.Fatalf("final assistant message was not persisted: messages=%#v runs=%#v providerCalls=%d", messages, runs, p.callCount())
}

func transcriptContent(m map[string]any) string {
	msg, ok := m["message"].(map[string]any)
	if !ok {
		return ""
	}
	content, _ := msg["content"].(string)
	return content
}

func writeTestFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

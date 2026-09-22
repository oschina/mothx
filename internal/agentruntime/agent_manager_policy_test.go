package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
)

type managedPolicyProvider struct {
	model *provider.Model

	mu    sync.Mutex
	calls []provider.ChatParams
}

func (p *managedPolicyProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	p.calls = append(p.calls, params)
	callNumber := len(p.calls)
	p.mu.Unlock()
	events := make(chan provider.StreamEvent, 3)
	go func() {
		defer close(events)
		events <- provider.StreamEvent{Type: provider.StreamStart}
		if callNumber == 1 {
			arguments, _ := json.Marshal(map[string]any{"command": "sh -c 'echo should-not-run'"})
			events <- provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
				ID: "bash-1", Name: "bash", Arguments: arguments,
			}}
			events <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "tool_calls"}
			return
		}
		events <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "done"}
		events <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "stop"}
	}()
	return events
}

func (p *managedPolicyProvider) Name() string              { return "managed-policy" }
func (p *managedPolicyProvider) API() string               { return "openai-chat" }
func (p *managedPolicyProvider) Models() []*provider.Model { return []*provider.Model{p.model} }
func (p *managedPolicyProvider) GetModel(id string) *provider.Model {
	if p.model != nil && p.model.ID == id {
		return p.model
	}
	return nil
}

func TestAgentManagerAppliesBoundSessionPolicyWithoutParent(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	bound, err := CreateSession(CreateSessionOptions{
		WorkDir: workDir, SessionDir: sessionDir, ChannelType: "wechat", ChannelID: "background-policy-user",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	model := &provider.Model{ID: "managed-policy", ContextWindow: 32768, MaxTokens: 1024}
	recorder := &managedPolicyProvider{model: model}
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	runtime := &SessionRuntime{Source: SourceUnknown, EntrySource: SourceCron, WorkDir: workDir}
	manager, err := NewAgentManager(AgentManagerOptions{
		Runtime: runtime, Provider: recorder, Model: model, Settings: settings,
	})
	if err != nil {
		t.Fatalf("NewAgentManager: %v", err)
	}
	managed, err := manager.Create(agent.AgentOptions{
		Mode: ModeAgent, WorkDir: workDir, Session: bound,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for range managed.Run(context.Background(), "run command") {
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.calls) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(recorder.calls))
	}
	for _, message := range recorder.calls[1].Messages {
		if message.Role != "toolResult" || message.ToolName != "bash" {
			continue
		}
		var content strings.Builder
		content.WriteString(message.Content)
		for _, block := range message.Contents {
			content.WriteString(block.Text)
		}
		if !message.IsError || !strings.Contains(content.String(), "blocked high risk") {
			t.Fatalf("tool result = %#v, want policy block", message)
		}
		return
	}
	t.Fatal("provider follow-up did not receive bash tool result")
}

package channels

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/messaging"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/serve/hooks"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

type channelToolCallProvider struct {
	model   *provider.Model
	command string

	mu    sync.Mutex
	calls []provider.ChatParams
}

func (p *channelToolCallProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	p.calls = append(p.calls, params)
	callNumber := len(p.calls)
	p.mu.Unlock()

	events := make(chan provider.StreamEvent, 3)
	go func() {
		defer close(events)
		events <- provider.StreamEvent{Type: provider.StreamStart}
		if callNumber == 1 {
			arguments, _ := json.Marshal(map[string]any{"command": p.command})
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

func (p *channelToolCallProvider) Name() string              { return "channel-tool-call" }
func (p *channelToolCallProvider) API() string               { return "openai-chat" }
func (p *channelToolCallProvider) Models() []*provider.Model { return []*provider.Model{p.model} }
func (p *channelToolCallProvider) GetModel(id string) *provider.Model {
	if p.model != nil && p.model.ID == id {
		return p.model
	}
	return nil
}

func (p *channelToolCallProvider) toolResult() (provider.Message, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) < 2 {
		return provider.Message{}, false
	}
	for _, message := range p.calls[1].Messages {
		if message.Role == "toolResult" && message.ToolName == "bash" {
			return message, true
		}
	}
	return provider.Message{}, false
}

func channelToolResultText(message provider.Message) string {
	if message.Content != "" {
		return message.Content
	}
	var text strings.Builder
	for _, content := range message.Contents {
		if content.Type == "text" {
			text.WriteString(content.Text)
		}
	}
	return text.String()
}

type recordingChannelBashTool struct {
	executed *atomic.Bool
}

func (t recordingChannelBashTool) Name() string               { return "bash" }
func (t recordingChannelBashTool) Description() string        { return "record a bash execution" }
func (t recordingChannelBashTool) PromptSnippet() string      { return "record a bash execution" }
func (t recordingChannelBashTool) PromptGuidelines() []string { return nil }
func (t recordingChannelBashTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)
}
func (t recordingChannelBashTool) Execute(context.Context, map[string]any) (tools.ToolResult, error) {
	t.executed.Store(true)
	return tools.NewTextToolResult("executed"), nil
}

func TestChannelYoloHardRiskGuardRunsBeforeApproval(t *testing.T) {
	for _, tc := range []struct {
		name        string
		command     string
		wantExecute bool
	}{
		{name: "high risk blocked", command: "rm -rf /", wantExecute: false},
		{name: "medium risk allowed", command: "docker ps", wantExecute: true},
		{name: "low risk allowed", command: "go test ./...", wantExecute: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			settings := config.DefaultSettings()
			settings.SessionDir = t.TempDir()
			if len(settings.Approval.BashBlacklist) != 0 {
				t.Fatalf("default bash blacklist = %#v, want empty to exercise hard policy", settings.Approval.BashBlacklist)
			}
			cfg := DefaultConfig()
			cfg.WorkDir = workDir
			cfg.Security.SmartApprovals = false
			model := &provider.Model{ID: "channel-security", ContextWindow: 32768, MaxTokens: 1024}
			toolProvider := &channelToolCallProvider{model: model, command: tc.command}
			dispatcher := &Dispatcher{
				cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
				security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""), provider: toolProvider, model: model,
				sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
			}
			defer dispatcher.Close()

			sess, err := dispatcher.resolveSession("wechat", "security-user")
			if err != nil {
				t.Fatalf("resolveSession: %v", err)
			}
			if sess.Mode != "yolo" {
				t.Fatalf("channel mode = %q, want forced yolo", sess.Mode)
			}
			var executed atomic.Bool
			sess.Registry.Register(recordingChannelBashTool{executed: &executed})

			response, err := dispatcher.HandleMessage(context.Background(), messaging.InboundMessage{
				Platform: "wechat", UserID: "security-user", Text: "run the command",
			})
			if err != nil {
				t.Fatalf("HandleMessage: %v", err)
			}
			if response != "done" {
				t.Fatalf("response = %q, want done", response)
			}
			if got := executed.Load(); got != tc.wantExecute {
				t.Fatalf("bash executed = %v, want %v for %q", got, tc.wantExecute, tc.command)
			}
			result, ok := toolProvider.toolResult()
			if !ok {
				t.Fatal("provider follow-up did not receive bash tool result")
			}
			resultText := channelToolResultText(result)
			if tc.wantExecute {
				if result.IsError || resultText != "executed" {
					t.Fatalf("allowed tool result = %#v", result)
				}
			} else if !result.IsError || !strings.Contains(resultText, "blocked") || !strings.Contains(resultText, "high risk") {
				t.Fatalf("blocked tool result = %#v, want high-risk error", result)
			}
		})
	}
}

func TestChannelYoloPreToolHookRunsBeforeApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook fixture requires a POSIX shell")
	}
	workDir := t.TempDir()
	script := filepath.Join(t.TempDir(), "block-tool.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho '{\"action\":\"block\",\"reason\":\"blocked by test hook\"}'\n"), 0o700); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	cfg.Security.SmartApprovals = false
	model := &provider.Model{ID: "channel-hook", ContextWindow: 32768, MaxTokens: 1024}
	toolProvider := &channelToolCallProvider{model: model, command: "go test ./..."}
	dispatcher := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager(script, ""), provider: toolProvider, model: model,
		sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
	}
	defer dispatcher.Close()

	sess, err := dispatcher.resolveSession("wechat", "hook-user")
	if err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	var executed atomic.Bool
	sess.Registry.Register(recordingChannelBashTool{executed: &executed})

	if _, err := dispatcher.HandleMessage(context.Background(), messaging.InboundMessage{
		Platform: "wechat", UserID: "hook-user", Text: "run the command",
	}); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if executed.Load() {
		t.Fatal("pre-tool hook block was bypassed in yolo mode")
	}
	result, ok := toolProvider.toolResult()
	if !ok || !result.IsError || channelToolResultText(result) != "blocked by test hook" {
		t.Fatalf("hook tool result = %#v, want blocked error", result)
	}
}

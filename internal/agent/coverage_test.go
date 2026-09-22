package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/config"
	ctxpkg "github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/tools"
)

// --- Coverage helpers ---

func newTestRegistry(workDir string, sb sandbox.Sandbox) *tools.Registry {
	r := tools.NewRegistry(workDir, sb)
	r.RegisterDefaults()
	return r
}

func sandboxNewNone() sandbox.Sandbox {
	return sandbox.NewNoneSandbox()
}

func newMockProvider() provider.Provider {
	return provider.NewMockProvider("mock", []*provider.Model{
		{ID: "m1", Name: "Model 1", ContextWindow: 100000},
	}, nil)
}

func compactionSettings() ctxpkg.CompactionSettings {
	return ctxpkg.CompactionSettings{Enabled: false, ReserveTokens: 16384}
}

// --- Coverage tests ---

func TestAgentIDAndParentID(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "my-agent",
		ParentID: "parent-agent",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1"},
		Mode:     "agent",
	}
	a := New(cfg, registry)
	if a.ID() != "my-agent" {
		t.Errorf("expected 'my-agent', got %q", a.ID())
	}
	if a.ParentID() != "parent-agent" {
		t.Errorf("expected 'parent-agent', got %q", a.ParentID())
	}
}

func TestAgentAutoID(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1"},
		Mode:     "agent",
	}
	a := New(cfg, registry)
	if a.ID() == "" {
		t.Error("expected non-empty auto-generated ID")
	}
}

func TestAgentLoadHistoryMessages(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "test",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1"},
		Mode:     "agent",
	}
	a := New(cfg, registry)

	msgs := []provider.Message{
		provider.NewUserMessage("hello"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "hi there"}}),
	}
	a.LoadHistoryMessages(msgs)

	got := a.GetMessages()
	if len(got) != 2 {
		t.Errorf("expected 2 messages, got %d", len(got))
	}
}

func TestAgentEmit(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "emit-test",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1"},
		Mode:     "agent",
	}
	a := New(cfg, registry)

	ch := make(chan Event, 1)
	a.emit(ch, Event{Type: EventTextDelta, TextDelta: "hello"})

	e := <-ch
	if e.AgentID != "emit-test" {
		t.Errorf("expected 'emit-test', got %q", e.AgentID)
	}
	if e.TextDelta != "hello" {
		t.Errorf("expected 'hello', got %q", e.TextDelta)
	}
}

func TestAgentHandleApprovalResponse(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "test",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1"},
		Mode:     "agent",
	}
	a := New(cfg, registry)

	a.approvalMu.Lock()
	a.approvalCounter++
	approvalID := "approval-1"
	responseCh := make(chan bool, 1)
	a.pendingApprovals[approvalID] = responseCh
	a.approvalMu.Unlock()

	go a.HandleApprovalResponse(approvalID, true)

	select {
	case approved := <-responseCh:
		if !approved {
			t.Error("expected approved=true")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for approval response")
	}
}

func TestAgentHandleApprovalResponseNotFound(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "test",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1"},
		Mode:     "agent",
	}
	a := New(cfg, registry)
	a.HandleApprovalResponse("nonexistent", true) // Should not panic
}

func TestAgentGetContextUsageNilModel(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "test",
		Provider: newMockProvider(),
		Model:    nil,
		Mode:     "agent",
	}
	a := New(cfg, registry)
	if a.GetContextUsage() != nil {
		t.Error("expected nil for nil model")
	}
}

func TestAgentGetContextUsageZeroWindow(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "test",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1", ContextWindow: 0},
		Mode:     "agent",
	}
	a := New(cfg, registry)
	if a.GetContextUsage() != nil {
		t.Error("expected nil for zero context window")
	}
}

func TestAgentGetContextUsageWithMessages(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "test",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1", ContextWindow: 100000},
		Mode:     "agent",
	}
	a := New(cfg, registry)
	a.LoadHistoryMessages([]provider.Message{provider.NewUserMessage("hello world")})

	usage := a.GetContextUsage()
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.Tokens <= 0 {
		t.Errorf("expected positive tokens, got %d", usage.Tokens)
	}
	if usage.ContextWindow != 100000 {
		t.Errorf("expected 100000, got %d", usage.ContextWindow)
	}
}

func TestAgentNewWithLoopConfigAutoID(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := AgentLoopConfig{
		Config: Config{
			Provider: newMockProvider(),
			Model:    &provider.Model{ID: "m1"},
			Mode:     "agent",
		},
	}
	a := NewWithLoopConfig(cfg, registry)
	if a.ID() == "" {
		t.Error("expected non-empty auto-generated ID")
	}
}

// --- Bridge coverage ---

func TestMessagesFromPublic(t *testing.T) {
	pub := []agentpkg.Message{
		agentpkg.NewUserMessage("hello"),
		agentpkg.NewAssistantTextMessage("world"),
	}
	internal := MessagesFromPublic(pub)
	if len(internal) != 2 {
		t.Fatalf("expected 2, got %d", len(internal))
	}
	if internal[0].Role != "user" {
		t.Errorf("expected 'user', got %q", internal[0].Role)
	}
}

func TestMessagesToPublic(t *testing.T) {
	internal := []provider.Message{
		provider.NewUserMessage("hello"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "world"}}),
	}
	pub := MessagesToPublic(internal)
	if len(pub) != 2 {
		t.Fatalf("expected 2, got %d", len(pub))
	}
	if pub[0].Role != agentpkg.RoleUser {
		t.Errorf("expected 'user', got %q", pub[0].Role)
	}
}

func TestAgentAdapterAllMethods(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "adapter-test",
		ParentID: "parent",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1", ContextWindow: 100000},
		Mode:     "agent",
	}
	a := New(cfg, registry)
	adapter := NewAgentAdapter(a)

	if adapter.ID() != "adapter-test" {
		t.Errorf("expected 'adapter-test', got %q", adapter.ID())
	}
	if adapter.ParentID() != "parent" {
		t.Errorf("expected 'parent', got %q", adapter.ParentID())
	}

	adapter.Abort()
	msgs := adapter.GetMessages()
	if msgs == nil {
		msgs = []agentpkg.Message{}
	}
	adapter.SetMessages([]agentpkg.Message{agentpkg.NewUserMessage("test")})

	ctx := adapter.GetContext()
	if ctx == nil {
		t.Error("expected non-nil context")
	}
	adapter.SetContext(&agentpkg.AgentContext{SystemPrompt: "test"})

	adapter.LoadHistoryMessages([]agentpkg.Message{agentpkg.NewUserMessage("hello")})
	usage := adapter.GetContextUsage()
	if usage == nil {
		t.Error("expected non-nil usage")
	}

	adapter.HandleApprovalResponse("nonexistent", true)
}

func TestAdapterRunWithMessages(t *testing.T) {
	responses := []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "hi"},
		{Type: provider.StreamDone},
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "m1", Name: "Model 1"},
	}, responses)
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "test",
		Provider: mockProvider,
		Model:    &provider.Model{ID: "m1"},
		Mode:     "agent",
	}
	a := New(cfg, registry)
	adapter := NewAgentAdapter(a)

	ch := adapter.RunWithMessages(context.Background(), []agentpkg.Message{
		agentpkg.NewUserMessage("test"),
	})
	var events []agentpkg.Event
	for e := range ch {
		events = append(events, e)
	}
	if len(events) == 0 {
		t.Error("expected events")
	}
}

// --- EventLoop coverage ---

func TestEventHandlerFunc(t *testing.T) {
	called := false
	f := EventHandlerFunc(func(ctx context.Context, e Event) error {
		called = true
		return nil
	})
	err := f.HandleAgentEvent(context.Background(), Event{})
	if err != nil || !called {
		t.Errorf("expected call, got err=%v called=%v", err, called)
	}
}

// --- Factory coverage ---

func TestAgentFactoryProviderAndSettings(t *testing.T) {
	mockProvider := newMockProvider()
	settings := &config.Settings{}
	factory := NewAgentFactory(mockProvider, nil, settings, nil, "", "", nil, compactionSettings(), nil)

	if factory.Provider() != mockProvider {
		t.Error("expected same provider")
	}
	if factory.Settings() != settings {
		t.Error("expected same settings")
	}
}

// --- PromptSnippet/PromptGuidelines coverage ---

func TestSubAgentPromptSnippets(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tools := []struct {
		name string
		fn   func() string
	}{
		{"subagent_spawn", func() string { return NewSubAgentSpawnTool(mgr).PromptSnippet() }},
		{"subagent_status", func() string { return NewSubAgentStatusTool(mgr).PromptSnippet() }},
		{"subagent_send", func() string { return NewSubAgentSendTool(mgr).PromptSnippet() }},
		{"subagent_destroy", func() string { return NewSubAgentDestroyTool(mgr).PromptSnippet() }},
	}
	for _, tt := range tools {
		if tt.fn() == "" {
			t.Errorf("%s: expected non-empty PromptSnippet", tt.name)
		}
	}

	guidelines := NewSubAgentSpawnTool(mgr).PromptGuidelines()
	if len(guidelines) == 0 {
		t.Error("expected non-empty guidelines for spawn tool")
	}
	NewSubAgentStatusTool(mgr).PromptGuidelines()
	NewSubAgentSendTool(mgr).PromptGuidelines()
	NewSubAgentDestroyTool(mgr).PromptGuidelines()
}

// --- ConsumeEvents coverage ---

func TestConsumeEvents(t *testing.T) {
	ch := make(chan Event, 2)
	ch <- Event{Type: EventTextDelta, TextDelta: "hi"}
	ch <- Event{Type: EventDone}
	close(ch)

	var received []Event
	handler := EventHandlerFunc(func(ctx context.Context, e Event) error {
		received = append(received, e)
		return nil
	})

	err := ConsumeEvents(context.Background(), ch, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(received) != 2 {
		t.Errorf("expected 2 events, got %d", len(received))
	}
}

func TestConsumeEventsError(t *testing.T) {
	ch := make(chan Event, 1)
	ch <- Event{Type: EventError, Error: context.Canceled}
	close(ch)

	testErr := fmt.Errorf("handler error")
	handler := EventHandlerFunc(func(ctx context.Context, e Event) error {
		return testErr
	})

	err := ConsumeEvents(context.Background(), ch, handler)
	if err != testErr {
		t.Errorf("expected %v, got %v", testErr, err)
	}
}

func TestConsumeEventsContextCancel(t *testing.T) {
	ch := make(chan Event) // Never close
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	handler := EventHandlerFunc(func(ctx context.Context, e Event) error {
		return nil
	})

	err := ConsumeEvents(ctx, ch, handler)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// --- RequestApproval coverage ---

func TestAgentRequestApproval(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "test",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1"},
		Mode:     "agent",
	}
	a := New(cfg, registry)
	ch := make(chan Event, 10)

	// Request approval in background
	approvedCh := make(chan bool, 1)
	go func() {
		approvedCh <- a.RequestApproval(ch, "bash", map[string]any{"command": "ls"})
	}()

	// Wait for approval request event
	time.Sleep(50 * time.Millisecond)

	// Find the approval ID from events
	a.approvalMu.Lock()
	var approvalID string
	for id := range a.pendingApprovals {
		approvalID = id
		break
	}
	a.approvalMu.Unlock()

	if approvalID == "" {
		t.Fatal("expected pending approval")
	}

	// Approve it
	a.HandleApprovalResponse(approvalID, true)

	select {
	case approved := <-approvedCh:
		if !approved {
			t.Error("expected approved=true")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for approval")
	}
}

// --- NeedsApproval coverage ---

func TestAgentNeedsApproval(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	confirmWrite := true
	cfg := Config{
		ID:       "test",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1"},
		Mode:     "agent",
		Settings: &config.Settings{
			Approval: config.ApprovalSettings{
				ConfirmBeforeWrite: &confirmWrite,
				BashWhitelist:      []string{"git "},
				BashBlacklist:      []string{"rm "},
			},
		},
	}
	a := New(cfg, registry)

	// bash in agent mode needs approval
	if !a.NeedsApproval("bash", map[string]any{"command": "ls"}) {
		t.Error("expected bash needs approval in agent mode")
	}

	// whitelisted bash skips approval
	if a.NeedsApproval("bash", map[string]any{"command": "git status"}) {
		t.Error("expected whitelisted bash to skip approval")
	}

	// write in agent mode with confirmBeforeWrite
	if !a.NeedsApproval("write", map[string]any{"path": "/tmp/x"}) {
		t.Error("expected write needs approval")
	}

	// read never needs approval
	if a.NeedsApproval("read", map[string]any{"path": "/tmp/x"}) {
		t.Error("expected read to not need approval")
	}
}

func TestAgentNeedsApprovalYolo(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "test",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1"},
		Mode:     "yolo",
	}
	a := New(cfg, registry)

	if a.NeedsApproval("bash", map[string]any{"command": "rm -rf /"}) {
		t.Error("expected no approval in yolo mode")
	}
}

func TestAgentNeedsApprovalBlacklist(t *testing.T) {
	sb := sandboxNewNone()
	registry := newTestRegistry("/tmp", sb)
	cfg := Config{
		ID:       "test",
		Provider: newMockProvider(),
		Model:    &provider.Model{ID: "m1"},
		Mode:     "yolo",
		Settings: &config.Settings{
			Approval: config.ApprovalSettings{
				BashBlacklist: []string{"rm "},
			},
		},
	}
	a := New(cfg, registry)

	// blacklisted bash needs approval even in yolo
	if !a.NeedsApproval("bash", map[string]any{"command": "rm -rf /"}) {
		t.Error("expected blacklisted bash needs approval even in yolo")
	}
}

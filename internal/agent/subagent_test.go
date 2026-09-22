package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/config"
	ctxpkg "github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/skills"
	"github.com/oschina/mothx/internal/tools"
)

func newTestFactoryAndManager(t testing.TB) (*AgentFactory, *AgentManager) {
	t.Helper()

	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, []provider.StreamEvent{
		// Minimal normal completion. Real providers always emit at least
		// StreamDone; an empty stream would be classified as an empty
		// response by provider.ClassifyTurn and retried/errored instead of
		// finishing as "done".
		{Type: provider.StreamStart},
		{Type: provider.StreamDone, StopReason: "stop"},
	})

	sandboxMgr := sandbox.NewManager(t.TempDir())
	sandboxMgr.SetLevel(sandbox.LevelNone)
	settings := &config.Settings{SessionDir: t.TempDir()}

	factory := NewAgentFactory(
		mockProvider,
		mockProvider.Models()[0],
		settings,
		sandboxMgr,
		"",
		"",
		nil,
		ctxpkg.CompactionSettings{},
		nil,
	)
	return factory, NewAgentManager(factory)
}

func TestAgentManagerChildInheritsParentBeforeToolCallPolicy(t *testing.T) {
	_, manager := newTestFactoryAndManager(t)
	parentRegistry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	parent := NewWithLoopConfig(AgentLoopConfig{
		Config:     Config{ID: "parent", Mode: "yolo"},
		ForcedMode: "yolo",
		BeforeToolCall: func(BeforeToolCallContext) *ToolCallBlockResult {
			return &ToolCallBlockResult{Block: true, Reason: "inherited policy"}
		},
	}, parentRegistry)
	manager.Register(NewAgentAdapter(parent))

	child, err := manager.Create(AgentOptions{ParentID: parent.ID(), Mode: "plan", WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	childConfig, ok := runtimeConfigOfManagedAgent(child)
	if !ok || childConfig.BeforeToolCall == nil {
		t.Fatalf("child runtime config = %#v, want inherited pre-tool policy", childConfig)
	}
	if childConfig.Mode != "yolo" || childConfig.ForcedMode != "yolo" {
		t.Fatalf("child mode=%q forced=%q, want inherited yolo", childConfig.Mode, childConfig.ForcedMode)
	}
	decision := childConfig.BeforeToolCall(BeforeToolCallContext{})
	if decision == nil || !decision.Block || decision.Reason != "inherited policy" {
		t.Fatalf("child pre-tool decision = %#v", decision)
	}
}

func TestAgentManagerChildInheritsParentBeforeToolExecuteFence(t *testing.T) {
	_, manager := newTestFactoryAndManager(t)
	parentRegistry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	parent := NewWithLoopConfig(AgentLoopConfig{
		Config:     Config{ID: "parent-fence", Mode: "yolo"},
		ForcedMode: "yolo",
		BeforeToolExecute: func(BeforeToolExecuteContext) *ToolCallBlockResult {
			return &ToolCallBlockResult{Block: true, Reason: "inherited fence"}
		},
	}, parentRegistry)
	manager.Register(NewAgentAdapter(parent))

	child, err := manager.Create(AgentOptions{ParentID: parent.ID(), Mode: "plan", WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	childConfig, ok := runtimeConfigOfManagedAgent(child)
	if !ok || childConfig.BeforeToolExecute == nil {
		t.Fatalf("child runtime config = %#v, want inherited pre-execute fence", childConfig)
	}
	decision := childConfig.BeforeToolExecute(BeforeToolExecuteContext{})
	if decision == nil || !decision.Block || decision.Reason != "inherited fence" {
		t.Fatalf("child pre-execute decision = %#v", decision)
	}
}

func TestAgentFactoryWorkflowPromptNotInheritedByChild(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, nil)
	sandboxMgr := sandbox.NewManager(t.TempDir())
	sandboxMgr.SetLevel(sandbox.LevelNone)
	settings := &config.Settings{SessionDir: t.TempDir()}
	factory := NewAgentFactoryWithOptions(
		mockProvider,
		mockProvider.Models()[0],
		settings,
		sandboxMgr,
		"",
		"",
		nil,
		ctxpkg.CompactionSettings{},
		nil,
		AgentFactoryOptions{
			MultiAgentEnabled: true,
			DelegateEnabled:   true,
			WorkflowsEnabled:  true,
		},
	)
	mgr := NewAgentManager(factory)

	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	parentAdapter := parent.(*AgentAdapter)
	if !parentAdapter.inner.config.Workflows {
		t.Fatal("expected top-level factory agent to enable workflows")
	}
	if !contains(parentAdapter.inner.frozenSystemPrompt, "Workflow Tools") {
		t.Fatal("expected top-level prompt to include workflow instructions")
	}

	child, err := mgr.Create(AgentOptions{ID: "child", ParentID: "main"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	childAdapter := child.(*AgentAdapter)
	if childAdapter.inner.config.MultiAgent {
		t.Fatal("expected child agent to disable multi-agent prompt mode")
	}
	if childAdapter.inner.config.DelegateMode {
		t.Fatal("expected child agent to disable delegate prompt mode")
	}
	if childAdapter.inner.config.Workflows {
		t.Fatal("expected child agent to disable workflow prompt mode")
	}
	if contains(childAdapter.inner.frozenSystemPrompt, "Workflow Tools") {
		t.Fatal("expected child prompt to omit workflow instructions")
	}
}

func TestAgentFactoryStoresChildSessionsSeparately(t *testing.T) {
	factory, mgr := newTestFactoryAndManager(t)
	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := mgr.Create(AgentOptions{ID: "child", ParentID: parent.ID()})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	childSession := child.(*AgentAdapter).inner.config.Session
	if _, err := childSession.AppendMessage(provider.NewUserMessage("child work")); err != nil {
		t.Fatalf("append child message: %v", err)
	}

	db, err := session.OpenRootDB(factory.settings.GetSessionDir())
	if err != nil {
		t.Fatalf("open session db: %v", err)
	}
	var mainCount, subCount int
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM sessions").Scan(&mainCount); err != nil {
		t.Fatalf("count main sessions: %v", err)
	}
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM sub_session").Scan(&subCount); err != nil {
		t.Fatalf("count child sessions: %v", err)
	}
	if mainCount != 0 || subCount != 1 {
		t.Fatalf("session counts = main:%d sub:%d, want main:0 sub:1", mainCount, subCount)
	}
}

func TestAgentFactoryPropagatesProviderNameToChildren(t *testing.T) {
	mockProvider := provider.NewMockProvider("underlying-vendor", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, nil)
	sandboxMgr := sandbox.NewManager(t.TempDir())
	sandboxMgr.SetLevel(sandbox.LevelNone)
	settings := &config.Settings{SessionDir: t.TempDir()}
	factory := NewAgentFactoryWithOptions(
		mockProvider,
		mockProvider.Models()[0],
		settings,
		sandboxMgr,
		"",
		"",
		nil,
		ctxpkg.CompactionSettings{},
		nil,
		AgentFactoryOptions{
			MultiAgentEnabled: true,
			ProviderName:      "configured-provider",
		},
	)
	mgr := NewAgentManager(factory)

	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	parentAdapter := parent.(*AgentAdapter)
	if got := parentAdapter.inner.config.Vendor; got != "configured-provider" {
		t.Fatalf("parent vendor = %q, want configured-provider", got)
	}

	child, err := mgr.Create(AgentOptions{ID: "child", ParentID: "main"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	childAdapter := child.(*AgentAdapter)
	if got := childAdapter.inner.config.Vendor; got != "configured-provider" {
		t.Fatalf("child vendor = %q, want configured-provider", got)
	}

	nextProvider := provider.NewMockProvider("next-underlying-vendor", []*provider.Model{
		{ID: "model2", Name: "Model 2"},
	}, nil)
	mgr.UpdateRuntimeConfig(nextProvider, "next-configured-provider", nextProvider.Models()[0], settings, nil)
	next, err := mgr.Create(AgentOptions{ID: "after-switch"})
	if err != nil {
		t.Fatalf("create after-switch agent: %v", err)
	}
	nextAdapter := next.(*AgentAdapter)
	if got := nextAdapter.inner.config.Vendor; got != "next-configured-provider" {
		t.Fatalf("updated vendor = %q, want next-configured-provider", got)
	}
}

func TestSubAgentSpawnTool(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentSpawnTool(mgr)

	if tool.Name() != "subagent_spawn" {
		t.Errorf("expected 'subagent_spawn', got %q", tool.Name())
	}

	result, err := tool.Execute(context.Background(), map[string]any{
		"task": "list files",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if parsed["handle"] == nil || parsed["handle"] == "" {
		t.Error("expected non-empty handle")
	}
	if parsed["status"] != "running" {
		t.Errorf("expected 'running', got %q", parsed["status"])
	}
	handle, _ := parsed["handle"].(string)
	waitForManagedAgentToStop(t, mgr, agentpkg.AgentID(handle))
	if err := mgr.Destroy(agentpkg.AgentID(handle)); err != nil {
		t.Fatalf("destroy spawned agent: %v", err)
	}
}

func waitForManagedAgentToStop(t testing.TB, mgr *AgentManager, id agentpkg.AgentID) {
	t.Helper()
	// Generous budget: under -race on loaded machines the full child run
	// (session init, prompt build, stream, persistence) can exceed 1s.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, ok := mgr.Status(id)
		if ok && (st.State == "done" || st.State == "error") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for agent %s to stop", id)
}

func TestDelegateSubAgentTool(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mgr.Create(AgentOptions{ID: "main"})
	tool := NewDelegateSubAgentTool(mgr)
	ctx := ContextWithAgentID(context.Background(), "main")

	result, err := tool.Execute(ctx, map[string]any{"task": "summarize"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if parsed["status"] != "done" {
		t.Fatalf("expected done status, got %q", parsed["status"])
	}
	handle, _ := parsed["handle"].(string)
	if handle == "" {
		t.Fatal("expected delegate result to include handle")
	}
	if st, ok := mgr.Status(agentpkg.AgentID(handle)); !ok || st.State != "done" {
		t.Fatalf("expected retained delegated child status, got %#v ok=%v", st, ok)
	}
	if _, ok := mgr.Get(agentpkg.AgentID(handle)); !ok {
		t.Fatal("expected delegated child messages to remain inspectable")
	}
	if children := mgr.Children("main"); len(children) != 0 {
		t.Fatalf("expected delegated child cleanup, got %v", children)
	}
}

func TestDelegateSubAgentToolBlocksUntilChildCompletes(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mockProvider := &blockingSubAgentProvider{
		model:   &provider.Model{ID: "model1", Name: "Model 1"},
		started: started,
		release: release,
	}
	sandboxMgr := sandbox.NewManager(t.TempDir())
	sandboxMgr.SetLevel(sandbox.LevelNone)
	settings := &config.Settings{SessionDir: t.TempDir()}
	factory := NewAgentFactory(mockProvider, mockProvider.model, settings, sandboxMgr, "", "", nil, ctxpkg.CompactionSettings{}, nil)
	mgr := NewAgentManager(factory)
	if _, err := mgr.Create(AgentOptions{ID: "main"}); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	resultCh := make(chan tools.ToolResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := NewDelegateSubAgentTool(mgr).Execute(ContextWithAgentID(context.Background(), "main"), map[string]any{"task": "wait for final result"})
		resultCh <- result
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("delegated child did not start")
	}
	// The provider cannot complete before release is closed. Use a
	// non-blocking check here instead of sleeping for an arbitrary interval;
	// the latter makes this ordering test sensitive to scheduler/load noise.
	select {
	case <-resultCh:
		t.Fatal("delegate returned before the child completed")
	default:
	}

	close(release)
	select {
	case result := <-resultCh:
		if err := <-errCh; err != nil {
			t.Fatalf("delegate returned error: %v", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
			t.Fatalf("parse delegate result: %v", err)
		}
		if parsed["status"] != "done" || parsed["result"] != "final child result" {
			t.Fatalf("delegate result = %#v, want completed child result", parsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("delegate did not return after the child completed")
	}
}

type blockingSubAgentProvider struct {
	model   *provider.Model
	started chan struct{}
	release chan struct{}
}

func (p *blockingSubAgentProvider) Chat(ctx context.Context, _ provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 2)
	go func() {
		defer close(ch)
		select {
		case <-p.started:
		default:
			close(p.started)
		}
		select {
		case <-ctx.Done():
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err()}
		case <-p.release:
			ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "final child result"}
			ch <- provider.StreamEvent{Type: provider.StreamDone}
		}
	}()
	return ch
}

func (p *blockingSubAgentProvider) Name() string                    { return "blocking" }
func (p *blockingSubAgentProvider) API() string                     { return "mock" }
func (p *blockingSubAgentProvider) Models() []*provider.Model       { return []*provider.Model{p.model} }
func (p *blockingSubAgentProvider) GetModel(string) *provider.Model { return p.model }

func TestSubAgentSpawnInheritsYoloMode(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	parent, err := mgr.Create(AgentOptions{ID: "main", Mode: "yolo"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := mgr.Create(AgentOptions{ID: "sub-1", ParentID: parent.ID()})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	childAdapter := child.(*AgentAdapter)
	if childAdapter.inner.config.Mode != "yolo" {
		t.Fatalf("child mode = %q, want yolo", childAdapter.inner.config.Mode)
	}
	if !contains(childAdapter.inner.frozenSystemPrompt, "YOLO") {
		t.Fatal("expected child prompt to use yolo mode")
	}
}

func TestSubAgentInheritsRegisteredParentProvider(t *testing.T) {
	oldProvider := provider.NewMockProvider("old", []*provider.Model{
		{ID: "old-model", Name: "Old Model"},
	}, nil)
	newProvider := provider.NewMockProvider("new", []*provider.Model{
		{ID: "new-model", Name: "New Model"},
	}, nil)
	sandboxMgr := sandbox.NewManager(t.TempDir())
	sandboxMgr.SetLevel(sandbox.LevelNone)
	settings := &config.Settings{SessionDir: t.TempDir()}
	factory := NewAgentFactory(
		oldProvider,
		oldProvider.Models()[0],
		settings,
		sandboxMgr,
		"old context",
		"",
		nil,
		ctxpkg.CompactionSettings{},
		nil,
	)
	mgr := NewAgentManager(factory)

	parentRegistry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	parent := New(Config{
		ID:           "main",
		Provider:     newProvider,
		Model:        newProvider.Models()[0],
		Mode:         "agent",
		Settings:     settings,
		ExtraContext: "new context",
	}, parentRegistry)
	mgr.Register(NewAgentAdapter(parent))

	child, err := mgr.Create(AgentOptions{ID: "sub-1", ParentID: "main"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	childAdapter := child.(*AgentAdapter)
	gotProvider := childAdapter.inner.config.Provider
	if gotProvider != newProvider {
		gotName := "<nil>"
		if gotProvider != nil {
			gotName = gotProvider.Name()
		}
		t.Fatalf("child provider = %s, want registered parent provider %s", gotName, newProvider.Name())
	}
	if childAdapter.inner.config.Model == nil || childAdapter.inner.config.Model.ID != "new-model" {
		t.Fatalf("child model = %#v, want new-model", childAdapter.inner.config.Model)
	}
	if !contains(childAdapter.inner.config.ExtraContext, "new context") {
		t.Fatalf("child extra context = %q, want parent context", childAdapter.inner.config.ExtraContext)
	}
	if contains(childAdapter.inner.config.ExtraContext, "old context") {
		t.Fatalf("child extra context kept stale factory context: %q", childAdapter.inner.config.ExtraContext)
	}
}

func TestSubAgentSpawnToolInheritsParentYoloMode(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	parent, err := mgr.Create(AgentOptions{ID: "main", Mode: "yolo"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := NewSubAgentSpawnTool(mgr)
	ctx := ContextWithAgentID(context.Background(), parent.ID())
	ctx = ContextWithParentMode(ctx, "yolo")

	result, err := tool.Execute(ctx, map[string]any{"task": "list files"})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	handle, _ := parsed["handle"].(string)
	child, ok := mgr.Get(agentpkg.AgentID(handle))
	if !ok {
		t.Fatalf("expected spawned child %q", handle)
	}
	childAdapter := child.(*AgentAdapter)
	if childAdapter.inner.config.Mode != "yolo" {
		t.Fatalf("child mode = %q, want yolo", childAdapter.inner.config.Mode)
	}
	waitForManagedAgentToStop(t, mgr, agentpkg.AgentID(handle))
	if err := mgr.Destroy(agentpkg.AgentID(handle)); err != nil {
		t.Fatalf("destroy spawned agent: %v", err)
	}
}

func TestDelegateSubAgentToolMissingTask(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewDelegateSubAgentTool(mgr)
	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestSubAgentSpawnToolMissingTask(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentSpawnTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestSubAgentStatusTool(t *testing.T) {
	factory, mgr := newTestFactoryAndManager(t)
	_ = factory

	// Create an agent manually
	a, _ := mgr.Create(AgentOptions{ID: "test-agent"})

	tool := NewSubAgentStatusTool(mgr)
	result, err := tool.Execute(context.Background(), map[string]any{
		"handle": string(a.ID()),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal([]byte(result.Text), &parsed)
	if parsed["handle"] != "test-agent" {
		t.Errorf("expected 'test-agent', got %q", parsed["handle"])
	}
}

func TestSubAgentStatusToolNotFound(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentStatusTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]any{
		"handle": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

func TestSubAgentStatusToolAfterParentFinish(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mgr.Create(AgentOptions{ID: "main"})
	mgr.Create(AgentOptions{ID: "sub-1", ParentID: "main"})
	mgr.MarkDone("sub-1", "finished work")
	mgr.Finish("main", nil)

	tool := NewSubAgentStatusTool(mgr)
	result, err := tool.Execute(context.Background(), map[string]any{
		"handle": "sub-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if parsed["status"] != "done" {
		t.Fatalf("expected done status, got %q", parsed["status"])
	}
	if parsed["last_response"] != "finished work" {
		t.Fatalf("expected retained response, got %q", parsed["last_response"])
	}
}

func TestSubAgentStatusToolMissingHandle(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentStatusTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing handle")
	}
}

func TestSubAgentSendTool(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	a, _ := mgr.Create(AgentOptions{ID: "test-agent"})

	tool := NewSubAgentSendTool(mgr)
	result, err := tool.Execute(context.Background(), map[string]any{
		"handle":  string(a.ID()),
		"message": "do something",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal([]byte(result.Text), &parsed)
	if parsed["status"] != "message_sent" {
		t.Errorf("expected 'message_sent', got %q", parsed["status"])
	}
	waitForManagedAgentToStop(t, mgr, a.ID())
	if err := mgr.Destroy(a.ID()); err != nil {
		t.Fatalf("destroy sent agent: %v", err)
	}
}

func TestSubAgentSendToolNotFound(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentSendTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]any{
		"handle":  "nonexistent",
		"message": "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSubAgentSendToolMissingParams(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentSendTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]any{
		"handle": "x",
	})
	if err == nil {
		t.Fatal("expected error for missing message")
	}
}

func TestSubAgentDestroyTool(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	a, _ := mgr.Create(AgentOptions{ID: "to-destroy"})

	tool := NewSubAgentDestroyTool(mgr)
	result, err := tool.Execute(context.Background(), map[string]any{
		"handle": string(a.ID()),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal([]byte(result.Text), &parsed)
	if parsed["status"] != "destroyed" {
		t.Errorf("expected 'destroyed', got %q", parsed["status"])
	}

	// Verify it's gone
	if _, ok := mgr.Get("to-destroy"); ok {
		t.Error("expected agent to be destroyed")
	}
}

func TestSubAgentDestroyToolNotFound(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentDestroyTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]any{
		"handle": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSubAgentDestroyToolMissingHandle(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentDestroyTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing handle")
	}
}

// --- SubAgentPolicy tests ---

func TestSubAgentPolicyDefault(t *testing.T) {
	p := DefaultSubAgentPolicy()
	if p.MaxChildren != 5 {
		t.Errorf("expected MaxChildren=5, got %d", p.MaxChildren)
	}
	if len(p.AllowedModes) != 4 || p.AllowedModes[0] != "plan" || p.AllowedModes[1] != "agent" || p.AllowedModes[2] != "yolo" || p.AllowedModes[3] != "os" {
		t.Errorf("expected AllowedModes=[plan agent yolo os], got %v", p.AllowedModes)
	}
	if p.TimeoutPerAgent != 30*time.Minute {
		t.Errorf("expected TimeoutPerAgent=30m, got %s", p.TimeoutPerAgent)
	}
}

func TestSubAgentPolicyValidateTopLevel(t *testing.T) {
	p := DefaultSubAgentPolicy()
	// Top-level agents (no parent) are always allowed
	if err := p.Validate("", "yolo", 0); err != nil {
		t.Errorf("expected no error for top-level, got %v", err)
	}
}

func TestSubAgentPolicyValidateAllowed(t *testing.T) {
	p := DefaultSubAgentPolicy()
	if err := p.Validate("parent", "agent", 0); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestSubAgentPolicyValidateMaxChildren(t *testing.T) {
	p := DefaultSubAgentPolicy()
	err := p.Validate("parent", "agent", 5)
	if err == nil {
		t.Fatal("expected error for max children")
	}
}

func TestSubAgentPolicyValidateDisallowedMode(t *testing.T) {
	p := DefaultSubAgentPolicy()
	err := p.Validate("parent", "admin", 0)
	if err == nil {
		t.Fatal("expected error for disallowed mode")
	}
}

func TestSubAgentPolicyValidateCustom(t *testing.T) {
	p := SubAgentPolicy{
		MaxChildren:  3,
		AllowedModes: []string{"agent", "plan"},
	}
	if err := p.Validate("parent", "plan", 1); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if err := p.Validate("parent", "yolo", 0); err == nil {
		t.Error("expected error for yolo")
	}
	if err := p.Validate("parent", "agent", 3); err == nil {
		t.Error("expected error for max children")
	}
}

func TestSubAgentPromptContractOnlyForChild(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := mgr.Create(AgentOptions{ID: "sub-1", ParentID: parent.ID()})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	parentCtx := parent.GetContext()
	if parentCtx == nil || !contains(parentCtx.SystemPrompt, "Sub-Agent Tools") {
		t.Fatal("expected top-level multi-agent prompt to include orchestration guidance")
	}
	if contains(parentCtx.SystemPrompt, "Sub-Agent Operating Contract") {
		t.Error("expected top-level prompt to omit worker contract")
	}

	childCtx := child.GetContext()
	if childCtx == nil || !contains(childCtx.SystemPrompt, "Sub-Agent Operating Contract") {
		t.Fatal("expected child prompt to include worker contract")
	}
	if contains(childCtx.SystemPrompt, "Sub-Agent Tools") {
		t.Error("expected child prompt to omit sub-agent tools guidance")
	}
}

func TestAgentFactorySubAgentsRespectPlanToolSetting(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, nil)
	sandboxMgr := sandbox.NewManager(t.TempDir())
	sandboxMgr.SetLevel(sandbox.LevelNone)
	disabled := false
	settings := &config.Settings{
		SessionDir:      t.TempDir(),
		EnablePlanTool:  &disabled,
		DefaultProvider: "mock",
	}

	factory := NewAgentFactory(
		mockProvider,
		mockProvider.Models()[0],
		settings,
		sandboxMgr,
		"",
		"",
		nil,
		ctxpkg.CompactionSettings{},
		nil,
	)
	mgr := NewAgentManager(factory)

	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := mgr.Create(AgentOptions{ID: "sub-1", ParentID: parent.ID()})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	for _, a := range []agentpkg.Agent{parent, child} {
		adapter, ok := a.(*AgentAdapter)
		if !ok {
			t.Fatalf("expected AgentAdapter, got %T", a)
		}
		if toolNamesContain(adapter.inner.GetContext().Tools, "plan") {
			t.Fatalf("expected %s to omit plan tool when disabled", a.ID())
		}
	}
}

func TestAgentFactorySubAgentsRegisterSkillRef(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, nil)
	sandboxMgr := sandbox.NewManager(t.TempDir())
	sandboxMgr.SetLevel(sandbox.LevelNone)
	settings := &config.Settings{SessionDir: t.TempDir()}

	projectDir := t.TempDir()
	skillsDir := filepath.Join(projectDir, ".skills", "demo")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte("# Demo\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	skillsMgr := skills.NewManager("", filepath.Join(projectDir, ".skills"))
	if err := skillsMgr.Load(); err != nil {
		t.Fatalf("load skills: %v", err)
	}

	factory := NewAgentFactory(
		mockProvider,
		mockProvider.Models()[0],
		settings,
		sandboxMgr,
		"",
		"",
		skillsMgr,
		ctxpkg.CompactionSettings{},
		nil,
	)
	mgr := NewAgentManager(factory)

	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := mgr.Create(AgentOptions{ID: "sub-1", ParentID: parent.ID()})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	for _, a := range []agentpkg.Agent{parent, child} {
		adapter, ok := a.(*AgentAdapter)
		if !ok {
			t.Fatalf("expected AgentAdapter, got %T", a)
		}
		if !toolNamesContain(adapter.inner.GetContext().Tools, "skill_ref") {
			t.Fatalf("expected %s to include skill_ref", a.ID())
		}
	}
}

func TestAgentManagerEnforcesSubAgentPolicy(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	for i := 0; i < DefaultSubAgentPolicy().MaxChildren; i++ {
		_, err := mgr.Create(AgentOptions{
			ID:       agentpkg.AgentID(fmt.Sprintf("sub-%d", i)),
			ParentID: parent.ID(),
			Mode:     "agent",
		})
		if err != nil {
			t.Fatalf("create child %d: %v", i, err)
		}
	}

	_, err = mgr.Create(AgentOptions{ID: "sub-overflow", ParentID: parent.ID(), Mode: "agent"})
	if err == nil {
		t.Fatal("expected max-children error")
	}

	// yolo mode is now allowed by default (inherited from parent)
	_, mgr = newTestFactoryAndManager(t)
	parent, _ = mgr.Create(AgentOptions{ID: "main"})
	a, err := mgr.Create(AgentOptions{ID: "sub-yolo", ParentID: parent.ID(), Mode: "yolo"})
	if err != nil {
		t.Fatalf("expected yolo mode to be allowed, got error: %v", err)
	}
	_ = a
	// Test a truly disallowed mode
	_, err = mgr.Create(AgentOptions{ID: "sub-admin", ParentID: parent.ID(), Mode: "admin"})
	if err == nil {
		t.Fatal("expected disallowed mode error for admin")
	}
}

func toolNamesContain(tools []provider.ToolDefinition, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// --- Tool interface compliance ---

func TestSubAgentToolsImplementToolInterface(t *testing.T) {
	var _ tools.Tool = (*SubAgentSpawnTool)(nil)
	var _ tools.Tool = (*SubAgentStatusTool)(nil)
	var _ tools.Tool = (*SubAgentSendTool)(nil)
	var _ tools.Tool = (*SubAgentDestroyTool)(nil)
}

func TestSubAgentToolsDescriptions(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)

	tools := []tools.Tool{
		NewSubAgentSpawnTool(mgr),
		NewSubAgentStatusTool(mgr),
		NewSubAgentSendTool(mgr),
		NewSubAgentDestroyTool(mgr),
	}

	for _, tool := range tools {
		if tool.Name() == "" {
			t.Errorf("tool %T has empty name", tool)
		}
		if tool.Description() == "" {
			t.Errorf("tool %s has empty description", tool.Name())
		}
		if tool.Parameters() == nil {
			t.Errorf("tool %s has nil parameters", tool.Name())
		}
	}
}

// TestSendParentEvent_ClosedChannel verifies sendParentEvent does not panic
// when the channel is closed (recover logs and returns false).
func TestSendParentEvent_ClosedChannel(t *testing.T) {
	ch := make(chan Event, 1)
	close(ch)

	ev := Event{Type: EventStatus, StatusMessage: "test"}
	ok := sendParentEvent(context.Background(), ch, ev)
	if ok {
		t.Error("expected sendParentEvent to return false on closed channel")
	}
}

// TestSendParentEvent_ContextCanceled verifies sendParentEvent returns false
// when the context is canceled and the channel is full (unbuffered, never read).
func TestSendParentEvent_ContextCanceled(t *testing.T) {
	ch := make(chan Event) // unbuffered — will block until context cancels
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	ev := Event{Type: EventStatus, StatusMessage: "test"}
	ok := sendParentEvent(ctx, ch, ev)
	if ok {
		t.Error("expected sendParentEvent to return false on canceled context")
	}
}

// TestSendParentEvent_Success verifies sendParentEvent succeeds normally.
func TestSendParentEvent_Success(t *testing.T) {
	ch := make(chan Event, 1)
	ev := Event{Type: EventStatus, StatusMessage: "test"}
	ok := sendParentEvent(context.Background(), ch, ev)
	if !ok {
		t.Error("expected sendParentEvent to return true on success")
	}
	received := <-ch
	if received.StatusMessage != "test" {
		t.Errorf("expected 'test', got %q", received.StatusMessage)
	}
}

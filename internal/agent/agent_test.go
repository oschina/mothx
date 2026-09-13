package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/config"
	ctxpkg "github.com/startvibecoding/mothx/internal/context"
	"github.com/startvibecoding/mothx/internal/imageproc"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/tools"
)

type loopingToolProvider struct {
	models    []*provider.Model
	callCount int
}

// emptyRecoveringProvider returns an empty response (no content + stub usage
// {1,1,2}, mimicking an OpenAI-compatible gateway placeholder) for the first
// `emptyTimes` calls, then a normal text response. Used to exercise the
// agent loop's empty-response retry/recovery path.
type emptyRecoveringProvider struct {
	models     []*provider.Model
	callCount  int
	emptyTimes int
}

func (p *emptyRecoveringProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 4)
	p.callCount++
	n := p.callCount
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		if n <= p.emptyTimes {
			ch <- provider.StreamEvent{Type: provider.StreamUsage, Usage: &provider.Usage{Input: 1, Output: 1, TotalTokens: 2}}
			ch <- provider.StreamEvent{Type: provider.StreamDone} // no stopReason
			return
		}
		ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "done"}
		ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "stop"}
	}()
	return ch
}

func (p *emptyRecoveringProvider) Name() string              { return "empty-recovering" }
func (p *emptyRecoveringProvider) API() string               { return "openai-chat" }
func (p *emptyRecoveringProvider) Models() []*provider.Model { return p.models }
func (p *emptyRecoveringProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// timeoutRecoveringProvider emits a stream-timeout error on the first
// `timeoutTimes` calls (before any visible output), then a normal text response.
// Used to exercise the agent loop's stream-timeout retry/recovery path.
type timeoutRecoveringProvider struct {
	models       []*provider.Model
	callCount    int
	timeoutTimes int
}

func (p *timeoutRecoveringProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 4)
	p.callCount++
	n := p.callCount
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		if n <= p.timeoutTimes {
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: context.DeadlineExceeded, StopReason: "error"}
			return
		}
		ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "recovered"}
		ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "stop"}
	}()
	return ch
}

func (p *timeoutRecoveringProvider) Name() string              { return "timeout-recovering" }
func (p *timeoutRecoveringProvider) API() string               { return "openai-chat" }
func (p *timeoutRecoveringProvider) Models() []*provider.Model { return p.models }
func (p *timeoutRecoveringProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func TestResponseArchiveSinkRecordsLineageConflict(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	agent := &Agent{config: AgentLoopConfig{Config: Config{
		Session:  manager,
		Provider: &emptyRecoveringProvider{},
		Model:    &provider.Model{ID: "model-test"},
	}}}

	archive := provider.ResponseArchive{
		Status: "completed", StateMode: "previous_response_id",
		UnknownEventTypes: []string{"response.future_event"},
	}
	archive.ResponseID = "resp-active"
	agent.responseArchiveSink("turn-active", 0)(archive)

	archive.ResponseID = "resp-branch"
	agent.responseArchiveSink("turn-branch", 0)(archive)

	state, err := session.GetResponseSessionState(sessionDir, manager.GetHeader().ID)
	if err != nil || state == nil || state.PreviousResponseID != "resp-active" || state.Version != 1 {
		t.Fatalf("active response state = %#v, err=%v", state, err)
	}
	turn, err := session.GetResponseTurn(sessionDir, manager.GetHeader().ID, "turn-branch")
	if err != nil || turn == nil {
		t.Fatalf("load branched turn: %#v, err=%v", turn, err)
	}
	var summary map[string]any
	if err := json.Unmarshal(turn.ResponseSummary, &summary); err != nil {
		t.Fatalf("decode response summary: %v", err)
	}
	if summary["lineageUpdate"] != "conflict" || summary["lineageExpectedVersion"] != float64(0) ||
		!reflect.DeepEqual(summary["unknownEventTypes"], []any{"response.future_event"}) {
		t.Fatalf("branched response summary = %#v", summary)
	}
}

func TestRecordResponsesStateFailureArchivesSanitizedClass(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	agent := &Agent{config: AgentLoopConfig{Config: Config{
		Session:  manager,
		Provider: &emptyRecoveringProvider{},
		Model:    &provider.Model{ID: "model-test"},
	}}}
	agent.recordResponsesStateFailure("turn-failed", responsesStateSnapshot{previousResponseID: "resp-previous", version: 3, remoteStateActive: true}, errors.New("API error 403: secret value"))
	turn, err := session.GetResponseTurn(sessionDir, manager.GetHeader().ID, "turn-failed")
	if err != nil || turn == nil {
		t.Fatalf("load failed response turn: %#v, err=%v", turn, err)
	}
	if turn.Status != "failed" || turn.IncompleteReason != "request_failed" || strings.Contains(string(turn.ResponseSummary), "secret value") {
		t.Fatalf("failed response turn = %#v", turn)
	}
}

func TestClaimToolExecutionScopesToTurnAndSkipsPlan(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "model1", Name: "Model 1"}}, nil)
	a := New(Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "plan",
		Session:  manager,
	}, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))

	call := provider.ToolCallBlock{ID: "reused-call-id", Name: "read"}
	params := map[string]any{"path": "README.md"}
	first, reused, err := a.claimToolExecution("turn-one", call, params)
	if err != nil || first == nil || reused != nil {
		t.Fatalf("first claim = record=%#v reused=%#v err=%v", first, reused, err)
	}
	second, reused, err := a.claimToolExecution("turn-two", call, params)
	if err != nil || second == nil || reused != nil {
		t.Fatalf("second-turn claim = record=%#v reused=%#v err=%v", second, reused, err)
	}
	if first.ExecutionKey == second.ExecutionKey {
		t.Fatalf("execution keys must differ across local turns: %q", first.ExecutionKey)
	}

	planRecord, reused, err := a.claimToolExecution("turn-one", provider.ToolCallBlock{ID: "plan-call", Name: "plan"}, map[string]any{"steps": []any{}})
	if err != nil || planRecord != nil || reused != nil {
		t.Fatalf("plan claim = record=%#v reused=%#v err=%v, want no durable claim", planRecord, reused, err)
	}
}

func TestClaimToolExecutionMarksYoloWriteAsSideEffecting(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model1"}}, nil)
	a := New(Config{Provider: p, Model: p.Models()[0], Mode: "yolo", Session: manager}, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))
	record, reused, err := a.claimToolExecution("turn-yolo", provider.ToolCallBlock{ID: "write-call", Name: "write"}, map[string]any{"path": "README.md", "content": "changed"})
	if err != nil || record == nil || reused != nil {
		t.Fatalf("write claim = record=%#v reused=%#v err=%v", record, reused, err)
	}
	if !record.SideEffecting {
		t.Fatal("yolo write must remain side-effecting even without approval")
	}
}

func TestClaimToolExecutionRecoveryReopensOnlyReadOnlyRecords(t *testing.T) {
	workDir := t.TempDir()
	manager := session.New(workDir, workDir)
	if err := manager.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model1"}}, nil)
	a := New(Config{Provider: p, Model: p.Models()[0], Session: manager, Mode: "yolo"}, tools.NewRegistry(workDir, sandbox.NewNoneSandbox()))
	readCall := provider.ToolCallBlock{ID: "read-call", Name: "read"}
	readArgs := map[string]any{"path": "README.md"}
	if record, reused, err := a.claimToolExecution("turn-read", readCall, readArgs); err != nil || record == nil || reused != nil {
		t.Fatalf("initial read claim = record=%#v reused=%#v err=%v", record, reused, err)
	}
	reclaimed, reused, err := a.claimToolExecutionWithRecovery("turn-read", readCall, readArgs, true)
	if err != nil || reclaimed == nil || reused != nil {
		t.Fatalf("recovered read claim = record=%#v reused=%#v err=%v", reclaimed, reused, err)
	}

	writeCall := provider.ToolCallBlock{ID: "write-call", Name: "write"}
	writeArgs := map[string]any{"path": "README.md", "content": "changed"}
	writeRecord, _, err := a.claimToolExecution("turn-write", writeCall, writeArgs)
	if err != nil || writeRecord == nil {
		t.Fatalf("initial write claim = %#v err=%v", writeRecord, err)
	}
	writeRecord.SideEffecting = true
	if err := session.UpdateToolExecutionRecord(manager.GetSessionDir(), *writeRecord); err != nil {
		t.Fatalf("mark write side effecting: %v", err)
	}
	reclaimed, reused, err = a.claimToolExecutionWithRecovery("turn-write", writeCall, writeArgs, true)
	if err != nil || reclaimed != nil || reused == nil || !reused.IsError {
		t.Fatalf("side-effecting recovery = record=%#v reused=%#v err=%v", reclaimed, reused, err)
	}
	if !strings.Contains(messageTextForTest(*reused), "exactly-once") {
		t.Fatalf("side-effecting recovery message = %q, want explicit exactly-once limitation", messageTextForTest(*reused))
	}
	if requested, err := session.RequestToolExecutionRecovery(manager.GetSessionDir(), manager.GetHeader().ID, "turn-write", []string{writeCall.ID}); err != nil || requested != 1 {
		t.Fatalf("request confirmed write recovery = %d, err=%v", requested, err)
	}
	reclaimed, reused, err = a.claimToolExecutionWithRecovery("turn-write", writeCall, writeArgs, true)
	if err != nil || reclaimed == nil || reused != nil {
		t.Fatalf("confirmed side-effecting recovery = record=%#v reused=%#v err=%v", reclaimed, reused, err)
	}
}

type recordingToolProvider struct {
	models []*provider.Model
	calls  []provider.ChatParams
}

type compactionReplayProvider struct {
	models []*provider.Model
	calls  []provider.ChatParams
}

type canceledCompactionProvider struct {
	models []*provider.Model
}

type workflowRunToolProvider struct {
	models []*provider.Model
	calls  []provider.ChatParams
}

func newRecordingToolProvider() *recordingToolProvider {
	return &recordingToolProvider{
		models: []*provider.Model{{ID: "model1", Name: "Model 1"}},
	}
}

func newCompactionReplayProvider() *compactionReplayProvider {
	return &compactionReplayProvider{
		models: []*provider.Model{{
			ID:            "model1",
			Name:          "Model 1",
			ContextWindow: 4096,
			MaxTokens:     1024,
		}},
	}
}

func (p *canceledCompactionProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 1)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamError, Error: context.Canceled}
	}()
	return ch
}

func (p *canceledCompactionProvider) Name() string {
	return "canceled-compaction"
}

func (p *canceledCompactionProvider) API() string {
	return "openai-chat"
}

func (p *canceledCompactionProvider) Models() []*provider.Model {
	return p.models
}

func (p *canceledCompactionProvider) GetModel(id string) *provider.Model {
	for _, model := range p.models {
		if model.ID == id {
			return model
		}
	}
	return nil
}

func (p *recordingToolProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.calls = append(p.calls, provider.ChatParams{
		Messages: cloneMessages(params.Messages),
		Tools:    append([]provider.ToolDefinition(nil), params.Tools...),
	})

	ch := make(chan provider.StreamEvent, 3)
	callNumber := len(p.calls)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		if callNumber == 1 {
			ch <- provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
				ID:        "call_1",
				Name:      "bash",
				Arguments: []byte(`{"command":"echo visible"}`),
			}}
		} else {
			ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "done"}
		}
		ch <- provider.StreamEvent{Type: provider.StreamDone}
	}()
	return ch
}

func (p *recordingToolProvider) Name() string {
	return "recording"
}

func (p *recordingToolProvider) API() string {
	return "openai-chat"
}

func (p *recordingToolProvider) Models() []*provider.Model {
	return p.models
}

func (p *recordingToolProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func (p *compactionReplayProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.calls = append(p.calls, provider.ChatParams{
		Messages: cloneMessages(params.Messages),
		Tools:    append([]provider.ToolDefinition(nil), params.Tools...),
	})

	ch := make(chan provider.StreamEvent, 3)
	callNumber := len(p.calls)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		switch callNumber {
		case 1:
			ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "## Goal\ncheckpoint"}
		default:
			ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "continued"}
		}
		ch <- provider.StreamEvent{Type: provider.StreamDone}
	}()
	return ch
}

func (p *compactionReplayProvider) Name() string {
	return "compaction-replay"
}

func (p *compactionReplayProvider) API() string {
	return "openai-chat"
}

func (p *compactionReplayProvider) Models() []*provider.Model {
	return p.models
}

func (p *compactionReplayProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func (p *workflowRunToolProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.calls = append(p.calls, provider.ChatParams{
		Messages: cloneMessages(params.Messages),
		Tools:    append([]provider.ToolDefinition(nil), params.Tools...),
	})

	ch := make(chan provider.StreamEvent, 3)
	callNumber := len(p.calls)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		if callNumber == 1 {
			ch <- provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
				ID:        "workflow_call_1",
				Name:      "workflow_run",
				Arguments: []byte(`{"source":"(workflow \"slow\")"}`),
			}}
		} else {
			ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "workflow complete"}
		}
		ch <- provider.StreamEvent{Type: provider.StreamDone}
	}()
	return ch
}

func (p *workflowRunToolProvider) Name() string {
	return "workflow-run-tool"
}

func (p *workflowRunToolProvider) API() string {
	return "openai-chat"
}

func (p *workflowRunToolProvider) Models() []*provider.Model {
	return p.models
}

func (p *workflowRunToolProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

type fixedBashTool struct{}

func (fixedBashTool) Name() string { return "bash" }

func (fixedBashTool) Description() string { return "fake bash for tests" }

func (fixedBashTool) PromptSnippet() string { return "fake bash" }

func (fixedBashTool) PromptGuidelines() []string { return nil }

func (fixedBashTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)
}

func (fixedBashTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	return tools.NewTextToolResult("bash output visible to the next model turn"), nil
}

type richImageTool struct{}

func (richImageTool) Name() string { return "image_tool" }

func (richImageTool) Description() string { return "fake image-producing tool for tests" }

func (richImageTool) PromptSnippet() string { return "fake image tool" }

func (richImageTool) PromptGuidelines() []string { return nil }

func (richImageTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (richImageTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	return tools.NewImageToolResult("image output", "image/png", "aW1hZ2U="), nil
}

type hugeBashTool struct {
	fixedBashTool
	output string
}

func (t hugeBashTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	return tools.NewTextToolResult(t.output), nil
}

type timeoutBashTool struct {
	fixedBashTool
	timeout time.Duration
}

func (t timeoutBashTool) ExecutionTimeout(params map[string]any) (time.Duration, bool) {
	return t.timeout, true
}

type blockingWorkflowRunTool struct {
	delay time.Duration
}

func (blockingWorkflowRunTool) Name() string { return "workflow_run" }

func (blockingWorkflowRunTool) Description() string { return "fake workflow_run for tests" }

func (blockingWorkflowRunTool) PromptSnippet() string { return "fake workflow_run" }

func (blockingWorkflowRunTool) PromptGuidelines() []string { return nil }

func (blockingWorkflowRunTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"source":{"type":"string"}}}`)
}

func (t blockingWorkflowRunTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	timer := time.NewTimer(t.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return tools.ToolResult{}, ctx.Err()
	case <-timer.C:
		return tools.NewTextToolResult(`{"id":"run-1","status":"done"}`), nil
	}
}

func TestToolExecutionContextUsesToolTimeoutOverride(t *testing.T) {
	parent := context.Background()

	defaultCtx, defaultCancel := toolExecutionContext(parent, fixedBashTool{}, nil)
	defer defaultCancel()
	if _, ok := defaultCtx.Deadline(); !ok {
		t.Fatal("expected default tool execution deadline")
	}

	customCtx, customCancel := toolExecutionContext(parent, timeoutBashTool{timeout: 2 * time.Second}, nil)
	defer customCancel()
	deadline, ok := customCtx.Deadline()
	if !ok {
		t.Fatal("expected custom tool execution deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 3*time.Second {
		t.Fatalf("custom deadline remaining = %s, want about 2s", remaining)
	}

	noDeadlineCtx, noDeadlineCancel := toolExecutionContext(parent, timeoutBashTool{timeout: 0}, nil)
	defer noDeadlineCancel()
	if _, ok := noDeadlineCtx.Deadline(); ok {
		t.Fatal("expected no agent-level deadline when tool timeout is zero")
	}
}

func TestBeforeToolExecuteRunsAfterApprovalAndBeforeTool(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(fixedBashTool{})
	var order []string
	a := NewWithLoopConfig(AgentLoopConfig{
		Config: Config{
			Provider: provider.NewMockProvider("mock", []*provider.Model{{ID: "model"}}, nil),
			Model:    &provider.Model{ID: "model"},
			Mode:     "agent",
			ApprovalHandler: func(string, string, map[string]any) bool {
				order = append(order, "approval")
				return true
			},
		},
		BeforeToolExecute: func(BeforeToolExecuteContext) *ToolCallBlockResult {
			order = append(order, "fence")
			return nil
		},
	}, registry)
	ch := make(chan Event, 8)
	result := a.executeSingleToolCall(context.Background(), provider.ToolCallBlock{ID: "call-1", Name: "bash", Arguments: json.RawMessage(`{"command":"printf ok"}`)}, "turn-1", ch, nil)
	if result.IsError {
		t.Fatalf("tool result error = %q", result.Content)
	}
	if len(order) != 2 || order[0] != "approval" || order[1] != "fence" {
		t.Fatalf("hook order = %#v, want approval then fence", order)
	}
}

func newLoopingToolProvider() *loopingToolProvider {
	return &loopingToolProvider{
		models: []*provider.Model{{ID: "model1", Name: "Model 1"}},
	}
}

func (p *loopingToolProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 3)
	p.callCount++
	toolCall := &provider.ToolCallBlock{
		ID:        fmt.Sprintf("call_%d", p.callCount),
		Name:      "unknown_tool",
		Arguments: []byte(`{}`),
	}
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		ch <- provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: toolCall}
		ch <- provider.StreamEvent{Type: provider.StreamDone}
	}()
	return ch
}

func (p *loopingToolProvider) Name() string {
	return "looping"
}

func (p *loopingToolProvider) API() string {
	return "openai-chat"
}

func (p *loopingToolProvider) Models() []*provider.Model {
	return p.models
}

func (p *loopingToolProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func TestNewAgent(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, nil)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry("/tmp", sb)
	registry.RegisterDefaults()

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
	}

	a := New(cfg, registry)

	if a == nil {
		t.Fatal("expected non-nil agent")
	}
}

func TestBuildBackgroundParamsDoNotLeakResponsesOptionsToOtherProtocols(t *testing.T) {
	for _, api := range []string{"openai-chat", "anthropic-messages", "google-gemini"} {
		t.Run(api, func(t *testing.T) {
			workDir := t.TempDir()
			sess := session.New(workDir, workDir)
			if err := sess.Init(); err != nil {
				t.Fatalf("init session: %v", err)
			}
			p := provider.NewMockProvider("other-vendor", []*provider.Model{{ID: "model-1"}}, nil)
			p.SetAPI(api)
			a := New(Config{Provider: p, Model: p.Models()[0], Session: sess, Mode: "agent"}, tools.NewRegistry(workDir, sandbox.NewNoneSandbox()))
			params, err := a.BuildBackgroundChatParams("turn-1", provider.NewUserMessage("hello"))
			if err != nil {
				t.Fatalf("build params: %v", err)
			}
			if params.ResponseOptions != nil {
				t.Fatalf("ResponseOptions leaked into %s request: %#v", api, params.ResponseOptions)
			}
		})
	}
}

func TestNewAgentConfiguresRegistryImageHint(t *testing.T) {
	mockProvider := provider.NewMockProvider("openai", []*provider.Model{
		{ID: "MiniMax-M3", Name: "MiniMax M3"},
	}, nil)
	mockProvider.SetAPI("anthropic-messages")

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry("/tmp", sb)
	registry.RegisterDefaults()

	cfg := Config{
		Provider: mockProvider,
		Vendor:   "minimax-anthropic",
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
	}
	New(cfg, registry)

	policy := registry.ImagePolicy(imageproc.ModeDetail)
	if policy.MaxOutputBytes != 5<<20 {
		t.Fatalf("MaxOutputBytes = %d, want %d", policy.MaxOutputBytes, 5<<20)
	}
}

func TestNewWithLoopConfig(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, nil)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry("/tmp", sb)
	registry.RegisterDefaults()

	cfg := AgentLoopConfig{
		Config: Config{
			Provider: mockProvider,
			Model:    mockProvider.Models()[0],
			Mode:     "agent",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     100,
	}

	a := NewWithLoopConfig(cfg, registry)

	if a == nil {
		t.Fatal("expected non-nil agent")
	}
}

func TestAgentAbort(t *testing.T) {
	// Use a slow provider that gives us time to abort
	responses := []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "Hello"},
		{Type: provider.StreamDone},
	}

	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, responses)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry("/tmp", sb)

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
	}

	a := New(cfg, registry)

	// Run and collect events
	ch := a.Run(context.Background(), "test")

	// Abort after a short delay
	go func() {
		time.Sleep(10 * time.Millisecond)
		a.Abort()
	}()

	var events []Event
	for event := range ch {
		events = append(events, event)
	}

	// Should have events (abort may or may not cause error depending on timing)
	if len(events) == 0 {
		t.Error("expected at least one event")
	}
}

func TestAgentRun(t *testing.T) {
	responses := []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "Hello"},
		{Type: provider.StreamTextDelta, TextDelta: " World"},
		{Type: provider.StreamDone},
	}

	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, responses)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry("/tmp", sb)
	registry.RegisterDefaults()

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
	}

	a := New(cfg, registry)
	ch := a.Run(context.Background(), "test")

	var events []Event
	for event := range ch {
		events = append(events, event)
	}

	// Should have: AgentStart, TurnStart, TextDelta, TextDelta, TurnEnd, Done, AgentEnd
	if len(events) < 5 {
		t.Errorf("expected at least 5 events, got %d", len(events))
	}

	// Check first event is AgentStart
	if events[0].Type != EventAgentStart {
		t.Errorf("expected first event to be AgentStart, got %d", events[0].Type)
	}

	// Check last event is AgentEnd
	lastEvent := events[len(events)-1]
	if lastEvent.Type != EventAgentEnd {
		t.Errorf("expected last event to be AgentEnd, got %d", lastEvent.Type)
	}
}

func TestAgentRunEmitsErrorAfterPartialStreamOutput(t *testing.T) {
	streamErr := fmt.Errorf("stream read error: stream error: stream ID 19; INTERNAL_ERROR; received from peer")
	responses := []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "partial"},
		{Type: provider.StreamError, Error: streamErr, StopReason: "error"},
	}

	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, responses)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry("/tmp", sb)
	registry.RegisterDefaults()

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
	}

	a := New(cfg, registry)

	var sawPartial bool
	var sawError bool
	for event := range a.Run(context.Background(), "test") {
		switch event.Type {
		case EventTextDelta:
			if event.TextDelta == "partial" {
				sawPartial = true
			}
		case EventError:
			if !sawPartial {
				t.Fatal("EventError emitted before partial text delta")
			}
			sawError = true
			if event.Error == nil || !strings.Contains(event.Error.Error(), "INTERNAL_ERROR") {
				t.Fatalf("EventError = %v, want INTERNAL_ERROR", event.Error)
			}
		case EventDone:
			t.Fatal("unexpected EventDone after stream error")
		}
	}

	if !sawPartial {
		t.Fatal("missing partial text delta")
	}
	if !sawError {
		t.Fatal("missing EventError")
	}
}

func TestRunWithMessages(t *testing.T) {
	responses := []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "Response"},
		{Type: provider.StreamDone},
	}

	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, responses)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry("/tmp", sb)
	registry.RegisterDefaults()

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
	}

	a := New(cfg, registry)

	messages := []provider.Message{
		provider.NewUserMessage("Hello"),
	}

	ch := a.RunWithMessages(context.Background(), messages)

	var events []Event
	for event := range ch {
		events = append(events, event)
	}

	if len(events) < 3 {
		t.Errorf("expected at least 3 events, got %d", len(events))
	}
}

func TestGetSetMessages(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, nil)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry("/tmp", sb)

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
	}

	a := New(cfg, registry)

	messages := []provider.Message{
		provider.NewUserMessage("Hello"),
		provider.NewAssistantMessage([]provider.ContentBlock{
			{Type: "text", Text: "Hi"},
		}),
	}

	a.SetMessages(messages)

	got := a.GetMessages()
	if len(got) != 2 {
		t.Errorf("expected 2 messages, got %d", len(got))
	}
}

func TestGetSetContext(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, nil)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry("/tmp", sb)

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
	}

	a := New(cfg, registry)

	ctx := &AgentContext{
		SystemPrompt: "test prompt",
		Messages:     []provider.Message{provider.NewUserMessage("Hello")},
	}

	a.SetContext(ctx)

	got := a.GetContext()
	if got.SystemPrompt != "test prompt" {
		t.Errorf("expected system prompt 'test prompt', got '%s'", got.SystemPrompt)
	}
}

func TestAgentRunWithToolCall(t *testing.T) {
	toolCall := &provider.ToolCallBlock{
		ID:        "call_1",
		Name:      "ls",
		Arguments: []byte(`{"path": "."}`),
	}

	responses := []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamToolCall, ToolCall: toolCall},
		{Type: provider.StreamDone},
	}

	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, responses)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)
	registry.RegisterDefaults()

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
	}

	a := New(cfg, registry)
	ch := a.Run(context.Background(), "list files")

	var events []Event
	for event := range ch {
		events = append(events, event)
	}

	// Check that tool events are present
	hasToolCall := false
	hasToolExecution := false
	for _, event := range events {
		if event.Type == EventToolCall {
			hasToolCall = true
		}
		if event.Type == EventToolExecutionStart {
			hasToolExecution = true
		}
	}

	if !hasToolCall {
		t.Error("expected tool call event")
	}

	if !hasToolExecution {
		t.Error("expected tool execution event")
	}
}

func TestNativeResponsesReplayInterleavesArchivedAssistantItems(t *testing.T) {
	sessionDir := t.TempDir()
	sess := session.New(t.TempDir(), sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := sess.GetHeader().ID
	if err := session.SaveResponseTurn(sessionDir, session.ResponseTurn{
		SessionID: sessionID, LocalTurnID: "turn-1", Provider: "openai", API: "openai-responses",
		Model: "test", StateMode: "replay", Status: "completed",
	}); err != nil {
		t.Fatalf("save response turn: %v", err)
	}
	for index, raw := range []json.RawMessage{
		json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"working"}]}`),
		json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"a\"}"}`),
	} {
		if err := session.SaveResponseItem(sessionDir, session.ResponseItemArchive{
			SessionID: sessionID, LocalTurnID: "turn-1", ResponseID: "resp-1", ItemID: fmt.Sprintf("item-%d", index),
			OutputIndex: index, ItemType: "message", ItemStatus: "completed", SanitizedJSON: raw,
		}); err != nil {
			t.Fatalf("save response item: %v", err)
		}
	}
	a := &Agent{config: AgentLoopConfig{Config: Config{Session: sess}}}
	messages := []provider.Message{
		provider.NewUserMessage("inspect a"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "working"}}),
		provider.NewToolResultMessage("call_1", "read", "contents", false),
	}
	items, err := a.nativeResponsesReplayItems(messages)
	if err != nil {
		t.Fatalf("native replay items: %v", err)
	}
	if len(items) != 4 || !strings.Contains(string(items[1]), `"role":"assistant"`) || !strings.Contains(string(items[2]), `"call_id":"call_1"`) || !strings.Contains(string(items[3]), `"function_call_output"`) {
		t.Fatalf("replay items = %#v", items)
	}
	items, err = a.nativeResponsesReplayItems(append(messages, provider.NewAssistantMessage(nil)))
	if err != nil || items != nil {
		t.Fatalf("mismatched replay = %#v, err=%v; want fallback", items, err)
	}
}

func TestNativeResponsesReplayFallsBackForOversizedItem(t *testing.T) {
	sessionDir := t.TempDir()
	sess := session.New(t.TempDir(), sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := sess.GetHeader().ID
	if err := session.SaveResponseTurn(sessionDir, session.ResponseTurn{
		SessionID: sessionID, LocalTurnID: "turn-oversized", Provider: "openai",
		API: "openai-responses", Model: "test", StateMode: "replay", Status: "completed",
	}); err != nil {
		t.Fatalf("save response turn: %v", err)
	}
	if err := session.SaveResponseItem(sessionDir, session.ResponseItemArchive{
		SessionID: sessionID, LocalTurnID: "turn-oversized", ItemID: "item-1",
		OutputIndex: 0, ItemType: "message", ItemStatus: "completed",
		SanitizedJSON: json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"working"}]}`),
	}); err != nil {
		t.Fatalf("save response item: %v", err)
	}

	a := &Agent{config: AgentLoopConfig{Config: Config{Session: sess}}}
	toolOutput := strings.Repeat("x", 128*1024)
	items, err := a.nativeResponsesReplayItems([]provider.Message{
		provider.NewUserMessage("inspect"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "working"}}),
		provider.NewToolResultMessage("call-1", "read", toolOutput, false),
	})
	if err != nil {
		t.Fatalf("native replay items: %v", err)
	}
	if items != nil {
		t.Fatalf("oversized replay = %#v, want transcript fallback", items)
	}
}

func TestToolResultIsIncludedInNextProviderTurn(t *testing.T) {
	recorder := newRecordingToolProvider()

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)
	registry.Register(fixedBashTool{})

	cfg := AgentLoopConfig{
		Config: Config{
			Provider: recorder,
			Model:    recorder.Models()[0],
			Mode:     "yolo",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     2,
	}

	a := NewWithLoopConfig(cfg, registry)
	for range a.Run(context.Background(), "run bash") {
	}

	if len(recorder.calls) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(recorder.calls))
	}

	var foundToolResult bool
	for _, msg := range recorder.calls[1].Messages {
		if msg.Role == "toolResult" && msg.ToolName == "bash" && strings.Contains(messageTextForTest(msg), "bash output visible") {
			foundToolResult = true
			break
		}
	}
	if !foundToolResult {
		t.Fatalf("second provider call did not include bash tool result: %#v", recorder.calls[1].Messages)
	}
}

func TestWorkflowRunWaitDoesNotConsumeMainAgentIterations(t *testing.T) {
	delay := 75 * time.Millisecond
	recorder := &workflowRunToolProvider{
		models: []*provider.Model{{ID: "model1", Name: "Model 1"}},
	}

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)
	registry.Register(blockingWorkflowRunTool{delay: delay})

	cfg := AgentLoopConfig{
		Config: Config{
			Provider: recorder,
			Model:    recorder.Models()[0],
			Mode:     "yolo",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     2,
	}

	a := NewWithLoopConfig(cfg, registry)
	start := time.Now()
	var turnStarts int
	var toolStarted bool
	var toolEnded bool
	var runErr error
	for ev := range a.Run(context.Background(), "run workflow") {
		switch ev.Type {
		case EventTurnStart:
			turnStarts++
		case EventToolExecutionStart:
			if ev.ToolName == "workflow_run" {
				toolStarted = true
			}
		case EventToolExecutionEnd:
			if ev.ToolName == "workflow_run" {
				toolEnded = true
			}
		case EventError:
			runErr = ev.Error
		}
	}
	elapsed := time.Since(start)

	if runErr != nil {
		t.Fatalf("Run() error = %v", runErr)
	}
	if elapsed < delay {
		t.Fatalf("workflow_run did not block long enough: elapsed %s, delay %s", elapsed, delay)
	}
	if len(recorder.calls) != 2 {
		t.Fatalf("expected exactly 2 provider calls, got %d", len(recorder.calls))
	}
	if turnStarts != 2 {
		t.Fatalf("expected exactly 2 main-agent turns, got %d", turnStarts)
	}
	if !toolStarted || !toolEnded {
		t.Fatalf("expected workflow_run tool start/end events, started=%v ended=%v", toolStarted, toolEnded)
	}
}

func TestOversizedToolResultIsOmittedBeforeNextProviderTurn(t *testing.T) {
	recorder := &recordingToolProvider{
		models: []*provider.Model{{
			ID:            "model1",
			Name:          "Model 1",
			ContextWindow: 12000,
			MaxTokens:     512,
		}},
	}

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)
	registry.Register(hugeBashTool{output: strings.Repeat("x", 60000)})

	cfg := AgentLoopConfig{
		Config: Config{
			Provider:  recorder,
			Model:     recorder.Models()[0],
			Mode:      "yolo",
			MaxTokens: 512,
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     2,
	}

	a := NewWithLoopConfig(cfg, registry)
	for range a.Run(context.Background(), "run bash") {
	}

	if len(recorder.calls) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(recorder.calls))
	}

	var guardMessage string
	for _, msg := range recorder.calls[1].Messages {
		if msg.Role == "toolResult" && msg.ToolName == "bash" {
			guardMessage = messageTextForTest(msg)
			if strings.Contains(guardMessage, strings.Repeat("x", 1000)) {
				t.Fatal("second provider call included oversized raw tool result")
			}
			break
		}
	}
	if !strings.Contains(guardMessage, "[Context guard]") {
		t.Fatalf("second provider call missing context guard tool result: %#v", recorder.calls[1].Messages)
	}
	if !strings.Contains(guardMessage, "offset/limit") || !strings.Contains(guardMessage, "maxResults") {
		t.Fatalf("context guard did not instruct the model to narrow scope: %q", guardMessage)
	}
}

func messageTextForTest(msg provider.Message) string {
	if msg.Content != "" || len(msg.Contents) == 0 {
		return msg.Content
	}
	var parts []string
	for _, block := range msg.Contents {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestToolOnlyWarningAppendedAfterToolResults(t *testing.T) {
	mockProvider := newLoopingToolProvider()

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)

	var stopped bool
	cfg := AgentLoopConfig{
		Config: Config{
			Provider: mockProvider,
			Model:    mockProvider.Models()[0],
			Mode:     "agent",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     95,
		ShouldStopAfterTurn: func(ctx ShouldStopAfterTurnContext) bool {
			for _, msg := range ctx.NewMessages {
				if msg.Role == "user" && contains(msg.Content, "You have been making tool calls") {
					stopped = true
					return true
				}
			}
			return false
		},
	}

	a := NewWithLoopConfig(cfg, registry)
	ch := a.Run(context.Background(), "keep using tools")

	for range ch {
	}

	if !stopped {
		t.Fatal("expected warning-triggered stop")
	}

	messages := a.GetMessages()
	warningIndex := -1
	for i, msg := range messages {
		if msg.Role == "user" && contains(msg.Content, "You have been making tool calls") {
			warningIndex = i
			break
		}
	}
	if warningIndex < 2 {
		t.Fatalf("warning index = %d, want at least 2", warningIndex)
	}
	if messages[warningIndex-1].Role != "toolResult" {
		t.Fatalf("message before warning role = %q, want toolResult", messages[warningIndex-1].Role)
	}
	if messages[warningIndex-2].Role != "assistant" {
		t.Fatalf("message before tool result role = %q, want assistant", messages[warningIndex-2].Role)
	}
}

func TestShouldStopAfterTurnDoneIncludesFinalMetadata(t *testing.T) {
	toolCall := &provider.ToolCallBlock{
		ID:        "call_1",
		Name:      "unknown_tool",
		Arguments: []byte(`{}`),
	}
	responses := []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamToolCall, ToolCall: toolCall},
		{Type: provider.StreamUsage, Usage: &provider.Usage{Input: 10, Output: 3}},
		{Type: provider.StreamDone, StopReason: "tool_use"},
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 50000, MaxTokens: 512},
	}, responses)

	cfg := AgentLoopConfig{
		Config: Config{
			Provider:  mockProvider,
			Model:     mockProvider.Models()[0],
			Mode:      "agent",
			MaxTokens: 512,
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     2,
		ShouldStopAfterTurn: func(ctx ShouldStopAfterTurnContext) bool {
			return true
		},
	}
	a := NewWithLoopConfig(cfg, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))

	var done *Event
	for event := range a.Run(context.Background(), "use a tool") {
		if event.Type == EventDone {
			ev := event
			done = &ev
		}
	}

	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.StopReason != "should_stop" {
		t.Fatalf("stop reason = %q, want should_stop", done.StopReason)
	}
	if done.Usage == nil {
		t.Fatal("expected EventDone to include usage")
	}
	if done.ContextUsage == nil {
		t.Fatal("expected EventDone to include context usage")
	}
}

func TestSessionSaveErrorEmitsAgentEnd(t *testing.T) {
	responses := []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "hello"},
		{Type: provider.StreamDone},
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, responses)

	sess := session.New(t.TempDir(), t.TempDir())
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionFile := sess.GetFile()
	dbPath := filepath.Join(filepath.Dir(sessionFile), "sessions.db")
	if err := os.Chmod(dbPath, 0400); err != nil {
		t.Fatalf("chmod sessions.db read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dbPath, 0600)
	})

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
		Session:  sess,
	}
	a := New(cfg, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))

	var events []Event
	for event := range a.RunWithMessages(context.Background(), []provider.Message{provider.NewUserMessage("test")}) {
		events = append(events, event)
	}

	if len(events) == 0 {
		t.Fatal("expected events")
	}
	if events[len(events)-1].Type != EventAgentEnd {
		t.Fatalf("last event = %v, want EventAgentEnd", events[len(events)-1].Type)
	}
	var sawError bool
	for _, event := range events {
		if event.Type == EventError && event.Error != nil && strings.Contains(event.Error.Error(), "save assistant message to session") {
			sawError = true
			break
		}
	}
	if !sawError {
		t.Fatal("expected save assistant message error")
	}
}

func TestInvalidToolArgumentsDoNotBreakSessionSave(t *testing.T) {
	responses := []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
			ID:        "call_1",
			Name:      "bash",
			Arguments: json.RawMessage(`]`),
		}},
		{Type: provider.StreamDone},
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, responses)

	sess := session.New(t.TempDir(), t.TempDir())
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}

	cfg := AgentLoopConfig{
		Config: Config{
			Provider: mockProvider,
			Model:    mockProvider.Models()[0],
			Mode:     "agent",
			Session:  sess,
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     1,
	}
	a := NewWithLoopConfig(cfg, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))

	var events []Event
	for event := range a.Run(context.Background(), "test") {
		events = append(events, event)
	}

	var sawParseError bool
	for _, event := range events {
		if event.Type == EventError && event.Error != nil && strings.Contains(event.Error.Error(), "save assistant message to session") {
			t.Fatalf("unexpected session save error: %v", event.Error)
		}
		if event.Type == EventToolExecutionEnd && event.ToolError != nil && strings.Contains(event.ToolError.Error(), "invalid character ']'") {
			sawParseError = true
		}
	}
	if !sawParseError {
		t.Fatal("expected tool argument parse error")
	}

	var foundSanitized, foundInvalid bool
	err := filepath.Walk(filepath.Dir(filepath.Dir(sess.GetFile())), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(data), `"arguments":{}`) {
			foundSanitized = true
		}
		if strings.Contains(string(data), `"invalidArguments":"]"`) {
			foundInvalid = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk session files: %v", err)
	}
	if !foundSanitized {
		t.Fatal("expected sanitized arguments in session")
	}
	if !foundInvalid {
		t.Fatal("expected original invalid arguments in session")
	}
	if _, err := session.Open(sess.GetFile()); err != nil {
		t.Fatalf("reopen session: %v", err)
	}
}

func TestCallbackSnapshotDoesNotExposeInternalSlices(t *testing.T) {
	mockProvider := newMockProvider()
	a := New(Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
	}, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))

	a.messages = []provider.Message{
		provider.NewAssistantMessage([]provider.ContentBlock{{
			Type: "toolCall",
			ToolCall: &provider.ToolCallBlock{
				ID:        "call-1",
				Name:      "read",
				Arguments: json.RawMessage(`{"path":"a"}`),
			},
		}}),
	}
	a.context.Messages = a.messages

	messages, ctx := a.callbackSnapshot()
	messages[0].Contents[0].ToolCall.Name = "mutated"
	ctx.Messages[0].Contents[0].ToolCall.Arguments[0] = '{'

	if a.messages[0].Contents[0].ToolCall.Name != "read" {
		t.Fatalf("internal tool name mutated: %s", a.messages[0].Contents[0].ToolCall.Name)
	}
	if string(a.context.Messages[0].Contents[0].ToolCall.Arguments) != `{"path":"a"}` {
		t.Fatalf("internal arguments mutated: %s", string(a.context.Messages[0].Contents[0].ToolCall.Arguments))
	}
}

func TestAgentRunSequential(t *testing.T) {
	toolCall1 := &provider.ToolCallBlock{
		ID:        "call_1",
		Name:      "ls",
		Arguments: []byte(`{"path": "."}`),
	}

	// First call returns tool call, second call returns text
	callCount := 0
	responses := func() []provider.StreamEvent {
		callCount++
		if callCount == 1 {
			return []provider.StreamEvent{
				{Type: provider.StreamStart},
				{Type: provider.StreamToolCall, ToolCall: toolCall1},
				{Type: provider.StreamDone},
			}
		}
		return []provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamTextDelta, TextDelta: "Done"},
			{Type: provider.StreamDone},
		}
	}

	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, responses())

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry("/tmp", sb)
	registry.RegisterDefaults()

	cfg := AgentLoopConfig{
		Config: Config{
			Provider: mockProvider,
			Model:    mockProvider.Models()[0],
			Mode:     "agent",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     10,
	}

	a := NewWithLoopConfig(cfg, registry)
	ch := a.Run(context.Background(), "test")

	var events []Event
	for event := range ch {
		events = append(events, event)
	}

	// Should have tool execution and text events
	hasToolExecution := false
	for _, event := range events {
		if event.Type == EventToolExecutionStart {
			hasToolExecution = true
		}
	}

	if !hasToolExecution {
		t.Error("expected tool execution event")
	}
}

func TestImageGenerationToolDefinitionUsesConfiguredResponsesProvider(t *testing.T) {
	settings := &config.Settings{
		DefaultProvider: "openai",
		Providers: map[string]*config.ProviderConfig{
			"openai": {API: "openai-responses"},
		},
	}
	def, ok := imageGenerationToolDefinition(settings, "openai")
	if !ok {
		t.Fatal("expected image generation tool definition")
	}
	if def.Name != "image_generation" || def.Kind != "hosted" || def.ProviderType != "openai-responses" {
		t.Fatalf("image generation definition = %#v", def)
	}
}

func TestConfiguredWebSearchToolDefinitionCarriesModelMetadata(t *testing.T) {
	settings := &config.Settings{
		WebSearch: config.WebSearchSettings{
			Enabled:      config.BoolPtr(true),
			Provider:     "anthropic",
			ProviderType: "anthropic-messages",
			Model:        "claude-sonnet-4-20250514",
		},
	}
	def, ok := configuredWebSearchToolDefinition(settings)
	if !ok {
		t.Fatal("expected web search tool definition")
	}
	if def.Name != "web_search" {
		t.Fatalf("name = %q, want web_search", def.Name)
	}
	if def.Provider != "anthropic" {
		t.Fatalf("provider = %q, want anthropic", def.Provider)
	}
	if def.ProviderType != "anthropic-messages" {
		t.Fatalf("providerType = %q, want anthropic-messages", def.ProviderType)
	}
	if def.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("model = %q, want claude-sonnet-4-20250514", def.Model)
	}
}

func TestConfiguredWebSearchToolDefinitionResolvesProviderReference(t *testing.T) {
	settings := &config.Settings{
		DefaultProvider: "gpt",
		WebSearch: config.WebSearchSettings{
			Enabled:      config.BoolPtr(true),
			Provider:     "gpt",
			ProviderType: "openai-responses",
		},
		Providers: map[string]*config.ProviderConfig{
			"gpt": {
				BaseURL: "https://co.yes.vg/v1",
				API:     "openai-responses",
			},
		},
	}
	def, ok := configuredWebSearchToolDefinition(settings)
	if !ok {
		t.Fatal("expected web search tool definition")
	}
	if def.Provider != "gpt" {
		t.Fatalf("provider = %q, want gpt", def.Provider)
	}
	if def.ProviderType != "openai-responses" {
		t.Fatalf("providerType = %q, want openai-responses", def.ProviderType)
	}
	if def.Provider == "" {
		t.Fatal("expected hosted provider to be resolved")
	}
}

func TestOpenAIResponsesWebSearchToolDefinition(t *testing.T) {
	p := provider.NewMockProvider("gpt", nil, nil)
	p.SetAPI("openai-responses")
	def, ok := openAIResponsesWebSearchToolDefinition(p)
	if !ok {
		t.Fatal("expected native OpenAI Responses web search definition")
	}
	if def.Name != provider.HostedToolOpenAIResponsesWebSearch || def.ProviderType != "openai-responses" {
		t.Fatalf("definition = %#v", def)
	}
}

func TestAgentInjectsNativeOpenAIResponsesWebSearchWithoutLocalSetting(t *testing.T) {
	p := provider.NewMockProvider("gpt", nil, nil)
	p.SetAPI("openai-responses")
	a := New(Config{Provider: p, Mode: "agent"}, tools.NewRegistry(".", sandbox.NewNoneSandbox()))
	for _, def := range a.context.Tools {
		if def.Name == provider.HostedToolOpenAIResponsesWebSearch {
			return
		}
	}
	t.Fatal("expected OpenAI Responses native web search to be injected")
}

func TestBuildSystemPrompt(t *testing.T) {
	toolNames := []string{"read", "write", "bash"}
	cwd := "/home/user/project"
	extraContext := "## Extra\nSome extra context"
	toolSnippets := map[string]string{
		"read":  "Read file contents",
		"write": "Create or overwrite files",
		"bash":  "Execute bash commands",
	}
	toolGuidelines := []string{"Use read to examine files instead of cat or sed."}

	prompt := BuildSystemPrompt("agent", toolNames, cwd, "", extraContext, toolSnippets, toolGuidelines, false, false, false)

	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}

	// Check that prompt contains expected content
	if !contains(prompt, "MothX") {
		t.Error("expected prompt to contain 'MothX'")
	}

	if !contains(prompt, "/home/user/project") {
		t.Error("expected prompt to contain working directory")
	}

	if !contains(prompt, "read") {
		t.Error("expected prompt to contain tool names")
	}

	if !contains(prompt, "Extra") {
		t.Error("expected prompt to contain extra context")
	}

	if !contains(prompt, ".skills/<name>/SKILL.md") {
		t.Error("expected prompt to direct project skill creation to .skills")
	}

	if !contains(prompt, "Do not create legacy .vibe directories for skills") {
		t.Error("expected prompt to forbid legacy .vibe skill directories")
	}
}

func TestBuildSystemPromptModes(t *testing.T) {
	// Test plan mode
	planPrompt := BuildSystemPrompt("plan", nil, "/tmp", "", "", nil, nil, false, false, false)
	if !contains(planPrompt, "PLAN") {
		t.Error("expected plan prompt to contain 'PLAN'")
	}

	if !contains(planPrompt, "READ-ONLY") {
		t.Error("expected plan prompt to contain 'READ-ONLY'")
	}

	// Test agent mode
	agentPrompt := BuildSystemPrompt("agent", nil, "/tmp", "", "", nil, nil, false, false, false)
	if !contains(agentPrompt, "AGENT") {
		t.Error("expected agent prompt to contain 'AGENT'")
	}

	// Test yolo mode
	yoloPrompt := BuildSystemPrompt("yolo", nil, "/tmp", "", "", nil, nil, false, false, false)
	if !contains(yoloPrompt, "YOLO") {
		t.Error("expected yolo prompt to contain 'YOLO'")
	}

	// Test unknown mode
	unknownPrompt := BuildSystemPrompt("custom", nil, "/tmp", "", "", nil, nil, false, false, false)
	if !contains(unknownPrompt, "CUSTOM") {
		t.Error("expected unknown prompt to contain mode name")
	}
}

func TestBuildSystemPromptToolExecutionProtocol(t *testing.T) {
	parallelPrompt := BuildSystemPromptWithOptions(
		"agent", []string{"read", "write"}, "/tmp", "", "", nil, nil,
		false, false, false,
		SystemPromptOptions{ToolExecutionMode: "parallel", MaxToolConcurrency: 6},
	)
	for _, want := range []string{
		"## Tool Execution Protocol",
		"group independent calls in one response",
		"no data dependency and no shared side effect",
		"at most 6 local tool call(s) in flight per batch",
		"does not control provider-hosted tools",
	} {
		if !contains(parallelPrompt, want) {
			t.Errorf("parallel prompt missing %q", want)
		}
	}

	sequentialPrompt := BuildSystemPromptWithOptions(
		"agent", nil, "/tmp", "", "", nil, nil,
		false, false, false,
		SystemPromptOptions{ToolExecutionMode: "sequential", MaxToolConcurrency: 1},
	)
	if !contains(sequentialPrompt, "Local execution policy: sequential mode, at most 1 local tool call(s) in flight per batch") {
		t.Error("sequential prompt missing resolved local execution policy")
	}
}

func TestAgentFrozenPromptUsesToolExecutionSettings(t *testing.T) {
	settings := &config.Settings{ToolExecution: config.ToolExecutionSettings{Mode: "sequential", MaxConcurrency: 3}}
	a := New(Config{Settings: settings, Mode: "agent"}, tools.NewRegistry("/tmp", sandbox.NewNoneSandbox()))
	if !contains(a.frozenSystemPrompt, "Local execution policy: sequential mode, at most 3 local tool call(s) in flight per batch") {
		t.Fatal("frozen prompt does not include normalized tool execution settings")
	}
}

func TestBuildSystemPromptAuthored(t *testing.T) {
	const trailer = "Co-Authored-By: MothX <harness@mothx.net>"

	disabled := BuildSystemPromptWithOptions(
		"agent", nil, "/tmp", "", "project context", nil, nil,
		false, false, false, SystemPromptOptions{},
	)
	if contains(disabled, trailer) {
		t.Fatal("authored trailer should be absent when disabled")
	}

	enabled := BuildSystemPromptWithOptions(
		"agent", nil, "/tmp", "", "project context", nil, nil,
		false, false, false, SystemPromptOptions{Authored: true},
	)
	if !strings.HasSuffix(strings.TrimSpace(enabled), trailer) {
		t.Fatalf("authored trailer must be the final system prompt text, got suffix %q", enabled[max(0, len(enabled)-len(trailer)-20):])
	}
}

func TestAgentFrozenPromptUsesAuthoredSetting(t *testing.T) {
	settings := &config.Settings{Authored: true}
	a := New(Config{Settings: settings, Mode: "agent"}, tools.NewRegistry("/tmp", sandbox.NewNoneSandbox()))
	if !strings.HasSuffix(strings.TrimSpace(a.frozenSystemPrompt), "Co-Authored-By: MothX <harness@mothx.net>") {
		t.Fatal("frozen prompt does not include authored commit trailer")
	}
}

func TestBuildSystemPromptMultiAgentGated(t *testing.T) {
	defaultPrompt := BuildSystemPrompt("agent", nil, "/tmp", "", "", nil, nil, false, false, false)
	if contains(defaultPrompt, "Sub-Agent Tools") {
		t.Error("expected default prompt to omit sub-agent instructions")
	}

	multiPrompt := BuildSystemPrompt("agent", []string{"subagent_spawn"}, "/tmp", "", "", nil, nil, true, false, false)
	if !contains(multiPrompt, "Sub-Agent Tools") {
		t.Error("expected multi-agent prompt to include sub-agent instructions")
	}
	if !contains(multiPrompt, "Act as the orchestrator") {
		t.Error("expected multi-agent prompt to include orchestration guidance")
	}
}

func TestBuildSystemPromptDelegateModeGated(t *testing.T) {
	defaultPrompt := BuildSystemPrompt("agent", nil, "/tmp", "", "", nil, nil, false, false, false)
	if contains(defaultPrompt, "Delegation Mode") {
		t.Error("expected default prompt to omit delegation instructions")
	}

	delegatePrompt := BuildSystemPrompt("agent", []string{"delegate_subagent"}, "/tmp", "", "", nil, nil, false, true, false)
	if !contains(delegatePrompt, "Delegation Mode") {
		t.Error("expected delegate prompt to include delegation instructions")
	}
	if !contains(delegatePrompt, "delegate_subagent") {
		t.Error("expected delegate prompt to mention delegate_subagent")
	}
}

func TestBuildSystemPromptWorkflowGated(t *testing.T) {
	defaultPrompt := BuildSystemPrompt("agent", nil, "/tmp", "", "", nil, nil, false, false, false)
	if contains(defaultPrompt, "Workflow Tools") {
		t.Error("expected default prompt to omit workflow instructions")
	}
	if contains(defaultPrompt, "Sub-Agent Tools") {
		t.Error("expected default prompt to omit sub-agent instructions")
	}
	if contains(defaultPrompt, "legacy workflow wording: old VM scope") || contains(defaultPrompt, "legacy workflow wording: Workflow DSL forms") || contains(defaultPrompt, "legacy workflow wording: Syntax checklist before workflow_run") {
		t.Error("expected default prompt to omit legacy workflow DSL reference")
	}

	workflowPrompt := BuildSystemPrompt("agent", []string{"workflow_run"}, "/tmp", "", "", nil, nil, false, false, true)
	if !contains(workflowPrompt, "Workflow Tools") {
		t.Error("expected workflow prompt to include workflow instructions")
	}
	if contains(workflowPrompt, "Sub-Agent Tools") {
		t.Error("expected workflow prompt to omit sub-agent instructions")
	}
	for _, want := range []string{
		"The workflow_run source must be raw JavaScript text, not Markdown",
		"Use result, resultKey, resultLatest, results, and log for runtime expressions",
		"workflow, phase, and agent names must be string literals",
		"Use plain JavaScript arrays for tools",
	} {
		if !contains(workflowPrompt, want) {
			t.Errorf("expected workflow prompt to contain %q", want)
		}
	}
	for _, unwanted := range []string{
		"legacy workflow wording: old VM scope",
		"legacy workflow wording: Workflow DSL forms",
		"Supported special forms:",
		"Supported builtins:",
		"Minimal valid skeleton",
		"legacy workflow wording: Syntax checklist before workflow_run",
		"(workflow \"auth audit\"",
	} {
		if contains(workflowPrompt, unwanted) {
			t.Errorf("expected workflow prompt to omit detailed tutorial text %q", unwanted)
		}
	}
	if contains(workflowPrompt, "JSON DSL") {
		t.Error("expected workflow prompt to avoid JSON DSL negative guidance")
	}
}

func TestToolResultImageCapabilityGate(t *testing.T) {
	a := &Agent{config: AgentLoopConfig{Config: Config{Model: &provider.Model{Input: []string{"text"}}}}}
	contents := []provider.ContentBlock{{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "base64data"}}}
	content, gatedContents, isError, err := a.gateToolResultImages("image output", contents, false)
	if err == nil || !isError || gatedContents != nil {
		t.Fatalf("gate = content %q, contents %#v, isError %v, err %v", content, gatedContents, isError, err)
	}
	if content != unsupportedImageToolResultMessage || !strings.Contains(err.Error(), "does not support image input") {
		t.Fatalf("gate error = %q, want capability message", content)
	}
}

func TestBuildRequestMessagesRetainsImagesForUnsupportedModel(t *testing.T) {
	a := &Agent{
		config:   AgentLoopConfig{Config: Config{Model: &provider.Model{Input: []string{"text"}}}},
		messages: []provider.Message{{Role: "toolResult", Contents: []provider.ContentBlock{{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "base64data"}}}}},
	}
	messages := a.buildRequestMessages(provider.NewSystemInjectedUserMessage("context"))
	if len(messages) != 2 || len(messages[1].Contents) != 1 || messages[1].Contents[0].Type != "image" {
		t.Fatalf("buildRequestMessages dropped image content: %#v", messages)
	}
}

func TestExecuteToolCallRejectsUnsupportedImageResult(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(richImageTool{})
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "text-model", Input: []string{"text"}}}, nil)
	a := New(Config{Provider: mockProvider, Model: mockProvider.Models()[0], Mode: "yolo"}, registry)
	events := make(chan Event, 8)
	result := a.executeSingleToolCall(context.Background(), provider.ToolCallBlock{ID: "call-image", Name: "image_tool", Arguments: []byte(`{}`)}, "", events, nil)
	if !result.IsError || len(result.Contents) != 0 || result.Content != unsupportedImageToolResultMessage {
		t.Fatalf("tool result = %#v, want explicit image capability error", result)
	}
	var sawError bool
	close(events)
	for event := range events {
		if event.Type == EventToolExecutionEnd && event.ToolError != nil {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected tool execution error event for unsupported image result")
	}
}

func TestValidateImageRequestBudgetRejectsStrictProviderPayload(t *testing.T) {
	model := &provider.Model{ID: "vision", Input: []string{"text", "image"}}
	groq := provider.NewMockProvider("groq", []*provider.Model{model}, nil)
	a := &Agent{config: AgentLoopConfig{Config: Config{Provider: groq, Vendor: "groq", Model: model}}}
	imageData := strings.Repeat("A", (4<<20)-128)
	messages := []provider.Message{{Role: "toolResult", Contents: []provider.ContentBlock{{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: imageData, Detail: "raw"}}}}}
	if err := a.validateImageRequestBudget(messages); err == nil || !strings.Contains(err.Error(), "provider limit") {
		t.Fatalf("expected strict provider payload error, got %v", err)
	}
}

func TestValidateImageRequestBudgetRejectsImageCount(t *testing.T) {
	model := &provider.Model{ID: "vision", Input: []string{"text", "image"}}
	groq := provider.NewMockProvider("groq", []*provider.Model{model}, nil)
	a := &Agent{config: AgentLoopConfig{Config: Config{Provider: groq, Vendor: "groq", Model: model}}}
	contents := make([]provider.ContentBlock, 6)
	for i := range contents {
		contents[i] = provider.ContentBlock{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "aW1hZ2U="}}
	}
	if err := a.validateImageRequestBudget([]provider.Message{{Role: "user", Contents: contents}}); err == nil || !strings.Contains(err.Error(), "provider limit is 5") {
		t.Fatalf("expected image count error, got %v", err)
	}
}

func TestSupportsImages(t *testing.T) {
	a := &Agent{config: AgentLoopConfig{}}
	a.config.Model = &provider.Model{Input: []string{"text"}}
	if a.supportsImages() {
		t.Error("expected false for text-only model")
	}

	a.config.Model = &provider.Model{Input: []string{"text", "image"}}
	if !a.supportsImages() {
		t.Error("expected true for text+image model")
	}

	a.config.Model = nil
	if a.supportsImages() {
		t.Error("expected false for nil model")
	}
}

func TestFormatToolListWithSnippets(t *testing.T) {
	// Test with tools and snippets
	tools := []string{"read", "write", "bash"}
	snippets := map[string]string{"read": "Read a file", "write": "Write a file"}
	list := formatToolListWithSnippets(tools, snippets)

	if !contains(list, "read") {
		t.Error("expected list to contain 'read'")
	}

	if !contains(list, "Read a file") {
		t.Error("expected list to contain snippet")
	}

	// Test empty
	emptyList := formatToolListWithSnippets(nil, nil)
	if emptyList != "(none)" {
		t.Errorf("expected empty list to say '(none)', got %q", emptyList)
	}
}

func TestBuildSkillsContext(t *testing.T) {
	skills := []SkillInfo{
		{Name: "test", Description: "Test skill", Path: "/path/to/skill"},
	}

	context := BuildSkillsContext(skills)

	if context == "" {
		t.Fatal("expected non-empty context")
	}

	if !contains(context, "test") {
		t.Error("expected context to contain skill name")
	}

	// Test empty
	emptyContext := BuildSkillsContext(nil)
	if emptyContext != "" {
		t.Error("expected empty context for nil skills")
	}
}

func TestBuildContextFilesContext(t *testing.T) {
	files := []ContextFileInfo{
		{Name: "AGENTS.md", Path: "/path", Scope: "project", Content: "# Test"},
	}

	context := BuildContextFilesContext(files)

	if context == "" {
		t.Fatal("expected non-empty context")
	}

	if !contains(context, "AGENTS.md") {
		t.Error("expected context to contain file name")
	}

	// Test empty
	emptyContext := BuildContextFilesContext(nil)
	if emptyContext != "" {
		t.Error("expected empty context for nil files")
	}
}

func TestBaseProvider(t *testing.T) {
	models := []*provider.Model{
		{ID: "model1", Name: "Model 1"},
		{ID: "model2", Name: "Model 2"},
	}

	p := NewBaseProvider("test", models)

	if p.Name() != "test" {
		t.Errorf("expected name 'test', got '%s'", p.Name())
	}

	if len(p.Models()) != 2 {
		t.Errorf("expected 2 models, got %d", len(p.Models()))
	}

	m := p.GetModel("model1")
	if m == nil {
		t.Fatal("expected model, got nil")
	}

	if m.Name != "Model 1" {
		t.Errorf("expected name 'Model 1', got '%s'", m.Name)
	}

	m = p.GetModel("nonexistent")
	if m != nil {
		t.Error("expected nil for nonexistent model")
	}
}

// --- ContextWithAgentID tests ---

func TestContextWithAgentID(t *testing.T) {
	ctx := context.Background()
	ctx = ContextWithAgentID(ctx, "test-agent")

	id, ok := AgentIDFromContext(ctx)
	if !ok {
		t.Fatal("expected agent ID in context")
	}
	if id != "test-agent" {
		t.Errorf("agent ID = %q, want 'test-agent'", id)
	}

	// Missing from context
	_, ok = AgentIDFromContext(context.Background())
	if ok {
		t.Error("expected no agent ID in empty context")
	}
}

func TestContextWithEventChan(t *testing.T) {
	ch := make(chan Event, 1)
	ctx := ContextWithEventChan(context.Background(), ch)

	got, ok := EventChanFromContext(ctx)
	if !ok {
		t.Fatal("expected event chan in context")
	}
	if got == nil {
		t.Fatal("expected non-nil event chan")
	}

	_, ok = EventChanFromContext(context.Background())
	if ok {
		t.Error("expected no event chan in empty context")
	}
}

func TestContextWithParentRunContext(t *testing.T) {
	parent := context.Background()
	ctx := ContextWithParentRunContext(context.Background(), parent)

	got, ok := ParentRunContextFromContext(ctx)
	if !ok {
		t.Fatal("expected parent run context")
	}
	if got != parent {
		t.Fatal("unexpected parent run context")
	}

	_, ok = ParentRunContextFromContext(context.Background())
	if ok {
		t.Error("expected no parent run context in empty context")
	}
}

// --- Manager status tests ---

func TestAgentManagerMarkRunning(t *testing.T) {
	m := NewAgentManager(&AgentFactory{})
	m.Create(AgentOptions{ID: "a1"})
	m.MarkRunning("a1")
	st, ok := m.Status("a1")
	if !ok {
		t.Fatal("expected status")
	}
	if st.State != "running" {
		t.Errorf("state = %q, want running", st.State)
	}
}

func TestAgentManagerMarkDone(t *testing.T) {
	m := NewAgentManager(&AgentFactory{})
	m.Create(AgentOptions{ID: "a1"})
	m.MarkDone("a1", "completed")
	st, _ := m.Status("a1")
	if st.State != "done" {
		t.Errorf("state = %q, want done", st.State)
	}
	if st.Result != "completed" {
		t.Errorf("result = %q, want completed", st.Result)
	}
}

func TestAgentManagerMarkError(t *testing.T) {
	m := NewAgentManager(&AgentFactory{})
	m.Create(AgentOptions{ID: "a1"})
	m.MarkError("a1", fmt.Errorf("test error"))
	st, _ := m.Status("a1")
	if st.State != "error" {
		t.Errorf("state = %q, want error", st.State)
	}
	if st.Error != "test error" {
		t.Errorf("error = %q, want 'test error'", st.Error)
	}
}

func TestAgentManagerMarkErrorNil(t *testing.T) {
	m := NewAgentManager(&AgentFactory{})
	m.Create(AgentOptions{ID: "a1"})
	m.MarkError("a1", nil)
	st, _ := m.Status("a1")
	if st.Error != "" {
		t.Errorf("error = %q, want empty", st.Error)
	}
}

func TestAgentManagerRegister(t *testing.T) {
	m := NewAgentManager(&AgentFactory{})
	// Create an agent through factory to get a valid agentpkg.Agent
	a, _ := m.Create(AgentOptions{ID: "parent"})
	m.Destroy("parent")
	// Re-register
	m.Register(a)
	if m.Count() != 1 {
		t.Errorf("count = %d, want 1", m.Count())
	}
}

func TestAgentManagerRegisterNil(t *testing.T) {
	m := NewAgentManager(&AgentFactory{})
	m.Register(nil) // Should not panic
	if m.Count() != 0 {
		t.Errorf("count = %d, want 0", m.Count())
	}
}

func TestAgentManagerStatusNotFound(t *testing.T) {
	m := NewAgentManager(&AgentFactory{})
	_, ok := m.Status("nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- ForceCompact tests ---

func TestSetForceCompact_ShouldCompactReturnsTrue(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 100000},
	}, nil)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
		CompactionSettings: ctxpkg.CompactionSettings{
			KeepRecentTokens: 1,
		},
	}

	a := New(cfg, registry)

	// Load some messages so there's something to compact
	a.LoadHistoryMessages([]provider.Message{
		provider.NewUserMessage("Hello"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "Hi there"}}),
		provider.NewUserMessage("Second turn"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "Second response"}}),
	})

	// Without force, ShouldCompact should be false (context is tiny)
	if a.ShouldCompact() {
		t.Fatal("ShouldCompact should be false without force and small context")
	}

	// Set force flag
	a.SetForceCompact()

	// Now ShouldCompact should return true (force flag set)
	if !a.ShouldCompact() {
		t.Fatal("ShouldCompact should be true after SetForceCompact")
	}

	// Force flag is consumed — second call should return false
	if a.ShouldCompact() {
		t.Fatal("ShouldCompact should be false after force flag was consumed")
	}
}

func TestSetForceCompact_NoMessagesDoesNotForce(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 100000},
	}, nil)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
	}

	a := New(cfg, registry)

	// No messages loaded — force should not trigger (nothing to compact)
	a.SetForceCompact()
	if a.ShouldCompact() {
		t.Fatal("ShouldCompact should be false with force but no messages")
	}
}

func TestShouldCompact_OverThresholdButNoCompactableMessages(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 100},
	}, nil)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)

	cfg := Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     "agent",
		CompactionSettings: ctxpkg.CompactionSettings{
			Enabled:          true,
			ReserveTokens:    10,
			KeepRecentTokens: 20,
		},
	}

	a := New(cfg, registry)
	a.LoadHistoryMessages([]provider.Message{
		provider.NewUserMessage("current request"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: strings.Repeat("x", 500)}}),
	})

	if a.ShouldCompact() {
		t.Fatal("ShouldCompact should be false when only the current turn would be kept")
	}
}

func TestCompactClearsKeptMessageUsage(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "sessions")
	sess := session.New(tmpDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	p := newCompactionReplayProvider()
	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(tmpDir, sb)
	a := New(Config{
		Provider: p,
		Model:    p.models[0],
		Mode:     "agent",
		Session:  sess,
		CompactionSettings: ctxpkg.CompactionSettings{
			Enabled:          true,
			ReserveTokens:    256,
			KeepRecentTokens: 1,
		},
	}, registry)

	history := []provider.Message{
		provider.NewUserMessage("old user context"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "old assistant context"}}),
		provider.NewUserMessage("recent user context"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "recent assistant context"}}),
	}
	history[3].Usage = &provider.Usage{Input: 1000, Output: 50, TotalTokens: 1050}
	for _, msg := range history {
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}
	a.LoadHistoryState(sess.GetReplayState().Messages, sess.GetReplayState().EntryIDs)

	eventCh := make(chan Event, 32)
	go func() {
		defer close(eventCh)
		if err := a.Compact(context.Background(), eventCh); err != nil {
			t.Errorf("Compact() error = %v", err)
		}
	}()
	for range eventCh {
	}

	messages := a.GetMessages()
	if len(messages) != 3 {
		t.Fatalf("messages len = %d, want 3", len(messages))
	}
	if messages[2].Usage != nil {
		t.Fatalf("kept assistant usage = %#v, want nil stale usage", messages[2].Usage)
	}
	usage := a.GetContextUsage()
	if usage == nil {
		t.Fatal("expected context usage")
	}
	if usage.Tokens >= 1050 {
		t.Fatalf("context usage tokens = %d, still using stale assistant usage", usage.Tokens)
	}
}

func TestCompactCanceledEmitsCanceledEndEvent(t *testing.T) {
	p := &canceledCompactionProvider{
		models: []*provider.Model{{ID: "model1", Name: "Model 1", ContextWindow: 4096, MaxTokens: 1024}},
	}
	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)
	a := New(Config{
		Provider: p,
		Model:    p.models[0],
		Mode:     "agent",
		CompactionSettings: ctxpkg.CompactionSettings{
			Enabled:          true,
			ReserveTokens:    256,
			KeepRecentTokens: 1,
		},
	}, registry)
	a.LoadHistoryMessages([]provider.Message{
		provider.NewUserMessage("old user context"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "old assistant context"}}),
		provider.NewUserMessage("recent user context"),
	})

	eventCh := make(chan Event, 8)
	err := a.Compact(context.Background(), eventCh)
	if err == nil {
		t.Fatal("Compact() error = nil, want context.Canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Compact() error = %v, want context.Canceled", err)
	}

	var end Event
	for i := 0; i < 2; i++ {
		end = <-eventCh
	}
	if end.Type != EventCompactionEnd {
		t.Fatalf("second event type = %v, want EventCompactionEnd", end.Type)
	}
	if end.Error != nil {
		t.Fatalf("compaction end error = %v, want nil for canceled", end.Error)
	}
	if end.StopReason != "canceled" {
		t.Fatalf("compaction end stop reason = %q, want canceled", end.StopReason)
	}
}

func TestSetForceCompact_NoModelDoesNotForce(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, nil)

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)

	cfg := Config{
		Provider: mockProvider,
		Model:    nil, // no model
		Mode:     "agent",
	}

	a := New(cfg, registry)
	a.LoadHistoryMessages([]provider.Message{
		provider.NewUserMessage("Hello"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "Hi"}}),
	})

	a.SetForceCompact()
	if a.ShouldCompact() {
		t.Fatal("ShouldCompact should be false with force but no model")
	}
}

func TestRunAutoCompactsBeforePlainTextTurn(t *testing.T) {
	p := newCompactionReplayProvider()
	p.models[0].ContextWindow = 8000
	p.models[0].MaxTokens = 512

	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)
	a := New(Config{
		Provider: p,
		Model:    p.models[0],
		Mode:     "agent",
		CompactionSettings: ctxpkg.CompactionSettings{
			Enabled:          true,
			ReserveTokens:    64,
			KeepRecentTokens: 1,
		},
	}, registry)
	oldUser := provider.NewUserMessage(strings.Repeat("old user ", 3000))
	recentUser := provider.NewUserMessage("recent user")
	a.LoadHistoryMessages([]provider.Message{
		oldUser,
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: strings.Repeat("old assistant ", 3000)}}),
		recentUser,
	})

	var eventTypes []EventType
	for ev := range a.Run(context.Background(), "continue") {
		eventTypes = append(eventTypes, ev.Type)
	}

	if len(p.calls) != 2 {
		t.Fatalf("provider call count = %d, want 2 (compaction + response); event types = %#v", len(p.calls), eventTypes)
	}
	actualCall := p.calls[1]
	foundSummary := false
	foundOldUser := false
	for _, msg := range actualCall.Messages {
		if msg.SystemInjected && msg.Content == "## Goal\ncheckpoint" {
			foundSummary = true
		}
		if msg.Content == oldUser.Content {
			foundOldUser = true
		}
	}
	if !foundSummary {
		t.Fatal("plain text turn did not include compacted summary")
	}
	if foundOldUser {
		t.Fatal("plain text turn still included pre-compaction old user message")
	}
}

func TestCompactionReplayPersistsAcrossSessionReload(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "sessions")
	sess := session.New(tmpDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	p := newCompactionReplayProvider()
	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(tmpDir, sb)
	a := New(Config{
		Provider: p,
		Model:    p.models[0],
		Mode:     "agent",
		Session:  sess,
		CompactionSettings: ctxpkg.CompactionSettings{
			Enabled:          true,
			ReserveTokens:    256,
			KeepRecentTokens: 48,
		},
	}, registry)

	oldUser := provider.NewUserMessage(strings.Repeat("old user ", 24))
	oldAssistant := provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: strings.Repeat("old assistant ", 24)}})
	recentUser := provider.NewUserMessage(strings.Repeat("recent user ", 20))
	recentAssistant := provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: strings.Repeat("recent assistant ", 20)}})
	history := []provider.Message{oldUser, oldAssistant, recentUser, recentAssistant}
	for _, msg := range history {
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}
	a.LoadHistoryState(sess.GetReplayState().Messages, sess.GetReplayState().EntryIDs)

	eventCh := make(chan Event, 32)
	go func() {
		defer close(eventCh)
		if err := a.Compact(context.Background(), eventCh); err != nil {
			t.Errorf("Compact() error = %v", err)
		}
	}()
	for range eventCh {
	}

	reopened, err := session.Open(sess.GetFile())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	replay := reopened.GetReplayState()

	a2 := New(Config{
		Provider: p,
		Model:    p.models[0],
		Mode:     "agent",
		Session:  reopened,
		CompactionSettings: ctxpkg.CompactionSettings{
			Enabled:          true,
			ReserveTokens:    256,
			KeepRecentTokens: 48,
		},
	}, registry)
	a2.LoadHistoryState(replay.Messages, replay.EntryIDs)

	runCh := a2.Run(context.Background(), "next step")
	for range runCh {
	}

	if len(p.calls) != 2 {
		t.Fatalf("provider call count = %d, want 2", len(p.calls))
	}

	continued := p.calls[1].Messages
	if len(continued) < 4 {
		t.Fatalf("continued call messages = %d, want at least 4", len(continued))
	}

	foundSummary := false
	foundOldUser := false
	foundRecentUser := false
	for _, msg := range continued {
		if msg.SystemInjected && msg.Content == "## Goal\ncheckpoint" {
			foundSummary = true
		}
		if msg.Content == oldUser.Content {
			foundOldUser = true
		}
		if msg.Content == recentUser.Content {
			foundRecentUser = true
		}
	}

	if !foundSummary {
		t.Fatal("continued run did not include compacted summary")
	}
	if foundOldUser {
		t.Fatal("continued run still included pre-compaction old user message")
	}
	if !foundRecentUser {
		t.Fatal("continued run lost recent user message after compaction replay")
	}
}

// --- MaxConsecutiveNoText tests ---

func TestMaxConsecutiveNoText_Default(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "m1", Name: "M1"}}, nil)
	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)

	a := NewWithLoopConfig(AgentLoopConfig{
		Config: Config{
			Provider: mockProvider,
			Model:    mockProvider.Models()[0],
			Mode:     "agent",
		},
	}, registry)

	// Default MaxConsecutiveNoText should be 200 (MaxIterations default)
	// but the threshold is 95. Verify the config field is 0 (uses default).
	if a.config.MaxConsecutiveNoText != 0 {
		t.Fatalf("expected default MaxConsecutiveNoText=0, got %d", a.config.MaxConsecutiveNoText)
	}
}

func TestMaxConsecutiveNoText_Custom(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "m1", Name: "M1"}}, nil)
	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)

	a := NewWithLoopConfig(AgentLoopConfig{
		Config: Config{
			Provider: mockProvider,
			Model:    mockProvider.Models()[0],
			Mode:     "agent",
		},
		MaxConsecutiveNoText: 10,
	}, registry)

	if a.config.MaxConsecutiveNoText != 10 {
		t.Fatalf("expected MaxConsecutiveNoText=10, got %d", a.config.MaxConsecutiveNoText)
	}
}

func TestEmptyResponseRetriesThenErrors(t *testing.T) {
	p := &emptyRecoveringProvider{
		models:     []*provider.Model{{ID: "model1", Name: "Model 1"}},
		emptyTimes: 10, // always empty -> exhaust retries and error
	}
	cfg := AgentLoopConfig{
		Config: Config{
			Provider: p,
			Model:    p.models[0],
			Mode:     "agent",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     20,
	}
	a := NewWithLoopConfig(cfg, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))

	var errEvent *Event
	for event := range a.Run(context.Background(), "test") {
		if event.Type == EventError && event.StopReason == "empty_response" {
			ev := event
			errEvent = &ev
		}
	}
	if errEvent == nil {
		t.Fatal("expected EventError with stopReason=empty_response")
	}
	// 1 initial call + 2 retries = 3 empty calls before erroring.
	if p.callCount != 3 {
		t.Fatalf("callCount = %d, want 3 (1 initial + 2 retries)", p.callCount)
	}
}

func TestEmptyResponseRecoversAfterRetry(t *testing.T) {
	p := &emptyRecoveringProvider{
		models:     []*provider.Model{{ID: "model1", Name: "Model 1"}},
		emptyTimes: 2, // first 2 empty, 3rd succeeds
	}
	cfg := AgentLoopConfig{
		Config: Config{
			Provider: p,
			Model:    p.models[0],
			Mode:     "agent",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     20,
	}
	a := NewWithLoopConfig(cfg, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))

	var done *Event
	for event := range a.Run(context.Background(), "test") {
		if event.Type == EventDone {
			ev := event
			done = &ev
		}
	}
	if done == nil {
		t.Fatal("expected EventDone after retry recovery")
	}
	if done.StopReason != "stop" {
		t.Fatalf("stopReason = %q, want stop", done.StopReason)
	}
	if p.callCount != 3 {
		t.Fatalf("callCount = %d, want 3 (2 empty + 1 success)", p.callCount)
	}
	var found bool
	for _, msg := range a.GetMessages() {
		if msg.Role == "assistant" && strings.Contains(messageTextForTest(msg), "done") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected recovered assistant text in messages")
	}
}

func TestStreamTimeoutRetriesThenErrors(t *testing.T) {
	p := &timeoutRecoveringProvider{
		models:       []*provider.Model{{ID: "model1", Name: "Model 1"}},
		timeoutTimes: 10, // always timeout -> exhaust retries and error
	}
	cfg := AgentLoopConfig{
		Config: Config{
			Provider: p,
			Model:    p.models[0],
			Mode:     "agent",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     20,
	}
	a := NewWithLoopConfig(cfg, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))

	var errEvent *Event
	statusCount := 0
	for event := range a.Run(context.Background(), "test") {
		if event.Type == EventError {
			ev := event
			errEvent = &ev
		}
		if event.Type == EventStatus && strings.HasPrefix(event.StatusMessage, "⚠️") {
			statusCount++
		}
	}
	if errEvent == nil {
		t.Fatal("expected EventError after exhausting stream-timeout retries")
	}
	// 1 initial + 2 auto-retries = 3 provider calls before giving up.
	if p.callCount != 3 {
		t.Fatalf("callCount = %d, want 3 (1 initial + 2 retries)", p.callCount)
	}
	if statusCount != 2 {
		t.Fatalf("friendly retry status count = %d, want 2", statusCount)
	}
	if !strings.Contains(errEvent.Error.Error(), "供应商响应超时") {
		t.Fatalf("expected friendly timeout error, got %q", errEvent.Error)
	}
}

func TestStreamTimeoutRetriesThenRecovers(t *testing.T) {
	p := &timeoutRecoveringProvider{
		models:       []*provider.Model{{ID: "model1", Name: "Model 1"}},
		timeoutTimes: 2, // first 2 timeout, 3rd succeeds
	}
	cfg := AgentLoopConfig{
		Config: Config{
			Provider: p,
			Model:    p.models[0],
			Mode:     "agent",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     20,
	}
	a := NewWithLoopConfig(cfg, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))

	var done *Event
	for event := range a.Run(context.Background(), "test") {
		if event.Type == EventDone {
			ev := event
			done = &ev
		}
	}
	if done == nil {
		t.Fatal("expected EventDone after stream-timeout retry recovery")
	}
	if done.StopReason != "stop" {
		t.Fatalf("stopReason = %q, want stop", done.StopReason)
	}
	if p.callCount != 3 {
		t.Fatalf("callCount = %d, want 3 (2 timeout + 1 success)", p.callCount)
	}
}

func TestAbortDuringToolExecutionRecordsToolResult(t *testing.T) {
	mockProvider := &workflowRunToolProvider{
		models: []*provider.Model{{ID: "model1", Name: "Model 1"}},
	}
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(blockingWorkflowRunTool{delay: time.Minute})

	cfg := AgentLoopConfig{
		Config: Config{
			Provider: mockProvider,
			Model:    mockProvider.Models()[0],
			Mode:     "yolo",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     10,
	}
	a := NewWithLoopConfig(cfg, registry)

	eventCh := a.Run(context.Background(), "test")

	// Wait until the tool is actually executing, then abort the run.
	deadline := time.After(10 * time.Second)
	started := false
	for !started {
		select {
		case event, ok := <-eventCh:
			if !ok {
				t.Fatal("event channel closed before tool execution started")
			}
			if event.Type == EventToolExecutionStart {
				started = true
			}
		case <-deadline:
			t.Fatal("tool execution did not start in time")
		}
	}

	abortStart := time.Now()
	a.Abort()

	var abortedErr *Event
	drainTimer := time.After(10 * time.Second)
drain:
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				break drain
			}
			if event.Type == EventError && event.StopReason == "aborted" {
				ev := event
				abortedErr = &ev
			}
		case <-drainTimer:
			t.Fatal("agent loop did not terminate after abort")
		}
	}
	if elapsed := time.Since(abortStart); elapsed > 5*time.Second {
		t.Fatalf("abort took %s; in-flight tool was not interrupted", elapsed)
	}
	if abortedErr == nil {
		t.Fatal("expected aborted error event")
	}

	// The interrupted tool call must still have a tool result recorded in
	// history, otherwise strict tool APIs (Kimi/OpenAI) reject the next
	// request with a 400 error about missing tool responses.
	var foundAssistantCall, foundToolResult bool
	for _, msg := range a.GetMessages() {
		for _, c := range msg.Contents {
			if c.Type == "toolCall" && c.ToolCall != nil && c.ToolCall.ID == "workflow_call_1" {
				foundAssistantCall = true
			}
		}
		if msg.Role == "toolResult" && msg.ToolCallID == "workflow_call_1" {
			foundToolResult = true
			if !msg.IsError {
				t.Fatal("interrupted tool result should be marked as error")
			}
		}
	}
	if !foundAssistantCall {
		t.Fatal("expected assistant tool call in history")
	}
	if !foundToolResult {
		t.Fatal("expected tool result for interrupted tool call in history")
	}
}

func TestRepairDanglingToolCalls(t *testing.T) {
	toolCallMsg := func(id, name string) provider.Message {
		return provider.NewAssistantMessage([]provider.ContentBlock{{
			Type: "toolCall",
			ToolCall: &provider.ToolCallBlock{
				ID:        id,
				Name:      name,
				Arguments: json.RawMessage(`{}`),
			},
		}})
	}

	t.Run("no tool calls returns input unchanged", func(t *testing.T) {
		msgs := []provider.Message{
			provider.NewUserMessage("hi"),
			provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "hello"}}),
		}
		out := repairDanglingToolCalls(msgs)
		if len(out) != len(msgs) {
			t.Fatalf("len(out) = %d, want %d", len(out), len(msgs))
		}
	})

	t.Run("valid history unchanged", func(t *testing.T) {
		msgs := []provider.Message{
			provider.NewUserMessage("hi"),
			toolCallMsg("call_1", "bash"),
			provider.NewToolResultMessage("call_1", "bash", "ok", false),
			provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "done"}}),
		}
		out := repairDanglingToolCalls(msgs)
		if len(out) != len(msgs) {
			t.Fatalf("len(out) = %d, want %d", len(out), len(msgs))
		}
		for i := range msgs {
			if out[i].Role != msgs[i].Role || out[i].ToolCallID != msgs[i].ToolCallID {
				t.Fatalf("message %d changed: %+v", i, out[i])
			}
		}
	})

	t.Run("dangling tool call gets synthesized error result", func(t *testing.T) {
		msgs := []provider.Message{
			provider.NewUserMessage("hi"),
			toolCallMsg("bash:0", "bash"),
			provider.NewUserMessage("next question"),
		}
		out := repairDanglingToolCalls(msgs)
		if len(out) != 4 {
			t.Fatalf("len(out) = %d, want 4", len(out))
		}
		synth := out[2]
		if synth.Role != "toolResult" || synth.ToolCallID != "bash:0" || synth.ToolName != "bash" {
			t.Fatalf("synthesized result = %+v", synth)
		}
		if !synth.IsError {
			t.Fatal("synthesized result should be marked as error")
		}
		if out[3].Role != "user" {
			t.Fatalf("user message should stay after synthesized result, got %s", out[3].Role)
		}
	})

	t.Run("result recorded later is moved adjacent", func(t *testing.T) {
		late := provider.NewToolResultMessage("bash:0", "bash", "interrupted output", true)
		msgs := []provider.Message{
			provider.NewUserMessage("hi"),
			toolCallMsg("bash:0", "bash"),
			provider.NewUserMessage("next question"),
			late,
		}
		out := repairDanglingToolCalls(msgs)
		if len(out) != 4 {
			t.Fatalf("len(out) = %d, want 4", len(out))
		}
		if out[2].Role != "toolResult" || out[2].ToolCallID != "bash:0" || out[2].Content != "interrupted output" {
			t.Fatalf("late result not moved adjacent: %+v", out[2])
		}
		if out[3].Role != "user" {
			t.Fatalf("expected user message last, got %s", out[3].Role)
		}
	})

	t.Run("multiple tool calls with partial results", func(t *testing.T) {
		assistant := provider.NewAssistantMessage([]provider.ContentBlock{
			{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "call_1", Name: "bash", Arguments: json.RawMessage(`{}`)}},
			{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "call_2", Name: "read", Arguments: json.RawMessage(`{}`)}},
		})
		msgs := []provider.Message{
			provider.NewUserMessage("hi"),
			assistant,
			provider.NewToolResultMessage("call_1", "bash", "ok", false),
		}
		out := repairDanglingToolCalls(msgs)
		if len(out) != 4 {
			t.Fatalf("len(out) = %d, want 4", len(out))
		}
		if out[2].ToolCallID != "call_1" || out[2].IsError {
			t.Fatalf("existing result broken: %+v", out[2])
		}
		if out[3].Role != "toolResult" || out[3].ToolCallID != "call_2" || !out[3].IsError {
			t.Fatalf("missing synthesized result for call_2: %+v", out[3])
		}
	})

	t.Run("input slice is not mutated", func(t *testing.T) {
		msgs := []provider.Message{
			toolCallMsg("bash:0", "bash"),
		}
		out := repairDanglingToolCalls(msgs)
		if len(msgs) != 1 {
			t.Fatalf("input mutated: len = %d", len(msgs))
		}
		if len(out) != 2 {
			t.Fatalf("len(out) = %d, want 2", len(out))
		}
	})
}

// overflowThenRecoverProvider fails the calls listed in failCalls with the
// given error messages and answers all other calls with a short text.
type overflowThenRecoverProvider struct {
	models    []*provider.Model
	calls     []provider.ChatParams
	failCalls map[int]string
}

func newOverflowThenRecoverProvider(failCalls map[int]string) *overflowThenRecoverProvider {
	return &overflowThenRecoverProvider{
		models: []*provider.Model{{
			ID:            "model1",
			Name:          "Model 1",
			ContextWindow: 262144,
			MaxTokens:     16384,
		}},
		failCalls: failCalls,
	}
}

func (p *overflowThenRecoverProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.calls = append(p.calls, provider.ChatParams{
		Messages: cloneMessages(params.Messages),
		Tools:    append([]provider.ToolDefinition(nil), params.Tools...),
	})

	ch := make(chan provider.StreamEvent, 3)
	callNumber := len(p.calls)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		if msg, ok := p.failCalls[callNumber]; ok {
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: errors.New(msg), StopReason: "error"}
			return
		}
		ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "## Goal\nrecovered"}
		ch <- provider.StreamEvent{Type: provider.StreamDone}
	}()
	return ch
}

func (p *overflowThenRecoverProvider) Name() string { return "overflow-recover" }
func (p *overflowThenRecoverProvider) API() string  { return "openai-chat" }
func (p *overflowThenRecoverProvider) Models() []*provider.Model {
	return p.models
}
func (p *overflowThenRecoverProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func TestRunRecoversFromContextOverflow(t *testing.T) {
	// The provider rejects the first request as too large even though the local
	// token estimate is below the auto-compaction threshold (underestimation).
	p := newOverflowThenRecoverProvider(map[int]string{
		1: "API error 400: This model's maximum context length is 262144 tokens",
	})
	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)
	a := New(Config{
		Provider: p,
		Model:    p.models[0],
		Mode:     "agent",
		CompactionSettings: ctxpkg.CompactionSettings{
			Enabled:          true,
			ReserveTokens:    64,
			KeepRecentTokens: 1,
		},
	}, registry)
	a.LoadHistoryMessages([]provider.Message{
		provider.NewUserMessage("old user"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "old assistant"}}),
	})

	var sawCompactionStart, sawError bool
	for ev := range a.Run(context.Background(), "continue") {
		switch ev.Type {
		case EventCompactionStart:
			sawCompactionStart = true
		case EventError:
			sawError = true
		}
	}

	if sawError {
		t.Fatal("run should recover from provider context overflow, not error")
	}
	if !sawCompactionStart {
		t.Fatal("overflow recovery should trigger compaction")
	}
	if len(p.calls) != 3 {
		t.Fatalf("provider call count = %d, want 3 (failed + compaction + retry)", len(p.calls))
	}
}

func TestRunFallsBackToTruncationWhenCompactionOverflows(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "sessions")
	sess := session.New(tmpDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Both the initial request and the summarization request overflow.
	p := newOverflowThenRecoverProvider(map[int]string{
		1: "API error 400: maximum context length exceeded",
		2: "API error 400: maximum context length exceeded",
	})
	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(tmpDir, sb)
	a := New(Config{
		Provider: p,
		Model:    p.models[0],
		Mode:     "agent",
		Session:  sess,
		CompactionSettings: ctxpkg.CompactionSettings{
			Enabled:          true,
			ReserveTokens:    256,
			KeepRecentTokens: 1,
		},
	}, registry)

	var history []provider.Message
	for i := 0; i < 4; i++ {
		history = append(history,
			provider.NewUserMessage(fmt.Sprintf("old user %d", i)),
			provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("old assistant %d", i)}}),
		)
	}
	for _, msg := range history {
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}
	a.LoadHistoryState(sess.GetReplayState().Messages, sess.GetReplayState().EntryIDs)

	var sawError bool
	for ev := range a.Run(context.Background(), "continue") {
		if ev.Type == EventError {
			sawError = true
		}
	}

	if sawError {
		t.Fatal("run should recover via truncation fallback, not error")
	}
	if len(p.calls) != 3 {
		t.Fatalf("provider call count = %d, want 3 (failed + failed compaction + retry)", len(p.calls))
	}

	retryMessages := p.calls[2].Messages
	foundRecoveryNote := false
	for _, msg := range retryMessages {
		if strings.Contains(msg.Content, "old user 0") {
			t.Fatal("retry request still included oldest history after truncation")
		}
		if msg.SystemInjected && strings.Contains(msg.Content, "Context recovery") {
			foundRecoveryNote = true
		}
	}
	if !foundRecoveryNote {
		t.Fatal("retry request should include the context recovery note")
	}

	// The truncation must persist so sessions rebuilt from storage (channels)
	// do not reload the overflowing history.
	reopened, err := session.Open(sess.GetFile())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	replay := reopened.GetReplayState()
	if len(replay.Messages) == 0 || !strings.Contains(replay.Messages[0].Content, "Context recovery") {
		t.Fatalf("replayed history should start with the recovery note, got %#v", replay.Messages)
	}
	for _, msg := range replay.Messages {
		if strings.Contains(msg.Content, "old user 0") {
			t.Fatal("replayed history still included oldest messages after persisted truncation")
		}
	}
}

func TestRunContextOverflowWithoutCompactionEnabled(t *testing.T) {
	p := newOverflowThenRecoverProvider(map[int]string{
		1: "API error 400: maximum context length exceeded",
	})
	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)
	a := New(Config{
		Provider:           p,
		Model:              p.models[0],
		Mode:               "agent",
		CompactionSettings: ctxpkg.CompactionSettings{Enabled: false},
	}, registry)
	a.LoadHistoryMessages([]provider.Message{provider.NewUserMessage("old user")})

	var sawError bool
	for ev := range a.Run(context.Background(), "continue") {
		if ev.Type == EventError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("overflow with compaction disabled should surface the provider error")
	}
	if len(p.calls) != 1 {
		t.Fatalf("provider call count = %d, want 1 (no recovery retry)", len(p.calls))
	}
}

func TestRunRecoversFromContextGuardError(t *testing.T) {
	// The estimated request exceeds the input budget before any provider call;
	// auto-compaction fails (provider rejects the summarization request), so the
	// run must recover through the overflow path instead of erroring out.
	p := newOverflowThenRecoverProvider(map[int]string{
		1: "API error 400: maximum context length exceeded",
	})
	p.models[0].ContextWindow = 4096
	p.models[0].MaxTokens = 512
	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)
	a := New(Config{
		Provider: p,
		Model:    p.models[0],
		Mode:     "agent",
		CompactionSettings: ctxpkg.CompactionSettings{
			Enabled:          true,
			ReserveTokens:    256,
			KeepRecentTokens: 1,
		},
	}, registry)
	a.LoadHistoryMessages([]provider.Message{
		provider.NewUserMessage(strings.Repeat("old user ", 3000)),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: strings.Repeat("old assistant ", 3000)}}),
		provider.NewUserMessage("recent user"),
	})

	var sawError bool
	for ev := range a.Run(context.Background(), "continue") {
		if ev.Type == EventError {
			sawError = true
		}
	}

	if sawError {
		t.Fatal("run should recover from context guard error, not fail permanently")
	}
	if len(p.calls) < 2 {
		t.Fatalf("provider call count = %d, want at least 2 (failed compaction + retry)", len(p.calls))
	}
	finalMessages := p.calls[len(p.calls)-1].Messages
	for _, msg := range finalMessages {
		if strings.Contains(msg.Content, "old user ") && strings.Repeat("old user ", 2) == msg.Content[:18] {
			t.Fatal("final request still included oversized old history")
		}
	}
}

package agent

import (
	"context"
	"encoding/json"
	"testing"
)

// MockProvider is a mock implementation of Provider for testing.
type MockProvider struct {
	nameVal   string
	modelsVal []ModelInfo
	chatChan  chan StreamEvent
}

func NewMockProvider(name string, models []ModelInfo) *MockProvider {
	return &MockProvider{
		nameVal:   name,
		modelsVal: models,
		chatChan:  make(chan StreamEvent, 10),
	}
}

func (m *MockProvider) Chat(ctx context.Context, params ChatParams) <-chan StreamEvent {
	go func() {
		defer close(m.chatChan)
		m.chatChan <- StreamEvent{Type: StreamDone, StopReason: "stop"}
	}()
	return m.chatChan
}

func (m *MockProvider) Name() string {
	return m.nameVal
}

func (m *MockProvider) Models() []ModelInfo {
	return m.modelsVal
}

func (m *MockProvider) GetModel(id string) *ModelInfo {
	for i := range m.modelsVal {
		if m.modelsVal[i].ID == id {
			return &m.modelsVal[i]
		}
	}
	return nil
}

type testExternalTool struct {
	name string
}

func (t testExternalTool) Name() string { return t.name }

func (t testExternalTool) Description() string { return "test tool" }

func (t testExternalTool) Parameters() []byte {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t testExternalTool) Execute(ctx context.Context, params map[string]any) (ExternalToolResult, error) {
	return ExternalToolResult{Text: "ok"}, nil
}

// ============ types.go tests ============

func TestNewUserMessage(t *testing.T) {
	msg := NewUserMessage("hello")
	if msg.Role != RoleUser {
		t.Errorf("expected role user, got %v", msg.Role)
	}
	if msg.Content != "hello" {
		t.Errorf("expected content 'hello', got %q", msg.Content)
	}
}

func TestNewAssistantTextMessage(t *testing.T) {
	msg := NewAssistantTextMessage("response")
	if msg.Role != RoleAssistant {
		t.Errorf("expected role assistant, got %v", msg.Role)
	}
	if msg.Content != "response" {
		t.Errorf("expected content 'response', got %q", msg.Content)
	}
}

func TestNewAssistantMessage(t *testing.T) {
	contents := []ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "thinking", Thinking: "let me think"},
	}
	msg := NewAssistantMessage(contents)
	if msg.Role != RoleAssistant {
		t.Errorf("expected role assistant, got %v", msg.Role)
	}
	if len(msg.Contents) != 2 {
		t.Errorf("expected 2 contents, got %d", len(msg.Contents))
	}
}

func TestNewToolResultMessage(t *testing.T) {
	msg := NewToolResultMessage("call-123", "bash", "output", false)
	if msg.Role != RoleToolResult {
		t.Errorf("expected role toolResult, got %v", msg.Role)
	}
	if msg.ToolCallID != "call-123" {
		t.Errorf("expected toolCallID 'call-123', got %q", msg.ToolCallID)
	}
	if msg.ToolName != "bash" {
		t.Errorf("expected toolName 'bash', got %q", msg.ToolName)
	}
	if msg.Content != "output" {
		t.Errorf("expected content 'output', got %q", msg.Content)
	}
	if msg.IsError {
		t.Error("expected IsError to be false")
	}
}

func TestNewToolResultMessageWithError(t *testing.T) {
	msg := NewToolResultMessage("call-456", "read", "error occurred", true)
	if !msg.IsError {
		t.Error("expected IsError to be true")
	}
}

func TestNewToolResultMessageWithContents(t *testing.T) {
	contents := []ContentBlock{
		{Type: "text", Text: "result"},
	}
	msg := NewToolResultMessageWithContents("call-789", "write", "done", contents, false)
	if msg.Role != RoleToolResult {
		t.Errorf("expected role toolResult, got %v", msg.Role)
	}
	if len(msg.Contents) != 1 {
		t.Errorf("expected 1 content, got %d", len(msg.Contents))
	}
}

func TestNewSystemInjectedUserMessage(t *testing.T) {
	msg := NewSystemInjectedUserMessage("system prompt")
	if msg.Role != RoleUser {
		t.Errorf("expected role user, got %v", msg.Role)
	}
	if !msg.SystemInjected {
		t.Error("expected SystemInjected to be true")
	}
}

func TestUsageCalculateCost(t *testing.T) {
	usage := &Usage{
		InputTokens:  1000,
		OutputTokens: 500,
		CacheRead:    100,
		CacheWrite:   50,
	}

	usage.CalculateCost(0.27, 1.10, 0.10, 0.27)

	if usage.Cost.Input <= 0 {
		t.Error("expected positive input cost")
	}
	if usage.Cost.Output <= 0 {
		t.Error("expected positive output cost")
	}
	if usage.Cost.Total <= 0 {
		t.Error("expected positive total cost")
	}

	expectedInput := 0.00027
	if diff(usage.Cost.Input, expectedInput) > 0.0001 {
		t.Errorf("expected input cost %.6f, got %.6f", expectedInput, usage.Cost.Input)
	}
}

func TestUsageCalculateCostDoesNotDoubleChargeIncludedCacheRead(t *testing.T) {
	usage := &Usage{
		InputTokens:  1000,
		OutputTokens: 100,
		CacheRead:    750,
		TotalTokens:  1100,
	}
	usage.CalculateCost(2, 10, 0.5, 0)

	if got, want := usage.Cost.Input, 0.0005; got != want {
		t.Errorf("input cost = %f, want %f for 250 uncached tokens", got, want)
	}
	if got, want := usage.Cost.CacheRead, 0.000375; got != want {
		t.Errorf("cache-read cost = %f, want %f", got, want)
	}
	if got, want := usage.Cost.Total, 0.001875; got != want {
		t.Errorf("total cost = %f, want %f", got, want)
	}
}

func TestUsageTotalInputTokens(t *testing.T) {
	tests := []struct {
		name      string
		usage     *Usage
		wantInput int
	}{
		{
			name:      "nil usage",
			usage:     nil,
			wantInput: 0,
		},
		{
			name: "uses total tokens when present",
			usage: &Usage{
				InputTokens:  400,
				OutputTokens: 50,
				CacheRead:    200,
				CacheWrite:   100,
				TotalTokens:  750,
			},
			wantInput: 700,
		},
		{
			// Providers that omit the grand total still report the parts; the
			// SDK's InputTokens is normalized to the non-cached prompt portion.
			name: "falls back to components when total missing",
			usage: &Usage{
				InputTokens:  400,
				OutputTokens: 50,
				CacheRead:    200,
				CacheWrite:   100,
			},
			wantInput: 700,
		},
		{
			name: "ignores a total smaller than the output",
			usage: &Usage{
				InputTokens:  30,
				OutputTokens: 50,
				TotalTokens:  40,
			},
			wantInput: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.usage.TotalInputTokens(); got != tt.wantInput {
				t.Errorf("TotalInputTokens() = %d, want %d", got, tt.wantInput)
			}
		})
	}
}

func TestRoleConstants(t *testing.T) {
	if RoleUser != "user" {
		t.Errorf("expected user, got %q", RoleUser)
	}
	if RoleAssistant != "assistant" {
		t.Errorf("expected assistant, got %q", RoleAssistant)
	}
	if RoleToolResult != "toolResult" {
		t.Errorf("expected toolResult, got %q", RoleToolResult)
	}
	if RoleSystem != "system" {
		t.Errorf("expected system, got %q", RoleSystem)
	}
}

func TestContextUsage(t *testing.T) {
	usage := &ContextUsage{
		Tokens:        50000,
		ContextWindow: 128000,
	}

	percent := float64(50000) / float64(128000) * 100
	usage.Percent = &percent

	if usage.Percent == nil {
		t.Error("expected Percent to be set")
	}
}

// ============ builder.go tests ============

func TestNewBuilder(t *testing.T) {
	b := NewBuilder()

	if b.mode != "yolo" {
		t.Errorf("expected mode 'yolo', got %q", b.mode)
	}
	if b.thinkingLevel != ThinkingMedium {
		t.Errorf("expected thinking level medium, got %v", b.thinkingLevel)
	}
	if b.maxTokens != 16384 {
		t.Errorf("expected maxTokens 16384, got %d", b.maxTokens)
	}
	if b.maxIterations != 200 {
		t.Errorf("expected maxIterations 200, got %d", b.maxIterations)
	}
	if b.toolExecutionMode != "parallel" {
		t.Errorf("expected toolExecutionMode 'parallel', got %q", b.toolExecutionMode)
	}
	if !b.compactionEnabled {
		t.Error("expected compactionEnabled to be true")
	}
	if b.compactionReserve != 16384 {
		t.Errorf("expected compactionReserve 16384, got %d", b.compactionReserve)
	}
}

func TestBuilderWithProvider(t *testing.T) {
	b := NewBuilder()
	provider := NewMockProvider("test", []ModelInfo{{ID: "gpt-4"}})

	result := b.WithProvider(provider)

	if result.provider != provider {
		t.Error("provider not set correctly")
	}
	if result != b {
		t.Error("WithProvider should return the same builder")
	}
}

func TestBuilderWithModel(t *testing.T) {
	b := NewBuilder()
	result := b.WithModel("gpt-4o")

	if b.modelID != "gpt-4o" {
		t.Errorf("expected modelID 'gpt-4o', got %q", b.modelID)
	}
	if result != b {
		t.Error("WithModel should return the same builder")
	}
}

func TestBuilderWithMode(t *testing.T) {
	b := NewBuilder()
	b.WithMode("plan")
	if b.mode != "plan" {
		t.Errorf("expected mode 'plan', got %q", b.mode)
	}

	b.WithMode("yolo")
	if b.mode != "yolo" {
		t.Errorf("expected mode 'yolo', got %q", b.mode)
	}
}

func TestBuilderWithWorkDir(t *testing.T) {
	b := NewBuilder()
	result := b.WithWorkDir("/tmp/project")

	if b.workDir != "/tmp/project" {
		t.Errorf("expected workDir '/tmp/project', got %q", b.workDir)
	}
	if result != b {
		t.Error("WithWorkDir should return the same builder")
	}
}

func TestBuilderWithThinkingLevel(t *testing.T) {
	b := NewBuilder()
	result := b.WithThinkingLevel(ThinkingHigh)

	if b.thinkingLevel != ThinkingHigh {
		t.Errorf("expected thinkingLevel high, got %v", b.thinkingLevel)
	}
	if result != b {
		t.Error("WithThinkingLevel should return the same builder")
	}
}

func TestBuilderThinkingLevelConstants(t *testing.T) {
	if ThinkingOff != "off" {
		t.Errorf("expected off, got %q", ThinkingOff)
	}
	if ThinkingMinimal != "minimal" {
		t.Errorf("expected minimal, got %q", ThinkingMinimal)
	}
	if ThinkingLow != "low" {
		t.Errorf("expected low, got %q", ThinkingLow)
	}
	if ThinkingMedium != "medium" {
		t.Errorf("expected medium, got %q", ThinkingMedium)
	}
	if ThinkingHigh != "high" {
		t.Errorf("expected high, got %q", ThinkingHigh)
	}
	if ThinkingXHigh != "xhigh" {
		t.Errorf("expected xhigh, got %q", ThinkingXHigh)
	}
}

func TestBuilderWithMaxTokens(t *testing.T) {
	b := NewBuilder()
	result := b.WithMaxTokens(8192)

	if b.maxTokens != 8192 {
		t.Errorf("expected maxTokens 8192, got %d", b.maxTokens)
	}
	if result != b {
		t.Error("WithMaxTokens should return the same builder")
	}
}

func TestBuilderWithSystemPromptExtra(t *testing.T) {
	b := NewBuilder()
	result := b.WithSystemPromptExtra("extra context")

	if b.systemPromptExtra != "extra context" {
		t.Errorf("expected systemPromptExtra, got %q", b.systemPromptExtra)
	}
	if result != b {
		t.Error("WithSystemPromptExtra should return the same builder")
	}
}

func TestBuilderWithMaxIterations(t *testing.T) {
	b := NewBuilder()
	result := b.WithMaxIterations(100)

	if b.maxIterations != 100 {
		t.Errorf("expected maxIterations 100, got %d", b.maxIterations)
	}
	if result != b {
		t.Error("WithMaxIterations should return the same builder")
	}
}

func TestBuilderWithToolExecutionMode(t *testing.T) {
	b := NewBuilder()
	b.WithToolExecutionMode("sequential")
	if b.toolExecutionMode != "sequential" {
		t.Errorf("expected sequential, got %q", b.toolExecutionMode)
	}

	b.WithToolExecutionMode("parallel")
	if b.toolExecutionMode != "parallel" {
		t.Errorf("expected parallel, got %q", b.toolExecutionMode)
	}
}

func TestBuilderWithMaxToolConcurrency(t *testing.T) {
	b := NewBuilder()
	if b.maxToolConcurrency != DefaultMaxToolConcurrency {
		t.Fatalf("default maxToolConcurrency = %d, want %d", b.maxToolConcurrency, DefaultMaxToolConcurrency)
	}
	if got := b.WithMaxToolConcurrency(4); got != b {
		t.Fatal("WithMaxToolConcurrency should return the same builder")
	}
	if b.maxToolConcurrency != 4 {
		t.Fatalf("maxToolConcurrency = %d, want 4", b.maxToolConcurrency)
	}
	if cfg := b.Config(); cfg.MaxToolConcurrency != 4 {
		t.Fatalf("BuilderConfig.MaxToolConcurrency = %d, want 4", cfg.MaxToolConcurrency)
	}
}

func TestBuilderWithTools(t *testing.T) {
	b := NewBuilder()
	result := b.WithTools([]string{"read", "write", "edit"})

	if len(b.tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(b.tools))
	}
	if b.tools[0] != "read" {
		t.Errorf("expected first tool 'read', got %q", b.tools[0])
	}
	if result != b {
		t.Error("WithTools should return the same builder")
	}
}

func TestBuilderWithSandbox(t *testing.T) {
	b := NewBuilder()

	b.WithSandbox(true)
	if !b.sandboxEnabled {
		t.Error("expected sandboxEnabled to be true")
	}

	b.WithSandbox(false)
	if b.sandboxEnabled {
		t.Error("expected sandboxEnabled to be false")
	}
}

func TestBuilderWithSessionDir(t *testing.T) {
	b := NewBuilder()
	result := b.WithSessionDir("/tmp/sessions")

	if b.sessionDir != "/tmp/sessions" {
		t.Errorf("expected sessionDir '/tmp/sessions', got %q", b.sessionDir)
	}
	if result != b {
		t.Error("WithSessionDir should return the same builder")
	}
}

func TestBuilderWithCompaction(t *testing.T) {
	b := NewBuilder()
	result := b.WithCompaction(false, 8192)

	if b.compactionEnabled {
		t.Error("expected compactionEnabled to be false")
	}
	if b.compactionReserve != 8192 {
		t.Errorf("expected compactionReserve 8192, got %d", b.compactionReserve)
	}
	if result != b {
		t.Error("WithCompaction should return the same builder")
	}
}

func TestBuilderWithMultiAgent(t *testing.T) {
	b := NewBuilder()

	b.WithMultiAgent(true)
	if !b.multiAgent {
		t.Error("expected multiAgent to be true")
	}

	b.WithMultiAgent(false)
	if b.multiAgent {
		t.Error("expected multiAgent to be false")
	}
}

func TestBuilderWithApprovalHandler(t *testing.T) {
	b := NewBuilder()
	handler := func(toolCallID, toolName string, args map[string]any) bool {
		return true
	}

	result := b.WithApprovalHandler(handler)

	if b.approvalHandler == nil {
		t.Error("expected approvalHandler to be set")
	}

	b.WithApprovalHandler(nil)
	if b.approvalHandler != nil {
		t.Error("expected approvalHandler to be nil")
	}

	_ = result
}

func TestBuilderConfig(t *testing.T) {
	provider := NewMockProvider("test", []ModelInfo{{ID: "gpt-4"}})
	b := NewBuilder().
		WithProvider(provider).
		WithModel("gpt-4").
		WithMode("yolo").
		WithWorkDir("/home/user/project").
		WithThinkingLevel(ThinkingHigh).
		WithMaxTokens(8192).
		WithSystemPromptExtra("extra").
		WithMaxIterations(100).
		WithToolExecutionMode("sequential").
		WithTools([]string{"read"}).
		WithSandbox(true).
		WithSessionDir("/tmp/sessions").
		WithCompaction(false, 8192).
		WithMultiAgent(true)

	cfg := b.Config()

	if cfg.Provider != provider {
		t.Error("Provider not matched")
	}
	if cfg.ModelID != "gpt-4" {
		t.Errorf("expected ModelID 'gpt-4', got %q", cfg.ModelID)
	}
	if cfg.Mode != "yolo" {
		t.Errorf("expected Mode 'yolo', got %q", cfg.Mode)
	}
	if cfg.WorkDir != "/home/user/project" {
		t.Errorf("expected WorkDir, got %q", cfg.WorkDir)
	}
	if cfg.ThinkingLevel != ThinkingHigh {
		t.Errorf("expected ThinkingLevel high, got %v", cfg.ThinkingLevel)
	}
	if cfg.MaxTokens != 8192 {
		t.Errorf("expected MaxTokens 8192, got %d", cfg.MaxTokens)
	}
	if cfg.SystemPromptExtra != "extra" {
		t.Errorf("expected SystemPromptExtra, got %q", cfg.SystemPromptExtra)
	}
	if cfg.MaxIterations != 100 {
		t.Errorf("expected MaxIterations 100, got %d", cfg.MaxIterations)
	}
	if cfg.ToolExecutionMode != "sequential" {
		t.Errorf("expected ToolExecutionMode, got %q", cfg.ToolExecutionMode)
	}
	if len(cfg.Tools) != 1 || cfg.Tools[0] != "read" {
		t.Error("Tools not matched")
	}
	if !cfg.SandboxEnabled {
		t.Error("expected SandboxEnabled true")
	}
	if cfg.SessionDir != "/tmp/sessions" {
		t.Errorf("expected SessionDir, got %q", cfg.SessionDir)
	}
	if cfg.CompactionEnabled {
		t.Error("expected CompactionEnabled false")
	}
	if cfg.CompactionReserve != 8192 {
		t.Errorf("expected CompactionReserve 8192, got %d", cfg.CompactionReserve)
	}
	if !cfg.MultiAgent {
		t.Error("expected MultiAgent true")
	}
}

func TestBuilderConfigIncludesExternalTools(t *testing.T) {
	b := NewBuilder().
		WithProvider(NewMockProvider("test", []ModelInfo{{ID: "gpt-4"}})).
		WithModel("gpt-4").
		WithExternalTools(testExternalTool{name: "custom_tool"}).
		WithoutBuiltinTools()

	cfg := b.Config()
	if len(cfg.ExternalTools) != 1 || cfg.ExternalTools[0].Name() != "custom_tool" {
		t.Fatalf("external tools = %#v, want one matching tool", cfg.ExternalTools)
	}
	if !cfg.DisableBuiltinTools {
		t.Fatal("expected DisableBuiltinTools true")
	}
}

func TestBuilderBuildRequiresProvider(t *testing.T) {
	b := NewBuilder()
	_, err := b.Build()

	if err == nil {
		t.Error("expected error when provider is nil")
	}
}

func TestBuilderBuildRequiresModel(t *testing.T) {
	provider := NewMockProvider("test", []ModelInfo{})
	b := NewBuilder().WithProvider(provider)

	_, err := b.Build()

	if err == nil {
		t.Error("expected error when no models available")
	}
}

// ============ provider.go tests ============

func TestBaseProviderName(t *testing.T) {
	provider := NewBaseProvider("openai", []ModelInfo{{ID: "gpt-4"}})
	if provider.Name() != "openai" {
		t.Errorf("expected 'openai', got %q", provider.Name())
	}
}

func TestBaseProviderModels(t *testing.T) {
	models := []ModelInfo{
		{ID: "gpt-4"},
		{ID: "gpt-3.5-turbo"},
	}
	provider := NewBaseProvider("openai", models)

	result := provider.Models()
	if len(result) != 2 {
		t.Errorf("expected 2 models, got %d", len(result))
	}
}

func TestBaseProviderGetModel(t *testing.T) {
	models := []ModelInfo{
		{ID: "gpt-4", Name: "GPT-4"},
		{ID: "gpt-3.5-turbo", Name: "GPT-3.5"},
	}
	provider := NewBaseProvider("openai", models)

	model := provider.GetModel("gpt-4")
	if model == nil {
		t.Error("expected to find gpt-4")
	}
	if model.Name != "GPT-4" {
		t.Errorf("expected name 'GPT-4', got %q", model.Name)
	}

	model = provider.GetModel("gpt-5")
	if model != nil {
		t.Error("expected nil for non-existing model")
	}
}

func TestBoolPtr(t *testing.T) {
	truePtr := BoolPtr(true)
	if truePtr == nil || !*truePtr {
		t.Error("expected true")
	}

	falsePtr := BoolPtr(false)
	if falsePtr == nil || *falsePtr {
		t.Error("expected false")
	}
}

func TestVendorFromBaseURL(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"api.deepseek.com", "deepseek"},
		{"https://api.deepseek.com/v1", "deepseek"},
		{"api.xiaomimimo.com", "xiaomi"},
		{"api.moonshot.cn", "kimi"},
		{"api.kimi.com", "kimi"},
		{"api.z.ai", "zai"},
		{"open.bigmodel.cn", "zai"},
		{"api.minimaxi.com", "minimax"},
		{"ark.cn-beijing.volces.com", "volcengine"},
		{"aip.baidubce.com", "qianfan"},
		{"dashscope.aliyuncs.com", "bailian"},
		{"ai.gitee.com", "gitee"},
		{"openrouter.ai", "openrouter"},
		{"api.together.xyz", "together"},
		{"api.groq.com", "groq"},
		{"api.fireworks.ai", "fireworks"},
		{"apihub.agnes-ai.com", "agnes"},
		{"api.agnes-ai.cn", "agnes"},
		{"unknown.api.com", ""},
		{"", ""},
	}

	for _, tt := range tests {
		result := VendorFromBaseURL(tt.url)
		if result != tt.expected {
			t.Errorf("for %q: expected %q, got %q", tt.url, tt.expected, result)
		}
	}
}

func TestThinkingLevelValues(t *testing.T) {
	if string(ThinkingOff) != "off" {
		t.Errorf("expected off, got %q", ThinkingOff)
	}
	if string(ThinkingMinimal) != "minimal" {
		t.Errorf("expected minimal, got %q", ThinkingMinimal)
	}
	if string(ThinkingLow) != "low" {
		t.Errorf("expected low, got %q", ThinkingLow)
	}
	if string(ThinkingMedium) != "medium" {
		t.Errorf("expected medium, got %q", ThinkingMedium)
	}
	if string(ThinkingHigh) != "high" {
		t.Errorf("expected high, got %q", ThinkingHigh)
	}
	if string(ThinkingXHigh) != "xhigh" {
		t.Errorf("expected xhigh, got %q", ThinkingXHigh)
	}
}

func TestStreamEventTypeValues(t *testing.T) {
	if StreamStart != 0 {
		t.Errorf("StreamStart should be 0, got %d", StreamStart)
	}
	if StreamTextDelta != 1 {
		t.Errorf("StreamTextDelta should be 1, got %d", StreamTextDelta)
	}
	if StreamThinkDelta != 2 {
		t.Errorf("StreamThinkDelta should be 2, got %d", StreamThinkDelta)
	}
	if StreamToolCall != 3 {
		t.Errorf("StreamToolCall should be 3, got %d", StreamToolCall)
	}
	if StreamUsage != 4 {
		t.Errorf("StreamUsage should be 4, got %d", StreamUsage)
	}
	if StreamDone != 5 {
		t.Errorf("StreamDone should be 5, got %d", StreamDone)
	}
	if StreamError != 6 {
		t.Errorf("StreamError should be 6, got %d", StreamError)
	}
	if StreamRetry != 8 {
		t.Errorf("StreamRetry should be 8, got %d", StreamRetry)
	}
}

func TestModelInfo(t *testing.T) {
	compat := &ModelCompat{
		ThinkingFormat: "deepseek",
	}

	model := ModelInfo{
		ID:            "deepseek-chat",
		Name:          "DeepSeek Chat",
		Provider:      "deepseek",
		Reasoning:     true,
		ContextWindow: 64000,
		MaxTokens:     8192,
		Compat:        compat,
	}

	if model.ID != "deepseek-chat" {
		t.Errorf("expected ID, got %q", model.ID)
	}
	if model.Compat == nil {
		t.Error("expected Compat to be set")
	}
	if model.Compat.ThinkingFormat != "deepseek" {
		t.Errorf("expected thinking format, got %q", model.Compat.ThinkingFormat)
	}
}

func TestModelCompatBoolPtrs(t *testing.T) {
	trueVal := true
	falseVal := false

	compat := &ModelCompat{
		SupportsDeveloperRole:   &trueVal,
		SupportsStore:           &falseVal,
		SupportsReasoningEffort: nil,
	}

	if compat.SupportsDeveloperRole == nil || !*compat.SupportsDeveloperRole {
		t.Error("expected SupportsDeveloperRole to be true")
	}
	if compat.SupportsStore == nil || *compat.SupportsStore {
		t.Error("expected SupportsStore to be false")
	}
}

func diff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

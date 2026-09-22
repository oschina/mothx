package context

import (
	"context"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

type compactRecordingProvider struct {
	models   []*provider.Model
	lastChat provider.ChatParams
}

type fixedTokenEstimator struct {
	tokensByContent map[string]int
	defaultTokens   int
}

func (e fixedTokenEstimator) EstimateTokens(msg provider.Message) int {
	if v, ok := e.tokensByContent[msg.Content]; ok {
		return v
	}
	return e.defaultTokens
}

func (e fixedTokenEstimator) EstimateMessagesTokens(messages []provider.Message) int {
	total := 0
	for _, msg := range messages {
		total += e.EstimateTokens(msg)
	}
	return total
}

func (p *compactRecordingProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.lastChat = params
	ch := make(chan provider.StreamEvent, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "updated summary"}
		ch <- provider.StreamEvent{Type: provider.StreamDone}
	}()
	return ch
}

func (p *compactRecordingProvider) Name() string {
	return "compact-recording"
}

func (p *compactRecordingProvider) API() string {
	return "openai-chat"
}

func (p *compactRecordingProvider) Models() []*provider.Model {
	return p.models
}

func (p *compactRecordingProvider) GetModel(id string) *provider.Model {
	for _, model := range p.models {
		if model.ID == id {
			return model
		}
	}
	return nil
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		message  provider.Message
		expected int
	}{
		{
			name: "simple text message",
			message: provider.Message{
				Role:    "user",
				Content: "Hello, world!",
			},
			expected: 4, // 13 chars / 4 = 3.25, ceil = 4
		},
		{
			name: "empty message",
			message: provider.Message{
				Role:    "user",
				Content: "",
			},
			expected: 0,
		},
		{
			name: "assistant with text content block",
			message: provider.Message{
				Role: "assistant",
				Contents: []provider.ContentBlock{
					{Type: "text", Text: "This is a test message with some content"},
				},
			},
			expected: 8, // DeepSeek V3 tokenizer count
		},
		{
			name: "assistant with tool call",
			message: provider.Message{
				Role: "assistant",
				Contents: []provider.ContentBlock{
					{
						Type: "toolCall",
						ToolCall: &provider.ToolCallBlock{
							Name:      "bash",
							Arguments: []byte(`{"command":"ls -la"}`),
						},
					},
				},
			},
			expected: 8, // DeepSeek V3 tokenizer count
		},
		{
			name: "tool result message",
			message: provider.Message{
				Role:    "toolResult",
				Content: "file1.txt\nfile2.txt\nfile3.txt",
			},
			expected: 11, // DeepSeek V3 tokenizer count
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EstimateTokens(tt.message)
			if result != tt.expected {
				t.Errorf("EstimateTokens() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestCalculateContextTokens(t *testing.T) {
	tests := []struct {
		name     string
		usage    *provider.Usage
		expected int
	}{
		{
			name:     "nil usage",
			usage:    nil,
			expected: 0,
		},
		{
			name: "with totalTokens",
			usage: &provider.Usage{
				Input:       100,
				Output:      50,
				CacheRead:   20,
				CacheWrite:  10,
				TotalTokens: 180,
			},
			expected: 130,
		},
		{
			name: "with cache aware totalTokens",
			usage: &provider.Usage{
				Input:       100,
				Output:      50,
				CacheRead:   20,
				CacheWrite:  10,
				TotalTokens: 180,
			},
			expected: 130,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateContextTokens(tt.usage)
			if result != tt.expected {
				t.Errorf("CalculateContextTokens() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestContextUsageFromMessagesNormalizesCacheBreakdown(t *testing.T) {
	messages := []provider.Message{
		provider.NewUserMessage("current request"),
		{
			Role:    "assistant",
			Content: "response",
			Usage: &provider.Usage{
				Input:       100,
				Output:      50,
				CacheRead:   20,
				CacheWrite:  10,
				TotalTokens: 180,
			},
		},
	}

	usage := ContextUsageFromMessages(messages, GenericTokenEstimator{})
	if usage.TotalTokens != 130 || usage.Input != 100 || usage.CacheRead != 20 || usage.CacheWrite != 10 {
		t.Fatalf("usage = %#v, want total=130 input=100 cacheRead=20 cacheWrite=10", usage)
	}
	if usage.Tokens != usage.TotalTokens {
		t.Fatalf("Tokens alias = %d, want %d", usage.Tokens, usage.TotalTokens)
	}
}

func TestEstimateTextTokensUsesDeepSeekAddedTokens(t *testing.T) {
	if got := EstimateTextTokens("<｜User｜>"); got != 1 {
		t.Fatalf("EstimateTextTokens(added token) = %d, want 1", got)
	}
}

func TestEstimateContextTokens(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there", Usage: &provider.Usage{Input: 100, Output: 50, TotalTokens: 150}},
		{Role: "user", Content: "How are you?"},
	}

	tokens, lastUsageIndex := EstimateContextTokens(messages)
	if lastUsageIndex != 1 {
		t.Errorf("lastUsageIndex = %d, want 1", lastUsageIndex)
	}
	// 150 (from usage) + estimate of "How are you?" (12 chars / 4 = 3)
	if tokens != 104 {
		t.Errorf("tokens = %d, want 104", tokens)
	}
}

func TestEstimateContextTokensWithEstimator(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "already counted"},
		{Role: "assistant", Content: "response", Usage: &provider.Usage{Input: 100, Output: 50, TotalTokens: 150}},
		{Role: "user", Content: "trailing"},
	}
	estimator := fixedTokenEstimator{tokensByContent: map[string]int{"trailing": 42}, defaultTokens: 1}

	tokens, lastUsageIndex := EstimateContextTokensWithEstimator(messages, estimator)
	if lastUsageIndex != 1 {
		t.Errorf("lastUsageIndex = %d, want 1", lastUsageIndex)
	}
	if tokens != 142 {
		t.Errorf("tokens = %d, want 142", tokens)
	}
}

func TestShouldCompact(t *testing.T) {
	tests := []struct {
		name          string
		contextTokens int
		contextWindow int
		reserveTokens int
		expected      bool
	}{
		{
			name:          "should compact - over threshold",
			contextTokens: 190000,
			contextWindow: 200000,
			reserveTokens: 16384,
			expected:      true,
		},
		{
			name:          "should not compact - under threshold",
			contextTokens: 100000,
			contextWindow: 200000,
			reserveTokens: 16384,
			expected:      false,
		},
		{
			name:          "no context window",
			contextTokens: 100000,
			contextWindow: 0,
			reserveTokens: 16384,
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldCompact(tt.contextTokens, tt.contextWindow, tt.reserveTokens)
			if result != tt.expected {
				t.Errorf("ShouldCompact() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestShouldCompactPercent(t *testing.T) {
	if !ShouldCompactPercent(160, 200, 0.8) {
		t.Fatal("80% usage should compact at 0.8 threshold")
	}
	if ShouldCompactPercent(159, 200, 0.8) {
		t.Fatal("usage below threshold should not compact")
	}
	if !ShouldCompactPercent(80, 100, 80) {
		t.Fatal("whole-number percentage threshold should be supported")
	}
	if ShouldCompactPercent(80, 0, 0.8) {
		t.Fatal("zero context window should not compact")
	}
}

func TestFindCutPoint(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "Message 1"},
		{Role: "assistant", Content: "Response 1"},
		{Role: "user", Content: "Message 2"},
		{Role: "assistant", Content: "Response 2"},
		{Role: "user", Content: "Message 3"},
		{Role: "assistant", Content: "Response 3"},
	}

	// Try to keep only ~10 tokens (should cut near the end)
	cutPoint := FindCutPoint(messages, 0, len(messages), 10)
	if cutPoint.FirstKeptIndex < 0 || cutPoint.FirstKeptIndex >= len(messages) {
		t.Errorf("FirstKeptIndex out of range: %d", cutPoint.FirstKeptIndex)
	}
}

func TestFindCutPointWithEstimator(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "old"},
		{Role: "assistant", Content: "old response"},
		{Role: "user", Content: "recent"},
		{Role: "assistant", Content: "recent response"},
	}
	estimator := fixedTokenEstimator{
		tokensByContent: map[string]int{
			"old":             1,
			"old response":    1,
			"recent":          40,
			"recent response": 40,
		},
		defaultTokens: 1,
	}

	cutPoint := FindCutPointWithEstimator(messages, 0, len(messages), 40, estimator)
	if cutPoint.FirstKeptIndex != 3 {
		t.Errorf("FirstKeptIndex = %d, want 3", cutPoint.FirstKeptIndex)
	}
	if !cutPoint.IsSplitTurn || cutPoint.TurnStartIndex != 2 {
		t.Errorf("cutPoint split turn = (%v, %d), want (true, 2)", cutPoint.IsSplitTurn, cutPoint.TurnStartIndex)
	}
}

func TestResolveCompressionTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		wantName string
		wantText string
	}{
		{name: "default empty", template: "", wantName: "default", wantText: "structured context checkpoint"},
		{name: "code", template: "code", wantName: "code", wantText: "structured coding checkpoint"},
		{name: "conversation", template: "conversation", wantName: "conversation", wantText: "conversation checkpoint"},
		{name: "unknown fallback", template: "missing", wantName: "default", wantText: "structured context checkpoint"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := ResolveCompressionTemplate(tt.template)
			if template.Name != tt.wantName {
				t.Fatalf("Name = %q, want %q", template.Name, tt.wantName)
			}
			if !strings.Contains(template.Instruction, tt.wantText) {
				t.Fatalf("Instruction = %q, want text %q", template.Instruction, tt.wantText)
			}
		})
	}
}

func TestCompactUsesConfiguredTemplate(t *testing.T) {
	p := &compactRecordingProvider{
		models: []*provider.Model{{ID: "m", Name: "m", MaxTokens: 1024}},
	}
	messages := []provider.Message{
		{Role: "user", Content: strings.Repeat("old ", 80)},
		{Role: "assistant", Content: strings.Repeat("old response ", 80)},
		{Role: "user", Content: "recent"},
	}

	_, err := Compact(
		context.Background(),
		messages,
		p,
		p.models[0],
		"system",
		nil,
		CompactionSettings{ReserveTokens: 1024, KeepRecentTokens: 1, Template: "code"},
		"",
	)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	got := p.lastChat.Messages[len(p.lastChat.Messages)-1].Content
	if !strings.Contains(got, "structured coding checkpoint") {
		t.Fatalf("compression instruction = %q, want code template", got)
	}
}

func TestCompactCapsSummaryMaxTokens(t *testing.T) {
	p := &compactRecordingProvider{
		models: []*provider.Model{{ID: "m", Name: "m", MaxTokens: 16000}},
	}
	messages := []provider.Message{
		{Role: "user", Content: strings.Repeat("old ", 80)},
		{Role: "assistant", Content: strings.Repeat("old response ", 80)},
		{Role: "user", Content: "recent"},
	}

	_, err := Compact(
		context.Background(),
		messages,
		p,
		p.models[0],
		"system",
		nil,
		CompactionSettings{ReserveTokens: 16384, KeepRecentTokens: 1},
		"",
	)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if p.lastChat.MaxTokens != defaultMaxCompactionSummaryTokens {
		t.Fatalf("summary MaxTokens = %d, want %d", p.lastChat.MaxTokens, defaultMaxCompactionSummaryTokens)
	}
}

func TestCompactWithOptionsForceAllowsSummaryOnly(t *testing.T) {
	p := &compactRecordingProvider{
		models: []*provider.Model{{ID: "m", Name: "m", MaxTokens: 1024}},
	}
	messages := []provider.Message{
		{Role: "user", Content: "only current user"},
		{Role: "assistant", Content: "only current response"},
	}

	result, err := CompactWithOptions(
		context.Background(),
		messages,
		p,
		p.models[0],
		"system",
		nil,
		CompactionSettings{ReserveTokens: 1024, KeepRecentTokens: 20000},
		"",
		CompactOptions{Force: true},
	)
	if err != nil {
		t.Fatalf("CompactWithOptions(force) error = %v", err)
	}
	if result.FirstKeptIndex != len(messages) {
		t.Fatalf("FirstKeptIndex = %d, want %d", result.FirstKeptIndex, len(messages))
	}
}

func TestGenerateSummaryUsesConfiguredUpdateTemplate(t *testing.T) {
	p := &compactRecordingProvider{
		models: []*provider.Model{{ID: "m", Name: "m", MaxTokens: 1024}},
	}

	_, err := GenerateSummaryInsertThenCompressWithTemplate(
		context.Background(),
		[]provider.Message{{Role: "user", Content: "new information"}},
		p,
		p.models[0],
		"system",
		nil,
		"## Goal\nprevious",
		512,
		ResolveCompressionTemplate("code"),
	)
	if err != nil {
		t.Fatalf("GenerateSummaryInsertThenCompressWithTemplate() error = %v", err)
	}
	got := p.lastChat.Messages[len(p.lastChat.Messages)-1].Content
	if !strings.Contains(got, "existing coding checkpoint summary") {
		t.Fatalf("update instruction = %q, want code update template", got)
	}
	if !strings.Contains(got, "## Goal\nprevious") {
		t.Fatalf("update instruction missing previous summary: %q", got)
	}
}

func TestEstimateTokensImage(t *testing.T) {
	msg := provider.Message{
		Role: "user",
		Contents: []provider.ContentBlock{
			{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "base64data"}},
		},
	}
	result := EstimateTokens(msg)
	if result != 1200 { // 4800 chars / 4 = 1200
		t.Errorf("EstimateTokens(image) = %d, want 1200", result)
	}
}

func TestEstimateTokensLargeImageUsesPayloadSize(t *testing.T) {
	msg := provider.Message{
		Role: "user",
		Contents: []provider.ContentBlock{
			{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: strings.Repeat("a", 20000)}},
		},
	}
	result := EstimateTokens(msg)
	if result != 5000 {
		t.Errorf("EstimateTokens(large image) = %d, want 5000", result)
	}
}

func TestEstimateTokensImageUsesDimensions(t *testing.T) {
	msg := provider.Message{
		Role: "user",
		Contents: []provider.ContentBlock{
			{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "abc", Width: 1024, Height: 513}},
		},
	}
	result := EstimateTokens(msg)
	if result != 3200 {
		t.Errorf("EstimateTokens(sized image) = %d, want 3200", result)
	}
}

func TestResolveTokenEstimatorUsesModelAwareImageRules(t *testing.T) {
	tests := []struct {
		name  string
		model *provider.Model
		image *provider.ImageContent
		want  int
	}{
		{
			name:  "claude patch estimate",
			model: &provider.Model{ID: "claude-sonnet-4-5", Provider: "anthropic"},
			image: &provider.ImageContent{MimeType: "image/png", Data: "abc", Width: 1568, Height: 1019},
			want:  2072,
		},
		{
			name:  "gemini small tile estimate",
			model: &provider.Model{ID: "gemini-2.5-pro", Provider: "google-gemini"},
			image: &provider.ImageContent{MimeType: "image/png", Data: "abc", Width: 384, Height: 300},
			want:  258,
		},
		{
			name:  "qwen patch estimate",
			model: &provider.Model{ID: "qwen3.7-plus", Provider: "alibaba-coding-plan"},
			image: &provider.ImageContent{MimeType: "image/png", Data: "abc", Width: 1024, Height: 768},
			want:  1036,
		},
		{
			name:  "openai low detail estimate",
			model: &provider.Model{ID: "gpt-4o", Provider: "openai"},
			image: &provider.ImageContent{MimeType: "image/png", Data: "abc", Width: 1024, Height: 1024, Detail: "fast"},
			want:  85,
		},
		{
			name:  "openai high detail estimate",
			model: &provider.Model{ID: "gpt-4o", Provider: "openai"},
			image: &provider.ImageContent{MimeType: "image/png", Data: "abc", Width: 1024, Height: 513, Detail: "detail"},
			want:  765,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimator := ResolveTokenEstimator(CompactionSettings{Tokenizer: "auto"}, tt.model)
			msg := provider.Message{
				Role: "user",
				Contents: []provider.ContentBlock{
					{Type: "image", Image: tt.image},
				},
			}
			if got := estimator.EstimateTokens(msg); got != tt.want {
				t.Fatalf("EstimateTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestModelAwareTokenEstimatorSumsMultipleImages(t *testing.T) {
	estimator := ResolveTokenEstimator(CompactionSettings{Tokenizer: "auto"}, &provider.Model{ID: "gemini-2.5-pro", Provider: "google-gemini"})
	msg := provider.Message{
		Role: "user",
		Contents: []provider.ContentBlock{
			{Type: "text", Text: "compare"},
			{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "abc", Width: 384, Height: 300}},
			{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "abc", Width: 1024, Height: 768}},
		},
	}

	if got, want := estimator.EstimateTokens(msg), 775; got != want {
		t.Fatalf("EstimateTokens() = %d, want %d", got, want)
	}
}

func TestEstimateTokensThinking(t *testing.T) {
	msg := provider.Message{
		Role: "assistant",
		Contents: []provider.ContentBlock{
			{Type: "thinking", Thinking: "Let me think about this..."},
		},
	}
	result := EstimateTokens(msg)
	if result != 6 {
		t.Errorf("EstimateTokens(thinking) = %d, want 6", result)
	}
}

func TestCompactDoesNotResendPreviousSummaryAsConversationMessage(t *testing.T) {
	summary := "## Goal\ncarry forward state"
	messages := []provider.Message{
		provider.NewSystemInjectedUserMessage(summary),
		provider.NewUserMessage(strings.Repeat("old context ", 20)),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: strings.Repeat("assistant context ", 20)}}),
		provider.NewUserMessage(strings.Repeat("recent question ", 16)),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: strings.Repeat("recent answer ", 16)}}),
	}

	p := &compactRecordingProvider{
		models: []*provider.Model{{ID: "model1", Name: "Model 1", MaxTokens: 1024}},
	}

	_, err := Compact(
		context.Background(),
		messages,
		p,
		p.models[0],
		"",
		nil,
		CompactionSettings{ReserveTokens: 1024, KeepRecentTokens: 48},
		summary,
	)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}

	if len(p.lastChat.Messages) == 0 {
		t.Fatal("provider received no messages")
	}
	if p.lastChat.Messages[0].Content == summary {
		t.Fatalf("previous summary was resent as a conversation message: %+v", p.lastChat.Messages[0])
	}
	last := p.lastChat.Messages[len(p.lastChat.Messages)-1]
	if !strings.Contains(last.Content, "<existing-summary>") {
		t.Fatalf("expected update instruction with embedded existing summary, got %q", last.Content)
	}
}

func TestEstimateTokensContentBlocksTakePrecedence(t *testing.T) {
	// When Contents is non-empty, Content should be ignored
	msg := provider.Message{
		Role:    "assistant",
		Content: "This should be ignored because Contents is set",
		Contents: []provider.ContentBlock{
			{Type: "text", Text: "Short"},
		},
	}
	result := EstimateTokens(msg)
	if result != 1 {
		t.Errorf("EstimateTokens() = %d, want 1 (should use Contents, not Content)", result)
	}
}

func TestEstimateTokensToolCallNilBlock(t *testing.T) {
	msg := provider.Message{
		Role: "assistant",
		Contents: []provider.ContentBlock{
			{Type: "toolCall", ToolCall: nil},
		},
	}
	result := EstimateTokens(msg)
	if result != 0 { // 0 chars -> (0+3)/4 = 0
		t.Errorf("EstimateTokens(nil toolCall) = %d, want 0", result)
	}
}

func TestCalculateContextTokensFallback(t *testing.T) {
	// When TotalTokens is 0, should sum components
	usage := &provider.Usage{
		Input:       100,
		Output:      50,
		CacheRead:   20,
		CacheWrite:  10,
		TotalTokens: 0,
	}
	result := CalculateContextTokens(usage)
	if result != 130 {
		t.Errorf("CalculateContextTokens() = %d, want 130", result)
	}
}

func TestEstimateContextTokensNoUsage(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
	}

	tokens, lastUsageIndex := EstimateContextTokens(messages)
	if lastUsageIndex != -1 {
		t.Errorf("lastUsageIndex = %d, want -1", lastUsageIndex)
	}
	// Should estimate all messages
	expected := EstimateTokens(messages[0]) + EstimateTokens(messages[1])
	if tokens != expected {
		t.Errorf("tokens = %d, want %d", tokens, expected)
	}
}

func TestEstimateContextTokensEmptyMessages(t *testing.T) {
	tokens, lastUsageIndex := EstimateContextTokens(nil)
	if tokens != 0 {
		t.Errorf("tokens = %d, want 0", tokens)
	}
	if lastUsageIndex != -1 {
		t.Errorf("lastUsageIndex = %d, want -1", lastUsageIndex)
	}
}

func TestEstimateContextTokensUsageWithZeroTotal(t *testing.T) {
	// Usage present but TotalTokens=0 → should skip and estimate manually
	messages := []provider.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi", Usage: &provider.Usage{TotalTokens: 0}},
	}
	_, lastUsageIndex := EstimateContextTokens(messages)
	// Usage TotalTokens=0 means we skip it
	if lastUsageIndex != -1 {
		t.Errorf("lastUsageIndex = %d, want -1 (zero TotalTokens should be skipped)", lastUsageIndex)
	}
}

func TestFindValidCutPoints(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "msg1"},
		{Role: "assistant", Content: "resp1"},
		{Role: "toolResult", Content: "result1"},
		{Role: "user", Content: "msg2"},
		{Role: "assistant", Content: "resp2"},
	}

	cuts := FindValidCutPoints(messages, 0, len(messages))
	// Should include indices 0,1,3,4 but NOT 2 (toolResult)
	expected := []int{0, 1, 3, 4}
	if len(cuts) != len(expected) {
		t.Fatalf("FindValidCutPoints() = %v, want %v", cuts, expected)
	}
	for i, c := range cuts {
		if c != expected[i] {
			t.Errorf("cuts[%d] = %d, want %d", i, c, expected[i])
		}
	}
}

func TestFindValidCutPointsSubrange(t *testing.T) {
	messages := []provider.Message{
		{Role: "user"},
		{Role: "assistant"},
		{Role: "user"},
		{Role: "assistant"},
	}

	cuts := FindValidCutPoints(messages, 1, 3)
	expected := []int{1, 2}
	if len(cuts) != len(expected) {
		t.Fatalf("FindValidCutPoints(1,3) = %v, want %v", cuts, expected)
	}
}

func TestFindValidCutPointsEmpty(t *testing.T) {
	cuts := FindValidCutPoints(nil, 0, 0)
	if len(cuts) != 0 {
		t.Errorf("FindValidCutPoints(nil) = %v, want empty", cuts)
	}
}

func TestFindTurnStartIndex(t *testing.T) {
	messages := []provider.Message{
		{Role: "user"},
		{Role: "assistant"},
		{Role: "toolResult"},
		{Role: "assistant"},
	}

	// From index 3, should find user at index 0
	idx := FindTurnStartIndex(messages, 3, 0)
	if idx != 0 {
		t.Errorf("FindTurnStartIndex(3) = %d, want 0", idx)
	}

	// From index 1, should find user at index 0
	idx = FindTurnStartIndex(messages, 1, 0)
	if idx != 0 {
		t.Errorf("FindTurnStartIndex(1) = %d, want 0", idx)
	}

	// No user message found
	noUserMsgs := []provider.Message{
		{Role: "assistant"},
		{Role: "toolResult"},
	}
	idx = FindTurnStartIndex(noUserMsgs, 1, 0)
	if idx != -1 {
		t.Errorf("FindTurnStartIndex(no user) = %d, want -1", idx)
	}
}

func TestFindCutPointNoCutPoints(t *testing.T) {
	// All toolResult messages → no valid cut points
	messages := []provider.Message{
		{Role: "toolResult", Content: "result1"},
		{Role: "toolResult", Content: "result2"},
	}

	result := FindCutPoint(messages, 0, len(messages), 10)
	if result.FirstKeptIndex != 0 {
		t.Errorf("FirstKeptIndex = %d, want 0", result.FirstKeptIndex)
	}
	if result.TurnStartIndex != -1 {
		t.Errorf("TurnStartIndex = %d, want -1", result.TurnStartIndex)
	}
}

func TestFindCutPointSplitTurn(t *testing.T) {
	// Create messages where cut lands on an assistant message (not user)
	messages := []provider.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: strings.Repeat("x", 200)}, // large
		{Role: "user", Content: "third question"},
		{Role: "assistant", Content: strings.Repeat("y", 200)}, // large
	}

	// keepRecentTokens small enough to trigger cut in the middle
	result := FindCutPoint(messages, 0, len(messages), 20)
	if result.FirstKeptIndex < 0 || result.FirstKeptIndex >= len(messages) {
		t.Errorf("FirstKeptIndex = %d, out of range", result.FirstKeptIndex)
	}
}

func TestFindCutPointKeepAll(t *testing.T) {
	// keepRecentTokens very large → keep all messages
	messages := []provider.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
	}

	result := FindCutPoint(messages, 0, len(messages), 999999)
	if result.FirstKeptIndex != 0 {
		t.Errorf("FirstKeptIndex = %d, want 0 (should keep all)", result.FirstKeptIndex)
	}
}

func TestSerializeConversation(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
	}

	result := SerializeConversation(messages)
	if result == "" {
		t.Error("SerializeConversation() returned empty string")
	}
	if !contains(result, "User: Hello") {
		t.Error("SerializeConversation() missing user message")
	}
	if !contains(result, "Assistant: Hi there") {
		t.Error("SerializeConversation() missing assistant message")
	}
}

func TestSerializeConversationToolResult(t *testing.T) {
	messages := []provider.Message{
		{Role: "toolResult", ToolName: "bash", Content: "output here"},
	}

	result := SerializeConversation(messages)
	if !contains(result, "Tool Result [bash]") {
		t.Error("SerializeConversation() missing tool result")
	}
	if !contains(result, "output here") {
		t.Error("SerializeConversation() missing tool output")
	}
}

func TestSerializeConversationThinking(t *testing.T) {
	messages := []provider.Message{
		{Role: "assistant", Contents: []provider.ContentBlock{
			{Type: "thinking", Thinking: "hmm let me think"},
			{Type: "text", Text: "Here is my answer"},
		}},
	}

	result := SerializeConversation(messages)
	if !contains(result, "[thinking: hmm let me think]") {
		t.Error("SerializeConversation() missing thinking block")
	}
	if !contains(result, "Here is my answer") {
		t.Error("SerializeConversation() missing text content")
	}
}

func TestSerializeConversationToolCall(t *testing.T) {
	messages := []provider.Message{
		{Role: "assistant", Contents: []provider.ContentBlock{
			{Type: "toolCall", ToolCall: &provider.ToolCallBlock{Name: "read", Arguments: []byte(`{"path":"foo.go"}`)}},
		}},
	}

	result := SerializeConversation(messages)
	if !contains(result, "[tool_call: read(") {
		t.Errorf("SerializeConversation() missing tool call, got: %s", result)
	}
}

func TestSerializeConversationSystemInjectedSkipped(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "Hello", SystemInjected: true},
		{Role: "user", Content: "World"},
	}

	result := SerializeConversation(messages)
	if contains(result, "Hello") {
		t.Error("SerializeConversation() should skip system injected messages")
	}
	if !contains(result, "World") {
		t.Error("SerializeConversation() should include normal messages")
	}
}

func TestSerializeConversationUserContentBlocks(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Contents: []provider.ContentBlock{
			{Type: "text", Text: "block content"},
		}},
	}

	result := SerializeConversation(messages)
	if !contains(result, "User: block content") {
		t.Errorf("SerializeConversation() missing user content block, got: %s", result)
	}
}

func TestSerializeConversationUserNonTextContentBlocks(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Contents: []provider.ContentBlock{
			{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "abc"}},
		}},
	}

	result := SerializeConversation(messages)
	if !contains(result, "[image: image/png]") {
		t.Errorf("SerializeConversation() missing image block, got: %s", result)
	}
}

func TestSerializeConversationToolResultContentBlocks(t *testing.T) {
	messages := []provider.Message{
		{Role: "toolResult", ToolName: "read", Contents: []provider.ContentBlock{
			{Type: "text", Text: "tool block output"},
		}},
	}

	result := SerializeConversation(messages)
	if !contains(result, "tool block output") {
		t.Errorf("SerializeConversation() missing tool result content block, got: %s", result)
	}
}

func TestSerializeConversationLongToolResult(t *testing.T) {
	longContent := strings.Repeat("x", 600)
	messages := []provider.Message{
		{Role: "toolResult", ToolName: "bash", Content: longContent},
	}

	result := SerializeConversation(messages)
	// Should be truncated to 500 chars + "..."
	if !contains(result, "...") {
		t.Error("SerializeConversation() should truncate long tool results")
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"exact", 5, "exact"},
		{"toolong", 4, "tool..."},
		{"你好世界", 5, "你..."},
		{"", 10, ""},
	}

	for _, tt := range tests {
		result := truncateString(tt.input, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncateString(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
		}
	}
}

func TestDefaultCompactionSettings(t *testing.T) {
	s := DefaultCompactionSettings()
	if !s.Enabled {
		t.Error("expected Enabled=true")
	}
	if s.ReserveTokens != 16384 {
		t.Errorf("ReserveTokens = %d, want 16384", s.ReserveTokens)
	}
	if s.KeepRecentTokens != 20000 {
		t.Errorf("KeepRecentTokens = %d, want 20000", s.KeepRecentTokens)
	}
}

func TestShouldCompactExact(t *testing.T) {
	// Exactly at threshold
	if ShouldCompact(183616, 200000, 16384) {
		t.Error("exactly at threshold should NOT compact")
	}
	// One token over
	if !ShouldCompact(183617, 200000, 16384) {
		t.Error("one over threshold should compact")
	}
}

func TestAbsHelper(t *testing.T) {
	if abs(-5) != 5 {
		t.Errorf("abs(-5) = %d, want 5", abs(-5))
	}
	if abs(5) != 5 {
		t.Errorf("abs(5) = %d, want 5", abs(5))
	}
	if abs(0) != 0 {
		t.Errorf("abs(0) = %d, want 0", abs(0))
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

func TestCompressLargeToolResultsRunsParallelSubSummaries(t *testing.T) {
	model := &provider.Model{ID: "test-model"}
	p := provider.NewMockProvider("test", []*provider.Model{model}, []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "concise tool summary"},
	})
	messages := []provider.Message{
		{Role: "assistant", Contents: []provider.ContentBlock{{Type: "toolCall", ToolCall: &provider.ToolCallBlock{Name: "read"}}}},
		provider.NewToolResultMessage("call-1", "read", strings.Repeat("a", 50000), false),
		{Role: "assistant", Contents: []provider.ContentBlock{{Type: "toolCall", ToolCall: &provider.ToolCallBlock{Name: "grep"}}}},
		provider.NewToolResultMessage("call-2", "grep", strings.Repeat("b", 50000), false),
	}

	got, err := compressLargeToolResults(context.Background(), messages, p, model, "system", GenericTokenEstimator{})
	if err != nil {
		t.Fatalf("compressLargeToolResults() error = %v", err)
	}
	if p.GetCallCount() != 2 {
		t.Fatalf("sub-compression Chat calls = %d, want 2", p.GetCallCount())
	}
	for _, index := range []int{1, 3} {
		if got[index].Role != "toolResult" {
			t.Errorf("message[%d] role = %q, want toolResult", index, got[index].Role)
		}
		if got[index].Content != "concise tool summary" {
			t.Errorf("message[%d] content = %q, want sub-summary", index, got[index].Content)
		}
		if got[index].ToolCallID == "" || got[index].ToolName == "" {
			t.Errorf("message[%d] lost tool identity: %#v", index, got[index])
		}
	}
}

func TestCompressLargeToolResultsSkipsSmallResults(t *testing.T) {
	model := &provider.Model{ID: "test-model"}
	p := provider.NewMockProvider("test", []*provider.Model{model}, []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "summary"},
	})
	messages := []provider.Message{provider.NewToolResultMessage("call-1", "read", "small", false)}
	got, err := compressLargeToolResults(context.Background(), messages, p, model, "system", GenericTokenEstimator{})
	if err != nil {
		t.Fatalf("compressLargeToolResults() error = %v", err)
	}
	if p.GetCallCount() != 0 {
		t.Fatalf("sub-compression Chat calls = %d, want 0", p.GetCallCount())
	}
	if got[0].Content != "small" {
		t.Errorf("small tool result changed to %q", got[0].Content)
	}
}

func TestSummarizeToolResultUsesValidStandaloneUserMessage(t *testing.T) {
	p := &compactRecordingProvider{models: []*provider.Model{{ID: "test-model"}}}
	msg := provider.NewToolResultMessage("call-1", "read", "important file output", false)

	if _, err := summarizeToolResultOnce(context.Background(), msg, p, p.models[0], "system"); err != nil {
		t.Fatalf("summarizeToolResultOnce() error = %v", err)
	}
	if len(p.lastChat.Messages) != 1 {
		t.Fatalf("sub-summary message count = %d, want 1", len(p.lastChat.Messages))
	}
	request := p.lastChat.Messages[0]
	if request.Role != "user" {
		t.Fatalf("sub-summary message role = %q, want user", request.Role)
	}
	if strings.Contains(request.Content, "toolResult") {
		t.Fatalf("sub-summary request contains provider tool role: %q", request.Content)
	}
	if !strings.Contains(request.Content, "important file output") {
		t.Fatalf("sub-summary request omitted tool output: %q", request.Content)
	}
}

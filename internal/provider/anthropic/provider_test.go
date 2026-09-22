package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func chatAndCollect(t *testing.T, p *Provider, params provider.ChatParams) []provider.StreamEvent {
	t.Helper()
	var events []provider.StreamEvent
	for e := range p.Chat(context.Background(), params) {
		events = append(events, e)
	}
	return events
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type errorAfterBody struct {
	r   *strings.Reader
	err error
}

func (b *errorAfterBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err == io.EOF {
		return n, b.err
	}
	return n, err
}

func (b *errorAfterBody) Close() error { return nil }

func newMockAnthropicProvider(t *testing.T, models []*provider.Model, sse string, bodyCh chan<- string, check func(*http.Request)) *Provider {
	t.Helper()
	p := NewProviderWithModels("fake-key", "https://api.anthropic.com", models)
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if check != nil {
			check(r)
		}
		if bodyCh != nil {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			bodyCh <- string(body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(sse)),
			Request:    r,
		}, nil
	})}
	return p
}

func TestAnthropicRetriesEarlyStreamReadError(t *testing.T) {
	streamErr := errors.New("stream error: stream ID 19; INTERNAL_ERROR; received from peer")
	attempts := 0
	p := NewProviderWithModels("fake-key", "https://api.anthropic.com", []*provider.Model{{ID: "mock"}})
	p.SetRetryConfig(&provider.RetryConfig{Enabled: true, MaxRetries: 1, BaseDelayMs: 1})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		var body io.ReadCloser
		if attempts == 1 {
			body = &errorAfterBody{r: strings.NewReader(""), err: streamErr}
		} else {
			body = io.NopCloser(strings.NewReader("data: {\"type\":\"message_stop\"}\n"))
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: r}, nil
	})}

	events := chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})})
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	var retryEvent *provider.StreamEvent
	var sawDone bool
	for _, e := range events {
		switch e.Type {
		case provider.StreamRetry:
			retry := e
			retryEvent = &retry
		case provider.StreamDone:
			sawDone = true
		case provider.StreamError:
			t.Fatalf("unexpected StreamError: %v", e.Error)
		}
	}
	if retryEvent == nil || !sawDone {
		t.Fatalf("retryEvent=%#v sawDone=%v, want both true", retryEvent, sawDone)
	}
	if retryEvent.RetryAttempt != 1 || retryEvent.RetryMaxAttempts != 1 || retryEvent.RetryMax != 1 || retryEvent.RetryAfterMS != 1 {
		t.Fatalf("retry metadata = %#v, want attempt=1 max=1 delay=1ms", retryEvent)
	}
}

func TestAnthropicDoesNotRetryStreamReadErrorAfterVisibleOutput(t *testing.T) {
	streamErr := errors.New("stream error: stream ID 19; INTERNAL_ERROR; received from peer")
	attempts := 0
	p := NewProviderWithModels("fake-key", "https://api.anthropic.com", []*provider.Model{{ID: "mock"}})
	p.SetRetryConfig(&provider.RetryConfig{Enabled: true, MaxRetries: 1, BaseDelayMs: 1})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       &errorAfterBody{r: strings.NewReader("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n"), err: streamErr},
			Request:    r,
		}, nil
	})}

	events := chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})})
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	var sawText, sawError bool
	for _, e := range events {
		switch e.Type {
		case provider.StreamTextDelta:
			sawText = e.TextDelta == "hello"
		case provider.StreamRetry:
			t.Fatal("unexpected StreamRetry after visible output")
		case provider.StreamError:
			sawError = true
			if e.Error == nil || !strings.Contains(e.Error.Error(), "INTERNAL_ERROR") {
				t.Fatalf("error = %v, want INTERNAL_ERROR", e.Error)
			}
		}
	}
	if !sawText || !sawError {
		t.Fatalf("sawText=%v sawError=%v, want both true", sawText, sawError)
	}
}

func TestAnthropicProviderHTTPProxy(t *testing.T) {
	p, err := NewProviderWithModelsAndProxy("fake-key", "https://api.anthropic.com", "http://127.0.0.1:7890", []*provider.Model{{ID: "m1"}})
	if err != nil {
		t.Fatalf("provider with proxy: %v", err)
	}
	transport, ok := p.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", p.client.Transport)
	}
	proxyURL, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "api.anthropic.com"}})
	if err != nil {
		t.Fatalf("proxy lookup: %v", err)
	}
	if proxyURL == nil || proxyURL.String() != "http://127.0.0.1:7890" {
		t.Fatalf("proxy = %v, want http://127.0.0.1:7890", proxyURL)
	}
}

func TestAnthropicParallelToolUseRequest(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "mock"}},
		"data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	req := captureAnthropicRequestBody(t, p, provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("use the tools")},
		Tools:    []provider.ToolDefinition{{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Abort:    make(chan struct{}),
	}, bodyCh)
	if req.ToolChoice == nil || req.ToolChoice.Type != "auto" || req.ToolChoice.DisableParallelToolUse == nil || *req.ToolChoice.DisableParallelToolUse {
		t.Fatalf("tool_choice = %#v, want auto with parallel enabled", req.ToolChoice)
	}
	parallel := false
	bodyCh = make(chan string, 1)
	p = newMockAnthropicProvider(t, []*provider.Model{{ID: "mock"}},
		"data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	req = captureAnthropicRequestBody(t, p, provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("use the tool")},
		Tools:    []provider.ToolDefinition{{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ResponseOptions: &provider.ResponseOptions{
			ParallelTools: &parallel,
		},
		Abort: make(chan struct{}),
	}, bodyCh)
	if req.ToolChoice == nil || req.ToolChoice.DisableParallelToolUse == nil || !*req.ToolChoice.DisableParallelToolUse {
		t.Fatalf("tool_choice = %#v, want parallel disabled by response option", req.ToolChoice)
	}

	falseValue := false
	bodyCh = make(chan string, 1)
	p = newMockAnthropicProvider(t, []*provider.Model{{ID: "mock", Compat: &provider.ModelCompat{SupportsParallelToolCalls: &falseValue}}},
		"data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	req = captureAnthropicRequestBody(t, p, provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("use the tool")},
		Tools:    []provider.ToolDefinition{{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Abort:    make(chan struct{}),
	}, bodyCh)
	if req.ToolChoice != nil {
		t.Fatalf("tool_choice = %#v, want omitted for unsupported model", req.ToolChoice)
	}
	parallel = true
	bodyCh = make(chan string, 1)
	p = newMockAnthropicProvider(t, []*provider.Model{{ID: "mock", Compat: &provider.ModelCompat{SupportsParallelToolCalls: &falseValue}}},
		"data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	req = captureAnthropicRequestBody(t, p, provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("use the tool")},
		Tools:    []provider.ToolDefinition{{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ResponseOptions: &provider.ResponseOptions{
			ParallelTools: &parallel,
		},
		Abort: make(chan struct{}),
	}, bodyCh)
	if req.ToolChoice != nil {
		t.Fatalf("tool_choice = %#v, want omitted even with parallel response option", req.ToolChoice)
	}
	trueValue := true
	bodyCh = make(chan string, 1)
	p = newMockAnthropicProvider(t, []*provider.Model{{ID: "mock", Compat: &provider.ModelCompat{
		SupportsParallelToolCalls: &trueValue,
		SupportsToolChoice:        &falseValue,
	}}}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	req = captureAnthropicRequestBody(t, p, provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("use the tool")},
		Tools:    []provider.ToolDefinition{{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Abort:    make(chan struct{}),
	}, bodyCh)
	if req.ToolChoice != nil {
		t.Fatalf("tool_choice = %#v, want omitted when tool_choice is unsupported", req.ToolChoice)
	}
}

func TestMergeToolCallInputPreservesInitialOnMalformedStream(t *testing.T) {
	got := mergeToolCallInput([]byte(`{"path":"a"}`), []byte(`{"path":"b"`))
	if string(got) != `{"path":"a"}` {
		t.Fatalf("merged input = %s, want preserved initial object", got)
	}
}

func TestAnthropicStreamMultipleToolCallsWithInitialInput(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-1","name":"read","input":{"path":"a"}}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-2","name":"read","input":{"path":"b"}}}`,
		`data: {"type":"content_block_stop","index":1}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
	}, "\n") + "\n"
	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	calls := make([]provider.ToolCallBlock, 0, 2)
	for _, event := range chatAndCollect(t, p, provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("read both")},
		Abort:    make(chan struct{}),
	}) {
		if event.Type == provider.StreamToolCall && event.ToolCall != nil {
			calls = append(calls, *event.ToolCall)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("tool call count = %d, want 2", len(calls))
	}
	if calls[0].ID != "call-1" || calls[1].ID != "call-2" {
		t.Fatalf("call IDs = %q, %q", calls[0].ID, calls[1].ID)
	}
	if string(calls[0].Arguments) != `{"path":"a"}` || string(calls[1].Arguments) != `{"path":"b"}` {
		t.Fatalf("call args = %s, %s", calls[0].Arguments, calls[1].Arguments)
	}
}

func TestAnthropicCustomHeaders(t *testing.T) {
	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "claude-test"}}, "data: {\"type\":\"message_stop\"}\n", nil, func(r *http.Request) {
		if r.Header.Get("X-Custom-Header") != "custom-value" {
			t.Fatalf("X-Custom-Header = %q, want custom-value", r.Header.Get("X-Custom-Header"))
		}
		if r.Header.Get("anthropic-version") != "2024-01-01" {
			t.Fatalf("anthropic-version = %q, want 2024-01-01", r.Header.Get("anthropic-version"))
		}
	})
	p.SetHeaders(map[string]string{
		"X-Custom-Header":   "custom-value",
		"anthropic-version": "2024-01-01",
	})

	params := provider.ChatParams{
		ModelID:  "claude-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}
}

func mustUsage(t *testing.T, events []provider.StreamEvent) *provider.Usage {
	t.Helper()
	for _, e := range events {
		if e.Type == provider.StreamUsage && e.Usage != nil {
			return e.Usage
		}
	}
	t.Fatal("no StreamUsage event received")
	return nil
}

func boolPtr(v bool) *bool {
	return &v
}

// ─── standard Anthropic SSE scenarios ────────────────────────────────────────

func TestConvertMessagesPreservesCacheControlOnSingleTextBlock(t *testing.T) {
	p := NewProvider("fake-key", "https://api.anthropic.com")
	p.SetCacheControlEnabled(boolPtr(true))
	msgs := p.convertMessages(provider.ChatParams{
		Messages: []provider.Message{
			{
				Role: "user",
				Contents: []provider.ContentBlock{
					{
						Type:         "text",
						Text:         "cached text",
						CacheControl: &provider.CacheControl{Type: "ephemeral"},
					},
				},
			},
		},
	})

	if len(msgs) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(msgs))
	}
	blocks, ok := msgs[0].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("content type = %T, want []anthropicContentBlock", msgs[0].Content)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].CacheControl == nil || blocks[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("cache_control = %#v, want ephemeral", blocks[0].CacheControl)
	}
}

func TestConvertMessagesOmitsCacheControlWhenDisabled(t *testing.T) {
	p := NewProvider("fake-key", "https://api.anthropic.com")
	p.SetCacheControlEnabled(boolPtr(false))
	msgs := p.convertMessages(provider.ChatParams{
		Messages: []provider.Message{
			{
				Role: "user",
				Contents: []provider.ContentBlock{
					{
						Type:         "text",
						Text:         "cached text",
						CacheControl: &provider.CacheControl{Type: "ephemeral"},
					},
				},
			},
		},
	})

	if got, ok := msgs[0].Content.(string); !ok || got != "cached text" {
		t.Fatalf("content = %#v (%T), want simple text", msgs[0].Content, msgs[0].Content)
	}
}

func TestChatRequestPreservesCacheControlOnSingleTextBlock(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "claude-test"}}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	p.SetCacheControlEnabled(boolPtr(true))
	params := provider.ChatParams{
		ModelID: "claude-test",
		Messages: []provider.Message{
			{
				Role: "user",
				Contents: []provider.ContentBlock{
					{
						Type:         "text",
						Text:         "cached text",
						CacheControl: &provider.CacheControl{Type: "ephemeral"},
					},
				},
			},
		},
		Abort: make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req anthropicRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}

	if len(req.Messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(req.Messages))
	}
	rawContent, err := json.Marshal(req.Messages[0].Content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(rawContent, &blocks); err != nil {
		t.Fatalf("content is not a block array: %v\ncontent: %s", err, rawContent)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].CacheControl == nil || blocks[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("cache_control = %#v, want ephemeral", blocks[0].CacheControl)
	}
}

func TestChatRequestUsesExplicitMaxTokens(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "claude-test"}}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	params := provider.ChatParams{
		ModelID:   "claude-test",
		Messages:  []provider.Message{provider.NewUserMessage("hi")},
		MaxTokens: 4096,
		Abort:     make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req anthropicRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if req.MaxTokens == nil || *req.MaxTokens != 4096 {
		t.Fatalf("MaxTokens = %#v, want 4096", req.MaxTokens)
	}
}

func TestChatRequestExplicitZeroMaxTokensFallsBackToDefault(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "claude-test", MaxTokensSet: true}}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	params := provider.ChatParams{
		ModelID:  "claude-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var raw map[string]json.RawMessage
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	// The Anthropic API requires max_tokens, so an explicit-zero model config
	// must fall back to the default instead of omitting the field.
	v, ok := raw["max_tokens"]
	if !ok || string(v) != "16384" {
		t.Fatalf("max_tokens = %s (present=%v), want 16384", v, ok)
	}
}

func TestChatRequestHostedWebSearchTool(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "claude-test"}}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	params := provider.ChatParams{
		ModelID: "claude-test",
		Messages: []provider.Message{
			provider.NewUserMessage("search the web"),
		},
		Tools: []provider.ToolDefinition{
			{Name: "web_search", Kind: "hosted", Provider: "anthropic", ProviderType: "messages"},
		},
		Abort: make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req anthropicRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if len(req.Tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Type != "web_search_20250305" {
		t.Fatalf("tool.type = %q, want web_search_20250305", req.Tools[0].Type)
	}
	if req.Tools[0].Name != "" {
		t.Fatalf("hosted tool should not include name: %#v", req.Tools[0])
	}
}

func TestConvertMessagesAnthropicToolResultEmptyContentFallback(t *testing.T) {
	p := NewProvider("fake-key", "https://api.anthropic.com")
	msgs := p.convertMessages(provider.ChatParams{
		Messages: []provider.Message{
			provider.NewToolResultMessage("toolu_1", "bash", "", false),
		},
	})

	if len(msgs) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Fatalf("role = %q, want user", msgs[0].Role)
	}
	blocks, ok := msgs[0].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("content type = %T, want []anthropicContentBlock", msgs[0].Content)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].Type != "tool_result" {
		t.Fatalf("block type = %q, want tool_result", blocks[0].Type)
	}
	if blocks[0].ToolUseID != "toolu_1" {
		t.Fatalf("tool_use_id = %q, want toolu_1", blocks[0].ToolUseID)
	}
	if blocks[0].Content != "Tool completed with no output." {
		t.Fatalf("content = %#v, want fallback text", blocks[0].Content)
	}
}

func TestConvertMessagesAnthropicGroupsConsecutiveToolResults(t *testing.T) {
	p := NewProvider("fake-key", "https://api.anthropic.com")
	msgs := p.convertMessages(provider.ChatParams{
		Messages: []provider.Message{
			provider.NewToolResultMessage("toolu_1", "read", "first", false),
			provider.NewToolResultMessageWithContents("toolu_2", "screenshot", "image result", []provider.ContentBlock{
				{Type: "text", Text: "second"},
				{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "abc123"}},
			}, false),
			provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "done"}}),
		},
	})

	if len(msgs) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Fatalf("role = %q, want user", msgs[0].Role)
	}
	blocks, ok := msgs[0].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("content type = %T, want []anthropicContentBlock", msgs[0].Content)
	}
	if len(blocks) != 3 {
		t.Fatalf("len(blocks) = %d, want 3", len(blocks))
	}
	if blocks[0].Type != "tool_result" || blocks[0].ToolUseID != "toolu_1" || blocks[0].Content != "first" {
		t.Fatalf("first block = %#v, want first tool_result", blocks[0])
	}
	if blocks[1].Type != "tool_result" || blocks[1].ToolUseID != "toolu_2" || blocks[1].Content != "second" {
		t.Fatalf("second block = %#v, want second tool_result", blocks[1])
	}
	if blocks[2].Type != "image" || blocks[2].Source == nil || blocks[2].Source.Data != "abc123" {
		t.Fatalf("third block = %#v, want image block after tool results", blocks[2])
	}
}

func TestAnthropicThinkingFormatDeepSeek(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{
		{ID: "deepseek-test", Reasoning: true},
	}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	p.SetThinkingFormat("deepseek")
	params := provider.ChatParams{
		ModelID:       "deepseek-test",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingXHigh,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req anthropicRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}

	if req.Thinking == nil || req.Thinking.Type != "enabled" || req.Thinking.BudgetTokens != nil {
		t.Fatalf("thinking = %#v, want enabled without budget_tokens", req.Thinking)
	}
	if req.OutputConfig == nil || req.OutputConfig.Effort != "max" {
		t.Fatalf("output_config = %#v, want effort max", req.OutputConfig)
	}
}

func TestAnthropicThinkingFormatDeepSeekHigh(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{
		{ID: "deepseek-v4-pro", Reasoning: true},
	}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	p.SetThinkingFormat("deepseek")
	params := provider.ChatParams{
		ModelID:       "deepseek-v4-pro",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingHigh,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req anthropicRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}

	if req.Thinking == nil || req.Thinking.Type != "enabled" {
		t.Fatalf("thinking = %#v, want enabled", req.Thinking)
	}
	if req.OutputConfig == nil || req.OutputConfig.Effort != "high" {
		t.Fatalf("output_config = %#v, want effort high", req.OutputConfig)
	}
}

func TestAnthropicThinkingOmittedForNonReasoningModel(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{
		{ID: "claude-opus-test", Reasoning: false},
	}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	params := provider.ChatParams{
		ModelID:       "claude-opus-test",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingMedium,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req anthropicRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}

	if req.Thinking != nil {
		t.Fatalf("thinking = %#v, want nil for non-reasoning model", req.Thinking)
	}
	if req.OutputConfig != nil {
		t.Fatalf("output_config = %#v, want nil for non-reasoning model", req.OutputConfig)
	}
}

func TestAnthropicThinkingAdaptiveForOpus47(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{
		{ID: "claude-opus-4-7", Reasoning: true},
	}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	params := provider.ChatParams{
		ModelID:       "claude-opus-4-7",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingHigh,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req anthropicRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}

	if req.Thinking == nil || req.Thinking.Type != "adaptive" || req.Thinking.BudgetTokens != nil {
		t.Fatalf("thinking = %#v, want adaptive without budget_tokens", req.Thinking)
	}
	if req.OutputConfig == nil || req.OutputConfig.Effort != "high" {
		t.Fatalf("output_config = %#v, want effort high", req.OutputConfig)
	}
}

func TestAnthropicThinkingAdaptiveFromModelCompat(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{
		{ID: "custom-adaptive", Reasoning: true, Compat: &provider.ModelCompat{ForceAdaptiveThinking: true}},
	}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)
	params := provider.ChatParams{
		ModelID:       "custom-adaptive",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingMedium,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req anthropicRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if req.Thinking == nil || req.Thinking.Type != "adaptive" {
		t.Fatalf("thinking = %#v, want adaptive", req.Thinking)
	}
	if req.OutputConfig == nil || req.OutputConfig.Effort != "medium" {
		t.Fatalf("output_config = %#v, want effort medium", req.OutputConfig)
	}
}

// TestAnthropicCache_FirstTurn: cache is created for the first time.
// message_start carries cache_creation_input_tokens; no cache_read yet.
func TestAnthropicCache_FirstTurn(t *testing.T) {
	sse := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":1000,\"output_tokens\":0,\"cache_creation_input_tokens\":5000,\"cache_read_input_tokens\":0}}}\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":10}}\n" +
		"data: {\"type\":\"message_stop\"}\n"

	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	if u.Input != 1000 {
		t.Errorf("Input = %d, want 1000", u.Input)
	}
	if u.Output != 10 {
		t.Errorf("Output = %d, want 10", u.Output)
	}
	if u.CacheRead != 0 {
		t.Errorf("CacheRead = %d, want 0", u.CacheRead)
	}
	if u.CacheWrite != 5000 {
		t.Errorf("CacheWrite = %d, want 5000", u.CacheWrite)
	}
	if u.TotalTokens != 6010 {
		t.Errorf("TotalTokens = %d, want 6010", u.TotalTokens)
	}
	if got, want := u.CacheInfo(), "CacheWrite: 5000"; got != want {
		t.Errorf("CacheInfo() = %q, want %q", got, want)
	}
}

// TestAnthropicCache_CachedTurn: subsequent turn where the cache is hit.
// message_start carries cache_read_input_tokens; no cache_creation.
func TestAnthropicCache_CachedTurn(t *testing.T) {
	sse := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_2\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":1000,\"output_tokens\":0,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":750}}}\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"World\"}}\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":15}}\n" +
		"data: {\"type\":\"message_stop\"}\n"

	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	if u.Input != 1000 {
		t.Errorf("Input = %d, want 1000", u.Input)
	}
	if u.Output != 15 {
		t.Errorf("Output = %d, want 15", u.Output)
	}
	if u.CacheRead != 750 {
		t.Errorf("CacheRead = %d, want 750", u.CacheRead)
	}
	if u.CacheWrite != 0 {
		t.Errorf("CacheWrite = %d, want 0", u.CacheWrite)
	}
	if u.TotalTokens != 1765 {
		t.Errorf("TotalTokens = %d, want 1765", u.TotalTokens)
	}
	if got, want := u.CacheInfo(), "Cache: 43%"; got != want {
		t.Errorf("CacheInfo() = %q, want %q", got, want)
	}
}

// TestAnthropicCache_NoCache: turn with no cache activity at all.
func TestAnthropicCache_NoCache(t *testing.T) {
	sse := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_3\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":200,\"output_tokens\":0,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0}}}\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n" +
		"data: {\"type\":\"message_stop\"}\n"

	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	if u.Input != 200 {
		t.Errorf("Input = %d, want 200", u.Input)
	}
	if u.CacheRead != 0 {
		t.Errorf("CacheRead = %d, want 0", u.CacheRead)
	}
	if u.CacheWrite != 0 {
		t.Errorf("CacheWrite = %d, want 0", u.CacheWrite)
	}
	if u.TotalTokens != 205 {
		t.Errorf("TotalTokens = %d, want 205", u.TotalTokens)
	}
	if got, want := u.CacheInfo(), "Cache: 0%"; got != want {
		t.Errorf("CacheInfo() = %q, want %q", got, want)
	}
}

// ─── proxy-compatibility scenarios ───────────────────────────────────────────

// TestAnthropicCache_ProxyAllUsageInMessageDelta: some proxies send the full
// usage (including input and cache tokens) in message_delta instead of
// message_start. The parser must pick up those values.
func TestAnthropicCache_ProxyAllUsageInMessageDelta(t *testing.T) {
	sse := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_4\",\"content\":[],\"stop_reason\":null}}\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hey\"}}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":800,\"output_tokens\":20,\"cache_read_input_tokens\":600,\"cache_creation_input_tokens\":0}}\n" +
		"data: {\"type\":\"message_stop\"}\n"

	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	if u.Input != 800 {
		t.Errorf("Input = %d, want 800", u.Input)
	}
	if u.Output != 20 {
		t.Errorf("Output = %d, want 20", u.Output)
	}
	if u.CacheRead != 600 {
		t.Errorf("CacheRead = %d, want 600", u.CacheRead)
	}
	if u.TotalTokens != 1420 {
		t.Errorf("TotalTokens = %d, want 1420", u.TotalTokens)
	}
	if got, want := u.CacheInfo(), "Cache: 43%"; got != want {
		t.Errorf("CacheInfo() = %q, want %q", got, want)
	}
}

// TestAnthropicCache_ProxySplitUsage: message_start sets input+cache fields,
// message_delta adds output_tokens. Both contributions must merge correctly.
func TestAnthropicCache_ProxySplitUsage(t *testing.T) {
	sse := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_5\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":500,\"output_tokens\":0,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":500}}}\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":8}}\n" +
		"data: {\"type\":\"message_stop\"}\n"

	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	if u.Input != 500 {
		t.Errorf("Input = %d, want 500", u.Input)
	}
	if u.Output != 8 {
		t.Errorf("Output = %d, want 8", u.Output)
	}
	if u.CacheRead != 500 {
		t.Errorf("CacheRead = %d, want 500", u.CacheRead)
	}
	if u.TotalTokens != 1008 {
		t.Errorf("TotalTokens = %d, want 1008", u.TotalTokens)
	}
	// 500/(500+500) = 50%
	if got, want := u.CacheInfo(), "Cache: 50%"; got != want {
		t.Errorf("CacheInfo() = %q, want %q", got, want)
	}
}

// TestAnthropicCache_FirstWinsOnConflict: if a proxy sends usage in both
// message_start and message_delta with conflicting values, the message_start
// values (first seen) must be preserved.
func TestAnthropicCache_FirstWinsOnConflict(t *testing.T) {
	sse := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_6\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":1000,\"output_tokens\":0,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":750}}}\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Done\"}}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":999,\"output_tokens\":12,\"cache_read_input_tokens\":800}}\n" +
		"data: {\"type\":\"message_stop\"}\n"

	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	// message_start values win
	if u.Input != 1000 {
		t.Errorf("Input = %d, want 1000 (message_start wins)", u.Input)
	}
	if u.CacheRead != 750 {
		t.Errorf("CacheRead = %d, want 750 (message_start wins)", u.CacheRead)
	}
	// output_tokens was 0 in message_start, so message_delta fills it in
	if u.Output != 12 {
		t.Errorf("Output = %d, want 12 (message_delta fills zero)", u.Output)
	}
}

// ─── sampling params suppression ─────────────────────────────────────────────

func captureAnthropicRequestBody(t *testing.T, p *Provider, params provider.ChatParams, bodyCh <-chan string) anthropicRequest {
	t.Helper()
	chatAndCollect(t, p, params)
	var req anthropicRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	return req
}

func TestAnthropicThinkingDropsSamplingParams(t *testing.T) {
	temp := 0.7
	topP := 0.9
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "mock", Reasoning: true}},
		"data: {\"type\":\"message_stop\"}\n", bodyCh, nil)

	req := captureAnthropicRequestBody(t, p, provider.ChatParams{
		ModelID:       "mock",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingMedium,
		Temperature:   &temp,
		TopP:          &topP,
		Abort:         make(chan struct{}),
	}, bodyCh)

	if req.Thinking == nil {
		t.Fatal("thinking = nil, want enabled")
	}
	if req.Temperature != nil {
		t.Fatalf("temperature = %#v, want nil (dropped when thinking enabled)", *req.Temperature)
	}
	if req.TopP != nil {
		t.Fatalf("top_p = %#v, want nil (dropped when thinking enabled)", *req.TopP)
	}
}

func TestAnthropicDisableSamplingParamsCompat(t *testing.T) {
	temp := 0.7
	topP := 0.9
	disable := true
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{{
		ID:     "mock",
		Compat: &provider.ModelCompat{DisableSamplingParams: &disable},
	}}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)

	req := captureAnthropicRequestBody(t, p, provider.ChatParams{
		ModelID:     "mock",
		Messages:    []provider.Message{provider.NewUserMessage("hi")},
		Temperature: &temp,
		TopP:        &topP,
		Abort:       make(chan struct{}),
	}, bodyCh)

	if req.Thinking != nil {
		t.Fatalf("thinking = %#v, want nil", req.Thinking)
	}
	if req.Temperature != nil {
		t.Fatalf("temperature = %#v, want nil (DisableSamplingParams)", *req.Temperature)
	}
	if req.TopP != nil {
		t.Fatalf("top_p = %#v, want nil (DisableSamplingParams)", *req.TopP)
	}
}

func TestAnthropicSamplingParamsDroppedByDefault(t *testing.T) {
	temp := 0.7
	topP := 0.9
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{{ID: "mock"}},
		"data: {\"type\":\"message_stop\"}\n", bodyCh, nil)

	req := captureAnthropicRequestBody(t, p, provider.ChatParams{
		ModelID:     "mock",
		Messages:    []provider.Message{provider.NewUserMessage("hi")},
		Temperature: &temp,
		TopP:        &topP,
		Abort:       make(chan struct{}),
	}, bodyCh)

	if req.Temperature != nil {
		t.Fatalf("temperature = %#v, want nil (disabled by default)", *req.Temperature)
	}
	if req.TopP != nil {
		t.Fatalf("top_p = %#v, want nil (disabled by default)", *req.TopP)
	}
}

func TestAnthropicSamplingParamsPassThrough(t *testing.T) {
	temp := 0.7
	topP := 0.9
	allow := false
	bodyCh := make(chan string, 1)
	p := newMockAnthropicProvider(t, []*provider.Model{{
		ID:     "mock",
		Compat: &provider.ModelCompat{DisableSamplingParams: &allow},
	}}, "data: {\"type\":\"message_stop\"}\n", bodyCh, nil)

	req := captureAnthropicRequestBody(t, p, provider.ChatParams{
		ModelID:     "mock",
		Messages:    []provider.Message{provider.NewUserMessage("hi")},
		Temperature: &temp,
		TopP:        &topP,
		Abort:       make(chan struct{}),
	}, bodyCh)

	if req.Temperature == nil || *req.Temperature != temp {
		t.Fatalf("temperature = %#v, want %v", req.Temperature, temp)
	}
	if req.TopP == nil || *req.TopP != topP {
		t.Fatalf("top_p = %#v, want %v", req.TopP, topP)
	}
}

func TestConvertMessagesReplacesAudioVideoWithPlaceholder(t *testing.T) {
	p := &Provider{}
	msgs := p.convertMessages(provider.ChatParams{Messages: []provider.Message{{
		Role: "user",
		Contents: []provider.ContentBlock{
			{Type: "text", Text: "analyze"},
			{Type: "audio", Audio: &provider.AudioContent{MimeType: "audio/wav", Data: "YXVkaW8="}},
			{Type: "video", Video: &provider.VideoContent{MimeType: "video/mp4", Data: "dmlkZW8="}},
		},
	}}})
	if len(msgs) != 1 {
		t.Fatalf("messages = %#v", msgs)
	}
	blocks, ok := msgs[0].Content.([]anthropicContentBlock)
	if !ok || len(blocks) != 3 {
		t.Fatalf("content = %#v", msgs[0].Content)
	}
	if blocks[1].Type != "text" || !strings.Contains(blocks[1].Text, "[audio unavailable:") {
		t.Fatalf("audio placeholder = %#v", blocks[1])
	}
	if blocks[2].Type != "text" || !strings.Contains(blocks[2].Text, "[video unavailable:") {
		t.Fatalf("video placeholder = %#v", blocks[2])
	}
}

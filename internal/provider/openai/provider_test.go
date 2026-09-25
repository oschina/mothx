package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
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

type blockingAfterBody struct {
	reader *strings.Reader
	closed chan struct{}
}

func (b *blockingAfterBody) Read(p []byte) (int, error) {
	if b.reader.Len() > 0 {
		return b.reader.Read(p)
	}
	<-b.closed
	return 0, io.EOF
}

func (b *blockingAfterBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

func newMockOpenAIProvider(t *testing.T, models []*provider.Model, sse string, bodyCh chan<- string, check func(*http.Request)) *Provider {
	t.Helper()
	p := NewProviderWithModels("fake-key", "https://api.test/v1", models)
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

func TestOpenAIRetriesEarlyStreamReadError(t *testing.T) {
	streamErr := errors.New("read tcp 192.168.1.143:44252->180.76.199.86:443: read: connection reset by peer")
	attempts := 0
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "mock"}})
	p.SetRetryConfig(&provider.RetryConfig{Enabled: true, MaxRetries: 1, BaseDelayMs: 1})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		var body io.ReadCloser
		if attempts == 1 {
			body = &errorAfterBody{r: strings.NewReader(""), err: streamErr}
		} else {
			body = io.NopCloser(strings.NewReader("data: [DONE]\n"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
			Request:    r,
		}, nil
	})}

	events := chatAndCollect(t, p, provider.ChatParams{
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})
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
	if retryEvent == nil {
		t.Fatal("missing StreamRetry")
	}
	if !sawDone {
		t.Fatal("missing StreamDone")
	}
	if retryEvent.RetryAttempt != 1 || retryEvent.RetryMaxAttempts != 1 || retryEvent.RetryMax != 1 || retryEvent.RetryAfterMS != 1 {
		t.Fatalf("retry metadata = %#v, want attempt=1 max=1 delay=1ms", retryEvent)
	}
}

func TestOpenAIRetriesGeneric4xxResponse(t *testing.T) {
	attempts := 0
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "mock"}})
	p.SetRetryConfig(&provider.RetryConfig{Enabled: true, MaxRetries: 1, BaseDelayMs: 1})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temporary compatibility failure"}}`)),
				Request:    r,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\ndata: [DONE]\n")),
			Request:    r,
		}, nil
	})}

	events := chatAndCollect(t, p, provider.ChatParams{
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	var sawRetry, sawText, sawDone bool
	for _, event := range events {
		switch event.Type {
		case provider.StreamRetry:
			sawRetry = true
		case provider.StreamTextDelta:
			sawText = sawText || event.TextDelta == "recovered"
		case provider.StreamDone:
			sawDone = true
		case provider.StreamError:
			t.Fatalf("unexpected StreamError after 4xx retry: %v", event.Error)
		}
	}
	if !sawRetry || !sawText || !sawDone {
		t.Fatalf("events = %#v, want retry followed by recovered response", events)
	}
}

func TestOpenAIPreservesFinal4xxDiagnosticAfterRetries(t *testing.T) {
	attempts := 0
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "mock"}})
	p.SetRetryConfig(&provider.RetryConfig{Enabled: true, MaxRetries: 1, BaseDelayMs: 1})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"invalid tool sequence","request_id":"req_400"}}`)),
			Request:    r,
		}, nil
	})}

	events := chatAndCollect(t, p, provider.ChatParams{
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	for _, event := range events {
		if event.Type == provider.StreamError {
			if event.Error == nil || !strings.Contains(event.Error.Error(), "API error 400") || !strings.Contains(event.Error.Error(), "invalid tool sequence") {
				t.Fatalf("final error = %v, want HTTP status and provider body", event.Error)
			}
			return
		}
	}
	t.Fatalf("events = %#v, want final provider error", events)
}

func TestOpenAIDoesNotRetryStreamReadErrorAfterVisibleOutput(t *testing.T) {
	streamErr := errors.New("stream error: stream ID 19; INTERNAL_ERROR; received from peer")
	attempts := 0
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "mock"}})
	p.SetRetryConfig(&provider.RetryConfig{Enabled: true, MaxRetries: 1, BaseDelayMs: 1})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       &errorAfterBody{r: strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n"), err: streamErr},
			Request:    r,
		}, nil
	})}

	events := chatAndCollect(t, p, provider.ChatParams{
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	var sawText, sawError bool
	for _, e := range events {
		switch e.Type {
		case provider.StreamTextDelta:
			if e.TextDelta == "hello" {
				sawText = true
			}
		case provider.StreamRetry:
			t.Fatal("unexpected StreamRetry after visible output")
		case provider.StreamError:
			sawError = true
			if e.Error == nil || !strings.Contains(e.Error.Error(), "INTERNAL_ERROR") {
				t.Fatalf("error = %v, want INTERNAL_ERROR", e.Error)
			}
		}
	}
	if !sawText {
		t.Fatal("missing text delta")
	}
	if !sawError {
		t.Fatal("missing StreamError")
	}
}

func TestOpenAIProviderHTTPProxy(t *testing.T) {
	p, err := NewProviderWithModelsAndProxy("fake-key", "https://api.test/v1", "http://127.0.0.1:7890", []*provider.Model{{ID: "m1"}})
	if err != nil {
		t.Fatalf("provider with proxy: %v", err)
	}
	transport, ok := p.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", p.client.Transport)
	}
	proxyURL, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "api.test"}})
	if err != nil {
		t.Fatalf("proxy lookup: %v", err)
	}
	if proxyURL == nil || proxyURL.String() != "http://127.0.0.1:7890" {
		t.Fatalf("proxy = %v, want http://127.0.0.1:7890", proxyURL)
	}
}

func TestConvertMessagesToolResultUsesTextContents(t *testing.T) {
	p := &Provider{}
	messages := p.convertMessages(provider.ChatParams{
		Messages: []provider.Message{
			{
				Role:       "toolResult",
				ToolCallID: "call_1",
				ToolName:   "bash",
				Contents: []provider.ContentBlock{
					{Type: "text", Text: "bash output from content block", CacheControl: &provider.CacheControl{Type: "ephemeral"}},
				},
			},
		},
	}, false)

	if len(messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(messages))
	}
	if messages[0].Role != "tool" || messages[0].ToolCallID != "call_1" || messages[0].Name != "bash" || messages[0].Content != "bash output from content block" {
		t.Fatalf("tool message = %#v, want text content from content block", messages[0])
	}
}

func TestConvertMessagesToolResultIncludesKimiToolName(t *testing.T) {
	p := &Provider{}
	messages := p.convertMessages(provider.ChatParams{Messages: []provider.Message{{
		Role:     "assistant",
		Contents: []provider.ContentBlock{{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "read:2", Name: "read", Arguments: []byte(`{"path":"main.go"}`)}}},
	}, {
		Role: "toolResult", ToolCallID: "read:2", ToolName: "read", Content: "ok",
	}}}, false)

	if len(messages) != 2 || len(messages[0].ToolCalls) != 1 {
		t.Fatalf("converted messages = %#v", messages)
	}
	if messages[1].Role != "tool" || messages[1].ToolCallID != "read:2" || messages[1].Name != "read" {
		t.Fatalf("tool message = %#v, want Kimi-compatible id and name", messages[1])
	}
}

func TestConvertMessagesImageDetailForOfficialProviders(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		detail  string
		want    string
	}{
		{name: "openai detail", baseURL: "https://api.openai.com/v1", detail: "detail", want: "high"},
		{name: "xai fast", baseURL: "https://api.x.ai/v1", detail: "fast", want: "low"},
		{name: "spoofed openai domain omitted", baseURL: "https://api.openai.com.example/v1", detail: "detail", want: ""},
		{name: "compatible gateway omitted", baseURL: "https://openrouter.ai/api/v1", detail: "detail", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Provider{baseURL: tt.baseURL}
			messages := p.convertMessages(provider.ChatParams{
				Messages: []provider.Message{
					{
						Role: "user",
						Contents: []provider.ContentBlock{
							{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "abc123", Detail: tt.detail}},
						},
					},
				},
			}, false)

			if len(messages) != 1 {
				t.Fatalf("messages len = %d, want 1", len(messages))
			}
			blocks, ok := messages[0].Content.([]openAIContentBlock)
			if !ok || len(blocks) != 1 || blocks[0].ImageURL == nil {
				t.Fatalf("content = %#v, want one image block", messages[0].Content)
			}
			if got := blocks[0].ImageURL.Detail; got != tt.want {
				t.Fatalf("detail = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConvertMessagesLimitsHistoricalImagesForMoark(t *testing.T) {
	var input []provider.Message
	for i := 0; i < 6; i++ {
		input = append(input, provider.Message{
			Role: "user",
			Contents: []provider.ContentBlock{{
				Type:  "image",
				Image: &provider.ImageContent{MimeType: "image/png", Data: fmt.Sprintf("image-%d", i)},
			}},
		})
	}

	p := NewProviderWithModels("key", "https://api.moark.com/v1", nil)
	got := p.convertMessages(provider.ChatParams{Messages: input}, false)
	if len(got) != len(input) {
		t.Fatalf("converted messages = %d, want %d", len(got), len(input))
	}

	imageCount := 0
	for i, msg := range got {
		blocks, ok := msg.Content.([]openAIContentBlock)
		if ok {
			for _, block := range blocks {
				if block.Type == "image_url" {
					imageCount++
				}
			}
		}
		if i == 0 {
			if text, ok := msg.Content.(string); !ok || text != "[image omitted: provider image limit]" {
				t.Fatalf("oldest image content = %#v, want omission marker", msg.Content)
			}
		}
	}
	if imageCount != 5 {
		t.Fatalf("image block count = %d, want 5", imageCount)
	}
}

func TestConvertMessagesDoesNotLimitImagesForOtherGateways(t *testing.T) {
	var input []provider.Message
	for i := 0; i < 6; i++ {
		input = append(input, provider.Message{
			Role: "user",
			Contents: []provider.ContentBlock{{
				Type:  "image",
				Image: &provider.ImageContent{MimeType: "image/png", Data: fmt.Sprintf("image-%d", i)},
			}},
		})
	}

	p := NewProviderWithModels("key", "https://api.example.test/v1", nil)
	got := p.convertMessages(provider.ChatParams{Messages: input}, false)
	imageCount := 0
	for _, msg := range got {
		blocks, ok := msg.Content.([]openAIContentBlock)
		if !ok {
			continue
		}
		for _, block := range blocks {
			if block.Type == "image_url" {
				imageCount++
			}
		}
	}
	if imageCount != 6 {
		t.Fatalf("image block count = %d, want 6", imageCount)
	}
}

func TestConvertMessagesUsesConfiguredImageLimit(t *testing.T) {
	var input []provider.Message
	for i := 0; i < 4; i++ {
		input = append(input, provider.Message{
			Role: "user",
			Contents: []provider.ContentBlock{{
				Type:  "image",
				Image: &provider.ImageContent{MimeType: "image/png", Data: fmt.Sprintf("image-%d", i)},
			}},
		})
	}

	p := NewProviderWithModels("key", "https://api.example.test/v1", nil)
	p.SetMaxImagesPerRequest(2)
	got := p.convertMessages(provider.ChatParams{Messages: input}, false)
	imageCount := 0
	for _, msg := range got {
		blocks, ok := msg.Content.([]openAIContentBlock)
		if !ok {
			continue
		}
		for _, block := range blocks {
			if block.Type == "image_url" {
				imageCount++
			}
		}
	}
	if imageCount != 2 {
		t.Fatalf("image block count = %d, want 2", imageCount)
	}

	p.SetMaxImagesPerRequest(-1)
	got = p.convertMessages(provider.ChatParams{Messages: input}, false)
	imageCount = 0
	for _, msg := range got {
		blocks, ok := msg.Content.([]openAIContentBlock)
		if !ok {
			continue
		}
		for _, block := range blocks {
			if block.Type == "image_url" {
				imageCount++
			}
		}
	}
	if imageCount != 4 {
		t.Fatalf("unlimited image block count = %d, want 4", imageCount)
	}
}

func TestResponsesMessageContentImageDetailForOfficialProviders(t *testing.T) {
	msg := provider.Message{
		Role: "user",
		Contents: []provider.ContentBlock{
			{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "abc123", Detail: "detail"}},
		},
	}

	for _, baseURL := range []string{"https://api.openai.com/v1", "https://api.x.ai/v1"} {
		p := &Provider{baseURL: baseURL}
		content, ok := p.responsesMessageContent(msg, "input_text").([]responsesContentBlock)
		if !ok || len(content) != 1 {
			t.Fatalf("%s content = %#v, want one responses content block", baseURL, content)
		}
		if content[0].Detail != "high" {
			t.Fatalf("%s detail = %q, want high", baseURL, content[0].Detail)
		}
	}

	p := &Provider{baseURL: "https://openrouter.ai/api/v1"}
	content, ok := p.responsesMessageContent(msg, "input_text").([]responsesContentBlock)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v, want one responses content block", content)
	}
	if content[0].Detail != "" {
		t.Fatalf("gateway detail = %q, want empty", content[0].Detail)
	}
}

func TestOpenAICustomHeaders(t *testing.T) {
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "gpt-test"}}, "data: [DONE]\n", nil, func(r *http.Request) {
		if r.Header.Get("X-Custom-Header") != "custom-value" {
			t.Fatalf("X-Custom-Header = %q, want custom-value", r.Header.Get("X-Custom-Header"))
		}
		if r.Header.Get("Authorization") != "Bearer override-key" {
			t.Fatalf("Authorization = %q, want Bearer override-key", r.Header.Get("Authorization"))
		}
	})
	p.SetHeaders(map[string]string{
		"X-Custom-Header": "custom-value",
		"Authorization":   "Bearer override-key",
	})

	params := provider.ChatParams{
		ModelID:  "gpt-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}
}

func TestOpenAIChatParallelToolCallsRequest(t *testing.T) {
	falseValue := false
	tests := []struct {
		name          string
		model         *provider.Model
		responseValue *bool
		want          any
	}{
		{
			name:  "defaults enabled when function tools are present",
			model: &provider.Model{ID: "chat-test"},
			want:  true,
		},
		{
			name:          "response option overrides default",
			model:         &provider.Model{ID: "chat-test"},
			responseValue: &falseValue,
			want:          false,
		},
		{
			name: "compatibility flag omits unsupported field",
			model: &provider.Model{
				ID:     "chat-test",
				Compat: &provider.ModelCompat{SupportsParallelToolCalls: &falseValue},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyCh := make(chan string, 1)
			p := newMockOpenAIProvider(t, []*provider.Model{tt.model}, "data: [DONE]\n", bodyCh, nil)
			params := provider.ChatParams{
				ModelID:  tt.model.ID,
				Messages: []provider.Message{provider.NewUserMessage("use the tool")},
				Tools: []provider.ToolDefinition{{
					Name:       "read",
					Parameters: json.RawMessage(`{"type":"object"}`),
				}},
				Abort: make(chan struct{}),
			}
			if tt.responseValue != nil {
				params.ResponseOptions = &provider.ResponseOptions{ParallelTools: tt.responseValue}
			}
			for range p.Chat(context.Background(), params) {
			}

			var raw map[string]any
			select {
			case body := <-bodyCh:
				if err := json.Unmarshal([]byte(body), &raw); err != nil {
					t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
				}
			default:
				t.Fatal("no request body captured")
			}
			got, present := raw["parallel_tool_calls"]
			if tt.want == nil {
				if present {
					t.Fatalf("parallel_tool_calls = %#v, want omitted", got)
				}
				return
			}
			if !present || got != tt.want {
				t.Fatalf("parallel_tool_calls = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestOpenAIChatParsesMultipleToolCalls(t *testing.T) {
	toolChunk := func(index int, id, name, arguments string) string {
		t.Helper()
		payload := map[string]any{
			"choices": []any{map[string]any{
				"delta": map[string]any{
					"tool_calls": []any{map[string]any{
						"index": index,
						"id":    id,
						"type":  "function",
						"function": map[string]any{
							"name":      name,
							"arguments": arguments,
						},
					}},
				},
			}},
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal tool chunk: %v", err)
		}
		return "data: " + string(raw)
	}

	sse := strings.Join([]string{
		toolChunk(0, "call_0", "read", `{"path":"a`),
		toolChunk(1, "call_1", "read", `{"path":"b`),
		toolChunk(0, "", "", `"}`),
		toolChunk(1, "", "", `"}`),
		"data: [DONE]",
	}, "\n") + "\n"
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "chat-test"}}, sse, nil, nil)
	events := chatAndCollect(t, p, provider.ChatParams{
		ModelID:  "chat-test",
		Messages: []provider.Message{provider.NewUserMessage("read both files")},
		Abort:    make(chan struct{}),
	})

	var calls []provider.ToolCallBlock
	for _, event := range events {
		if event.Type == provider.StreamToolCall && event.ToolCall != nil {
			calls = append(calls, *event.ToolCall)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("tool call count = %d, want 2; events = %#v", len(calls), events)
	}
	want := []provider.ToolCallBlock{
		{ID: "call_0", Name: "read", Arguments: json.RawMessage(`{"path":"a"}`)},
		{ID: "call_1", Name: "read", Arguments: json.RawMessage(`{"path":"b"}`)},
	}
	for i := range want {
		if calls[i].ID != want[i].ID || calls[i].Name != want[i].Name || string(calls[i].Arguments) != string(want[i].Arguments) {
			t.Fatalf("tool call %d = %#v, want %#v", i, calls[i], want[i])
		}
	}
}

func TestOpenAIResponsesCustomHeaders(t *testing.T) {
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "gpt-test"}}, "data: [DONE]\n", nil, func(r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
		}
		if r.Header.Get("X-Responses-Header") != "responses-value" {
			t.Fatalf("X-Responses-Header = %q, want responses-value", r.Header.Get("X-Responses-Header"))
		}
	})
	p.SetUseResponsesAPI(true)
	p.SetHeaders(map[string]string{"X-Responses-Header": "responses-value"})

	params := provider.ChatParams{
		ModelID:  "gpt-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}
}

func TestOpenAIKimiThinkingEffort(t *testing.T) {
	cases := []struct {
		level provider.ThinkingLevel
		want  string
	}{
		{provider.ThinkingMinimal, "low"},
		{provider.ThinkingLow, "low"},
		{provider.ThinkingMedium, "high"},
		{provider.ThinkingHigh, "high"},
		{provider.ThinkingXHigh, "max"},
	}
	for _, tc := range cases {
		if got := kimiReasoningEffort(tc.level); got != tc.want {
			t.Errorf("kimiReasoningEffort(%q) = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestNormalizeToolResultSequenceRepairsMissingKimiResponses(t *testing.T) {
	messages := []provider.Message{
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "toolCall", ToolCall: &provider.ToolCallBlock{
			ID: "read:26", Name: "read", Arguments: []byte(`{"path":"main.go"}`),
		}}}),
		provider.NewUserMessage("continue"),
	}

	got := normalizeToolResultSequence(messages)
	if len(got) != 3 {
		t.Fatalf("message count = %d, want 3", len(got))
	}
	if got[1].Role != "toolResult" || got[1].ToolCallID != "read:26" || got[1].ToolName != "read" {
		t.Fatalf("repaired result = %#v", got[1])
	}
	if !got[1].IsError || !strings.Contains(got[1].Content, "unavailable") {
		t.Fatalf("repaired result content = %#v", got[1])
	}
	if got[2].Role != "user" {
		t.Fatalf("message after repair = %#v, want user", got[2])
	}
}

func TestNormalizeToolResultSequenceDoesNotDuplicateResults(t *testing.T) {
	messages := []provider.Message{
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "call-1", Name: "read"}}}),
		provider.NewToolResultMessage("call-1", "read", "ok", false),
	}
	got := normalizeToolResultSequence(messages)
	if len(got) != len(messages) {
		t.Fatalf("message count = %d, want %d", len(got), len(messages))
	}
}

func TestNormalizeToolResultSequenceOrdersAndFiltersResults(t *testing.T) {
	messages := []provider.Message{
		provider.NewAssistantMessage([]provider.ContentBlock{
			{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "a", Name: "read"}},
			{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "b", Name: "grep"}},
		}),
		provider.NewToolResultMessage("stale", "old", "bad", false),
		provider.NewToolResultMessage("b", "grep", "B", false),
		provider.NewToolResultMessage("b", "grep", "duplicate", false),
		provider.NewToolResultMessage("a", "read", "A", false),
	}
	got := normalizeToolResultSequence(messages)
	if len(got) != 3 || got[1].ToolCallID != "a" || got[2].ToolCallID != "b" {
		t.Fatalf("normalized sequence = %#v", got)
	}
}

func TestNormalizeToolResultSequenceDropsOrphanedResults(t *testing.T) {
	messages := []provider.Message{
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "call-1", Name: "read"}}}),
		provider.NewToolResultMessage("call-1", "read", "ok", false),
		provider.NewToolResultMessage("read:25", "read", "stale", false),
		provider.NewUserMessage("continue"),
	}
	got := normalizeToolResultSequence(messages)
	if len(got) != 3 || got[2].Role != "user" {
		t.Fatalf("normalized orphan sequence = %#v", got)
	}
}

func TestOpenAIRequiresReasoningContentForKimiModels(t *testing.T) {
	p := NewProviderWithModels("key", "https://api.test/v1", nil)
	if !p.requiresReasoningContentOnAssistant(&provider.Model{ID: "kimi-k3"}) {
		t.Fatal("kimi-k3 should require reasoning_content")
	}
	if !p.requiresReasoningContentOnAssistant(&provider.Model{ID: "k3"}) {
		t.Fatal("k3 should require reasoning_content")
	}
}

func TestConvertMessagesKeepsImageToolResponsesAdjacent(t *testing.T) {
	image := &provider.ImageContent{Data: "a", MimeType: "image/png"}
	messages := []provider.Message{
		provider.NewAssistantMessage([]provider.ContentBlock{
			{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "r1", Name: "read"}},
			{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "r2", Name: "read"}},
			{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "r3", Name: "read"}},
		}),
		provider.NewToolResultMessageWithContents("r1", "read", "one", []provider.ContentBlock{{Type: "image", Image: image}}, false),
		provider.NewToolResultMessageWithContents("r2", "read", "two", []provider.ContentBlock{{Type: "image", Image: image}}, false),
		provider.NewToolResultMessageWithContents("r3", "read", "three", []provider.ContentBlock{{Type: "image", Image: image}}, false),
	}
	p := NewProviderWithModels("key", "https://api.test/v1", nil)
	got := p.convertMessages(provider.ChatParams{Messages: messages}, false)
	if len(got) != 5 || got[1].Role != "tool" || got[2].Role != "tool" || got[3].Role != "tool" || got[4].Role != "user" {
		t.Fatalf("converted roles = %#v", got)
	}
}

func TestDeepSeekReasoningEffort(t *testing.T) {
	cases := []struct {
		level provider.ThinkingLevel
		want  string
	}{
		{provider.ThinkingMinimal, "high"},
		{provider.ThinkingLow, "high"},
		{provider.ThinkingMedium, "high"},
		{provider.ThinkingHigh, "high"},
		{provider.ThinkingXHigh, "max"},
	}
	for _, tc := range cases {
		if got := deepseekReasoningEffort(tc.level); got != tc.want {
			t.Errorf("deepseekReasoningEffort(%q) = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestDoubaoSeedReasoningEffort(t *testing.T) {
	cases := []struct {
		level provider.ThinkingLevel
		want  string
	}{
		{provider.ThinkingMinimal, "minimal"},
		{provider.ThinkingLow, "low"},
		{provider.ThinkingMedium, "medium"},
		{provider.ThinkingHigh, "high"},
		{provider.ThinkingXHigh, "high"},
		{provider.ThinkingOff, ""},
	}
	for _, tc := range cases {
		if got := doubaoSeedReasoningEffort(tc.level); got != tc.want {
			t.Errorf("doubaoSeedReasoningEffort(%q) = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestIsDoubaoSeedModel(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"doubao-seed-2.1-turbo", true},
		{"doubao-seed-2-1-turbo-260628", true},
		{"doubao-seed-evolving", true},
		{"doubao-seed-2-0-lite", false},
	}
	for _, tc := range cases {
		if got := isDoubaoSeedModel(tc.id); got != tc.want {
			t.Errorf("isDoubaoSeedModel(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestOpenAIThinkingFormatDeepSeekV41NoReasoningEffort(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{
		{ID: "deepseek-v4.1-flash", Reasoning: true},
	}, "data: [DONE]\n", bodyCh, nil)
	params := provider.ChatParams{
		ModelID:       "deepseek-v4.1-flash",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingHigh,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req openAIRequest
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
	if req.ReasoningEffort != "" {
		t.Fatalf("reasoning_effort = %q, want empty: vLLM-backed gateways reject reasoning_effort for DeepSeek V4.1 chat template", req.ReasoningEffort)
	}
}

func TestIsDeepSeekV41Model(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"deepseek-v4.1-flash", true},
		{"DeepSeek-V4.1-Flash", true},
		{"deepseek-v4-1", true},
		{"deepseek-v4-flash", false},
		{"deepseek-v4-pro", false},
		{"glm-5.3-flash", false},
	}
	for _, tc := range cases {
		if got := isDeepSeekV41Model(tc.id); got != tc.want {
			t.Errorf("isDeepSeekV41Model(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestOpenAIThinkingFormatDeepSeekAutoDetect(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{
		{ID: "deepseek-test", Reasoning: true},
	}, "data: [DONE]\n", bodyCh, nil)
	p.baseURL = p.baseURL + "/deepseek"
	params := provider.ChatParams{
		ModelID:       "deepseek-test",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingXHigh,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req openAIRequest
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
	if req.ReasoningEffort != "max" {
		t.Fatalf("reasoning_effort = %q, want max", req.ReasoningEffort)
	}
}

func TestOpenAIThinkingFormatDeepSeekHighEffort(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{
		{ID: "deepseek-v4-flash", Reasoning: true},
	}, "data: [DONE]\n", bodyCh, nil)
	p.baseURL = p.baseURL + "/deepseek"
	params := provider.ChatParams{
		ModelID:       "deepseek-v4-flash",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingHigh,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req openAIRequest
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
	if req.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort = %q, want high", req.ReasoningEffort)
	}
}

func TestOpenAIThinkingFormatFromModelCompat(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{
		{ID: "compat-test", Reasoning: true, Compat: &provider.ModelCompat{ThinkingFormat: "deepseek"}},
	}, "data: [DONE]\n", bodyCh, nil)
	params := provider.ChatParams{
		ModelID:       "compat-test",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingHigh,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req openAIRequest
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
	if req.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort = %q, want high", req.ReasoningEffort)
	}
}

func TestQwenThinkingBudget(t *testing.T) {
	cases := []struct {
		level provider.ThinkingLevel
		want  int
	}{
		{provider.ThinkingMinimal, 500},
		{provider.ThinkingLow, 500},
		{provider.ThinkingMedium, 4096},
		{provider.ThinkingHigh, 4096},
		{provider.ThinkingXHigh, 10240},
	}
	for _, tc := range cases {
		if got := qwenThinkingBudget(tc.level); got != tc.want {
			t.Errorf("qwenThinkingBudget(%q) = %d, want %d", tc.level, got, tc.want)
		}
	}
}

func TestIsQwenModel(t *testing.T) {
	positive := []string{"qwen3.6-flash", "qwen3.6-plus", "qwen3.7-plus", "qwen3.7-max", "qwen3.8-max-preview", "qwen/qwen3.7-plus", "Qwen3.6-Max"}
	for _, id := range positive {
		if !isQwenModel(id) {
			t.Errorf("isQwenModel(%q) = false, want true", id)
		}
	}
	negative := []string{"qwen3-coder-plus", "qwen3-max-2026-01-23", "qwen2.5-72b", "deepseek-v4-flash", "glm-5"}
	for _, id := range negative {
		if isQwenModel(id) {
			t.Errorf("isQwenModel(%q) = true, want false", id)
		}
	}
}

func TestOpenAIThinkingFormatQwen(t *testing.T) {
	cases := []struct {
		name       string
		modelID    string
		level      provider.ThinkingLevel
		wantBudget int
	}{
		{"qwen3.7-plus low", "qwen3.7-plus", provider.ThinkingLow, 500},
		{"qwen3.7-plus medium", "qwen3.7-plus", provider.ThinkingMedium, 4096},
		{"qwen3.6-flash high", "qwen3.6-flash", provider.ThinkingHigh, 4096},
		{"qwen3.8-max-preview xhigh", "qwen3.8-max-preview", provider.ThinkingXHigh, 10240},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bodyCh := make(chan string, 1)
			p := newMockOpenAIProvider(t, []*provider.Model{
				{ID: tc.modelID, Reasoning: true},
			}, "data: [DONE]\n", bodyCh, nil)
			params := provider.ChatParams{
				ModelID:       tc.modelID,
				Messages:      []provider.Message{provider.NewUserMessage("hi")},
				ThinkingLevel: tc.level,
				Abort:         make(chan struct{}),
			}
			for range p.Chat(context.Background(), params) {
			}

			var req openAIRequest
			select {
			case body := <-bodyCh:
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
				}
			default:
				t.Fatal("no request body captured")
			}

			if req.EnableThinking == nil || !*req.EnableThinking {
				t.Fatalf("enable_thinking = %v, want true", req.EnableThinking)
			}
			if req.ThinkingBudget != tc.wantBudget {
				t.Fatalf("thinking_budget = %d, want %d", req.ThinkingBudget, tc.wantBudget)
			}
			if req.ReasoningEffort != "" {
				t.Fatalf("reasoning_effort = %q, should be empty for qwen", req.ReasoningEffort)
			}
			if req.Thinking != nil {
				t.Fatalf("thinking = %#v, should be nil for qwen", req.Thinking)
			}
		})
	}
}

func TestOpenAIOmitsMaxTokensByDefault(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "gpt-test", MaxTokens: 64000}}, "data: [DONE]\n", bodyCh, nil)
	params := provider.ChatParams{
		ModelID:  "gpt-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if _, ok := raw["max_tokens"]; ok {
		t.Fatalf("max_tokens = %#v, want omitted by default", raw["max_tokens"])
	}
	if _, ok := raw["max_completion_tokens"]; ok {
		t.Fatalf("max_completion_tokens = %#v, want omitted by default", raw["max_completion_tokens"])
	}
}

func TestOpenAIInfersMaxCompletionTokensForNewModels(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "gpt-5-mini", MaxTokens: 64000}}, "data: [DONE]\n", bodyCh, nil)
	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:   "gpt-5-mini",
		Messages:  []provider.Message{provider.NewUserMessage("summarize")},
		MaxTokens: 2048,
		Abort:     make(chan struct{}),
	}) {
	}

	var raw map[string]any
	body := <-bodyCh
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
	}
	if _, ok := raw["max_tokens"]; ok {
		t.Fatalf("max_tokens = %#v, want omitted", raw["max_tokens"])
	}
	if got := raw["max_completion_tokens"]; got != float64(2048) {
		t.Fatalf("max_completion_tokens = %#v, want 2048", got)
	}
}

func TestOpenAIModelCompatRequestFields(t *testing.T) {
	bodyCh := make(chan string, 1)
	supportsReasoningEffort := false

	p := newMockOpenAIProvider(t, []*provider.Model{
		{
			ID:        "compat-fields",
			Reasoning: true,
			Compat: &provider.ModelCompat{
				MaxTokensField:          "max_completion_tokens",
				SupportsReasoningEffort: &supportsReasoningEffort,
			},
		},
	}, "data: [DONE]\n", bodyCh, nil)
	params := provider.ChatParams{
		ModelID:       "compat-fields",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingHigh,
		MaxTokens:     1234,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if _, ok := raw["max_tokens"]; ok {
		t.Fatalf("max_tokens present, want max_completion_tokens only: %#v", raw)
	}
	if got := raw["max_completion_tokens"]; got != float64(1234) {
		t.Fatalf("max_completion_tokens = %#v, want 1234", got)
	}
	if _, ok := raw["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort present despite compat flag: %#v", raw)
	}
}

func TestOpenAIRetriesUnsupportedMaxTokensWithCompletionTokens(t *testing.T) {
	attempts := 0
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "custom-reasoning-model"}})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if attempts == 1 {
			if _, ok := raw["max_tokens"]; !ok {
				t.Fatalf("first request did not contain max_tokens: %s", body)
			}
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead."}}`)),
				Request:    r,
			}, nil
		}
		if _, ok := raw["max_tokens"]; ok {
			t.Errorf("fallback request still contained max_tokens: %s", body)
		}
		if got := raw["max_completion_tokens"]; got != float64(2048) {
			t.Errorf("max_completion_tokens = %#v, want 2048", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n")),
			Request:    r,
		}, nil
	})}

	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:   "custom-reasoning-model",
		Messages:  []provider.Message{provider.NewUserMessage("summarize")},
		MaxTokens: 2048,
		Abort:     make(chan struct{}),
	}) {
	}
	if attempts != 2 {
		t.Fatalf("request attempts = %d, want 2", attempts)
	}
}
func TestOpenAIRequiresReasoningContentOnAssistant(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{
		{
			ID: "compat-reasoning",
			Compat: &provider.ModelCompat{
				RequiresReasoningContentOnAssistant: true,
			},
		},
	}, "data: [DONE]\n", bodyCh, nil)
	params := provider.ChatParams{
		ModelID: "compat-reasoning",
		Messages: []provider.Message{
			provider.NewAssistantMessage([]provider.ContentBlock{
				{Type: "text", Text: "previous answer"},
			}),
			provider.NewUserMessage("continue"),
		},
		Abort: make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	messages, ok := raw["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("messages = %#v, want non-empty array", raw["messages"])
	}
	assistant, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("first message = %#v, want object", messages[0])
	}
	value, ok := assistant["reasoning_content"]
	if !ok {
		t.Fatalf("reasoning_content missing from assistant message: %#v", assistant)
	}
	if value != "" {
		t.Fatalf("reasoning_content = %#v, want empty string", value)
	}
}

func TestOpenAIResponsesAPIRequest(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{
		{ID: "responses-test", Reasoning: true},
	}, "data: [DONE]\n", bodyCh, func(r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
		}
	})
	p.SetUseResponsesAPI(true)

	params := provider.ChatParams{
		ModelID:       "responses-test",
		SystemPrompt:  "You are a helper.",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingXHigh,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if raw["model"] != "responses-test" {
		t.Fatalf("model = %#v, want responses-test", raw["model"])
	}
	if raw["instructions"] != "You are a helper." {
		t.Fatalf("instructions = %#v, want system prompt", raw["instructions"])
	}
	if raw["stream"] != true {
		t.Fatalf("stream = %#v, want true", raw["stream"])
	}
	if _, ok := raw["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens = %#v, want omitted by default", raw["max_output_tokens"])
	}
	if _, ok := raw["input"].([]any); !ok {
		t.Fatalf("input = %#v, want array", raw["input"])
	}
	if _, ok := raw["reasoning"].(map[string]any); !ok {
		t.Fatalf("reasoning = %#v, want object", raw["reasoning"])
	}
	reasoning := raw["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning.effort = %#v, want high", reasoning["effort"])
	}
	if reasoning["summary"] != "auto" {
		t.Fatalf("reasoning.summary = %#v, want auto", reasoning["summary"])
	}
	if raw["prompt_cache_key"] == "" {
		t.Fatalf("prompt_cache_key missing: %#v", raw)
	}
}

func TestOpenAIResponsesAPIConfigOverrides(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{
		{ID: "responses-test", Reasoning: true},
	}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)
	p.SetResponsesConfig(config.ResponsesConfig{
		ReasoningSummary:     "concise",
		PromptCacheKey:       "custom-cache-key",
		PromptCacheRetention: "24h",
	})

	params := provider.ChatParams{
		ModelID:       "responses-test",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		MaxTokens:     1234,
		ThinkingLevel: provider.ThinkingMinimal,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	reasoning, ok := raw["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want object", raw["reasoning"])
	}
	if reasoning["effort"] != "minimal" {
		t.Fatalf("reasoning.effort = %#v, want minimal", reasoning["effort"])
	}
	if reasoning["summary"] != "concise" {
		t.Fatalf("reasoning.summary = %#v, want concise", reasoning["summary"])
	}
	if raw["prompt_cache_key"] != "custom-cache-key" {
		t.Fatalf("prompt_cache_key = %#v, want custom-cache-key", raw["prompt_cache_key"])
	}
	if raw["prompt_cache_retention"] != "24h" {
		t.Fatalf("prompt_cache_retention = %#v, want 24h", raw["prompt_cache_retention"])
	}
	if raw["max_output_tokens"] != float64(1234) {
		t.Fatalf("max_output_tokens = %#v, want 1234", raw["max_output_tokens"])
	}
}

func TestOpenAIResponsesAPIConfigAndResponseOptions(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "responses-test"}}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)
	p.SetResponsesConfig(config.ResponsesConfig{
		StateMode:    "conversation",
		Store:        config.BoolPtr(true),
		Conversation: "conv_123",
		Truncation:   "auto",
		Include:      []string{"reasoning.encrypted_content"},
		ServiceTier:  "flex",
	})

	parallel := false
	maxCalls := 2
	params := provider.ChatParams{
		ModelID:  "responses-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
		ResponseOptions: &provider.ResponseOptions{
			ParallelTools: &parallel,
			MaxToolCalls:  &maxCalls,
			ToolChoice:    &provider.ToolChoice{Type: "function", Name: "bash"},
			StructuredOutput: &provider.StructuredOutputOptions{
				Format: "json_schema",
				Name:   "result",
				Strict: true,
				Schema: json.RawMessage(`{"type":"object"}`),
			},
		},
	}
	for range p.Chat(context.Background(), params) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if raw["store"] != true {
		t.Fatalf("store = %#v, want true", raw["store"])
	}
	if raw["conversation"] != "conv_123" {
		t.Fatalf("conversation = %#v, want conv_123", raw["conversation"])
	}
	if raw["truncation"] != "auto" {
		t.Fatalf("truncation = %#v, want auto", raw["truncation"])
	}
	if raw["service_tier"] != "flex" {
		t.Fatalf("service_tier = %#v, want flex", raw["service_tier"])
	}
	if raw["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls = %#v, want false", raw["parallel_tool_calls"])
	}
	if raw["max_tool_calls"] != float64(2) {
		t.Fatalf("max_tool_calls = %#v, want 2", raw["max_tool_calls"])
	}
	include, ok := raw["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v, want reasoning encrypted content", raw["include"])
	}
	choice, ok := raw["tool_choice"].(map[string]any)
	if !ok || choice["type"] != "function" || choice["name"] != "bash" {
		t.Fatalf("tool_choice = %#v, want function bash", raw["tool_choice"])
	}
	text, ok := raw["text"].(map[string]any)
	if !ok {
		t.Fatalf("text = %#v, want object", raw["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok || format["type"] != "json_schema" || format["name"] != "result" || format["strict"] != true {
		t.Fatalf("text.format = %#v, want strict json_schema result", text["format"])
	}
}

func TestOpenAIResponsesAPIConfigFieldsAreEncoded(t *testing.T) {
	bodyCh := make(chan string, 1)
	strict := false
	parallel := false
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "responses-test"}}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{
		StructuredOutput: config.ResponsesStructuredOutputConfig{
			Name:   "result",
			Strict: &strict,
			Schema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`),
		},
		ToolControl: config.ResponsesToolControlConfig{
			Choice:   "required",
			Parallel: &parallel,
			MaxCalls: 3,
		},
		HostedTools: config.ResponsesHostedToolsConfig{
			FileSearch: map[string]any{"max_num_results": 5},
		},
	}); err != nil {
		t.Fatalf("set Responses config: %v", err)
	}

	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:  "responses-test",
		Messages: []provider.Message{provider.NewUserMessage("find a file")},
		Abort:    make(chan struct{}),
	}) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if raw["tool_choice"] != "required" {
		t.Fatalf("tool_choice = %#v, want required", raw["tool_choice"])
	}
	if raw["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls = %#v, want false", raw["parallel_tool_calls"])
	}
	if raw["max_tool_calls"] != float64(3) {
		t.Fatalf("max_tool_calls = %#v, want 3", raw["max_tool_calls"])
	}
	tools, ok := raw["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one configured hosted tool", raw["tools"])
	}
	fileSearch, ok := tools[0].(map[string]any)
	if !ok || fileSearch["type"] != "file_search" || fileSearch["max_num_results"] != float64(5) {
		t.Fatalf("file search tool = %#v, want preserved hosted config", tools[0])
	}
	text, ok := raw["text"].(map[string]any)
	if !ok {
		t.Fatalf("text = %#v, want object", raw["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok || format["type"] != "json_schema" || format["name"] != "result" || format["strict"] != false {
		t.Fatalf("text.format = %#v, want configured schema", text["format"])
	}
}

func TestOpenAIResponsesAPIPreviousResponseIDIsEncoded(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "responses-test"}}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{StateMode: "previous_response_id"}); err != nil {
		t.Fatalf("set Responses config: %v", err)
	}

	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:  "responses-test",
		Messages: []provider.Message{provider.NewUserMessage("continue")},
		ResponseOptions: &provider.ResponseOptions{
			PreviousResponseID: "resp_previous",
		},
		Abort: make(chan struct{}),
	}) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if raw["previous_response_id"] != "resp_previous" {
		t.Fatalf("previous_response_id = %#v, want resp_previous", raw["previous_response_id"])
	}
	if _, ok := raw["conversation"]; ok {
		t.Fatalf("conversation = %#v, want omitted", raw["conversation"])
	}
}

func TestOpenAIResponsesAPIConversationCanBeSuppressedForReplay(t *testing.T) {
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "responses-test"}})
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{
		StateMode:    "conversation",
		Conversation: "conv_1",
		Store:        config.BoolPtr(true),
	}); err != nil {
		t.Fatalf("set Responses config: %v", err)
	}
	req, err := p.buildResponsesRequest(provider.ChatParams{
		Messages:        []provider.Message{provider.NewUserMessage("replay")},
		ResponseOptions: &provider.ResponseOptions{SuppressConversation: true},
	}, "responses-test", p.GetModel("responses-test"), false, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Conversation != "" {
		t.Fatalf("conversation = %q, want suppressed", req.Conversation)
	}
}

func TestOpenAIResponsesAPINativeReplayItemsArePreserved(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "responses-test"}}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)
	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:  "responses-test",
		Messages: []provider.Message{provider.NewUserMessage("must not be rebuilt")},
		ResponseOptions: &provider.ResponseOptions{ReplayItems: []json.RawMessage{
			json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]}`),
			json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"ciphertext"}`),
			json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"a\"}"}`),
		}},
		Abort: make(chan struct{}),
	}) {
	}
	var raw struct {
		Input []json.RawMessage `json:"input"`
	}
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v", err)
		}
	default:
		t.Fatal("no request body captured")
	}
	if len(raw.Input) != 3 || !strings.Contains(string(raw.Input[1]), `"encrypted_content":"ciphertext"`) || !strings.Contains(string(raw.Input[2]), `"call_id":"call_1"`) {
		t.Fatalf("native replay input = %#v", raw.Input)
	}
}

func TestOpenAIResponsesAPICustomToolRequestAndContinuation(t *testing.T) {
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "responses-test"}})
	p.SetUseResponsesAPI(true)
	params := provider.ChatParams{
		Tools: []provider.ToolDefinition{{
			Name:        "shell_script",
			Description: "Execute a constrained script.",
			Kind:        "custom",
			Format:      json.RawMessage(`{"type":"grammar","syntax":"regex","definition":"[a-z]+"}`),
		}},
		Messages: []provider.Message{
			provider.NewAssistantMessage([]provider.ContentBlock{{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "call_custom", Name: "shell_script", Kind: "custom", Input: "echo hello"}}}),
			func() provider.Message {
				result := provider.NewToolResultMessage("call_custom", "shell_script", "hello", false)
				result.ToolKind = "custom"
				return result
			}(),
		},
	}
	req, err := p.buildResponsesRequest(params, "responses-test", p.GetModel("responses-test"), false, false)
	if err != nil {
		t.Fatalf("build Responses request: %v", err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Type != "custom" || req.Tools[0].Name != "shell_script" || string(req.Tools[0].Format) != `{"type":"grammar","syntax":"regex","definition":"[a-z]+"}` {
		t.Fatalf("custom tool request = %#v", req.Tools)
	}
	if len(req.Input) != 3 || req.Input[1].Type != "custom_tool_call" || req.Input[1].Input != "echo hello" || req.Input[2].Type != "custom_tool_call_output" || req.Input[2].Output != "hello" {
		t.Fatalf("custom tool continuation = %#v", req.Input)
	}
}

func TestOpenAIResponsesAPICustomToolContentListOutput(t *testing.T) {
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "responses-test"}})
	result := provider.NewToolResultMessageWithContents("call-custom", "render", "", []provider.ContentBlock{
		{Type: "text", Text: "preview"},
		{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "aW1n", Detail: "low"}},
		{Type: "file", File: &provider.FileContent{ID: "file_123", Filename: "report.csv"}},
	}, false)
	result.ToolKind = "custom"
	items := p.convertResponsesInput(provider.ChatParams{Messages: []provider.Message{result}})
	if len(items) != 1 || items[0].Type != "custom_tool_call_output" {
		t.Fatalf("custom output items = %#v", items)
	}
	content, ok := items[0].Output.([]responsesContentBlock)
	if !ok || len(content) != 3 || content[0].Type != "input_text" || content[0].Text != "preview" || content[1].Type != "input_image" || content[1].ImageURL != "data:image/png;base64,aW1n" || content[1].Detail != "low" || content[2].Type != "input_file" || content[2].FileID != "file_123" || content[2].Filename != "report.csv" {
		t.Fatalf("custom output content = %#v", items[0].Output)
	}
}

func TestOpenAIResponsesAPIRejectsInvalidCustomToolFormat(t *testing.T) {
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "responses-test"}})
	p.SetUseResponsesAPI(true)
	err := p.validateResponsesCapabilities(p.GetModel("responses-test"), provider.ChatParams{Tools: []provider.ToolDefinition{{
		Name:   "shell_script",
		Kind:   "custom",
		Format: json.RawMessage(`{"type":"grammar","syntax":"invalid","definition":"x"}`),
	}}})
	if err == nil || !strings.Contains(err.Error(), "grammar syntax") {
		t.Fatalf("error = %v, want grammar syntax validation", err)
	}
}

func TestOpenAIResponsesAPIRejectsInvalidNativeReplayItem(t *testing.T) {
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "responses-test"}})
	p.SetUseResponsesAPI(true)
	_, err := p.buildResponsesRequest(provider.ChatParams{ResponseOptions: &provider.ResponseOptions{
		ReplayItems: []json.RawMessage{json.RawMessage(`{invalid`)},
	}}, "responses-test", p.GetModel("responses-test"), true, false)
	if err == nil || !strings.Contains(err.Error(), "replay item") {
		t.Fatalf("error = %v, want replay item validation", err)
	}
}

func TestOpenAIResponsesAPIRejectsInvalidConfig(t *testing.T) {
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "responses-test"}})
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{
		StateMode:    "conversation",
		Conversation: "",
	}); err == nil || !strings.Contains(err.Error(), "conversation") {
		t.Fatalf("error = %v, want conversation validation error", err)
	}
}

func TestOpenAIResponsesAPIValidatesStrictStructuredOutputSchema(t *testing.T) {
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "responses-test"}})
	p.SetUseResponsesAPI(true)
	strict := true
	invalid := config.ResponsesConfig{StructuredOutput: config.ResponsesStructuredOutputConfig{
		Strict: &strict, Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":[]}`),
	}}
	if err := p.SetResponsesConfig(invalid); err == nil || !strings.Contains(err.Error(), "additionalProperties=false") {
		t.Fatalf("invalid strict schema error = %v", err)
	}
	valid := config.ResponsesConfig{StructuredOutput: config.ResponsesStructuredOutputConfig{
		Strict: &strict, Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"answer":{"type":"string"},"items":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string"}},"required":["name"]}}},"required":["answer","items"]}`),
	}}
	if err := p.SetResponsesConfig(valid); err != nil {
		t.Fatalf("valid strict schema: %v", err)
	}
}

func TestOpenAIResponsesStateFallbackError(t *testing.T) {
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "responses-test"}})
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{StateMode: "previous_response_id"}); err != nil {
		t.Fatalf("set Responses config: %v", err)
	}
	for _, test := range []struct {
		name string
		err  string
		want bool
	}{
		{name: "not found", err: "API error 404: response not found", want: true},
		{name: "expired lineage", err: "previous_response_id expired", want: true},
		{name: "permission denied", err: "API error 403: forbidden", want: true},
		{name: "rate limit", err: "API error 429: rate limit", want: false},
		{name: "server error", err: "API error 500: unavailable", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := p.ResponseStateFallbackError(fmt.Errorf("%s", test.err)); got != test.want {
				t.Fatalf("fallback = %v, want %v", got, test.want)
			}
		})
	}
	if got := p.ResponseStateFailureClass(fmt.Errorf("API error 403: forbidden")); got != provider.ResponseStateFailurePermission {
		t.Fatalf("permission failure class = %q", got)
	}
}

func TestOpenAIResponsesAPIRejectsComputerUseConfig(t *testing.T) {
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "responses-test"}})
	p.SetUseResponsesAPI(true)
	err := p.SetResponsesConfig(config.ResponsesConfig{
		HostedTools: config.ResponsesHostedToolsConfig{
			ComputerUse: map[string]any{},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "computerUse") {
		t.Fatalf("error = %v, want computer use configuration rejection", err)
	}

	err = p.SetResponsesConfig(config.ResponsesConfig{
		HostedTools: config.ResponsesHostedToolsConfig{
			ComputerUse: map[string]any{"display_width": 1280},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "computerUse") {
		t.Fatalf("error = %v, want computer use configuration rejection", err)
	}
}

func TestOpenAIResponsesAPIValidatesRemoteMCPURLs(t *testing.T) {
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "responses-test"}})
	p.SetUseResponsesAPI(true)
	checks := []struct {
		name string
		tool map[string]any
		want string
	}{
		{name: "missing endpoint", tool: map[string]any{"server_label": "docs"}, want: "requires server_url or connector_id"},
		{name: "http", tool: map[string]any{"server_url": "http://example.test/mcp"}, want: "public https URL"},
		{name: "localhost", tool: map[string]any{"server_url": "https://localhost/mcp"}, want: "localhost"},
		{name: "private IPv4", tool: map[string]any{"server_url": "https://127.0.0.1/mcp"}, want: "private IP"},
		{name: "userinfo", tool: map[string]any{"server_url": "https://user@example.test/mcp"}, want: "public https URL"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			err := p.SetResponsesConfig(config.ResponsesConfig{HostedTools: config.ResponsesHostedToolsConfig{RemoteMCP: []map[string]any{check.tool}}})
			if err == nil || !strings.Contains(err.Error(), check.want) {
				t.Fatalf("error = %v, want %q", err, check.want)
			}
		})
	}
	if err := p.SetResponsesConfig(config.ResponsesConfig{HostedTools: config.ResponsesHostedToolsConfig{RemoteMCP: []map[string]any{
		{"connector_id": "connector_123"}, {"server_url": "https://mcp.example.test/endpoint"},
	}}}); err != nil {
		t.Fatalf("valid remote MCP config: %v", err)
	}
}

func TestRemoteMCPDNSPreflightIsConfirmatoryAndAvailabilityFirst(t *testing.T) {
	privateLookup := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.8")}, nil
	}
	if err := validateRemoteMCPServerURLWithLookup("https://mcp.example.test/endpoint", privateLookup); err == nil || !strings.Contains(err.Error(), "private IP") {
		t.Fatalf("private DNS result accepted: %v", err)
	}
	publicLookup := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	if err := validateRemoteMCPServerURLWithLookup("https://mcp.example.test/endpoint", publicLookup); err != nil {
		t.Fatalf("public DNS result rejected: %v", err)
	}
	failingLookup := func(context.Context, string) ([]net.IP, error) {
		return nil, fmt.Errorf("temporary DNS failure")
	}
	if err := validateRemoteMCPServerURLWithLookup("https://mcp.example.test/endpoint", failingLookup); err != nil {
		t.Fatalf("transient DNS failure should preserve availability: %v", err)
	}
}

func TestResponsesConfigRemoteMCPDefaultsToApproval(t *testing.T) {
	tools := responsesConfigHostedTools(config.ResponsesHostedToolsConfig{RemoteMCP: []map[string]any{{
		"server_label": "docs", "server_url": "https://mcp.example.test",
	}}})
	if len(tools) != 1 || tools[0].Type != "mcp" || tools[0].Extra["require_approval"] != "always" {
		t.Fatalf("default remote MCP tool = %#v", tools)
	}
	explicit := responsesConfigHostedTools(config.ResponsesHostedToolsConfig{RemoteMCP: []map[string]any{{
		"server_label": "docs", "server_url": "https://mcp.example.test", "require_approval": "never",
	}}})
	if len(explicit) != 1 || explicit[0].Extra["require_approval"] != "never" {
		t.Fatalf("explicit remote MCP approval = %#v", explicit)
	}
}

func TestResponsesConfigHostedToolsOwnsDescriptorMaps(t *testing.T) {
	values := map[string]any{
		"type":    "web_search_preview",
		"filters": map[string]any{"country": "US"},
	}
	tools := responsesConfigHostedTools(config.ResponsesHostedToolsConfig{WebSearch: values})
	if len(tools) != 1 {
		t.Fatalf("hosted tools = %#v", tools)
	}
	values["type"] = "file_search"
	values["filters"].(map[string]any)["country"] = "CN"
	if tools[0].Type != "web_search_preview" {
		t.Fatalf("descriptor type mutated to %q", tools[0].Type)
	}
	filters, ok := tools[0].Extra["filters"].(map[string]any)
	if !ok || filters["country"] != "US" {
		t.Fatalf("descriptor nested map mutated: %#v", tools[0].Extra)
	}
}

func TestResponsesCodeInterpreterPolicyStaysLocal(t *testing.T) {
	values := map[string]any{
		"type":  "code_interpreter",
		"mothx": map[string]any{"maxCalls": 3, "timeoutSecs": 60},
	}
	tools := responsesConfigHostedTools(config.ResponsesHostedToolsConfig{CodeInterpreter: values})
	if len(tools) != 1 || tools[0].Type != "code_interpreter" {
		t.Fatalf("code interpreter tools = %#v", tools)
	}
	if _, present := tools[0].Extra["mothx"]; present {
		t.Fatalf("private MothX policy leaked upstream: %#v", tools[0].Extra)
	}
	policies, err := responsesHostedPoliciesWithError(config.ResponsesHostedToolsConfig{CodeInterpreter: values})
	if err != nil || policies["code_interpreter"].MaxCalls != 3 || policies["code_interpreter"].Timeout != time.Minute {
		t.Fatalf("policies = %#v err=%v", policies, err)
	}
	for _, invalid := range []map[string]any{
		{"maxCalls": -1}, {"maxCalls": 1.5}, {"timeoutSecs": -1}, {"timeoutSecs": 86401},
	} {
		if _, err := responsesHostedPoliciesWithError(config.ResponsesHostedToolsConfig{CodeInterpreter: map[string]any{"mothx": invalid}}); err == nil {
			t.Fatalf("invalid policy accepted: %#v", invalid)
		}
	}
	zeroPolicies, err := responsesHostedPoliciesWithError(config.ResponsesHostedToolsConfig{CodeInterpreter: map[string]any{"mothx": map[string]any{"maxCalls": 0}}})
	if err != nil || !zeroPolicies["code_interpreter"].MaxCallsSet {
		t.Fatalf("zero quota was not retained: %#v err=%v", zeroPolicies, err)
	}
}

func TestResponsesCodeInterpreterQuotaStopsSSE(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"ci-1","type":"code_interpreter_call","status":"completed","container_id":"container-1"}}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"id":"ci-2","type":"code_interpreter_call","status":"completed","container_id":"container-1"}}`,
		`data: {"type":"response.completed","response":{"id":"resp-quota","status":"completed"}}`,
		`data: [DONE]`,
	}, "\n\n") + "\n"
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "responses-quota"}}, sse, make(chan string, 1), nil)
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{HostedTools: config.ResponsesHostedToolsConfig{
		CodeInterpreter: map[string]any{"mothx": map[string]any{"maxCalls": 1}},
	}}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	events := chatAndCollect(t, p, provider.ChatParams{ModelID: "responses-quota", Messages: []provider.Message{provider.NewUserMessage("run code")}, Abort: make(chan struct{})})
	var found bool
	for _, event := range events {
		if event.Type == provider.StreamError && event.Error != nil && strings.Contains(event.Error.Error(), "quota exceeded") {
			found = true
		}
	}
	if !found {
		t.Fatalf("quota error missing from events: %#v", events)
	}
}

func TestOpenAIResponsesCapabilityProfileGatesExplicitFields(t *testing.T) {
	falseValue := false
	model := &provider.Model{ID: "responses-limited", Compat: &provider.ModelCompat{
		SupportsBackground:        &falseValue,
		SupportsConversation:      &falseValue,
		SupportsParallelToolCalls: &falseValue,
		SupportsToolChoice:        &falseValue,
		SupportsHostedTools:       map[string]bool{"web_search_preview": false},
		SupportedInclude:          []string{"reasoning.encrypted_content"},
	}}
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{model})
	p.SetUseResponsesAPI(true)
	if _, ok := p.ResponsesCapabilityReport(model.ID).HostedPolicies["code_interpreter"]; !ok {
		t.Fatal("code interpreter hosted policy capability missing")
	}
	checks := []struct {
		name string
		cfg  config.ResponsesConfig
		want string
	}{
		{name: "conversation", cfg: config.ResponsesConfig{StateMode: "conversation", Store: config.BoolPtr(true), Conversation: "conv"}, want: "conversation state"},
		{name: "include", cfg: config.ResponsesConfig{Include: []string{"file_search_call.results"}}, want: "include"},
		{name: "parallel", cfg: config.ResponsesConfig{ToolControl: config.ResponsesToolControlConfig{Parallel: config.BoolPtr(false)}}, want: "parallel tool calls"},
		{name: "tool choice", cfg: config.ResponsesConfig{ToolControl: config.ResponsesToolControlConfig{Choice: "required"}}, want: "tool_choice"},
		{name: "hosted tool", cfg: config.ResponsesConfig{HostedTools: config.ResponsesHostedToolsConfig{WebSearch: map[string]any{"type": "web_search_preview"}}}, want: "hosted tool"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := p.SetResponsesConfig(check.cfg); err != nil {
				t.Fatalf("set config: %v", err)
			}
			events := chatAndCollect(t, p, provider.ChatParams{ModelID: model.ID, Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})})
			if len(events) != 1 || events[0].Type != provider.StreamError || events[0].Error == nil || !strings.Contains(events[0].Error.Error(), check.want) {
				t.Fatalf("events = %#v, want capability error containing %q", events, check.want)
			}
		})
	}
}

func TestResponsesHostedToolRegistryIsCodecScoped(t *testing.T) {
	if len(responsesHostedToolRegistry) == 0 || len(responsesHostedItemTypes) != len(responsesHostedToolRegistry) {
		t.Fatalf("hosted registry and item type list diverged: %d vs %d", len(responsesHostedToolRegistry), len(responsesHostedItemTypes))
	}
	for _, itemType := range responsesHostedItemTypes {
		descriptor, ok := hostedToolDescriptorForType(itemType)
		if !ok || descriptor.ExecutionMode == "" || descriptor.Capability == "" {
			t.Fatalf("missing hosted descriptor for %q: %#v", itemType, descriptor)
		}
	}
	if capabilities := hostedRequestCapabilities(); !capabilities["web_search_preview"] || !capabilities["code_interpreter"] {
		t.Fatalf("known hosted request types missing from registry: %#v", capabilities)
	}
	if _, ok := hostedToolDescriptorForType("future_gateway_item"); ok {
		t.Fatal("unknown hosted item unexpectedly acquired a descriptor")
	}
}

func TestOpenAIResponsesAPIBackgroundFallsBackToSynchronousChat(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "responses-test"}}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)
	p.SetResponsesConfig(config.ResponsesConfig{Background: config.BoolPtr(true)})

	events := chatAndCollect(t, p, provider.ChatParams{
		ModelID:  "responses-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})
	for _, event := range events {
		if event.Type == provider.StreamError {
			t.Fatalf("unexpected synchronous fallback error: %v", event.Error)
		}
	}
	var body map[string]any
	select {
	case raw := <-bodyCh:
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
	default:
		t.Fatal("no request body captured")
	}
	if body["background"] != nil {
		t.Fatalf("background = %#v, want omitted for synchronous fallback", body["background"])
	}
}

func TestOpenAIResponsesAPIHostedWebSearchTool(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "responses-test"}}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)

	params := provider.ChatParams{
		ModelID:  "responses-test",
		Messages: []provider.Message{provider.NewUserMessage("latest news?")},
		Tools: []provider.ToolDefinition{
			{Name: "web_search", Kind: "hosted", Provider: "gpt", ProviderType: "responses"},
		},
		Abort: make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	tools, ok := raw["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one hosted tool", raw["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool = %#v, want object", tools[0])
	}
	if tool["type"] != "web_search" {
		t.Fatalf("tool.type = %#v, want web_search", tool["type"])
	}
	if _, ok := tool["name"]; ok {
		t.Fatalf("hosted web search should not include function name: %#v", tool)
	}
}

func TestConvertResponsesToolsDeduplicatesConfiguredAndNativeWebSearch(t *testing.T) {
	p := &Provider{}
	tools := p.convertResponsesTools([]provider.ToolDefinition{
		{Name: provider.HostedToolWebSearch, Kind: "hosted", ProviderType: "openai-responses"},
		{Name: provider.HostedToolOpenAIResponsesWebSearch, Kind: "hosted", ProviderType: "openai-responses"},
	})
	if len(tools) != 1 || tools[0].Type != provider.HostedToolWebSearch {
		t.Fatalf("tools = %#v, want one web_search tool", tools)
	}
}

func TestOpenAIResponsesAPIStreamToolCall(t *testing.T) {
	lines := []string{
		`{"type":"response.output_text.delta","delta":"Working"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"call_1","delta":"{\"command\":"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"call_1","delta":"\"echo hi\"}"}`,
		`{"type":"response.output_item.done","item":{"id":"call_1","type":"function_call","call_id":"call_1","name":"bash"}}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"output_tokens":5,"total_tokens":105,"input_tokens_details":{"cached_tokens":75},"output_tokens_details":{"reasoning_tokens":3}}}}`,
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("data: [DONE]\n")

	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock", Reasoning: true}}, b.String(), nil, nil)
	p.SetUseResponsesAPI(true)

	params := provider.ChatParams{
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	}
	var events []provider.StreamEvent
	for e := range p.Chat(context.Background(), params) {
		events = append(events, e)
	}
	if len(events) == 0 {
		t.Fatal("no events returned")
	}

	var (
		gotText  string
		gotTool  *provider.ToolCallBlock
		gotUsage *provider.Usage
		gotDone  bool
	)
	for _, e := range events {
		switch e.Type {
		case provider.StreamTextDelta:
			gotText += e.TextDelta
		case provider.StreamToolCall:
			gotTool = e.ToolCall
		case provider.StreamUsage:
			gotUsage = e.Usage
		case provider.StreamDone:
			gotDone = true
		}
	}
	if gotText != "Working" {
		t.Fatalf("text = %q, want Working", gotText)
	}
	if gotTool == nil {
		t.Fatal("missing StreamToolCall event")
	}
	if gotTool.ID != "call_1" {
		t.Fatalf("tool ID = %q, want call_1", gotTool.ID)
	}
	if gotTool.Name != "bash" {
		t.Fatalf("tool name = %q, want bash", gotTool.Name)
	}
	if string(gotTool.Arguments) != "{\"command\":\"echo hi\"}" {
		t.Fatalf("tool args = %q, want %q", string(gotTool.Arguments), "{\"command\":\"echo hi\"}")
	}
	if gotUsage == nil || gotUsage.CacheRead != 75 {
		t.Fatalf("usage = %#v, want cacheRead 75", gotUsage)
	}
	if gotUsage.Reasoning != 3 {
		t.Fatalf("usage reasoning = %d, want 3", gotUsage.Reasoning)
	}
	if !gotDone {
		t.Fatal("missing StreamDone event")
	}
}

func TestOpenAIResponsesAPISupportsDoneOnlyTextEvent(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.done\",\"text\":\"done-only\"}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n" +
		"data: [DONE]\n"
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	p.SetUseResponsesAPI(true)
	var text strings.Builder
	for event := range p.Chat(context.Background(), provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hello")}}) {
		if event.Type == provider.StreamTextDelta {
			text.WriteString(event.TextDelta)
		}
	}
	if text.String() != "done-only" {
		t.Fatalf("text = %q, want done-only", text.String())
	}
}

func TestOpenAIResponsesAPISupportsDoneOnlyRefusalEvent(t *testing.T) {
	sse := "data: {\"type\":\"response.refusal.done\",\"refusal\":\"cannot comply\"}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n"
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	p.SetUseResponsesAPI(true)
	var text strings.Builder
	for event := range p.Chat(context.Background(), provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hello")}}) {
		if event.Type == provider.StreamTextDelta {
			text.WriteString(event.TextDelta)
		}
	}
	if text.String() != "cannot comply" {
		t.Fatalf("refusal text = %q, want cannot comply", text.String())
	}
}

func TestOpenAIResponsesAPISupportsDoneOnlyReasoningEvent(t *testing.T) {
	sse := "data: {\"type\":\"response.reasoning_summary_text.done\",\"text\":\"think-only\"}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n"
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock", Reasoning: true}}, sse, nil, nil)
	p.SetUseResponsesAPI(true)
	var reasoning strings.Builder
	for event := range p.Chat(context.Background(), provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hello")}, ThinkingLevel: provider.ThinkingLow}) {
		if event.Type == provider.StreamThinkDelta {
			reasoning.WriteString(event.ThinkDelta)
		}
	}
	if reasoning.String() != "think-only" {
		t.Fatalf("reasoning text = %q, want think-only", reasoning.String())
	}
}

// ─── standard OpenAI SSE scenarios ───────────────────────────────────────────

// TestOpenAICache_CacheHit: final SSE chunk carries full usage with cached tokens.
func TestOpenAICache_CacheHit(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n" +
		"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":null}]}\n" +
		"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":5,\"total_tokens\":1005,\"prompt_tokens_details\":{\"cached_tokens\":750}}}\n" +
		"data: [DONE]\n"

	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	if u.Input != 1000 {
		t.Errorf("Input = %d, want 1000", u.Input)
	}
	if u.Output != 5 {
		t.Errorf("Output = %d, want 5", u.Output)
	}
	if u.CacheRead != 750 {
		t.Errorf("CacheRead = %d, want 750", u.CacheRead)
	}
	if got, want := u.CacheInfo(), "Cache: 75%"; got != want {
		t.Errorf("CacheInfo() = %q, want %q", got, want)
	}
}

// TestOpenAICache_NoCache: usage chunk present but no cached tokens.
func TestOpenAICache_NoCache(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n" +
		"data: {\"id\":\"chatcmpl-2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":200,\"completion_tokens\":8,\"total_tokens\":208}}\n" +
		"data: [DONE]\n"

	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	if u.Input != 200 {
		t.Errorf("Input = %d, want 200", u.Input)
	}
	if u.CacheRead != 0 {
		t.Errorf("CacheRead = %d, want 0", u.CacheRead)
	}
	if got, want := u.CacheInfo(), "Cache: 0%"; got != want {
		t.Errorf("CacheInfo() = %q, want %q", got, want)
	}
}

// TestOpenAICache_100Pct: all input tokens are cached.
func TestOpenAICache_100Pct(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-3\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Full\"},\"finish_reason\":null}]}\n" +
		"data: {\"id\":\"chatcmpl-3\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":500,\"completion_tokens\":4,\"total_tokens\":504,\"prompt_tokens_details\":{\"cached_tokens\":500}}}\n" +
		"data: [DONE]\n"

	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	if u.CacheRead != 500 {
		t.Errorf("CacheRead = %d, want 500", u.CacheRead)
	}
	if got, want := u.CacheInfo(), "Cache: 100%"; got != want {
		t.Errorf("CacheInfo() = %q, want %q", got, want)
	}
}

// ─── proxy-compatibility scenarios ───────────────────────────────────────────

// TestOpenAICache_ProxyFirstChunkHasUsage: some proxies send usage in an early
// chunk rather than the final one. The first-seen values must be kept.
func TestOpenAICache_ProxyFirstChunkHasUsage(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-4\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hey\"},\"finish_reason\":null}],\"usage\":{\"prompt_tokens\":800,\"completion_tokens\":3,\"total_tokens\":803,\"prompt_tokens_details\":{\"cached_tokens\":600}}}\n" +
		"data: {\"id\":\"chatcmpl-4\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n" +
		"data: [DONE]\n"

	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	if u.Input != 800 {
		t.Errorf("Input = %d, want 800", u.Input)
	}
	if u.CacheRead != 600 {
		t.Errorf("CacheRead = %d, want 600", u.CacheRead)
	}
	if got, want := u.CacheInfo(), "Cache: 75%"; got != want {
		t.Errorf("CacheInfo() = %q, want %q", got, want)
	}
}

// TestOpenAICache_ProxyFirstWinsOnConflict: if two chunks carry usage with
// different values for the same field, the first chunk's value must win.
func TestOpenAICache_ProxyFirstWinsOnConflict(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-5\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"A\"},\"finish_reason\":null}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":6,\"total_tokens\":1006,\"prompt_tokens_details\":{\"cached_tokens\":750}}}\n" +
		// Second chunk has different (wrong) values — must be ignored
		"data: {\"id\":\"chatcmpl-5\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":999,\"completion_tokens\":99,\"total_tokens\":1098,\"prompt_tokens_details\":{\"cached_tokens\":800}}}\n" +
		"data: [DONE]\n"

	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	if u.Input != 1000 {
		t.Errorf("Input = %d, want 1000 (first chunk wins)", u.Input)
	}
	if u.Output != 6 {
		t.Errorf("Output = %d, want 6 (first chunk wins)", u.Output)
	}
	if u.CacheRead != 750 {
		t.Errorf("CacheRead = %d, want 750 (first chunk wins)", u.CacheRead)
	}
	if got, want := u.CacheInfo(), "Cache: 75%"; got != want {
		t.Errorf("CacheInfo() = %q, want %q", got, want)
	}
}

// TestOpenAICache_ProxySplitUsage: first chunk has prompt/completion counts
// but no cache details; a later chunk fills in the cache details.
// The first-wins rule applies per-field: the later chunk's cache value fills
// the zero CacheRead.
func TestOpenAICache_ProxySplitUsage(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-6\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"B\"},\"finish_reason\":null}],\"usage\":{\"prompt_tokens\":400,\"completion_tokens\":7,\"total_tokens\":407}}\n" +
		// Second chunk has only cache details (no prompt/completion override since those are non-zero)
		"data: {\"id\":\"chatcmpl-6\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0,\"total_tokens\":0,\"prompt_tokens_details\":{\"cached_tokens\":300}}}\n" +
		"data: [DONE]\n"

	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	u := mustUsage(t, chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}))

	if u.Input != 400 {
		t.Errorf("Input = %d, want 400 (first chunk)", u.Input)
	}
	if u.Output != 7 {
		t.Errorf("Output = %d, want 7 (first chunk)", u.Output)
	}
	if u.CacheRead != 300 {
		t.Errorf("CacheRead = %d, want 300 (second chunk fills zero)", u.CacheRead)
	}
	// 300/400 = 75%
	if got, want := u.CacheInfo(), "Cache: 75%"; got != want {
		t.Errorf("CacheInfo() = %q, want %q", got, want)
	}
}

func TestOpenAIToolCall_MissingIDGetsFallback(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-tool-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\"}}]},\"finish_reason\":null}]}\n" +
		"data: {\"id\":\"chatcmpl-tool-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"type\":\"function\",\"function\":{\"arguments\":\"\\\"echo hi\\\"}\"}}]},\"finish_reason\":null}]}\n" +
		"data: {\"id\":\"chatcmpl-tool-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n" +
		"data: [DONE]\n"

	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	events := chatAndCollect(t, p, provider.ChatParams{Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})})

	var got *provider.ToolCallBlock
	for _, e := range events {
		if e.Type == provider.StreamToolCall && e.ToolCall != nil {
			got = e.ToolCall
			break
		}
	}
	if got == nil {
		t.Fatal("expected StreamToolCall event")
	}
	if got.ID == "" {
		t.Fatal("ToolCall.ID is empty, want fallback ID")
	}
	if !strings.HasPrefix(got.ID, "openai_toolcall_") {
		t.Fatalf("ToolCall.ID = %q, want prefix 'openai_toolcall_'", got.ID)
	}
	if got.Name != "bash" {
		t.Fatalf("ToolCall.Name = %q, want %q", got.Name, "bash")
	}
	if string(got.Arguments) != "{\"command\":\"echo hi\"}" {
		t.Fatalf("ToolCall.Arguments = %q, want %q", string(got.Arguments), "{\"command\":\"echo hi\"}")
	}
}

func TestOpenAIToolCall_AcceptsObjectArguments(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-tool-object\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_write\",\"type\":\"function\",\"function\":{\"name\":\"write\",\"arguments\":{\"path\":\"internal/raft/node.go\",\"content\":\"package raft\\n\"}}}]},\"finish_reason\":\"tool_calls\"}]}\n" +
		"data: [DONE]\n"

	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	events := chatAndCollect(t, p, provider.ChatParams{
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})

	var got *provider.ToolCallBlock
	for _, e := range events {
		if e.Type == provider.StreamToolCall {
			got = e.ToolCall
			break
		}
	}
	if got == nil {
		t.Fatal("expected StreamToolCall event")
	}
	if got.ID != "call_write" || got.Name != "write" {
		t.Fatalf("tool call = %#v, want write call_write", got)
	}
	if string(got.Arguments) != `{"path":"internal/raft/node.go","content":"package raft\n"}` {
		t.Fatalf("ToolCall.Arguments = %q", string(got.Arguments))
	}
}

func TestOpenAIResponsesAPICompatDisablesOptionalParams(t *testing.T) {
	bodyCh := make(chan string, 1)
	no := false
	p := newMockOpenAIProvider(t, []*provider.Model{{
		ID:        "responses-test",
		Reasoning: true,
		Compat: &provider.ModelCompat{
			SupportsPromptCacheKey:   &no,
			SupportsReasoningSummary: &no,
		},
	}}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)

	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:       "responses-test",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingHigh,
		Abort:         make(chan struct{}),
	}) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if _, ok := raw["prompt_cache_key"]; ok {
		t.Fatalf("prompt_cache_key present despite compat flag: %#v", raw)
	}
	reasoning, ok := raw["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want object", raw["reasoning"])
	}
	if _, ok := reasoning["summary"]; ok {
		t.Fatalf("reasoning.summary present despite compat flag: %#v", reasoning)
	}
}

func TestOpenAIResponsesAPILongCacheRetentionCompat(t *testing.T) {
	no := false
	p := newMockOpenAIProvider(t, []*provider.Model{{
		ID: "responses-test",
		Compat: &provider.ModelCompat{
			SupportsLongCacheRetention: &no,
		},
	}}, "data: [DONE]\n", nil, nil)
	p.SetUseResponsesAPI(true)
	p.SetResponsesConfig(config.ResponsesConfig{PromptCacheRetention: "24h"})

	events := chatAndCollect(t, p, provider.ChatParams{
		ModelID:  "responses-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})
	if len(events) != 1 || events[0].Type != provider.StreamError {
		t.Fatalf("events = %#v, want one capability error", events)
	}
	if !strings.Contains(events[0].Error.Error(), "prompt_cache_retention") {
		t.Fatalf("error = %v, want prompt_cache_retention capability diagnostic", events[0].Error)
	}
}

func TestOpenAIResponsesAPIPromptCacheCanBeDisabled(t *testing.T) {
	bodyCh := make(chan string, 1)
	no := false
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "responses-test", Reasoning: true}}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)
	p.SetResponsesConfig(config.ResponsesConfig{PromptCacheEnabled: &no})

	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:       "responses-test",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingHigh,
		Abort:         make(chan struct{}),
	}) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if _, ok := raw["prompt_cache_key"]; ok {
		t.Fatalf("prompt_cache_key present despite disabled cache: %#v", raw)
	}
}

func TestOpenAIResponsesAPINoReasoningWhenOff(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "responses-test", Reasoning: true}}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)

	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:       "responses-test",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingOff,
		Abort:         make(chan struct{}),
	}) {
	}

	var raw map[string]any
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if _, ok := raw["reasoning"]; ok {
		t.Fatalf("reasoning present despite thinking off: %#v", raw)
	}
}

func TestOpenAIResponsesAPIStreamFailureNestedResponseError(t *testing.T) {
	// Some OpenAI-compatible servers (e.g. Kimi) nest the failure reason inside
	// the response object rather than the top-level error field.
	sse := "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"maximum context length exceeded\",\"code\":\"context_length_exceeded\"}}}\n"
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	p.SetUseResponsesAPI(true)

	events := chatAndCollect(t, p, provider.ChatParams{
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})
	for _, e := range events {
		if e.Type == provider.StreamError {
			if e.Error == nil || !strings.Contains(e.Error.Error(), "maximum context length exceeded") {
				t.Fatalf("error = %v, want nested response error detail", e.Error)
			}
			if !provider.IsContextOverflowError(e.Error) {
				t.Fatalf("error %v should classify as context overflow", e.Error)
			}
			return
		}
	}
	t.Fatal("missing StreamError event")
}

func TestOpenAIResponsesAPIStreamFailure(t *testing.T) {
	sse := "data: {\"type\":\"response.failed\",\"error\":{\"message\":\"bad request\"}}\n"
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "mock"}}, sse, nil, nil)
	p.SetUseResponsesAPI(true)

	events := chatAndCollect(t, p, provider.ChatParams{
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})
	for _, e := range events {
		if e.Type == provider.StreamError {
			if e.Error == nil || !strings.Contains(e.Error.Error(), "bad request") {
				t.Fatalf("error = %v, want bad request", e.Error)
			}
			return
		}
	}
	t.Fatal("missing StreamError event")
}

func TestOpenAIResponsesRetriesStreamReadError(t *testing.T) {
	attempts := 0
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "mock"}})
	p.SetUseResponsesAPI(true)
	p.SetRetryConfig(&provider.RetryConfig{Enabled: true, MaxRetries: 1, BaseDelayMs: 1})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		body := "data: {\"type\":\"error\",\"error\":{\"message\":\"stream_read_error\"}}\n"
		if attempts > 1 {
			body = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered\"}\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n"
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}

	events := chatAndCollect(t, p, provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	var sawRetry, sawText, sawDone bool
	for _, event := range events {
		switch event.Type {
		case provider.StreamRetry:
			sawRetry = true
		case provider.StreamTextDelta:
			sawText = sawText || event.TextDelta == "recovered"
		case provider.StreamDone:
			sawDone = true
		case provider.StreamError:
			t.Fatalf("unexpected StreamError: %v", event.Error)
		}
	}
	if !sawRetry || !sawText || !sawDone {
		t.Fatalf("events = %#v, want retry followed by recovered response", events)
	}
}

func TestOpenAIResponsesRetriesGenericStreamFailure(t *testing.T) {
	// A bare response.failed / error SSE event without error details becomes
	// the generic "responses stream failed" error. It must be retried when no
	// visible output was streamed yet.
	attempts := 0
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "mock"}})
	p.SetUseResponsesAPI(true)
	p.SetRetryConfig(&provider.RetryConfig{Enabled: true, MaxRetries: 1, BaseDelayMs: 1})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		body := "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n"
		if attempts > 1 {
			body = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered\"}\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n"
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}

	events := chatAndCollect(t, p, provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	var sawRetry, sawText, sawDone bool
	for _, event := range events {
		switch event.Type {
		case provider.StreamRetry:
			sawRetry = true
		case provider.StreamTextDelta:
			sawText = sawText || event.TextDelta == "recovered"
		case provider.StreamDone:
			sawDone = true
		case provider.StreamError:
			t.Fatalf("unexpected StreamError: %v", event.Error)
		}
	}
	if !sawRetry || !sawText || !sawDone {
		t.Fatalf("events = %#v, want retry followed by recovered response", events)
	}
}

func TestOpenAIResponsesAPICompletesOnResponseCompletedWithoutDone(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n"
	body := &blockingAfterBody{reader: strings.NewReader(sse), closed: make(chan struct{})}
	p := NewProviderWithModels("fake-key", "https://api.test/v1", []*provider.Model{{ID: "mock"}})
	p.SetUseResponsesAPI(true)
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
			Request:    r,
		}, nil
	})}

	done := make(chan []provider.StreamEvent, 1)
	go func() {
		done <- chatAndCollect(t, p, provider.ChatParams{
			ModelID:  "mock",
			Messages: []provider.Message{provider.NewUserMessage("hi")},
			Abort:    make(chan struct{}),
		})
	}()

	var events []provider.StreamEvent
	select {
	case events = <-done:
	case <-time.After(time.Second):
		body.Close()
		t.Fatal("stream did not finish after response.completed")
	}

	var sawText, sawUsage, sawDone bool
	for _, event := range events {
		switch event.Type {
		case provider.StreamTextDelta:
			sawText = sawText || event.TextDelta == "OK"
		case provider.StreamUsage:
			sawUsage = event.Usage != nil && event.Usage.TotalTokens == 2
		case provider.StreamDone:
			sawDone = event.StopReason == "stop"
		case provider.StreamError:
			t.Fatalf("unexpected StreamError: %v", event.Error)
		}
	}
	if !sawText {
		t.Fatal("missing text delta")
	}
	if !sawUsage {
		t.Fatal("missing usage")
	}
	if !sawDone {
		t.Fatal("missing StreamDone with stop reason")
	}
}

func TestOpenAIResponsesFactoryEnablesResponsesMode(t *testing.T) {
	p, err := provider.CreateProvider("openai-responses", &config.ProviderConfig{
		API:    "openai-responses",
		APIKey: "k",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	op, ok := p.(*Provider)
	if !ok {
		t.Fatalf("provider type = %T, want *Provider", p)
	}
	if !op.useResponsesAPI {
		t.Fatal("expected responses API mode to be enabled")
	}
}

func TestOpenAIParseReasoningInContent(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"<think>rea\"}}]}\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"soning</think>vis\"}}]}\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"ible\"},\"finish_reason\":\"stop\"}]}\n" +
		"data: [DONE]\n"
	models := []*provider.Model{{
		ID:        "mock",
		Reasoning: true,
		Compat:    &provider.ModelCompat{ParseReasoningInContent: true},
	}}
	p := newMockOpenAIProvider(t, models, sse, nil, nil)

	events := chatAndCollect(t, p, provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})

	var text, think string
	for _, e := range events {
		switch e.Type {
		case provider.StreamTextDelta:
			text += e.TextDelta
		case provider.StreamThinkDelta:
			think += e.ThinkDelta
		}
	}
	if think != "reasoning" {
		t.Fatalf("think = %q, want %q", think, "reasoning")
	}
	if text != "visible" {
		t.Fatalf("text = %q, want %q", text, "visible")
	}
}

func TestOpenAIParseReasoningInContentDisabled(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"<think>x</think>y\"},\"finish_reason\":\"stop\"}]}\n" +
		"data: [DONE]\n"
	models := []*provider.Model{{ID: "mock", Reasoning: true}}
	p := newMockOpenAIProvider(t, models, sse, nil, nil)

	events := chatAndCollect(t, p, provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	})

	var text, think string
	for _, e := range events {
		switch e.Type {
		case provider.StreamTextDelta:
			text += e.TextDelta
		case provider.StreamThinkDelta:
			think += e.ThinkDelta
		}
	}
	if think != "" {
		t.Fatalf("think = %q, want empty", think)
	}
	if text != "<think>x</think>y" {
		t.Fatalf("text = %q, want literal tags", text)
	}
}

// ─── sampling params suppression ─────────────────────────────────────────────

func captureOpenAIRequestBody(t *testing.T, p *Provider, params provider.ChatParams, bodyCh <-chan string) openAIRequest {
	t.Helper()
	for range p.Chat(context.Background(), params) {
	}
	var req openAIRequest
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

func TestOpenAIReasoningDropsSamplingParams(t *testing.T) {
	temp := 0.7
	topP := 0.9
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "gpt-reasoning", Reasoning: true}}, "data: [DONE]\n", bodyCh, nil)

	req := captureOpenAIRequestBody(t, p, provider.ChatParams{
		ModelID:       "gpt-reasoning",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingHigh,
		Temperature:   &temp,
		TopP:          &topP,
		Abort:         make(chan struct{}),
	}, bodyCh)

	if req.ReasoningEffort == "" {
		t.Fatal("reasoning_effort = \"\", want set")
	}
	if req.Temperature != nil {
		t.Fatalf("temperature = %#v, want nil (dropped for OpenAI reasoning models)", *req.Temperature)
	}
	if req.TopP != nil {
		t.Fatalf("top_p = %#v, want nil (dropped for OpenAI reasoning models)", *req.TopP)
	}
}

func TestOpenAIDeepSeekReasoningKeepsSamplingParams(t *testing.T) {
	temp := 0.7
	topP := 0.9
	allow := false
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{
		ID:        "deepseek-test",
		Reasoning: true,
		Compat:    &provider.ModelCompat{DisableSamplingParams: &allow},
	}}, "data: [DONE]\n", bodyCh, nil)
	p.baseURL = p.baseURL + "/deepseek"

	req := captureOpenAIRequestBody(t, p, provider.ChatParams{
		ModelID:       "deepseek-test",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingHigh,
		Temperature:   &temp,
		TopP:          &topP,
		Abort:         make(chan struct{}),
	}, bodyCh)

	if req.Temperature == nil || *req.Temperature != temp {
		t.Fatalf("temperature = %#v, want %v (deepseek format keeps sampling params)", req.Temperature, temp)
	}
	if req.TopP == nil || *req.TopP != topP {
		t.Fatalf("top_p = %#v, want %v (deepseek format keeps sampling params)", req.TopP, topP)
	}
}

func TestOpenAIDisableSamplingParamsCompat(t *testing.T) {
	temp := 0.7
	topP := 0.9
	disable := true
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{
		ID:     "no-sampling",
		Compat: &provider.ModelCompat{DisableSamplingParams: &disable},
	}}, "data: [DONE]\n", bodyCh, nil)

	req := captureOpenAIRequestBody(t, p, provider.ChatParams{
		ModelID:     "no-sampling",
		Messages:    []provider.Message{provider.NewUserMessage("hi")},
		Temperature: &temp,
		TopP:        &topP,
		Abort:       make(chan struct{}),
	}, bodyCh)

	if req.Temperature != nil {
		t.Fatalf("temperature = %#v, want nil (DisableSamplingParams)", *req.Temperature)
	}
	if req.TopP != nil {
		t.Fatalf("top_p = %#v, want nil (DisableSamplingParams)", *req.TopP)
	}
}

func TestOpenAIResponsesReasoningDropsSamplingParams(t *testing.T) {
	temp := 0.7
	topP := 0.9
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "gpt-5-test", Reasoning: true}}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)

	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:       "gpt-5-test",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		ThinkingLevel: provider.ThinkingHigh,
		Temperature:   &temp,
		TopP:          &topP,
		Abort:         make(chan struct{}),
	}) {
	}

	var req responsesRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}

	if req.Reasoning == nil {
		t.Fatal("reasoning = nil, want set")
	}
	if req.Temperature != nil {
		t.Fatalf("temperature = %#v, want nil (dropped for Responses reasoning models)", *req.Temperature)
	}
	if req.TopP != nil {
		t.Fatalf("top_p = %#v, want nil (dropped for Responses reasoning models)", *req.TopP)
	}
}

func TestConvertMessagesAudioVideoBlocks(t *testing.T) {
	p := &Provider{}
	messages := p.convertMessages(provider.ChatParams{Messages: []provider.Message{{
		Role: "user",
		Contents: []provider.ContentBlock{
			{Type: "text", Text: "listen and watch"},
			{Type: "audio", Audio: &provider.AudioContent{MimeType: "audio/wav", Format: "wav", Data: "YXVkaW8="}},
			{Type: "video", Video: &provider.VideoContent{MimeType: "video/mp4", Data: "dmlkZW8="}},
		},
	}}}, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	blocks, ok := messages[0].Content.([]openAIContentBlock)
	if !ok || len(blocks) != 3 {
		t.Fatalf("content = %#v, want three content blocks", messages[0].Content)
	}
	if blocks[1].Type != "input_audio" || blocks[1].InputAudio == nil ||
		blocks[1].InputAudio.Data != "data:audio/wav;base64,YXVkaW8=" || blocks[1].InputAudio.Format != "wav" {
		t.Fatalf("audio block = %#v", blocks[1])
	}
	if blocks[2].Type != "video_url" || blocks[2].VideoURL == nil ||
		blocks[2].VideoURL.URL != "data:video/mp4;base64,dmlkZW8=" {
		t.Fatalf("video block = %#v", blocks[2])
	}
}

func TestResponsesMessageContentAudioVideoBlocks(t *testing.T) {
	p := &Provider{}
	content := p.responsesMessageContent(provider.Message{Role: "user", Contents: []provider.ContentBlock{
		{Type: "text", Text: "analyze"},
		{Type: "audio", Audio: &provider.AudioContent{MimeType: "audio/mpeg", Data: "YXVkaW8="}},
		{Type: "video", Video: &provider.VideoContent{MimeType: "video/mp4", URL: "https://example.com/clip.mp4"}},
	}}, "input_text")
	blocks, ok := content.([]responsesContentBlock)
	if !ok || len(blocks) != 3 {
		t.Fatalf("content = %#v", content)
	}
	if blocks[1].Type != "input_audio" || blocks[1].AudioURL != "data:audio/mpeg;base64,YXVkaW8=" || blocks[1].Format != "mp3" {
		t.Fatalf("audio block = %#v", blocks[1])
	}
	if blocks[2].Type != "input_video" || blocks[2].VideoURL != "https://example.com/clip.mp4" {
		t.Fatalf("video block = %#v", blocks[2])
	}
}

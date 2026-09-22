package google

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

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
)

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

func newMockGoogleProvider(t *testing.T, p *Provider, sse string, bodyCh chan<- string, check func(*http.Request)) *Provider {
	t.Helper()
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

func chatAndCollect(t *testing.T, p *Provider, params provider.ChatParams) []provider.StreamEvent {
	t.Helper()
	var events []provider.StreamEvent
	for e := range p.Chat(context.Background(), params) {
		events = append(events, e)
	}
	return events
}

func TestGoogleRetriesEarlyStreamReadError(t *testing.T) {
	streamErr := errors.New("stream error: stream ID 19; INTERNAL_ERROR; received from peer")
	attempts := 0
	p := NewGeminiProviderWithModels("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", []*provider.Model{{ID: "mock"}})
	p.SetRetryConfig(&provider.RetryConfig{Enabled: true, MaxRetries: 1, BaseDelayMs: 1})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		var body io.ReadCloser
		if attempts == 1 {
			body = &errorAfterBody{r: strings.NewReader(""), err: streamErr}
		} else {
			body = io.NopCloser(strings.NewReader("data: [DONE]\n"))
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: r}, nil
	})}

	events := chatAndCollect(t, p, provider.ChatParams{ModelID: "mock", Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})})
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

func TestGoogleDoesNotRetryStreamReadErrorAfterVisibleOutput(t *testing.T) {
	streamErr := errors.New("stream error: stream ID 19; INTERNAL_ERROR; received from peer")
	attempts := 0
	p := NewGeminiProviderWithModels("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", []*provider.Model{{ID: "mock"}})
	p.SetRetryConfig(&provider.RetryConfig{Enabled: true, MaxRetries: 1, BaseDelayMs: 1})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       &errorAfterBody{r: strings.NewReader("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n"), err: streamErr},
			Request:    r,
		}, nil
	})}

	events := chatAndCollect(t, p, provider.ChatParams{ModelID: "mock", Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})})
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

func TestResolveAPIKeyShellCommandRequiresOptIn(t *testing.T) {
	t.Setenv("VIBECODING_ALLOW_SHELL_CONFIG", "")
	if got := resolveAPIKey(&config.ProviderConfig{APIKey: "!printf secret"}); got != "!printf secret" {
		t.Fatalf("resolveAPIKey without opt-in = %q, want literal", got)
	}

	t.Setenv("VIBECODING_ALLOW_SHELL_CONFIG", "1")
	if got := resolveAPIKey(&config.ProviderConfig{APIKey: "!printf secret"}); got != "secret" {
		t.Fatalf("resolveAPIKey with opt-in = %q, want secret", got)
	}
}

func TestGoogleProviderHTTPProxy(t *testing.T) {
	p, err := NewGeminiProviderWithModelsAndProxy("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", "http://127.0.0.1:7890", []*provider.Model{{ID: "m1"}})
	if err != nil {
		t.Fatalf("provider with proxy: %v", err)
	}
	transport, ok := p.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", p.client.Transport)
	}
	proxyURL, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "generativelanguage.googleapis.com"}})
	if err != nil {
		t.Fatalf("proxy lookup: %v", err)
	}
	if proxyURL == nil || proxyURL.String() != "http://127.0.0.1:7890" {
		t.Fatalf("proxy = %v, want http://127.0.0.1:7890", proxyURL)
	}
}

func TestConvertMessagesToolResultUsesTextContents(t *testing.T) {
	p := &Provider{}
	contents := p.convertMessages(provider.ChatParams{
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
	})

	if len(contents) != 1 || len(contents[0].Parts) != 1 || contents[0].Parts[0].FunctionResponse == nil {
		t.Fatalf("contents = %#v, want one function response", contents)
	}
	got := contents[0].Parts[0].FunctionResponse.Response["content"]
	if got != "bash output from content block" {
		t.Fatalf("function response content = %#v, want text content from content block", got)
	}
}

func TestConvertMessagesGroupsConsecutiveToolResults(t *testing.T) {
	p := &Provider{}
	contents := p.convertMessages(provider.ChatParams{
		Messages: []provider.Message{
			provider.NewAssistantMessage([]provider.ContentBlock{
				{
					Type: "toolCall",
					ToolCall: &provider.ToolCallBlock{
						ID:        "call_1",
						Name:      "read",
						Arguments: json.RawMessage(`{"path":"main.go"}`),
					},
				},
				{
					Type: "toolCall",
					ToolCall: &provider.ToolCallBlock{
						ID:        "call_2",
						Name:      "bash",
						Arguments: json.RawMessage(`{"cmd":"pwd"}`),
					},
				},
			}),
			provider.NewToolResultMessage("call_1", "read", "file content", false),
			provider.NewToolResultMessage("call_2", "bash", "workdir", false),
			provider.NewUserMessage("next"),
		},
	})

	if len(contents) != 3 {
		t.Fatalf("len(contents) = %d, want 3: %#v", len(contents), contents)
	}
	if contents[1].Role != "user" {
		t.Fatalf("tool result role = %q, want user", contents[1].Role)
	}
	if len(contents[1].Parts) != 2 {
		t.Fatalf("tool result parts = %d, want 2: %#v", len(contents[1].Parts), contents[1].Parts)
	}
	first := contents[1].Parts[0].FunctionResponse
	second := contents[1].Parts[1].FunctionResponse
	if first == nil || first.Name != "read" || first.Response["content"] != "file content" {
		t.Fatalf("first function response = %#v, want read file content", first)
	}
	if second == nil || second.Name != "bash" || second.Response["content"] != "workdir" {
		t.Fatalf("second function response = %#v, want bash workdir", second)
	}
	if contents[2].Parts[0].Text != "next" {
		t.Fatalf("message after tool results = %#v, want next user message", contents[2])
	}
}

func TestConvertMessagesPreservesGoogleFunctionCallIDs(t *testing.T) {
	p := &Provider{}
	contents := p.convertMessages(provider.ChatParams{
		Messages: []provider.Message{
			provider.NewAssistantMessage([]provider.ContentBlock{
				{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{"key":"a"}`)}},
				{Type: "toolCall", ToolCall: &provider.ToolCallBlock{ID: "call-2", Name: "lookup", Arguments: json.RawMessage(`{"key":"b"}`)}},
			}),
			provider.NewToolResultMessage("call-1", "lookup", "value", false),
			provider.NewToolResultMessage("call-2", "lookup", "value", false),
		},
	})

	if got := contents[0].Parts[0].FunctionCall; got == nil || got.ID != "call-1" {
		t.Fatalf("first function call = %#v, want call-1", contents[0].Parts[0].FunctionCall)
	}
	if got := contents[0].Parts[1].FunctionCall; got == nil || got.ID != "call-2" {
		t.Fatalf("second function call = %#v, want call-2", contents[0].Parts[1].FunctionCall)
	}
	if got := contents[1].Parts[0].FunctionResponse; got == nil || got.ID != "call-1" {
		t.Fatalf("first function response = %#v, want call-1", contents[1].Parts[0].FunctionResponse)
	}
	if got := contents[1].Parts[1].FunctionResponse; got == nil || got.ID != "call-2" {
		t.Fatalf("second function response = %#v, want call-2", contents[1].Parts[1].FunctionResponse)
	}

	fallback := p.convertMessages(provider.ChatParams{Messages: []provider.Message{
		provider.NewAssistantMessage([]provider.ContentBlock{{
			Type:     "toolCall",
			ToolCall: &provider.ToolCallBlock{ID: "google_toolcall_9", Name: "lookup", Arguments: json.RawMessage(`{}`)},
		}}),
		provider.NewToolResultMessage("google_toolcall_9", "lookup", "value", false),
	}})
	if got := fallback[0].Parts[0].FunctionCall; got == nil || got.ID != "" {
		t.Fatalf("fallback function call = %#v, want omitted ID", got)
	}
	if got := fallback[1].Parts[0].FunctionResponse; got == nil || got.ID != "" {
		t.Fatalf("fallback function response = %#v, want omitted ID", got)
	}
}

func TestGoogleCustomHeaders(t *testing.T) {
	p := newMockGoogleProvider(t,
		NewGeminiProviderWithModels("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", []*provider.Model{{ID: "gemini-test"}}),
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n",
		nil,
		func(r *http.Request) {
			if r.Header.Get("X-Custom-Header") != "custom-value" {
				t.Fatalf("X-Custom-Header = %q, want custom-value", r.Header.Get("X-Custom-Header"))
			}
			if r.Header.Get("x-goog-api-key") != "override-key" {
				t.Fatalf("x-goog-api-key = %q, want override-key", r.Header.Get("x-goog-api-key"))
			}
		})
	p.SetHeaders(map[string]string{
		"X-Custom-Header": "custom-value",
		"x-goog-api-key":  "override-key",
	})

	params := provider.ChatParams{
		ModelID:  "gemini-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}
}

func TestGoogleStreamMultipleFunctionCallsPreservesIDs(t *testing.T) {
	sse := "data: {\"candidates\":[{\"content\":{\"parts\":[" +
		"{\"functionCall\":{\"id\":\"call-1\",\"name\":\"lookup\",\"args\":{\"key\":\"a\"}}}," +
		"{\"functionCall\":{\"id\":\"call-2\",\"name\":\"lookup\",\"args\":{\"key\":\"b\"}}}]},\"finishReason\":\"STOP\"}]}\n"
	for _, tc := range []struct {
		name string
		p    *Provider
	}{
		{name: "gemini", p: NewGeminiProviderWithModels("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", []*provider.Model{{ID: "mock"}})},
		{name: "vertex", p: NewVertexProviderWithModels("fake-key", "https://aiplatform.googleapis.com/v1/publishers/google/models", []*provider.Model{{ID: "mock"}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newMockGoogleProvider(t, tc.p, sse, nil, nil)
			var calls []*provider.ToolCallBlock
			for _, event := range chatAndCollect(t, p, provider.ChatParams{ModelID: "mock", Messages: []provider.Message{provider.NewUserMessage("hi")}, Abort: make(chan struct{})}) {
				if event.Type == provider.StreamToolCall {
					calls = append(calls, event.ToolCall)
				}
			}
			if len(calls) != 2 {
				t.Fatalf("tool calls = %d, want 2", len(calls))
			}
			if calls[0].ID != "call-1" || calls[1].ID != "call-2" {
				t.Fatalf("call IDs = %q, %q, want call-1/call-2", calls[0].ID, calls[1].ID)
			}
			if string(calls[0].Arguments) != `{"key":"a"}` || string(calls[1].Arguments) != `{"key":"b"}` {
				t.Fatalf("call args = %s, %s", calls[0].Arguments, calls[1].Arguments)
			}
		})
	}
}

func TestGoogleGeminiRequest(t *testing.T) {
	bodyCh := make(chan string, 1)
	allowSampling := false
	p := newMockGoogleProvider(t,
		NewGeminiProviderWithModels("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", []*provider.Model{{
			ID:        "gemini-test",
			Reasoning: true,
			Compat:    &provider.ModelCompat{DisableSamplingParams: &allowSampling},
		}}),
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n",
		bodyCh,
		func(r *http.Request) {
			if r.URL.Path != "/v1beta/models/gemini-test:streamGenerateContent" {
				t.Fatalf("path = %q, want /v1beta/models/gemini-test:streamGenerateContent", r.URL.Path)
			}
			if r.URL.Query().Get("alt") != "sse" {
				t.Fatalf("alt query = %q, want sse", r.URL.Query().Get("alt"))
			}
			if r.Header.Get("x-goog-api-key") != "fake-key" {
				t.Fatalf("x-goog-api-key = %q, want fake-key", r.Header.Get("x-goog-api-key"))
			}
		})

	temp := 0.2
	params := provider.ChatParams{
		ModelID:       "gemini-test",
		SystemPrompt:  "system",
		Messages:      []provider.Message{provider.NewUserMessage("hi")},
		Tools:         []provider.ToolDefinition{{Name: "read", Description: "Read file", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ThinkingLevel: provider.ThinkingHigh,
		MaxTokens:     123,
		Temperature:   &temp,
		Abort:         make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req googleRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if req.SystemInstruction == nil || req.SystemInstruction.Parts[0].Text != "system" {
		t.Fatalf("systemInstruction = %#v, want system text", req.SystemInstruction)
	}
	if len(req.Contents) != 1 || req.Contents[0].Role != "user" || req.Contents[0].Parts[0].Text != "hi" {
		t.Fatalf("contents = %#v, want user hi", req.Contents)
	}
	if req.GenerationConfig == nil || req.GenerationConfig.MaxOutputTokens != 123 {
		t.Fatalf("generationConfig = %#v, want max 123", req.GenerationConfig)
	}
	if req.GenerationConfig.Temperature == nil || *req.GenerationConfig.Temperature != temp {
		t.Fatalf("temperature = %#v, want %v", req.GenerationConfig.Temperature, temp)
	}
	if req.GenerationConfig.ThinkingConfig == nil || req.GenerationConfig.ThinkingConfig.ThinkingBudget != 8192 {
		t.Fatalf("thinkingConfig = %#v, want high budget", req.GenerationConfig.ThinkingConfig)
	}
	if !req.GenerationConfig.ThinkingConfig.IncludeThoughts {
		t.Fatal("thinkingConfig.includeThoughts = false, want true")
	}
	if len(req.Tools) != 1 || len(req.Tools[0].FunctionDeclarations) != 1 || req.Tools[0].FunctionDeclarations[0].Name != "read" {
		t.Fatalf("tools = %#v, want read declaration", req.Tools)
	}
}

func TestGoogleGeminiOmitsMaxOutputTokensByDefault(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockGoogleProvider(t,
		NewGeminiProviderWithModels("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", []*provider.Model{{ID: "gemini-test", MaxTokens: 65536}}),
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n",
		bodyCh,
		nil)

	params := provider.ChatParams{
		ModelID:  "gemini-test",
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
	generationConfig, ok := raw["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("generationConfig = %#v, want object", raw["generationConfig"])
	}
	if _, ok := generationConfig["maxOutputTokens"]; ok {
		t.Fatalf("maxOutputTokens = %#v, want omitted by default", generationConfig["maxOutputTokens"])
	}
}

func TestGoogleImageMediaResolution(t *testing.T) {
	tests := []struct {
		name   string
		detail string
		want   string
	}{
		{name: "detail maps high", detail: "detail", want: "MEDIA_RESOLUTION_HIGH"},
		{name: "raw maps high", detail: "raw", want: "MEDIA_RESOLUTION_HIGH"},
		{name: "fast maps low", detail: "fast", want: "MEDIA_RESOLUTION_LOW"},
		{name: "auto omitted", detail: "auto", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyCh := make(chan string, 1)
			p := newMockGoogleProvider(t,
				NewGeminiProviderWithModels("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", []*provider.Model{{ID: "gemini-test"}}),
				"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n",
				bodyCh,
				nil)

			for range p.Chat(context.Background(), provider.ChatParams{
				ModelID: "gemini-test",
				Messages: []provider.Message{
					{
						Role: "user",
						Contents: []provider.ContentBlock{
							{Type: "image", Image: &provider.ImageContent{Data: "aW1hZ2U=", MimeType: "image/png", Detail: tt.detail}},
						},
					},
				},
				Abort: make(chan struct{}),
			}) {
			}

			var req googleRequest
			select {
			case body := <-bodyCh:
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
				}
			default:
				t.Fatal("no request body captured")
			}
			if req.GenerationConfig == nil {
				t.Fatal("generationConfig = nil, want config")
			}
			if req.GenerationConfig.MediaResolution != tt.want {
				t.Fatalf("mediaResolution = %q, want %q", req.GenerationConfig.MediaResolution, tt.want)
			}
		})
	}
}

func TestGoogleRequestCachedContent(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := NewGeminiProviderWithModels("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", []*provider.Model{{ID: "gemini-test"}})
	p.SetCachedContent("cachedContents/test-cache")
	p = newMockGoogleProvider(t, p, "data: {}\n", bodyCh, nil)

	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:  "gemini-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	}) {
	}

	var req googleRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}

	if req.CachedContent != "cachedContents/test-cache" {
		t.Fatalf("cachedContent = %q, want cachedContents/test-cache", req.CachedContent)
	}
}

func TestGoogleAssistantToolCallIncludesThoughtSignature(t *testing.T) {
	p := NewGeminiProviderWithModels("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", []*provider.Model{{ID: "gemini-test"}})
	contents := p.convertMessages(provider.ChatParams{
		Messages: []provider.Message{
			provider.NewAssistantMessage([]provider.ContentBlock{
				{
					Type:      "thinking",
					Thinking:  "thinking",
					Signature: "think-sig",
				},
				{
					Type: "toolCall",
					ToolCall: &provider.ToolCallBlock{
						ID:               "call_1",
						Name:             "bash",
						Arguments:        json.RawMessage(`{"command":"pwd"}`),
						ThoughtSignature: "tool-sig",
					},
				},
			}),
		},
	})

	if len(contents) != 1 || len(contents[0].Parts) != 2 {
		t.Fatalf("contents = %#v, want thinking and tool call parts", contents)
	}
	thinkingPart := contents[0].Parts[0]
	if thinkingPart.Text != "thinking" || !thinkingPart.Thought || thinkingPart.ThoughtSignature != "think-sig" {
		t.Fatalf("thinking part = %#v, want signed thought", thinkingPart)
	}
	part := contents[0].Parts[1]
	if part.FunctionCall == nil || part.FunctionCall.Name != "bash" {
		t.Fatalf("functionCall = %#v, want bash", part.FunctionCall)
	}
	if part.ThoughtSignature != "tool-sig" {
		t.Fatalf("thoughtSignature = %q, want tool-sig", part.ThoughtSignature)
	}
}

func TestGoogleVertexAPIKeyHeaderAndEndpoint(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockGoogleProvider(t,
		NewVertexProviderWithModels("fake-key", "https://aiplatform.googleapis.com/v1/projects/test/locations/global/publishers/google/models", []*provider.Model{{ID: "gemini-test"}}),
		"data: {}\n",
		bodyCh,
		func(r *http.Request) {
			if r.URL.Path != "/v1/publishers/google/models/gemini-test:streamGenerateContent" {
				t.Fatalf("path = %q, want Vertex API key streamGenerateContent path", r.URL.Path)
			}
			if r.Header.Get("x-goog-api-key") != "fake-key" {
				t.Fatalf("x-goog-api-key = %q, want fake-key", r.Header.Get("x-goog-api-key"))
			}
			if r.Header.Get("Authorization") != "" {
				t.Fatalf("Authorization = %q, want empty", r.Header.Get("Authorization"))
			}
		})

	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:  "gemini-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	}) {
	}
}

func TestGoogleVertexOAuthAuthorizationHeader(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockGoogleProvider(t,
		NewVertexProviderWithModels("ya29.fake-token", "https://aiplatform.googleapis.com/v1/projects/test/locations/global/publishers/google/models", []*provider.Model{{ID: "gemini-test"}}),
		"data: {}\n",
		bodyCh,
		func(r *http.Request) {
			if r.URL.Path != "/v1/projects/test/locations/global/publishers/google/models/gemini-test:streamGenerateContent" {
				t.Fatalf("path = %q, want Vertex OAuth streamGenerateContent path", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer ya29.fake-token" {
				t.Fatalf("Authorization = %q, want Bearer ya29.fake-token", r.Header.Get("Authorization"))
			}
		})

	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:  "gemini-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	}) {
	}
}

func TestGoogleStreamTextThinkToolCallAndUsage(t *testing.T) {
	sse := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"thinking\",\"thought\":true,\"thoughtSignature\":\"sig-1\"},{\"text\":\"Hello \"}]}}]}\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"thoughtSignature\":\"tool-sig\",\"functionCall\":{\"name\":\"read\",\"args\":{\"path\":\"main.go\"}}}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":5,\"thoughtsTokenCount\":2,\"cachedContentTokenCount\":7,\"totalTokenCount\":17}}\n"
	p := newMockGoogleProvider(t,
		NewGeminiProviderWithModels("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", []*provider.Model{{ID: "gemini-test"}}),
		sse,
		nil,
		nil)

	var text string
	var think string
	var thinkSignature string
	var tool *provider.ToolCallBlock
	var usage *provider.Usage
	var done bool
	for ev := range p.Chat(context.Background(), provider.ChatParams{
		ModelID:  "gemini-test",
		Messages: []provider.Message{provider.NewUserMessage("hi")},
		Abort:    make(chan struct{}),
	}) {
		switch ev.Type {
		case provider.StreamTextDelta:
			text += ev.TextDelta
		case provider.StreamThinkDelta:
			think += ev.ThinkDelta
		case provider.StreamThinkSignature:
			thinkSignature = ev.ThinkSignature
		case provider.StreamToolCall:
			tool = ev.ToolCall
		case provider.StreamUsage:
			usage = ev.Usage
		case provider.StreamDone:
			done = true
			if ev.StopReason != "stop" {
				t.Fatalf("stop reason = %q, want stop", ev.StopReason)
			}
		}
	}
	if text != "Hello " {
		t.Fatalf("text = %q, want Hello", text)
	}
	if think != "thinking" {
		t.Fatalf("think = %q, want thinking", think)
	}
	if thinkSignature != "sig-1" {
		t.Fatalf("thinkSignature = %q, want sig-1", thinkSignature)
	}
	if tool == nil || tool.Name != "read" || string(tool.Arguments) != `{"path":"main.go"}` {
		t.Fatalf("tool = %#v, want read path", tool)
	}
	if tool.ThoughtSignature != "tool-sig" {
		t.Fatalf("tool thought signature = %q, want tool-sig", tool.ThoughtSignature)
	}
	if usage == nil || usage.Input != 10 || usage.Output != 5 || usage.Reasoning != 2 || usage.CacheRead != 7 || usage.TotalTokens != 17 {
		t.Fatalf("usage = %#v, want token counts", usage)
	}
	if !done {
		t.Fatal("missing StreamDone")
	}
}

func TestGoogleDisableSamplingParamsCompat(t *testing.T) {
	bodyCh := make(chan string, 1)
	disable := true
	p := newMockGoogleProvider(t,
		NewGeminiProviderWithModels("fake-key", "https://generativelanguage.googleapis.com/v1beta/models", []*provider.Model{{
			ID:     "gemini-test",
			Compat: &provider.ModelCompat{DisableSamplingParams: &disable},
		}}),
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n",
		bodyCh,
		nil)

	temp := 0.2
	topP := 0.9
	params := provider.ChatParams{
		ModelID:     "gemini-test",
		Messages:    []provider.Message{provider.NewUserMessage("hi")},
		Temperature: &temp,
		TopP:        &topP,
		Abort:       make(chan struct{}),
	}
	for range p.Chat(context.Background(), params) {
	}

	var req googleRequest
	select {
	case body := <-bodyCh:
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
		}
	default:
		t.Fatal("no request body captured")
	}
	if req.GenerationConfig == nil {
		t.Fatal("generationConfig = nil")
	}
	if req.GenerationConfig.Temperature != nil {
		t.Fatalf("temperature = %#v, want nil (DisableSamplingParams)", *req.GenerationConfig.Temperature)
	}
	if req.GenerationConfig.TopP != nil {
		t.Fatalf("topP = %#v, want nil (DisableSamplingParams)", *req.GenerationConfig.TopP)
	}
}

func TestConvertMessagesAudioVideoInlineData(t *testing.T) {
	p := &Provider{}
	contents := p.convertMessages(provider.ChatParams{Messages: []provider.Message{{
		Role: "user",
		Contents: []provider.ContentBlock{
			{Type: "audio", Audio: &provider.AudioContent{MimeType: "audio/wav", Data: "YXVkaW8="}},
			{Type: "video", Video: &provider.VideoContent{MimeType: "video/mp4", Data: "dmlkZW8="}},
		},
	}}})
	if len(contents) != 1 || len(contents[0].Parts) != 2 {
		t.Fatalf("contents = %#v", contents)
	}
	if first := contents[0].Parts[0].InlineData; first == nil || first.MimeType != "audio/wav" || first.Data != "YXVkaW8=" {
		t.Fatalf("audio part = %#v", contents[0].Parts[0])
	}
	if second := contents[0].Parts[1].InlineData; second == nil || second.MimeType != "video/mp4" || second.Data != "dmlkZW8=" {
		t.Fatalf("video part = %#v", contents[0].Parts[1])
	}
}

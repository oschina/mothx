package google

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/ua"
)

type APIKind string

const (
	APIKindGemini APIKind = "gemini"
	APIKindVertex APIKind = "vertex"
)

type Provider struct {
	provider.BaseProvider
	apiKey        string
	baseURL       string
	apiKind       APIKind
	client        *http.Client
	retryConfig   *provider.RetryConfig
	cachedContent string
	headers       map[string]string
}

func DefaultModels(providerName string) []*provider.Model {
	return []*provider.Model{
		{
			ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Provider: providerName, Reasoning: true,
			Input: []string{"text", "image"}, ContextWindow: 1000000, MaxTokens: 65536,
		},
		{
			ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", Provider: providerName, Reasoning: true,
			Input: []string{"text", "image"}, ContextWindow: 1000000, MaxTokens: 65536,
		},
	}
}

func NewGeminiProvider(apiKey, baseURL string) *Provider {
	return NewGeminiProviderWithModels(apiKey, baseURL, DefaultModels("google-gemini"))
}

func NewGeminiProviderWithModels(apiKey, baseURL string, models []*provider.Model) *Provider {
	p, err := NewGeminiProviderWithModelsAndProxy(apiKey, baseURL, "", models)
	if err != nil {
		hc, _ := provider.NewStreamHTTPClient("")
		if hc == nil {
			hc = &http.Client{}
		}
		return newProviderWithHTTPClient("google-gemini", APIKindGemini, apiKey, baseURL, "https://generativelanguage.googleapis.com/v1beta/models", models, hc)
	}
	return p
}

func NewGeminiProviderWithModelsAndProxy(apiKey, baseURL, proxyURL string, models []*provider.Model) (*Provider, error) {
	return NewGeminiProviderWithModelsAndOptions(apiKey, baseURL, models, provider.HTTPClientOptions{ProxyURL: proxyURL})
}

func NewGeminiProviderWithModelsAndOptions(apiKey, baseURL string, models []*provider.Model, opts provider.HTTPClientOptions) (*Provider, error) {
	return newProvider("google-gemini", APIKindGemini, apiKey, baseURL, "https://generativelanguage.googleapis.com/v1beta/models", models, opts)
}

func NewVertexProviderWithModelsAndOptions(apiKey, baseURL string, models []*provider.Model, opts provider.HTTPClientOptions) (*Provider, error) {
	return newProvider("google-vertex", APIKindVertex, apiKey, baseURL, "https://aiplatform.googleapis.com/v1/publishers/google/models", models, opts)
}

func NewVertexProvider(apiKey, baseURL string) *Provider {
	return NewVertexProviderWithModels(apiKey, baseURL, DefaultModels("google-vertex"))
}

func NewVertexProviderWithModels(apiKey, baseURL string, models []*provider.Model) *Provider {
	p, err := NewVertexProviderWithModelsAndProxy(apiKey, baseURL, "", models)
	if err != nil {
		hc, _ := provider.NewStreamHTTPClient("")
		if hc == nil {
			hc = &http.Client{}
		}
		return newProviderWithHTTPClient("google-vertex", APIKindVertex, apiKey, baseURL, "https://aiplatform.googleapis.com/v1/publishers/google/models", models, hc)
	}
	return p
}

func NewVertexProviderWithModelsAndProxy(apiKey, baseURL, proxyURL string, models []*provider.Model) (*Provider, error) {
	return NewVertexProviderWithModelsAndOptions(apiKey, baseURL, models, provider.HTTPClientOptions{ProxyURL: proxyURL})
}

func newProvider(name string, kind APIKind, apiKey, baseURL, defaultBaseURL string, models []*provider.Model, opts provider.HTTPClientOptions) (*Provider, error) {
	client, err := provider.NewStreamHTTPClientWithOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("configure http proxy: %w", err)
	}
	return newProviderWithHTTPClient(name, kind, apiKey, baseURL, defaultBaseURL, models, client), nil
}

func newProviderWithHTTPClient(name string, kind APIKind, apiKey, baseURL, defaultBaseURL string, models []*provider.Model, client *http.Client) *Provider {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if apiKey == "" {
		switch kind {
		case APIKindGemini:
			apiKey = os.Getenv("GOOGLE_API_KEY")
		case APIKindVertex:
			apiKey = os.Getenv("GOOGLE_CLOUD_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("GOOGLE_VERTEX_ACCESS_TOKEN")
			}
		}
	}
	return &Provider{
		BaseProvider: provider.NewBaseProvider(name, models),
		apiKey:       apiKey,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKind:      kind,
		client:       client,
	}
}

// API returns the protocol/API type.
func (p *Provider) API() string {
	switch p.apiKind {
	case APIKindVertex:
		return "google-vertex"
	default:
		return "google-gemini"
	}
}

func (p *Provider) SetRetryConfig(cfg *provider.RetryConfig) {
	p.retryConfig = cfg
}

// SetHeaders sets custom HTTP headers applied to every provider request.
func (p *Provider) SetHeaders(headers map[string]string) {
	p.headers = cloneHeaders(headers)
}

// SetCachedContent sets an explicit Google cached content resource to reuse.
// The value should be a full cached content resource name, for example
// "cachedContents/abc123". Empty disables explicit cached content reuse.
func (p *Provider) SetCachedContent(name string) {
	p.cachedContent = strings.TrimSpace(name)
}

type googleRequest struct {
	SystemInstruction *googleContent        `json:"systemInstruction,omitempty"`
	Contents          []googleContent       `json:"contents"`
	Tools             []googleTool          `json:"tools,omitempty"`
	GenerationConfig  *googleGenerationConf `json:"generationConfig,omitempty"`
	CachedContent     string                `json:"cachedContent,omitempty"`
}

type googleGenerationConf struct {
	MaxOutputTokens int                   `json:"maxOutputTokens,omitempty"`
	Temperature     *float64              `json:"temperature,omitempty"`
	TopP            *float64              `json:"topP,omitempty"`
	MediaResolution string                `json:"mediaResolution,omitempty"`
	ThinkingConfig  *googleThinkingConfig `json:"thinkingConfig,omitempty"`
}

type googleThinkingConfig struct {
	ThinkingBudget  int  `json:"thinkingBudget,omitempty"`
	IncludeThoughts bool `json:"includeThoughts,omitempty"`
}

type googleContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []googlePart `json:"parts"`
}

type googlePart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	InlineData       *googleInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *googleFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *googleFunctionResponse `json:"functionResponse,omitempty"`
}

type googleInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type googleFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type googleFunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type googleTool struct {
	FunctionDeclarations []googleFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type googleFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type googleResponse struct {
	Candidates    []googleCandidate    `json:"candidates,omitempty"`
	UsageMetadata *googleUsageMetadata `json:"usageMetadata,omitempty"`
	Error         *googleResponseError `json:"error,omitempty"`
}

type googleCandidate struct {
	Content      googleContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
}

type googleUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

type googleResponseError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Status  string `json:"status,omitempty"`
}

func (p *Provider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 100)
	go func() {
		defer close(ch)

		if p.apiKey == "" {
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("%s API key/token not set", p.Name())}
			return
		}

		modelID := params.ModelID
		if modelID == "" {
			if len(p.Models()) > 0 {
				modelID = p.Models()[0].ID
			} else {
				modelID = "gemini-2.5-flash"
			}
		}

		reqBody := googleRequest{
			Contents:         p.convertMessages(params),
			Tools:            p.convertTools(params.Tools),
			GenerationConfig: p.generationConfig(params, p.GetModel(modelID)),
		}
		if p.cachedContent != "" {
			reqBody.CachedContent = p.cachedContent
		}
		if params.SystemPrompt != "" {
			reqBody.SystemInstruction = &googleContent{Parts: []googlePart{{Text: params.SystemPrompt}}}
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("marshal request: %w", err)}
			return
		}
		provider.DebugJSON("Google request JSON", body)

		maxRetries := 0
		baseDelayMs := 2000
		if p.retryConfig != nil && p.retryConfig.Enabled {
			maxRetries = p.retryConfig.MaxRetries
			baseDelayMs = p.retryConfig.BaseDelayMs
		}

		endpoint := p.streamEndpoint(modelID)
		for attempt := 0; attempt <= maxRetries; attempt++ {
			if err := ctx.Err(); err != nil {
				ch <- provider.StreamEvent{Type: provider.StreamError, Error: err, StopReason: "aborted"}
				return
			}

			req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
			if err != nil {
				ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("create request: %w", err)}
				return
			}
			p.setHeaders(req)

			resp, err := p.client.Do(req)
			if err != nil {
				if attempt < maxRetries && provider.IsRetryable(err, 0) {
					if !sendRetryEventAndWait(ctx, ch, attempt, maxRetries, baseDelayMs, err) {
						return
					}
					continue
				}
				ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("send request: %w", err)}
				return
			}

			if resp.StatusCode != http.StatusOK {
				bodyBytes, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				provider.DebugJSON("Google response JSON", bodyBytes)
				if attempt < maxRetries && provider.IsRetryable(fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes)), resp.StatusCode) {
					err := fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
					if !sendRetryEventAndWait(ctx, ch, attempt, maxRetries, baseDelayMs, err) {
						return
					}
					continue
				}
				ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))}
				return
			}

			streamBody := provider.NewIdleTimeoutReadCloser(resp.Body, provider.StreamIdleTimeout)
			visibleOutput, err := p.parseSSE(ctx, streamBody, ch, params)
			streamBody.Close()
			if err == nil {
				return
			}
			if attempt < maxRetries && !visibleOutput && provider.IsRetryable(err, 0) {
				if !sendRetryEventAndWait(ctx, ch, attempt, maxRetries, baseDelayMs, err) {
					return
				}
				continue
			}
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("stream read error: %w", err), StopReason: "error"}
			return
		}
		ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("all %d retry attempts exhausted", maxRetries)}
	}()
	return ch
}

func sendRetryEventAndWait(ctx context.Context, ch chan<- provider.StreamEvent, attempt, maxRetries, baseDelayMs int, err error) bool {
	delay := provider.RetryDelay(attempt, baseDelayMs)
	ch <- provider.StreamEvent{
		Type:             provider.StreamRetry,
		RetryAttempt:     attempt + 1,
		RetryMax:         maxRetries,
		RetryMaxAttempts: maxRetries,
		RetryAfterMS:     int(delay.Milliseconds()),
		Error:            fmt.Errorf("%s", provider.FormatRetryMessage(attempt, maxRetries, delay, err)),
		RetryDetail:      provider.RetryErrorDetail(err),
	}
	return sleepOrAbort(ctx, delay, ch)
}

func sleepOrAbort(ctx context.Context, delay time.Duration, ch chan<- provider.StreamEvent) bool {
	select {
	case <-ctx.Done():
		ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err(), StopReason: "aborted"}
		return false
	case <-time.After(delay):
		return true
	}
}

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", ua.ProviderUserAgent())
	switch p.apiKind {
	case APIKindVertex:
		if isGoogleOAuthToken(p.apiKey) {
			req.Header.Set("Authorization", "Bearer "+p.apiKey)
		} else {
			req.Header.Set("x-goog-api-key", p.apiKey)
		}
	default:
		req.Header.Set("x-goog-api-key", p.apiKey)
	}
	provider.ApplyHeaders(req, p.headers)
}

func isGoogleOAuthToken(token string) bool {
	token = strings.TrimSpace(token)
	return strings.HasPrefix(token, "ya29.") || strings.HasPrefix(token, "gya29.")
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for name, value := range headers {
		cloned[name] = value
	}
	return cloned
}

func (p *Provider) streamEndpoint(modelID string) string {
	base := strings.TrimRight(p.baseURL, "/")
	if p.apiKind == APIKindVertex && !isGoogleOAuthToken(p.apiKey) {
		base = vertexAPIKeyBaseURL(base)
	}
	model := strings.TrimPrefix(modelID, "models/")
	if strings.Contains(model, "/") {
		model = strings.Trim(model, "/")
	}
	return base + "/" + model + ":streamGenerateContent?alt=sse"
}

func vertexAPIKeyBaseURL(base string) string {
	if base == "" || strings.Contains(base, "/projects/") {
		return "https://aiplatform.googleapis.com/v1/publishers/google/models"
	}
	if strings.HasSuffix(base, "/publishers/google/models") {
		return base
	}
	if strings.HasSuffix(base, "/publishers/google") {
		return base + "/models"
	}
	return base
}

func (p *Provider) generationConfig(params provider.ChatParams, model *provider.Model) *googleGenerationConf {
	cfg := &googleGenerationConf{
		Temperature:     params.Temperature,
		TopP:            params.TopP,
		MediaResolution: googleMediaResolution(params.Messages),
	}
	if provider.SamplingParamsDisabled(model) {
		cfg.Temperature = nil
		cfg.TopP = nil
	}
	if params.MaxTokens > 0 {
		cfg.MaxOutputTokens = params.MaxTokens
	}
	if params.ThinkingLevel != provider.ThinkingOff && model != nil && model.Reasoning {
		cfg.ThinkingConfig = &googleThinkingConfig{ThinkingBudget: googleThinkingBudget(params.ThinkingLevel), IncludeThoughts: true}
	}
	return cfg
}

func googleMediaResolution(messages []provider.Message) string {
	hasLow := false
	for _, msg := range messages {
		for _, block := range msg.Contents {
			if block.Type != "image" || block.Image == nil {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(block.Image.Detail)) {
			case "detail", "high", "raw", "original":
				return "MEDIA_RESOLUTION_HIGH"
			case "fast", "low":
				hasLow = true
			}
		}
	}
	if hasLow {
		return "MEDIA_RESOLUTION_LOW"
	}
	return ""
}

func googleThinkingBudget(level provider.ThinkingLevel) int {
	switch level {
	case provider.ThinkingMinimal:
		return 128
	case provider.ThinkingLow:
		return 1024
	case provider.ThinkingHigh:
		return 8192
	case provider.ThinkingXHigh, provider.ThinkingMax:
		return 24576
	default:
		return 4096
	}
}

func (p *Provider) convertMessages(params provider.ChatParams) []googleContent {
	var contents []googleContent
	for i := 0; i < len(params.Messages); i++ {
		msg := params.Messages[i]
		content := googleContent{Role: googleRole(msg.Role)}
		if msg.Role == "toolResult" {
			// Google requires all functionResponse parts for the preceding
			// model functionCall parts to be in one user turn.
			toolResultContent, next := p.convertToolResultRun(params.Messages, i)
			contents = append(contents, toolResultContent)
			i = next - 1
			continue
		}

		if len(msg.Contents) == 0 {
			if msg.Content != "" {
				content.Parts = append(content.Parts, googlePart{Text: msg.Content})
			}
			if len(content.Parts) > 0 {
				contents = append(contents, content)
			}
			continue
		}

		for _, block := range msg.Contents {
			switch block.Type {
			case "text":
				if block.Text != "" {
					content.Parts = append(content.Parts, googlePart{Text: block.Text})
				}
			case "thinking":
				if block.Thinking != "" || block.Signature != "" {
					content.Parts = append(content.Parts, googlePart{Text: block.Thinking, Thought: true, ThoughtSignature: block.Signature})
				}
			case "image":
				if block.Image != nil {
					content.Parts = append(content.Parts, googlePart{InlineData: &googleInlineData{MimeType: block.Image.MimeType, Data: block.Image.Data}})
				}
			case "toolCall":
				if block.ToolCall != nil {
					content.Parts = append(content.Parts, googlePart{
						ThoughtSignature: block.ToolCall.ThoughtSignature,
						FunctionCall:     &googleFunctionCall{ID: googleWireCallID(block.ToolCall.ID), Name: block.ToolCall.Name, Args: block.ToolCall.Arguments},
					})
				}
			}
		}
		if len(content.Parts) > 0 {
			contents = append(contents, content)
		}
	}
	return contents
}

func (p *Provider) convertToolResultRun(messages []provider.Message, start int) (googleContent, int) {
	content := googleContent{Role: googleRole("toolResult")}
	var imageParts []googlePart
	i := start
	for i < len(messages) && messages[i].Role == "toolResult" {
		msg := messages[i]
		response := map[string]any{"content": googleToolResultText(msg)}
		if msg.IsError {
			response["error"] = true
		}
		functionResponse := &googleFunctionResponse{Name: msg.ToolName, Response: response}
		functionResponse.ID = googleWireCallID(msg.ToolCallID)
		content.Parts = append(content.Parts, googlePart{FunctionResponse: functionResponse})
		for _, block := range msg.Contents {
			if block.Type == "image" && block.Image != nil {
				imageParts = append(imageParts, googlePart{InlineData: &googleInlineData{MimeType: block.Image.MimeType, Data: block.Image.Data}})
			}
		}
		i++
	}
	// Keep all function responses first; attach tool images in the same user
	// turn afterward so no later function response is separated from the run.
	content.Parts = append(content.Parts, imageParts...)
	return content, i
}

func googleToolResultText(msg provider.Message) string {
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

// googleWireCallID returns only a provider-issued ID. Older Gemini/Vertex
// responses do not include IDs, so locally generated execution IDs must not be
// sent back as if they were model IDs.
func googleWireCallID(id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "google_toolcall_") || strings.HasPrefix(id, "agent_toolcall_") {
		return ""
	}
	return id
}

func googleRole(role string) string {
	switch role {
	case "assistant":
		return "model"
	case "toolResult":
		return "user"
	default:
		return "user"
	}
}

func (p *Provider) convertTools(tools []provider.ToolDefinition) []googleTool {
	var declarations []googleFunctionDeclaration
	for _, t := range tools {
		if t.Kind == "hosted" {
			continue
		}
		declarations = append(declarations, googleFunctionDeclaration{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	if len(declarations) == 0 {
		return nil
	}
	return []googleTool{{FunctionDeclarations: declarations}}
}

func (p *Provider) parseSSE(ctx context.Context, body io.Reader, ch chan<- provider.StreamEvent, params provider.ChatParams) (bool, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var textContent, reasoning strings.Builder
	var toolCalls []provider.ToolCallBlock
	var usage *provider.Usage
	var stopReason string
	var visibleOutput bool

	ch <- provider.StreamEvent{Type: provider.StreamStart}
	defer func() {
		provider.DebugCompleteResponse(provider.DebugResponse{
			Provider: p.Name(), API: string(p.apiKind), Content: textContent.String(), Reasoning: reasoning.String(),
			ToolCalls: toolCalls, StopReason: stopReason, Usage: usage,
		})
	}()
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err(), StopReason: "aborted"}
			return true, nil
		case <-params.Abort:
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("aborted"), StopReason: "aborted"}
			return true, nil
		default:
		}

		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk googleResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("%s: %s", chunk.Error.Status, chunk.Error.Message), StopReason: "error"}
			return true, nil
		}
		if chunk.UsageMetadata != nil {
			usage = convertUsage(chunk.UsageMetadata)
		}

		for _, candidate := range chunk.Candidates {
			if candidate.FinishReason != "" {
				stopReason = strings.ToLower(candidate.FinishReason)
			}
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					visibleOutput = true
					if part.Thought {
						reasoning.WriteString(part.Text)
						ch <- provider.StreamEvent{Type: provider.StreamThinkDelta, ThinkDelta: part.Text}
					} else {
						textContent.WriteString(part.Text)
						ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: part.Text}
					}
				}
				if part.ThoughtSignature != "" && part.FunctionCall == nil {
					visibleOutput = true
					ch <- provider.StreamEvent{Type: provider.StreamThinkSignature, ThinkSignature: part.ThoughtSignature}
				}
				if part.FunctionCall != nil {
					visibleOutput = true
					args := part.FunctionCall.Args
					if len(args) == 0 {
						args = json.RawMessage(`{}`)
					}
					callID := part.FunctionCall.ID
					if callID == "" {
						callID = provider.NextToolCallFallbackID("google_toolcall")
					}
					tc := &provider.ToolCallBlock{
						ID:               callID,
						Name:             part.FunctionCall.Name,
						Arguments:        args,
						ThoughtSignature: part.ThoughtSignature,
					}
					toolCalls = append(toolCalls, *tc)
					ch <- provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: tc}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return visibleOutput, err
	}
	if usage != nil {
		visibleOutput = true
		ch <- provider.StreamEvent{Type: provider.StreamUsage, Usage: usage}
	}
	ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: stopReason}
	return visibleOutput, nil
}

func convertUsage(u *googleUsageMetadata) *provider.Usage {
	if u == nil {
		return nil
	}
	return &provider.Usage{
		Input:       u.PromptTokenCount,
		Output:      u.CandidatesTokenCount,
		Reasoning:   u.ThoughtsTokenCount,
		CacheRead:   u.CachedContentTokenCount,
		TotalTokens: u.TotalTokenCount,
	}
}

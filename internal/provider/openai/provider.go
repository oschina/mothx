package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/ua"
)

// Provider implements the OpenAI Chat Completions API.
type Provider struct {
	provider.BaseProvider
	apiKey  string
	baseURL string
	client  *http.Client
	headers map[string]string

	// api is the protocol type: "openai-chat" or "openai-responses"
	api string

	// Configuration options
	disableReasoning    bool   // Disable reasoning_content support for incompatible APIs
	thinkingFormat      string // "", "openai", "deepseek", "xiaomi"
	useResponsesAPI     bool
	responsesConfig     *responsesConfig
	maxImagesPerRequest int

	// Retry configuration
	retryConfig *provider.RetryConfig
}

type responsesConfig struct {
	reasoningSummary     string
	reasoningContext     string
	reasoningMode        string
	promptCacheEnabled   bool
	promptCacheKey       string
	promptCacheRetention string
	promptCacheMode      string
	promptCacheTTL       string
	safetyIdentifier     string
	metadata             map[string]string
	stateMode            string
	store                *bool
	conversation         string
	truncation           string
	background           bool
	include              []string
	serviceTier          string
	structuredOutput     *responsesTextFormat
	toolChoice           interface{}
	parallelToolCalls    *bool
	maxToolCalls         int
	hostedTools          []responsesTool
	hostedPolicies       map[string]responsesHostedPolicy
}

// DefaultModels returns the default OpenAI model list.
func DefaultModels() []*provider.Model {
	return []*provider.Model{
		{
			ID: "gpt-4o", Name: "GPT-4o", Provider: "openai",
			Input: []string{"text", "image"}, Cost: provider.ModelPricing{Input: 2.5, Output: 10.0, CacheRead: 1.25, CacheWrite: 2.5},
			ContextWindow: 128000, MaxTokens: 16384,
		},
		{
			ID: "gpt-4o-mini", Name: "GPT-4o Mini", Provider: "openai",
			Input: []string{"text", "image"}, Cost: provider.ModelPricing{Input: 0.15, Output: 0.6, CacheRead: 0.075, CacheWrite: 0.15},
			ContextWindow: 128000, MaxTokens: 16384,
		},
		{
			ID: "o1", Name: "o1", Provider: "openai", Reasoning: true,
			Input: []string{"text", "image"}, Cost: provider.ModelPricing{Input: 15.0, Output: 60.0, CacheRead: 7.5, CacheWrite: 15.0},
			ContextWindow: 200000, MaxTokens: 100000,
		},
		{
			ID: "o3-mini", Name: "o3-mini", Provider: "openai", Reasoning: true,
			Input: []string{"text", "image"}, Cost: provider.ModelPricing{Input: 1.1, Output: 4.4, CacheRead: 0.55, CacheWrite: 1.1},
			ContextWindow: 200000, MaxTokens: 100000,
		},
	}
}

// NewProvider creates a new OpenAI provider with default models.
func NewProvider(apiKey, baseURL string) *Provider {
	return NewProviderWithModels(apiKey, baseURL, DefaultModels())
}

// NewProviderWithModels creates a new OpenAI provider with custom models.
func NewProviderWithModels(apiKey, baseURL string, models []*provider.Model) *Provider {
	p, err := NewProviderWithModelsAndProxy(apiKey, baseURL, "", models)
	if err != nil {
		hc, _ := provider.NewStreamHTTPClient("")
		if hc == nil {
			hc = &http.Client{}
		}
		return newProviderWithHTTPClient(apiKey, baseURL, models, hc)
	}
	return p
}

func NewProviderWithModelsAndProxy(apiKey, baseURL, proxyURL string, models []*provider.Model) (*Provider, error) {
	return NewProviderWithModelsAndOptions(apiKey, baseURL, models, provider.HTTPClientOptions{ProxyURL: proxyURL})
}

func NewProviderWithModelsAndOptions(apiKey, baseURL string, models []*provider.Model, opts provider.HTTPClientOptions) (*Provider, error) {
	client, err := provider.NewStreamHTTPClientWithOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("configure http proxy: %w", err)
	}
	return newProviderWithHTTPClient(apiKey, baseURL, models, client), nil
}

func newProviderWithHTTPClient(apiKey, baseURL string, models []*provider.Model, client *http.Client) *Provider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	p := &Provider{
		BaseProvider: provider.NewBaseProvider("openai", models),
		apiKey:       apiKey,
		baseURL:      strings.TrimRight(baseURL, "/"),
		client:       client,
		api:          "openai-chat",
		responsesConfig: &responsesConfig{
			promptCacheEnabled: true,
		},
	}

	// Check environment variable to disable reasoning
	if os.Getenv("OPENAI_DISABLE_REASONING") == "1" || os.Getenv("OPENAI_DISABLE_REASONING") == "true" {
		p.disableReasoning = true
	}

	return p
}

// API returns the protocol/API type.
func (p *Provider) API() string {
	return p.api
}

// ResponseStateMode implements provider.ResponseStateModeProvider.
func (p *Provider) ResponseStateMode() string {
	if p.responsesConfig == nil || p.responsesConfig.stateMode == "" {
		return "replay"
	}
	return p.responsesConfig.stateMode
}

// ResponseStateFallbackError reports errors that invalidate a remote
// previous_response_id but are recoverable from the local Responses archive.
func (p *Provider) ResponseStateFallbackError(err error) bool {
	switch p.ResponseStateFailureClass(err) {
	case provider.ResponseStateFailureExpired, provider.ResponseStateFailurePermission:
		return true
	default:
		return false
	}
}

// ResponseStateFailureClass categorizes stateful Responses failures for
// recovery/audit. Explicit remote state invalidation is replayable from the
// local archive; ordinary request failures remain visible to the caller.
func (p *Provider) ResponseStateFailureClass(err error) provider.ResponseStateFailureClass {
	if err == nil || p == nil {
		return provider.ResponseStateFailureRequestFailed
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "api error 401") || strings.Contains(message, "api error 403") ||
		strings.Contains(message, "unauthorized") || strings.Contains(message, "forbidden") || strings.Contains(message, "permission") {
		return provider.ResponseStateFailurePermission
	}
	if strings.Contains(message, "api error 404") || strings.Contains(message, "api error 410") ||
		strings.Contains(message, "previous_response_id") &&
			(strings.Contains(message, "expired") || strings.Contains(message, "not found") || strings.Contains(message, "invalid")) {
		return provider.ResponseStateFailureExpired
	}
	return provider.ResponseStateFailureRequestFailed
}

// SetUseResponsesAPI switches the provider to the Responses API.
func (p *Provider) SetUseResponsesAPI(enabled bool) {
	p.useResponsesAPI = enabled
	p.api = "openai-responses"
}

// SetResponsesConfig applies Responses API-specific configuration.
func (p *Provider) SetResponsesConfig(cfg config.ResponsesConfig) error {
	if err := validateResponsesConfig(cfg); err != nil {
		return err
	}
	p.responsesConfig = &responsesConfig{
		reasoningSummary:     cfg.ReasoningSummary,
		reasoningContext:     cfg.ReasoningContext,
		reasoningMode:        cfg.ReasoningMode,
		promptCacheEnabled:   cfg.PromptCacheEnabled == nil || *cfg.PromptCacheEnabled,
		promptCacheKey:       cfg.PromptCacheKey,
		promptCacheRetention: cfg.PromptCacheRetention,
		promptCacheMode:      cfg.PromptCacheMode,
		promptCacheTTL:       cfg.PromptCacheTTL,
		safetyIdentifier:     cfg.SafetyIdentifier,
		metadata:             cloneStringMap(cfg.Metadata),
		stateMode:            cfg.StateMode,
		store:                config.CloneBoolPtr(cfg.Store),
		conversation:         cfg.Conversation,
		truncation:           cfg.Truncation,
		background:           cfg.Background != nil && *cfg.Background,
		include:              config.CloneStringSlice(cfg.Include),
		serviceTier:          cfg.ServiceTier,
		structuredOutput:     responsesConfigTextFormat(cfg.StructuredOutput),
		toolChoice:           responsesConfigToolChoice(cfg.ToolControl.Choice),
		parallelToolCalls:    config.CloneBoolPtr(cfg.ToolControl.Parallel),
		maxToolCalls:         cfg.ToolControl.MaxCalls,
		hostedTools:          responsesConfigHostedTools(cfg.HostedTools),
		hostedPolicies:       responsesHostedPolicies(cfg.HostedTools),
	}
	return nil
}

// ResponsesBackgroundEnabled reports whether this provider must submit
// Responses requests through the durable background run manager.
func (p *Provider) ResponsesBackgroundEnabled() bool {
	return p != nil && p.responsesConfig != nil && p.responsesConfig.background
}

func (p *Provider) responsesHostedTimeout() time.Duration {
	if p == nil || p.responsesConfig == nil {
		return 0
	}
	return p.responsesConfig.hostedPolicies["code_interpreter"].Timeout
}

// ResponsesHostedTimeout exposes the optional local hosted-tool deadline to
// the durable Serve coordinator without adding it to the common Provider
// interface. A zero duration means no local deadline was configured.
func (p *Provider) ResponsesHostedTimeout() time.Duration {
	return p.responsesHostedTimeout()
}

// DisableReasoning disables reasoning_content support for incompatible APIs.
func (p *Provider) DisableReasoning() {
	p.disableReasoning = true
}

// SetRetryConfig sets the retry configuration for this provider.
func (p *Provider) SetRetryConfig(cfg *provider.RetryConfig) {
	p.retryConfig = cfg
}

// SetHeaders sets custom HTTP headers applied to every provider request.
func (p *Provider) SetHeaders(headers map[string]string) {
	p.headers = cloneHeaders(headers)
}

// SetMaxImagesPerRequest configures the client-side image history limit.
// Positive values keep the newest N images, zero uses the provider default,
// and negative values disable the limit.
func (p *Provider) SetMaxImagesPerRequest(max int) {
	p.maxImagesPerRequest = max
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// IsReasoningDisabled returns whether reasoning support is disabled.
func (p *Provider) IsReasoningDisabled() bool {
	return p.disableReasoning
}

// SetThinkingFormat sets the thinking parameter format.
// "openai" = reasoning_effort, "deepseek" = thinking + reasoning_effort,
// "kimi" = reasoning_effort with Kimi's low/high/max levels,
// "doubao-seed" = reasoning_effort with minimal/low/medium/high (minimal = no thinking),
// "xiaomi" = legacy thinking-only format.
// "thinking-only" = thinking switch without any effort field. DeepSeek V4.1
// models auto-select this format: their chat templates accept only a thinking
// switch, and any OpenAI-style reasoning_effort string makes vLLM-backed
// gateways fail with
// "cannot unmarshal bool into ... chat_template_kwargs.reasoning_effort".
func (p *Provider) SetThinkingFormat(format string) {
	p.thinkingFormat = format
}

// openAIRequest represents the request body for OpenAI Chat Completions.
type openAIRequest struct {
	Model               string          `json:"model"`
	Messages            []openAIMessage `json:"messages"`
	Tools               []openAITool    `json:"tools,omitempty"`
	ParallelToolCalls   *bool           `json:"parallel_tool_calls,omitempty"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	Stream              bool            `json:"stream"`
	StreamOptions       *streamOptions  `json:"stream_options,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	Thinking            *thinkingConfig `json:"thinking,omitempty"`
	EnableThinking      *bool           `json:"enable_thinking,omitempty"`
	ThinkingBudget      int             `json:"thinking_budget,omitempty"`
}

type thinkingConfig struct {
	Type string `json:"type"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    interface{}      `json:"content"`
	Reasoning  *string          `json:"reasoning_content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type openAIContentBlock struct {
	Type       string            `json:"type"`
	Text       string            `json:"text,omitempty"`
	ImageURL   *openAIImage      `json:"image_url,omitempty"`
	InputAudio *openAIInputAudio `json:"input_audio,omitempty"`
	VideoURL   *openAIVideo      `json:"video_url,omitempty"`
}

type openAIImage struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// openAIInputAudio carries inline audio in the OpenAI-compatible input_audio
// shape used by Qwen/DashScope-family gateways: data holds a URL or a data URL,
// format names the codec. OpenAI-native audio models expect raw base64 in data
// instead; if such a model lands in the catalog, gate the shape through
// ModelCompat rather than forking this codec.
type openAIInputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format,omitempty"`
}

// openAIVideo carries inline video in the OpenAI-compatible video_url shape
// used by Qwen/DashScope-family gateways: url holds a URL or a data URL.
type openAIVideo struct {
	URL string `json:"url"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Index    int                `json:"index"`
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name      string              `json:"name"`
	Arguments openAIToolArguments `json:"arguments"`
}

// openAIToolArguments accepts both the OpenAI-standard JSON string and the
// JSON object returned by some OpenAI-compatible APIs, including Volcengine.
// Internally, tool arguments are always kept as their raw JSON object.
type openAIToolArguments json.RawMessage

func (a *openAIToolArguments) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*a = nil
		return nil
	}

	var encoded string
	if err := json.Unmarshal(data, &encoded); err == nil {
		*a = openAIToolArguments(encoded)
		return nil
	}
	if !json.Valid(data) {
		return fmt.Errorf("invalid tool arguments JSON")
	}
	*a = append((*a)[:0], data...)
	return nil
}

func (a openAIToolArguments) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(a))
}

type openAIResponse struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []openAIChoice       `json:"choices"`
	Usage   *openAIUsageResponse `json:"usage,omitempty"`
}

type openAIChoice struct {
	Index        int         `json:"index"`
	Delta        openAIDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type openAIDelta struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Reasoning *string          `json:"reasoning_content,omitempty"`
	ToolCalls []openAIToolCall `json:"tool_calls"`
}

type openAIUsageResponse struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// Chat implements the streaming chat interface.
func (p *Provider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	if p.useResponsesAPI {
		return p.chatResponses(ctx, params)
	}
	return p.chatCompletions(ctx, params)
}

func (p *Provider) chatCompletions(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 100)

	go func() {
		defer close(ch)

		if p.apiKey == "" {
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("OPENAI_API_KEY not set")}
			return
		}

		modelID := params.ModelID
		if modelID == "" {
			if len(p.Models()) > 0 {
				modelID = p.Models()[0].ID
			} else {
				ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("no models available from provider %q", p.Name())}
				return
			}
		}

		model := p.GetModel(modelID)
		messages := p.convertMessages(params, p.requiresReasoningContentOnAssistant(model))
		tools := p.convertTools(params.Tools)

		reqBody := openAIRequest{
			Model:             modelID,
			Messages:          messages,
			Tools:             tools,
			ParallelToolCalls: chatParallelToolCalls(model, tools, params.ResponseOptions),
			Stream:            true,
			StreamOptions:     &streamOptions{IncludeUsage: true},
			Temperature:       params.Temperature,
			TopP:              params.TopP,
		}
		if params.MaxTokens > 0 {
			if maxTokensField(model) == "max_completion_tokens" {
				reqBody.MaxCompletionTokens = params.MaxTokens
			} else {
				reqBody.MaxTokens = params.MaxTokens
			}
		}

		if !p.disableReasoning && params.ThinkingLevel != provider.ThinkingOff && model != nil && model.Reasoning {
			// Determine thinking format: explicit config > URL auto-detect > default
			format := p.thinkingFormatForModel(model)
			switch format {
			case "deepseek":
				reqBody.Thinking = &thinkingConfig{Type: "enabled"}
				if supportsReasoningEffort(model) {
					reqBody.ReasoningEffort = deepseekReasoningEffort(params.ThinkingLevel)
				}
			case "kimi":
				if supportsReasoningEffort(model) {
					reqBody.ReasoningEffort = kimiReasoningEffort(params.ThinkingLevel)
				}
			case "doubao-seed":
				if supportsReasoningEffort(model) {
					reqBody.ReasoningEffort = doubaoSeedReasoningEffort(params.ThinkingLevel)
				}
			case "thinking-only", "xiaomi":
				reqBody.Thinking = &thinkingConfig{Type: "enabled"}
			case "qwen":
				enabled := true
				reqBody.EnableThinking = &enabled
				if budget := qwenThinkingBudget(params.ThinkingLevel); budget > 0 {
					reqBody.ThinkingBudget = budget
				}
			default: // "openai" or ""
				if supportsReasoningEffort(model) {
					reqBody.ReasoningEffort = openAIReasoningEffort(params.ThinkingLevel)
					// OpenAI reasoning models reject temperature/top_p.
					reqBody.Temperature = nil
					reqBody.TopP = nil
				}
			}
		}

		// Some models reject sampling parameters entirely (compat flag).
		if provider.SamplingParamsDisabled(model) {
			reqBody.Temperature = nil
			reqBody.TopP = nil
		}

		// Build the request body once (reused across retries)
		body, err := json.Marshal(reqBody)
		if err != nil {
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("marshal request: %w", err)}
			return
		}

		provider.DebugJSON("OpenAI request JSON", body)

		// Retry loop covers initial HTTP failures and early SSE read failures.
		// Once visible streamed content has been emitted, retrying could duplicate output.
		maxRetries := 0
		baseDelayMs := 2000
		if p.retryConfig != nil && p.retryConfig.Enabled {
			maxRetries = p.retryConfig.MaxRetries
			baseDelayMs = p.retryConfig.BaseDelayMs
		}

		completionTokenFallbackUsed := false
		for attempt := 0; attempt <= maxRetries; attempt++ {
			if err := ctx.Err(); err != nil {
				ch <- provider.StreamEvent{Type: provider.StreamError, Error: err, StopReason: "aborted"}
				return
			}

			req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(body))
			if err != nil {
				ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("create request: %w", err)}
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+p.apiKey)
			req.Header.Set("Accept", "text/event-stream")
			req.Header.Set("User-Agent", ua.ProviderUserAgent())
			provider.ApplyHeaders(req, p.headers)

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
				provider.DebugJSON("OpenAI response JSON", bodyBytes)
				if resp.StatusCode == http.StatusBadRequest && !completionTokenFallbackUsed &&
					params.MaxTokens > 0 && maxTokensField(model) != "max_completion_tokens" &&
					isMaxTokensUnsupportedResponse(bodyBytes) {
					reqBody.MaxTokens = 0
					reqBody.MaxCompletionTokens = params.MaxTokens
					body, err = json.Marshal(reqBody)
					if err != nil {
						ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("marshal max completion tokens fallback: %w", err)}
						return
					}
					completionTokenFallbackUsed = true
					attempt--
					continue
				}
				if attempt < maxRetries && provider.IsRetryable(fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes)), resp.StatusCode) {
					if !sendRetryEventAndWait(ctx, ch, attempt, maxRetries, baseDelayMs, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))) {
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

		// All retries exhausted (should not reach here with for..break logic, but safety net)
		ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("all %d retry attempts exhausted", maxRetries)}
	}()

	return ch
}

// chatParallelToolCalls resolves the OpenAI Chat Completions parallel tool
// call request flag. Chat requests with function tools opt in by default,
// while model compatibility metadata can explicitly disable the field for
// gateways that reject it. A per-request ResponseOptions value has priority
// over the default.
func chatParallelToolCalls(model *provider.Model, tools []openAITool, opts *provider.ResponseOptions) *bool {
	if len(tools) == 0 {
		return nil
	}
	if model != nil && model.Compat != nil && model.Compat.SupportsParallelToolCalls != nil && !*model.Compat.SupportsParallelToolCalls {
		return nil
	}
	if opts != nil && opts.ParallelTools != nil {
		return cloneBoolPtr(opts.ParallelTools)
	}
	enabled := true
	return &enabled
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
	select {
	case <-ctx.Done():
		ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err(), StopReason: "aborted"}
		return false
	case <-time.After(delay):
		return true
	}
}

func (p *Provider) parseSSE(ctx context.Context, body io.Reader, ch chan<- provider.StreamEvent, params provider.ChatParams) (bool, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		textContent     strings.Builder
		reasoning       strings.Builder
		toolCalls       []provider.ToolCallBlock
		toolCallBuffers = make(map[int]*strings.Builder)
		stopReason      string
		usage           *provider.Usage
		visibleOutput   bool
	)

	var splitter *thinkSplitter
	if model := p.GetModel(params.ModelID); model != nil && model.Compat != nil && model.Compat.ParseReasoningInContent {
		splitter = &thinkSplitter{}
	}

	ch <- provider.StreamEvent{Type: provider.StreamStart}
	defer func() {
		provider.DebugCompleteResponse(provider.DebugResponse{
			Provider: "openai", API: "chat-completions", Content: textContent.String(),
			Reasoning: reasoning.String(), ToolCalls: toolCalls, StopReason: stopReason, Usage: usage,
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

		var chunk openAIResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			mergeOpenAIUsage(&usage, chunk.Usage)
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				if splitter != nil {
					text, think := splitter.push(choice.Delta.Content)
					if think != "" {
						visibleOutput = true
						reasoning.WriteString(think)
						ch <- provider.StreamEvent{Type: provider.StreamThinkDelta, ThinkDelta: think}
					}
					if text != "" {
						visibleOutput = true
						textContent.WriteString(text)
						ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: text}
					}
				} else {
					visibleOutput = true
					textContent.WriteString(choice.Delta.Content)
					ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: choice.Delta.Content}
				}
			}
			if !p.disableReasoning && choice.Delta.Reasoning != nil && *choice.Delta.Reasoning != "" {
				visibleOutput = true
				reasoning.WriteString(*choice.Delta.Reasoning)
				ch <- provider.StreamEvent{Type: provider.StreamThinkDelta, ThinkDelta: *choice.Delta.Reasoning}
			}
			for _, tc := range choice.Delta.ToolCalls {
				visibleOutput = true
				idx := tc.Index
				if idx < 0 {
					continue
				}
				if _, ok := toolCallBuffers[idx]; !ok {
					toolCallBuffers[idx] = &strings.Builder{}
					for len(toolCalls) <= idx {
						toolCalls = append(toolCalls, provider.ToolCallBlock{})
					}
					toolCalls[idx].ID = tc.ID
					toolCalls[idx].Name = tc.Function.Name
				}
				if tc.ID != "" {
					toolCalls[idx].ID = tc.ID
				}
				if tc.Function.Name != "" {
					toolCalls[idx].Name = tc.Function.Name
				}
				if len(tc.Function.Arguments) > 0 {
					toolCallBuffers[idx].Write(tc.Function.Arguments)
				}
			}
			if choice.FinishReason != nil {
				stopReason = *choice.FinishReason
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return visibleOutput, err
	}

	if splitter != nil {
		text, think := splitter.flush()
		if think != "" {
			visibleOutput = true
			reasoning.WriteString(think)
			ch <- provider.StreamEvent{Type: provider.StreamThinkDelta, ThinkDelta: think}
		}
		if text != "" {
			visibleOutput = true
			textContent.WriteString(text)
			ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: text}
		}
	}

	for i, tc := range toolCalls {
		if buf, ok := toolCallBuffers[i]; ok {
			if tc.ID == "" {
				// Some OpenAI-compatible providers omit tool call IDs in stream deltas.
				tc.ID = provider.NextToolCallFallbackID("openai_toolcall")
			}
			tc.Arguments = json.RawMessage(buf.String())
			toolCalls[i] = tc
			visibleOutput = true
			ch <- provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &toolCalls[i]}
		}
	}

	if usage != nil {
		visibleOutput = true
		ch <- provider.StreamEvent{Type: provider.StreamUsage, Usage: usage}
	}
	ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: stopReason}
	return visibleOutput, nil
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

func mergeOpenAIUsage(dst **provider.Usage, src *openAIUsageResponse) {
	if src == nil {
		return
	}
	if *dst == nil {
		*dst = &provider.Usage{
			Input:       src.PromptTokens,
			Output:      src.CompletionTokens,
			TotalTokens: src.TotalTokens,
		}
		if src.PromptTokensDetails != nil {
			(*dst).CacheRead = src.PromptTokensDetails.CachedTokens
		}
		return
	}
	if src.PromptTokens > 0 && (*dst).Input == 0 {
		(*dst).Input = src.PromptTokens
	}
	if src.CompletionTokens > 0 && (*dst).Output == 0 {
		(*dst).Output = src.CompletionTokens
	}
	if src.TotalTokens > 0 && (*dst).TotalTokens == 0 {
		(*dst).TotalTokens = src.TotalTokens
	}
	if src.PromptTokensDetails != nil && src.PromptTokensDetails.CachedTokens > 0 && (*dst).CacheRead == 0 {
		(*dst).CacheRead = src.PromptTokensDetails.CachedTokens
	}
}

func openAIReasoningEffort(level provider.ThinkingLevel) string {
	switch level {
	case provider.ThinkingMinimal, provider.ThinkingLow:
		return "low"
	case provider.ThinkingMedium:
		return "medium"
	case provider.ThinkingHigh, provider.ThinkingXHigh:
		return "high"
	case provider.ThinkingMax:
		return "max"
	default:
		return ""
	}
}

func kimiReasoningEffort(level provider.ThinkingLevel) string {
	switch level {
	case provider.ThinkingMinimal, provider.ThinkingLow:
		return "low"
	case provider.ThinkingMedium, provider.ThinkingHigh:
		return "high"
	case provider.ThinkingXHigh, provider.ThinkingMax:
		return "max"
	default:
		return ""
	}
}

func deepseekReasoningEffort(level provider.ThinkingLevel) string {
	switch level {
	case provider.ThinkingXHigh, provider.ThinkingMax:
		return "max"
	default:
		return "high"
	}
}

func doubaoSeedReasoningEffort(level provider.ThinkingLevel) string {
	switch level {
	case provider.ThinkingMinimal:
		return "minimal"
	case provider.ThinkingLow:
		return "low"
	case provider.ThinkingMedium:
		return "medium"
	case provider.ThinkingHigh, provider.ThinkingXHigh:
		return "high"
	case provider.ThinkingMax:
		return "max"
	default:
		return ""
	}
}

func qwenThinkingBudget(level provider.ThinkingLevel) int {
	switch level {
	case provider.ThinkingMinimal, provider.ThinkingLow:
		return 500
	case provider.ThinkingMedium, provider.ThinkingHigh:
		return 4096
	case provider.ThinkingXHigh, provider.ThinkingMax:
		return 10240
	default:
		return 0
	}
}

func (p *Provider) thinkingFormatForModel(model *provider.Model) string {
	if p.thinkingFormat != "" {
		return p.thinkingFormat
	}
	if model != nil && model.Compat != nil && model.Compat.ThinkingFormat != "" {
		return model.Compat.ThinkingFormat
	}
	if model != nil && isDeepSeekV41Model(model.ID) {
		return "thinking-only"
	}
	if model != nil && isQwenModel(model.ID) {
		return "qwen"
	}
	if model != nil && isDoubaoSeedModel(model.ID) {
		return "doubao-seed"
	}
	lowerBaseURL := strings.ToLower(p.baseURL)
	if strings.Contains(lowerBaseURL, "deepseek") {
		return "deepseek"
	}
	if strings.Contains(lowerBaseURL, "xiaomimimo") {
		return "xiaomi"
	}
	return ""
}

func isQwenModel(modelID string) bool {
	lower := strings.ToLower(modelID)
	// Match qwen3.6+, qwen3.7+, qwen3.8+ with or without prefix (e.g. "qwen/qwen3.7-plus")
	return strings.Contains(lower, "qwen3.6") ||
		strings.Contains(lower, "qwen3.7") ||
		strings.Contains(lower, "qwen3.8")
}

func isDeepSeekV41Model(modelID string) bool {
	lower := strings.ToLower(modelID)
	return strings.Contains(lower, "deepseek-v4.1") || strings.Contains(lower, "deepseek-v4-1")
}

func isDoubaoSeedModel(modelID string) bool {
	lower := strings.ToLower(modelID)
	// Match doubao-seed-2.1-turbo, doubao-seed-2-1-turbo-260628, doubao-seed-evolving, etc.
	return strings.Contains(lower, "doubao-seed-2.1") ||
		strings.Contains(lower, "doubao-seed-2-1") ||
		strings.Contains(lower, "doubao-seed-evolving")
}

func supportsReasoningEffort(model *provider.Model) bool {
	if model != nil && model.Compat != nil && model.Compat.SupportsReasoningEffort != nil {
		return *model.Compat.SupportsReasoningEffort
	}
	return true
}

func isMaxTokensUnsupportedResponse(body []byte) bool {
	message := strings.ToLower(string(body))
	return strings.Contains(message, "max_tokens") &&
		(strings.Contains(message, "max_completion_tokens") || strings.Contains(message, "not supported"))
}

func maxTokensField(model *provider.Model) string {
	if model == nil {
		return ""
	}
	if model.Compat != nil && model.Compat.MaxTokensField != "" {
		return model.Compat.MaxTokensField
	}

	// OpenAI's newer reasoning and GPT-5 families reject max_tokens even when
	// the model was configured without an explicit compatibility block. Keep
	// this inference in the provider request builder so Agent and Subagent use
	// the same behavior, rather than teaching compaction about OpenAI fields.
	id := strings.ToLower(strings.TrimSpace(model.ID))
	// Configured IDs may be namespaced (for example openai/gpt-5.2 or
	// azure:gpt-5-chat). Match the model family anywhere in the ID rather than
	// relying on a particular registry naming convention.
	if strings.Contains(id, "gpt-5") || strings.HasPrefix(id, "o1") ||
		strings.HasPrefix(id, "o3") || strings.HasPrefix(id, "o4") ||
		strings.Contains(id, "/o1") || strings.Contains(id, "/o3") ||
		strings.Contains(id, "/o4") {
		return "max_completion_tokens"
	}
	return ""
}

func (p *Provider) requiresReasoningContentOnAssistant(model *provider.Model) bool {
	if model != nil && model.Compat != nil && model.Compat.RequiresReasoningContentOnAssistant {
		return true
	}
	if model != nil {
		modelID := strings.ToLower(model.ID)
		if strings.Contains(modelID, "kimi") || modelID == "k3" || strings.HasPrefix(modelID, "k3-") {
			return true
		}
	}
	lowerBaseURL := strings.ToLower(p.baseURL)
	return strings.Contains(lowerBaseURL, "deepseek") || strings.Contains(lowerBaseURL, "xiaomimimo") ||
		strings.Contains(lowerBaseURL, "moonshot") || strings.Contains(lowerBaseURL, "kimi.com")
}

func (p *Provider) convertMessages(params provider.ChatParams, forceAssistantReasoning bool) []openAIMessage {
	var messages []openAIMessage

	// Add system prompt as the first message if provided
	if params.SystemPrompt != "" {
		messages = append(messages, openAIMessage{
			Role:    "system",
			Content: params.SystemPrompt,
		})
	}

	inputMessages := normalizeToolResultSequence(params.Messages)
	if maxImages := p.maxImagesPerRequestForRequest(params); maxImages > 0 {
		inputMessages = limitImageHistory(inputMessages, maxImages)
	}
	var pendingToolImages []openAIContentBlock
	flushToolImages := func() {
		if len(pendingToolImages) == 0 {
			return
		}
		messages = append(messages, openAIMessage{Role: "user", Content: pendingToolImages})
		pendingToolImages = nil
	}
	for _, msg := range inputMessages {
		if msg.Role != "toolResult" {
			flushToolImages()
		}
		// OpenAI-compatible tool messages require both the call ID and the
		// function name. Kimi validates the name as part of matching a tool
		// result to the preceding assistant tool_call.
		om := openAIMessage{Role: msg.Role, ToolCallID: msg.ToolCallID, Name: msg.ToolName}
		if msg.Role == "toolResult" {
			om.Role = "tool"
			if len(msg.Contents) > 0 {
				// Rich tool result: send text as tool message, images as supplementary user message
				om.Content = responseToolOutput(msg)
				messages = append(messages, om)
				// Collect image blocks for a supplementary user message. Defer
				// emitting that user message until all consecutive tool results
				// have been emitted; Kimi requires tool responses to stay adjacent
				// to the preceding assistant tool_calls.
				var imageBlocks []openAIContentBlock
				for _, c := range msg.Contents {
					if c.Type == "image" && c.Image != nil {
						imageBlocks = append(imageBlocks, openAIContentBlock{Type: "image_url", ImageURL: p.openAIImage(c.Image)})
					}
				}
				if len(imageBlocks) > 0 {
					pendingToolImages = append(pendingToolImages, imageBlocks...)
				}
				continue
			}
			om.Content = msg.Content
		} else if len(msg.Contents) > 0 {
			var blocks []openAIContentBlock
			var reasoningContent string
			for _, c := range msg.Contents {
				switch c.Type {
				case "text":
					blocks = append(blocks, openAIContentBlock{Type: "text", Text: c.Text})
				case "image":
					if c.Image != nil {
						blocks = append(blocks, openAIContentBlock{Type: "image_url", ImageURL: p.openAIImage(c.Image)})
					}
				case "audio":
					if c.Audio != nil {
						blocks = append(blocks, openAIContentBlock{Type: "input_audio", InputAudio: openAIInputAudioPart(c.Audio)})
					}
				case "video":
					if c.Video != nil {
						blocks = append(blocks, openAIContentBlock{Type: "video_url", VideoURL: &openAIVideo{URL: mediaSourceURL(c.Video.MimeType, c.Video.Data, c.Video.URL)}})
					}
				case "thinking":
					// Store reasoning content for OpenAI-compatible APIs
					if !p.disableReasoning {
						reasoningContent += c.Thinking
					}
				}
			}
			if len(blocks) == 1 && blocks[0].Type == "text" {
				om.Content = blocks[0].Text
			} else if len(blocks) > 0 {
				om.Content = blocks
			}
			// For assistant messages with tool calls, ensure content is not an empty array
			// Set reasoning content if available
			if reasoningContent != "" {
				om.Reasoning = &reasoningContent
			}
		} else {
			om.Content = msg.Content
		}
		if msg.Role == "assistant" {
			for _, c := range msg.Contents {
				if c.Type == "toolCall" && c.ToolCall != nil {
					om.ToolCalls = append(om.ToolCalls, openAIToolCall{ID: c.ToolCall.ID, Type: "function", Function: openAIToolFunction{
						Name:      c.ToolCall.Name,
						Arguments: openAIToolArguments(c.ToolCall.Arguments),
					}})
				}
			}
		}
		if msg.Role == "assistant" && forceAssistantReasoning && om.Reasoning == nil {
			reasoningContent := ""
			om.Reasoning = &reasoningContent
		}
		messages = append(messages, om)
	}
	flushToolImages()
	return messages
}

// maxImagesPerRequestForRequest resolves the configured image count. A zero
// value uses URL-based defaults for the known Moark/Gitee gateways; other
// gateways are left uncapped unless configured explicitly.
func (p *Provider) maxImagesPerRequestForRequest(_ provider.ChatParams) int {
	if p.maxImagesPerRequest != 0 {
		return p.maxImagesPerRequest
	}
	baseURL := strings.ToLower(p.baseURL)
	if strings.Contains(baseURL, "api.moark.com") || strings.Contains(baseURL, "ai.gitee.com") {
		return 5
	}
	return 0
}

// limitImageHistory keeps the newest images and replaces older image blocks
// with a compact marker. Text and tool-result descriptions remain available
// while providers with small image limits receive a valid request.
func limitImageHistory(messages []provider.Message, maxImages int) []provider.Message {
	if maxImages <= 0 {
		return messages
	}
	imageCount := 0
	for _, msg := range messages {
		for _, block := range msg.Contents {
			if block.Type == "image" && block.Image != nil {
				imageCount++
			}
		}
	}
	if imageCount <= maxImages {
		return messages
	}

	result := make([]provider.Message, len(messages))
	copy(result, messages)
	toOmit := imageCount - maxImages
	for i := range result {
		if len(result[i].Contents) == 0 {
			continue
		}
		contents := make([]provider.ContentBlock, 0, len(result[i].Contents))
		for _, block := range result[i].Contents {
			if block.Type == "image" && block.Image != nil && toOmit > 0 {
				contents = append(contents, provider.ContentBlock{
					Type: "text",
					Text: "[image omitted: provider image limit]",
				})
				toOmit--
				continue
			}
			contents = append(contents, block)
		}
		result[i].Contents = contents
	}
	return result
}

// normalizeToolResultSequence repairs stale or partially persisted histories
// before they are sent to OpenAI-compatible APIs. The API contract requires
// every assistant tool call to be followed immediately by a tool response with
// the same ID. Interrupted runs and older session snapshots can leave that
// response out; Kimi rejects the whole request in that case.
func normalizeToolResultSequence(input []provider.Message) []provider.Message {
	if len(input) == 0 {
		return nil
	}
	hasAssistantToolCalls := false
	for _, msg := range input {
		if msg.Role != "assistant" {
			continue
		}
		for _, block := range msg.Contents {
			if block.Type == "toolCall" && block.ToolCall != nil && block.ToolCall.ID != "" {
				hasAssistantToolCalls = true
				break
			}
		}
		if hasAssistantToolCalls {
			break
		}
	}
	out := make([]provider.Message, 0, len(input))
	for i := 0; i < len(input); i++ {
		msg := input[i]
		if msg.Role != "assistant" {
			// A tool result is valid only when it is consumed immediately after
			// its assistant tool-call message. Orphaned results can be left by
			// interrupted sub-agent/ESM runs and make Kimi reject the request.
			if msg.Role != "toolResult" || !hasAssistantToolCalls {
				out = append(out, msg)
			}
			continue
		}
		out = append(out, msg)
		calls := make(map[string]string)
		for _, block := range msg.Contents {
			if block.Type == "toolCall" && block.ToolCall != nil && block.ToolCall.ID != "" {
				calls[block.ToolCall.ID] = block.ToolCall.Name
			}
		}
		if len(calls) == 0 {
			continue
		}
		results := make(map[string]provider.Message, len(calls))
		j := i + 1
		for j < len(input) && input[j].Role == "toolResult" {
			result := input[j]
			// Keep only results belonging to this assistant message. A stale
			// or duplicate tool result is invalid in the OpenAI/Kimi message
			// protocol and must not be replayed.
			if _, ok := calls[result.ToolCallID]; ok {
				if _, duplicate := results[result.ToolCallID]; !duplicate {
					results[result.ToolCallID] = result
				}
			}
			j++
		}
		// Emit results in the same order as the assistant tool_calls. This is
		// required by Kimi and avoids relying on map iteration order.
		for _, block := range msg.Contents {
			if block.Type != "toolCall" || block.ToolCall == nil || block.ToolCall.ID == "" {
				continue
			}
			id, name := block.ToolCall.ID, block.ToolCall.Name
			if result, ok := results[id]; ok {
				out = append(out, result)
			} else {
				out = append(out, provider.NewToolResultMessage(id, name,
					"[tool result unavailable: the previous tool execution was interrupted]", true))
			}
		}
		i = j - 1
	}
	return out
}

func (p *Provider) openAIImage(image *provider.ImageContent) *openAIImage {
	if image == nil {
		return nil
	}
	result := &openAIImage{URL: fmt.Sprintf("data:%s;base64,%s", image.MimeType, image.Data)}
	if p.supportsImageDetail() {
		result.Detail = normalizeImageDetail(image.Detail)
	}
	return result
}

// mediaSourceURL renders inline media as a data URL for OpenAI-compatible
// media reference fields. Qwen/DashScope-family gateways accept data URLs (and
// remote URLs) in input_audio.data and video_url.url. Runtime materialization
// always supplies inline bytes, so the URL field is only a passthrough for
// externally referenced media.
func mediaSourceURL(mimeType, data, url string) string {
	if data == "" {
		return url
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, data)
}

// openAIInputAudioPart builds the input_audio payload for one audio block.
func openAIInputAudioPart(audio *provider.AudioContent) *openAIInputAudio {
	if audio == nil {
		return nil
	}
	return &openAIInputAudio{Data: mediaSourceURL(audio.MimeType, audio.Data, audio.URL), Format: audioInputFormat(audio)}
}

// audioInputFormat resolves the wire format name for an input_audio part,
// preferring the block's explicit format hint.
func audioInputFormat(audio *provider.AudioContent) string {
	if format := strings.TrimSpace(audio.Format); format != "" {
		return format
	}
	base := strings.ToLower(strings.TrimSpace(strings.Split(audio.MimeType, ";")[0]))
	switch base {
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return "wav"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	default:
		return strings.TrimPrefix(strings.TrimPrefix(base, "audio/"), "x-")
	}
}

func (p *Provider) supportsImageDetail() bool {
	if p == nil {
		return false
	}
	u, err := url.Parse(p.baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.openai.com" || host == "api.x.ai"
}

func normalizeImageDetail(detail string) string {
	switch strings.ToLower(strings.TrimSpace(detail)) {
	case "fast", "low":
		return "low"
	case "auto":
		return "auto"
	case "detail", "high":
		return "high"
	case "raw", "original":
		return "high"
	default:
		return ""
	}
}

func (p *Provider) convertTools(tools []provider.ToolDefinition) []openAITool {
	var result []openAITool
	for _, t := range tools {
		if t.Kind == "hosted" {
			continue
		}
		result = append(result, openAITool{Type: "function", Function: openAIFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters}})
	}
	return result
}

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/ua"
)

// responsesRequest represents the request body for OpenAI Responses API.
type responsesRequest struct {
	Model                string                       `json:"model"`
	Instructions         string                       `json:"instructions,omitempty"`
	Input                []responsesInputItem         `json:"input"`
	Tools                []responsesTool              `json:"tools,omitempty"`
	MaxOutputTokens      int                          `json:"max_output_tokens,omitempty"`
	Temperature          *float64                     `json:"temperature,omitempty"`
	TopP                 *float64                     `json:"top_p,omitempty"`
	Store                *bool                        `json:"store,omitempty"`
	PreviousResponseID   string                       `json:"previous_response_id,omitempty"`
	Conversation         string                       `json:"conversation,omitempty"`
	Truncation           string                       `json:"truncation,omitempty"`
	Stream               bool                         `json:"stream"`
	Background           bool                         `json:"background,omitempty"`
	Include              []string                     `json:"include,omitempty"`
	Reasoning            *responsesReasoning          `json:"reasoning,omitempty"`
	ParallelToolCalls    *bool                        `json:"parallel_tool_calls,omitempty"`
	MaxToolCalls         int                          `json:"max_tool_calls,omitempty"`
	ToolChoice           interface{}                  `json:"tool_choice,omitempty"`
	Text                 *responsesText               `json:"text,omitempty"`
	PromptCacheKey       string                       `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string                       `json:"prompt_cache_retention,omitempty"`
	PromptCacheOptions   *responsesPromptCacheOptions `json:"prompt_cache_options,omitempty"`
	ServiceTier          string                       `json:"service_tier,omitempty"`
	Metadata             map[string]string            `json:"metadata,omitempty"`
	SafetyIdentifier     string                       `json:"safety_identifier,omitempty"`
}

type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
	Context string `json:"context,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type responsesPromptCacheOptions struct {
	Mode string `json:"mode,omitempty"`
	TTL  string `json:"ttl,omitempty"`
}

type responsesText struct {
	Format *responsesTextFormat `json:"format,omitempty"`
}

type responsesTextFormat struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

type responsesInputItem struct {
	Raw       json.RawMessage `json:"-"`
	Type      string          `json:"type,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   interface{}     `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Input     string          `json:"input,omitempty"`
	Output    any             `json:"output,omitempty"`
}

func (i responsesInputItem) MarshalJSON() ([]byte, error) {
	if len(i.Raw) > 0 {
		if !json.Valid(i.Raw) {
			return nil, fmt.Errorf("invalid raw Responses input item")
		}
		return i.Raw, nil
	}
	type wireItem responsesInputItem
	return json.Marshal(wireItem(i))
}

type responsesContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	FileURL  string `json:"file_url,omitempty"`
	FileData string `json:"file_data,omitempty"`
	Filename string `json:"filename,omitempty"`
	// AudioURL/VideoURL follow the Responses media shape used by
	// Qwen/DashScope-family gateways (input_audio.audio_url + format,
	// input_video.video_url); both accept data URLs for inline payloads.
	AudioURL string `json:"audio_url,omitempty"`
	Format   string `json:"format,omitempty"`
	VideoURL string `json:"video_url,omitempty"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Format      json.RawMessage `json:"format,omitempty"`
	Extra       map[string]any  `json:"-"`
}

func (t responsesTool) MarshalJSON() ([]byte, error) {
	type wireTool struct {
		Type        string          `json:"type"`
		Name        string          `json:"name,omitempty"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
		Format      json.RawMessage `json:"format,omitempty"`
	}
	raw, err := json.Marshal(wireTool{
		Type:        t.Type,
		Name:        t.Name,
		Description: t.Description,
		Parameters:  t.Parameters,
		Format:      t.Format,
	})
	if err != nil {
		return nil, err
	}
	if len(t.Extra) == 0 {
		return raw, nil
	}
	var merged map[string]any
	if err := json.Unmarshal(raw, &merged); err != nil {
		return nil, err
	}
	for key, value := range t.Extra {
		if key == "type" || key == "name" || key == "description" || key == "parameters" {
			continue
		}
		merged[key] = value
	}
	return json.Marshal(merged)
}

type responsesSSEEvent struct {
	Type        string                    `json:"type"`
	Delta       string                    `json:"delta,omitempty"`
	Text        string                    `json:"text,omitempty"`
	Refusal     string                    `json:"refusal,omitempty"`
	Arguments   json.RawMessage           `json:"arguments,omitempty"`
	Input       string                    `json:"input,omitempty"`
	ItemID      string                    `json:"item_id,omitempty"`
	CallID      string                    `json:"call_id,omitempty"`
	OutputIndex int                       `json:"output_index,omitempty"`
	Item        *responsesOutputItem      `json:"item,omitempty"`
	Response    *responsesCompletedObject `json:"response,omitempty"`
	Error       *responsesError           `json:"error,omitempty"`
}

type responsesOutputItem struct {
	ID        string          `json:"id,omitempty"`
	Type      string          `json:"type,omitempty"`
	Status    string          `json:"status,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Input     string          `json:"input,omitempty"`
}

type responsesCompletedObject struct {
	ID                 string            `json:"id,omitempty"`
	Status             string            `json:"status,omitempty"`
	PreviousResponseID string            `json:"previous_response_id,omitempty"`
	ConversationID     string            `json:"-"`
	ConversationRaw    json.RawMessage   `json:"conversation,omitempty"`
	Output             []json.RawMessage `json:"output,omitempty"`
	Usage              *responsesUsage   `json:"usage,omitempty"`
	Error              *responsesError   `json:"error,omitempty"`
	IncompleteDetails  *struct {
		Reason string `json:"reason,omitempty"`
	} `json:"incomplete_details,omitempty"`
}

type responsesError struct {
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
	Type    string `json:"type,omitempty"`
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details,omitempty"`
}

func (p *Provider) chatResponses(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
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
		if err := p.validateResponsesCapabilitiesForRequest(model, params, false); err != nil {
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: err, StopReason: "error"}
			return
		}

		reqBody, err := p.buildResponsesRequest(params, modelID, model, true, false)
		if err != nil {
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: err, StopReason: "error"}
			return
		}
		if timeout := p.responsesHostedTimeout(); timeout > 0 && responsesRequestHasCodeInterpreter(reqBody) {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		diagnostics := responsesRequestDiagnostics(params, reqBody)

		body, err := json.Marshal(reqBody)
		if err != nil {
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("marshal request: %w", err)}
			return
		}
		provider.DebugJSON("OpenAI Responses request JSON", body)

		maxRetries := 0
		baseDelayMs := 2000
		if p.retryConfig != nil && p.retryConfig.Enabled {
			maxRetries = p.retryConfig.MaxRetries
			baseDelayMs = p.retryConfig.BaseDelayMs
		}

		for attempt := 0; attempt <= maxRetries; attempt++ {
			if err := ctx.Err(); err != nil {
				ch <- provider.StreamEvent{Type: provider.StreamError, Error: err, StopReason: "aborted"}
				return
			}

			req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/responses", bytes.NewReader(body))
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
				provider.DebugJSON("OpenAI Responses response JSON", bodyBytes)
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
			visibleOutput, err := p.parseResponsesSSE(ctx, streamBody, ch, params, diagnostics)
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

func responsesRequestHasCodeInterpreter(req responsesRequest) bool {
	for _, tool := range req.Tools {
		if tool.Type == "code_interpreter" {
			return true
		}
	}
	return false
}

func (p *Provider) buildResponsesRequest(params provider.ChatParams, modelID string, model *provider.Model, stream, background bool) (responsesRequest, error) {
	input := p.convertResponsesInput(params)
	if params.ResponseOptions != nil && len(params.ResponseOptions.ReplayItems) > 0 {
		var err error
		input, err = nativeResponsesReplayInput(params.ResponseOptions.ReplayItems)
		if err != nil {
			return responsesRequest{}, err
		}
	}
	reqBody := responsesRequest{
		Model:        modelID,
		Instructions: params.SystemPrompt,
		Input:        input,
		Tools:        p.mergeResponsesTools(p.convertResponsesTools(params.Tools)),
		Temperature:  params.Temperature,
		TopP:         params.TopP,
		Stream:       stream,
		Background:   background,
	}
	p.applyResponsesConfig(&reqBody, params.ResponseOptions)
	applyResponsesOptions(&reqBody, params.ResponseOptions)
	if params.ResponseOptions != nil && strings.TrimSpace(params.ResponseOptions.PreviousResponseID) != "" {
		reqBody.PreviousResponseID = strings.TrimSpace(params.ResponseOptions.PreviousResponseID)
		reqBody.Conversation = ""
	}
	if params.MaxTokens > 0 {
		reqBody.MaxOutputTokens = params.MaxTokens
	}

	if p.responsesConfig != nil && p.responsesConfig.promptCacheEnabled && supportsPromptCacheKey(model) {
		reqBody.PromptCacheKey = p.responsesPromptCacheKey(modelID)
		if supportsPromptCacheRetention(model) {
			reqBody.PromptCacheRetention = p.responsesConfig.promptCacheRetention
		}
		if p.responsesConfig.promptCacheMode != "" || p.responsesConfig.promptCacheTTL != "" {
			reqBody.PromptCacheOptions = &responsesPromptCacheOptions{
				Mode: p.responsesConfig.promptCacheMode,
				TTL:  p.responsesConfig.promptCacheTTL,
			}
		}
	}

	if !p.disableReasoning && params.ThinkingLevel != provider.ThinkingOff && model != nil && model.Reasoning {
		reqBody.Reasoning = &responsesReasoning{
			Effort:  responsesReasoningEffort(params.ThinkingLevel),
			Summary: p.responsesReasoningSummary(model),
		}
	}
	if p.responsesConfig != nil && (p.responsesConfig.reasoningContext != "" || p.responsesConfig.reasoningMode != "") {
		if reqBody.Reasoning == nil {
			reqBody.Reasoning = &responsesReasoning{}
		}
		reqBody.Reasoning.Context = p.responsesConfig.reasoningContext
		reqBody.Reasoning.Mode = p.responsesConfig.reasoningMode
	}

	// Responses-API reasoning models reject temperature/top_p, and some
	// models reject sampling parameters entirely (compat flag).
	if reqBody.Reasoning != nil || provider.SamplingParamsDisabled(model) {
		reqBody.Temperature = nil
		reqBody.TopP = nil
	}
	return reqBody, nil
}

// responsesRequestDiagnostics describes intentional local field omissions. It
// is emitted on StreamStart so callers can audit compatibility decisions
// without changing the request contract or persisting sensitive request data.
func responsesRequestDiagnostics(params provider.ChatParams, req responsesRequest) []map[string]any {
	var result []map[string]any
	if (params.Temperature != nil || params.TopP != nil) && req.Temperature == nil && req.TopP == nil {
		reason := "model_compat"
		if req.Reasoning != nil {
			reason = "reasoning_incompatible"
		}
		result = append(result, map[string]any{
			"field":  "temperature/top_p",
			"action": "omitted",
			"reason": reason,
		})
	}
	if params.ResponseOptions != nil && params.ResponseOptions.SuppressConversation && req.Conversation == "" {
		result = append(result, map[string]any{
			"field":  "conversation",
			"action": "omitted",
			"reason": "remote_state_replay_fallback",
		})
	}
	return result
}

func nativeResponsesReplayInput(items []json.RawMessage) ([]responsesInputItem, error) {
	result := make([]responsesInputItem, 0, len(items))
	for index, raw := range items {
		if len(raw) == 0 || !json.Valid(raw) {
			return nil, fmt.Errorf("Responses replay item %d is not valid JSON", index)
		}
		if len(raw) > responsesMaxCanonicalItemBytes {
			return nil, fmt.Errorf("Responses replay item %d exceeds %d bytes", index, responsesMaxCanonicalItemBytes)
		}
		result = append(result, responsesInputItem{Raw: cloneRawMessage(raw)})
	}
	return result, nil
}

func (p *Provider) applyResponsesConfig(req *responsesRequest, opts *provider.ResponseOptions) {
	if p.responsesConfig == nil {
		return
	}
	req.Store = p.responsesConfig.store
	req.Truncation = p.responsesConfig.truncation
	req.Include = cloneStringSlice(p.responsesConfig.include)
	req.ServiceTier = p.responsesConfig.serviceTier
	req.Metadata = cloneStringMap(p.responsesConfig.metadata)
	req.SafetyIdentifier = p.responsesConfig.safetyIdentifier
	req.Text = responsesTextOptionFromFormat(p.responsesConfig.structuredOutput)
	req.ToolChoice = p.responsesConfig.toolChoice
	req.ParallelToolCalls = cloneBoolPtr(p.responsesConfig.parallelToolCalls)
	req.MaxToolCalls = p.responsesConfig.maxToolCalls
	if p.responsesConfig.stateMode == "conversation" && p.responsesConfig.conversation != "" &&
		(opts == nil || !opts.SuppressConversation) {
		req.Conversation = p.responsesConfig.conversation
	}
}

func applyResponsesOptions(req *responsesRequest, opts *provider.ResponseOptions) {
	if opts == nil {
		return
	}
	if opts.ParallelTools != nil {
		req.ParallelToolCalls = opts.ParallelTools
	}
	if opts.MaxToolCalls != nil && *opts.MaxToolCalls > 0 {
		req.MaxToolCalls = *opts.MaxToolCalls
	}
	if opts.PreviousResponseID != "" {
		req.PreviousResponseID = strings.TrimSpace(opts.PreviousResponseID)
	}
	if opts.ToolChoice != nil {
		req.ToolChoice = responsesToolChoice(opts.ToolChoice)
	}
	if opts.StructuredOutput != nil {
		req.Text = responsesTextOption(opts.StructuredOutput)
	}
}

func responsesToolChoice(choice *provider.ToolChoice) interface{} {
	if choice == nil || choice.Type == "" {
		return nil
	}
	switch choice.Type {
	case "function", "custom":
		if choice.Name == "" {
			return nil
		}
		return map[string]any{"type": choice.Type, "name": choice.Name}
	default:
		return choice.Type
	}
}

func responsesTextOption(opts *provider.StructuredOutputOptions) *responsesText {
	if opts == nil {
		return nil
	}
	formatType := opts.Format
	if formatType == "" {
		if len(opts.Schema) > 0 {
			formatType = "json_schema"
		} else {
			formatType = "text"
		}
	}
	format := &responsesTextFormat{
		Type:        formatType,
		Name:        opts.Name,
		Description: opts.Description,
		Schema:      cloneRawMessage(opts.Schema),
	}
	if opts.Strict {
		format.Strict = &opts.Strict
	}
	return &responsesText{Format: format}
}

func responsesTextOptionFromFormat(format *responsesTextFormat) *responsesText {
	if format == nil {
		return nil
	}
	return &responsesText{Format: &responsesTextFormat{
		Type:        format.Type,
		Name:        format.Name,
		Description: format.Description,
		Strict:      cloneBoolPtr(format.Strict),
		Schema:      cloneRawMessage(format.Schema),
	}}
}

func cloneStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func cloneRawMessage(src json.RawMessage) json.RawMessage {
	if src == nil {
		return nil
	}
	dst := make(json.RawMessage, len(src))
	copy(dst, src)
	return dst
}

func cloneBoolPtr(src *bool) *bool {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func (p *Provider) convertResponsesInput(params provider.ChatParams) []responsesInputItem {
	messages := params.Messages
	if maxImages := p.maxImagesPerRequestForRequest(params); maxImages > 0 {
		messages = limitImageHistory(messages, maxImages)
	}
	items := make([]responsesInputItem, 0, len(messages))
	var pendingImages []responsesContentBlock
	flushImages := func() {
		if len(pendingImages) == 0 {
			return
		}
		items = append(items, responsesInputItem{Type: "message", Role: "user", Content: pendingImages})
		pendingImages = nil
	}
	for _, msg := range messages {
		if msg.Role != "toolResult" {
			flushImages()
		}
		switch msg.Role {
		case "toolResult":
			itemType := "function_call_output"
			output := any(responseToolOutput(msg))
			if msg.ToolKind == "custom" {
				itemType = "custom_tool_call_output"
				output = responseCustomToolOutput(msg)
			}
			items = append(items, responsesInputItem{Type: itemType, CallID: msg.ToolCallID, Output: output})
			// Responses API function_call_output is text-only. Preserve images
			// as a following user message, but only after the complete run of
			// function outputs (handled by the normal input ordering).
			if msg.ToolKind != "custom" {
				for _, c := range msg.Contents {
					if c.Type == "image" && c.Image != nil {
						pendingImages = append(pendingImages, responsesContentBlock{Type: "input_image", ImageURL: fmt.Sprintf("data:%s;base64,%s", c.Image.MimeType, c.Image.Data)})
					}
				}
			}
		case "assistant":
			content := p.responsesMessageContent(msg, "output_text")
			if content != nil {
				items = append(items, responsesInputItem{Type: "message", Role: "assistant", Content: content})
			}
			for _, c := range msg.Contents {
				if c.Type == "toolCall" && c.ToolCall != nil {
					if c.ToolCall.Kind == "custom" {
						items = append(items, responsesInputItem{Type: "custom_tool_call", CallID: c.ToolCall.ID, Name: c.ToolCall.Name, Input: c.ToolCall.Input})
					} else {
						items = append(items, responsesInputItem{Type: "function_call", CallID: c.ToolCall.ID, Name: c.ToolCall.Name, Arguments: string(c.ToolCall.Arguments)})
					}
				}
			}
		default:
			role := msg.Role
			if role == "" {
				role = "user"
			}
			content := p.responsesMessageContent(msg, "input_text")
			items = append(items, responsesInputItem{Type: "message", Role: role, Content: content})
		}
	}
	flushImages()
	return items
}

func (p *Provider) responsesMessageContent(msg provider.Message, textType string) interface{} {
	if len(msg.Contents) == 0 {
		return []responsesContentBlock{{Type: textType, Text: msg.Content}}
	}
	blocks := make([]responsesContentBlock, 0, len(msg.Contents))
	for _, c := range msg.Contents {
		switch c.Type {
		case "text":
			blocks = append(blocks, responsesContentBlock{Type: textType, Text: c.Text})
		case "image":
			if c.Image != nil {
				block := responsesContentBlock{Type: "input_image", ImageURL: fmt.Sprintf("data:%s;base64,%s", c.Image.MimeType, c.Image.Data)}
				if p.supportsImageDetail() {
					block.Detail = normalizeImageDetail(c.Image.Detail)
				}
				blocks = append(blocks, block)
			}
		case "file":
			if c.File != nil {
				blocks = append(blocks, responsesContentBlock{Type: "input_file", FileID: c.File.ID, FileURL: c.File.URL, FileData: c.File.Data, Filename: c.File.Filename})
			}
		case "audio":
			if c.Audio != nil {
				blocks = append(blocks, responsesContentBlock{Type: "input_audio", AudioURL: mediaSourceURL(c.Audio.MimeType, c.Audio.Data, c.Audio.URL), Format: audioInputFormat(c.Audio)})
			}
		case "video":
			if c.Video != nil {
				blocks = append(blocks, responsesContentBlock{Type: "input_video", VideoURL: mediaSourceURL(c.Video.MimeType, c.Video.Data, c.Video.URL)})
			}
		}
	}
	if len(blocks) == 0 && msg.Content != "" {
		blocks = append(blocks, responsesContentBlock{Type: textType, Text: msg.Content})
	}
	return blocks
}

func responseToolOutput(msg provider.Message) string {
	if msg.Content != "" || len(msg.Contents) == 0 {
		return msg.Content
	}
	var parts []string
	for _, c := range msg.Contents {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// responseCustomToolOutput preserves the Responses custom tool content-list
// contract. Function outputs retain their historical text-only encoding for
// compatibility with OpenAI-compatible gateways.
func responseCustomToolOutput(msg provider.Message) any {
	if len(msg.Contents) == 0 {
		return msg.Content
	}
	content := make([]responsesContentBlock, 0, len(msg.Contents)+1)
	for _, block := range msg.Contents {
		switch block.Type {
		case "text":
			if block.Text != "" {
				content = append(content, responsesContentBlock{Type: "input_text", Text: block.Text})
			}
		case "image":
			if block.Image != nil && block.Image.Data != "" {
				content = append(content, responsesContentBlock{
					Type:     "input_image",
					ImageURL: fmt.Sprintf("data:%s;base64,%s", block.Image.MimeType, block.Image.Data),
					Detail:   normalizeImageDetail(block.Image.Detail),
				})
			}
		case "file":
			if block.File != nil && (block.File.ID != "" || block.File.URL != "" || block.File.Data != "") {
				content = append(content, responsesContentBlock{Type: "input_file", FileID: block.File.ID, FileURL: block.File.URL, FileData: block.File.Data, Filename: block.File.Filename})
			}
		}
	}
	if len(content) == 0 {
		return msg.Content
	}
	if msg.Content != "" && len(content) == 1 && content[0].Type != "input_text" {
		content = append([]responsesContentBlock{{Type: "input_text", Text: msg.Content}}, content...)
	}
	return content
}

func (p *Provider) convertResponsesTools(tools []provider.ToolDefinition) []responsesTool {
	result := make([]responsesTool, 0, len(tools))
	seenHosted := make(map[string]struct{})
	for _, t := range tools {
		if t.Kind == "hosted" {
			toolType := provider.HostedToolType(t.ProviderType, t.Name)
			if toolType == "" {
				continue
			}
			if _, exists := seenHosted[toolType]; exists {
				continue
			}
			seenHosted[toolType] = struct{}{}
			result = append(result, responsesTool{Type: toolType})
			continue
		}
		if t.Kind == "custom" {
			result = append(result, responsesTool{Type: "custom", Name: t.Name, Description: t.Description, Format: cloneRawMessage(t.Format)})
			continue
		}
		result = append(result, responsesTool{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	return result
}

// responsesEventError extracts the most specific error detail from a
// response.failed / error SSE event. Some OpenAI-compatible servers (e.g.
// Kimi) nest the failure reason inside the response object rather than the
// top-level error field, so both locations are checked.
func responsesEventError(event responsesSSEEvent) error {
	err := event.Error
	if err == nil && event.Response != nil {
		err = event.Response.Error
	}
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(err.Message)
	if detail == "" {
		detail = strings.TrimSpace(err.Code)
	}
	if detail == "" {
		detail = strings.TrimSpace(err.Type)
	}
	if detail == "" {
		return nil
	}
	return fmt.Errorf("responses error: %s", detail)
}

func (p *Provider) parseResponsesSSE(ctx context.Context, body io.Reader, ch chan<- provider.StreamEvent, params provider.ChatParams, diagnostics []map[string]any) (bool, error) {
	var (
		textContent   strings.Builder
		reasoning     strings.Builder
		usage         *provider.Usage
		stopReason    string
		visibleOutput bool
		completed     bool
	)
	normalizer := newResponsesNormalizer()
	if p.responsesConfig != nil {
		normalizer.hostedPolicies = p.responsesConfig.hostedPolicies
	}
	defer p.archiveResponsesTurn(params, normalizer)

	start := provider.StreamEvent{Type: provider.StreamStart}
	if len(diagnostics) > 0 {
		start.Metadata = map[string]any{"responsesRequestDiagnostics": diagnostics}
	}
	ch <- start
	defer func() {
		toolCalls := make([]provider.ToolCallBlock, 0)
		for _, call := range normalizer.toolCalls() {
			id := call.ID
			if id == "" {
				id = call.ItemID
			}
			if id == "" {
				id = provider.NextToolCallFallbackID("openai_toolcall")
			}
			toolCalls = append(toolCalls, provider.ToolCallBlock{
				ID:        id,
				Name:      call.Name,
				Kind:      call.Kind,
				Input:     call.Input,
				Arguments: cloneRawMessage(call.Arguments),
			})
		}
		provider.DebugCompleteResponse(provider.DebugResponse{
			Provider: "openai", API: "responses", Content: textContent.String(),
			Reasoning: reasoning.String(), ToolCalls: toolCalls, StopReason: stopReason, Usage: usage,
		})
	}()

	decodeErr := decodeResponsesSSE(body, func(frame responsesSSEFrame) error {
		select {
		case <-ctx.Done():
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err(), StopReason: "aborted"}
			return errResponsesAbort
		case <-params.Abort:
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("aborted"), StopReason: "aborted"}
			return errResponsesAbort
		default:
		}

		data := strings.TrimSpace(frame.Data)
		if data == "[DONE]" {
			return errResponsesStop
		}

		var event responsesSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			eventType := frame.Event
			if eventType == "" {
				eventType = "unknown"
			}
			return fmt.Errorf("responses event %d (%s): invalid JSON: %w", frame.Sequence, eventType, err)
		}
		if event.Type == "" {
			event.Type = frame.Event
		}
		if event.Type == "" {
			return fmt.Errorf("responses event %d: missing event type", frame.Sequence)
		}
		if err := normalizer.apply(event, []byte(data)); err != nil {
			return fmt.Errorf("responses event %d (%s): %w", frame.Sequence, event.Type, err)
		}
		if (event.Type == "response.output_item.added" || event.Type == "response.output_item.done") && event.Item != nil && isHostedResponsesItemType(event.Item.Type) {
			ch <- provider.StreamEvent{
				Type: provider.StreamHostedItem, ProviderEventType: event.Type,
				ItemID: event.Item.ID, HostedItem: &provider.HostedItem{
					ID: event.Item.ID, Type: event.Item.Type, Status: event.Item.Status,
					OutputIndex: event.OutputIndex,
				},
			}
		}

		base := func(eventType string) provider.StreamEvent {
			return provider.StreamEvent{
				ProviderEventType: eventType,
				ItemID:            event.ItemID,
				CallID:            event.CallID,
			}
		}
		if err := normalizer.unsupportedError(); err != nil {
			streamEvent := base(event.Type)
			streamEvent.Type = provider.StreamError
			streamEvent.Error = err
			streamEvent.StopReason = "error"
			ch <- streamEvent
			return errResponsesAbort
		}
		if err := normalizer.hostedPolicyError(); err != nil {
			streamEvent := base(event.Type)
			streamEvent.Type = provider.StreamError
			streamEvent.Error = err
			streamEvent.StopReason = "error"
			ch <- streamEvent
			return errResponsesAbort
		}

		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				visibleOutput = true
				textContent.WriteString(event.Delta)
				streamEvent := base(event.Type)
				streamEvent.Type = provider.StreamTextDelta
				streamEvent.TextDelta = event.Delta
				ch <- streamEvent
			}
		case "response.output_text.done":
			// Some gateways omit deltas and only send the terminal text. When
			// deltas were already received, the done payload is a duplicate.
			if event.Text != "" && textContent.Len() == 0 {
				visibleOutput = true
				textContent.WriteString(event.Text)
				streamEvent := base(event.Type)
				streamEvent.Type = provider.StreamTextDelta
				streamEvent.TextDelta = event.Text
				ch <- streamEvent
			}
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			if !p.disableReasoning && event.Delta != "" {
				visibleOutput = true
				reasoning.WriteString(event.Delta)
				streamEvent := base(event.Type)
				streamEvent.Type = provider.StreamThinkDelta
				streamEvent.ThinkDelta = event.Delta
				ch <- streamEvent
			}
		case "response.reasoning_text.done", "response.reasoning_summary_text.done":
			if !p.disableReasoning && event.Text != "" && reasoning.Len() == 0 {
				visibleOutput = true
				reasoning.WriteString(event.Text)
				streamEvent := base(event.Type)
				streamEvent.Type = provider.StreamThinkDelta
				streamEvent.ThinkDelta = event.Text
				ch <- streamEvent
			}
		case "response.refusal.delta":
			if event.Delta != "" {
				visibleOutput = true
				streamEvent := base(event.Type)
				streamEvent.Type = provider.StreamTextDelta
				streamEvent.TextDelta = event.Delta
				streamEvent.Metadata = map[string]any{"refusal": true}
				ch <- streamEvent
			}
		case "response.refusal.done":
			refusal := event.Text
			if refusal == "" {
				refusal = event.Refusal
			}
			if refusal != "" && textContent.Len() == 0 {
				visibleOutput = true
				streamEvent := base(event.Type)
				streamEvent.Type = provider.StreamTextDelta
				streamEvent.TextDelta = refusal
				streamEvent.Metadata = map[string]any{"refusal": true}
				ch <- streamEvent
			}
		case "response.function_call_arguments.delta", "response.function_call_arguments.done":
			visibleOutput = true
		case "response.output_item.done":
			if event.Item != nil && event.Item.Type == "function_call" {
				visibleOutput = true
			}
		case "response.completed":
			completed = true
			if event.Response != nil {
				usage = convertResponsesUsage(event.Response.Usage)
				stopReason = responseStopReason(event.Response.Status)
			}
			if err := responsesEventError(event); err != nil {
				return err
			}
			// response.completed is terminal. Do not wait for EOF or [DONE].
			return errResponsesStop
		case "response.incomplete":
			completed = true
			if event.Response != nil {
				usage = convertResponsesUsage(event.Response.Usage)
				stopReason = responseStopReason(event.Response.Status)
				if stopReason == "" {
					stopReason = "incomplete"
				}
			} else {
				stopReason = "incomplete"
			}
			return errResponsesStop
		case "response.failed", "error":
			err := responsesEventError(event)
			if err == nil {
				err = fmt.Errorf("responses stream failed")
			}
			return err
		}
		return nil
	})

	switch decodeErr {
	case errResponsesStop:
		// Normal terminal event. Continue with the normalized final state.
	case errResponsesAbort:
		return true, nil
	default:
		if decodeErr != nil {
			return visibleOutput, decodeErr
		}
	}

	for _, call := range normalizer.toolCalls() {
		id := call.ID
		if id == "" {
			id = call.ItemID
		}
		if id == "" {
			id = provider.NextToolCallFallbackID("openai_toolcall")
		}
		tc := &provider.ToolCallBlock{
			ID:        id,
			Name:      call.Name,
			Kind:      call.Kind,
			Input:     call.Input,
			Arguments: cloneRawMessage(call.Arguments),
		}
		visibleOutput = true
		event := provider.StreamEvent{
			Type:              provider.StreamToolCall,
			ProviderEventType: "response.output_item.done",
			ItemID:            call.ItemID,
			CallID:            call.ID,
			ToolCall:          tc,
		}
		ch <- event
	}
	if usage != nil {
		visibleOutput = true
		ch <- provider.StreamEvent{Type: provider.StreamUsage, Usage: usage}
	}
	attachments := normalizer.attachments()
	if stopReason == "" && len(normalizer.toolCalls()) > 0 {
		stopReason = "tool_calls"
	}
	if completed && stopReason == "" {
		stopReason = "stop"
	}
	ch <- provider.StreamEvent{
		Type:        provider.StreamDone,
		StopReason:  stopReason,
		Metadata:    normalizer.metadata(),
		Attachments: attachments,
	}
	return visibleOutput, nil
}

func isHostedResponsesItemType(itemType string) bool {
	for _, hostedType := range responsesHostedItemTypes {
		if itemType == hostedType {
			return true
		}
	}
	return false
}

func (p *Provider) archiveResponsesTurn(params provider.ChatParams, normalizer *responsesNormalizer) {
	if params.ResponseOptions == nil || params.ResponseOptions.ResponseArchive == nil || normalizer == nil {
		return
	}
	response := normalizer.response
	if response.ID == "" && len(response.Items) == 0 {
		return
	}
	items := make([]provider.ResponseArchiveItem, 0, len(response.Items))
	for _, item := range response.Items {
		if item == nil || item.Type == "" {
			continue
		}
		items = append(items, provider.ResponseArchiveItem{
			ID: item.ID, Type: item.Type, Status: item.Status, OutputIndex: item.OutputIndex,
			Canonical: cloneRawMessage(item.Canonical),
		})
	}
	params.ResponseOptions.ResponseArchive(provider.ResponseArchive{
		ResponseID: response.ID, Status: response.Status, PreviousResponseID: response.PreviousResponseID,
		ConversationID: response.ConversationID, IncompleteReason: response.IncompleteReason,
		StateMode: p.ResponseStateMode(), Usage: convertResponsesUsage(response.Usage), Items: items,
		Attachments: normalizer.attachments(), UnknownEventTypes: append([]string(nil), response.UnknownEventTypes...),
	})
}

func responsesToolKey(itemID string, outputIndex int) string {
	if itemID != "" {
		return itemID + "#" + strconv.Itoa(outputIndex)
	}
	return strconv.Itoa(outputIndex)
}

func convertResponsesUsage(u *responsesUsage) *provider.Usage {
	if u == nil {
		return nil
	}
	usage := &provider.Usage{Input: u.InputTokens, Output: u.OutputTokens, TotalTokens: u.TotalTokens}
	if u.InputTokensDetails != nil {
		usage.CacheRead = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil {
		usage.Reasoning = u.OutputTokensDetails.ReasoningTokens
	}
	return usage
}

func responsesReasoningEffort(level provider.ThinkingLevel) string {
	switch level {
	case provider.ThinkingOff:
		return ""
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

func (p *Provider) responsesReasoningSummary(model *provider.Model) string {
	if !supportsReasoningSummary(model) {
		return ""
	}
	if p.responsesConfig == nil {
		return "auto"
	}
	if p.responsesConfig.reasoningSummary == "none" || p.responsesConfig.reasoningSummary == "off" {
		return ""
	}
	if p.responsesConfig.reasoningSummary != "" {
		return p.responsesConfig.reasoningSummary
	}
	return "auto"
}

func (p *Provider) responsesPromptCacheKey(modelID string) string {
	if p.responsesConfig == nil {
		return ""
	}
	if p.responsesConfig.promptCacheKey != "" {
		return p.responsesConfig.promptCacheKey
	}
	if modelID == "" {
		return ""
	}
	return "vibecoding:" + strings.TrimPrefix(strings.TrimPrefix(p.baseURL, "https://"), "http://") + ":" + modelID
}

func supportsPromptCacheKey(model *provider.Model) bool {
	if model != nil && model.Compat != nil && model.Compat.SupportsPromptCacheKey != nil {
		return *model.Compat.SupportsPromptCacheKey
	}
	return true
}

func supportsPromptCacheRetention(model *provider.Model) bool {
	if model != nil && model.Compat != nil && model.Compat.SupportsLongCacheRetention != nil {
		return *model.Compat.SupportsLongCacheRetention
	}
	return true
}

func supportsReasoningSummary(model *provider.Model) bool {
	if model != nil && model.Compat != nil && model.Compat.SupportsReasoningSummary != nil {
		return *model.Compat.SupportsReasoningSummary
	}
	return true
}

func responseStopReason(status string) string {
	switch status {
	case "completed":
		return "stop"
	case "incomplete":
		return "length"
	case "failed":
		return "error"
	default:
		return status
	}
}

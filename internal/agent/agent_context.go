package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/startvibecoding/mothx/internal/config"
	ctxpkg "github.com/startvibecoding/mothx/internal/context"
	"github.com/startvibecoding/mothx/internal/provider"
)

const defaultAutoCompactionThreshold = 0.80

// supportsImages checks if the model supports image input.
func (a *Agent) supportsImages() bool {
	if a.config.Model == nil {
		return false
	}
	for _, input := range a.config.Model.Input {
		if input == "image" {
			return true
		}
	}
	return false
}

const unsupportedImageToolResultMessage = "tool result contains image content, but the selected model does not support image input; select a vision-capable model to continue"

func containsImageContent(contents []provider.ContentBlock) bool {
	for _, content := range contents {
		if content.Type == "image" || content.Image != nil {
			return true
		}
	}
	return false
}

// gateToolResultImages converts an image-bearing tool result into an explicit
// tool error when the selected model cannot accept image input. This gate is
// applied at the Agent Core boundary, before the result is persisted or sent
// to the provider, so unsupported images are never silently discarded.
func (a *Agent) gateToolResultImages(content string, contents []provider.ContentBlock, isError bool) (string, []provider.ContentBlock, bool, error) {
	if a.supportsImages() || !containsImageContent(contents) {
		return content, contents, isError, nil
	}
	return unsupportedImageToolResultMessage, nil, true, errors.New(unsupportedImageToolResultMessage)
}

// toolResultImages extracts the image payloads of a rich tool result so event
// consumers can project them (for example ACP tool_call_update image content)
// without re-parsing provider messages. The extracted data is the exact
// base64 payload the tool produced; no re-encoding happens here.
func toolResultImages(contents []provider.ContentBlock) []ToolImage {
	var images []ToolImage
	for _, content := range contents {
		if content.Type != "image" || content.Image == nil || content.Image.Data == "" {
			continue
		}
		images = append(images, ToolImage{MimeType: content.Image.MimeType, Data: content.Image.Data})
	}
	return images
}

type imageRequestBudget struct {
	maxImages      int
	maxSingleBytes int64
	maxTotalBytes  int64
}

func providerImageRequestBudget(p provider.Provider, vendor string) imageRequestBudget {
	// These limits describe the encoded image payload accepted by the common
	// provider wire formats. Keep the conservative limits here so raw mode
	// cannot bypass the final Agent Core request admission check.
	budget := imageRequestBudget{maxSingleBytes: 20 << 20}
	if p == nil {
		return budget
	}
	providerKey := strings.ToLower(strings.Join([]string{vendor, p.Name(), p.API()}, " "))
	switch {
	case strings.Contains(providerKey, "groq"):
		budget.maxImages = 5
		budget.maxSingleBytes = 4 << 20
		budget.maxTotalBytes = 4 << 20
	case strings.Contains(providerKey, "bedrock"):
		budget.maxSingleBytes = 5 << 20
	case strings.Contains(providerKey, "anthropic"):
		budget.maxSingleBytes = 10 << 20
	}
	return budget
}

func encodedImagePayloadBytes(image *provider.ImageContent) int64 {
	if image == nil {
		return 0
	}
	// ImageContent.Data already contains base64, so its string length is the
	// encoded payload that crosses JSON provider APIs.
	return int64(len(image.Data) + len(image.MimeType) + len("data:;base64,") + 128)
}

// validateImageRequestBudget checks the final canonical messages before they
// cross the provider boundary. Provider adapters still serialize the request,
// but no known single-image or total encoded-payload limit is left to a remote
// 4xx response, especially when a caller explicitly requested raw mode.
func (a *Agent) validateImageRequestBudget(messages []provider.Message) error {
	budget := providerImageRequestBudget(a.config.Provider, a.config.Vendor)
	if budget.maxSingleBytes <= 0 {
		return nil
	}
	var imageCount int
	var totalBytes int64
	for _, message := range messages {
		for _, block := range message.Contents {
			if block.Type != "image" || block.Image == nil {
				continue
			}
			imageCount++
			payloadBytes := encodedImagePayloadBytes(block.Image)
			if payloadBytes > budget.maxSingleBytes {
				return fmt.Errorf("image request exceeds %d-byte provider limit: image %d is %d bytes (detail=%s)", budget.maxSingleBytes, imageCount, payloadBytes, block.Image.Detail)
			}
			totalBytes += payloadBytes
		}
	}
	if imageCount == 0 {
		return nil
	}
	if budget.maxImages > 0 && imageCount > budget.maxImages {
		return fmt.Errorf("image request contains %d images, but provider limit is %d", imageCount, budget.maxImages)
	}
	if budget.maxTotalBytes > 0 && totalBytes > budget.maxTotalBytes {
		return fmt.Errorf("image request payload exceeds %d-byte provider limit: %d bytes", budget.maxTotalBytes, totalBytes)
	}
	return nil
}

func estimateTextTokens(s string) int {
	return ctxpkg.EstimateTextTokens(s)
}

func estimateToolDefinitionTokens(tools []provider.ToolDefinition) int {
	if len(tools) == 0 {
		return 0
	}
	data, err := json.Marshal(tools)
	if err != nil {
		return 0
	}
	return estimateTextTokens(string(data))
}

func estimateChatRequestTokens(systemPrompt string, messages []provider.Message, tools []provider.ToolDefinition, estimator ctxpkg.TokenEstimator) int {
	if estimator == nil {
		estimator = ctxpkg.GenericTokenEstimator{}
	}
	total := estimateTextTokens(systemPrompt)
	total += estimateToolDefinitionTokens(tools)
	for _, msg := range messages {
		total += estimator.EstimateTokens(msg)
	}
	return total
}

func estimateGuardToolResultTokens(msg provider.Message, estimator ctxpkg.TokenEstimator) int {
	return ctxpkg.EstimateGuardTokens(msg, estimator)
}

func estimateGuardRequestTokens(systemPrompt string, messages []provider.Message, tools []provider.ToolDefinition, estimator ctxpkg.TokenEstimator) int {
	total := estimateTextTokens(systemPrompt) + estimateToolDefinitionTokens(tools)
	for _, msg := range messages {
		total += estimateGuardToolResultTokens(msg, estimator)
	}
	return total
}

func estimateProviderUsage(systemPrompt string, messages []provider.Message, tools []provider.ToolDefinition, assistant provider.Message, estimator ctxpkg.TokenEstimator) *provider.Usage {
	input := estimateChatRequestTokens(systemPrompt, messages, tools, estimator)
	output := estimator.EstimateTokens(assistant)
	return &provider.Usage{
		Input:       input,
		Output:      output,
		TotalTokens: input + output,
	}
}

func completeProviderUsage(usage, estimated *provider.Usage) *provider.Usage {
	if usage == nil {
		return estimated
	}
	if usage.Input <= 0 {
		if usage.TotalTokens > 0 && usage.Output > 0 {
			usage.Input = usage.TotalTokens - usage.Output
		}
		if usage.Input <= 0 {
			usage.Input = estimated.Input
		}
	}
	if usage.Output <= 0 {
		usage.Output = estimated.Output
	}
	if usage.TotalTokens <= 0 {
		usage.TotalTokens = usage.Input + usage.CacheRead + usage.CacheWrite + usage.Output
	}
	return usage
}

// buildSessionContextMessage builds the [session context] message with dynamic information.
// This implements Rule R2.3 from LLM_Agent_Cache.md: dynamic info goes into a separate message.
// The message is marked as SystemInjected so cache markers skip it.
func (a *Agent) buildSessionContextMessage() provider.Message {
	modelID := "unknown"
	modelName := "unknown"
	if a.config.Model != nil {
		modelID = a.config.Model.ID
		modelName = a.config.Model.Name
	}

	context := fmt.Sprintf(`[session context]
- Current date: %s
- Model: %s (%s)
- Working directory: %s
- Mode: %s
`,
		time.Now().Format("2006-01-02"),
		modelName,
		modelID,
		a.registry.GetWorkDir(),
		a.config.Mode,
	)

	return provider.NewSystemInjectedUserMessage(context)
}

func (a *Agent) outputReserveTokens() int {
	reserve := a.config.MaxTokens
	if reserve <= 0 {
		reserve = 16384
	}
	if a.config.Model != nil && a.config.Model.ContextWindow > 0 && reserve >= a.config.Model.ContextWindow {
		return a.config.Model.ContextWindow / 2
	}
	return reserve
}

func (a *Agent) requestTokenBudget() (budget int, reserve int, contextWindow int, ok bool) {
	if a.config.Model == nil || a.config.Model.ContextWindow <= 0 {
		return 0, 0, 0, false
	}
	contextWindow = a.config.Model.ContextWindow
	reserve = a.outputReserveTokens()
	budget = contextWindow - reserve
	if budget <= 0 {
		budget = contextWindow / 2
	}
	return budget, reserve, contextWindow, true
}

func (a *Agent) buildRequestMessages(sessionContextMsg provider.Message) []provider.Message {
	a.mu.RLock()
	allMessages := make([]provider.Message, 0, len(a.messages)+1)
	allMessages = append(allMessages, sessionContextMsg)
	allMessages = append(allMessages, a.messages...)
	a.mu.RUnlock()

	allMessages = repairDanglingToolCalls(allMessages)
	return allMessages
}

// repairDanglingToolCalls returns a copy of messages where every assistant
// toolCall is directly followed by a matching toolResult. Results that were
// recorded later in history (e.g. an aborted run appended them after newer
// messages) are moved next to their assistant message; tool calls that never
// produced a result (e.g. the run was interrupted before completion) get a
// synthesized error result. Strict tool APIs (Kimi/OpenAI) reject requests
// where an assistant tool_call has no adjacent tool response, so this keeps
// the request valid even when the persisted history was left inconsistent by
// an interrupted run. The input slice is never mutated.
func repairDanglingToolCalls(messages []provider.Message) []provider.Message {
	hasToolCall := false
	for _, msg := range messages {
		for _, c := range msg.Contents {
			if c.Type == "toolCall" && c.ToolCall != nil {
				hasToolCall = true
				break
			}
		}
		if hasToolCall {
			break
		}
	}
	if !hasToolCall {
		return messages
	}

	// Positions of toolResult messages by tool call ID.
	resultIndex := make(map[string][]int)
	for i, msg := range messages {
		if msg.Role == "toolResult" && msg.ToolCallID != "" {
			resultIndex[msg.ToolCallID] = append(resultIndex[msg.ToolCallID], i)
		}
	}

	consumed := make(map[int]bool) // toolResult indices already placed after their assistant message
	out := make([]provider.Message, 0, len(messages))
	for i, msg := range messages {
		if msg.Role == "toolResult" && consumed[i] {
			continue
		}
		out = append(out, msg)
		if msg.Role != "assistant" {
			continue
		}
		var ids []string
		for _, c := range msg.Contents {
			if c.Type == "toolCall" && c.ToolCall != nil && c.ToolCall.ID != "" {
				ids = append(ids, c.ToolCall.ID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		pending := make(map[string]bool, len(ids))
		for _, id := range ids {
			pending[id] = true
		}
		// Consume toolResults that already directly follow this assistant message.
		for j := i + 1; j < len(messages); j++ {
			next := messages[j]
			if next.Role != "toolResult" || !pending[next.ToolCallID] || consumed[j] {
				break
			}
			out = append(out, next)
			consumed[j] = true
			delete(pending, next.ToolCallID)
		}
		// Pull matching results recorded later in history (e.g. appended by an
		// aborted run after newer messages) up next to the assistant message.
		for _, id := range ids {
			if !pending[id] {
				continue
			}
			for _, j := range resultIndex[id] {
				if j > i && !consumed[j] {
					out = append(out, messages[j])
					consumed[j] = true
					delete(pending, id)
					break
				}
			}
		}
		// Synthesize error results for tool calls that never produced a result.
		for _, id := range ids {
			if !pending[id] {
				continue
			}
			name := ""
			for _, c := range msg.Contents {
				if c.Type == "toolCall" && c.ToolCall != nil && c.ToolCall.ID == id {
					name = c.ToolCall.Name
					break
				}
			}
			out = append(out, provider.NewToolResultMessage(id, name, "[Interrupted] Tool execution was aborted before a result was recorded.", true))
			delete(pending, id)
		}
	}
	return out
}

func isContextGuardToolResult(msg provider.Message) bool {
	return msg.Role == "toolResult" && strings.HasPrefix(msg.Content, "[Context guard]")
}

func contextGuardToolResult(msg provider.Message, estimatedTokens, budgetTokens, contextWindow, reserveTokens int) provider.Message {
	toolName := msg.ToolName
	if toolName == "" {
		toolName = "tool"
	}
	content := fmt.Sprintf("[Context guard] The %q tool output was omitted because sending it would exceed the model context window (estimated request: %d tokens; input budget: %d tokens; context window: %d; reserved for output: %d). Retry with a narrower scope: use read with offset/limit, grep/find with path/include/maxResults, or request smaller chunks and summarize incrementally.", toolName, estimatedTokens, budgetTokens, contextWindow, reserveTokens)
	return provider.Message{
		Role:       "toolResult",
		Content:    content,
		ToolCallID: msg.ToolCallID,
		ToolName:   msg.ToolName,
		IsError:    true,
		Timestamp:  msg.Timestamp,
	}
}

func (a *Agent) replaceLargestToolResultForContext(estimatedTokens, budgetTokens, contextWindow, reserveTokens int) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	estimator := ctxpkg.ResolveTokenEstimator(a.config.CompactionSettings, a.config.Model)
	bestIndex := -1
	bestTokens := 0
	for i, msg := range a.messages {
		if msg.Role != "toolResult" || isContextGuardToolResult(msg) {
			continue
		}
		tokens := estimator.EstimateTokens(msg)
		if tokens > bestTokens {
			bestIndex = i
			bestTokens = tokens
		}
	}
	if bestIndex < 0 {
		return "", false
	}

	original := a.messages[bestIndex]
	a.messages[bestIndex] = contextGuardToolResult(original, estimatedTokens, budgetTokens, contextWindow, reserveTokens)
	if a.context != nil {
		if len(a.context.Messages) == len(a.messages) {
			a.context.Messages[bestIndex] = a.messages[bestIndex]
		} else {
			a.context.Messages = a.messages
		}
	}
	return original.ToolName, true
}

func (a *Agent) prepareRequestMessages(sessionContextMsg provider.Message, ch chan<- Event) ([]provider.Message, error) {
	budgetTokens, reserveTokens, contextWindow, ok := a.requestTokenBudget()
	if !ok {
		return a.buildRequestMessages(sessionContextMsg), nil
	}

	estimator := ctxpkg.ResolveTokenEstimator(a.config.CompactionSettings, a.config.Model)
	for attempts := 0; attempts < 16; attempts++ {
		messages := a.buildRequestMessages(sessionContextMsg)
		estimatedTokens := estimateGuardRequestTokens(a.frozenSystemPrompt, messages, a.frozenToolDefs, estimator)
		if estimatedTokens <= budgetTokens {
			return messages, nil
		}
		toolName, replaced := a.replaceLargestToolResultForContext(estimatedTokens, budgetTokens, contextWindow, reserveTokens)
		if !replaced {
			return nil, fmt.Errorf("estimated request tokens %d exceed input budget %d for context window %d (reserved output: %d). Narrow the request or reduce context before retrying", estimatedTokens, budgetTokens, contextWindow, reserveTokens)
		}
		if toolName == "" {
			toolName = "tool"
		}
		a.sendEvent(ch, Event{Type: EventStatus, StatusMessage: fmt.Sprintf("Context guard omitted oversized %s output; asking model to retry with a narrower scope.", toolName)})
	}

	return nil, fmt.Errorf("estimated request still exceeds context after omitting oversized tool outputs")
}

const contextTokenSafetyMargin = 512

func clampMaxTokensToContext(maxTokens, contextWindow, estimatedInputTokens int) int {
	if maxTokens <= 0 || contextWindow <= 0 || estimatedInputTokens <= 0 {
		return maxTokens
	}
	available := contextWindow - estimatedInputTokens - contextTokenSafetyMargin
	if available < 1 {
		available = 1
	}
	if maxTokens > available {
		return available
	}
	return maxTokens
}

func (a *Agent) maxTokensForRequest(messages []provider.Message) int {
	maxTokens := a.config.MaxTokens
	if a.config.Model == nil || a.config.Model.ContextWindow <= 0 || maxTokens <= 0 {
		return maxTokens
	}
	estimator := ctxpkg.ResolveTokenEstimator(a.config.CompactionSettings, a.config.Model)
	estimatedTokens := estimateChatRequestTokens(a.frozenSystemPrompt, messages, a.frozenToolDefs, estimator)
	return clampMaxTokensToContext(maxTokens, a.config.Model.ContextWindow, estimatedTokens)
}

func selectCacheMarkers(messages []provider.Message) [2]int {
	var markers [2]int
	markers[0] = -1
	markers[1] = -1

	count := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].SystemInjected {
			continue
		}
		if count == 0 {
			markers[1] = i // newest marker
		} else if count == 1 {
			markers[0] = i // second newest marker
			break
		}
		count++
	}
	return markers
}

func applyCacheMarkers(messages []provider.Message, markers [2]int) []provider.Message {
	if markers[0] == -1 && markers[1] == -1 {
		return messages
	}

	// Create a deep copy to avoid modifying the original messages
	result := make([]provider.Message, len(messages))
	for i, msg := range messages {
		result[i] = msg
		// Deep copy Contents slice and pointer fields
		if len(msg.Contents) > 0 {
			result[i].Contents = make([]provider.ContentBlock, len(msg.Contents))
			for j, cb := range msg.Contents {
				result[i].Contents[j] = cb
				if cb.Image != nil {
					imgCopy := *cb.Image
					result[i].Contents[j].Image = &imgCopy
				}
				if cb.ToolCall != nil {
					tcCopy := *cb.ToolCall
					result[i].Contents[j].ToolCall = &tcCopy
				}
				if cb.CacheControl != nil {
					ccCopy := *cb.CacheControl
					result[i].Contents[j].CacheControl = &ccCopy
				}
			}
		}
	}

	for _, idx := range markers {
		if idx < 0 || idx >= len(result) {
			continue
		}
		msg := &result[idx]
		if len(msg.Contents) > 0 {
			// Add cache_control to the last content block
			lastIdx := len(msg.Contents) - 1
			msg.Contents[lastIdx].CacheControl = &provider.CacheControl{Type: "ephemeral"}
		} else if msg.Content != "" {
			// Convert simple text to content blocks with cache_control
			msg.Contents = []provider.ContentBlock{
				{
					Type:         "text",
					Text:         msg.Content,
					CacheControl: &provider.CacheControl{Type: "ephemeral"},
				},
			}
			msg.Content = ""
		}
	}

	return result
}

// GetMessages returns a copy of the current message history.
func (a *Agent) GetMessages() []provider.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make([]provider.Message, len(a.messages))
	copy(result, a.messages)
	return result
}

// GetHistoryState returns a copy of message history plus aligned session entry IDs.
func (a *Agent) GetHistoryState() ([]provider.Message, []string) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	msgs := make([]provider.Message, len(a.messages))
	copy(msgs, a.messages)
	ids := append([]string(nil), a.messageIDs...)
	return msgs, ids
}

// SetMessages replaces the message history.
func (a *Agent) SetMessages(msgs []provider.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = msgs
	a.messageIDs = make([]string, len(msgs))
	a.context.Messages = msgs
}

// GetContext returns a copy of the current agent context.
func (a *Agent) GetContext() *AgentContext {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.context == nil {
		return nil
	}
	ctx := *a.context
	ctx.Messages = make([]provider.Message, len(a.context.Messages))
	copy(ctx.Messages, a.context.Messages)
	ctx.Tools = make([]provider.ToolDefinition, len(a.context.Tools))
	copy(ctx.Tools, a.context.Tools)
	return &ctx
}

// SetContext replaces the agent context.
func (a *Agent) SetContext(ctx *AgentContext) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.context = ctx
}

// GetContextUsage calculates and returns the current context usage.
func (a *Agent) GetContextUsage() *ctxpkg.ContextUsage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.config.Model == nil {
		return nil
	}
	contextWindow := a.config.Model.ContextWindow
	if contextWindow <= 0 {
		return nil
	}

	estimator := ctxpkg.ResolveTokenEstimator(a.config.CompactionSettings, a.config.Model)
	usage := ctxpkg.ContextUsageFromMessages(a.messages, estimator)
	usage.ContextWindow = contextWindow
	percent := float64(usage.TotalTokens) / float64(contextWindow) * 100
	usage.Percent = &percent
	return &usage
}

// SetForceCompact marks the agent for forced compaction on the next turn.
func (a *Agent) SetForceCompact() {
	atomic.StoreInt32(&a.forceCompact, 1)
}

func (a *Agent) previousCompactionSummary(messages []provider.Message) string {
	if a.config.Session != nil {
		if compaction, ok := a.config.Session.GetLatestCompaction(); ok {
			return compaction.Summary
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].SystemInjected && messages[i].Role == "user" && strings.HasPrefix(messages[i].Content, "## Goal") {
			return messages[i].Content
		}
	}
	return ""
}

// CanCompact reports whether the current conversation has older messages that
// can be summarized while preserving the configured recent context.
func (a *Agent) CanCompact() bool {
	a.mu.RLock()
	model := a.config.Model
	settings := a.config.CompactionSettings
	msgs := make([]provider.Message, len(a.messages))
	copy(msgs, a.messages)
	a.mu.RUnlock()

	if model == nil {
		return false
	}
	return ctxpkg.HasCompactableMessages(msgs, model, settings, a.previousCompactionSummary(msgs))
}

func (a *Agent) canForceCompact() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.Model != nil && len(a.messages) > 0
}

func (a *Agent) shouldAutoCompact() bool {
	if !a.config.CompactionSettings.Enabled {
		return false
	}
	if a.config.Model == nil || a.config.Model.ContextWindow <= 0 {
		return false
	}
	sessionContextMsg := a.buildSessionContextMessage()
	messages := a.buildRequestMessages(sessionContextMsg)
	estimator := ctxpkg.ResolveTokenEstimator(a.config.CompactionSettings, a.config.Model)
	tokens := estimateChatRequestTokens(a.frozenSystemPrompt, messages, a.frozenToolDefs, estimator)
	if !ctxpkg.ShouldCompactPercent(tokens, a.config.Model.ContextWindow, defaultAutoCompactionThreshold) {
		return false
	}

	a.mu.RLock()
	msgs := make([]provider.Message, len(a.messages))
	copy(msgs, a.messages)
	model := a.config.Model
	settings := a.config.CompactionSettings
	a.mu.RUnlock()
	return ctxpkg.HasCompactableMessages(msgs, model, settings, a.previousCompactionSummary(msgs))
}

// ShouldCompact checks if compaction should trigger.
// Returns true if context exceeds the threshold OR if forced via SetForceCompact.
func (a *Agent) ShouldCompact() bool {
	if atomic.CompareAndSwapInt32(&a.forceCompact, 1, 0) {
		return a.canForceCompact()
	}
	return a.shouldAutoCompact()
}

func (a *Agent) compactIfNeeded(ctx context.Context, ch chan<- Event) {
	if atomic.CompareAndSwapInt32(&a.forceCompact, 1, 0) {
		if a.canForceCompact() {
			_ = a.CompactForced(ctx, ch)
		}
		return
	}
	if a.shouldAutoCompact() {
		_ = a.Compact(ctx, ch)
	}
}

// Compact performs context compaction using Insert-then-Compress pattern (R4.1-R4.4).
// Uses the SAME system prompt and tools as the main conversation.
func (a *Agent) Compact(ctx context.Context, ch chan<- Event) error {
	return a.compact(ctx, ch, false)
}

// CompactForced performs explicit user-requested compaction. It skips
// preflight compactability checks and allows summary-only checkpoints.
func (a *Agent) CompactForced(ctx context.Context, ch chan<- Event) error {
	return a.compact(ctx, ch, true)
}

func (a *Agent) compact(ctx context.Context, ch chan<- Event, force bool) error {
	if a.config.Model == nil {
		return fmt.Errorf("no model set for compaction")
	}

	// Do not impose a separate wall-clock deadline here. The provider owns
	// streaming/SSE idle-timeout behavior; this context only propagates caller
	// cancellation and Agent.Abort().
	compactCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-a.abort:
			cancel()
		case <-compactCtx.Done():
		}
	}()

	a.sendEvent(ch, Event{Type: EventCompactionStart})

	// Snapshot messages under lock
	a.mu.RLock()
	msgs := make([]provider.Message, len(a.messages))
	copy(msgs, a.messages)
	msgIDs := append([]string(nil), a.messageIDs...)
	a.mu.RUnlock()

	previousSummary := a.previousCompactionSummary(msgs)

	// Use Insert-then-Compress with the SAME system prompt and tools (R4.1)
	result, err := ctxpkg.CompactWithOptions(compactCtx, msgs, a.config.Provider, a.config.Model,
		a.frozenSystemPrompt, a.frozenToolDefs,
		a.config.CompactionSettings, previousSummary,
		ctxpkg.CompactOptions{
			Force:         force,
			ThinkingLevel: provider.NormalizeThinkingLevel(a.config.ThinkingLevel),
			Temperature:   config.NormalizeSamplingPtr(a.config.Model.Temperature),
			TopP:          config.NormalizeSamplingPtr(a.config.Model.TopP),
			Summarize: func(summaryCtx context.Context, summaryMessages []provider.Message, maxTokens int) (string, error) {
				return a.summarizeMessagesWithSubAgent(summaryCtx, summaryMessages, maxTokens)
			},
		})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			a.sendEvent(ch, Event{Type: EventCompactionEnd, StatusMessage: "Context compaction canceled", StopReason: "canceled"})
			return err
		}
		a.sendEvent(ch, Event{Type: EventCompactionEnd, Error: err})
		return fmt.Errorf("compaction failed: %w", err)
	}

	// Replace messages with summary + kept messages
	// Mark summary as system_injected so cache markers skip it
	firstKeptEntryID := ""
	if result.FirstKeptIndex >= 0 && result.FirstKeptIndex < len(msgIDs) {
		firstKeptEntryID = msgIDs[result.FirstKeptIndex]
	}
	a.mu.Lock()
	summaryMsg := provider.NewSystemInjectedUserMessage(result.Summary)
	keptMessages := cloneMessagesWithoutUsage(msgs[result.FirstKeptIndex:])

	newMessages := make([]provider.Message, 0, 1+len(keptMessages))
	newMessages = append(newMessages, summaryMsg)
	newMessages = append(newMessages, keptMessages...)

	a.messages = newMessages
	a.context.Messages = newMessages

	// Align messageIDs: summary gets empty ID, kept messages keep their IDs
	newIDs := make([]string, 0, 1+len(keptMessages))
	newIDs = append(newIDs, "")
	if result.FirstKeptIndex >= 0 {
		newIDs = append(newIDs, msgIDs[result.FirstKeptIndex:]...)
	}
	a.messageIDs = newIDs
	a.mu.Unlock()

	// Persist compaction to session
	if a.config.Session != nil {
		if _, err := a.config.Session.AppendCompaction(result.Summary, firstKeptEntryID, result.TokensBefore); err != nil {
			// Non-fatal: compaction worked, just couldn't persist the metadata
			a.sendEvent(ch, Event{Type: EventStatus, StatusMessage: fmt.Sprintf("Failed to persist compaction: %v", err)})
		}
	}

	a.sendEvent(ch, Event{
		Type:          EventCompactionEnd,
		StatusMessage: fmt.Sprintf("Context compacted: %d tokens", result.TokensBefore),
	})

	return nil
}

// tryRecoverContextOverflow attempts a one-shot recovery when a request
// cannot be sent because it exceeds the model context window: first LLM
// compaction, then deterministic truncation of the oldest messages when the
// summarization request itself no longer fits. Returns true when the caller
// should retry the turn.
func (a *Agent) tryRecoverContextOverflow(ctx context.Context, ch chan<- Event, retried *bool, cause error) bool {
	if *retried || !a.config.CompactionSettings.Enabled || !a.canForceCompact() {
		return false
	}
	*retried = true
	a.sendEvent(ch, Event{Type: EventStatus, StatusMessage: fmt.Sprintf("Context too large (%v); compacting context and retrying...", cause)})
	if err := a.CompactForced(ctx, ch); err != nil {
		// The summarization request itself no longer fits the context window;
		// fall back to deterministic truncation so the session can recover
		// instead of failing on every subsequent message.
		a.sendEvent(ch, Event{Type: EventStatus, StatusMessage: fmt.Sprintf("Context compaction failed (%v); dropping oldest messages to fit the context window...", err)})
		a.truncateHistoryForOverflow(ch)
	}
	return true
}

func (a *Agent) tryRetryStreamTimeout(ctx context.Context, ch chan<- Event, retried *int, maxRetries int, textContent, thinkContent string, cause error) bool {
	if cause == nil || *retried >= maxRetries {
		return false
	}
	if !provider.IsStreamTimeoutError(cause) {
		return false
	}
	// Only retry turns with no visible output yet: retrying an already-partially-
	// streamed turn would duplicate the partial content the user already saw.
	if textContent != "" || thinkContent != "" {
		return false
	}
	*retried++
	msg := fmt.Sprintf("⚠️ 供应商响应超时（长时间未收到数据），正在自动重试第 %d/%d 次…", *retried, maxRetries)
	a.sendEvent(ch, Event{Type: EventStatus, StatusMessage: msg})
	a.sendEvent(ch, Event{Type: EventRetry, RetryAttempt: *retried, RetryMaxAttempts: maxRetries, RetryReason: "timeout"})
	return true
}

func (a *Agent) setMessageID(index int, id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if index >= 0 && index < len(a.messageIDs) {
		a.messageIDs[index] = id
	}
}

// truncateHistoryForOverflow is the last-resort context recovery used when the
// provider rejects requests for exceeding the context window and LLM-based
// compaction also fails (the summarization request itself no longer fits). It
// drops the oldest messages — cutting only at user/assistant turn boundaries —
// until the estimated request fits a safe share of the context budget, then
// persists the cut as a compaction entry so sessions that are rebuilt from
// storage (e.g. messaging channel sessions) keep the truncated state.
func (a *Agent) truncateHistoryForOverflow(ch chan<- Event) {
	a.mu.RLock()
	msgs := make([]provider.Message, len(a.messages))
	copy(msgs, a.messages)
	msgIDs := append([]string(nil), a.messageIDs...)
	a.mu.RUnlock()

	if len(msgs) == 0 {
		return
	}

	estimator := ctxpkg.ResolveTokenEstimator(a.config.CompactionSettings, a.config.Model)
	tokensBefore := estimateChatRequestTokens(a.frozenSystemPrompt, msgs, a.frozenToolDefs, estimator)

	target := 0
	if budget, _, _, ok := a.requestTokenBudget(); ok {
		target = int(float64(budget) * 0.6)
	}
	fits := func(candidate []provider.Message) bool {
		return target > 0 && estimateChatRequestTokens(a.frozenSystemPrompt, candidate, a.frozenToolDefs, estimator) <= target
	}

	// Candidate cuts start at user/assistant messages only (never at a tool
	// result). When a session is attached, the first kept message must have an
	// entry ID so the cut can be persisted and replayed consistently.
	cut := -1
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role != "user" && msgs[i].Role != "assistant" {
			continue
		}
		if a.config.Session != nil && (i >= len(msgIDs) || msgIDs[i] == "") {
			continue
		}
		if fits(msgs[i:]) {
			cut = i
			break
		}
	}
	if cut < 0 {
		// No cut satisfies the target; keep only the final turn so the retry is
		// as small as possible. Skip persistence when the kept window cannot be
		// anchored to a stored entry.
		for i := len(msgs) - 1; i >= 1; i-- {
			if msgs[i].Role != "user" && msgs[i].Role != "assistant" {
				continue
			}
			if a.config.Session != nil && (i >= len(msgIDs) || msgIDs[i] == "") {
				continue
			}
			cut = i
			break
		}
	}
	if cut < 0 {
		return
	}

	kept := cloneMessagesWithoutUsage(msgs[cut:])
	note := fmt.Sprintf("[Context recovery] The provider rejected the request for exceeding the context window and automatic summarization failed, so %d older messages were dropped without a summary to recover. Earlier context is no longer available.", cut)

	newMessages := make([]provider.Message, 0, 1+len(kept))
	newMessages = append(newMessages, provider.NewSystemInjectedUserMessage(note))
	newMessages = append(newMessages, kept...)

	newIDs := make([]string, 0, 1+len(kept))
	newIDs = append(newIDs, "")
	if cut < len(msgIDs) {
		newIDs = append(newIDs, msgIDs[cut:]...)
	}

	a.mu.Lock()
	a.messages = newMessages
	a.context.Messages = newMessages
	a.messageIDs = newIDs
	a.mu.Unlock()

	if a.config.Session != nil {
		firstKeptEntryID := ""
		if cut < len(msgIDs) {
			firstKeptEntryID = msgIDs[cut]
		}
		if _, err := a.config.Session.AppendCompaction(note, firstKeptEntryID, tokensBefore); err != nil {
			// Non-fatal: in-memory state is already truncated.
			a.sendEvent(ch, Event{Type: EventStatus, StatusMessage: fmt.Sprintf("Failed to persist context recovery: %v", err)})
		}
	}

	a.sendEvent(ch, Event{Type: EventStatus, StatusMessage: fmt.Sprintf("Context recovery: dropped %d oldest messages after provider context overflow", cut)})
}

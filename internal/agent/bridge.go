package agent

import (
	"context"

	agentpkg "github.com/startvibecoding/mothx/agent"
	ctxpkg "github.com/startvibecoding/mothx/internal/context"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/tools"
)

// --- Type conversion helpers ---

// MessageToPublic converts an internal provider.Message to a public agent.Message.
func MessageToPublic(m provider.Message) agentpkg.Message {
	msg := agentpkg.Message{
		Role:           agentpkg.Role(m.Role),
		Content:        m.Content,
		IsError:        m.IsError,
		SystemInjected: m.SystemInjected,
		ToolCallID:     m.ToolCallID,
		ToolName:       m.ToolName,
		ToolKind:       m.ToolKind,
		Attachments:    AttachmentsToPublic(m.Attachments),
	}
	if m.Usage != nil {
		msg.Usage = UsageToPublic(m.Usage)
	}
	for _, cb := range m.Contents {
		msg.Contents = append(msg.Contents, ContentBlockToPublic(cb))
	}
	return msg
}

// MessageFromPublic converts a public agent.Message to an internal provider.Message.
func MessageFromPublic(m agentpkg.Message) provider.Message {
	msg := provider.Message{
		Role:           string(m.Role),
		Content:        m.Content,
		IsError:        m.IsError,
		SystemInjected: m.SystemInjected,
		ToolCallID:     m.ToolCallID,
		ToolName:       m.ToolName,
		ToolKind:       m.ToolKind,
		Attachments:    AttachmentsFromPublic(m.Attachments),
	}
	if m.Usage != nil {
		msg.Usage = &provider.Usage{
			Input:       m.Usage.InputTokens,
			Output:      m.Usage.OutputTokens,
			CacheRead:   m.Usage.CacheRead,
			CacheWrite:  m.Usage.CacheWrite,
			TotalTokens: m.Usage.TotalTokens,
		}
	}
	for _, cb := range m.Contents {
		msg.Contents = append(msg.Contents, ContentBlockFromPublic(cb))
	}
	return msg
}

// ContentBlockToPublic converts an internal provider.ContentBlock to public.
func ContentBlockToPublic(cb provider.ContentBlock) agentpkg.ContentBlock {
	pub := agentpkg.ContentBlock{
		Type:      cb.Type,
		Text:      cb.Text,
		Thinking:  cb.Thinking,
		Signature: cb.Signature,
	}
	if cb.ToolCall != nil {
		pub.ToolCall = &agentpkg.ToolCallBlock{
			ID:               cb.ToolCall.ID,
			Name:             cb.ToolCall.Name,
			Kind:             cb.ToolCall.Kind,
			Input:            cb.ToolCall.Input,
			Arguments:        cb.ToolCall.Arguments,
			InvalidArguments: cb.ToolCall.InvalidArguments,
			ThoughtSignature: cb.ToolCall.ThoughtSignature,
		}
	}
	if cb.Image != nil {
		pub.Image = &agentpkg.ImageContent{
			MimeType:       cb.Image.MimeType,
			Data:           cb.Image.Data,
			Width:          cb.Image.Width,
			Height:         cb.Image.Height,
			Bytes:          cb.Image.Bytes,
			OriginalWidth:  cb.Image.OriginalWidth,
			OriginalHeight: cb.Image.OriginalHeight,
			OriginalBytes:  cb.Image.OriginalBytes,
			Detail:         cb.Image.Detail,
			Scale:          cb.Image.Scale,
			Cropped:        cb.Image.Cropped,
			CropX:          cb.Image.CropX,
			CropY:          cb.Image.CropY,
			CropWidth:      cb.Image.CropWidth,
			CropHeight:     cb.Image.CropHeight,
		}
	}
	if cb.File != nil {
		pub.File = &agentpkg.FileContent{ID: cb.File.ID, URL: cb.File.URL, Data: cb.File.Data, Filename: cb.File.Filename, MimeType: cb.File.MimeType, Title: cb.File.Title, Description: cb.File.Description, Size: cb.File.Size}
	}
	if cb.CacheControl != nil {
		pub.CacheControl = &agentpkg.CacheControl{Type: cb.CacheControl.Type}
	}
	return pub
}

// ContentBlockFromPublic converts a public agent.ContentBlock to internal.
func ContentBlockFromPublic(cb agentpkg.ContentBlock) provider.ContentBlock {
	internal := provider.ContentBlock{
		Type:      cb.Type,
		Text:      cb.Text,
		Thinking:  cb.Thinking,
		Signature: cb.Signature,
	}
	if cb.ToolCall != nil {
		internal.ToolCall = &provider.ToolCallBlock{
			ID:               cb.ToolCall.ID,
			Name:             cb.ToolCall.Name,
			Kind:             cb.ToolCall.Kind,
			Input:            cb.ToolCall.Input,
			Arguments:        cb.ToolCall.Arguments,
			InvalidArguments: cb.ToolCall.InvalidArguments,
			ThoughtSignature: cb.ToolCall.ThoughtSignature,
		}
	}
	if cb.Image != nil {
		internal.Image = &provider.ImageContent{
			MimeType:       cb.Image.MimeType,
			Data:           cb.Image.Data,
			Width:          cb.Image.Width,
			Height:         cb.Image.Height,
			Bytes:          cb.Image.Bytes,
			OriginalWidth:  cb.Image.OriginalWidth,
			OriginalHeight: cb.Image.OriginalHeight,
			OriginalBytes:  cb.Image.OriginalBytes,
			Detail:         cb.Image.Detail,
			Scale:          cb.Image.Scale,
			Cropped:        cb.Image.Cropped,
			CropX:          cb.Image.CropX,
			CropY:          cb.Image.CropY,
			CropWidth:      cb.Image.CropWidth,
			CropHeight:     cb.Image.CropHeight,
		}
	}
	if cb.File != nil {
		internal.File = &provider.FileContent{ID: cb.File.ID, URL: cb.File.URL, Data: cb.File.Data, Filename: cb.File.Filename, MimeType: cb.File.MimeType, Title: cb.File.Title, Description: cb.File.Description, Size: cb.File.Size}
	}
	if cb.CacheControl != nil {
		internal.CacheControl = &provider.CacheControl{Type: cb.CacheControl.Type}
	}
	return internal
}

// MessagesToPublic converts a slice of internal messages to public.
func MessagesToPublic(msgs []provider.Message) []agentpkg.Message {
	result := make([]agentpkg.Message, len(msgs))
	for i, m := range msgs {
		result[i] = MessageToPublic(m)
	}
	return result
}

// MessagesFromPublic converts a slice of public messages to internal.
func MessagesFromPublic(msgs []agentpkg.Message) []provider.Message {
	result := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		result[i] = MessageFromPublic(m)
	}
	return result
}

// ContextUsageToPublic converts internal context usage to public.
func ContextUsageToPublic(u *ctxpkg.ContextUsage) *agentpkg.ContextUsage {
	if u == nil {
		return nil
	}
	return &agentpkg.ContextUsage{
		Tokens:        u.TotalTokens,
		TotalTokens:   u.TotalTokens,
		Input:         u.Input,
		CacheRead:     u.CacheRead,
		CacheWrite:    u.CacheWrite,
		ContextWindow: u.ContextWindow,
		Percent:       u.Percent,
	}
}

// EventToPublic converts an internal Event to a public agent.Event.
func EventToPublic(e Event) agentpkg.Event {
	return agentpkg.Event{
		AgentID:                   agentpkg.AgentID(e.AgentID),
		Type:                      EventTypeToPublic(e.Type),
		MemberID:                  e.MemberID,
		ExpertID:                  e.ExpertID,
		MemberDisplayName:         e.MemberDisplayName,
		MemberEmoji:               e.MemberEmoji,
		MemberRole:                e.MemberRole,
		Messages:                  MessagesToPublic(e.Messages),
		TurnMessage:               MessageToPublic(e.TurnMessage),
		TurnToolResults:           MessagesToPublic(e.TurnToolResults),
		Message:                   MessageToPublic(e.Message),
		TextDelta:                 e.TextDelta,
		ThinkDelta:                e.ThinkDelta,
		HostedItem:                hostedItemToPublic(e.HostedItem),
		ToolCall:                  ToolCallBlockToPublic(e.ToolCall),
		ToolCallID:                e.ToolCallID,
		ToolName:                  e.ToolName,
		ToolArgs:                  e.ToolArgs,
		ToolResult:                e.ToolResult,
		ToolDiff:                  FileDiffToPublic(e.ToolDiff),
		ToolError:                 e.ToolError,
		ToolExecutionState:        e.ToolExecutionState,
		ToolImages:                ToolImagesToPublic(e.ToolImages),
		PartialResult:             e.PartialResult,
		Plan:                      TaskPlanToPublic(e.Plan),
		StatusMessage:             e.StatusMessage,
		ResponseStateFailureClass: e.ResponseStateFailureClass,
		RetryStatus:               e.RetryStatus,
		RetryAttempt:              e.RetryAttempt,
		RetryMaxAttempts:          e.RetryMaxAttempts,
		RetryAfterMS:              e.RetryAfterMS,
		RetryMaxTokens:            e.RetryMaxTokens,
		RetryReason:               e.RetryReason,
		RetryContinue:             e.RetryContinue,
		Done:                      e.Done,
		StopReason:                e.StopReason,
		Error:                     e.Error,
		Status:                    agentpkg.TaskStatus(e.Status),
		ApprovalID:                e.ApprovalID,
		ApprovalTool:              e.ApprovalTool,
		ApprovalArgs:              e.ApprovalArgs,
		ApprovalResult:            e.ApprovalResult,
		QuestionID:                e.QuestionID,
		QuestionText:              e.QuestionText,
		QuestionOptions:           e.QuestionOptions,
		QuestionContext:           e.QuestionContext,
		QuestionAnswer:            e.QuestionAnswer,
		Usage:                     UsageToPublic(e.Usage),
		Attachments:               AttachmentsToPublic(e.Attachments),
		ContextUsage:              ContextUsageToPublic(e.ContextUsage),
	}
}

// ToolImagesToPublic converts internal tool result images to the public SDK
// type. The base64 payload is passed through unchanged.
func ToolImagesToPublic(images []ToolImage) []agentpkg.ToolImage {
	if len(images) == 0 {
		return nil
	}
	result := make([]agentpkg.ToolImage, 0, len(images))
	for _, image := range images {
		result = append(result, agentpkg.ToolImage{MimeType: image.MimeType, Data: image.Data})
	}
	return result
}

// EventTypeToPublic converts the internal event enum to the public enum.
// The enums intentionally retain their historical ordering, so this must not
// be a numeric cast: EventRetry was added in different positions.
func EventTypeToPublic(t EventType) agentpkg.EventType {
	switch t {
	case EventAgentStart:
		return agentpkg.EventAgentStart
	case EventAgentEnd:
		return agentpkg.EventAgentEnd
	case EventTurnStart:
		return agentpkg.EventTurnStart
	case EventTurnEnd:
		return agentpkg.EventTurnEnd
	case EventMessageStart:
		return agentpkg.EventMessageStart
	case EventMessageUpdate:
		return agentpkg.EventMessageUpdate
	case EventMessageEnd:
		return agentpkg.EventMessageEnd
	case EventTextDelta:
		return agentpkg.EventTextDelta
	case EventThinkDelta:
		return agentpkg.EventThinkDelta
	case EventHostedItem:
		return agentpkg.EventHostedItem
	case EventToolCall:
		return agentpkg.EventToolCall
	case EventToolExecutionStart:
		return agentpkg.EventToolExecutionStart
	case EventToolExecutionUpdate:
		return agentpkg.EventToolExecutionUpdate
	case EventToolExecutionEnd:
		return agentpkg.EventToolExecutionEnd
	case EventToolResult:
		return agentpkg.EventToolResult
	case EventToolApprovalRequest:
		return agentpkg.EventToolApprovalRequest
	case EventToolApprovalResponse:
		return agentpkg.EventToolApprovalResponse
	case EventQuestionRequest:
		return agentpkg.EventQuestionRequest
	case EventQuestionResponse:
		return agentpkg.EventQuestionResponse
	case EventPlanUpdate:
		return agentpkg.EventPlanUpdate
	case EventStatus:
		return agentpkg.EventStatus
	case EventDone:
		return agentpkg.EventDone
	case EventError:
		return agentpkg.EventError
	case EventUsage:
		return agentpkg.EventUsage
	case EventRetry:
		return agentpkg.EventRetry
	case EventCompactionStart:
		return agentpkg.EventCompactionStart
	case EventCompactionEnd:
		return agentpkg.EventCompactionEnd
	case EventContextPressure:
		return agentpkg.EventContextPressure
	case EventBudgetPressure:
		return agentpkg.EventBudgetPressure
	case EventRunFinished:
		return agentpkg.EventRunFinished
	default:
		return agentpkg.EventStatus
	}
}

func hostedItemToPublic(item *provider.HostedItem) *agentpkg.HostedItem {
	if item == nil {
		return nil
	}
	return &agentpkg.HostedItem{ID: item.ID, Type: item.Type, Status: item.Status, OutputIndex: item.OutputIndex, Metadata: item.Metadata}
}

func AttachmentsToPublic(items []provider.Attachment) []agentpkg.Attachment {
	if len(items) == 0 {
		return nil
	}
	result := make([]agentpkg.Attachment, len(items))
	for i, item := range items {
		result[i] = agentpkg.Attachment{Kind: item.Kind, Name: item.Name, URL: item.URL,
			MediaType: item.MediaType, Metadata: item.Metadata, ProviderRef: item.ProviderRef}
	}
	return result
}

func AttachmentsFromPublic(items []agentpkg.Attachment) []provider.Attachment {
	if len(items) == 0 {
		return nil
	}
	result := make([]provider.Attachment, len(items))
	for i, item := range items {
		result[i] = provider.Attachment{Kind: item.Kind, Name: item.Name, URL: item.URL,
			MediaType: item.MediaType, Metadata: item.Metadata, ProviderRef: item.ProviderRef}
	}
	return result
}

// ToolCallBlockToPublic converts an internal provider.ToolCallBlock to public.
func ToolCallBlockToPublic(tc *provider.ToolCallBlock) *agentpkg.ToolCallBlock {
	if tc == nil {
		return nil
	}
	return &agentpkg.ToolCallBlock{
		ID:               tc.ID,
		Name:             tc.Name,
		Kind:             tc.Kind,
		Input:            tc.Input,
		Arguments:        tc.Arguments,
		InvalidArguments: tc.InvalidArguments,
		ThoughtSignature: tc.ThoughtSignature,
	}
}

// FileDiffToPublic converts an internal tools.FileDiff to public agent.FileDiff.
func FileDiffToPublic(d *tools.FileDiff) *agentpkg.FileDiff {
	if d == nil {
		return nil
	}
	return &agentpkg.FileDiff{
		Path:         d.Path,
		Added:        d.Added,
		Deleted:      d.Deleted,
		AddedLines:   d.AddedLines,
		DeletedLines: d.DeletedLines,
		Unified:      d.Unified,
		OldText:      d.OldText,
		NewText:      d.NewText,
		Truncated:    d.Truncated,
	}
}

// TaskPlanToPublic converts an internal tools.TaskPlan to public agent.TaskPlan.
func TaskPlanToPublic(p *tools.TaskPlan) *agentpkg.TaskPlan {
	if p == nil {
		return nil
	}
	steps := make([]agentpkg.PlanStep, len(p.Steps))
	for i, s := range p.Steps {
		steps[i] = agentpkg.PlanStep{
			Title:  s.Title,
			Status: s.Status,
		}
	}
	return &agentpkg.TaskPlan{
		Title: p.Title,
		Steps: steps,
		Note:  p.Note,
	}
}

// UsageToPublic converts an internal provider.Usage to public agent.Usage.
func UsageToPublic(u *provider.Usage) *agentpkg.Usage {
	if u == nil {
		return nil
	}
	return &agentpkg.Usage{
		InputTokens:  u.Input,
		OutputTokens: u.Output,
		CacheRead:    u.CacheRead,
		CacheWrite:   u.CacheWrite,
		TotalTokens:  u.TotalTokens,
	}
}

// ChatParamsFromPublic converts public ChatParams to internal.
func ChatParamsFromPublic(p agentpkg.ChatParams) provider.ChatParams {
	msgs := make([]provider.Message, len(p.Messages))
	for i, m := range p.Messages {
		msgs[i] = MessageFromPublic(m)
	}
	toolsList := make([]provider.ToolDefinition, len(p.Tools))
	for i, t := range p.Tools {
		toolsList[i] = provider.ToolDefinition{
			Name:         t.Name,
			Description:  t.Description,
			Parameters:   t.Parameters,
			Kind:         t.Kind,
			Format:       t.Format,
			Provider:     t.Provider,
			ProviderType: t.ProviderType,
			Model:        t.Model,
		}
	}
	var abort chan struct{}
	if p.Abort != nil {
		abort = make(chan struct{})
		go func() {
			<-p.Abort
			close(abort)
		}()
	}
	return provider.ChatParams{
		Messages:      msgs,
		Tools:         toolsList,
		SystemPrompt:  p.SystemPrompt,
		ThinkingLevel: provider.ThinkingLevel(p.ThinkingLevel),
		MaxTokens:     p.MaxTokens,
		ModelID:       p.ModelID,
		Abort:         abort,
	}
}

// StreamEventToPublic converts internal StreamEvent to public.
func StreamEventToPublic(e provider.StreamEvent) agentpkg.StreamEvent {
	ev := agentpkg.StreamEvent{
		Type:             StreamEventTypeToPublic(e.Type),
		TextDelta:        e.TextDelta,
		ThinkDelta:       e.ThinkDelta,
		StopReason:       e.StopReason,
		Error:            e.Error,
		RetryAttempt:     e.RetryAttempt,
		RetryMaxAttempts: streamRetryMaxAttempts(e),
		RetryAfterMS:     e.RetryAfterMS,
		Attachments:      AttachmentsToPublic(e.Attachments),
	}
	if e.HostedItem != nil {
		ev.HostedItem = hostedItemToPublic(e.HostedItem)
	}
	if e.ToolCall != nil {
		ev.ToolCall = &agentpkg.ToolCallBlock{
			ID:               e.ToolCall.ID,
			Name:             e.ToolCall.Name,
			Kind:             e.ToolCall.Kind,
			Input:            e.ToolCall.Input,
			Arguments:        e.ToolCall.Arguments,
			InvalidArguments: e.ToolCall.InvalidArguments,
			ThoughtSignature: e.ToolCall.ThoughtSignature,
		}
	}
	if e.Usage != nil {
		ev.Usage = &agentpkg.Usage{
			InputTokens:  e.Usage.Input,
			OutputTokens: e.Usage.Output,
			CacheRead:    e.Usage.CacheRead,
			CacheWrite:   e.Usage.CacheWrite,
			TotalTokens:  e.Usage.TotalTokens,
		}
	}
	return ev
}

func streamRetryMaxAttempts(e provider.StreamEvent) int {
	if e.RetryMaxAttempts > 0 {
		return e.RetryMaxAttempts
	}
	return e.RetryMax
}

// ModelToPublic converts an internal *provider.Model to a public agent.ModelInfo.
func ModelToPublic(m *provider.Model) agentpkg.ModelInfo {
	if m == nil {
		return agentpkg.ModelInfo{}
	}
	info := agentpkg.ModelInfo{
		ID:            m.ID,
		Name:          m.Name,
		Provider:      m.Provider,
		Reasoning:     m.Reasoning,
		Input:         m.Input,
		ContextWindow: m.ContextWindow,
		MaxTokens:     m.MaxTokens,
	}
	if m.Compat != nil {
		info.Compat = &agentpkg.ModelCompat{
			ThinkingFormat:                      m.Compat.ThinkingFormat,
			RequiresReasoningContentOnAssistant: m.Compat.RequiresReasoningContentOnAssistant,
			ForceAdaptiveThinking:               m.Compat.ForceAdaptiveThinking,
			SupportsDeveloperRole:               m.Compat.SupportsDeveloperRole,
			SupportsStore:                       m.Compat.SupportsStore,
			SupportsReasoningEffort:             m.Compat.SupportsReasoningEffort,
			SupportsStrictMode:                  m.Compat.SupportsStrictMode,
			MaxTokensField:                      m.Compat.MaxTokensField,
			DisableSamplingParams:               m.Compat.DisableSamplingParams,
			SupportsCacheControlOnTools:         m.Compat.SupportsCacheControlOnTools,
			SupportsLongCacheRetention:          m.Compat.SupportsLongCacheRetention,
			SendSessionAffinityHeaders:          m.Compat.SendSessionAffinityHeaders,
			SupportsEagerToolInputStreaming:     m.Compat.SupportsEagerToolInputStreaming,
		}
	}
	return info
}

// PublicProviderAdapter wraps an internal provider.Provider to satisfy the public agentpkg.Provider interface.
type PublicProviderAdapter struct {
	inner provider.Provider
}

// NewPublicProviderAdapter creates a public Provider from an internal one.
func NewPublicProviderAdapter(inner provider.Provider) *PublicProviderAdapter {
	return &PublicProviderAdapter{inner: inner}
}

func (pa *PublicProviderAdapter) Name() string {
	return pa.inner.Name()
}

func (pa *PublicProviderAdapter) Models() []agentpkg.ModelInfo {
	innerModels := pa.inner.Models()
	models := make([]agentpkg.ModelInfo, len(innerModels))
	for i, m := range innerModels {
		models[i] = ModelToPublic(m)
	}
	return models
}

func (pa *PublicProviderAdapter) GetModel(id string) *agentpkg.ModelInfo {
	m := pa.inner.GetModel(id)
	if m == nil {
		return nil
	}
	pub := ModelToPublic(m)
	return &pub
}

func (pa *PublicProviderAdapter) Chat(ctx context.Context, params agentpkg.ChatParams) <-chan agentpkg.StreamEvent {
	internalParams := ChatParamsFromPublic(params)
	internalCh := pa.inner.Chat(ctx, internalParams)

	ch := make(chan agentpkg.StreamEvent, 100)
	go func() {
		defer close(ch)
		for e := range internalCh {
			ch <- StreamEventToPublic(e)
		}
	}()
	return ch
}

func StreamEventTypeFromPublic(t agentpkg.StreamEventType) provider.StreamEventType {
	switch t {
	case agentpkg.StreamStart:
		return provider.StreamStart
	case agentpkg.StreamTextDelta:
		return provider.StreamTextDelta
	case agentpkg.StreamThinkDelta:
		return provider.StreamThinkDelta
	case agentpkg.StreamToolCall:
		return provider.StreamToolCall
	case agentpkg.StreamHostedItem:
		return provider.StreamHostedItem
	case agentpkg.StreamUsage:
		return provider.StreamUsage
	case agentpkg.StreamDone:
		return provider.StreamDone
	case agentpkg.StreamError:
		return provider.StreamError
	case agentpkg.StreamRetry:
		return provider.StreamRetry
	default:
		return provider.StreamStart
	}
}

func StreamEventTypeToPublic(t provider.StreamEventType) agentpkg.StreamEventType {
	switch t {
	case provider.StreamStart:
		return agentpkg.StreamStart
	case provider.StreamTextDelta:
		return agentpkg.StreamTextDelta
	case provider.StreamThinkDelta:
		return agentpkg.StreamThinkDelta
	case provider.StreamToolCall:
		return agentpkg.StreamToolCall
	case provider.StreamHostedItem:
		return agentpkg.StreamHostedItem
	case provider.StreamUsage:
		return agentpkg.StreamUsage
	case provider.StreamDone:
		return agentpkg.StreamDone
	case provider.StreamError:
		return agentpkg.StreamError
	case provider.StreamRetry:
		return agentpkg.StreamRetry
	default:
		return agentpkg.StreamStart
	}
}

// WrapEventChan wraps an internal event channel into a public event channel.
func WrapEventChan(in <-chan Event) <-chan agentpkg.Event {
	out := make(chan agentpkg.Event, 100)
	go func() {
		defer close(out)
		for e := range in {
			out <- EventToPublic(e)
		}
	}()
	return out
}

// --- ProviderAdapter wraps a public agent.Provider to satisfy internal provider.Provider ---

// ProviderAdapter wraps a public agent.Provider to satisfy the internal provider.Provider interface.
// This enables the public Builder to supply an external Provider implementation.
type ProviderAdapter struct {
	provider.BaseProvider
	pub agentpkg.Provider
}

// NewProviderAdapter creates an internal Provider from a public one.
func NewProviderAdapter(pub agentpkg.Provider) *ProviderAdapter {
	pubModels := pub.Models()
	models := make([]*provider.Model, len(pubModels))
	for i, m := range pubModels {
		models[i] = ModelInfoToInternal(m)
	}
	return &ProviderAdapter{
		BaseProvider: provider.NewBaseProvider(pub.Name(), models),
		pub:          pub,
	}
}

// API returns the protocol/API type.
// The public provider interface does not expose API type, so we default to
// "openai-chat" which is the de-facto standard protocol.
func (pa *ProviderAdapter) API() string {
	return "openai-chat"
}

// Chat delegates to the public provider, converting between public and internal types.
func (pa *ProviderAdapter) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	pubParams := ChatParamsToPublic(params)
	pubCh := pa.pub.Chat(ctx, pubParams)

	ch := make(chan provider.StreamEvent, 100)
	go func() {
		defer close(ch)
		for e := range pubCh {
			ch <- StreamEventFromPublic(e)
		}
	}()
	return ch
}

// ModelInfoToInternal converts a public ModelInfo to an internal *Model.
func ModelInfoToInternal(m agentpkg.ModelInfo) *provider.Model {
	model := &provider.Model{
		ID:            m.ID,
		Name:          m.Name,
		Provider:      m.Provider,
		Reasoning:     m.Reasoning,
		Input:         m.Input,
		ContextWindow: m.ContextWindow,
		MaxTokens:     m.MaxTokens,
	}
	if m.Compat != nil {
		model.Compat = &provider.ModelCompat{
			ThinkingFormat:                      m.Compat.ThinkingFormat,
			RequiresReasoningContentOnAssistant: m.Compat.RequiresReasoningContentOnAssistant,
			ForceAdaptiveThinking:               m.Compat.ForceAdaptiveThinking,
			SupportsDeveloperRole:               m.Compat.SupportsDeveloperRole,
			SupportsStore:                       m.Compat.SupportsStore,
			SupportsReasoningEffort:             m.Compat.SupportsReasoningEffort,
			SupportsStrictMode:                  m.Compat.SupportsStrictMode,
			MaxTokensField:                      m.Compat.MaxTokensField,
			DisableSamplingParams:               m.Compat.DisableSamplingParams,
			SupportsCacheControlOnTools:         m.Compat.SupportsCacheControlOnTools,
			SupportsLongCacheRetention:          m.Compat.SupportsLongCacheRetention,
			SendSessionAffinityHeaders:          m.Compat.SendSessionAffinityHeaders,
			SupportsEagerToolInputStreaming:     m.Compat.SupportsEagerToolInputStreaming,
		}
	}
	return model
}

// ChatParamsToPublic converts internal ChatParams to public.
func ChatParamsToPublic(p provider.ChatParams) agentpkg.ChatParams {
	msgs := make([]agentpkg.Message, len(p.Messages))
	for i, m := range p.Messages {
		msgs[i] = MessageToPublic(m)
	}
	tools := make([]agentpkg.ToolDefinition, len(p.Tools))
	for i, t := range p.Tools {
		tools[i] = agentpkg.ToolDefinition{
			Name:         t.Name,
			Description:  t.Description,
			Parameters:   t.Parameters,
			Kind:         t.Kind,
			Format:       t.Format,
			Provider:     t.Provider,
			ProviderType: t.ProviderType,
			Model:        t.Model,
		}
	}
	var abort chan struct{}
	if p.Abort != nil {
		// The internal type is <-chan struct{}, but the public type is chan struct{}.
		// We create a bridging channel.
		abort = make(chan struct{})
		go func() {
			<-p.Abort
			close(abort)
		}()
	}
	return agentpkg.ChatParams{
		Messages:      msgs,
		Tools:         tools,
		SystemPrompt:  p.SystemPrompt,
		ThinkingLevel: agentpkg.ThinkingLevel(p.ThinkingLevel),
		MaxTokens:     p.MaxTokens,
		ModelID:       p.ModelID,
		Abort:         abort,
	}
}

// StreamEventFromPublic converts a public StreamEvent to internal.
func StreamEventFromPublic(e agentpkg.StreamEvent) provider.StreamEvent {
	ev := provider.StreamEvent{
		Type:             StreamEventTypeFromPublic(e.Type),
		TextDelta:        e.TextDelta,
		ThinkDelta:       e.ThinkDelta,
		StopReason:       e.StopReason,
		Error:            e.Error,
		RetryAttempt:     e.RetryAttempt,
		RetryMax:         e.RetryMaxAttempts,
		RetryMaxAttempts: e.RetryMaxAttempts,
		RetryAfterMS:     e.RetryAfterMS,
		Attachments:      AttachmentsFromPublic(e.Attachments),
	}
	if e.HostedItem != nil {
		ev.HostedItem = &provider.HostedItem{ID: e.HostedItem.ID, Type: e.HostedItem.Type, Status: e.HostedItem.Status, OutputIndex: e.HostedItem.OutputIndex, Metadata: e.HostedItem.Metadata}
	}
	if e.ToolCall != nil {
		ev.ToolCall = &provider.ToolCallBlock{
			ID:               e.ToolCall.ID,
			Name:             e.ToolCall.Name,
			Kind:             e.ToolCall.Kind,
			Input:            e.ToolCall.Input,
			Arguments:        e.ToolCall.Arguments,
			InvalidArguments: e.ToolCall.InvalidArguments,
			ThoughtSignature: e.ToolCall.ThoughtSignature,
		}
	}
	if e.Usage != nil {
		ev.Usage = &provider.Usage{
			Input:       e.Usage.InputTokens,
			Output:      e.Usage.OutputTokens,
			CacheRead:   e.Usage.CacheRead,
			CacheWrite:  e.Usage.CacheWrite,
			TotalTokens: e.Usage.TotalTokens,
		}
	}
	return ev
}

// --- AgentAdapter wraps internal Agent to satisfy public agent.Agent interface ---

// AgentAdapter wraps an internal *Agent and satisfies the public agent.Agent interface.
type AgentAdapter struct {
	inner *Agent
}

// NewAgentAdapter creates an adapter that wraps an internal Agent.
func NewAgentAdapter(a *Agent) *AgentAdapter {
	return &AgentAdapter{inner: a}
}

func (a *AgentAdapter) ID() agentpkg.AgentID       { return a.inner.id }
func (a *AgentAdapter) ParentID() agentpkg.AgentID { return a.inner.parentID }
func (a *AgentAdapter) Abort()                     { a.inner.Abort() }
func (a *AgentAdapter) HandleApprovalResponse(id string, approved bool) {
	a.inner.HandleApprovalResponse(id, approved)
}

func (a *AgentAdapter) HandleQuestionResponse(questionID string, answer string) {
	a.inner.HandleQuestionResponse(questionID, answer)
}

// DeliverQuestionAnswer exposes the atomic answer delivery to callers that
// answer on another agent's behalf; it is intentionally not part of the public
// QuestionHandler interface so existing implementers keep compiling.
func (a *AgentAdapter) DeliverQuestionAnswer(questionID string, answer string) bool {
	return a.inner.DeliverQuestionAnswer(questionID, answer)
}
func (a *AgentAdapter) Run(ctx context.Context, userMsg string) <-chan agentpkg.Event {
	return WrapEventChan(a.inner.Run(ctx, userMsg))
}
func (a *AgentAdapter) RunWithMessages(ctx context.Context, msgs []agentpkg.Message) <-chan agentpkg.Event {
	return WrapEventChan(a.inner.RunWithMessages(ctx, MessagesFromPublic(msgs)))
}
func (a *AgentAdapter) GetMessages() []agentpkg.Message {
	return MessagesToPublic(a.inner.GetMessages())
}
func (a *AgentAdapter) SetMessages(msgs []agentpkg.Message) {
	a.inner.SetMessages(MessagesFromPublic(msgs))
}
func (a *AgentAdapter) GetContextUsage() *agentpkg.ContextUsage {
	return ContextUsageToPublic(a.inner.GetContextUsage())
}
func (a *AgentAdapter) LoadHistoryMessages(msgs []agentpkg.Message) {
	a.inner.LoadHistoryMessages(MessagesFromPublic(msgs))
}

func (a *AgentAdapter) GetContext() *agentpkg.AgentContext {
	x := a.inner.GetContext()
	if x == nil {
		return nil
	}
	return &agentpkg.AgentContext{
		SystemPrompt: x.SystemPrompt,
		Messages:     MessagesToPublic(x.Messages),
	}
}

func (a *AgentAdapter) SetContext(ctx *agentpkg.AgentContext) {
	a.inner.SetContext(&AgentContext{
		SystemPrompt: ctx.SystemPrompt,
		Messages:     MessagesFromPublic(ctx.Messages),
	})
}

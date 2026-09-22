package bootstrap

import (
	"context"

	"github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/config"
	internalprovider "github.com/oschina/mothx/internal/provider"
)

// providerAdapter exposes an internal provider through the public agent.Provider
// interface. It lives in bootstrap (not in the public agent package) so the
// public SDK stays free of internal imports; external modules enable it by
// blank-importing this package.
type providerAdapter struct {
	inner internalprovider.Provider
}

func (a *providerAdapter) Chat(ctx context.Context, params agent.ChatParams) <-chan agent.StreamEvent {
	internalParams := internalprovider.ChatParams{
		Messages:      make([]internalprovider.Message, len(params.Messages)),
		Tools:         make([]internalprovider.ToolDefinition, len(params.Tools)),
		SystemPrompt:  params.SystemPrompt,
		ThinkingLevel: internalprovider.ThinkingLevel(params.ThinkingLevel),
		MaxTokens:     params.MaxTokens,
		ModelID:       params.ModelID,
		Abort:         params.Abort,
	}
	for i, m := range params.Messages {
		internalParams.Messages[i] = internalprovider.Message{
			Role:           string(m.Role),
			Content:        m.Content,
			Contents:       make([]internalprovider.ContentBlock, len(m.Contents)),
			IsError:        m.IsError,
			SystemInjected: m.SystemInjected,
			ToolCallID:     m.ToolCallID,
			ToolName:       m.ToolName,
			ToolKind:       m.ToolKind,
			Attachments:    attachmentsFromPublic(m.Attachments),
		}
		for j, cb := range m.Contents {
			internalParams.Messages[i].Contents[j] = internalprovider.ContentBlock{
				Type:      cb.Type,
				Text:      cb.Text,
				Thinking:  cb.Thinking,
				Signature: cb.Signature,
			}
			if cb.Image != nil {
				internalParams.Messages[i].Contents[j].Image = &internalprovider.ImageContent{
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
			if cb.Audio != nil {
				internalParams.Messages[i].Contents[j].Audio = &internalprovider.AudioContent{MimeType: cb.Audio.MimeType, Format: cb.Audio.Format, Data: cb.Audio.Data, URL: cb.Audio.URL, Bytes: cb.Audio.Bytes}
			}
			if cb.Video != nil {
				internalParams.Messages[i].Contents[j].Video = &internalprovider.VideoContent{MimeType: cb.Video.MimeType, Data: cb.Video.Data, URL: cb.Video.URL, Bytes: cb.Video.Bytes}
			}
			if cb.File != nil {
				internalParams.Messages[i].Contents[j].File = &internalprovider.FileContent{ID: cb.File.ID, URL: cb.File.URL, Data: cb.File.Data, Filename: cb.File.Filename, MimeType: cb.File.MimeType, Title: cb.File.Title, Description: cb.File.Description, Size: cb.File.Size}
			}
			if cb.ToolCall != nil {
				internalParams.Messages[i].Contents[j].ToolCall = &internalprovider.ToolCallBlock{
					ID:               cb.ToolCall.ID,
					Name:             cb.ToolCall.Name,
					Kind:             cb.ToolCall.Kind,
					Input:            cb.ToolCall.Input,
					Arguments:        cb.ToolCall.Arguments,
					InvalidArguments: cb.ToolCall.InvalidArguments,
					ThoughtSignature: cb.ToolCall.ThoughtSignature,
				}
			}
			if cb.CacheControl != nil {
				internalParams.Messages[i].Contents[j].CacheControl = &internalprovider.CacheControl{Type: cb.CacheControl.Type}
			}
		}
	}
	for i, t := range params.Tools {
		internalParams.Tools[i] = internalprovider.ToolDefinition{
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

	ch := make(chan agent.StreamEvent, 100)
	go func() {
		defer close(ch)
		for ev := range a.inner.Chat(ctx, internalParams) {
			ch <- agent.StreamEvent{
				Type:             streamEventTypeToPublic(ev.Type),
				TextDelta:        ev.TextDelta,
				ThinkDelta:       ev.ThinkDelta,
				ToolCall:         toolCallToPublic(ev.ToolCall),
				HostedItem:       hostedItemToPublic(ev.HostedItem),
				Usage:            usageToPublic(ev.Usage),
				Error:            ev.Error,
				StopReason:       ev.StopReason,
				RetryAttempt:     ev.RetryAttempt,
				RetryMaxAttempts: streamRetryMaxAttempts(ev),
				RetryAfterMS:     ev.RetryAfterMS,
			}
		}
	}()
	return ch
}

func streamEventTypeToPublic(t internalprovider.StreamEventType) agent.StreamEventType {
	switch t {
	case internalprovider.StreamStart:
		return agent.StreamStart
	case internalprovider.StreamTextDelta:
		return agent.StreamTextDelta
	case internalprovider.StreamThinkDelta, internalprovider.StreamThinkSignature:
		return agent.StreamThinkDelta
	case internalprovider.StreamToolCall:
		return agent.StreamToolCall
	case internalprovider.StreamHostedItem:
		return agent.StreamHostedItem
	case internalprovider.StreamUsage:
		return agent.StreamUsage
	case internalprovider.StreamDone:
		return agent.StreamDone
	case internalprovider.StreamError:
		return agent.StreamError
	case internalprovider.StreamRetry:
		return agent.StreamRetry
	default:
		return agent.StreamError
	}
}

func streamRetryMaxAttempts(event internalprovider.StreamEvent) int {
	if event.RetryMaxAttempts > 0 {
		return event.RetryMaxAttempts
	}
	return event.RetryMax
}

func hostedItemToPublic(item *internalprovider.HostedItem) *agent.HostedItem {
	if item == nil {
		return nil
	}
	return &agent.HostedItem{ID: item.ID, Type: item.Type, Status: item.Status, OutputIndex: item.OutputIndex, Metadata: item.Metadata}
}

func (a *providerAdapter) Name() string { return a.inner.Name() }

func (a *providerAdapter) Models() []agent.ModelInfo {
	models := a.inner.Models()
	result := make([]agent.ModelInfo, len(models))
	for i, model := range models {
		result[i] = agent.ModelInfo{
			ID:            model.ID,
			Name:          model.Name,
			Provider:      model.Provider,
			Reasoning:     model.Reasoning,
			Input:         append([]string(nil), model.Input...),
			ContextWindow: model.ContextWindow,
			MaxTokens:     model.MaxTokens,
		}
	}
	return result
}

func (a *providerAdapter) GetModel(id string) *agent.ModelInfo {
	model := a.inner.GetModel(id)
	if model == nil {
		return nil
	}
	pub := &agent.ModelInfo{
		ID:            model.ID,
		Name:          model.Name,
		Provider:      model.Provider,
		Reasoning:     model.Reasoning,
		Input:         append([]string(nil), model.Input...),
		ContextWindow: model.ContextWindow,
		MaxTokens:     model.MaxTokens,
	}
	return pub
}

func toolCallToPublic(tc *internalprovider.ToolCallBlock) *agent.ToolCallBlock {
	if tc == nil {
		return nil
	}
	return &agent.ToolCallBlock{
		ID:               tc.ID,
		Name:             tc.Name,
		Kind:             tc.Kind,
		Input:            tc.Input,
		Arguments:        tc.Arguments,
		InvalidArguments: tc.InvalidArguments,
		ThoughtSignature: tc.ThoughtSignature,
	}
}

func usageToPublic(u *internalprovider.Usage) *agent.Usage {
	if u == nil {
		return nil
	}
	return &agent.Usage{
		InputTokens:  u.UncachedInputTokens(),
		OutputTokens: u.Output,
		CacheRead:    u.CacheRead,
		CacheWrite:   u.CacheWrite,
		TotalTokens:  u.TotalTokens,
	}
}

func attachmentsFromPublic(items []agent.Attachment) []internalprovider.Attachment {
	if len(items) == 0 {
		return nil
	}
	result := make([]internalprovider.Attachment, len(items))
	for i, item := range items {
		result[i] = internalprovider.Attachment{
			Kind: item.Kind, Name: item.Name, URL: item.URL, MediaType: item.MediaType,
			Metadata: item.Metadata, ProviderRef: item.ProviderRef,
		}
	}
	return result
}

func init() {
	agent.SetResolveProviderFunc(func(vendor, baseURL, api, apiKey string) (agent.Provider, error) {
		p, err := internalprovider.ResolveProvider(&config.ProviderConfig{
			Vendor:  vendor,
			BaseURL: baseURL,
			API:     api,
			APIKey:  apiKey,
		})
		if err != nil {
			return nil, err
		}
		return &providerAdapter{inner: p}, nil
	})
}

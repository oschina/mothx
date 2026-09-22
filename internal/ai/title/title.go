// Package title provides provider-neutral conversation title generation.
package title

import (
	"context"
	"errors"
	"strings"

	"github.com/oschina/mothx/internal/provider"
)

const (
	maxTitleRunes = 50
	titlePrompt   = "Based on this conversation, give it a concise display title. Reply with only the title, without quotes, markdown, or explanation. Use the user's language. Keep it under 50 characters."
	titleSystem   = "You create short conversation titles. Return only one plain-text title."
)

var ErrEmptyTitle = errors.New("title model returned an empty title")

// Generator generates short display titles through the common provider
// interface. Provider-specific request and response handling stays inside the
// provider adapters.
type Generator struct {
	Provider provider.Provider
	Model    *provider.Model
}

// Generate creates a normalized title from the supplied conversation.
func (g Generator) Generate(ctx context.Context, messages []provider.Message) (string, error) {
	if g.Provider == nil {
		return "", errors.New("title provider is nil")
	}
	if g.Model == nil {
		return "", errors.New("title model is nil")
	}
	if len(messages) == 0 {
		return "", ErrEmptyTitle
	}
	fallback := Fallback(messages)

	input := append([]provider.Message(nil), messages...)
	input = append(input, provider.NewUserMessage(titlePrompt))
	var raw strings.Builder
	for event := range g.Provider.Chat(ctx, provider.ChatParams{
		Messages:     input,
		SystemPrompt: titleSystem,
		ModelID:      g.Model.ID,
		MaxTokens:    32,
	}) {
		switch event.Type {
		case provider.StreamTextDelta:
			raw.WriteString(event.TextDelta)
		case provider.StreamError:
			if fallback != "" {
				return fallback, nil
			}
			if event.Error != nil {
				return "", event.Error
			}
			return "", errors.New("title provider returned an error")
		}
	}

	name := Normalize(raw.String())
	if name == "" {
		if fallback != "" {
			return fallback, nil
		}
		return "", ErrEmptyTitle
	}
	return name, nil
}

// Fallback creates a useful local title from the first user message when the
// title model is unavailable or returns no text.
func Fallback(messages []provider.Message) string {
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		if name := Normalize(messageText(message)); name != "" {
			return name
		}
	}
	return ""
}

func messageText(message provider.Message) string {
	if strings.TrimSpace(message.Content) != "" {
		return message.Content
	}
	var parts []string
	for _, block := range message.Contents {
		if strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, " ")
}

// Normalize removes common model formatting and limits a title by Unicode code
// points so multibyte languages are not cut by byte length.
func Normalize(raw string) string {
	name := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(raw, "\n", " "), "\r", " "))
	name = strings.Trim(name, " \\t\\\"'`#")
	if runes := []rune(name); len(runes) > maxTitleRunes {
		name = string(runes[:maxTitleRunes])
	}
	return name
}

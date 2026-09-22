package agent

import (
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

func TestSelectCacheMarkers(t *testing.T) {
	tests := []struct {
		name     string
		messages []provider.Message
		want     [2]int
	}{
		{
			name:     "empty messages",
			messages: []provider.Message{},
			want:     [2]int{-1, -1},
		},
		{
			name: "single message",
			messages: []provider.Message{
				{Role: "user", Content: "Hello"},
			},
			want: [2]int{-1, 0},
		},
		{
			name: "two messages",
			messages: []provider.Message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi"},
			},
			want: [2]int{0, 1},
		},
		{
			name: "skips system injected",
			messages: []provider.Message{
				{Role: "user", Content: "Hello", SystemInjected: true},
				{Role: "user", Content: "Message 1"},
				{Role: "assistant", Content: "Response 1"},
			},
			want: [2]int{1, 2},
		},
		{
			name: "multiple messages with injected",
			messages: []provider.Message{
				{Role: "user", Content: "Session context", SystemInjected: true},
				{Role: "user", Content: "Message 1"},
				{Role: "assistant", Content: "Response 1"},
				{Role: "user", Content: "Message 2"},
				{Role: "assistant", Content: "Response 2"},
			},
			want: [2]int{3, 4},
		},
		{
			name: "all injected messages",
			messages: []provider.Message{
				{Role: "user", Content: "Context 1", SystemInjected: true},
				{Role: "user", Content: "Context 2", SystemInjected: true},
			},
			want: [2]int{-1, -1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectCacheMarkers(tt.messages)
			if got != tt.want {
				t.Errorf("selectCacheMarkers() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyCacheMarkers(t *testing.T) {
	tests := []struct {
		name     string
		messages []provider.Message
		markers  [2]int
		wantCC   []bool // which messages should have cache_control
	}{
		{
			name: "no markers",
			messages: []provider.Message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi"},
			},
			markers: [2]int{-1, -1},
			wantCC:  []bool{false, false},
		},
		{
			name: "apply to last message",
			messages: []provider.Message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi"},
			},
			markers: [2]int{-1, 1},
			wantCC:  []bool{false, true},
		},
		{
			name: "apply to two messages",
			messages: []provider.Message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi"},
			},
			markers: [2]int{0, 1},
			wantCC:  []bool{true, true},
		},
		{
			name: "apply to content blocks",
			messages: []provider.Message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Contents: []provider.ContentBlock{
					{Type: "text", Text: "Response"},
				}},
			},
			markers: [2]int{0, 1},
			wantCC:  []bool{true, true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyCacheMarkers(tt.messages, tt.markers)

			// Check that original messages are not modified
			for i, msg := range tt.messages {
				if len(msg.Contents) > 0 {
					for _, block := range msg.Contents {
						if block.CacheControl != nil {
							t.Errorf("original message %d was modified", i)
						}
					}
				}
			}

			// Check cache_control is applied correctly
			for i, want := range tt.wantCC {
				msg := result[i]
				hasCC := false
				if len(msg.Contents) > 0 {
					lastBlock := msg.Contents[len(msg.Contents)-1]
					if lastBlock.CacheControl != nil && lastBlock.CacheControl.Type == "ephemeral" {
						hasCC = true
					}
				}
				if hasCC != want {
					t.Errorf("message %d: hasCC = %v, want %v", i, hasCC, want)
				}
			}
		})
	}
}

func TestSystemInjectedMessagesSkipped(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "Session context", SystemInjected: true},
		{Role: "user", Content: "Message 1"},
		{Role: "assistant", Content: "Response 1"},
		{Role: "user", Content: "Compression summary", SystemInjected: true},
		{Role: "user", Content: "Message 2"},
		{Role: "assistant", Content: "Response 2"},
	}

	markers := selectCacheMarkers(messages)

	// Markers should be on Message 2 and Response 2 (indices 4 and 5)
	if markers[0] != 4 || markers[1] != 5 {
		t.Errorf("selectCacheMarkers() = %v, want [4, 5]", markers)
	}

	// Apply markers
	result := applyCacheMarkers(messages, markers)

	// System injected messages should NOT have cache_control
	for i, msg := range result {
		if msg.SystemInjected {
			if len(msg.Contents) > 0 {
				for _, block := range msg.Contents {
					if block.CacheControl != nil {
						t.Errorf("system injected message %d has cache_control", i)
					}
				}
			}
		}
	}
}

func TestNewSystemInjectedUserMessage(t *testing.T) {
	msg := provider.NewSystemInjectedUserMessage("test context")

	if msg.Role != "user" {
		t.Errorf("Role = %q, want 'user'", msg.Role)
	}
	if msg.Content != "test context" {
		t.Errorf("Content = %q, want 'test context'", msg.Content)
	}
	if !msg.SystemInjected {
		t.Error("SystemInjected should be true")
	}
}

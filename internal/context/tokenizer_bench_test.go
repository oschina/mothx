package context

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

// BenchmarkEstimateTokens benchmarks token estimation for different text types.
func BenchmarkEstimateTokens(b *testing.B) {
	tests := []struct {
		name    string
		message provider.Message
	}{
		{
			name: "English_Short",
			message: provider.Message{
				Role:    "user",
				Content: "Hello, how are you today?",
			},
		},
		{
			name: "English_Long",
			message: provider.Message{
				Role:    "user",
				Content: strings.Repeat("This is a test message with some English content. ", 100),
			},
		},
		{
			name: "Chinese_Short",
			message: provider.Message{
				Role:    "user",
				Content: "你好，今天怎么样？",
			},
		},
		{
			name: "Chinese_Long",
			message: provider.Message{
				Role:    "user",
				Content: strings.Repeat("这是一个测试消息，包含一些中文内容。", 100),
			},
		},
		{
			name: "Japanese_Short",
			message: provider.Message{
				Role:    "user",
				Content: "こんにちは、今日はいかがですか？",
			},
		},
		{
			name: "Japanese_Long",
			message: provider.Message{
				Role:    "user",
				Content: strings.Repeat("これはテストメッセージで、いくつかの日本語コンテンツが含まれています。", 100),
			},
		},
		{
			name: "Korean_Short",
			message: provider.Message{
				Role:    "user",
				Content: "안녕하세요, 오늘 기분 어때요?",
			},
		},
		{
			name: "Korean_Long",
			message: provider.Message{
				Role:    "user",
				Content: strings.Repeat("이것은 테스트 메시지이며 일부 한국어 콘텐츠가 포함되어 있습니다.", 100),
			},
		},
		{
			name: "Mixed_CJK_English",
			message: provider.Message{
				Role:    "user",
				Content: "Hello 你好 こんにちは 안녕하세요 " + strings.Repeat("Mixed content 混合内容 ミックス된 콘텐츠 ", 50),
			},
		},
		{
			name: "Code_Snippet",
			message: provider.Message{
				Role:    "user",
				Content: "```go\nfunc main() {\n\tfmt.Println(\"Hello, world!\")\n}\n```",
			},
		},
	}

	estimator := GenericTokenEstimator{}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = estimator.EstimateTokens(tt.message)
			}
		})
	}
}

// BenchmarkEstimateTokens_CJK_Comparison compares estimation accuracy across CJK languages.
// The comparison uses the embedded DeepSeek V3 tokenizer for every language.
func BenchmarkEstimateTokens_CJK_Comparison(b *testing.B) {
	// Same semantic content in different languages
	messages := map[string]provider.Message{
		"English":  {Role: "user", Content: "The quick brown fox jumps over the lazy dog."},
		"Chinese":  {Role: "user", Content: "快速的棕色狐狸跳过了懒狗。"},
		"Japanese": {Role: "user", Content: "速い茶色の狐が怠け者の犬を飛び越えます。"},
		"Korean":   {Role: "user", Content: "빠른 갈색 여우가 게으른 개를 뛰어넘습니다."},
	}

	estimator := GenericTokenEstimator{}
	for lang, msg := range messages {
		b.Run(lang, func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(len(msg.Content)), "chars")
			for i := 0; i < b.N; i++ {
				_ = estimator.EstimateTokens(msg)
			}
		})
	}
}

// BenchmarkEstimateMessagesTokens benchmarks batch message estimation.
func BenchmarkEstimateMessagesTokens(b *testing.B) {
	messages := []provider.Message{
		{Role: "user", Content: strings.Repeat("User message content ", 50)},
		{Role: "assistant", Content: strings.Repeat("Assistant response ", 50)},
		{Role: "user", Content: "Follow-up question with 中文 日本語 한국어"},
		{Role: "assistant", Content: strings.Repeat("Detailed explanation ", 100)},
	}

	estimator := GenericTokenEstimator{}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = estimator.EstimateMessagesTokens(messages)
	}
}

// TestTokenEstimationCJKAccuracy documents the embedded tokenizer's behavior
// with CJK and English text.
func TestTokenEstimationCJKAccuracy(t *testing.T) {
	tests := []struct {
		name            string
		text            string
		byteLen         int
		estimatedTokens int
		note            string
	}{
		{
			name:            "Chinese_8chars",
			text:            "你好世界测试消息",
			byteLen:         24, // 8 Chinese chars × 3 bytes each
			estimatedTokens: 4,
			note:            "DeepSeek V3 tokenizer result",
		},
		{
			name:            "Japanese_10chars",
			text:            "こんにちは世界テスト",
			byteLen:         30, // 10 Japanese chars × 3 bytes each
			estimatedTokens: 7,
			note:            "DeepSeek V3 tokenizer result",
		},
		{
			name:            "Korean_10chars",
			text:            "안녕하세요세계테스트",
			byteLen:         30, // 10 Korean chars × 3 bytes each
			estimatedTokens: 9,
			note:            "DeepSeek V3 tokenizer result",
		},
		{
			name:            "English_40chars",
			text:            "Hello world this is a test message here!",
			byteLen:         40,
			estimatedTokens: 9,
			note:            "DeepSeek V3 tokenizer result",
		},
	}

	estimator := GenericTokenEstimator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := provider.Message{Role: "user", Content: tt.text}
			estimated := estimator.EstimateTokens(msg)

			if len(tt.text) != tt.byteLen {
				t.Errorf("byte length mismatch: got %d, want %d", len(tt.text), tt.byteLen)
			}
			if estimated != tt.estimatedTokens {
				t.Errorf("estimated tokens = %d, want %d", estimated, tt.estimatedTokens)
			}

			t.Logf("%s: %d bytes → %d estimated tokens. %s", tt.name, tt.byteLen, estimated, tt.note)
		})
	}
}

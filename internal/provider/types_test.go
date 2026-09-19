package provider

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestUsageCacheInfo(t *testing.T) {
	tests := []struct {
		name       string
		input      int
		cacheRead  int
		cacheWrite int
		total      int
		want       string
	}{
		// ── No data ──────────────────────────────────────────────────────────
		{
			name: "all_zeros_empty",
		},
		// ── Input with no cache activity ─────────────────────────────────────
		{
			name:  "input_only_shows_zero_pct",
			input: 1000,
			want:  "Cache: 0%",
		},
		{
			name:  "single_token_no_cache",
			input: 1,
			want:  "Cache: 0%",
		},
		// ── Cache hit percentage ──────────────────────────────────────────────
		{
			name:      "cache_25pct",
			input:     1000,
			cacheRead: 250,
			want:      "Cache: 20%",
		},
		{
			name:      "cache_50pct",
			input:     1000,
			cacheRead: 500,
			want:      "Cache: 33%",
		},
		{
			name:      "cache_75pct",
			input:     1000,
			cacheRead: 750,
			want:      "Cache: 43%",
		},
		{
			name:      "cache_100pct_exact",
			input:     1000,
			cacheRead: 1000,
			want:      "Cache: 50%",
		},
		{
			name:       "prompt_tokens_use_total_tokens_when_present",
			input:      400,
			cacheRead:  200,
			cacheWrite: 100,
			total:      700,
			want:       "Cache: 29%",
		},
		// ── Rounding ─────────────────────────────────────────────────────────
		// 333/1000 = 33.3… → rounds to 33%
		{
			name:      "rounding_down_33pct",
			input:     1000,
			cacheRead: 333,
			want:      "Cache: 25%",
		},
		// 667/1000 = 66.7… → rounds to 67%
		{
			name:      "rounding_up_67pct",
			input:     1000,
			cacheRead: 667,
			want:      "Cache: 40%",
		},
		// Small counts: 3/4 = 75%
		{
			name:      "small_counts_75pct",
			input:     4,
			cacheRead: 3,
			want:      "Cache: 43%",
		},
		// ── Defensive cap: cache read > input ────────────────────────────────
		{
			name:      "cache_read_exceeds_input_capped_at_100pct",
			input:     100,
			cacheRead: 200,
			want:      "Cache: 67%",
		},
		// ── Cache write only (Anthropic first-turn: no reads yet) ─────────────
		{
			name:       "cache_write_only_no_input",
			cacheWrite: 5000,
			want:       "CacheWrite: 5000",
		},
		// First turn: cache written, input sent, but no reads yet
		{
			name:       "cache_write_with_input_no_reads",
			input:      1000,
			cacheWrite: 5000,
			want:       "CacheWrite: 5000",
		},
		// ── Edge: cache read present but input is zero ────────────────────────
		// Can happen with malformed proxy responses; no meaningful percentage.
		{
			name:      "cache_read_without_input_empty",
			cacheRead: 500,
			want:      "Cache: 100%",
		},
		// ── Both cache read and write, no input ──────────────────────────────
		// Read > 0 so case 1 (Input > 0 && CacheRead > 0) doesn't match;
		// case 2 (CacheWrite > 0 && CacheRead == 0) doesn't match either.
		// Falls through to default → "".
		{
			name:       "read_and_write_no_input_empty",
			cacheRead:  200,
			cacheWrite: 300,
			want:       "Cache: 40%",
		},
		{
			name:      "anthropic_proxy_split_usage_50pct",
			input:     500,
			cacheRead: 500,
			want:      "Cache: 50%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &Usage{
				Input:       tt.input,
				CacheRead:   tt.cacheRead,
				CacheWrite:  tt.cacheWrite,
				TotalTokens: tt.total,
			}
			got := u.CacheInfo()
			if got != tt.want {
				t.Errorf("CacheInfo() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUsagePromptTokens(t *testing.T) {
	tests := []struct {
		name       string
		usage      *Usage
		wantPrompt int
	}{
		{
			name:       "nil usage",
			usage:      nil,
			wantPrompt: 0,
		},
		{
			name: "uses total tokens when present",
			usage: &Usage{
				Input:       400,
				Output:      50,
				CacheRead:   200,
				CacheWrite:  100,
				TotalTokens: 750,
			},
			wantPrompt: 700,
		},
		{
			name: "falls back to input when total missing",
			usage: &Usage{
				Input:      400,
				Output:     50,
				CacheRead:  200,
				CacheWrite: 100,
			},
			wantPrompt: 400,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.usage.PromptTokens(); got != tt.wantPrompt {
				t.Errorf("PromptTokens() = %d, want %d", got, tt.wantPrompt)
			}
		})
	}
}

func TestUsageTotalInputTokens(t *testing.T) {
	tests := []struct {
		name      string
		usage     *Usage
		wantInput int
	}{
		{
			name:      "nil usage",
			usage:     nil,
			wantInput: 0,
		},
		{
			name: "uses total tokens when present",
			usage: &Usage{
				Input:       400,
				Output:      50,
				CacheRead:   200,
				CacheWrite:  100,
				TotalTokens: 750,
			},
			wantInput: 700,
		},
		{
			name: "falls back to components when total missing",
			usage: &Usage{
				Input:      400,
				Output:     50,
				CacheRead:  200,
				CacheWrite: 100,
			},
			wantInput: 700,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.usage.TotalInputTokens(); got != tt.wantInput {
				t.Errorf("TotalInputTokens() = %d, want %d", got, tt.wantInput)
			}
		})
	}
}

func TestClassifyTurn(t *testing.T) {
	stub := &Usage{Input: 1, Output: 1, TotalTokens: 2} // gateway placeholder, like moark's
	real := &Usage{Input: 338962, Output: 17, TotalTokens: 338979}
	toolCall := []ToolCallBlock{{ID: "c1", Name: "ls"}}

	tests := []struct {
		name       string
		text       string
		think      string
		toolCalls  []ToolCallBlock
		usage      *Usage
		stopReason string
		want       TurnClassification
	}{
		// Content present => meaningful, regardless of usage/stop.
		{"text present", "hi", "", nil, nil, "", TurnMeaningful},
		{"thinking present", "", "hmm", nil, nil, "", TurnMeaningful},
		{"toolcall present", "", "", toolCall, nil, "", TurnMeaningful},

		// Empty content + stub usage + no stop => EMPTY (the reported bug).
		{"empty+stub+nostop", "", "", nil, stub, "", TurnEmpty},
		{"empty+nilusage+nostop", "", "", nil, nil, "", TurnEmpty},

		// Empty content but real usage => meaningful (model chose to stop, real tokens).
		{"empty+realusage+nostop", "", "", nil, real, "", TurnMeaningful},

		// Explicit stop reason honours empty body even with stub usage.
		{"empty+stub+stop", "", "", nil, stub, "stop", TurnMeaningful},
		{"empty+stub+end_turn", "", "", nil, stub, "end_turn", TurnMeaningful},
		{"empty+nilusage+stop", "", "", nil, nil, "stop", TurnMeaningful},

		// Whitespace/case variations of stop reason.
		{"empty+stub+ STOP ", "", "", nil, stub, " STOP ", TurnMeaningful},
		{"empty+stub+Completed", "", "", nil, stub, "Completed", TurnMeaningful},

		// Non-stop reasons do NOT override stub usage.
		{"empty+stub+length", "", "", nil, stub, "length", TurnEmpty},
		{"empty+stub+tool_use", "", "", nil, stub, "tool_use", TurnEmpty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyTurn(tt.text, tt.think, tt.toolCalls, tt.usage, tt.stopReason)
			if got != tt.want {
				t.Errorf("ClassifyTurn() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsStubUsage(t *testing.T) {
	if !IsStubUsage(nil) {
		t.Error("nil usage should be stub")
	}
	if !IsStubUsage(&Usage{Input: 1, Output: 1, TotalTokens: 2}) {
		t.Error("1/1/2 usage should be stub")
	}
	if !IsStubUsage(&Usage{Input: 1, Output: 50, TotalTokens: 51}) {
		t.Error("input<=1 usage should be stub (input is the hard signal)")
	}
	if IsStubUsage(&Usage{Input: 50, Output: 1, TotalTokens: 51}) {
		t.Error("small output with normal input should NOT be stub (model may emit few tokens)")
	}
	if IsStubUsage(&Usage{Input: 338962, Output: 17, TotalTokens: 338979}) {
		t.Error("real usage should not be stub")
	}
	if IsStubUsage(&Usage{Input: 100, Output: 100, TotalTokens: 200}) {
		t.Error("normal small usage should not be stub")
	}
}

func TestNormalizeToolCallArgumentsRepairsEmptyAndInvalidJSON(t *testing.T) {
	t.Run("empty raw message", func(t *testing.T) {
		call := &ToolCallBlock{ID: "empty", Name: "read", Arguments: json.RawMessage{}}
		args, repaired, err := NormalizeToolCallArguments(call)
		if err != nil {
			t.Fatalf("NormalizeToolCallArguments() error = %v", err)
		}
		if !repaired || len(args) != 0 || string(call.Arguments) != "{}" {
			t.Fatalf("repaired = %v, args = %#v, arguments = %q", repaired, args, call.Arguments)
		}
		if _, err := json.Marshal(call); err != nil {
			t.Fatalf("repaired tool call does not marshal: %v", err)
		}
	})

	t.Run("malformed raw message", func(t *testing.T) {
		call := &ToolCallBlock{ID: "invalid", Name: "bash", Arguments: json.RawMessage("{")}
		args, repaired, err := NormalizeToolCallArguments(call)
		if !repaired || args != nil {
			t.Fatalf("repaired = %v, args = %#v, want repaired with nil args", repaired, args)
		}
		if !errors.Is(err, ErrInvalidToolCallArguments) {
			t.Fatalf("error = %v, want ErrInvalidToolCallArguments", err)
		}
		if call.InvalidArguments != "{" || string(call.Arguments) != "{}" {
			t.Fatalf("invalidArguments = %q, arguments = %q", call.InvalidArguments, call.Arguments)
		}
		if _, err := json.Marshal(call); err != nil {
			t.Fatalf("sanitized tool call does not marshal: %v", err)
		}
	})
}

func TestNormalizeMessageCopiesToolCallArguments(t *testing.T) {
	original := Message{Contents: []ContentBlock{{Type: "toolCall", ToolCall: &ToolCallBlock{
		ID: "empty", Name: "read", Arguments: json.RawMessage{},
	}}}}
	normalized, notices := NormalizeMessage(original)
	if len(notices) != 1 || notices[0] != `tool "read": empty arguments` {
		t.Fatalf("notices = %#v", notices)
	}
	if len(original.Contents[0].ToolCall.Arguments) != 0 {
		t.Fatalf("NormalizeMessage mutated original arguments = %q", original.Contents[0].ToolCall.Arguments)
	}
	if got := string(normalized.Contents[0].ToolCall.Arguments); got != "{}" {
		t.Fatalf("normalized arguments = %q, want {}", got)
	}
	if _, err := json.Marshal(normalized); err != nil {
		t.Fatalf("normalized message does not marshal: %v", err)
	}
}

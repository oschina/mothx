package context

import "github.com/oschina/mothx/internal/provider"

// ContextUsage holds the current request-input footprint. TotalTokens is the
// context-window total and is deliberately input-only; output tokens from a
// previous response are not counted as current context.
type ContextUsage struct {
	Tokens        int      `json:"tokens"`         // Deprecated alias for TotalTokens.
	TotalTokens   int      `json:"total_tokens"`   // Full current input footprint.
	Input         int      `json:"input"`          // Non-cache input tokens.
	CacheRead     int      `json:"cache_read"`     // Input tokens served from cache.
	CacheWrite    int      `json:"cache_write"`    // Input tokens written to cache.
	ContextWindow int      `json:"context_window"` // Maximum context window.
	Percent       *float64 `json:"percent,omitempty"`
}

// EstimateTokens estimates token count for a message using the default estimator.
func EstimateTokens(msg provider.Message) int {
	return GenericTokenEstimator{}.EstimateTokens(msg)
}

// CalculateContextTokens returns the provider-reported input footprint. It
// excludes output tokens because this value is used against the next request's
// context window.
func CalculateContextTokens(usage *provider.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens > 0 && usage.Output >= 0 {
		if input := usage.TotalTokens - usage.Output; input > 0 {
			return input
		}
	}
	return usage.Input + usage.CacheRead + usage.CacheWrite
}

// EstimateContextTokens estimates context tokens from messages using the
// shared local tokenizer and provider usage when available.
func EstimateContextTokens(messages []provider.Message) (tokens int, lastUsageIndex int) {
	return EstimateContextTokensWithEstimator(messages, GenericTokenEstimator{})
}

// ContextUsageFromMessages returns a detailed input-footprint estimate. A
// provider usage record is authoritative for the latest completed assistant
// turn; only messages added after it are estimated locally.
func ContextUsageFromMessages(messages []provider.Message, estimator TokenEstimator) ContextUsage {
	if estimator == nil {
		estimator = GenericTokenEstimator{}
	}
	last := -1
	var usage *provider.Usage
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && messages[i].Usage != nil && CalculateContextTokens(messages[i].Usage) > 0 {
			last, usage = i, messages[i].Usage
			break
		}
	}
	result := ContextUsage{}
	if usage != nil {
		result.TotalTokens = CalculateContextTokens(usage)
		result.CacheRead = usage.CacheRead
		result.CacheWrite = usage.CacheWrite
		// ContextUsage.Input is the non-cache portion of the input footprint.
		// Provider usage is normalized here because OpenAI-compatible APIs
		// commonly include cached tokens in their prompt_tokens field while
		// Anthropic reports them separately.
		result.Input = result.TotalTokens - result.CacheRead - result.CacheWrite
		if result.Input < 0 {
			result.Input = usage.Input
		}
		if result.TotalTokens == 0 {
			result.TotalTokens = result.Input + result.CacheRead + result.CacheWrite
		}
	}
	start := 0
	if last >= 0 {
		start = last + 1
	}
	for _, msg := range messages[start:] {
		tokens := estimator.EstimateTokens(msg)
		result.TotalTokens += tokens
		// Locally estimated trailing content has no provider cache attribution.
		result.Input += tokens
	}
	result.Tokens = result.TotalTokens
	return result
}

// EstimateContextTokensWithEstimator estimates context tokens using provider
// usage when available, then the supplied estimator for trailing messages.
func EstimateContextTokensWithEstimator(messages []provider.Message, estimator TokenEstimator) (tokens int, lastUsageIndex int) {
	result := ContextUsageFromMessages(messages, estimator)
	lastUsageIndex = -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && messages[i].Usage != nil && CalculateContextTokens(messages[i].Usage) > 0 {
			lastUsageIndex = i
			break
		}
	}
	return result.TotalTokens, lastUsageIndex
}

// ShouldCompact checks if compaction should trigger based on context usage.
func ShouldCompact(contextTokens int, contextWindow int, reserveTokens int) bool {
	if contextWindow <= 0 {
		return false
	}
	return contextTokens > contextWindow-reserveTokens
}

// ShouldCompactPercent checks if compaction should trigger based on the
// percentage of the context window currently occupied.
func ShouldCompactPercent(contextTokens int, contextWindow int, threshold float64) bool {
	if contextWindow <= 0 || threshold <= 0 {
		return false
	}
	if threshold > 1 {
		threshold = threshold / 100
	}
	return float64(contextTokens)/float64(contextWindow) >= threshold
}

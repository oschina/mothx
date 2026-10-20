package provider

// ParameterFallback describes a request parameter that a provider rejected and
// that will be omitted from the retried request.
type ParameterFallback struct {
	// ModelID is the model whose request was rejected.
	ModelID string
	// Parameter is the canonical parameter name, for example "reasoning_effort".
	Parameter string
	// ThinkingFormat, when non-empty, is the compatibility value that should be
	// recorded for the model so later requests use it instead of the rejected
	// parameter (for example "thinking-only").
	ThinkingFormat string
}

// ParameterFallbackHandler records a runtime-discovered parameter fallback.
// Implementations must be best-effort: a handler failure never fails the run.
type ParameterFallbackHandler func(ParameterFallback)

// ParameterReasoningEffort is the canonical name of the OpenAI-compatible
// reasoning_effort parameter.
const ParameterReasoningEffort = "reasoning_effort"

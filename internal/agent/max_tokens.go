package agent

import (
	"github.com/oschina/mothx/internal/provider"
)

const defaultAutoMaxTokens = 8192

// ResolveMaxTokens returns the output limit configured for the active model.
// Known models use a conservative default cap; explicit model configuration is
// preserved exactly so an explicit value never gets silently changed.
func ResolveMaxTokens(model *provider.Model) int {
	if model == nil {
		return 0
	}
	if model.MaxTokensSet {
		return model.MaxTokens
	}
	if model.MaxTokens > 0 && model.MaxTokens < defaultAutoMaxTokens {
		return model.MaxTokens
	}
	if model.MaxTokens > 0 {
		return defaultAutoMaxTokens
	}
	return 0
}

// ResolveMaxTokensValue returns an explicit per-request value when set.
// An explicit zero on a model disables the output-token parameter; otherwise
// the configured model limit is used.
func ResolveMaxTokensValue(explicit int, model *provider.Model) int {
	if explicit > 0 {
		return explicit
	}
	if model != nil {
		if model.MaxTokensSet && model.MaxTokens == 0 {
			return 0
		}
		if model.MaxTokens > 0 {
			return model.MaxTokens
		}
	}
	return 0
}

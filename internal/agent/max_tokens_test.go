package agent

import (
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

func TestResolveMaxTokensUsesModelValue(t *testing.T) {
	model := &provider.Model{ID: "m", ContextWindow: 128000, MaxTokens: 64000, MaxTokensSet: true}

	if got := ResolveMaxTokens(model); got != 64000 {
		t.Fatalf("ResolveMaxTokens = %d, want 64000", got)
	}
}

func TestResolveMaxTokensUsesConservativeDefaultForKnownModel(t *testing.T) {
	model := &provider.Model{ID: "m", ContextWindow: 128000, MaxTokens: 64000}
	if got := ResolveMaxTokens(model); got != 8192 {
		t.Fatalf("ResolveMaxTokens = %d, want 8192", got)
	}
}

func TestResolveMaxTokensUsesNativeLimitBelowDefault(t *testing.T) {
	model := &provider.Model{ID: "m", ContextWindow: 8192, MaxTokens: 4096}
	if got := ResolveMaxTokens(model); got != 4096 {
		t.Fatalf("ResolveMaxTokens = %d, want 4096", got)
	}
}
func TestResolveMaxTokensUsesExplicitModelValue(t *testing.T) {
	model := &provider.Model{ID: "m", ContextWindow: 128000, MaxTokens: 64000, MaxTokensSet: true}
	if got := ResolveMaxTokens(model); got != 64000 {
		t.Fatalf("ResolveMaxTokens = %d, want 64000", got)
	}
}

func TestResolveMaxTokensValuePrefersExplicit(t *testing.T) {
	model := &provider.Model{ID: "m", MaxTokens: 64000}

	if got := ResolveMaxTokensValue(4096, model); got != 4096 {
		t.Fatalf("ResolveMaxTokensValue = %d, want 4096", got)
	}
}

func TestResolveMaxTokensReturnsZeroWhenExplicitlyDisabled(t *testing.T) {
	model := &provider.Model{ID: "m", MaxTokens: 0, MaxTokensSet: true}
	if got := ResolveMaxTokens(model); got != 0 {
		t.Fatalf("ResolveMaxTokens = %d, want 0", got)
	}
}

func TestResolveMaxTokensReturnsZeroWhenUnknown(t *testing.T) {
	if got := ResolveMaxTokens(nil); got != 0 {
		t.Fatalf("ResolveMaxTokens = %d, want 0", got)
	}
}

func TestEscalatedMaxTokensNeverExceedsModelLimit(t *testing.T) {
	a := &Agent{config: AgentLoopConfig{Config: Config{Model: &provider.Model{MaxTokens: 16384, ContextWindow: 128000}}}}
	if got := a.escalatedMaxTokens(8192); got != 16384 {
		t.Fatalf("escalatedMaxTokens = %d, want 16384", got)
	}
}
func TestClampMaxTokensToContext(t *testing.T) {
	if got := clampMaxTokensToContext(10000, 12000, 3000); got != 8488 {
		t.Fatalf("clampMaxTokensToContext = %d, want 8488", got)
	}
}

func TestClampMaxTokensToContextReservesSafetyMargin(t *testing.T) {
	if got := clampMaxTokensToContext(262144, 262144, 6523+1565); got != 253544 {
		t.Fatalf("clampMaxTokensToContext = %d, want 253544", got)
	}
}

func TestClampMaxTokensToContextKeepsValueWhenItFits(t *testing.T) {
	if got := clampMaxTokensToContext(4000, 12000, 3000); got != 4000 {
		t.Fatalf("clampMaxTokensToContext = %d, want 4000", got)
	}
}

func TestClampMaxTokensToContextKeepsZeroFallback(t *testing.T) {
	if got := clampMaxTokensToContext(0, 12000, 3000); got != 0 {
		t.Fatalf("clampMaxTokensToContext = %d, want 0", got)
	}
}

package openaiapi_test

import (
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/serve"
)

// Serve's API default mode is user configuration too. A router value must not
// make session construction fail, and it must not be widened to full access.
func TestServeRouterDefaultModeResolvesReadOnly(t *testing.T) {
	cfg := serve.DefaultConfig()
	cfg.API.DefaultMode = agentruntime.ModeRouter

	mode, err := agentruntime.PolicyForSource(agentruntime.SourceWebUI, cfg.API.DefaultMode).ResolveMode("", "")
	if err != nil {
		t.Fatalf("resolving a router serve default failed: %v", err)
	}
	if mode == agentruntime.ModeRouter {
		t.Fatal("serve resolved router as an effective default without a worker binding")
	}
	if mode != agentruntime.ModePlan {
		t.Fatalf("serve default resolved to %q, want the read-only %q", mode, agentruntime.ModePlan)
	}
}

// An explicitly requested router (a real activation path) must still be honored:
// the fallback applies to defaults only, never to an explicit selection that the
// Runtime binding fence has already validated.
func TestServeExplicitRouterRequestIsNotDowngraded(t *testing.T) {
	mode, err := agentruntime.PolicyForSource(agentruntime.SourceWebUI, "yolo").ResolveMode("", agentruntime.ModeRouter)
	if err != nil {
		t.Fatalf("resolving an explicit router request failed: %v", err)
	}
	if mode != agentruntime.ModeRouter {
		t.Fatalf("explicit router request resolved to %q, want router", mode)
	}
}

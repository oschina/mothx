package agent

import (
	"context"
	"strings"
	"testing"
)

func TestDelegateRejectsExternalTargetOutsideRouterMode(t *testing.T) {
	manager := NewAgentManager(nil)
	tool := NewDelegateSubAgentTool(manager)
	ctx := ContextWithParentMode(context.Background(), "yolo")
	if _, err := tool.Execute(ctx, map[string]any{"task": "inspect", "agent": "codex"}); err == nil ||
		!strings.Contains(err.Error(), "only available in Router mode") {
		t.Fatalf("external target outside Router = %v", err)
	}
}

func TestDelegateRouterRequiresAndRejectsLocalFallback(t *testing.T) {
	manager := NewAgentManager(nil)
	tool := NewDelegateSubAgentTool(manager)
	ctx := ContextWithParentMode(context.Background(), "router")
	if _, err := tool.Execute(ctx, map[string]any{"task": "inspect"}); err == nil ||
		!strings.Contains(err.Error(), "requires agent=") {
		t.Fatalf("Router delegation without a target = %v", err)
	}

	// A named target must go through the Runtime backend; when no backend is
	// installed Agent Core refuses instead of falling back to a local child.
	tool2 := NewDelegateSubAgentTool(manager)
	if _, err := tool2.Execute(ctx, map[string]any{"task": "inspect", "agent": "codex"}); err == nil ||
		!strings.Contains(err.Error(), "Runtime-installed child backend") {
		t.Fatalf("Router external target without a backend = %v", err)
	}
}

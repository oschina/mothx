package agent

import (
	"strings"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// TestFactoryExtraContextProviderReadsAtBuildTime covers the manager-backed
// agent path used by ESM workers, /agent agents, and mailbox continuations: a
// mid-session skill/context change must be visible without rebuilding the
// factory.
func TestFactoryExtraContextProviderReadsAtBuildTime(t *testing.T) {
	current := "CONTEXT-V1"
	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "m1", Name: "M1"}}, nil)
	factory := NewAgentFactoryWithOptions(
		p, p.Models()[0], config.DefaultSettings(), nil, "STATIC", "", nil,
		context.CompactionSettings{}, nil,
		AgentFactoryOptions{ExtraContextProvider: func() string { return current }},
	)
	mgr := session.New(t.TempDir(), t.TempDir())
	if err := mgr.InitWithID("factory-provider"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })

	first := factory.Create(AgentOptions{Session: mgr, ID: "main", Mode: "yolo"}).(*AgentAdapter)
	if !strings.Contains(first.inner.config.ExtraContext, "CONTEXT-V1") {
		t.Fatalf("first agent context = %q, want provider value", first.inner.config.ExtraContext)
	}
	if strings.Contains(first.inner.config.ExtraContext, "STATIC") {
		t.Fatalf("static factory context leaked past provider: %q", first.inner.config.ExtraContext)
	}

	current = "CONTEXT-V2"
	second := factory.Create(AgentOptions{Session: mgr, ID: "main-2", Mode: "yolo"}).(*AgentAdapter)
	if !strings.Contains(second.inner.config.ExtraContext, "CONTEXT-V2") {
		t.Fatalf("second agent context = %q, want updated provider value", second.inner.config.ExtraContext)
	}

	// Children inherit the parent's resolved config rather than re-reading the
	// provider, so lead persona/per-agent context is preserved.
	clone := factory.withParentRuntimeConfig(AgentLoopConfig{ExtraContext: "PARENT-CONTEXT"})
	child := clone.Create(AgentOptions{Session: mgr, ID: "child", ParentID: agentpkg.AgentID("main"), Mode: "yolo"}).(*AgentAdapter)
	if !strings.Contains(child.inner.config.ExtraContext, "PARENT-CONTEXT") {
		t.Fatalf("child context = %q, want parent config context", child.inner.config.ExtraContext)
	}
	if strings.Contains(child.inner.config.ExtraContext, "CONTEXT-V2") {
		t.Fatalf("child re-read the provider instead of the parent config: %q", child.inner.config.ExtraContext)
	}
}

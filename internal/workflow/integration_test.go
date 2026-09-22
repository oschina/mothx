package workflow

import (
	"context"
	"encoding/json"
	"testing"

	internalagent "github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	ctxpkg "github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
)

func TestAgentHostUsesDSLNameForAgentID(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 4096, MaxTokens: 1024},
	}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "audit complete"},
		{Type: provider.StreamDone},
	})
	sandboxMgr := sandbox.NewManager(t.TempDir())
	sandboxMgr.SetLevel(sandbox.LevelNone)
	settings := &config.Settings{SessionDir: t.TempDir()}
	factory := internalagent.NewAgentFactoryWithOptions(
		mockProvider,
		mockProvider.Models()[0],
		settings,
		sandboxMgr,
		"",
		"",
		nil,
		ctxpkg.CompactionSettings{},
		nil,
		internalagent.AgentFactoryOptions{WorkflowsEnabled: true},
	)
	manager := internalagent.NewAgentManager(factory)
	events := make(chan internalagent.Event, 10)
	host := &AgentHost{Manager: manager, ParentMode: "plan", ParentEventCh: events}

	_, err := host.RunAgent(context.Background(), AgentTask{
		Name:   "handler-audit",
		Mode:   "plan",
		Tools:  []string{"read"},
		Prompt: "Audit the handler.",
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}

	want := "agent-handler-audit"
	found := false
	for {
		select {
		case ev := <-events:
			if string(ev.AgentID) == want {
				found = true
			}
		default:
			if !found {
				t.Fatalf("expected forwarded event from %s", want)
			}
			return
		}
	}
}

func TestWorkflowAgentIDIncludesInstanceKey(t *testing.T) {
	if got, want := workflowAgentID("handler-audit", "r1"), "agent-handler-audit[r1]"; string(got) != want {
		t.Fatalf("workflowAgentID() = %q, want %q", got, want)
	}
}

func TestWorkflowRunToolReadOnlyAuditEndToEnd(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 4096, MaxTokens: 1024},
	}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "read-only audit complete"},
		{Type: provider.StreamDone},
	})
	sandboxMgr := sandbox.NewManager(t.TempDir())
	sandboxMgr.SetLevel(sandbox.LevelNone)
	settings := &config.Settings{SessionDir: t.TempDir()}
	factory := internalagent.NewAgentFactoryWithOptions(
		mockProvider,
		mockProvider.Models()[0],
		settings,
		sandboxMgr,
		"",
		"",
		nil,
		ctxpkg.CompactionSettings{},
		nil,
		internalagent.AgentFactoryOptions{WorkflowsEnabled: true},
	)
	manager := internalagent.NewAgentManager(factory)
	store := &memoryStore{}
	active := NewActiveRegistry()
	tool := NewRunToolWithActive(manager, store, active)

	result, err := tool.Execute(context.Background(), map[string]any{
		"source": `workflow("readonly audit", {concurrency:2, phases:[phase("scan", parallel(agent("api", {mode:"plan", tools:["read","grep"], prompt:"audit api"}), agent("agent", {mode:"plan", tools:["read","grep"], prompt:"audit agent"}))), phase("verify", agent("cross-check", {mode:"plan", tools:["read"], prompt:"cross-check findings"}))]});`,
	})
	if err != nil {
		t.Fatalf("workflow_run Execute() error = %v", err)
	}

	var parsed struct {
		ID      string            `json:"id"`
		Status  string            `json:"status"`
		Results map[string]string `json:"results"`
	}
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		t.Fatalf("parse workflow_run result: %v", err)
	}
	if parsed.Status != StatusDone {
		t.Fatalf("status = %s, want done", parsed.Status)
	}
	if parsed.Results["scan.api"] != StatusDone || parsed.Results["scan.agent"] != StatusDone || parsed.Results["verify.cross-check"] != StatusDone {
		t.Fatalf("unexpected result statuses: %#v", parsed.Results)
	}
	if active.IsActive(parsed.ID) {
		t.Fatalf("workflow %s should not remain active after completion", parsed.ID)
	}
	state, err := store.Load(context.Background(), parsed.ID)
	if err != nil {
		t.Fatalf("load stored workflow: %v", err)
	}
	if len(state.Phases) != 2 {
		t.Fatalf("phases = %d, want 2", len(state.Phases))
	}
}

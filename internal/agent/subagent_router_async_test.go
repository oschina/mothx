package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/tools"
)

func newRouterSpawnFixture(t *testing.T) (*SubAgentSpawnTool, *stubChildBackend, *AgentManager) {
	t.Helper()
	manager := NewAgentManager(nil)
	manager.Register(&stubBackendAgent{id: "lead"})
	backend := &stubChildBackend{events: []agentpkg.Event{{Type: agentpkg.EventRunFinished, Status: agentpkg.TaskSuccess}}}
	manager.SetChildBackend(backend)
	tool := NewSubAgentSpawnTool(manager)
	tool.SetRouterTarget(&RouterTarget{ID: "opencode", Available: []string{"opencode", "codex"}})
	return tool, backend, manager
}

func routerSpawnContext() context.Context {
	ctx := ContextWithParentMode(context.Background(), "router")
	return ContextWithAgentID(ctx, "lead")
}

func TestRouterSpawnCarriesContractToExternalBackend(t *testing.T) {
	tool, backend, manager := newRouterSpawnFixture(t)
	result, err := tool.Execute(routerSpawnContext(), map[string]any{
		"task":                  "implement the retry fix",
		"agent":                 "opencode",
		"role":                  "implement",
		"read_scope":            []any{"internal/retry"},
		"write_scope":           []any{"internal/retry/client.go"},
		"expected_deliverables": []any{"changed-files", "test-evidence"},
		"verification":          "go test ./internal/retry",
		"stop_conditions":       []any{"tests pass", "no unrelated edits"},
		"attachments":           []any{"resource-1", "resource-2"},
	})
	if err != nil {
		t.Fatalf("spawn external worker: %v", err)
	}

	if backend.calls != 1 {
		t.Fatalf("backend calls = %d, want 1", backend.calls)
	}
	opts := backend.opts
	if opts.ExternalAgent != "opencode" || opts.ExternalTask != "implement the retry fix" {
		t.Fatalf("external identity/task = %q/%q", opts.ExternalAgent, opts.ExternalTask)
	}
	if opts.ExternalRole != "implement" || opts.ExternalVerification != "go test ./internal/retry" {
		t.Fatalf("external role/verification = %q/%q", opts.ExternalRole, opts.ExternalVerification)
	}
	if len(opts.ExternalReadScope) != 1 || opts.ExternalReadScope[0] != "internal/retry" {
		t.Fatalf("read scope = %#v", opts.ExternalReadScope)
	}
	if len(opts.ExternalWriteScope) != 1 || opts.ExternalWriteScope[0] != "internal/retry/client.go" {
		t.Fatalf("write scope = %#v", opts.ExternalWriteScope)
	}
	if len(opts.ExternalDeliverable) != 2 || len(opts.ExternalStopConditions) != 2 {
		t.Fatalf("deliverables/stop conditions = %#v/%#v", opts.ExternalDeliverable, opts.ExternalStopConditions)
	}
	if len(opts.ExternalAttachmentIDs) != 2 || opts.ExternalAttachmentIDs[0] != "resource-1" {
		t.Fatalf("external attachments = %#v", opts.ExternalAttachmentIDs)
	}
	if opts.Worktree != nil {
		t.Fatal("external spawn passed a local worktree request")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		t.Fatalf("parse spawn result: %v", err)
	}
	if parsed["status"] != "running" || parsed["agent"] != "opencode" || parsed["backend"] != "acp" {
		t.Fatalf("spawn result = %#v", parsed)
	}
	if _, ok := parsed["timeout"]; ok {
		t.Fatal("external spawn advertised the local 30-minute timeout")
	}
	handle, _ := parsed["handle"].(string)
	if handle == "" {
		t.Fatal("spawn result has no handle")
	}
	waitForManagedAgentToStop(t, manager, agentpkg.AgentID(handle))
}

func TestRouterSpawnRequiresActivatedAgentAndRejectsLocalParameters(t *testing.T) {
	tests := map[string]map[string]any{
		"missing agent":  {"task": "inspect"},
		"wrong agent":    {"task": "inspect", "agent": "codex"},
		"member":         {"task": "inspect", "agent": "opencode", "member": "engineer"},
		"mode":           {"task": "inspect", "agent": "opencode", "mode": "plan"},
		"work_dir":       {"task": "inspect", "agent": "opencode", "work_dir": "/tmp/elsewhere"},
		"worktree":       {"task": "inspect", "agent": "opencode", "worktree": "isolated"},
		"tools":          {"task": "inspect", "agent": "opencode", "tools": []any{"read"}},
		"max_iterations": {"task": "inspect", "agent": "opencode", "max_iterations": float64(10)},
	}
	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			tool, backend, _ := newRouterSpawnFixture(t)
			if _, err := tool.Execute(routerSpawnContext(), params); err == nil {
				t.Fatalf("%s was accepted for a Router external worker", name)
			}
			if backend.calls != 0 {
				t.Fatalf("%s reached the child backend despite failing validation", name)
			}
		})
	}
}

func TestSpawnRejectsExternalTargetOutsideRouter(t *testing.T) {
	tool, backend, _ := newRouterSpawnFixture(t)
	tool.SetRouterTarget(nil)
	ctx := ContextWithParentMode(context.Background(), "yolo")
	ctx = ContextWithAgentID(ctx, "lead")
	if _, err := tool.Execute(ctx, map[string]any{"task": "inspect", "agent": "opencode"}); err == nil ||
		!strings.Contains(err.Error(), "only available in Router mode") {
		t.Fatalf("external target outside Router = %v", err)
	}
	if backend.calls != 0 {
		t.Fatal("non-Router spawn reached the external backend")
	}
	tool.SetRouterTarget(&RouterTarget{ID: "opencode"})
	if _, err := tool.Execute(ctx, map[string]any{"task": "inspect", "attachments": []any{"resource-1"}}); err == nil ||
		!strings.Contains(err.Error(), "only available for an external") {
		t.Fatalf("local attachment spawn = %v", err)
	}
}

type longRunningRouterStub struct {
	*stubBackendAgent
}

func (a *longRunningRouterStub) LongRunningFollowUp() bool { return true }

func TestRouterSendRejectsRunningExternalWorker(t *testing.T) {
	manager := NewAgentManager(nil)
	child := &longRunningRouterStub{stubBackendAgent: &stubBackendAgent{id: "worker"}}
	manager.Register(child)
	manager.MarkRunning(child.ID())

	tool := NewSubAgentSendTool(manager)
	if _, err := tool.Execute(context.Background(), map[string]any{
		"handle": "worker", "message": "continue",
	}); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("concurrent external follow-up = %v, want a running-state rejection", err)
	}
	if status, ok := manager.Status(child.ID()); !ok || status.State != "running" {
		t.Fatalf("rejected follow-up mutated status to %#v", status)
	}
}

func TestRouterWorkerMgrSchemaIsValidAndPinned(t *testing.T) {
	tool := NewDelegateWorkerMgrTool(NewAgentManager(nil))
	tool.SetRouterTarget(&RouterTarget{ID: `odd"id`})
	var schema struct {
		Properties map[string]struct {
			Const string `json:"const"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v\n%s", err, tool.Parameters())
	}
	if schema.Properties["agent"].Const != `odd"id` {
		t.Fatalf("agent const does not pin the worker: %s", tool.Parameters())
	}
}

func TestRouterModeToolsExposeOnlyWorkerMgr(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	manager := NewAgentManager(nil)
	RegisterRouterSubAgentTools(registry, manager)
	RegisterDelegateSubAgentTool(registry, manager)

	names := make(map[string]bool)
	for _, definition := range registry.ModeTools("router") {
		names[definition.Name] = true
	}
	if !names["delegate_worker_mgr"] {
		t.Fatal("Router tool surface is missing delegate_worker_mgr")
	}
	for _, name := range []string{"delegate_subagent", "subagent_spawn", "subagent_status", "subagent_send", "subagent_wait", "subagent_integrate", "subagent_destroy", "subagent_answer", "write", "edit", "insert", "bash"} {
		if names[name] {
			t.Errorf("Router tool surface unexpectedly includes %q", name)
		}
	}
}

type recordingChangeSetIntegrator struct {
	selections []ChangeSetSelection
	result     ChangeSetIntegrationResult
	err        error
}

func (r *recordingChangeSetIntegrator) IntegrateChangeSets(_ context.Context, selections []ChangeSetSelection) (ChangeSetIntegrationResult, error) {
	r.selections = append([]ChangeSetSelection(nil), selections...)
	if r.result.Status == "" {
		r.result = ChangeSetIntegrationResult{IntegrationID: "integration-1", Status: "applied"}
	}
	return r.result, r.err
}

func TestSubAgentIntegrateProjectsSelectionsToRuntime(t *testing.T) {
	manager := NewAgentManager(nil)
	integrator := &recordingChangeSetIntegrator{}
	manager.SetChangeSetIntegrator(integrator)
	tool := NewSubAgentIntegrateTool(manager)

	result, err := tool.Execute(context.Background(), map[string]any{
		"changes": []any{
			map[string]any{"handle": "worker-1", "digest": "sha256:one"},
			map[string]any{"handle": "worker-2", "digest": "sha256:two"},
		},
	})
	if err != nil {
		t.Fatalf("integrate: %v", err)
	}
	if len(integrator.selections) != 2 ||
		integrator.selections[0] != (ChangeSetSelection{Handle: "worker-1", Digest: "sha256:one"}) ||
		integrator.selections[1] != (ChangeSetSelection{Handle: "worker-2", Digest: "sha256:two"}) {
		t.Fatalf("runtime selections = %#v", integrator.selections)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Text), &decoded); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if decoded["integrationId"] != "integration-1" || decoded["status"] != "applied" {
		t.Fatalf("integrator result = %#v", decoded)
	}
}

func TestSubAgentIntegrateRejectsMalformedSelections(t *testing.T) {
	manager := NewAgentManager(nil)
	integrator := &recordingChangeSetIntegrator{}
	manager.SetChangeSetIntegrator(integrator)
	tool := NewSubAgentIntegrateTool(manager)
	cases := []map[string]any{
		{"changes": []any{}},
		{"changes": []any{map[string]any{"handle": "worker-1"}}},
		{"changes": []any{map[string]any{"digest": "sha256:one"}}},
		{"changes": []any{
			map[string]any{"handle": "worker-1", "digest": "sha256:one"},
			map[string]any{"handle": "worker-1", "digest": "sha256:one"},
		}},
	}
	for index, params := range cases {
		if _, err := tool.Execute(context.Background(), params); err == nil {
			t.Fatalf("case %d was accepted", index)
		}
	}
	if len(integrator.selections) != 0 {
		t.Fatalf("malformed selections reached the Runtime: %#v", integrator.selections)
	}
}

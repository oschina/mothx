package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func newWorkerMgrTestManager(t *testing.T) *AgentManager {
	t.Helper()
	manager := NewAgentManager(nil)
	manager.Register(&stubBackendAgent{id: "lead"})
	manager.Register(&stubBackendAgent{id: "worker-1", parent: "lead"})
	manager.MarkDone("worker-1", "finished")
	manager.Register(&stubBackendAgent{id: "worker-2", parent: "lead"})
	manager.MarkRunning("worker-2")
	return manager
}

func TestWorkerMgrSchemaEnumeratesEveryAction(t *testing.T) {
	tool := NewDelegateWorkerMgrTool(NewAgentManager(nil))
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v\n%s", err, tool.Parameters())
	}
	want := []string{"delegate", "spawn", "status", "list", "send", "wait", "destroy", "integrate", "answer"}
	if got := strings.Join(schema.Properties["action"].Enum, ","); got != strings.Join(want, ",") {
		t.Fatalf("action enum = %q, want %q", got, strings.Join(want, ","))
	}
	if strings.Join(schema.Required, ",") != "action" {
		t.Fatalf("required = %v, want only action", schema.Required)
	}
}

func TestWorkerMgrRequiredFieldsPerAction(t *testing.T) {
	tool := NewDelegateWorkerMgrTool(NewAgentManager(nil))
	cases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"delegate missing task", map[string]any{"action": "delegate", "agent": "opencode"}, "task"},
		{"delegate missing agent", map[string]any{"action": "delegate", "task": "x"}, "agent"},
		{"spawn missing task", map[string]any{"action": "spawn", "agent": "opencode"}, "task"},
		{"spawn missing agent", map[string]any{"action": "spawn", "task": "x"}, "agent"},
		{"status missing handle", map[string]any{"action": "status"}, "handle"},
		{"send missing handle", map[string]any{"action": "send", "message": "x"}, "handle"},
		{"send missing message", map[string]any{"action": "send", "handle": "w"}, "message"},
		{"wait missing handles", map[string]any{"action": "wait"}, "handle"},
		{"destroy missing handle", map[string]any{"action": "destroy"}, "handle"},
		{"integrate missing changes", map[string]any{"action": "integrate"}, "changes"},
		{"answer missing question", map[string]any{"action": "answer", "answer": "x"}, "question_id"},
		{"answer missing answer", map[string]any{"action": "answer", "question_id": "q1"}, "answer"},
		{"unknown action", map[string]any{"action": "steer"}, "unknown action"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tool.Execute(context.Background(), tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to name %q", err, tc.want)
			}
		})
	}
	if _, err := tool.Execute(context.Background(), map[string]any{}); err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("missing action error = %v", err)
	}
}

func TestWorkerMgrListOnlyProjectsChildren(t *testing.T) {
	tool := NewDelegateWorkerMgrTool(newWorkerMgrTestManager(t))
	result, err := tool.Execute(context.Background(), map[string]any{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Action  string           `json:"action"`
		Status  string           `json:"status"`
		Count   int              `json:"count"`
		Workers []map[string]any `json:"workers"`
	}
	if err := json.Unmarshal([]byte(result.Text), &decoded); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, result.Text)
	}
	if decoded.Action != "list" || decoded.Count != 2 {
		t.Fatalf("list result = %#v", decoded)
	}
	for _, worker := range decoded.Workers {
		if worker["handle"] == "lead" {
			t.Fatalf("list projected the lead agent: %#v", decoded.Workers)
		}
	}
}

func TestWorkerMgrStatusAndDestroyAddAction(t *testing.T) {
	manager := newWorkerMgrTestManager(t)
	tool := NewDelegateWorkerMgrTool(manager)

	status, err := tool.Execute(context.Background(), map[string]any{"action": "status", "handle": "worker-1"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(status.Text), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["action"] != "status" || decoded["status"] != "done" {
		t.Fatalf("status projection = %#v", decoded)
	}

	destroy, err := tool.Execute(context.Background(), map[string]any{"action": "destroy", "handle": "worker-1"})
	if err != nil {
		t.Fatal(err)
	}
	decoded = nil
	if err := json.Unmarshal([]byte(destroy.Text), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["action"] != "destroy" || decoded["status"] != "destroyed" {
		t.Fatalf("destroy projection = %#v", decoded)
	}
}

func TestWorkerMgrWaitResolvesMultipleHandles(t *testing.T) {
	tool := NewDelegateWorkerMgrTool(newWorkerMgrTestManager(t))
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "wait", "handles": []any{"worker-2", "worker-1"}, "timeout_seconds": float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Action   string `json:"action"`
		TimedOut bool   `json:"timed_out"`
	}
	if err := json.Unmarshal([]byte(result.Text), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Action != "wait" || decoded.TimedOut {
		t.Fatalf("multi-handle wait = %#v, want an immediate terminal result", decoded)
	}
}

type recordingWorkerQuestionResolver struct {
	questionID string
	answer     string
	err        error
}

func (r *recordingWorkerQuestionResolver) ResolveWorkerQuestion(_ context.Context, questionID, answer string) error {
	r.questionID, r.answer = questionID, answer
	return r.err
}

func TestWorkerMgrAnswerUsesRuntimeResolver(t *testing.T) {
	manager := newWorkerMgrTestManager(t)
	resolver := &recordingWorkerQuestionResolver{}
	manager.SetWorkerQuestionResolver(resolver)
	tool := NewDelegateWorkerMgrTool(manager)

	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "answer", "question_id": " q-1 ", "answer": " proceed ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.questionID != "q-1" || resolver.answer != "proceed" {
		t.Fatalf("resolver call = %q/%q", resolver.questionID, resolver.answer)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Text), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["action"] != "answer" || decoded["status"] != "answered" {
		t.Fatalf("answer projection = %#v", decoded)
	}
}

func TestWorkerMgrAnswerFailsClosedWithoutResolver(t *testing.T) {
	tool := NewDelegateWorkerMgrTool(newWorkerMgrTestManager(t))
	if _, err := tool.Execute(context.Background(), map[string]any{
		"action": "answer", "question_id": "q-1", "answer": "yes",
	}); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("answer without resolver = %v", err)
	}
}

func TestWorkerMgrIntegrateReusesIntegrator(t *testing.T) {
	manager := newWorkerMgrTestManager(t)
	integrator := &recordingChangeSetIntegrator{}
	manager.SetChangeSetIntegrator(integrator)
	tool := NewDelegateWorkerMgrTool(manager)
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "integrate",
		"changes": []any{
			map[string]any{"handle": "worker-1", "digest": "sha256:one"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(integrator.selections) != 1 || integrator.selections[0].Handle != "worker-1" {
		t.Fatalf("integration selections = %#v", integrator.selections)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Text), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["action"] != "integrate" {
		t.Fatalf("integrate projection = %#v", decoded)
	}
}

func TestWorkerMgrTargetPropagatesToChildTools(t *testing.T) {
	tool := NewDelegateWorkerMgrTool(NewAgentManager(nil))
	tool.SetRouterTarget(&RouterTarget{ID: "opencode", Summary: map[string]string{"opencode": "report/ask, write"}})
	if target, ok := tool.delegate.RouterTarget(); !ok || target.ID != "opencode" {
		t.Fatalf("delegate target = %#v, ok=%v", target, ok)
	}
	if target, ok := tool.spawn.RouterTarget(); !ok || target.ID != "opencode" {
		t.Fatalf("spawn target = %#v, ok=%v", target, ok)
	}
	if !strings.Contains(tool.Description(), "opencode=report/ask, write") {
		t.Fatalf("description does not publish the capability summary: %s", tool.Description())
	}
}

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/tools"
)

// delegateWorkerMgrToolName is the single Router management tool. Router mode
// exposes exactly this tool plus its read-only local surface; the local
// delegate_subagent / subagent_* tools are never installed in a Router session.
const delegateWorkerMgrToolName = "delegate_worker_mgr"

// Worker manager action names. They are the model-facing vocabulary and the
// dispatch keys; validateWorkerMgrParams owns the per-action required fields so
// a mega-schema cannot silently accept an incomplete call.
const (
	WorkerActionDelegate  = "delegate"
	WorkerActionSpawn     = "spawn"
	WorkerActionStatus    = "status"
	WorkerActionList      = "list"
	WorkerActionSend      = "send"
	WorkerActionWait      = "wait"
	WorkerActionDestroy   = "destroy"
	WorkerActionIntegrate = "integrate"
	WorkerActionAnswer    = "answer"
)

// WorkerMgrMaxWaitTimeout bounds one wait action. It only limits waiting; it
// never cancels the worker.
const WorkerMgrMaxWaitTimeout = 120 * time.Second

// DelegateWorkerMgrTool is the single Router worker-management entry point. It
// dispatches to the existing internal child runner/tools instead of copying any
// spawn, run, wait, or integration path.
type DelegateWorkerMgrTool struct {
	routerTargetState
	manager   *AgentManager
	delegate  *DelegateSubAgentTool
	spawn     *SubAgentSpawnTool
	status    *SubAgentStatusTool
	send      *SubAgentSendTool
	wait      tools.Tool
	destroy   *SubAgentDestroyTool
	integrate *SubAgentIntegrateTool
}

// NewDelegateWorkerMgrTool builds the Router management tool over one manager.
func NewDelegateWorkerMgrTool(manager *AgentManager) *DelegateWorkerMgrTool {
	tool := &DelegateWorkerMgrTool{manager: manager}
	if manager != nil {
		tool.delegate = NewDelegateSubAgentTool(manager)
		tool.spawn = NewSubAgentSpawnTool(manager)
		tool.status = NewSubAgentStatusTool(manager)
		tool.send = NewSubAgentSendTool(manager)
		tool.wait = NewSubAgentWaitTool(manager)
		tool.destroy = NewSubAgentDestroyTool(manager)
		tool.integrate = &SubAgentIntegrateTool{manager: manager}
	}
	return tool
}

func (t *DelegateWorkerMgrTool) Name() string { return delegateWorkerMgrToolName }

// SetRouterTarget publishes the activated worker set to the management tool and
// to the internal child tools it dispatches through, so their contract
// validation and error messages stay identical to the pre-migration surface.
func (t *DelegateWorkerMgrTool) SetRouterTarget(target *RouterTarget) {
	t.routerTargetState.SetRouterTarget(target)
	if t.delegate != nil {
		t.delegate.SetRouterTarget(target)
	}
	if t.spawn != nil {
		t.spawn.SetRouterTarget(target)
	}
}

func (t *DelegateWorkerMgrTool) Description() string {
	var builder strings.Builder
	builder.WriteString("Manage this Router session's external ACP worker. One tool covers the whole lifecycle: delegate (blocking) or spawn (async) a bounded task, then status/list/send/wait/destroy/integrate/answer. Worker mode, tools, work directory, and lifetime are owned by the profile and Runtime; do not pass local mode/work_dir/worktree/tools/max_iterations.")
	if target, ok := t.RouterTarget(); ok {
		accepted := acceptedRouterTargets(target)
		if len(accepted) > 1 {
			fmt.Fprintf(&builder, " Bound workers: %s (default %q).", strings.Join(accepted, ", "), target.ID)
		} else {
			fmt.Fprintf(&builder, " The only bound worker is %q; pass agent=%q exactly.", target.ID, target.ID)
		}
		if summaries := routerTargetSummaries(target); len(summaries) > 0 {
			builder.WriteString(" Worker capabilities:")
			for index, summary := range summaries {
				if index > 0 {
					builder.WriteString(";")
				}
				builder.WriteString(" ")
				builder.WriteString(summary)
			}
			builder.WriteString(".")
		}
		if others := otherRouterTargets(target); len(others) > 0 {
			fmt.Fprintf(&builder, " Configured but unbound profiles are not accepted: %s.", strings.Join(others, ", "))
		}
	}
	return builder.String()
}

func routerTargetSummaries(target RouterTarget) []string {
	if len(target.Summary) == 0 {
		return nil
	}
	accepted := acceptedRouterTargets(target)
	summaries := make([]string, 0, len(accepted))
	for _, id := range accepted {
		if summary := strings.TrimSpace(target.Summary[id]); summary != "" {
			summaries = append(summaries, id+"="+summary)
		}
	}
	return summaries
}

func (t *DelegateWorkerMgrTool) PromptSnippet() string {
	return "Manage the external Router worker lifecycle through one action-based tool"
}

func (t *DelegateWorkerMgrTool) PromptGuidelines() []string {
	return []string{
		"Use delegate when you need the result now; use spawn when you can do independent local work while the worker runs",
		"Always pass the bound agent profile ID; a local child fallback does not exist in Router mode",
		"Use status/list to inspect handles, send to append one follow-up turn to an idle worker, and destroy to release it",
		"Use answer only for a worker question surfaced through the mailbox or wait summary; the answer resolves the Runtime-owned decision",
		"integrate applies immutable change-set snapshots by exact handle+digest; treat the result as evidence, not proof",
	}
}

func (t *DelegateWorkerMgrTool) Parameters() json.RawMessage {
	agentSchema := t.agentParameterSchema()
	schema := fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["delegate", "spawn", "status", "list", "send", "wait", "destroy", "integrate", "answer"],
				"description": "delegate=blocking task; spawn=async task; status/list=read; send=append one turn to an idle worker; wait=wait for a handle; destroy=close; integrate=apply change sets; answer=resolve a worker question"
			},
			"task": {"type": "string", "description": "delegate/spawn required: objective, scope, expected deliverable, and stop conditions"},
			"agent": %s,
			"role": {"type": "string", "enum": ["investigate", "implement", "test", "review", "integrate"], "description": "Optional semantic role hint. Defaults to investigate."},
			"read_scope": {"type": "array", "items": {"type": "string"}, "description": "Bounded paths or search targets the worker should read."},
			"write_scope": {"type": "array", "items": {"type": "string"}, "description": "Paths the worker is expected to modify. Metadata for audit/overlap; not a sandbox for an external Agent."},
			"expected_deliverables": {"type": "array", "items": {"type": "string", "enum": ["findings", "changed-files", "test-evidence", "review"]}, "description": "Evidence the worker must return. Defaults are role-specific."},
			"verification": {"type": "string", "description": "The check or evidence the worker must report. Required for implement/integrate/test roles."},
			"stop_conditions": {"type": "array", "items": {"type": "string"}, "description": "Explicit completion and non-goals."},
			"system_prompt_extra": {"type": "string", "description": "Additional bounded context or constraints for the worker."},
			"attachments": {"type": "array", "items": {"type": "string"}, "description": "Runtime input resource IDs from the request manifest; the Runtime resolves and verifies them."},
			"handle": {"type": "string", "description": "status/send/destroy required; wait may pass one handle"},
			"handles": {"type": "array", "items": {"type": "string"}, "description": "wait may pass several handles; returns when one reaches a terminal state or the wait times out"},
			"message": {"type": "string", "description": "send required: one follow-up turn for an idle worker"},
			"timeout_seconds": {"type": "integer", "description": "wait optional; bounded by the Runtime (max 120s). It never cancels the worker."},
			"changes": {
				"type": "array",
				"description": "integrate required: immutable worker change sets",
				"items": {
					"type": "object",
					"properties": {
						"handle": {"type": "string"},
						"digest": {"type": "string"}
					},
					"required": ["handle", "digest"]
				}
			},
			"question_id": {"type": "string", "description": "answer required: the question ID from the worker question projection"},
			"answer": {"type": "string", "description": "answer required: the lead's answer"}
		},
		"required": ["action"]
	}`, agentSchema)
	return json.RawMessage(schema)
}

func (t *DelegateWorkerMgrTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	if t.manager == nil {
		return tools.ToolResult{}, fmt.Errorf("agent manager is not initialized")
	}
	action, _ := params["action"].(string)
	action = strings.TrimSpace(action)
	if err := validateWorkerMgrParams(action, params); err != nil {
		return tools.ToolResult{}, err
	}
	switch action {
	case WorkerActionDelegate:
		result, err := t.delegate.Execute(ctx, params)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return withWorkerAction(result, action), nil
	case WorkerActionSpawn:
		result, err := t.spawn.Execute(ctx, params)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return withWorkerAction(result, action), nil
	case WorkerActionStatus:
		result, err := t.status.Execute(ctx, params)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return withWorkerAction(result, action), nil
	case WorkerActionList:
		return t.executeList(action), nil
	case WorkerActionSend:
		result, err := t.send.Execute(ctx, params)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return withWorkerAction(result, action), nil
	case WorkerActionWait:
		return t.executeWait(ctx, params, action)
	case WorkerActionDestroy:
		result, err := t.destroy.Execute(ctx, params)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return withWorkerAction(result, action), nil
	case WorkerActionIntegrate:
		result, err := t.integrate.Execute(ctx, params)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return withWorkerAction(result, action), nil
	case WorkerActionAnswer:
		questionID, _ := params["question_id"].(string)
		answer, _ := params["answer"].(string)
		if err := t.manager.ResolveWorkerQuestion(ctx, strings.TrimSpace(questionID), strings.TrimSpace(answer)); err != nil {
			return tools.ToolResult{}, err
		}
		return toolJSON(map[string]any{"action": action, "status": "answered", "question_id": strings.TrimSpace(questionID)}), nil
	}
	return tools.ToolResult{}, fmt.Errorf("unknown action %q; use one of delegate, spawn, status, list, send, wait, destroy, integrate, answer", action)
}

func (t *DelegateWorkerMgrTool) executeList(action string) tools.ToolResult {
	statuses := t.manager.Statuses()
	workers := make([]map[string]any, 0, len(statuses))
	for _, status := range statuses {
		if status.ParentID == "" {
			continue
		}
		entry := map[string]any{
			"handle": string(status.ID),
			"status": status.State,
		}
		if status.Result != "" {
			entry["last_response"] = status.Result
		}
		if status.Error != "" {
			entry["error"] = status.Error
		}
		if len(status.Metadata) > 0 {
			entry["worker"] = status.Metadata
		}
		if !status.UpdatedAt.IsZero() {
			entry["updated_at"] = status.UpdatedAt.Format(time.RFC3339)
		}
		workers = append(workers, entry)
	}
	sort.Slice(workers, func(i, j int) bool {
		return workers[i]["handle"].(string) < workers[j]["handle"].(string)
	})
	return toolJSON(map[string]any{"action": action, "status": "ok", "workers": workers, "count": len(workers)})
}

func (t *DelegateWorkerMgrTool) executeWait(ctx context.Context, params map[string]any, action string) (tools.ToolResult, error) {
	handles := normalizedWorkerHandles(params)
	if len(handles) == 0 {
		return tools.ToolResult{}, fmt.Errorf("wait requires handle or handles")
	}
	timeoutMS := resolveWorkerWaitTimeoutMS(params)
	if len(handles) == 1 {
		params["handle"] = handles[0]
		params["timeout_ms"] = timeoutMS
		result, err := t.wait.Execute(ctx, params)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return withWorkerAction(result, action), nil
	}
	// Multiple handles: poll the canonical manager status and return as soon as
	// one worker is terminal or the bounded window elapses. This is a projection
	// of the same status state, not a second child state machine.
	type snapshot struct {
		Handle string `json:"handle"`
		Status string `json:"status"`
	}
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		finished := false
		summaries := make([]snapshot, 0, len(handles))
		for _, handle := range handles {
			status, ok := t.manager.Status(agentpkg.AgentID(handle))
			state := status.State
			if !ok || state == "" {
				if _, exists := t.manager.Get(agentpkg.AgentID(handle)); !exists {
					return tools.ToolResult{}, fmt.Errorf("sub-agent %q not found", handle)
				}
				state = "unknown"
			}
			summaries = append(summaries, snapshot{Handle: handle, Status: state})
			if isTerminalManagedState(state) {
				finished = true
			}
		}
		if finished {
			return toolJSON(map[string]any{"action": action, "status": "ok", "timed_out": false, "workers": summaries}), nil
		}
		if time.Now().After(deadline) {
			return toolJSON(map[string]any{"action": action, "status": "timeout", "timed_out": true, "workers": summaries}), nil
		}
		select {
		case <-ctx.Done():
			return tools.ToolResult{}, fmt.Errorf("wait for workers: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func validateWorkerMgrParams(action string, params map[string]any) error {
	requireString := func(name string) error {
		value, _ := params[name].(string)
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("action %q requires %s", action, name)
		}
		return nil
	}
	switch action {
	case WorkerActionDelegate, WorkerActionSpawn:
		if err := requireString("task"); err != nil {
			return err
		}
		return requireString("agent")
	case WorkerActionStatus, WorkerActionDestroy:
		return requireString("handle")
	case WorkerActionList:
		return nil
	case WorkerActionSend:
		if err := requireString("handle"); err != nil {
			return err
		}
		return requireString("message")
	case WorkerActionWait:
		if len(normalizedWorkerHandles(params)) == 0 {
			return fmt.Errorf("action %q requires handle or handles", action)
		}
		return nil
	case WorkerActionIntegrate:
		if _, err := decodeChangeSetSelections(params["changes"]); err != nil {
			return err
		}
		return nil
	case WorkerActionAnswer:
		if err := requireString("question_id"); err != nil {
			return err
		}
		return requireString("answer")
	default:
		return fmt.Errorf("unknown action %q; use one of delegate, spawn, status, list, send, wait, destroy, integrate, answer", action)
	}
}

func normalizedWorkerHandles(params map[string]any) []string {
	if values := nonEmptyStringParams(params["handles"]); len(values) > 0 {
		return values
	}
	if handle, _ := params["handle"].(string); strings.TrimSpace(handle) != "" {
		return []string{strings.TrimSpace(handle)}
	}
	return nil
}

func resolveWorkerWaitTimeoutMS(params map[string]any) int {
	if seconds, ok := params["timeout_seconds"].(float64); ok && seconds > 0 {
		limited := time.Duration(seconds * float64(time.Second))
		if limited > WorkerMgrMaxWaitTimeout {
			limited = WorkerMgrMaxWaitTimeout
		}
		return int(limited / time.Millisecond)
	}
	if milliseconds, ok := params["timeout_ms"].(float64); ok && milliseconds > 0 {
		return resolveSubAgentWaitTimeoutMS(map[string]any{"timeout_ms": milliseconds})
	}
	return subAgentWaitDefaultTimeoutMS
}

// withWorkerAction adds the canonical action to a delegated tool result. The
// underlying result already carries status and evidence; re-encoding keeps one
// shape for every projection.
func withWorkerAction(result tools.ToolResult, action string) tools.ToolResult {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Text), &decoded); err != nil {
		decoded = map[string]any{"status": "ok", "result": result.Text}
	}
	decoded["action"] = action
	return toolJSON(decoded)
}

func toolJSON(value map[string]any) tools.ToolResult {
	encoded, err := json.Marshal(value)
	if err != nil {
		return tools.NewTextToolResult(fmt.Sprintf(`{"status":"error","error":%q}`, err.Error()))
	}
	return tools.NewTextToolResult(string(encoded))
}

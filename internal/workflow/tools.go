package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	internalagent "github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/tools"
)

// RegisterTools registers workflow tools.
func RegisterTools(registry *tools.Registry, manager *internalagent.AgentManager, store Store) {
	if registry == nil || manager == nil {
		return
	}
	if store == nil {
		store = DefaultStore()
	}
	active := DefaultActiveRegistry()
	registry.Register(NewLintTool())
	registry.Register(NewRunToolWithActive(manager, store, active))
	registry.Register(NewStatusTool(store))
	registry.Register(NewCancelTool(active))
}

func DefaultStore() Store {
	return NewFileStore(filepath.Join(config.ConfigDir(), "workflows", "runs"))
}

type LintTool struct{}

func NewLintTool() *LintTool { return &LintTool{} }

func (t *LintTool) Name() string { return "workflow_lint" }
func (t *LintTool) Description() string {
	return "Validate workflow JavaScript DSL syntax and references without running worker agents."
}
func (t *LintTool) PromptSnippet() string {
	return "Validate workflow JavaScript DSL before workflow_run"
}
func (t *LintTool) PromptGuidelines() []string {
	return []string{
		"Use workflow_lint before workflow_run when generating or modifying non-trivial workflow DSL.",
		"workflow_lint validates JavaScript syntax, workflow/phase/agent calls, agent options, required prompts, and result references without invoking worker agents.",
	}
}
func (t *LintTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"source": {
				"type": "string",
				"description": "Complete raw JavaScript workflow DSL source to validate. Do not pass Markdown fences."
			}
		},
		"required": ["source"]
	}`)
}
func (t *LintTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	source, _ := params["source"].(string)
	source = strings.TrimSpace(source)
	if source == "" {
		return tools.ToolResult{}, fmt.Errorf("source is required")
	}
	result := lintWorkflowSource(ctx, source)
	data, err := json.Marshal(result)
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("marshal lint result: %w", err)
	}
	return tools.NewTextToolResult(string(data)), nil
}

type lintResult struct {
	Valid   bool     `json:"valid"`
	Status  string   `json:"status"`
	Error   string   `json:"error,omitempty"`
	Tasks   []string `json:"tasks,omitempty"`
	Results []string `json:"results,omitempty"`
}

type lintHost struct {
	mu    sync.Mutex
	tasks []string
}

func (h *lintHost) RunAgent(ctx context.Context, task AgentTask) (AgentResult, error) {
	key := taskStorageKey(task.Phase, task.Name, task.InstanceKey)
	h.mu.Lock()
	h.tasks = append(h.tasks, key)
	h.mu.Unlock()
	return AgentResult{
		Key:         key,
		Name:        task.Name,
		Phase:       task.Phase,
		InstanceKey: task.InstanceKey,
		Status:      StatusDone,
		Result:      "__workflow_lint_placeholder__",
	}, nil
}

func lintWorkflowSource(ctx context.Context, source string) lintResult {
	return lintWorkflowSourceWithin(ctx, source, lintEvalTimeout)
}

// lintWorkflowSourceWithin runs the same lint of source with an explicit VM
// evaluation budget, so the fast-fail behavior stays testable without waiting
// for the production lint timeout.
func lintWorkflowSourceWithin(ctx context.Context, source string, evalTimeout time.Duration) lintResult {
	host := &lintHost{}
	runner := &Runner{Host: host, Active: NewActiveRegistry(), Concurrency: 100, EvalTimeout: evalTimeout}
	state, err := runner.Run(ctx, source)
	res := lintResult{
		Valid: err == nil,
		Tasks: sortedStrings(host.tasks),
	}
	if state != nil {
		res.Status = state.Status
		res.Results = sortedResultKeys(state.Results)
	}
	if res.Status == "" {
		if err != nil {
			res.Status = StatusError
		} else {
			res.Status = StatusDone
		}
	}
	if err != nil {
		res.Error = err.Error()
	}
	return res
}

func sortedResultKeys(results map[string]AgentResult) []string {
	if len(results) == 0 {
		return nil
	}
	keys := make([]string, 0, len(results))
	for key := range results {
		keys = append(keys, key)
	}
	return sortedStrings(keys)
}

func sortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

type RunTool struct {
	manager *internalagent.AgentManager
	store   Store
	active  *ActiveRegistry
}

func NewRunTool(manager *internalagent.AgentManager, store Store) *RunTool {
	return NewRunToolWithActive(manager, store, DefaultActiveRegistry())
}

func NewRunToolWithActive(manager *internalagent.AgentManager, store Store, active *ActiveRegistry) *RunTool {
	if active == nil {
		active = DefaultActiveRegistry()
	}
	return &RunTool{manager: manager, store: store, active: active}
}

func (t *RunTool) Name() string { return "workflow_run" }
func (t *RunTool) Description() string {
	return "Run a JavaScript workflow DSL script that orchestrates worker agents."
}
func (t *RunTool) PromptSnippet() string {
	return "Run a multi-phase JavaScript workflow with worker agents"
}
func (t *RunTool) PromptGuidelines() []string {
	return []string{
		"Use workflow_lint before workflow_run when generating or modifying non-trivial workflow DSL.",
		"Use workflow_run for multi-phase tasks with independent worker-agent branches and fan-in verification.",
		"Write workflow DSL using plain JavaScript syntax; do not use Markdown code fences.",
		"Before calling workflow_run, ensure the source is one complete workflow(\"name\", body) form with valid JavaScript and closed strings.",
		"Use plain JavaScript arrays for tools, for example tools: [\"read\", \"grep\"].",
		"Use key for repeated logical agents, especially inside loops; keyed results are stored as phase.agent[key].",
		"Keep worker prompts explicit and bounded; use result to pass prior phase outputs into later phases.",
		"Set timeoutSeconds to the expected workflow duration; use 0 only for intentional continuous workflows that must not hit the default tool deadline.",
	}
}
func (t *RunTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"source": {
					"type": "string",
					"description": "Complete raw JavaScript workflow DSL source. Must call workflow(\"name\", body) with valid JavaScript; do not pass Markdown fences."
				},
				"timeoutSeconds": {
					"type": "integer",
					"minimum": 0,
					"description": "Agent-level timeout for this workflow_run call in seconds. Omit to use the default tool timeout; set to 0 for intentional continuous workflows with no agent-level deadline."
				}
			},
			"required": ["source"]
		}`)
}

func (t *RunTool) ExecutionTimeout(params map[string]any) (time.Duration, bool) {
	v, ok := params["timeoutSeconds"]
	if !ok {
		return 0, false
	}
	seconds, ok := numericParam(v)
	if !ok || seconds < 0 {
		return 0, false
	}
	if seconds == 0 {
		return 0, true
	}
	return time.Duration(seconds) * time.Second, true
}

func (t *RunTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	source, _ := params["source"].(string)
	source = strings.TrimSpace(source)
	if source == "" {
		return tools.ToolResult{}, fmt.Errorf("source is required")
	}
	parentID, _ := internalagent.AgentIDFromContext(ctx)
	parentEventCh, _ := internalagent.EventChanFromContext(ctx)
	parentRunCtx, _ := internalagent.ParentRunContextFromContext(ctx)
	parentMode, _ := internalagent.ParentModeFromContext(ctx)

	host := &AgentHost{
		Manager:       t.manager,
		ParentID:      parentID,
		ParentMode:    parentMode,
		ParentEventCh: parentEventCh,
		ParentRunCtx:  parentRunCtx,
	}
	runner := &Runner{
		Host:   host,
		Store:  t.store,
		Active: t.active,
		Progress: func(ev ProgressEvent) {
			if parentEventCh == nil || ev.RunID == "" {
				return
			}
			msg := strings.TrimSpace(ev.Message)
			if msg == "" {
				msg = strings.TrimSpace(strings.Join([]string{ev.Phase, ev.Task, ev.Status}, " "))
			}
			sendCtx := parentRunCtx
			if sendCtx == nil {
				sendCtx = ctx
			}
			eventType := agentpkg.EventStatus
			var eventErr error
			if ev.Phase == "" && ev.Task == "" {
				switch ev.Status {
				case StatusDone:
					eventType = agentpkg.EventDone
				case StatusError, StatusCanceled:
					eventType = agentpkg.EventError
					if msg != "" {
						eventErr = fmt.Errorf("%s", msg)
					}
				}
			}
			_ = internalagent.ForwardChildAgentEvent(sendCtx, parentEventCh, agentpkg.AgentID("workflow:"+ev.RunID), agentpkg.Event{
				Type:          eventType,
				StatusMessage: msg,
				Error:         eventErr,
			})
		},
	}
	state, err := runner.Run(ctx, source)
	if err != nil {
		if errors.Is(err, context.Canceled) && state != nil {
			return runToolResult(state)
		}
		return tools.ToolResult{}, err
	}
	return runToolResult(state)
}

func runToolResult(state *RunState) (tools.ToolResult, error) {
	data, err := json.Marshal(map[string]any{
		"id":      state.ID,
		"name":    state.Name,
		"status":  state.Status,
		"results": summarizeResults(state),
	})
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("marshal workflow run result: %w", err)
	}
	return tools.NewTextToolResult(string(data)), nil
}

type StatusTool struct {
	store Store
}

func NewStatusTool(store Store) *StatusTool {
	return &StatusTool{store: store}
}

func (t *StatusTool) Name() string { return "workflow_status" }
func (t *StatusTool) Description() string {
	return "Show workflow run status. Pass an id for details, or omit id to list recent runs."
}
func (t *StatusTool) PromptSnippet() string { return "Inspect workflow run status and results" }
func (t *StatusTool) PromptGuidelines() []string {
	return []string{"Use workflow_status to inspect workflow run state without invoking the LLM."}
}
func (t *StatusTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {"type": "string", "description": "Workflow run id. Omit to list recent runs."}
		}
	}`)
}

func (t *StatusTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	id, _ := params["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		runs, err := t.store.List(ctx)
		if err != nil {
			return tools.ToolResult{}, err
		}
		data, err := json.Marshal(runs)
		if err != nil {
			return tools.ToolResult{}, fmt.Errorf("marshal workflow runs: %w", err)
		}
		return tools.NewTextToolResult(string(data)), nil
	}
	state, err := t.store.Load(ctx, id)
	if err != nil {
		return tools.ToolResult{}, err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("marshal workflow state: %w", err)
	}
	return tools.NewTextToolResult(string(data)), nil
}

type CancelTool struct {
	active *ActiveRegistry
}

func NewCancelTool(active *ActiveRegistry) *CancelTool {
	if active == nil {
		active = DefaultActiveRegistry()
	}
	return &CancelTool{active: active}
}

func (t *CancelTool) Name() string { return "workflow_cancel" }
func (t *CancelTool) Description() string {
	return "Cancel an active workflow run by id."
}
func (t *CancelTool) PromptSnippet() string { return "Cancel an active workflow run" }
func (t *CancelTool) PromptGuidelines() []string {
	return []string{"Use workflow_cancel only for active workflow runs that should be interrupted."}
}
func (t *CancelTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {"type": "string", "description": "Workflow run id"}
		},
		"required": ["id"]
	}`)
}

func (t *CancelTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	id, _ := params["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return tools.ToolResult{}, fmt.Errorf("id is required")
	}
	if !t.active.Cancel(id) {
		return tools.ToolResult{}, fmt.Errorf("workflow run %q is not active", id)
	}
	data, err := json.Marshal(map[string]any{
		"id":     id,
		"status": StatusCanceled,
	})
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("marshal cancel result: %w", err)
	}
	return tools.NewTextToolResult(string(data)), nil
}

func numericParam(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int64:
		return x, true
	case int32:
		return int64(x), true
	case float64:
		if x != float64(int64(x)) {
			return 0, false
		}
		return int64(x), true
	case float32:
		if x != float32(int64(x)) {
			return 0, false
		}
		return int64(x), true
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func summarizeResults(state *RunState) map[string]string {
	out := make(map[string]string, len(state.Results))
	for key, result := range state.Results {
		out[key] = result.Status
	}
	return out
}

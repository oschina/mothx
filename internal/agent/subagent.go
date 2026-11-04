package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	ctxpkg "github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tools"
)

// ExternalChildTerminator is implemented by a Runtime-owned external child
// whose remote session and worker permit must be released after a blocking
// delegate finishes. Spawned children intentionally keep the session alive for
// subagent_send and release it through subagent_destroy.
type ExternalChildTerminator interface {
	TerminateExternalChild()
}

// SubAgentSpawnTool creates and starts a sub-agent.
type SubAgentSpawnTool struct {
	routerTargetState
	manager *AgentManager
}

// parseWorktreeRequest interprets the sub-agent worktree parameter. It accepts a
// boolean (true = create with a derived name) or a string ("true" = create, a
// name = create named, ""/"false"/"none"/"off" = inherit the parent workspace).
func parseWorktreeRequest(value any) *WorktreeSpec {
	switch v := value.(type) {
	case bool:
		if v {
			return &WorktreeSpec{}
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "false", "none", "off":
			return nil
		case "true":
			return &WorktreeSpec{}
		default:
			return &WorktreeSpec{Name: strings.TrimSpace(v)}
		}
	}
	return nil
}

// NewSubAgentSpawnTool creates a new subagent_spawn tool.
func NewSubAgentSpawnTool(m *AgentManager) *SubAgentSpawnTool {
	return &SubAgentSpawnTool{manager: m}
}

// RouterTarget describes the external ACP workers this session may use. The
// Runtime publishes the bound allowlist so the model is not left to guess a
// plausible name like "codex"; ID remains the default target.
type RouterTarget struct {
	// ID is the default profile ID used when the model names no explicit worker.
	ID string
	// Accepted is the ordered allowlist of bound profile IDs. Empty means ID is
	// the only accepted worker.
	Accepted []string
	// Available lists other configured profile IDs, for the error message a model
	// sees when it names the wrong one.
	Available []string
}

// routerTargetState is the Runtime-published external worker projection shared
// by tools that can target a configured ACP worker. Embedding it gives every
// such tool the same atomic, defensive setter without duplicating state.
type routerTargetState struct {
	router atomic.Pointer[RouterTarget]
}

// SetRouterTarget publishes the activated external worker to the model-facing tool
// description and schema. A nil or empty target restores the generic wording, so
// leaving Router mode cannot leave a stale profile ID in the prompt.
func (s *routerTargetState) SetRouterTarget(target *RouterTarget) {
	if target == nil || strings.TrimSpace(target.ID) == "" {
		s.router.Store(nil)
		return
	}
	copied := RouterTarget{ID: strings.TrimSpace(target.ID)}
	if len(target.Accepted) > 0 {
		copied.Accepted = append([]string(nil), target.Accepted...)
	}
	if len(target.Available) > 0 {
		copied.Available = append([]string(nil), target.Available...)
	}
	copied.Accepted = normalizedRouterAccepted(copied)
	s.router.Store(&copied)
}

// RouterTarget returns the currently published target, if any.
func (s *routerTargetState) RouterTarget() (RouterTarget, bool) {
	target := s.router.Load()
	if target == nil || target.ID == "" {
		return RouterTarget{}, false
	}
	return *target, true
}

// DelegateSubAgentTool runs exactly one delegated sub-agent task synchronously.
type DelegateSubAgentTool struct {
	routerTargetState
	manager *AgentManager
	busy    atomic.Bool
}

// NewDelegateSubAgentTool creates a blocking delegate_subagent tool.
func NewDelegateSubAgentTool(m *AgentManager) *DelegateSubAgentTool {
	return &DelegateSubAgentTool{manager: m}
}

func (t *DelegateSubAgentTool) Name() string { return "delegate_subagent" }
func (t *DelegateSubAgentTool) Description() string {
	if target, ok := t.RouterTarget(); ok {
		accepted := acceptedRouterTargets(target)
		if len(accepted) > 1 {
			return fmt.Sprintf("Delegate one bounded independent subtask to a blocking external ACP worker. This Router session may use %s; pass one of those values as agent (default %q). Waits until completion and returns a summarized result.",
				strings.Join(accepted, ", "), target.ID)
		}
		return fmt.Sprintf("Delegate one bounded independent subtask to a blocking sub-agent. In this Router session the only external ACP worker is %q: pass agent=%q exactly. Waits until completion and returns a summarized result.", target.ID, target.ID)
	}
	return "Delegate one bounded independent subtask to a blocking sub-agent. Waits until completion and returns a summarized result."
}
func (t *DelegateSubAgentTool) PromptSnippet() string {
	return "Delegate one bounded independent subtask to a blocking sub-agent"
}
func (t *DelegateSubAgentTool) PromptGuidelines() []string {
	return []string{
		"Use delegate_subagent when the subtask requires multi-step exploration (grep many files, trace code paths, run multiple commands) but you only need the final answer — the intermediate steps would bloat your context",
		"Do NOT delegate single-tool tasks (read one file, run one command) — direct execution is cheaper",
		"Do NOT delegate tasks smaller than ~3 tool calls, tasks needing user clarification mid-way, or highly stateful work depending on conversation history",
		"Write a specific task: state the exact goal, list relevant file paths/names, specify expected output format, and include stop conditions",
		"Only one delegated sub-agent can run at a time; review its result before acting — treat the output as evidence, not ground truth",
		"In Router mode, agent=<profile> is required and must name the session's activated worker; supply role, scope, deliverables, verification, and stop conditions as a bounded task contract",
	}
}
func (t *DelegateSubAgentTool) Parameters() json.RawMessage {
	// The agent field is built dynamically so the model sees the activated worker
	// ID (and the configured alternatives) instead of guessing a well-known name.
	agentSchema := t.agentParameterSchema()
	required := `["task"]`
	if _, ok := t.RouterTarget(); ok {
		required = `["task", "agent"]`
	}
	schema := fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"task": {"type": "string", "description": "A specific, bounded task description. Must include: (1) the exact goal or question, (2) relevant file paths or search patterns, (3) expected output format, (4) stop conditions. Example: 'Find all Go files in internal/serve/openaiapi/ that import net/http but do not call http.Error. Return file paths with line numbers.'"},
			"mode": {"type": "string", "enum": ["plan", "agent", "yolo", "os"], "description": "Local sub-agent execution mode. Defaults to the parent agent's mode; if unavailable, falls back to 'yolo'. 'agent' is balanced, 'yolo' is unrestricted, and 'plan' is read-only analysis. Rejected for an external Router worker, whose mode is the profile policy."},
			"work_dir": {"type": "string", "description": "Local sub-agent working directory (defaults to the parent workspace). Rejected for an external Router worker, which always uses the Runtime-resolved session directory."},
			"worktree": {"type": "string", "description": "Run a local delegated sub-agent in its own isolated git worktree. Pass \"true\" to create one, or a worktree name. Rejected for an external Router worker in this release."},
			"tools": {"type": "array", "items": {"type": "string"}, "description": "Restrict a local sub-agent to specific tools (empty = all tools except nested sub-agent/delegate). Rejected for an external Router worker, whose tools are owned by its own policy."},
			"max_iterations": {"type": "integer", "description": "Local sub-agent tool-call iteration limit (defaults to 100). Rejected for an external Router worker, whose lifetime is not bounded by the MothX iteration budget."},
			"system_prompt_extra": {"type": "string", "description": "Additional context or constraints for the sub-agent. Use to pass domain knowledge, coding conventions, or specific instructions not in the task description."},
			"agent": %s,
			"role": {"type": "string", "enum": ["investigate", "implement", "test", "review", "integrate"], "description": "Router only: the task role. Defaults to investigate. implement/integrate require a write_scope inside the profile's configured write roots and verification evidence."},
			"read_scope": {"type": "array", "items": {"type": "string"}, "description": "Router only: bounded paths or search targets the worker should read."},
			"write_scope": {"type": "array", "items": {"type": "string"}, "description": "Router only: paths the worker may modify. Required for write roles and must stay inside the profile write roots."},
			"expected_deliverables": {"type": "array", "items": {"type": "string", "enum": ["findings", "changed-files", "test-evidence", "review"]}, "description": "Router only: evidence the worker must return. Defaults are role-specific."},
			"verification": {"type": "string", "description": "Router only: the check or evidence the worker must report. Required for implement/integrate/test roles."},
			"stop_conditions": {"type": "array", "items": {"type": "string"}, "description": "Router only: explicit completion and non-goals."},
			"attachments": {"type": "array", "items": {"type": "string"}, "description": "Router only: Runtime input resource IDs from the request manifest. Bytes are resolved and integrity-checked by the Runtime, never read from a model-supplied path."}
		},
		"required": %s
	}`, agentSchema, required)
	return json.RawMessage(schema)
}

// agentParameterSchema renders the `agent` property. In Router mode it pins the
// value to the activated worker so a wrong name is a schema violation rather than
// a runtime surprise; otherwise it stays a free-form string with no misleading
// hint.
func (s *routerTargetState) agentParameterSchema() string {
	target, ok := s.RouterTarget()
	if !ok {
		return `{"type": "string", "description": "Router only: the activated external ACP worker profile ID. Not used outside Router mode."}`
	}
	accepted := acceptedRouterTargets(target)
	// Encode values with the JSON encoder rather than %q: Go quoting escapes
	// quotes Go-style, which is not valid inside a JSON string literal.
	field := map[string]any{
		"type":        "string",
		"description": fmt.Sprintf("Required. This Router session may delegate to the bound ACP worker %q; pass exactly that value.", target.ID),
		"const":       target.ID,
	}
	if len(accepted) > 1 {
		field = map[string]any{
			"type":        "string",
			"description": fmt.Sprintf("Required. Choose one bound ACP worker: %s. The default is %q.", strings.Join(accepted, ", "), target.ID),
			"enum":        accepted,
		}
	}
	if others := otherRouterTargets(target); len(others) > 0 {
		if len(accepted) > 1 {
			field["description"] = fmt.Sprintf("Required. Choose one bound ACP worker: %s. The default is %q. Configured but unbound profiles are not accepted: %s.",
				strings.Join(accepted, ", "), target.ID, strings.Join(others, ", "))
		} else {
			field["description"] = fmt.Sprintf("Required. This Router session delegates to the activated ACP worker %q; pass exactly that value. Other configured profiles are not accepted for this session: %s.",
				target.ID, strings.Join(others, ", "))
		}
	}
	encoded, err := json.Marshal(field)
	if err != nil {
		// A hand-built fallback keeps the schema valid even if encoding fails; the
		// Execute-time error still names the correct worker.
		return `{"type": "string", "description": "Required. Pass the activated external ACP worker profile ID."}`
	}
	return string(encoded)
}

func otherRouterTargets(target RouterTarget) []string {
	accepted := acceptedRouterTargets(target)
	isAccepted := make(map[string]bool, len(accepted))
	for _, id := range accepted {
		isAccepted[id] = true
	}
	others := make([]string, 0, len(target.Available))
	for _, id := range target.Available {
		if !isAccepted[id] {
			others = append(others, id)
		}
	}
	return others
}

// acceptedRouterTargets normalizes the target's accepted values. A Router
// binding always includes its default ID, even if an older caller only set ID.
func acceptedRouterTargets(target RouterTarget) []string {
	accepted := make([]string, 0, len(target.Accepted)+1)
	seen := make(map[string]bool, len(target.Accepted)+1)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		accepted = append(accepted, value)
	}
	add(target.ID)
	for _, value := range target.Accepted {
		add(value)
	}
	return accepted
}

func normalizedRouterAccepted(target RouterTarget) []string {
	accepted := acceptedRouterTargets(target)
	if len(accepted) <= 1 {
		return nil
	}
	return accepted
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (t *DelegateSubAgentTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	if t.manager == nil {
		return tools.ToolResult{}, fmt.Errorf("agent manager is not initialized")
	}
	if !t.busy.CompareAndSwap(false, true) {
		return tools.ToolResult{}, fmt.Errorf("a delegated sub-agent is already running")
	}
	defer t.busy.Store(false)

	parentMode, _ := ParentModeFromContext(ctx)
	externalAgent, err := resolveRouterExternalAgent(ctx, &t.routerTargetState, params)
	if err != nil {
		return tools.ToolResult{}, err
	}
	attachmentIDs := nonEmptyStringParams(params["attachments"])
	if externalAgent == "" && len(attachmentIDs) > 0 {
		return tools.ToolResult{}, fmt.Errorf("attachments are only available for an external Router worker")
	}

	started := time.Now()
	task, _ := params["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return tools.ToolResult{}, fmt.Errorf("task is required")
	}

	mode, _ := params["mode"].(string)
	if mode == "" {
		// Inherit parent agent's mode (yolo/agent/plan) instead of hardcoding "agent"
		if parentMode != "" {
			mode = parentMode
		}
	}
	workDir, _ := params["work_dir"].(string)
	if strings.TrimSpace(workDir) == "" {
		if parent, ok := WorkDirFromContext(ctx); ok {
			workDir = parent
		}
	}
	worktreeReq := parseWorktreeRequest(params["worktree"])
	maxIter := 100
	if v, ok := params["max_iterations"].(float64); ok && v > 0 {
		maxIter = int(v)
	}
	extra, _ := params["system_prompt_extra"].(string)

	var toolFilter []string
	if ts, ok := params["tools"].([]any); ok {
		for _, tt := range ts {
			if s, ok := tt.(string); ok {
				toolFilter = append(toolFilter, s)
			}
		}
	}

	parentID, _ := AgentIDFromContext(ctx)
	parentRunID, _ := ParentRunIDFromContext(ctx)
	parentEventCh, _ := EventChanFromContext(ctx)
	parentRunCtx, ok := ParentRunContextFromContext(ctx)
	if !ok || parentRunCtx == nil {
		parentRunCtx = ctx
	}
	policy := DefaultSubAgentPolicy()
	runCtx := parentRunCtx
	cancel := func() {}
	if externalAgent == "" {
		// Local delegated children keep their bounded per-agent budget. An
		// external Router worker is long-running by design: cancellation and the
		// shared safety boundaries decide its lifetime, never an elapsed clock.
		runCtx, cancel = context.WithTimeout(parentRunCtx, policy.TimeoutPerAgent)
	}
	defer cancel()

	options := AgentOptions{
		ParentID: parentID,
		Mode:     mode,
		WorkDir:  workDir,
		Worktree: worktreeReq,
		Tools:    toolFilter,
		// A blocking delegate's caller is parked inside this tool call, so nobody
		// could ever answer a child question. Remove the tool instead of letting
		// the child block until its run is torn down with "no answer received".
		ExcludeTools:      []string{"question"},
		SystemPromptExtra: extra,
		MaxIterations:     maxIter,
	}
	if externalAgent != "" {
		role, _ := params["role"].(string)
		verification, _ := params["verification"].(string)
		options.ExternalAgent = externalAgent
		options.ExternalTask = task
		options.ExternalRole = strings.TrimSpace(role)
		options.ExternalVerification = verification
		options.ExternalReadScope = stringSliceParam(params["read_scope"])
		options.ExternalWriteScope = stringSliceParam(params["write_scope"])
		options.ExternalDeliverable = stringSliceParam(params["expected_deliverables"])
		options.ExternalStopConditions = stringSliceParam(params["stop_conditions"])
		options.ExternalAttachmentIDs = attachmentIDs
		options.RuntimeContext = ctx
	}
	a, err := t.manager.Create(options)
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("create delegated sub-agent: %w", err)
	}
	defer t.manager.DetachChild(a.ID())

	t.manager.MarkRunning(a.ID())
	t.manager.SetCancel(a.ID(), cancel)
	defer t.manager.SetCancel(a.ID(), nil)

	var runErr error
	completed := false
	canceled := false
	toolCallCount := 0
	toolNames := make(map[string]int)
	ch := a.Run(runCtx, buildSubAgentTask(task))
	for e := range ch {
		if e.Type == agentpkg.EventToolApprovalRequest && parentEventCh != nil {
			_ = sendParentEvent(runCtx, parentEventCh, Event{
				Type:         EventToolApprovalRequest,
				AgentID:      a.ID(),
				ApprovalID:   e.ApprovalID,
				ApprovalTool: e.ApprovalTool,
				ApprovalArgs: e.ApprovalArgs,
			})
		}
		ForwardChildAgentEvent(runCtx, parentEventCh, a.ID(), e)
		// Members ask the lead, not the human: a blocking question must reach a
		// wake path instead of being consumed and dropped here.
		if e.Type == agentpkg.EventQuestionRequest {
			forwardMemberQuestion(runCtx, t.manager, parentEventCh, a.ID(), e, nil)
		}
		if e.Type == agentpkg.EventToolCall {
			toolCallCount++
			if e.ToolName != "" {
				toolNames[e.ToolName]++
			} else if e.ToolCall != nil && e.ToolCall.Name != "" {
				toolNames[e.ToolCall.Name]++
			}
		}
		switch e.Type {
		case agentpkg.EventRunFinished:
			completed = true
			switch e.Status {
			case agentpkg.TaskFailed:
				runErr = normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
				t.manager.MarkError(a.ID(), runErr)
			case agentpkg.TaskCanceled:
				canceled = true
				runErr = normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
				t.manager.MarkCanceled(a.ID(), runErr)
			case agentpkg.TaskIncomplete:
				runErr = normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
				t.manager.MarkIncomplete(a.ID(), runErr)
			default:
				t.manager.MarkDone(a.ID(), lastAssistantResponse(a))
			}
		case agentpkg.EventDone:
			if !completed {
				completed = true
				t.manager.MarkDone(a.ID(), lastAssistantResponse(a))
			}
		case agentpkg.EventError:
			if !completed {
				completed = true
				runErr = normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
				t.manager.MarkError(a.ID(), runErr)
			}
		}
	}
	if !completed && runCtx.Err() != nil {
		runErr = normalizeSubAgentRunError(a.ID(), runCtx, runCtx.Err())
		t.manager.MarkError(a.ID(), runErr)
	} else if !completed {
		t.manager.MarkDone(a.ID(), lastAssistantResponse(a))
	}

	response := lastAssistantResponse(a)
	result := map[string]any{
		"handle":         string(a.ID()),
		"status":         "done",
		"result":         response,
		"duration":       time.Since(started).Round(time.Millisecond).String(),
		"tool_calls":     toolCallCount,
		"tool_breakdown": toolNames,
	}
	if provider, ok := a.(ChildCompletionProvider); ok {
		if metadata := provider.CompletionMetadata(); len(metadata) > 0 {
			result["worker"] = metadata
		}
	}
	if terminator, ok := a.(ExternalChildTerminator); ok {
		terminator.TerminateExternalChild()
	}
	if runErr != nil {
		result["status"] = "error"
		result["error"] = runErr.Error()
		if response != "" {
			result["partial_result"] = response
		}
	}
	if canceled {
		result["status"] = "canceled"
		if response != "" {
			result["partial_result"] = response
		}
	}
	if externalAgent == "" {
		recordLocalChildTerminal(t.manager, a, parentRunID)
	}
	data, _ := json.Marshal(result)
	return tools.NewTextToolResult(string(data)), nil
}

func recordLocalChildTerminal(manager *AgentManager, child agentpkg.Agent, parentRunID string) {
	if manager == nil || child == nil {
		return
	}
	current, ok := manager.Status(child.ID())
	if !ok {
		return
	}
	status := current.State
	switch status {
	case "done":
		status = string(agentpkg.TaskSuccess)
	case "error", "canceled", "incomplete":
		// Already the canonical TaskStatus spelling.
	default:
		return
	}
	if err := manager.RecordLocalChildRun(child, parentRunID, status); err != nil {
		log.Printf("[agent] record local child %s history: %v", child.ID(), err)
	}
}

// resolveRouterExternalAgent applies the shared Router target contract to every
// tool that can start an external ACP worker. Local-subagent parameters are
// rejected rather than silently ignored because the profile is the only
// authority for the worker's mode, tools, worktree, and lifetime.
func resolveRouterExternalAgent(ctx context.Context, state *routerTargetState, params map[string]any) (string, error) {
	parentMode, _ := ParentModeFromContext(ctx)
	externalAgent, _ := params["agent"].(string)
	externalAgent = strings.TrimSpace(externalAgent)
	if parentMode != "router" {
		if externalAgent != "" {
			return "", fmt.Errorf("agent is only available in Router mode")
		}
		return "", nil
	}
	target, hasTarget := state.RouterTarget()
	if externalAgent == "" {
		if hasTarget {
			return "", fmt.Errorf("Router delegation requires agent=%q (the activated ACP worker); next time pass agent=%q. Local child fallback is not available", target.ID, target.ID)
		}
		return "", fmt.Errorf("Router delegation requires agent=<activated ACP worker profile>; local child fallback is not available")
	}
	// A wrong name is the most common failure: the model guesses a well-known
	// product name. Name the one accepted value instead of restating the rule.
	if hasTarget && externalAgent != target.ID {
		accepted := acceptedRouterTargets(target)
		if !containsString(accepted, externalAgent) {
			available := ""
			if len(target.Available) > 1 {
				available = fmt.Sprintf(" Configured profiles: %s.", strings.Join(target.Available, ", "))
			}
			if len(accepted) > 1 {
				return "", fmt.Errorf("agent=%q is not bound to this Router session; choose one of %s (default %q).%s",
					externalAgent, strings.Join(accepted, ", "), target.ID, available)
			}
			return "", fmt.Errorf("agent=%q is not this session's Router worker; use agent=%q.%s", externalAgent, target.ID, available)
		}
	}
	if err := rejectRouterLocalOnlyParams(params); err != nil {
		return "", err
	}
	return externalAgent, nil
}

// rejectRouterLocalOnlyParams reports local-subagent parameters that an external
// Router worker cannot honor. Silently ignoring them would let the lead believe
// a tool, worktree, or mode restriction was applied when the worker's own policy
// is the only authority.
func rejectRouterLocalOnlyParams(params map[string]any) error {
	if member, _ := params["member"].(string); strings.TrimSpace(member) != "" {
		return fmt.Errorf("member is not available for an external Router worker; use agent and the bounded task contract")
	}
	if mode, _ := params["mode"].(string); strings.TrimSpace(mode) != "" {
		return fmt.Errorf("mode is not available for an external Router worker; its mode is resolved from the ACP profile")
	}
	if workDir, _ := params["work_dir"].(string); strings.TrimSpace(workDir) != "" {
		return fmt.Errorf("work_dir is not available for an external Router worker; the Runtime resolves its working directory")
	}
	if parseWorktreeRequest(params["worktree"]) != nil {
		return fmt.Errorf("worktree is not available for an external Router worker in this release")
	}
	if len(stringSliceParam(params["tools"])) > 0 {
		return fmt.Errorf("tools is not available for an external Router worker; its tool authority is the ACP profile policy")
	}
	if value, ok := params["max_iterations"].(float64); ok && value > 0 {
		return fmt.Errorf("max_iterations is not available for an external Router worker; its lifetime is not bounded by the MothX iteration budget")
	}
	return nil
}

func stringSliceParam(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if values, ok := value.([]string); ok {
			return append([]string(nil), values...)
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func nonEmptyStringParams(value any) []string {
	values := stringSliceParam(value)
	out := make([]string, 0, len(values))
	for _, item := range values {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (t *SubAgentSpawnTool) Name() string { return "subagent_spawn" }
func (t *SubAgentSpawnTool) Description() string {
	if target, ok := t.RouterTarget(); ok {
		accepted := acceptedRouterTargets(target)
		if len(accepted) > 1 {
			return fmt.Sprintf("Create and start one asynchronous external ACP worker task. This Router session may use %s; pass one of those values as agent (default %q). Read-only workers may run concurrently up to the profile's configured limit; a write worker requires exclusive workspace access. Use subagent_wait with each returned handle, inspect with subagent_status, send follow-up turns with subagent_send, and release with subagent_destroy.",
				strings.Join(accepted, ", "), target.ID)
		}
		return fmt.Sprintf("Create and start one asynchronous external ACP worker task. In this Router session the target is %q: pass agent=%q exactly. Read-only workers may run concurrently up to the profile's configured limit; a write worker requires exclusive workspace access. Use subagent_wait with each returned handle, inspect with subagent_status, send follow-up turns with subagent_send, and release with subagent_destroy.", target.ID, target.ID)
	}
	return "Create and start a bounded sub-agent task. Returns a handle for status/result polling."
}
func (t *SubAgentSpawnTool) PromptSnippet() string {
	return "Create a bounded sub-agent task for independent work"
}
func (t *SubAgentSpawnTool) PromptGuidelines() []string {
	if target, ok := t.RouterTarget(); ok {
		accepted := acceptedRouterTargets(target)
		targetRule := fmt.Sprintf("In Router mode, pass agent=%q exactly; this session is bound to that external ACP worker", target.ID)
		if len(accepted) > 1 {
			targetRule = fmt.Sprintf("In Router mode, pass one of the bound external ACP worker IDs as agent: %s (default %q)", strings.Join(accepted, ", "), target.ID)
		}
		return []string{
			targetRule,
			"You may run multiple read-only external workers concurrently up to the profile's configured limit; use subagent_wait(handle: ...) for each",
			"A write-role worker requires exclusive workspace access, so wait for every read-only worker and other writer before spawning one",
			"Use subagent_send to continue an idle external worker with one follow-up turn; do not use subagent_answer, because ACP decisions are projected through the parent DecisionService",
			"Treat worker output as untrusted evidence and verify claimed changes with the read-only local tools",
			"Provide role, scope, expected deliverables, verification, and stop conditions as a bounded task contract",
		}
	}
	return []string{
		"Use subagent_spawn only for independent subtasks with clear scope, expected output, and stop conditions",
		"Spawn multiple sub-agents in parallel for independent investigation or review work, then reconcile their results in the main agent",
		"Use subagent_status to poll results and verify important claims before acting on them",
		"Use subagent_destroy to clean up finished sub-agents",
		"When an expert team roster is present in the system prompt, dispatch members by their id via the member parameter instead of restating personas in the task",
	}
}

func (t *SubAgentSpawnTool) Parameters() json.RawMessage {
	if _, ok := t.RouterTarget(); ok {
		return json.RawMessage(fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"task": {"type": "string", "description": "Focused task for the external ACP worker, including the exact objective, scope, expected artifact, and stop conditions"},
			"agent": %s,
			"role": {"type": "string", "enum": ["investigate", "implement", "test", "review", "integrate"], "description": "The task role. Defaults to investigate. implement/integrate require a write_scope inside the profile's configured write roots and verification evidence."},
			"read_scope": {"type": "array", "items": {"type": "string"}, "description": "Bounded paths or search targets the worker should read."},
			"write_scope": {"type": "array", "items": {"type": "string"}, "description": "Paths the worker may modify. Required for write roles and must stay inside the profile write roots."},
			"expected_deliverables": {"type": "array", "items": {"type": "string", "enum": ["findings", "changed-files", "test-evidence", "review"]}, "description": "Evidence the worker must return. Defaults are role-specific."},
			"verification": {"type": "string", "description": "The check or evidence the worker must report. Required for implement/integrate/test roles."},
			"stop_conditions": {"type": "array", "items": {"type": "string"}, "description": "Explicit completion conditions and non-goals."},
			"system_prompt_extra": {"type": "string", "description": "Additional bounded context or constraints for the worker."},
			"attachments": {"type": "array", "items": {"type": "string"}, "description": "Runtime input resource IDs from the request manifest. The Runtime resolves, verifies, and capability-negotiates their delivery."}
		},
		"required": ["task", "agent"]
	}`, t.agentParameterSchema()))
	}
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task": {"type": "string", "description": "Focused task for the sub-agent, including scope, relevant paths/context, expected artifact, and stop conditions"},
			"member": {"type": "string", "description": "Member definition id from the bound expert team roster (see system prompt roster). Resolves the member persona and capability overrides."},
			"mode": {"type": "string", "enum": ["plan", "agent", "yolo", "os"], "description": "Sub-agent execution mode. Defaults to the parent agent's mode; if unavailable, falls back to 'yolo'."},
			"work_dir": {"type": "string", "description": "Working directory for the sub-agent (defaults to the parent workspace)"},
			"worktree": {"type": "string", "description": "Run this sub-agent in its own isolated git worktree. Pass \"true\" to create one, or a worktree name. Defaults to inheriting the parent workspace."},
			"tools": {"type": "array", "items": {"type": "string"}, "description": "Allowed tools (empty = all)"},
			"max_iterations": {"type": "integer", "default": 100, "description": "Maximum iterations"},
			"system_prompt_extra": {"type": "string", "description": "Extra context for the sub-agent"},
			"attachments": {"type": "array", "items": {"type": "string"}, "description": "Router only: Runtime input resource IDs from the request manifest."}
		},
		"required": ["task"]
	}`)
}

func (t *SubAgentSpawnTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	if t.manager == nil {
		return tools.ToolResult{}, fmt.Errorf("agent manager is not initialized")
	}
	externalAgent, err := resolveRouterExternalAgent(ctx, &t.routerTargetState, params)
	if err != nil {
		return tools.ToolResult{}, err
	}
	attachmentIDs := nonEmptyStringParams(params["attachments"])
	if externalAgent == "" && len(attachmentIDs) > 0 {
		return tools.ToolResult{}, fmt.Errorf("attachments are only available for an external Router worker")
	}

	task, _ := params["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return tools.ToolResult{}, fmt.Errorf("task is required")
	}

	// Resolve the optional expert-team member. The roster registry is
	// installed by the runtime assembly layer; without a binding the member
	// parameter is a tool error rather than a silent fallback.
	memberID, _ := params["member"].(string)
	memberID = strings.TrimSpace(memberID)
	var memberDef *MemberDef
	if externalAgent == "" && memberID != "" {
		if t.manager == nil || t.manager.Members == nil {
			return tools.ToolResult{}, fmt.Errorf("no expert team is bound to this session")
		}
		def, ok := t.manager.Members.Get(memberID)
		if !ok {
			return tools.ToolResult{}, fmt.Errorf("unknown member %q; known members: %v", memberID, t.manager.Members.IDs())
		}
		memberDef = def
	}

	// A named member's declaration is a capability ceiling, not a convenient
	// default a lead can replace. Resolve the requested mode against both the
	// parent/session mode and the member declaration before creating the child.
	// In particular, a plan parent must never obtain a yolo child through a
	// member declaration or an explicit tool argument.
	parentMode, _ := ParentModeFromContext(ctx)
	requestedMode, _ := params["mode"].(string)
	mode := requestedMode
	if memberDef != nil {
		var err error
		mode, err = resolveMemberMode(parentMode, requestedMode, memberDef)
		if err != nil {
			return tools.ToolResult{}, err
		}
	} else if mode == "" {
		// Preserve the pre-existing generic-subagent inheritance behavior.
		mode = parentMode
	}

	workDir, _ := params["work_dir"].(string)
	if strings.TrimSpace(workDir) == "" {
		if parent, ok := WorkDirFromContext(ctx); ok {
			workDir = parent
		}
	}
	if memberDef != nil && memberDef.WorkDir != "" {
		if workDir != "" && workDir != memberDef.WorkDir {
			return tools.ToolResult{}, fmt.Errorf("member %q work_dir is fixed to %q", memberID, memberDef.WorkDir)
		}
		workDir = memberDef.WorkDir
	}

	worktreeReq := parseWorktreeRequest(params["worktree"])
	if memberDef != nil && memberDef.Worktree {
		if memberDef.WorkDir != "" {
			return tools.ToolResult{}, fmt.Errorf("member %q declares both work_dir and worktree", memberID)
		}
		if worktreeReq == nil {
			worktreeReq = &WorktreeSpec{}
		}
	}

	maxIter := 0
	maxIterSet := false
	if v, ok := params["max_iterations"].(float64); ok && v > 0 {
		maxIter = int(v)
		maxIterSet = true
	}
	if memberDef != nil && memberDef.MaxIterations > 0 {
		if !maxIterSet || maxIter > memberDef.MaxIterations {
			maxIter = memberDef.MaxIterations
		}
		maxIterSet = true
	}
	if !maxIterSet {
		maxIter = 100
	}

	extra, _ := params["system_prompt_extra"].(string)
	if memberDef != nil && memberDef.Prompt != "" {
		if extra != "" {
			extra = memberDef.Prompt + "\n\n" + extra
		} else {
			extra = memberDef.Prompt
		}
	}

	var toolFilter []string
	if ts, ok := params["tools"].([]any); ok {
		for _, tt := range ts {
			if s, ok := tt.(string); ok {
				toolFilter = append(toolFilter, s)
			}
		}
	}
	if memberDef != nil && len(memberDef.Tools) > 0 {
		toolFilter = restrictMemberTools(toolFilter, memberDef.Tools)
	}

	memberDisplayName, memberEmoji, memberRole := "", "", ""
	if memberDef != nil {
		memberDisplayName = memberDef.DisplayName
		memberEmoji = memberDef.Emoji
		memberRole = memberDef.Role
	}

	// Extract parent agent ID from context (injected by executeTool)
	parentID, _ := AgentIDFromContext(ctx)
	parentRunID, _ := ParentRunIDFromContext(ctx)

	// Extract parent's event channel from context (injected by executeTool)
	parentEventCh, _ := EventChanFromContext(ctx)

	// Apply the bounded local-subagent timeout. An external Router worker is
	// long-running by design and is stopped only by cancellation, its Runtime
	// policy boundary, or a terminal event.
	policy := DefaultSubAgentPolicy()
	parentRunCtx, ok := ParentRunContextFromContext(ctx)
	if !ok || parentRunCtx == nil {
		parentRunCtx = context.Background()
	}
	runCtx := parentRunCtx
	cancel := func() {}
	if externalAgent == "" {
		runCtx, cancel = context.WithTimeout(parentRunCtx, policy.TimeoutPerAgent)
	} else {
		// An asynchronous external worker outlives the lead turn that spawned
		// it. The lead's run context is cancelled when that run reaches its
		// terminal state, so deriving the worker from it would cancel every
		// spawned worker the moment the lead finished its own turn. The worker
		// is still cancellable: the manager holds this cancel function, so
		// subagent_destroy and the shared Runtime shutdown reach it.
		runCtx, cancel = context.WithCancel(context.WithoutCancel(parentRunCtx))
	}

	options := AgentOptions{
		ParentID:          parentID,
		MemberID:          memberID,
		ExpertID:          t.manager.ExpertID,
		MemberDisplayName: memberDisplayName,
		MemberEmoji:       memberEmoji,
		MemberRole:        memberRole,
		Mode:              mode,
		WorkDir:           workDir,
		Worktree:          worktreeReq,
		Tools:             toolFilter,
		SystemPromptExtra: extra,
		MaxIterations:     maxIter,
	}
	if externalAgent != "" {
		role, _ := params["role"].(string)
		verification, _ := params["verification"].(string)
		options.ExternalAgent = externalAgent
		options.ExternalTask = task
		options.ExternalRole = strings.TrimSpace(role)
		options.ExternalVerification = verification
		options.ExternalReadScope = stringSliceParam(params["read_scope"])
		options.ExternalWriteScope = stringSliceParam(params["write_scope"])
		options.ExternalDeliverable = stringSliceParam(params["expected_deliverables"])
		options.ExternalStopConditions = stringSliceParam(params["stop_conditions"])
		options.ExternalAttachmentIDs = attachmentIDs
		options.RuntimeContext = ctx
	}
	a, err := t.manager.Create(options)
	if err != nil {
		cancel()
		return tools.ToolResult{}, fmt.Errorf("create sub-agent: %w", err)
	}
	t.manager.MarkRunning(a.ID())
	t.manager.SetCancel(a.ID(), cancel)

	// Start the sub-agent asynchronously, forward events to parent
	go func() {
		defer func() {
			cancel()
			t.manager.SetCancel(a.ID(), nil)
		}()
		// Terminal completions of spawned sub-agents are queued into the
		// session mailbox (when one is bound) so the lead receives them at
		// the next iteration boundary without polling. The blocking
		// delegate path returns results synchronously and never enqueues.
		notifierMemberID := memberID
		notifierDisplayName := memberDisplayName
		if externalAgent != "" {
			// An external worker has no expert-team member identity. Use the
			// canonical child handle so the mailbox item names the worker the lead
			// must follow up with; an empty member ID made the completion
			// unactionable.
			notifierMemberID = string(a.ID())
			if notifierDisplayName == "" {
				notifierDisplayName = "ACP worker " + externalAgent
			}
		}
		notifier := &memberNotifier{
			mailbox:     t.manager.Mailbox,
			memberID:    notifierMemberID,
			displayName: notifierDisplayName,
			external:    externalAgent != "",
		}
		eventMeta := ChildEventMeta{
			MemberID:          memberID,
			ExpertID:          t.manager.ExpertID,
			MemberDisplayName: memberDisplayName,
			MemberEmoji:       memberEmoji,
			MemberRole:        memberRole,
		}
		ch := a.Run(runCtx, buildSubAgentTask(task))
		for e := range ch {
			// Forward approval events to parent so the UI can handle them
			if e.Type == agentpkg.EventToolApprovalRequest && parentEventCh != nil {
				_ = sendParentEvent(runCtx, parentEventCh, Event{
					Type:              EventToolApprovalRequest,
					AgentID:           a.ID(),
					ApprovalID:        e.ApprovalID,
					ApprovalTool:      e.ApprovalTool,
					ApprovalArgs:      e.ApprovalArgs,
					MemberID:          memberID,
					ExpertID:          t.manager.ExpertID,
					MemberDisplayName: memberDisplayName,
					MemberEmoji:       memberEmoji,
					MemberRole:        memberRole,
				})
			}
			ForwardChildAgentEvent(runCtx, parentEventCh, a.ID(), e, eventMeta)
			// Members ask the lead, not the human: queue the question so a blocked
			// subagent_wait returns and the lead can answer with subagent_answer.
			if e.Type == agentpkg.EventQuestionRequest {
				forwardMemberQuestion(runCtx, t.manager, parentEventCh, a.ID(), e, &eventMeta)
			}
			switch e.Type {
			case agentpkg.EventRunFinished:
				switch e.Status {
				case agentpkg.TaskFailed:
					runErr := normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
					t.manager.MarkError(a.ID(), runErr)
					notifier.notify(MemberStatusError, memberTerminalPayload(runErr, a))
				case agentpkg.TaskIncomplete:
					runErr := normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
					t.manager.MarkIncomplete(a.ID(), runErr)
					notifier.notify(MemberStatusIncomplete, memberTerminalPayload(runErr, a))
				case agentpkg.TaskCanceled:
					runErr := normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
					t.manager.MarkCanceled(a.ID(), runErr)
					notifier.notify(MemberStatusCanceled, memberTerminalPayload(runErr, a))
				default:
					response := lastAssistantResponse(a)
					t.manager.MarkDone(a.ID(), response)
					notifier.notify(MemberStatusDone, response)
				}
			case agentpkg.EventDone:
				response := lastAssistantResponse(a)
				t.manager.MarkDone(a.ID(), response)
				notifier.notify(MemberStatusDone, response)
			case agentpkg.EventError:
				runErr := normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
				t.manager.MarkError(a.ID(), runErr)
				notifier.notify(MemberStatusError, memberTerminalPayload(runErr, a))
			}
		}
		if runCtx.Err() != nil {
			if st, ok := t.manager.Status(a.ID()); !ok || !isTerminalManagedState(st.State) {
				runErr := normalizeSubAgentRunError(a.ID(), runCtx, runCtx.Err())
				t.manager.MarkError(a.ID(), runErr)
				notifier.notify(MemberStatusError, memberTerminalPayload(runErr, a))
			}
		}
		if externalAgent == "" {
			recordLocalChildTerminal(t.manager, a, parentRunID)
		}
	}()

	result := map[string]any{
		"handle": string(a.ID()),
		"status": "running",
	}
	if externalAgent != "" {
		result["agent"] = externalAgent
		result["backend"] = "acp"
	} else {
		result["timeout"] = policy.TimeoutPerAgent.String()
	}
	data, _ := json.Marshal(result)
	return tools.NewTextToolResult(string(data)), nil
}

// resolveMemberMode intersects a named member's requested mode with the
// parent mode and its declared capability. The modes are deliberately not a
// simple numeric hierarchy: OS mode exposes only bash but grants yolo-style
// execution, so it must not be treated as a harmless restriction of agent or
// yolo. The table therefore permits only monotonic, well-defined reductions.
func resolveMemberMode(parentMode, requestedMode string, member *MemberDef) (string, error) {
	parentMode = strings.TrimSpace(parentMode)
	if parentMode == "" {
		parentMode = "yolo"
	}
	mode := parentMode
	if member != nil && strings.TrimSpace(member.Mode) != "" {
		declared := strings.TrimSpace(member.Mode)
		if modeWithinCapability(declared, mode) {
			mode = declared
		}
	}
	requestedMode = strings.TrimSpace(requestedMode)
	if requestedMode == "" {
		return mode, nil
	}
	if !modeWithinCapability(requestedMode, mode) {
		return "", fmt.Errorf("requested mode %q exceeds member/session capability %q", requestedMode, mode)
	}
	return requestedMode, nil
}

// modeWithinCapability reports whether candidate can be run without granting
// more capability than cap. Keeping OS separate is intentional: bash-only OS
// mode can still execute arbitrary commands outside the sandbox.
func modeWithinCapability(candidate, cap string) bool {
	switch cap {
	case "plan":
		return candidate == "plan"
	case "agent":
		return candidate == "agent" || candidate == "plan"
	case "yolo":
		return candidate == "yolo" || candidate == "agent" || candidate == "plan"
	case "os":
		return candidate == "os" || candidate == "plan"
	default:
		return false
	}
}

// restrictMemberTools returns the requested subset of a member's declared
// tools. An empty request retains the declaration; unknown or broader tool
// names are simply excluded so an LLM cannot turn a restrictive persona into
// an all-tools child by supplying a different list.
func restrictMemberTools(requested, allowed []string) []string {
	if len(allowed) == 0 {
		return requested
	}
	if len(requested) == 0 {
		return append([]string(nil), allowed...)
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	result := make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, name := range requested {
		if _, ok := allowedSet[name]; !ok {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

// forwardMemberQuestion routes a member's question to the lead instead of the
// human. It projects the request on the parent stream (so adapters can render
// "member asks the lead") and queues it in the session mailbox, which is what
// actually wakes the lead. The member keeps waiting for subagent_answer.
func forwardMemberQuestion(ctx context.Context, manager *AgentManager, parentEventCh chan<- Event, childID agentpkg.AgentID, e agentpkg.Event, meta *ChildEventMeta) {
	if parentEventCh != nil {
		ev := Event{
			Type:            EventQuestionRequest,
			AgentID:         childID,
			QuestionID:      e.QuestionID,
			QuestionText:    e.QuestionText,
			QuestionOptions: append([]string(nil), e.QuestionOptions...),
			QuestionContext: e.QuestionContext,
		}
		if meta != nil {
			ev.MemberID = meta.MemberID
			ev.ExpertID = meta.ExpertID
			ev.MemberDisplayName = meta.MemberDisplayName
			ev.MemberEmoji = meta.MemberEmoji
			ev.MemberRole = meta.MemberRole
		}
		_ = sendParentEvent(ctx, parentEventCh, ev)
	}
	if manager == nil {
		return
	}
	if manager.Mailbox == nil {
		// No expert team is bound, so the manager has no lead mailbox: the question
		// is only projected on the parent stream and nobody can answer it. Report
		// the miss instead of letting the member block silently until its own
		// timeout. Binding a team (or installing the session mailbox) is the fix.
		log.Printf("[agent] member %s asked question %s but the session has no lead mailbox; the member cannot be answered", childID, e.QuestionID)
	}
	displayName := ""
	if meta != nil {
		displayName = meta.MemberDisplayName
	}
	manager.NotifyMemberQuestion(string(childID), displayName, e.QuestionID, e.QuestionText, e.QuestionOptions)
}

func sendParentEvent(ctx context.Context, ch chan<- Event, ev Event) (ok bool) {
	if sink, ok := eventSinkFromContext(ctx); ok {
		return sink.send(ctx, ev)
	}
	// Fallback (no sink in context): the recover catches the data-race
	// send-on-closed panic. Callers should not rely on this path for
	// correctness of terminal child events.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[agent] sendParentEvent recovered from panic: %v (event type=%d)", r, ev.Type)
			ok = false
		}
	}()
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// ChildEventMeta carries optional expert-team metadata for forwarded child
// events. The zero value adds no metadata, keeping existing call sites
// behavior-identical.
type ChildEventMeta struct {
	MemberID          string
	ExpertID          string
	MemberDisplayName string
	MemberEmoji       string
	MemberRole        string
}

// ForwardChildAgentEvent forwards child-agent activity to the parent event
// stream so frontends can render background progress without mixing child
// output into the main transcript. The optional meta attaches expert-team
// member/expert identity to the projected event.
func ForwardChildAgentEvent(ctx context.Context, ch chan<- Event, childID agentpkg.AgentID, e agentpkg.Event, meta ...ChildEventMeta) bool {
	if ch == nil {
		return false
	}
	ev := Event{
		Type:          EventType(e.Type),
		AgentID:       childID,
		TextDelta:     e.TextDelta,
		ThinkDelta:    e.ThinkDelta,
		ToolCallID:    e.ToolCallID,
		ToolName:      e.ToolName,
		ToolArgs:      e.ToolArgs,
		ToolResult:    e.ToolResult,
		ToolError:     e.ToolError,
		StatusMessage: e.StatusMessage,
		Done:          e.Done,
		StopReason:    e.StopReason,
		Error:         e.Error,
		Status:        TaskStatus(e.Status),
		// Plan/usage/approval projections are additive: existing consumers that
		// ignore them keep their behavior, while external-child streams keep the
		// canonical metadata the Runtime produced.
		Plan:           taskPlanFromPublic(e.Plan),
		Usage:          usageFromPublic(e.Usage),
		ContextUsage:   contextUsageFromPublic(e.ContextUsage),
		ApprovalID:     e.ApprovalID,
		ApprovalTool:   e.ApprovalTool,
		ApprovalArgs:   e.ApprovalArgs,
		ApprovalResult: e.ApprovalResult,
	}
	if len(meta) > 0 {
		ev.MemberID = meta[0].MemberID
		ev.ExpertID = meta[0].ExpertID
		ev.MemberDisplayName = meta[0].MemberDisplayName
		ev.MemberEmoji = meta[0].MemberEmoji
		ev.MemberRole = meta[0].MemberRole
	}
	// Tool result images ride along so adapters projecting child tool events
	// on the parent stream keep their image content blocks.
	for _, image := range e.ToolImages {
		ev.ToolImages = append(ev.ToolImages, ToolImage{MimeType: image.MimeType, Data: image.Data})
	}
	if ev.ToolName == "" && e.ToolCall != nil {
		ev.ToolName = e.ToolCall.Name
	}
	switch e.Type {
	case agentpkg.EventTextDelta,
		agentpkg.EventThinkDelta,
		agentpkg.EventToolCall,
		agentpkg.EventToolExecutionStart,
		agentpkg.EventToolExecutionEnd,
		agentpkg.EventToolResult,
		agentpkg.EventPlanUpdate,
		agentpkg.EventUsage,
		agentpkg.EventToolApprovalResponse,
		agentpkg.EventStatus,
		agentpkg.EventDone,
		agentpkg.EventError,
		agentpkg.EventRunFinished:
		return sendParentEvent(ctx, ch, ev)
	default:
		return false
	}
}

func taskPlanFromPublic(plan *agentpkg.TaskPlan) *tools.TaskPlan {
	if plan == nil {
		return nil
	}
	steps := make([]tools.PlanStep, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		steps = append(steps, tools.PlanStep{Title: step.Title, Status: step.Status})
	}
	return &tools.TaskPlan{Title: plan.Title, Steps: steps, Note: plan.Note}
}

func usageFromPublic(usage *agentpkg.Usage) *provider.Usage {
	if usage == nil {
		return nil
	}
	return &provider.Usage{
		Input: usage.InputTokens, Output: usage.OutputTokens,
		CacheRead: usage.CacheRead, CacheWrite: usage.CacheWrite, TotalTokens: usage.TotalTokens,
	}
}

func contextUsageFromPublic(usage *agentpkg.ContextUsage) *ctxpkg.ContextUsage {
	if usage == nil {
		return nil
	}
	return &ctxpkg.ContextUsage{
		TotalTokens: usage.TotalTokens, Input: usage.Input,
		CacheRead: usage.CacheRead, CacheWrite: usage.CacheWrite,
		ContextWindow: usage.ContextWindow, Percent: usage.Percent,
	}
}

// SubAgentStatusTool queries sub-agent status and results.
type SubAgentStatusTool struct {
	manager *AgentManager
}

func NewSubAgentStatusTool(m *AgentManager) *SubAgentStatusTool {
	return &SubAgentStatusTool{manager: m}
}

func (t *SubAgentStatusTool) Name() string { return "subagent_status" }
func (t *SubAgentStatusTool) Description() string {
	return "Query the status and results of a sub-agent."
}
func (t *SubAgentStatusTool) PromptSnippet() string      { return "Check sub-agent status and get results" }
func (t *SubAgentStatusTool) PromptGuidelines() []string { return nil }

func (t *SubAgentStatusTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"handle": {"type": "string", "description": "The sub-agent handle ID"}
		},
		"required": ["handle"]
	}`)
}

func (t *SubAgentStatusTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	handle, _ := params["handle"].(string)
	if handle == "" {
		return tools.ToolResult{}, fmt.Errorf("handle is required")
	}

	st, statusOK := t.manager.Status(agentpkg.AgentID(handle))
	a, agentOK := t.manager.Get(agentpkg.AgentID(handle))
	if !statusOK && !agentOK {
		return tools.ToolResult{}, fmt.Errorf("sub-agent %q not found", handle)
	}

	status := st.State
	if status == "" {
		status = "unknown"
	}
	lastResponse := st.Result
	messageCount := 0
	if agentOK {
		messages := a.GetMessages()
		messageCount = len(messages)
	}
	if lastResponse == "" && agentOK {
		lastResponse = lastAssistantResponse(a)
	}

	result := map[string]any{
		"handle":        handle,
		"status":        status,
		"message_count": messageCount,
	}
	if lastResponse != "" {
		result["last_response"] = lastResponse
	}
	if len(st.Metadata) > 0 {
		result["worker"] = st.Metadata
	}
	if st.Error != "" {
		result["error"] = st.Error
	}
	if !st.UpdatedAt.IsZero() {
		result["updated_at"] = st.UpdatedAt.Format(time.RFC3339)
	}

	data, _ := json.Marshal(result)
	return tools.NewTextToolResult(string(data)), nil
}

// SubAgentSendTool sends a follow-up message to a running sub-agent.
type SubAgentSendTool struct {
	manager *AgentManager
}

func NewSubAgentSendTool(m *AgentManager) *SubAgentSendTool {
	return &SubAgentSendTool{manager: m}
}

func (t *SubAgentSendTool) Name() string { return "subagent_send" }
func (t *SubAgentSendTool) Description() string {
	return "Send a follow-up message to a sub-agent. An external Router worker must be idle (not running); wait for its terminal status first."
}
func (t *SubAgentSendTool) PromptSnippet() string {
	return "Send follow-up instructions to a sub-agent"
}
func (t *SubAgentSendTool) PromptGuidelines() []string { return nil }

func (t *SubAgentSendTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"handle": {"type": "string", "description": "The sub-agent handle ID"},
			"message": {"type": "string", "description": "The follow-up message"}
		},
		"required": ["handle", "message"]
	}`)
}

func (t *SubAgentSendTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	handle, _ := params["handle"].(string)
	message, _ := params["message"].(string)
	if handle == "" || message == "" {
		return tools.ToolResult{}, fmt.Errorf("handle and message are required")
	}

	a, ok := t.manager.Get(agentpkg.AgentID(handle))
	if !ok {
		return tools.ToolResult{}, fmt.Errorf("sub-agent %q not found", handle)
	}
	// An external ACP session runs one prompt at a time. Rejecting a concurrent
	// follow-up here keeps the original run's status and terminal event intact;
	// otherwise the proxy would fail the second Run and mark a still-live child
	// as errored.
	if longRunningFollowUp(a) {
		if status, ok := t.manager.Status(a.ID()); ok && !isTerminalManagedState(status.State) {
			return tools.ToolResult{}, fmt.Errorf("sub-agent %q is still running; wait for its terminal status before sending a follow-up", handle)
		}
	}

	parentRunCtx, ok := ParentRunContextFromContext(ctx)
	if !ok || parentRunCtx == nil {
		parentRunCtx = context.Background()
	}
	runCtx := parentRunCtx
	cancel := func() {}
	if !longRunningFollowUp(a) {
		runCtx, cancel = context.WithTimeout(parentRunCtx, DefaultSubAgentPolicy().TimeoutPerAgent)
	}
	t.manager.BeginFollowUp(a.ID())
	t.manager.SetCancel(a.ID(), cancel)

	// Extract parent's event channel for approval forwarding
	parentEventCh, _ := EventChanFromContext(ctx)
	status, _ := t.manager.Status(a.ID())
	external := longRunningFollowUp(a)
	notifierMemberID := string(a.ID())
	notifierDisplayName := status.MemberDisplayName
	if external {
		if profileID, _ := status.Metadata["profileId"].(string); profileID != "" {
			notifierDisplayName = "ACP worker " + profileID
		}
	} else if notifierDisplayName == "" {
		notifierMemberID = status.MemberID
		if notifierMemberID == "" {
			notifierMemberID = string(a.ID())
		}
	}
	notifier := &memberNotifier{
		mailbox:     t.manager.Mailbox,
		memberID:    notifierMemberID,
		displayName: notifierDisplayName,
		external:    external,
	}

	go func() {
		defer func() {
			cancel()
			t.manager.SetCancel(a.ID(), nil)
		}()
		ch := a.Run(runCtx, message)
		for e := range ch {
			// Forward approval events to parent
			if e.Type == agentpkg.EventToolApprovalRequest && parentEventCh != nil {
				_ = sendParentEvent(runCtx, parentEventCh, Event{
					Type:         EventToolApprovalRequest,
					AgentID:      a.ID(),
					ApprovalID:   e.ApprovalID,
					ApprovalTool: e.ApprovalTool,
					ApprovalArgs: e.ApprovalArgs,
				})
			}
			ForwardChildAgentEvent(runCtx, parentEventCh, a.ID(), e)
			if e.Type == agentpkg.EventQuestionRequest {
				forwardMemberQuestion(runCtx, t.manager, parentEventCh, a.ID(), e, nil)
			}
			switch e.Type {
			case agentpkg.EventRunFinished:
				switch e.Status {
				case agentpkg.TaskFailed:
					runErr := normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
					t.manager.MarkError(a.ID(), runErr)
					notifier.notify(MemberStatusError, memberTerminalPayload(runErr, a))
				case agentpkg.TaskIncomplete:
					runErr := normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
					t.manager.MarkIncomplete(a.ID(), runErr)
					notifier.notify(MemberStatusIncomplete, memberTerminalPayload(runErr, a))
				case agentpkg.TaskCanceled:
					runErr := normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
					t.manager.MarkCanceled(a.ID(), runErr)
					notifier.notify(MemberStatusCanceled, memberTerminalPayload(runErr, a))
				default:
					response := lastAssistantResponse(a)
					t.manager.MarkDone(a.ID(), response)
					notifier.notify(MemberStatusDone, response)
				}
			case agentpkg.EventDone:
				if !isTerminalManagedStatus(t.manager, a.ID()) {
					response := lastAssistantResponse(a)
					t.manager.MarkDone(a.ID(), response)
					notifier.notify(MemberStatusDone, response)
				}
			case agentpkg.EventError:
				if !isTerminalManagedStatus(t.manager, a.ID()) {
					runErr := normalizeSubAgentRunError(a.ID(), runCtx, e.Error)
					t.manager.MarkError(a.ID(), runErr)
					notifier.notify(MemberStatusError, memberTerminalPayload(runErr, a))
				}
			}
		}
		if runCtx.Err() != nil {
			if !isTerminalManagedStatus(t.manager, a.ID()) {
				runErr := normalizeSubAgentRunError(a.ID(), runCtx, runCtx.Err())
				t.manager.MarkError(a.ID(), runErr)
				notifier.notify(MemberStatusError, memberTerminalPayload(runErr, a))
			}
		}
	}()

	return tools.NewTextToolResult(fmt.Sprintf(`{"handle":%q,"status":"message_sent"}`, handle)), nil
}

func longRunningFollowUp(a agentpkg.Agent) bool {
	marker, ok := a.(interface{ LongRunningFollowUp() bool })
	return ok && marker.LongRunningFollowUp()
}

func isTerminalManagedStatus(manager *AgentManager, id agentpkg.AgentID) bool {
	if manager == nil {
		return false
	}
	status, ok := manager.Status(id)
	return ok && isTerminalManagedState(status.State)
}

func normalizeSubAgentRunError(id agentpkg.AgentID, runCtx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("sub-agent %s timed out after 30 minutes; the parent agent will continue. Check subagent_status for partial results", id)
	}
	if errors.Is(runCtx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("sub-agent %s stopped before completion; the parent agent will continue", id)
	}
	return err
}

func buildSubAgentTask(task string) string {
	task = strings.TrimSpace(task)
	return fmt.Sprintf(`Delegated task:
%s

Execute this task precisely. When done, structure your final response using this format:

Result: <the direct answer or completed change>
Evidence: <files inspected, commands run, test outputs — summarized>
Changes: <files modified with brief description, or "None">
Risks: <assumptions, uncertainty, follow-up needed, or "None">
`, task)
}

func lastAssistantResponse(a agentpkg.Agent) string {
	messages := a.GetMessages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == agentpkg.RoleAssistant {
			if messages[i].Content != "" {
				return messages[i].Content
			}
			var sb strings.Builder
			for _, block := range messages[i].Contents {
				if block.Type == "text" && block.Text != "" {
					sb.WriteString(block.Text)
				}
			}
			return sb.String()
		}
	}
	return ""
}

// memberNotifier enqueues at most one terminal MemberCompletion per spawned
// sub-agent run into the session mailbox. It lives entirely inside the spawn
// monitoring goroutine, so the once flag needs no synchronization. A nil
// mailbox (no expert team bound) makes notify a no-op.
type memberNotifier struct {
	mailbox     *MemberMailbox
	memberID    string
	displayName string
	external    bool
	notified    bool
}

func (n *memberNotifier) notify(status, payload string) {
	if n == nil || n.mailbox == nil || n.notified {
		return
	}
	n.notified = true
	n.mailbox.Enqueue(MemberCompletion{
		MemberID:    n.memberID,
		DisplayName: n.displayName,
		Status:      status,
		Payload:     payload,
		External:    n.external,
	})
}

// SubAgentAnswerTool answers a blocking question a running member asked the
// lead. The question is delivered as a [MEMBER_QUESTION] steering message (or a
// subagent_wait pending entry with status "question"); the member stays blocked
// until this tool resolves it.
type SubAgentAnswerTool struct {
	manager *AgentManager
}

func NewSubAgentAnswerTool(m *AgentManager) *SubAgentAnswerTool {
	return &SubAgentAnswerTool{manager: m}
}

func (t *SubAgentAnswerTool) Name() string { return "subagent_answer" }
func (t *SubAgentAnswerTool) Description() string {
	return "Answer a question a running sub-agent asked you (the lead). Use the member handle and question_id from the [MEMBER_QUESTION] message; the member unblocks and continues its task."
}
func (t *SubAgentAnswerTool) PromptSnippet() string {
	return "Answer a member's blocking question so it can continue"
}
func (t *SubAgentAnswerTool) PromptGuidelines() []string {
	return []string{
		"When a [MEMBER_QUESTION] message or a subagent_wait entry with status \"question\" appears, answer it with subagent_answer instead of ignoring it",
		"Pass the exact question_id from the message; use the member handle as the target",
	}
}

func (t *SubAgentAnswerTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"handle": {"type": "string", "description": "The sub-agent handle ID that asked the question"},
			"question_id": {"type": "string", "description": "The question_id from the [MEMBER_QUESTION] message"},
			"answer": {"type": "string", "description": "The answer for the member to continue with"}
		},
		"required": ["handle", "question_id", "answer"]
	}`)
}

func (t *SubAgentAnswerTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	handle, _ := params["handle"].(string)
	questionID, _ := params["question_id"].(string)
	answer, _ := params["answer"].(string)
	if strings.TrimSpace(handle) == "" || strings.TrimSpace(questionID) == "" || strings.TrimSpace(answer) == "" {
		return tools.ToolResult{}, fmt.Errorf("handle, question_id and answer are required")
	}
	if t.manager == nil {
		return tools.ToolResult{}, fmt.Errorf("sub-agent manager is not available")
	}
	target, ok := t.manager.Get(agentpkg.AgentID(handle))
	if !ok {
		return tools.ToolResult{}, fmt.Errorf("sub-agent %q not found", handle)
	}
	handler, ok := target.(agentpkg.QuestionHandler)
	if !ok {
		return tools.ToolResult{}, fmt.Errorf("sub-agent %q does not accept answers", handle)
	}
	// HandleQuestionResponse ignores unknown IDs, so report the miss instead of a
	// false success: a lead that believes a blocked member was unblocked may end
	// its run while the member keeps waiting for an answer that never arrives.
	if deliverer, ok := target.(interface{ DeliverQuestionAnswer(string, string) bool }); ok {
		if !deliverer.DeliverQuestionAnswer(questionID, answer) {
			return tools.ToolResult{}, fmt.Errorf("question %s is not pending on %s: it was already answered, expired, or never belonged to that member", questionID, handle)
		}
		return tools.NewTextToolResult(fmt.Sprintf("Answered %s's question %s.", handle, questionID)), nil
	}
	handler.HandleQuestionResponse(questionID, answer)
	return tools.NewTextToolResult(fmt.Sprintf("Answered %s's question %s.", handle, questionID)), nil
}

// memberTerminalPayload returns the error text for a failed/canceled/incomplete
// run, falling back to the partial assistant response when the run produced no
// error text.
func memberTerminalPayload(runErr error, a agentpkg.Agent) string {
	if runErr != nil {
		return runErr.Error()
	}
	return lastAssistantResponse(a)
}

// SubAgentDestroyTool destroys a sub-agent and releases resources.
type SubAgentDestroyTool struct {
	manager *AgentManager
}

func NewSubAgentDestroyTool(m *AgentManager) *SubAgentDestroyTool {
	return &SubAgentDestroyTool{manager: m}
}

func (t *SubAgentDestroyTool) Name() string { return "subagent_destroy" }
func (t *SubAgentDestroyTool) Description() string {
	return "Destroy a sub-agent and release resources."
}
func (t *SubAgentDestroyTool) PromptSnippet() string      { return "Destroy a finished sub-agent" }
func (t *SubAgentDestroyTool) PromptGuidelines() []string { return nil }

func (t *SubAgentDestroyTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"handle": {"type": "string", "description": "The sub-agent handle ID"}
		},
		"required": ["handle"]
	}`)
}

func (t *SubAgentDestroyTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	handle, _ := params["handle"].(string)
	if handle == "" {
		return tools.ToolResult{}, fmt.Errorf("handle is required")
	}

	if err := t.manager.Destroy(agentpkg.AgentID(handle)); err != nil {
		return tools.ToolResult{}, fmt.Errorf("destroy sub-agent: %w", err)
	}

	return tools.NewTextToolResult(fmt.Sprintf(`{"handle":%q,"status":"destroyed"}`, handle)), nil
}

// SubAgentPolicy defines security constraints for sub-agents.
type SubAgentPolicy struct {
	MaxChildren     int           // Maximum number of sub-agents (default 5)
	AllowedModes    []string      // Allowed modes for sub-agents (default ["plan", "agent", "yolo", "os"])
	InheritSandbox  bool          // Inherit parent's sandbox (default true)
	TimeoutPerAgent time.Duration // Per-agent timeout (default 30min)
	TotalTimeout    time.Duration // Total timeout for all sub-agents (default 30min)
}

// DefaultSubAgentPolicy returns the default policy.
func DefaultSubAgentPolicy() SubAgentPolicy {
	return SubAgentPolicy{
		MaxChildren:     5,
		AllowedModes:    []string{"plan", "agent", "yolo", "os"},
		InheritSandbox:  true,
		TimeoutPerAgent: 30 * time.Minute,
		TotalTimeout:    30 * time.Minute,
	}
}

// Validate checks if a sub-agent creation request is allowed.
func (p *SubAgentPolicy) Validate(parentID string, mode string, currentChildCount int) error {
	if parentID == "" {
		return nil
	}
	if currentChildCount >= p.MaxChildren {
		return fmt.Errorf("maximum %d sub-agents allowed", p.MaxChildren)
	}
	allowed := false
	for _, m := range p.AllowedModes {
		if m == mode {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("mode %q is not allowed for sub-agents; allowed: %v", mode, p.AllowedModes)
	}
	return nil
}

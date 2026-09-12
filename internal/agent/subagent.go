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

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/tools"
)

// SubAgentSpawnTool creates and starts a sub-agent.
type SubAgentSpawnTool struct {
	manager *AgentManager
}

// NewSubAgentSpawnTool creates a new subagent_spawn tool.
func NewSubAgentSpawnTool(m *AgentManager) *SubAgentSpawnTool {
	return &SubAgentSpawnTool{manager: m}
}

// DelegateSubAgentTool runs exactly one delegated sub-agent task synchronously.
type DelegateSubAgentTool struct {
	manager *AgentManager
	busy    atomic.Bool
}

// NewDelegateSubAgentTool creates a blocking delegate_subagent tool.
func NewDelegateSubAgentTool(m *AgentManager) *DelegateSubAgentTool {
	return &DelegateSubAgentTool{manager: m}
}

func (t *DelegateSubAgentTool) Name() string { return "delegate_subagent" }
func (t *DelegateSubAgentTool) Description() string {
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
	}
}
func (t *DelegateSubAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task": {"type": "string", "description": "A specific, bounded task description. Must include: (1) the exact goal or question, (2) relevant file paths or search patterns, (3) expected output format, (4) stop conditions. Example: 'Find all Go files in internal/serve/openaiapi/ that import net/http but do not call http.Error. Return file paths with line numbers.'"},
			"mode": {"type": "string", "enum": ["plan", "agent", "yolo", "os"], "description": "Sub-agent execution mode. Defaults to the parent agent's mode; if unavailable, falls back to 'yolo'. 'agent' is balanced, 'yolo' is unrestricted, and 'plan' is read-only analysis."},
			"work_dir": {"type": "string", "description": "Working directory for the sub-agent (defaults to current directory). Set explicitly if the task targets a different directory."},
			"tools": {"type": "array", "items": {"type": "string"}, "description": "Restrict sub-agent to specific tools (empty = all tools except nested sub-agent/delegate). Use to narrow scope, e.g. ['read', 'grep', 'find'] for investigation-only tasks."},
			"max_iterations": {"type": "integer", "default": 50, "description": "Maximum tool-call iterations. Lower for simple tasks (10-20), higher for complex exploration (50-100)."},
			"system_prompt_extra": {"type": "string", "description": "Additional context or constraints for the sub-agent. Use to pass domain knowledge, coding conventions, or specific instructions not in the task description."}
		},
		"required": ["task"]
	}`)
}

func (t *DelegateSubAgentTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	if t.manager == nil {
		return tools.ToolResult{}, fmt.Errorf("agent manager is not initialized")
	}
	if !t.busy.CompareAndSwap(false, true) {
		return tools.ToolResult{}, fmt.Errorf("a delegated sub-agent is already running")
	}
	defer t.busy.Store(false)

	started := time.Now()
	task, _ := params["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return tools.ToolResult{}, fmt.Errorf("task is required")
	}

	mode, _ := params["mode"].(string)
	if mode == "" {
		// Inherit parent agent's mode (yolo/agent/plan) instead of hardcoding "agent"
		if parentMode, ok := ParentModeFromContext(ctx); ok && parentMode != "" {
			mode = parentMode
		}
	}
	workDir, _ := params["work_dir"].(string)
	maxIter := 50
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
	parentEventCh, _ := EventChanFromContext(ctx)
	parentRunCtx, ok := ParentRunContextFromContext(ctx)
	if !ok || parentRunCtx == nil {
		parentRunCtx = ctx
	}
	policy := DefaultSubAgentPolicy()
	runCtx, cancel := context.WithTimeout(parentRunCtx, policy.TimeoutPerAgent)
	defer cancel()

	a, err := t.manager.Create(AgentOptions{
		ParentID: parentID,
		Mode:     mode,
		WorkDir:  workDir,
		Tools:    toolFilter,
		// A blocking delegate's caller is parked inside this tool call, so nobody
		// could ever answer a child question. Remove the tool instead of letting
		// the child block until its run is torn down with "no answer received".
		ExcludeTools:      []string{"question"},
		SystemPromptExtra: extra,
		MaxIterations:     maxIter,
	})
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
	data, _ := json.Marshal(result)
	return tools.NewTextToolResult(string(data)), nil
}

func (t *SubAgentSpawnTool) Name() string { return "subagent_spawn" }
func (t *SubAgentSpawnTool) Description() string {
	return "Create and start a bounded sub-agent task. Returns a handle for status/result polling."
}
func (t *SubAgentSpawnTool) PromptSnippet() string {
	return "Create a bounded sub-agent task for independent work"
}
func (t *SubAgentSpawnTool) PromptGuidelines() []string {
	return []string{
		"Use subagent_spawn only for independent subtasks with clear scope, expected output, and stop conditions",
		"Spawn multiple sub-agents in parallel for independent investigation or review work, then reconcile their results in the main agent",
		"Use subagent_status to poll results and verify important claims before acting on them",
		"Use subagent_destroy to clean up finished sub-agents",
		"When an expert team roster is present in the system prompt, dispatch members by their id via the member parameter instead of restating personas in the task",
	}
}

func (t *SubAgentSpawnTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task": {"type": "string", "description": "Focused task for the sub-agent, including scope, relevant paths/context, expected artifact, and stop conditions"},
			"member": {"type": "string", "description": "Member definition id from the bound expert team roster (see system prompt roster). Resolves the member persona and capability overrides."},
			"mode": {"type": "string", "enum": ["plan", "agent", "yolo", "os"], "description": "Sub-agent execution mode. Defaults to the parent agent's mode; if unavailable, falls back to 'yolo'."},
			"work_dir": {"type": "string", "description": "Working directory for the sub-agent (defaults to current)"},
			"tools": {"type": "array", "items": {"type": "string"}, "description": "Allowed tools (empty = all)"},
			"max_iterations": {"type": "integer", "default": 50, "description": "Maximum iterations"},
			"system_prompt_extra": {"type": "string", "description": "Extra context for the sub-agent"}
		},
		"required": ["task"]
	}`)
}

func (t *SubAgentSpawnTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	task, _ := params["task"].(string)
	if task == "" {
		return tools.ToolResult{}, fmt.Errorf("task is required")
	}

	// Resolve the optional expert-team member. The roster registry is
	// installed by the runtime assembly layer; without a binding the member
	// parameter is a tool error rather than a silent fallback.
	memberID, _ := params["member"].(string)
	memberID = strings.TrimSpace(memberID)
	var memberDef *MemberDef
	if memberID != "" {
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
	if memberDef != nil && memberDef.WorkDir != "" {
		if workDir != "" && workDir != memberDef.WorkDir {
			return tools.ToolResult{}, fmt.Errorf("member %q work_dir is fixed to %q", memberID, memberDef.WorkDir)
		}
		workDir = memberDef.WorkDir
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
		maxIter = 50
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

	// Extract parent's event channel from context (injected by executeTool)
	parentEventCh, _ := EventChanFromContext(ctx)

	// Apply per-agent timeout from default policy, tied to the parent run context.
	policy := DefaultSubAgentPolicy()
	parentRunCtx, ok := ParentRunContextFromContext(ctx)
	if !ok || parentRunCtx == nil {
		parentRunCtx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(parentRunCtx, policy.TimeoutPerAgent)

	a, err := t.manager.Create(AgentOptions{
		ParentID:          parentID,
		MemberID:          memberID,
		ExpertID:          t.manager.ExpertID,
		MemberDisplayName: memberDisplayName,
		MemberEmoji:       memberEmoji,
		MemberRole:        memberRole,
		Mode:              mode,
		WorkDir:           workDir,
		Tools:             toolFilter,
		SystemPromptExtra: extra,
		MaxIterations:     maxIter,
	})
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
		notifier := &memberNotifier{
			mailbox:     t.manager.Mailbox,
			memberID:    memberID,
			displayName: memberDisplayName,
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
	}()

	result := map[string]any{
		"handle":  string(a.ID()),
		"status":  "running",
		"timeout": policy.TimeoutPerAgent.String(),
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
		agentpkg.EventStatus,
		agentpkg.EventDone,
		agentpkg.EventError,
		agentpkg.EventRunFinished:
		return sendParentEvent(ctx, ch, ev)
	default:
		return false
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
	return "Send a follow-up message to a running sub-agent."
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

	// Apply per-agent timeout for follow-up messages too
	policy := DefaultSubAgentPolicy()
	parentRunCtx, ok := ParentRunContextFromContext(ctx)
	if !ok || parentRunCtx == nil {
		parentRunCtx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(parentRunCtx, policy.TimeoutPerAgent)
	t.manager.MarkRunning(a.ID())
	t.manager.SetCancel(a.ID(), cancel)

	// Extract parent's event channel for approval forwarding
	parentEventCh, _ := EventChanFromContext(ctx)

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
					t.manager.MarkError(a.ID(), normalizeSubAgentRunError(a.ID(), runCtx, e.Error))
				case agentpkg.TaskIncomplete:
					t.manager.MarkIncomplete(a.ID(), normalizeSubAgentRunError(a.ID(), runCtx, e.Error))
				case agentpkg.TaskCanceled:
					t.manager.MarkCanceled(a.ID(), normalizeSubAgentRunError(a.ID(), runCtx, e.Error))
				default:
					t.manager.MarkDone(a.ID(), lastAssistantResponse(a))
				}
			case agentpkg.EventDone:
				t.manager.MarkDone(a.ID(), lastAssistantResponse(a))
			case agentpkg.EventError:
				t.manager.MarkError(a.ID(), normalizeSubAgentRunError(a.ID(), runCtx, e.Error))
			}
		}
		if runCtx.Err() != nil {
			if st, ok := t.manager.Status(a.ID()); !ok || !isTerminalManagedState(st.State) {
				t.manager.MarkError(a.ID(), normalizeSubAgentRunError(a.ID(), runCtx, runCtx.Err()))
			}
		}
	}()

	return tools.NewTextToolResult(fmt.Sprintf(`{"handle":%q,"status":"message_sent"}`, handle)), nil
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

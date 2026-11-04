package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/tools"
)

// Bounded wait window for subagent_wait. These are tool ergonomics, not user
// configuration: they intentionally stay package constants and must not enter
// the settings schema.
const (
	subAgentWaitMinTimeoutMS     = 2500
	subAgentWaitMaxTimeoutMS     = 120000
	subAgentWaitDefaultTimeoutMS = 30000
	subAgentWaitPollInterval     = 100 * time.Millisecond
)

// SubAgentWaitTool blocks the calling (lead) agent until the session member
// mailbox reports activity or a bounded timeout elapses. It only merges the
// caller into already-scheduled member completions; like the mailbox itself it
// never wakes or starts a run. The result lists pending completions without
// their payloads — content is delivered by the mailbox drain at the next
// iteration boundary.
type SubAgentWaitTool struct {
	manager *AgentManager
}

// NewSubAgentWaitTool creates the subagent_wait tool.
func NewSubAgentWaitTool(m *AgentManager) tools.Tool {
	return &SubAgentWaitTool{manager: m}
}

func (t *SubAgentWaitTool) Name() string { return "subagent_wait" }
func (t *SubAgentWaitTool) Description() string {
	return "Wait for expert-team member activity: blocks until a spawned member or sub-agent completion lands in the session mailbox, or the bounded timeout elapses. Returns only a pending summary (member id, status); completion content is delivered automatically at the next iteration boundary."
}
func (t *SubAgentWaitTool) PromptSnippet() string {
	return "Wait for a member completion when the critical path is blocked"
}
func (t *SubAgentWaitTool) PromptGuidelines() []string {
	return []string{
		"Use subagent_wait only when the critical path is blocked on a member's result; do non-overlapping local work while members run",
		"For an external Router worker, pass the handle returned by subagent_spawn; the session mailbox still receives its completion",
		"Completion content is delivered automatically after the wait returns; never call subagent_wait again immediately after a wait (no reflexive waiting)",
	}
}

func (t *SubAgentWaitTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"handle": {"type": "string", "description": "Optional sub-agent handle. Use this for an external Router worker; waits for that child's terminal state instead of the team mailbox."},
			"timeout_ms": {"type": "integer", "description": "Maximum wait time in milliseconds (clamped to 2500-120000, default 30000)"}
		}
	}`)
}

func (t *SubAgentWaitTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	timeoutMS := resolveSubAgentWaitTimeoutMS(params)
	if handle, _ := params["handle"].(string); strings.TrimSpace(handle) != "" {
		return t.waitForHandle(ctx, strings.TrimSpace(handle), timeoutMS)
	}

	if t.manager == nil || t.manager.Mailbox == nil {
		return newSubAgentWaitResult("no member mailbox is bound to this session", false, nil), nil
	}
	mailbox := t.manager.Mailbox

	timedOut := false
	if !mailbox.HasPending() {
		var err error
		timedOut, err = mailbox.WaitForActivity(ctx, time.Duration(timeoutMS)*time.Millisecond)
		if err != nil {
			return tools.ToolResult{}, fmt.Errorf("subagent_wait: %w", err)
		}
	}
	message := "Wait completed."
	if timedOut {
		message = "Wait timed out."
	}
	return newSubAgentWaitResult(message, timedOut, mailbox.PendingSummary()), nil
}

// waitForHandle waits for one managed child through the existing manager status,
// which is the canonical terminal signal for each external worker. It is
// independent of the team mailbox so a handle wait works even when a completion
// was already drained as steering; this adds no second child state machine.
func (t *SubAgentWaitTool) waitForHandle(ctx context.Context, handle string, timeoutMS int) (tools.ToolResult, error) {
	if t.manager == nil {
		return tools.ToolResult{}, fmt.Errorf("sub-agent manager is not available")
	}
	id := agentpkg.AgentID(handle)
	status, ok := t.manager.Status(id)
	if !ok {
		if _, exists := t.manager.Get(id); !exists {
			return tools.ToolResult{}, fmt.Errorf("sub-agent %q not found", handle)
		}
		status = ManagedAgentStatus{ID: id, State: "unknown"}
	}
	if isTerminalManagedState(status.State) {
		return newSubAgentHandleWaitResult("Sub-agent finished.", false, status), nil
	}

	timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	defer timer.Stop()
	ticker := time.NewTicker(subAgentWaitPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return tools.ToolResult{}, fmt.Errorf("subagent_wait: %w", ctx.Err())
		case <-ticker.C:
			next, exists := t.manager.Status(id)
			if !exists {
				if _, agentExists := t.manager.Get(id); !agentExists {
					return tools.ToolResult{}, fmt.Errorf("sub-agent %q was destroyed while waiting", handle)
				}
				continue
			}
			status = next
			if isTerminalManagedState(status.State) {
				return newSubAgentHandleWaitResult("Sub-agent finished.", false, status), nil
			}
		case <-timer.C:
			return newSubAgentHandleWaitResult("Wait timed out.", true, status), nil
		}
	}
}

// resolveSubAgentWaitTimeoutMS applies the bounded wait window: default 30000,
// clamped to [2500, 120000] when timeout_ms is provided.
func resolveSubAgentWaitTimeoutMS(params map[string]any) int {
	timeoutMS := subAgentWaitDefaultTimeoutMS
	switch v := params["timeout_ms"].(type) {
	case float64:
		timeoutMS = int(v)
	case int:
		timeoutMS = v
	}
	if timeoutMS < subAgentWaitMinTimeoutMS {
		timeoutMS = subAgentWaitMinTimeoutMS
	}
	if timeoutMS > subAgentWaitMaxTimeoutMS {
		timeoutMS = subAgentWaitMaxTimeoutMS
	}
	return timeoutMS
}

// subAgentWaitPending is one summary entry of the wait result. It never
// carries the completion payload; content delivery belongs to the mailbox
// drain at the iteration boundary. A pending member question carries its
// question ID so the lead can answer it with subagent_answer even when it only
// inspects this projection.
type subAgentWaitPending struct {
	Member      string `json:"member"`
	Status      string `json:"status"`
	DisplayName string `json:"display_name,omitempty"`
	QuestionID  string `json:"question_id,omitempty"`
}

func newSubAgentWaitResult(message string, timedOut bool, pending []MemberCompletion) tools.ToolResult {
	payload := struct {
		Message  string                `json:"message"`
		TimedOut bool                  `json:"timed_out"`
		Pending  []subAgentWaitPending `json:"pending,omitempty"`
	}{Message: message, TimedOut: timedOut}
	for _, c := range pending {
		payload.Pending = append(payload.Pending, subAgentWaitPending{
			Member:      c.MemberID,
			Status:      c.Status,
			DisplayName: c.DisplayName,
			QuestionID:  c.QuestionID,
		})
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return tools.NewTextToolResult(fmt.Sprintf(`{"message":%q,"timed_out":%t}`, message, timedOut))
	}
	return tools.NewTextToolResult(string(data))
}

func newSubAgentHandleWaitResult(message string, timedOut bool, status ManagedAgentStatus) tools.ToolResult {
	state := status.State
	if state == "" {
		state = "unknown"
	}
	payload := struct {
		Message  string         `json:"message"`
		TimedOut bool           `json:"timed_out"`
		Handle   string         `json:"handle"`
		Status   string         `json:"status"`
		Result   string         `json:"result,omitempty"`
		Error    string         `json:"error,omitempty"`
		Worker   map[string]any `json:"worker,omitempty"`
	}{
		Message:  message,
		TimedOut: timedOut,
		Handle:   string(status.ID),
		Status:   state,
		Result:   status.Result,
		Error:    status.Error,
		Worker:   status.Metadata,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return tools.NewTextToolResult(fmt.Sprintf(`{"message":%q,"timed_out":%t,"handle":%q,"status":%q}`, message, timedOut, payload.Handle, state))
	}
	return tools.NewTextToolResult(string(data))
}
